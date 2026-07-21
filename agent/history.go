package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
)

// HistoryStats summarizes session size for observability (/history, debugging).
type HistoryStats struct {
	Messages int
	Bytes    int
	Runes    int
	// ByRole counts messages per role (system/user/assistant/tool).
	ByRole map[string]int
	// UserTurns is the number of user-started exchanges (each starts at RoleUser).
	UserTurns int
	// MaxHistoryMessages is the configured cap (0 = unlimited).
	MaxHistoryMessages int
	// OverLimit is true when Messages > MaxHistoryMessages and cap is active.
	// After a successful trim this should be false unless a single turn still exceeds the cap.
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
		// Rough extra for tool call payloads in assistant messages.
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
		// Stable-ish order for common roles.
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

// trimHistory drops the oldest complete user-turns until len(history) <= MaxHistoryMessages.
// A user-turn is: one RoleUser message plus all following messages until the next RoleUser.
// System messages at the front are always kept. tool_calls assistants are never split from
// their following tool results (they live inside the same user-turn).
//
// Returns how many user-turns were dropped. MaxHistoryMessages <= 0 means no trim.
func (a *Agent) trimHistory() int {
	if a.MaxHistoryMessages <= 0 || len(a.history) <= a.MaxHistoryMessages {
		return 0
	}
	trimmed, dropped := trimByUserTurns(a.history, a.MaxHistoryMessages)
	a.history = trimmed
	return dropped
}

// trimByUserTurns is the pure trimming algorithm (testable without Agent).
func trimByUserTurns(msgs []llm.Message, maxMessages int) (out []llm.Message, droppedTurns int) {
	if maxMessages <= 0 || len(msgs) <= maxMessages {
		return msgs, 0
	}

	// Split: leading non-user prefix (normally system) + user-turns.
	prefix, turns := splitUserTurns(msgs)
	if len(turns) == 0 {
		// No user turns to drop; cannot safely trim further without risking protocol.
		return msgs, 0
	}

	// Drop oldest turns until under cap, but always keep the latest turn if possible.
	for len(turns) > 1 && countMessages(prefix, turns) > maxMessages {
		turns = turns[1:]
		droppedTurns++
	}

	out = joinTurns(prefix, turns)
	// If still over (single huge turn), leave it: safer than splitting tool pairs.
	return out, droppedTurns
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
