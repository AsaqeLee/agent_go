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
	// lastReq is the most recent Chat request (for multi-turn assertions).
	lastReq llm.Request
	// onChat optional hook before returning the next response.
	onChat func(req llm.Request, callIndex int)
}

func (s *scriptedProvider) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	s.lastReq = req
	if s.onChat != nil {
		s.onChat(req, s.i)
	}
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
	// Session keeps system + user + assistant.
	h := a.History()
	if len(h) != 3 {
		t.Fatalf("history len=%d want 3", len(h))
	}
	if h[0].Role != llm.RoleSystem || h[1].Role != llm.RoleUser || h[2].Role != llm.RoleAssistant {
		t.Fatalf("unexpected roles: %+v", h)
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
	// Failed run must not commit partial history.
	if len(a.History()) != 0 {
		t.Fatalf("history should stay empty after failed run, got %d msgs", len(a.History()))
	}
}

func TestRunNilProvider(t *testing.T) {
	a := &Agent{}
	_, err := a.Run(context.Background(), "x")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRunEmptyInput(t *testing.T) {
	a := &Agent{Provider: &scriptedProvider{}}
	_, err := a.Run(context.Background(), "  ")
	if err == nil {
		t.Fatal("expected empty input error")
	}
}

func TestMultiTurnHistory(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "好的，记住了。"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "你叫小明。"}},
		},
	}
	// Second Chat must see the first user+assistant exchange.
	p.onChat = func(req llm.Request, callIndex int) {
		if callIndex != 1 {
			return
		}
		var hasName, hasAck bool
		for _, m := range req.Messages {
			if m.Role == llm.RoleUser && strings.Contains(m.Content, "小明") {
				hasName = true
			}
			if m.Role == llm.RoleAssistant && strings.Contains(m.Content, "记住") {
				hasAck = true
			}
		}
		if !hasName || !hasAck {
			t.Errorf("second turn missing prior context: name=%v ack=%v msgs=%+v", hasName, hasAck, req.Messages)
		}
	}

	a := &Agent{Provider: p, MaxTurns: 3}
	if _, err := a.Run(context.Background(), "我叫小明"); err != nil {
		t.Fatal(err)
	}
	got, err := a.Run(context.Background(), "我叫什么？")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "小明") {
		t.Fatalf("got %q", got)
	}
	// system + u1 + a1 + u2 + a2
	if len(a.History()) != 5 {
		t.Fatalf("history len=%d want 5", len(a.History()))
	}
}

func TestResetClearsHistory(t *testing.T) {
	p := &scriptedProvider{
		responses: []llm.Response{
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "fresh"}},
		},
	}
	a := &Agent{Provider: p, MaxTurns: 3}
	if _, err := a.Run(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	a.Reset()
	if len(a.History()) != 0 {
		t.Fatal("history not empty after Reset")
	}

	// After reset, second request should not include "first".
	p.onChat = func(req llm.Request, callIndex int) {
		if callIndex != 1 {
			return
		}
		for _, m := range req.Messages {
			if m.Role == llm.RoleUser && m.Content == "first" {
				t.Error("old user message still present after Reset")
			}
		}
	}
	if _, err := a.Run(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
}

// longBlobTool returns a large string so we can test history capping.
type longBlobTool struct {
	n int
}

func (longBlobTool) Name() string        { return "long_blob" }
func (longBlobTool) Description() string { return "returns a long string" }
func (longBlobTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t longBlobTool) Run(string) (string, error) {
	return strings.Repeat("你", t.n), nil // multi-byte runes
}

func TestToolResultTruncatedInHistory(t *testing.T) {
	const limit = 100
	const blobRunes = 5000

	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{{
						ID:   "call_long",
						Type: "function",
						Function: llm.FunctionCall{
							Name:      "long_blob",
							Arguments: `{}`,
						},
					}},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
		},
	}

	// Second Chat must receive a capped tool message, not 5000 runes.
	p.onChat = func(req llm.Request, callIndex int) {
		if callIndex != 1 {
			return
		}
		var toolContent string
		for _, m := range req.Messages {
			if m.Role == llm.RoleTool {
				toolContent = m.Content
			}
		}
		if toolContent == "" {
			t.Fatal("missing tool message on second turn")
		}
		if strings.Count(toolContent, "你") >= blobRunes {
			t.Fatalf("tool content not truncated: %d runes of 你", strings.Count(toolContent, "你"))
		}
		if !strings.Contains(toolContent, "truncated") {
			t.Fatalf("expected truncation marker, got %q", toolContent[:min(80, len(toolContent))])
		}
		// Hard upper bound: should not be much larger than limit + marker.
		if n := len([]rune(toolContent)); n > limit+80 {
			t.Fatalf("tool content still too long: %d runes", n)
		}
	}

	a := &Agent{
		Provider:           p,
		Tools:              []tool.Tool{longBlobTool{n: blobRunes}},
		MaxTurns:           4,
		MaxToolResultChars: limit,
	}
	if _, err := a.Run(context.Background(), "blob"); err != nil {
		t.Fatal(err)
	}

	// History commit also uses the capped content.
	for _, m := range a.History() {
		if m.Role == llm.RoleTool {
			if strings.Count(m.Content, "你") >= blobRunes {
				t.Fatal("history still has full blob")
			}
			if !strings.Contains(m.Content, "truncated") {
				t.Fatal("history missing truncation marker")
			}
		}
	}
}

func TestToolResultUnlimitedWhenNegative(t *testing.T) {
	blob := strings.Repeat("a", 8000)
	a := &Agent{MaxToolResultChars: -1}
	out, truncated := a.capToolResult(blob)
	if truncated || out != blob {
		t.Fatalf("truncated=%v len=%d", truncated, len(out))
	}
}

func TestTruncateRunes(t *testing.T) {
	s := strings.Repeat("汉", 100)
	out, cut := truncateRunes(s, 20)
	if !cut {
		t.Fatal("expected cut")
	}
	if !strings.Contains(out, "truncated") {
		t.Fatalf("got %q", out)
	}
	// Must not split UTF-8: every non-ASCII rune in prefix should be 汉
	prefix := strings.Split(out, "\n...")[0]
	for _, r := range prefix {
		if r != '汉' {
			t.Fatalf("bad rune %q in %q", r, prefix)
		}
	}
	out2, cut2 := truncateRunes("short", 100)
	if cut2 || out2 != "short" {
		t.Fatalf("%q %v", out2, cut2)
	}
}
