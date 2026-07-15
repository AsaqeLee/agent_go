package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

// scriptedProvider returns canned responses in order (no network).
type scriptedProvider struct {
	responses []llm.Response
	i         int
}

func (s *scriptedProvider) Chat(_ context.Context, _ llm.Request) (llm.Response, error) {
	if s.i >= len(s.responses) {
		return llm.Response{}, context.Canceled
	}
	r := s.responses[s.i]
	s.i++
	return r, nil
}

func TestRunPlainText(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}},
		},
	}
	a := &Agent{Provider: p, Tools: nil, MaxTurns: 3}
	got, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestRunWithToolCall(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:   "call_1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "calculator",
								Arguments: `{"expression":"2 + 3"}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "结果是 5"}},
		},
	}
	a := &Agent{
		Provider: p,
		Tools:    []tool.Tool{tool.Calculator{}},
		MaxTurns: 4,
	}
	got, err := a.Run(context.Background(), "2+3=?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "5") {
		t.Fatalf("got %q, want answer containing 5", got)
	}
	if p.i != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", p.i)
	}
}

func TestRunMaxTurns(t *testing.T) {
	// Always requests a tool → hits MaxTurns.
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID: "c1", Type: "function",
						Function: llm.FunctionCall{Name: "get_time", Arguments: `{}`},
					}},
				},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID: "c2", Type: "function",
						Function: llm.FunctionCall{Name: "get_time", Arguments: `{}`},
					}},
				},
			},
		},
	}
	a := &Agent{
		Provider: p,
		Tools:    []tool.Tool{tool.GetTime{}},
		MaxTurns: 2,
	}
	_, err := a.Run(context.Background(), "time?")
	if err == nil || !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("expected max turns error, got %v", err)
	}
}

func TestRunNilProvider(t *testing.T) {
	a := &Agent{}
	_, err := a.Run(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}
