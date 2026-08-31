package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/session"
	"github.com/asaqelee/agent_go/tool"
)

type streamScript struct {
	scriptedProvider
	deltas []string
}

func (s *streamScript) ChatStream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (llm.Response, error) {
	resp, err := s.Chat(ctx, req)
	if err != nil {
		return resp, err
	}
	if onDelta != nil {
		for _, d := range s.deltas {
			onDelta(llm.Delta{Content: d})
		}
	}
	return resp, nil
}

func TestConcurrentRunRejected(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	p := &scriptedProvider{
		onChat: func(req llm.Request, callIndex int) {
			if callIndex == 0 {
				close(started)
				<-release
			}
		},
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "a"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "b"}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 2}
	errCh := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), "one")
		errCh <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	_, err := a.Run(context.Background(), "two")
	if err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("err=%v", err)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSessionRoundTrip(t *testing.T) {
	st := session.NewMemory()
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}},
		},
	}
	a := &Agent{Provider: p, Sessions: st, SessionID: "s1", MaxTurns: 2}
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	p2 := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "again"}},
		},
	}
	a2 := &Agent{Provider: p2, Sessions: st, SessionID: "s1", MaxTurns: 2}
	if err := a2.RestoreSession(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(a2.History()) != 3 {
		t.Fatalf("restored len=%d", len(a2.History()))
	}
	if _, err := a2.Run(context.Background(), "next"); err != nil {
		t.Fatal(err)
	}
	if p2.lastReq.Messages[1].Content != "hi" {
		t.Fatalf("expected prior user turn in request, got %+v", p2.lastReq.Messages)
	}
}

func TestResetDeletesStoredSession(t *testing.T) {
	st := session.NewMemory()
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}},
		},
	}
	a := &Agent{Provider: p, Sessions: st, SessionID: "s1"}
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	got, _ := st.Load(context.Background(), "s1")
	if len(got) != 0 {
		t.Fatalf("want empty after reset, got %+v", got)
	}
}

func TestOnEventToolAndDone(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID: "c1", Type: "function",
						Function: llm.FunctionCall{Name: "calculator", Arguments: `{"expression":"2 + 3"}`},
					}},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "5"}},
		},
	}
	var kinds []EventKind
	a := &Agent{
		Provider: p,
		Tools:    []tool.Tool{tool.Calculator{}},
		MaxTurns: 4,
		OnEvent:  func(e Event) { kinds = append(kinds, e.Kind) },
	}
	got, err := a.Run(context.Background(), "2+3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "5" {
		t.Fatalf("got %q", got)
	}
	joined := fmtKinds(kinds)
	if !strings.Contains(joined, "tool_start") || !strings.Contains(joined, "tool_end") || !strings.Contains(joined, "done") {
		t.Fatalf("events=%s", joined)
	}
}

func TestStreamEmitsTokens(t *testing.T) {
	p := &streamScript{
		scriptedProvider: scriptedProvider{
			responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "Hello"}},
			},
		},
		deltas: []string{"Hel", "lo"},
	}
	var tokens []string
	a := &Agent{
		Provider: p,
		Stream:   true,
		OnEvent: func(e Event) {
			if e.Kind == EventToken {
				tokens = append(tokens, e.Token)
			}
		},
	}
	got, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello" {
		t.Fatalf("got %q", got)
	}
	if strings.Join(tokens, "") != "Hello" {
		t.Fatalf("tokens=%v", tokens)
	}
}

func fmtKinds(k []EventKind) string {
	s := make([]string, len(k))
	for i, x := range k {
		s[i] = string(x)
	}
	return strings.Join(s, ",")
}
