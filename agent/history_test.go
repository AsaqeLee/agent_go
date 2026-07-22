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
	out, droppedMsgs, dropped := trimByUserTurns(msgs, 5)
	if dropped != 1 {
		t.Fatalf("dropped=%d want 1", dropped)
	}
	if len(out) != 5 {
		t.Fatalf("len=%d want 5: %v", len(out), contents(out))
	}
	if out[0].Role != llm.RoleSystem || out[1].Content != "u2" || out[3].Content != "u3" {
		t.Fatalf("unexpected: %v", contents(out))
	}
	if len(droppedMsgs) != 2 || droppedMsgs[0].Content != "u1" {
		t.Fatalf("droppedMsgs=%v", contents(droppedMsgs))
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
	out, _, dropped := trimByUserTurns(msgs, 5)
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
	out, dmsgs, d := trimByUserTurns(msgs, 0)
	if d != 0 || len(out) != 2 || dmsgs != nil {
		t.Fatalf("len=%d dropped=%d dmsgs=%v", len(out), d, dmsgs)
	}
}

func TestCommitHistoryTrimsWithSummary(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r1-about-q1"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r2"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "r3"}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 3, MaxHistoryMessages: 5}
	for _, q := range []string{"q1-name-xiaoming", "q2", "q3"} {
		if _, err := a.Run(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
	h := a.History()
	// May be slightly over if a single turn is huge; with short turns should be <=5
	// or <=6 with summary then second pass. Prefer: no raw q1 user message, but summary has it.
	for _, m := range h {
		if m.Role == llm.RoleUser && m.Content == "q1-name-xiaoming" {
			t.Fatal("oldest user turn still present as raw message")
		}
	}
	sum := extractSummaryBody(h)
	if sum == "" {
		t.Fatalf("expected sticky summary, history=%v", contents(h))
	}
	if !strings.Contains(sum, "q1-name-xiaoming") && !strings.Contains(sum, "r1-about-q1") {
		t.Fatalf("summary missing dropped facts: %q", sum)
	}
	st := a.Stats()
	if !st.HasSummary {
		t.Fatalf("stats HasSummary=false: %s", st.FormatStats())
	}
	if !strings.Contains(st.FormatStats(), "summary=yes") {
		t.Fatalf("format=%q", st.FormatStats())
	}
}

func TestSummarySurvivesFurtherTrim(t *testing.T) {
	// Simulate history: system + summary + two short turns, then force trim of one turn.
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "main-sys"},
		{Role: llm.RoleSystem, Content: conversationSummaryMarker + "\n- User: I am Xiaoming\n"},
		{Role: llm.RoleUser, Content: "old-q"},
		{Role: llm.RoleAssistant, Content: "old-a"},
		{Role: llm.RoleUser, Content: "new-q"},
		{Role: llm.RoleAssistant, Content: "new-a"},
	}
	// max 4: drop old-q turn (2 msgs) => system, summary, new-q, new-a = 4, then rebuild summary
	a := &Agent{MaxHistoryMessages: 4, history: msgs}
	n := a.trimHistory()
	if n < 1 {
		t.Fatalf("expected drop, n=%d hist=%v", n, contents(a.history))
	}
	body := extractSummaryBody(a.history)
	if !strings.Contains(body, "Xiaoming") {
		t.Fatalf("prior summary fact lost: %q", body)
	}
	if !strings.Contains(body, "old-q") && !strings.Contains(body, "old-a") {
		t.Fatalf("newly dropped turn not folded: %q", body)
	}
	// raw old-q should be gone
	for _, m := range a.history {
		if m.Role == llm.RoleUser && m.Content == "old-q" {
			t.Fatal("old-q still present")
		}
	}
}

func TestBuildConversationSummaryIncludesTools(t *testing.T) {
	dropped := []llm.Message{
		{Role: llm.RoleUser, Content: "我是小明"},
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{
				ID: "c1", Type: "function",
				Function: llm.FunctionCall{Name: "echo_note", Arguments: "{}"},
			}},
		},
		{Role: llm.RoleTool, Name: "echo_note", ToolCallID: "c1", Content: "noted: 用户叫小明"},
		{Role: llm.RoleAssistant, Content: "已记住小明"},
	}
	s := buildConversationSummary("", dropped)
	for _, want := range []string{"我是小明", "echo_note", "用户叫小明", "已记住小明"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q: %s", want, s)
		}
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
