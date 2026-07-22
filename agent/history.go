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

// Summary is intentionally lossy: we do NOT archive full dropped turns.
// Only short, high-signal facts (user claims + tool results + short assistant
// conclusions) are kept, with a hard bullet cap and rune cap.
const (
	// DefaultMaxSummaryRunes hard-caps the entire sticky summary message.
	DefaultMaxSummaryRunes = 512
	// maxSummaryBullets keeps only the newest N facts (older ones fall off).
	maxSummaryBullets = 12
	// maxFactRunes clips each bullet body (not counting the "- user: " prefix).
	maxFactRunes = 64
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

// buildConversationSummary merges prior sticky facts with newly dropped turns into a
// **lossy** rolling memory: short bullets only, newest preferred, hard size limits.
// This is not an archive of the full conversation (that would re-bloat context).
func buildConversationSummary(prevBody string, dropped []llm.Message) string {
	bullets := parseSummaryBullets(prevBody)
	bullets = append(bullets, factsFromDropped(dropped)...)
	bullets = dedupeBullets(bullets)
	if len(bullets) > maxSummaryBullets {
		// Keep newest facts; older low-priority detail falls off.
		bullets = bullets[len(bullets)-maxSummaryBullets:]
	}

	var b strings.Builder
	b.WriteString("Lossy memory of trimmed turns (not full transcript). Prefer these facts:\n")
	for _, line := range bullets {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := strings.TrimSpace(b.String())
	capped, _ := truncateRunes(out, DefaultMaxSummaryRunes)
	return capped
}

// parseSummaryBullets pulls "- ..." lines from a previous summary body.
func parseSummaryBullets(body string) []string {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	// Drop legacy headers if re-folding old summaries.
	for _, header := range []string{
		"Lossy memory of trimmed turns (not full transcript). Prefer these facts:",
		"Earlier turns were removed to free context. Retain these facts:",
	} {
		body = strings.TrimSpace(strings.TrimPrefix(body, header))
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// factsFromDropped extracts high-signal, short facts only.
// Skips long assistant prose and pure tool-call shells that add little memory value.
func factsFromDropped(dropped []llm.Message) []string {
	var facts []string
	for _, m := range dropped {
		switch m.Role {
		case llm.RoleUser:
			if f := formatFact("user", m.Content); f != "" {
				facts = append(facts, f)
			}
		case llm.RoleTool:
			name := m.Name
			if name == "" {
				name = "tool"
			}
			// Tool outputs (notes, calc results) are the highest-value memory signal.
			if f := formatFact("tool:"+name, m.Content); f != "" {
				facts = append(facts, f)
			}
		case llm.RoleAssistant:
			// Skip "I will call a tool" shells with no useful prose.
			if len(m.ToolCalls) > 0 && strings.TrimSpace(m.Content) == "" {
				continue
			}
			// Long assistant chatter is usually noise for memory; keep only short conclusions.
			if utf8.RuneCountInString(strings.TrimSpace(m.Content)) > 80 {
				continue
			}
			if f := formatFact("asst", m.Content); f != "" {
				facts = append(facts, f)
			}
		}
	}
	return facts
}

func formatFact(kind, content string) string {
	t := clipOneLine(content, maxFactRunes)
	if t == "" {
		return ""
	}
	return kind + ": " + t
}

func dedupeBullets(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, b := range in {
		key := strings.ToLower(strings.TrimSpace(b))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, b)
	}
	return out
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
