package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
)

// conversationSummaryMarker prefixes the sticky system message that holds
// compressed facts from trimmed user-turns.
const conversationSummaryMarker = "[conversation_summary]"

// DefaultMaxSummaryRunes caps the sticky summary so it cannot grow without bound.
const DefaultMaxSummaryRunes = 1200

// HistoryStats summarizes session size for observability (/history, debugging).
type HistoryStats struct {
	Messages int
	Bytes    int
	Runes    int
	// ByRole counts messages per role (system/user/assistant/tool).
	ByRole map[string]int
	// UserTurns is the number of user-started exchanges (each starts at RoleUser).
	UserTurns int
	// HasSummary is true when a sticky [conversation_summary] system message is present.
	HasSummary bool
	// MaxHistoryMessages is the configured cap (0 = unlimited).
	MaxHistoryMessages int
	// OverLimit is true when Messages > MaxHistoryMessages and cap is active.
	OverLimit bool
}

// Stats returns size metrics for the current session history.
func (a *Agent) Stats() HistoryStats {
	st := HistoryStats{
		ByRole:             map[string]int{},
		MaxHistoryMessages: a.MaxHistoryMessages,
	}
	for _, m := range a.history {
		st.Messages++
		st.Bytes += len(m.Content)
		st.Runes += utf8.RuneCountInString(m.Content)
		for _, tc := range m.ToolCalls {
			st.Bytes += len(tc.Function.Name) + len(tc.Function.Arguments) + len(tc.ID)
			st.Runes += utf8.RuneCountInString(tc.Function.Name) +
				utf8.RuneCountInString(tc.Function.Arguments) +
				utf8.RuneCountInString(tc.ID)
		}
		role := string(m.Role)
		if role == "" {
			role = "?"
		}
		st.ByRole[role]++
		if m.Role == llm.RoleUser {
			st.UserTurns++
		}
		if isSummaryMessage(m) {
			st.HasSummary = true
		}
	}
	if a.MaxHistoryMessages > 0 && st.Messages > a.MaxHistoryMessages {
		st.OverLimit = true
	}
	return st
}

// FormatStats is a one-line summary for CLI display.
func (s HistoryStats) FormatStats() string {
	var b strings.Builder
	fmt.Fprintf(&b, "messages=%d bytes=%d runes=%d user_turns=%d",
		s.Messages, s.Bytes, s.Runes, s.UserTurns)
	if s.HasSummary {
		b.WriteString(" summary=yes")
	}
	if s.MaxHistoryMessages > 0 {
		fmt.Fprintf(&b, " max_messages=%d", s.MaxHistoryMessages)
		if s.OverLimit {
			b.WriteString(" over_limit=true")
		}
	} else {
		b.WriteString(" max_messages=unlimited")
	}
	if len(s.ByRole) > 0 {
		b.WriteString(" roles={")
		order := []string{"system", "user", "assistant", "tool"}
		first := true
		seen := map[string]bool{}
		for _, r := range order {
			if n, ok := s.ByRole[r]; ok {
				if !first {
					b.WriteString(" ")
				}
				fmt.Fprintf(&b, "%s:%d", r, n)
				first = false
				seen[r] = true
			}
		}
		for r, n := range s.ByRole {
			if seen[r] {
				continue
			}
			if !first {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s:%d", r, n)
			first = false
		}
		b.WriteByte('}')
	}
	return b.String()
}

// trimHistory drops the oldest complete user-turns until len(history) <= MaxHistoryMessages,
// then folds dropped content into a sticky [conversation_summary] system message so key
// facts (name, preferences, tool notes) survive context pressure.
//
// Returns how many user-turns were dropped. MaxHistoryMessages <= 0 means no trim.
func (a *Agent) trimHistory() int {
	if a.MaxHistoryMessages <= 0 || len(a.history) <= a.MaxHistoryMessages {
		return 0
	}

	prevBody := extractSummaryBody(a.history)
	trimmed, droppedMsgs, droppedTurns := trimByUserTurns(a.history, a.MaxHistoryMessages)
	if droppedTurns == 0 {
		a.history = trimmed
		return 0
	}

	summary := buildConversationSummary(prevBody, droppedMsgs)
	a.history = upsertSummary(trimmed, summary)

	// Inserting a new summary may push us 1 over the cap; drop more turns if needed.
	// Existing summary is part of the non-user prefix and will not be deleted as a user-turn.
	for a.MaxHistoryMessages > 0 && len(a.history) > a.MaxHistoryMessages {
		prevBody = extractSummaryBody(a.history)
		next, moreDropped, n := trimByUserTurns(a.history, a.MaxHistoryMessages)
		if n == 0 {
			break
		}
		droppedTurns += n
		summary = buildConversationSummary(prevBody, moreDropped)
		a.history = upsertSummary(next, summary)
	}
	return droppedTurns
}

// trimByUserTurns drops oldest user-turns until len <= maxMessages.
// dropped is the concatenation of removed turns (for summarization).
func trimByUserTurns(msgs []llm.Message, maxMessages int) (out []llm.Message, dropped []llm.Message, droppedTurns int) {
	if maxMessages <= 0 || len(msgs) <= maxMessages {
		return msgs, nil, 0
	}

	prefix, turns := splitUserTurns(msgs)
	if len(turns) == 0 {
		return msgs, nil, 0
	}

	var removed [][]llm.Message
	for len(turns) > 1 && countMessages(prefix, turns) > maxMessages {
		removed = append(removed, turns[0])
		turns = turns[1:]
		droppedTurns++
	}

	for _, t := range removed {
		dropped = append(dropped, t...)
	}
	out = joinTurns(prefix, turns)
	return out, dropped, droppedTurns
}

func splitUserTurns(msgs []llm.Message) (prefix []llm.Message, turns [][]llm.Message) {
	i := 0
	for i < len(msgs) && msgs[i].Role != llm.RoleUser {
		i++
	}
	prefix = msgs[:i]
	for i < len(msgs) {
		j := i + 1
		for j < len(msgs) && msgs[j].Role != llm.RoleUser {
			j++
		}
		turns = append(turns, msgs[i:j])
		i = j
	}
	return prefix, turns
}

func countMessages(prefix []llm.Message, turns [][]llm.Message) int {
	n := len(prefix)
	for _, t := range turns {
		n += len(t)
	}
	return n
}

func joinTurns(prefix []llm.Message, turns [][]llm.Message) []llm.Message {
	n := countMessages(prefix, turns)
	out := make([]llm.Message, 0, n)
	out = append(out, prefix...)
	for _, t := range turns {
		out = append(out, t...)
	}
	return out
}

func isSummaryMessage(m llm.Message) bool {
	return m.Role == llm.RoleSystem && strings.HasPrefix(m.Content, conversationSummaryMarker)
}

// extractSummaryBody returns the text after the marker, or empty.
func extractSummaryBody(msgs []llm.Message) string {
	for _, m := range msgs {
		if !isSummaryMessage(m) {
			continue
		}
		body := strings.TrimPrefix(m.Content, conversationSummaryMarker)
		return strings.TrimSpace(body)
	}
	return ""
}

// upsertSummary places/replaces a system summary message immediately after the
// first system message (or at index 0 if none).
func upsertSummary(msgs []llm.Message, summaryBody string) []llm.Message {
	content := conversationSummaryMarker + "\n" + strings.TrimSpace(summaryBody)
	sum := llm.Message{Role: llm.RoleSystem, Content: content}

	// Remove any existing summary messages from the non-user prefix area (and anywhere).
	filtered := make([]llm.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		if isSummaryMessage(m) {
			continue
		}
		filtered = append(filtered, m)
	}

	// Insert after first system, else at start.
	insertAt := 0
	if len(filtered) > 0 && filtered[0].Role == llm.RoleSystem {
		insertAt = 1
	}
	out := make([]llm.Message, 0, len(filtered)+1)
	out = append(out, filtered[:insertAt]...)
	out = append(out, sum)
	out = append(out, filtered[insertAt:]...)
	return out
}

// buildConversationSummary folds previous sticky summary + newly dropped messages
// into a compact, model-facing record (extractive; no extra LLM call).
func buildConversationSummary(prevBody string, dropped []llm.Message) string {
	var b strings.Builder
	b.WriteString("Earlier turns were removed to free context. Retain these facts:\n")
	if prevBody != "" {
		// Keep prior summary bullets if present; strip our own header lines if re-folded.
		prev := strings.TrimSpace(prevBody)
		prev = strings.TrimPrefix(prev, "Earlier turns were removed to free context. Retain these facts:")
		prev = strings.TrimSpace(prev)
		if prev != "" {
			b.WriteString(prev)
			if !strings.HasSuffix(prev, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	for _, m := range dropped {
		switch m.Role {
		case llm.RoleUser:
			if t := clipOneLine(m.Content, 160); t != "" {
				fmt.Fprintf(&b, "- User: %s\n", t)
			}
		case llm.RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var names []string
				for _, tc := range m.ToolCalls {
					names = append(names, tc.Function.Name)
				}
				fmt.Fprintf(&b, "- Assistant called tools: %s\n", strings.Join(names, ", "))
			}
			if t := clipOneLine(m.Content, 160); t != "" {
				fmt.Fprintf(&b, "- Assistant: %s\n", t)
			}
		case llm.RoleTool:
			name := m.Name
			if name == "" {
				name = "tool"
			}
			if t := clipOneLine(m.Content, 120); t != "" {
				fmt.Fprintf(&b, "- Tool(%s): %s\n", name, t)
			}
		}
	}
	out := strings.TrimSpace(b.String())
	// Cap summary size (reuse rune truncator from agent.go).
	capped, _ := truncateRunes(out, DefaultMaxSummaryRunes)
	return capped
}

func clipOneLine(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if maxRunes > 0 && len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return s
}
