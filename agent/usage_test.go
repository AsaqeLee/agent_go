package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func TestRunRecordsLastAndSessionUsage(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "first"}, Usage: llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "second"}, Usage: llm.Usage{PromptTokens: 20, CompletionTokens: 6, TotalTokens: 26}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 3}
	if _, err := a.Run(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if got := a.LastUsage(); got.TotalTokens != 14 {
		t.Fatalf("last after first run: %+v", got)
	}
	if got := a.SessionUsage(); got.Calls != 1 || got.TotalTokens != 14 {
		t.Fatalf("session after first run: %+v", got)
	}

	if _, err := a.Run(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if got := a.LastUsage(); got.TotalTokens != 26 {
		t.Fatalf("last after second run: %+v", got)
	}
	got := a.SessionUsage()
	if got.Calls != 2 || got.PromptTokens != 30 || got.CompletionTokens != 10 || got.TotalTokens != 40 {
		t.Fatalf("session after second run: %+v", got)
	}
}

func TestRunUsageIncludesToolTurns(t *testing.T) {
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
				Usage: llm.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
			{
				Message: llm.Message{Role: llm.RoleAssistant, Content: "now"},
				Usage:   llm.Usage{PromptTokens: 9, CompletionTokens: 2, TotalTokens: 11},
			},
		},
	}
	a := &Agent{Provider: p, Tools: []tool.Tool{tool.GetTime{}}, MaxTurns: 4}
	if _, err := a.Run(context.Background(), "time?"); err != nil {
		t.Fatal(err)
	}
	if got := a.LastUsage(); got.Calls != 2 || got.TotalTokens != 19 {
		t.Fatalf("last=%+v", got)
	}
	if got := a.SessionUsage(); got.Calls != 2 || got.TotalTokens != 19 {
		t.Fatalf("session=%+v", got)
	}
}

func TestResetClearsSessionUsage(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}, Usage: llm.Usage{TotalTokens: 5, PromptTokens: 3, CompletionTokens: 2}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "fresh"}, Usage: llm.Usage{TotalTokens: 7, PromptTokens: 4, CompletionTokens: 3}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 3}
	if _, err := a.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if got := a.SessionUsage(); got != (llm.Usage{}) {
		t.Fatalf("session usage should clear on Reset, got %+v", got)
	}
	if _, err := a.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if got := a.SessionUsage(); got.TotalTokens != 7 || got.Calls != 1 {
		t.Fatalf("session after reset+run: %+v", got)
	}
}

func TestFailedRunDoesNotCommitUsage(t *testing.T) {
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
				Usage: llm.Usage{TotalTokens: 4, PromptTokens: 2, CompletionTokens: 2},
			},
		},
	}
	a := &Agent{Provider: p, Tools: []tool.Tool{tool.GetTime{}}, MaxTurns: 1}
	_, err := a.Run(context.Background(), "time?")
	if err == nil || !strings.Contains(err.Error(), "max turns") {
		t.Fatalf("err=%v", err)
	}
	if got := a.SessionUsage(); got != (llm.Usage{}) {
		t.Fatalf("failed run leaked usage: %+v", got)
	}
	if got := a.LastUsage(); got != (llm.Usage{}) {
		t.Fatalf("failed run leaked last usage: %+v", got)
	}
}

func TestUsageFormat(t *testing.T) {
	u := llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14, Calls: 2}
	got := u.Format()
	for _, want := range []string{"calls=2", "prompt=10", "completion=4", "total=14"} {
		if !strings.Contains(got, want) {
			t.Fatalf("format %q missing %q", got, want)
		}
	}
}
