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
	"sync"
	"time"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/obs"
	"github.com/asaqelee/agent_go/session"
	"github.com/asaqelee/agent_go/tool"
)

// DefaultMaxToolResultChars caps each tool result written into the conversation.
// Large outputs (logs, HTML) otherwise inflate multi-turn history and the context window.
const DefaultMaxToolResultChars = 4096

// Agent holds the model, tools, system prompt, loop limits, session history, and profile memory.
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
	// KeepRecentFullTurns: newest N user-turns keep full tool trajectories; older turns are folded.
	// 0 → DefaultKeepRecentFullTurns (1); negative → disable folding.
	KeepRecentFullTurns int
	// DisableLLMSummary skips LLM compression of trim drafts (extractive only).
	DisableLLMSummary bool
	// SummaryMinDraftRunes: LLM compression only if extractive draft is at least this large.
	// 0 → DefaultSummaryMinDraftRunes; negative → never LLM.
	SummaryMinDraftRunes int
	// Memory is structured durable fields (name/likes/notes). Survives history trim; injected each Chat.
	// If nil, profile tools no-op / ephemeral depending on tool wiring.
	Memory *Memory
	// Verbose logs each turn to Log (or stderr if Log is nil).
	Verbose bool
	// Log is the optional verbose sink; defaults to os.Stderr when Verbose is true.
	Log io.Writer

	// SessionID selects which transcript Sessions Load/Save. Empty → "default".
	SessionID string
	// Sessions persists history after a successful Run. Nil disables disk/memory restore.
	Sessions session.Store
	// OnEvent receives tokens, tool timings, and completion (CLI streaming / HTTP SSE).
	OnEvent func(Event)
	// Stream uses Provider.(llm.Streamer) when set and the provider implements it.
	Stream bool
	// ToolTimeout caps each Tool.Run. Zero means no extra timeout.
	ToolTimeout time.Duration
	// MaxToolConcurrency limits same-turn fan-out. Zero means unlimited (one goroutine per call).
	MaxToolConcurrency int
	// Approver gates tools whose Annotations.NeedsApproval is true. Nil allows.
	Approver tool.Approver
	// Redact mutates tool Content before it enters history / the model. Nil is identity.
	Redact func(name, content string) string
	// Tracer records run / chat / tool spans. Nil is a no-op.
	Tracer obs.Tracer

	// history is short-term memory across Run calls (system + user/assistant/tool turns).
	// Only updated when a Run finishes successfully.
	history []llm.Message

	// lastUsage is token accounting for the most recent successful Run (all Chat calls in that loop).
	lastUsage llm.Usage
	// sessionUsage accumulates successful Runs until Reset / ResetAll.
	sessionUsage llm.Usage
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

	ctx, endRun := a.startSpan(ctx, "agent.run")
	defer endRun()

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
	registry.Policy = tool.Policy{
		Timeout:  a.ToolTimeout,
		Approver: a.Approver,
		Redact:   a.Redact,
	}
	toolDefs := tool.Defs(a.Tools)
	var runUsage llm.Usage

	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("agent: %w", err)
		}
		a.log("── turn %d ──", turn)

		// Refresh structured profile each Chat so mid-turn memory_set/echo_note is visible next step.
		messages = upsertProfile(messages, a.Memory)

		resp, err := a.chat(ctx, messages, toolDefs)
		if err != nil {
			a.emit(Event{Kind: EventError, Err: err.Error()})
			a.spanErr(ctx, err)
			return "", fmt.Errorf("agent: llm chat: %w", err)
		}

		assistant := resp.Message
		messages = append(messages, assistant)
		runUsage = runUsage.Add(normalizeUsage(resp.Usage))

		// Case A: no tools → done; commit history, then optional fold + session trim.
		if len(assistant.ToolCalls) == 0 {
			a.commitHistory(ctx, messages)
			a.lastUsage = runUsage
			a.sessionUsage = a.sessionUsage.Add(runUsage)
			a.log("final: %s", assistant.Content)
			if runUsage.TotalTokens > 0 || runUsage.Calls > 0 {
				a.log("usage: last %s | session %s", runUsage.Format(), a.sessionUsage.Format())
			}
			out := strings.TrimSpace(assistant.Content)
			a.emit(Event{Kind: EventDone, Content: out, Usage: runUsage})
			return out, nil
		}

		// Case B: same-turn tool_calls fan out, then join. Results are
		// appended in the model's call order (not completion order).
		// Shared Memory is mutex-serialized inside the store.
		a.log("tool_calls: %d", len(assistant.ToolCalls))
		for _, tc := range assistant.ToolCalls {
			a.log("  → %s(%s)", tc.Function.Name, tc.Function.Arguments)
		}
		results := a.executeToolCalls(ctx, registry, assistant.ToolCalls)
		for i, tc := range assistant.ToolCalls {
			result, raw, truncated := results[i].content, results[i].raw, results[i].truncated
			if truncated {
				a.log("  ← %s %s (truncated %d→%d chars) %s", tc.Function.Name, results[i].code, utf8.RuneCountInString(raw), utf8.RuneCountInString(result), preview(result, 200))
			} else {
				a.log("  ← %s %s %s", tc.Function.Name, results[i].code, preview(result, 200))
			}
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				ToolCallID: tc.ID,
				Name:       tc.Function.Name,
				Content:    result,
			})
		}
	}

	err := fmt.Errorf("agent: exceeded max turns (%d)", maxTurns)
	a.emit(Event{Kind: EventError, Err: err.Error()})
	return "", err
}

type toolCallResult struct {
	content   string
	raw       string
	truncated bool
	code      string
}

func (a *Agent) chat(ctx context.Context, messages []llm.Message, toolDefs []llm.ToolDef) (llm.Response, error) {
	ctx, end := a.startSpan(ctx, "llm.chat")
	defer end()
	req := llm.Request{Messages: messages, Tools: toolDefs}
	if a.Stream {
		if s, ok := a.Provider.(llm.Streamer); ok {
			return s.ChatStream(ctx, req, func(d llm.Delta) {
				if d.Content != "" {
					a.emit(Event{Kind: EventToken, Token: d.Content})
				}
			})
		}
	}
	return a.Provider.Chat(ctx, req)
}

func (a *Agent) startSpan(ctx context.Context, name string) (context.Context, func()) {
	if a == nil || a.Tracer == nil {
		return ctx, func() {}
	}
	ctx, sp := a.Tracer.Start(ctx, name)
	if id := obs.IDFrom(ctx); id != "" {
		sp.Set("request_id", id)
	}
	if a.SessionID != "" {
		sp.Set("session_id", a.SessionID)
	}
	return ctx, sp.End
}

func (a *Agent) spanErr(ctx context.Context, err error) {
	if a == nil || a.Tracer == nil || err == nil {
		return
	}
	_, sp := a.Tracer.Start(ctx, "error")
	sp.RecordError(err)
	sp.End()
}

// executeToolCalls runs one assistant tool_calls batch concurrently and
// returns results aligned with the input slice (model call order).
func (a *Agent) executeToolCalls(ctx context.Context, registry *tool.Registry, calls []llm.ToolCall) []toolCallResult {
	out := make([]toolCallResult, len(calls))
	if len(calls) == 0 {
		return out
	}
	run := func(i int, tc llm.ToolCall) {
		tctx, end := a.startSpan(ctx, "tool."+tc.Function.Name)
		defer end()
		a.emit(Event{Kind: EventToolStart, Tool: tc.Function.Name, ToolID: tc.ID, Args: tc.Function.Arguments})
		res := registry.ExecuteDetail(tctx, tc.Function.Name, tc.Function.Arguments)
		content, truncated := a.capToolResult(res.Content)
		out[i] = toolCallResult{content: content, raw: res.Content, truncated: truncated, code: res.Code}
		ev := Event{
			Kind:     EventToolEnd,
			Tool:     tc.Function.Name,
			ToolID:   tc.ID,
			Content:  content,
			Code:     res.Code,
			Duration: res.Duration,
		}
		if res.Err != nil {
			ev.Err = res.Err.Error()
		}
		a.emit(ev)
	}
	if len(calls) == 1 {
		run(0, calls[0])
		return out
	}
	conc := a.MaxToolConcurrency
	if conc <= 0 || conc > len(calls) {
		conc = len(calls)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, conc)
	wg.Add(len(calls))
	for i, tc := range calls {
		i, tc := i, tc
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			run(i, tc)
		}()
	}
	wg.Wait()
	return out
}

// commitHistory stores a successful run, folds old tool trajectories, then trims by MaxHistoryMessages.
// Dropped turns become a sticky [conversation_summary] (extractive, optionally LLM-compressed).
func (a *Agent) commitHistory(ctx context.Context, messages []llm.Message) {
	// Drop profile blocks from committed history; they are re-injected live each Chat.
	messages = stripProfileMessages(messages)
	var folded int
	messages, folded = foldOldTurns(messages, a.KeepRecentFullTurns)
	if folded > 0 {
		a.log("history fold: collapsed tool trails in %d older user-turn(s)", folded)
	}
	a.history = messages
	if dropped := a.trimHistory(ctx); dropped > 0 {
		a.log("history trim: dropped %d oldest user-turn(s), summary updated, now %d messages (%s)",
			dropped, len(a.history), a.Stats().FormatStats())
	}
	a.persistSession(ctx)
}

func stripProfileMessages(msgs []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		if isProfileMessage(m) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Reset clears conversation history (not structured Memory). Next Run reseeds system prompt.
func (a *Agent) Reset() {
	a.history = nil
	a.lastUsage = llm.Usage{}
	a.sessionUsage = llm.Usage{}
	if a.Sessions != nil {
		_ = a.Sessions.Delete(context.Background(), a.sessionID())
	}
}

func (a *Agent) sessionID() string {
	id := strings.TrimSpace(a.SessionID)
	if id == "" {
		return "default"
	}
	return id
}

func (a *Agent) persistSession(ctx context.Context) {
	if a.Sessions == nil {
		return
	}
	if err := a.Sessions.Save(ctx, a.sessionID(), a.history); err != nil {
		a.log("session save: %v", err)
	}
}

// RestoreSession loads history from Sessions. No-op when Sessions is nil or empty.
func (a *Agent) RestoreSession(ctx context.Context) error {
	if a.Sessions == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	msgs, err := a.Sessions.Load(ctx, a.sessionID())
	if err != nil {
		return err
	}
	a.history = msgs
	return nil
}

// LastUsage returns token accounting for the most recent successful Run.
func (a *Agent) LastUsage() llm.Usage {
	return a.lastUsage
}

// SessionUsage returns token accounting accumulated since the last Reset.
func (a *Agent) SessionUsage() llm.Usage {
	return a.sessionUsage
}

func normalizeUsage(u llm.Usage) llm.Usage {
	if u.Calls <= 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0) {
		u.Calls = 1
	}
	if u.TotalTokens == 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	return u
}

// ResetMemory clears structured profile fields only.
func (a *Agent) ResetMemory() {
	if a.Memory != nil {
		a.Memory.Clear()
	}
}

// ResetAll clears chat history and structured Memory.
func (a *Agent) ResetAll() {
	a.Reset()
	a.ResetMemory()
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

// sessionMessages returns a working copy of history, seeding system on first use,
// and always refreshing the structured [user_profile] block from Memory.
func (a *Agent) sessionMessages() []llm.Message {
	var out []llm.Message
	if len(a.history) > 0 {
		out = make([]llm.Message, len(a.history))
		copy(out, a.history)
	} else {
		system := a.SystemPrompt
		if system == "" {
			system = defaultSystemPrompt()
		}
		out = []llm.Message{
			{Role: llm.RoleSystem, Content: system},
		}
	}
	return upsertProfile(out, a.Memory)
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
- Use tools when they help answer accurately (time, math, profile).
- Prefer calculator for arithmetic; do not guess multiplications.

Durable user profile (survives chat history trim):
- When the user states durable facts about themselves, YOU must extract fields and call tools — do not rely on chat memory alone.
- Prefer profile_update with structured fields: name (string), likes (array of strings), notes (array of strings). Only fill fields you are confident about; omit the rest.
- memory_set sets one field at a time (name|like|note). echo_note only appends a free-text note (does NOT parse name/likes).
- Never invent profile data. If unsure, ask or omit.
- Trust [user_profile] over older chat when they conflict.

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
