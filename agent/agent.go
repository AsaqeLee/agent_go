// Package agent implements the core agent loop:
//
//	user message → LLM → if tool_calls, execute tools → append results → LLM again
//	until the model returns plain text or MaxTurns is hit.
//
// That loop is the essential difference between a one-shot chat completion and an agent.
//
// Conversation history is kept on the Agent across Run calls (multi-turn). Use Reset
// to start a fresh session.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

// DefaultMaxToolResultChars caps each tool result written into the conversation.
// Large outputs (logs, HTML) otherwise inflate multi-turn history and the context window.
const DefaultMaxToolResultChars = 4096

// Agent holds the model, tools, system prompt, loop limits, and session history.
type Agent struct {
	Provider     llm.Provider
	Tools        []tool.Tool
	SystemPrompt string
	MaxTurns     int // default 8; prevents runaway loops
	// MaxToolResultChars limits each tool result stored in history (and sent back to the model).
	// 0 means DefaultMaxToolResultChars; negative means no limit.
	MaxToolResultChars int
	// MaxHistoryMessages caps session length after each successful Run.
	// 0 means unlimited. Trimming drops oldest complete user-turns (never splits tool_calls from tool results).
	MaxHistoryMessages int
	// Verbose logs each turn to Log (or stderr if Log is nil).
	Verbose bool
	// Log is the optional verbose sink; defaults to os.Stderr when Verbose is true.
	Log io.Writer

	// history is short-term memory across Run calls (system + user/assistant/tool turns).
	// Only updated when a Run finishes successfully.
	history []llm.Message
}

// Run executes the agent loop for one user input and returns the final text answer.
// Prior successful turns stay in the agent; this call appends the new user message.
func (a *Agent) Run(ctx context.Context, userInput string) (string, error) {
	if a.Provider == nil {
		return "", fmt.Errorf("agent: provider is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(userInput) == "" {
		return "", fmt.Errorf("agent: empty input")
	}

	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}

	// Work on a copy so failed runs do not corrupt the session.
	messages := a.sessionMessages()
	messages = append(messages, llm.Message{
		Role:    llm.RoleUser,
		Content: userInput,
	})

	registry := tool.NewRegistry(a.Tools)
	toolDefs := tool.Defs(a.Tools)

	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("agent: %w", err)
		}
		a.log("── turn %d ──", turn)

		resp, err := a.Provider.Chat(ctx, llm.Request{
			Messages: messages,
			Tools:    toolDefs,
		})
		if err != nil {
			return "", fmt.Errorf("agent: llm chat: %w", err)
		}

		assistant := resp.Message
		messages = append(messages, assistant)

		// Case A: no tools → done; commit history, then optional session trim.
		if len(assistant.ToolCalls) == 0 {
			a.commitHistory(messages)
			a.log("final: %s", assistant.Content)
			return strings.TrimSpace(assistant.Content), nil
		}

		// Case B: execute tools and feed results back.
		a.log("tool_calls: %d", len(assistant.ToolCalls))
		for _, tc := range assistant.ToolCalls {
			a.log("  → %s(%s)", tc.Function.Name, tc.Function.Arguments)
			raw := registry.Execute(tc.Function.Name, tc.Function.Arguments)
			result, truncated := a.capToolResult(raw)
			if truncated {
				a.log("  ← (truncated %d→%d chars) %s", utf8.RuneCountInString(raw), utf8.RuneCountInString(result), preview(result, 200))
			} else {
				a.log("  ← %s", preview(result, 200))
			}

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}

	return "", fmt.Errorf("agent: exceeded max turns (%d)", maxTurns)
}

// commitHistory stores a successful run and trims old user-turns if over MaxHistoryMessages.
func (a *Agent) commitHistory(messages []llm.Message) {
	a.history = messages
	if dropped := a.trimHistory(); dropped > 0 {
		a.log("history trim: dropped %d oldest user-turn(s), now %d messages (%s)",
			dropped, len(a.history), a.Stats().FormatStats())
	}
}

// Reset clears conversation history. The next Run starts a new session (new system prompt).
func (a *Agent) Reset() {
	a.history = nil
}

// History returns a copy of the current session messages (for debugging / learning).
func (a *Agent) History() []llm.Message {
	if len(a.history) == 0 {
		return nil
	}
	out := make([]llm.Message, len(a.history))
	copy(out, a.history)
	return out
}

// sessionMessages returns a working copy of history, seeding system on first use.
func (a *Agent) sessionMessages() []llm.Message {
	if len(a.history) > 0 {
		out := make([]llm.Message, len(a.history))
		copy(out, a.history)
		return out
	}
	system := a.SystemPrompt
	if system == "" {
		system = defaultSystemPrompt()
	}
	return []llm.Message{
		{Role: llm.RoleSystem, Content: system},
	}
}

// capToolResult limits tool output size before it enters the conversation.
// limit <= 0 uses DefaultMaxToolResultChars; limit < 0 on the field means unlimited.
func (a *Agent) capToolResult(s string) (out string, truncated bool) {
	limit := a.MaxToolResultChars
	if limit < 0 {
		return s, false
	}
	if limit == 0 {
		limit = DefaultMaxToolResultChars
	}
	return truncateRunes(s, limit)
}

// truncateRunes keeps at most maxRunes runes and appends a clear marker when cut.
func truncateRunes(s string, maxRunes int) (string, bool) {
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s, false
	}
	// Leave room for the suffix so total stays near maxRunes when possible.
	suffix := fmt.Sprintf("\n...[truncated, original %d chars]", utf8.RuneCountInString(s))
	keep := maxRunes
	if keep > 32 {
		// Prefer keeping content under maxRunes including a short notice.
		// If suffix is longer than budget, still cut hard at maxRunes then append.
		if utf8.RuneCountInString(suffix) < keep {
			keep = maxRunes - utf8.RuneCountInString(suffix)
		}
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= keep {
			break
		}
		b.WriteRune(r)
		n++
	}
	b.WriteString(suffix)
	return b.String(), true
}

func defaultSystemPrompt() string {
	return strings.TrimSpace(`
You are a helpful assistant with tools.
- Use tools when they help answer accurately (time, math, notes).
- Prefer calculator for arithmetic; do not guess multiplications.
- After tools return, give a concise final answer to the user.
- Reply in the same language the user uses.
`)
}

func (a *Agent) log(format string, args ...any) {
	if !a.Verbose {
		return
	}
	w := a.Log
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "[agent] "+format+"\n", args...)
}

// preview is for verbose logs only (does not affect model context).
func preview(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if maxRunes <= 0 || len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
