package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func TestHandoffRunsSpecialistWithFilteredTools(t *testing.T) {
	var childToolNames []string
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID: "h1", Type: "function",
						Function: llm.FunctionCall{
							Name:      "handoff",
							Arguments: `{"agent":"docs","goal":"年假几天"}`,
						},
					}},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "from-docs"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "年假10天"}},
		},
		onChat: func(req llm.Request, callIndex int) {
			if callIndex != 1 {
				return
			}
			if !strings.Contains(req.Messages[0].Content, "knowledge") {
				t.Errorf("specialist prompt=%q", req.Messages[0].Content)
			}
			for _, d := range req.Tools {
				childToolNames = append(childToolNames, d.Function.Name)
			}
		},
	}
	parent := &Agent{
		Provider: p,
		Tools:    []tool.Tool{tool.Calculator{}, tool.GetTime{}},
		MaxTurns: 4,
	}
	roster := NewRoster(parent, []Specialist{{
		Name:        "docs",
		Description: "kb",
		Prompt:      "You only use the knowledge base.",
		Tools:       []string{"calculator"},
	}})
	parent.Tools = append(parent.Tools, roster.Tool())
	got, err := parent.Run(context.Background(), "问知识库")
	if err != nil {
		t.Fatal(err)
	}
	if got != "年假10天" {
		t.Fatalf("got %q", got)
	}
	joined := strings.Join(childToolNames, ",")
	if !strings.Contains(joined, "calculator") {
		t.Fatalf("child tools=%s", joined)
	}
	if strings.Contains(joined, "handoff") || strings.Contains(joined, "get_time") {
		t.Fatalf("child tools should be filtered: %s", joined)
	}
}

func TestHandoffUnknownAgent(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID: "h1", Type: "function",
						Function: llm.FunctionCall{Name: "handoff", Arguments: `{"agent":"nope","goal":"x"}`},
					}},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "unknown"}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 4}
	a.Tools = []tool.Tool{NewRoster(a, []Specialist{{Name: "docs"}}).Tool()}
	got, err := a.Run(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if got != "unknown" {
		t.Fatalf("%q", got)
	}
	found := false
	for _, m := range a.History() {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "unknown agent") {
			found = true
		}
	}
	if !found {
		t.Fatalf("history=%+v", a.History())
	}
}
