package agent

import (
	"testing"

	"github.com/asaqelee/agent_go/llm"
)

func TestFoldOldTurnsCollapsesToolTrail(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		// old turn with tools
		{Role: llm.RoleUser, Content: "old"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.FunctionCall{Name: "calculator", Arguments: "{}"}}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "calculator", Content: "42"},
		{Role: llm.RoleAssistant, Content: "answer was 42"},
		// recent full turn
		{Role: llm.RoleUser, Content: "new"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c2", Type: "function", Function: llm.FunctionCall{Name: "get_time", Arguments: "{}"}}}},
		{Role: llm.RoleTool, ToolCallID: "c2", Name: "get_time", Content: "now"},
		{Role: llm.RoleAssistant, Content: "it is now"},
	}
	out, folded := foldOldTurns(msgs, 1)
	if folded != 1 {
		t.Fatalf("folded=%d", folded)
	}
	if err := assertToolPairsIntact(out); err != nil {
		t.Fatal(err)
	}
	// old turn should not contain tool messages
	for _, m := range out {
		if m.ToolCallID == "c1" {
			t.Fatal("old tool trail not folded")
		}
	}
	// recent turn still has tools
	var hasC2 bool
	for _, m := range out {
		if m.ToolCallID == "c2" {
			hasC2 = true
		}
	}
	if !hasC2 {
		t.Fatal("recent tool trail should remain")
	}
}

func TestFoldDisabled(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u"},
		{Role: llm.RoleAssistant, Content: "a"},
	}
	out, n := foldOldTurns(msgs, -1)
	if n != 0 || len(out) != 2 {
		t.Fatalf("n=%d len=%d", n, len(out))
	}
}
