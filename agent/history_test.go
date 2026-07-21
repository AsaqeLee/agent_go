package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/asaqelee/agent_go/llm"
)

func TestTrimByUserTurnsDropsOldest(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleAssistant, Content: "a1"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleAssistant, Content: "a2"},
		{Role: llm.RoleUser, Content: "u3"},
		{Role: llm.RoleAssistant, Content: "a3"},
	}
	out, dropped := trimByUserTurns(msgs, 5)
	if dropped != 1 {
		t.Fatalf("dropped=%d want 1", dropped)
	}
	if len(out) != 5 {
		t.Fatalf("len=%d want 5: %v", len(out), contents(out))
	}
	if out[0].Role != llm.RoleSystem || out[1].Content != "u2" || out[3].Content != "u3" {
		t.Fatalf("unexpected: %v", contents(out))
	}
}

func TestTrimByUserTurnsKeepsToolPairs(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "old"},
		{
			Role:    llm.RoleAssistant,
			Content: "call",
			ToolCalls: []llm.ToolCall{{
				ID: "c1", Type: "function",
				Function: llm.FunctionCall{Name: "calculator", Arguments: "{}"},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "c1", Name: "calculator", Content: "1"},
		{Role: llm.RoleAssistant, Content: "done-old"},
		{Role: llm.RoleUser, Content: "new"},
		{
			Role:    llm.RoleAssistant,
			Content: "call2",
			ToolCalls: []llm.ToolCall{{
				ID: "c2", Type: "function",
				Function: llm.FunctionCall{Name: "calculator", Arguments: "{}"},
			}},
		},
		{Role: llm.RoleTool, ToolCallID: "c2", Name: "calculator", Content: "2"},
		{Role: llm.RoleAssistant, Content: "done-new"},
	}
	out, dropped := trimByUserTurns(msgs, 5)
	if dropped != 1 {
		t.Fatalf("dropped=%d want 1", dropped)
	}
	if err := assertToolPairsIntact(out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out {
		if m.Content == "old" || m.Content == "done-old" || m.ToolCallID == "c1" {
			t.Fatalf("old turn not fully dropped: %v", contents(out))
		}
	}
	if out[1].Content != "new" {
		t.Fatalf("expected new turn kept, got %v", contents(out))
	}
}

func TestTrimUnlimited(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "s"},
		{Role: llm.RoleUser, Content: "u"},
	}
	out, d := trimByUserTurns(msgs, 0)
	if d != 0 || len(out) != 2 {
		t.Fatalf("len=%d dropped=%d", len(out), d)
	}
}

func TestCommitHistoryTrims(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r1"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r2"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r3"}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 3, MaxHistoryMessages: 5}
	for _, q := range []string{"q1", "q2", "q3"} {
		if _, err := a.Run(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	h := a.History()
	if len(h) > 5 {
		t.Fatalf("len=%d want <=5 %v", len(h), contents(h))
	}
	for _, m := range h {
		if m.Role == llm.RoleUser && m.Content == "q1" {
			t.Fatal("oldest user turn still present")
		}
	}
	st := a.Stats()
	if st.Messages != len(h) || st.UserTurns < 1 {
		t.Fatalf("stats=%+v", st)
	}
	if !strings.Contains(st.FormatStats(), "messages=") {
		t.Fatalf("format=%q", st.FormatStats())
	}
}

func TestStatsEmpty(t *testing.T) {
	a := &Agent{}
	st := a.Stats()
	if st.Messages != 0 {
		t.Fatalf("%+v", st)
	}
	if !strings.Contains(st.FormatStats(), "unlimited") {
		t.Fatalf("format=%q", st.FormatStats())
	}
}

func contents(msgs []llm.Message) []string {
	var s []string
	for _, m := range msgs {
		s = append(s, string(m.Role)+":"+m.Content)
	}
	return s
}

func assertToolPairsIntact(msgs []llm.Message) error {
	pending := map[string]bool{}
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			if len(pending) > 0 {
				return fmt.Errorf("unresolved tool_calls before user: %v", pendingKeys(pending))
			}
		case llm.RoleAssistant:
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
		case llm.RoleTool:
			if !pending[m.ToolCallID] {
				return fmt.Errorf("orphan tool id %q", m.ToolCallID)
			}
			delete(pending, m.ToolCallID)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("unresolved tool_calls at end: %v", pendingKeys(pending))
	}
	return nil
}

func pendingKeys(m map[string]bool) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
