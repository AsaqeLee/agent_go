package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

// handshakeTool blocks after announcing start until release is closed.
// Two of these in one tool_calls batch only both start if the runtime fans out.
type handshakeTool struct {
	name    string
	started chan<- struct{}
	release <-chan struct{}
}

func (h handshakeTool) Name() string        { return h.name }
func (h handshakeTool) Description() string { return "test handshake tool" }
func (h handshakeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (h handshakeTool) Run(ctx context.Context, _ string) (string, error) {
	select {
	case h.started <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case <-h.release:
		return "ok:" + h.name, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func TestSameTurnToolCallsRunInParallel(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})

	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:   "c1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "hand_a",
								Arguments: `{}`,
							},
						},
						{
							ID:   "c2",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "hand_b",
								Arguments: `{}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "both done"}},
		},
	}

	a := &Agent{
		Provider: p,
		Tools: []tool.Tool{
			handshakeTool{name: "hand_a", started: started, release: release},
			handshakeTool{name: "hand_b", started: started, release: release},
		},
		MaxTurns: 4,
	}

	errc := make(chan error, 1)
	go func() {
		_, err := a.Run(context.Background(), "run both")
		errc <- err
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("same-turn tool_calls did not overlap (second tool never started while the first was blocked)")
		}
	}
	close(release)

	select {
	case err := <-errc:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not finish after both tools were released")
	}
}

type delayTool struct {
	name string
	d    time.Duration
	out  string
}

func (d delayTool) Name() string        { return d.name }
func (d delayTool) Description() string { return "test delay tool" }
func (d delayTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (d delayTool) Run(context.Context, string) (string, error) {
	time.Sleep(d.d)
	return d.out, nil
}

func TestSameTurnToolResultsPreserveCallOrder(t *testing.T) {
	var toolOrder []string
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:       "slow",
							Type:     "function",
							Function: llm.FunctionCall{Name: "slow_tool", Arguments: `{}`},
						},
						{
							ID:       "fast",
							Type:     "function",
							Function: llm.FunctionCall{Name: "fast_tool", Arguments: `{}`},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
		},
		onChat: func(req llm.Request, callIndex int) {
			if callIndex != 1 {
				return
			}
			for _, m := range req.Messages {
				if m.Role == llm.RoleTool {
					toolOrder = append(toolOrder, m.ToolCallID+":"+m.Content)
				}
			}
		},
	}

	a := &Agent{
		Provider: p,
		Tools: []tool.Tool{
			delayTool{name: "slow_tool", d: 40 * time.Millisecond, out: "SLOW"},
			delayTool{name: "fast_tool", d: time.Millisecond, out: "FAST"},
		},
		MaxTurns: 4,
	}
	if _, err := a.Run(context.Background(), "both"); err != nil {
		t.Fatal(err)
	}
	want := []string{"slow:SLOW", "fast:FAST"}
	if strings.Join(toolOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("tool results order=%v want %v (call order, not completion order)", toolOrder, want)
	}
}

func TestSameTurnMemoryWritesAllLand(t *testing.T) {
	mem := NewMemory()
	p := &scriptedProvider{
		responses: []llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					ToolCalls: []llm.ToolCall{
						{
							ID:   "n1",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "echo_note",
								Arguments: `{"text":"alpha"}`,
							},
						},
						{
							ID:   "n2",
							Type: "function",
							Function: llm.FunctionCall{
								Name:      "echo_note",
								Arguments: `{"text":"beta"}`,
							},
						},
					},
				},
			},
			{Message: llm.Message{Role: llm.RoleAssistant, Content: "saved"}},
		},
	}
	a := &Agent{
		Provider: p,
		Memory:   mem,
		Tools:    tool.DefaultTools(mem, ""),
		MaxTurns: 4,
	}
	if _, err := a.Run(context.Background(), "remember both"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(mem.Notes, ",")
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Fatalf("parallel memory writes dropped a note: %v", mem.Notes)
	}
}
