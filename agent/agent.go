// Package agent implements the core agent loop:
//
//	user message → LLM → if tool_calls, execute tools → append results → LLM again
//	until the model returns plain text or MaxTurns is hit.
//
// That loop is the essential difference between a one-shot chat completion and an agent.
package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

// Agent holds the model, tools, system prompt, and loop limits.
type Agent struct {
	Provider     llm.Provider
	Tools        []tool.Tool
	SystemPrompt string
	MaxTurns     int // default 8; prevents runaway loops
	// Verbose logs each turn to Log (or stderr if Log is nil).
	Verbose bool
	// Log is the optional verbose sink; defaults to os.Stderr when Verbose is true.
	Log io.Writer
}

// Run executes the full agent loop for one user input and returns the final text answer.
func (a *Agent) Run(ctx context.Context, userInput string) (string, error) {
	if a.Provider == nil {
		return "", fmt.Errorf("agent: provider is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 8
	}

	system := a.SystemPrompt
	if system == "" {
		system = defaultSystemPrompt()
	}

	// messages is growing short-term memory for this run.
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: system},
		{Role: llm.RoleUser, Content: userInput},
	}

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

		// Case A: no tools → done.
		if len(assistant.ToolCalls) == 0 {
			a.log("final: %s", assistant.Content)
			return strings.TrimSpace(assistant.Content), nil
		}

		// Case B: execute tools and feed results back.
		a.log("tool_calls: %d", len(assistant.ToolCalls))
		for _, tc := range assistant.ToolCalls {
			a.log("  → %s(%s)", tc.Function.Name, tc.Function.Arguments)
			result := registry.Execute(tc.Function.Name, tc.Function.Arguments)
			a.log("  ← %s", truncate(result, 200))

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

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
