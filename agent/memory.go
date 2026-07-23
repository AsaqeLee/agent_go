package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/llm"
)

// profileMarker prefixes the sticky system block injected from structured Memory.
const profileMarker = "[user_profile]"

const (
	maxLikes     = 12
	maxNotes     = 16
	maxNoteRunes = 120
	maxLikeRunes = 40
	maxNameRunes = 40
)

// Memory is a structured, bounded profile that survives history trim.
// It is NOT a chat transcript: only a few fields, injected into the system prompt.
type Memory struct {
	Name  string
	Likes []string
	Notes []string
}

// NewMemory returns an empty profile store.
func NewMemory() *Memory {
	return &Memory{}
}

// Empty reports whether any field is set.
func (m *Memory) Empty() bool {
	if m == nil {
		return true
	}
	return m.Name == "" && len(m.Likes) == 0 && len(m.Notes) == 0
}

// Clear wipes all fields.
func (m *Memory) Clear() {
	if m == nil {
		return
	}
	m.Name = ""
	m.Likes = nil
	m.Notes = nil
}

// Snapshot returns a copy for CLI display.
func (m *Memory) Snapshot() Memory {
	if m == nil {
		return Memory{}
	}
	out := Memory{Name: m.Name}
	if len(m.Likes) > 0 {
		out.Likes = append([]string(nil), m.Likes...)
	}
	if len(m.Notes) > 0 {
		out.Notes = append([]string(nil), m.Notes...)
	}
	return out
}

// RenderSystemBlock formats fields for model context. Empty memory → "".
func (m *Memory) RenderSystemBlock() string {
	if m.Empty() {
		return ""
	}
	var b strings.Builder
	b.WriteString(profileMarker)
	b.WriteString("\nDurable user profile (structured fields; prefer over chat fluff):\n")
	if m.Name != "" {
		fmt.Fprintf(&b, "- name: %s\n", m.Name)
	}
	if len(m.Likes) > 0 {
		fmt.Fprintf(&b, "- likes: %s\n", strings.Join(m.Likes, "; "))
	}
	if len(m.Notes) > 0 {
		b.WriteString("- notes:\n")
		for _, n := range m.Notes {
			fmt.Fprintf(&b, "  - %s\n", n)
		}
	}
	return strings.TrimSpace(b.String())
}

// SetField updates name|like|note. Implements tool.MemoryStore.
func (m *Memory) SetField(field, value string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("memory is nil")
	}
	field = strings.ToLower(strings.TrimSpace(field))
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("value is empty")
	}
	switch field {
	case "name":
		m.setName(value)
		return fmt.Sprintf("profile updated: name=%s", m.Name), nil
	case "like", "likes":
		m.addLike(value)
		return fmt.Sprintf("profile updated: likes=%s", strings.Join(m.Likes, "; ")), nil
	case "note", "notes":
		m.addNote(value)
		return fmt.Sprintf("profile updated: note recorded (%d notes)", len(m.Notes)), nil
	default:
		return "", fmt.Errorf("unknown field %q (want name|like|note)", field)
	}
}

// Remember appends free-form text to notes only (no regex/heuristic field extraction).
// Name/likes must be set via SetField or ApplyPatch (LLM tool arguments).
func (m *Memory) Remember(text string) string {
	if m == nil {
		return "error: memory is nil"
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "error: text is empty"
	}
	m.addNote(text)
	return fmt.Sprintf("noted into profile notes: %s | %s", clipRunes(text, 80), m.ShortStatus())
}

// ApplyPatch applies a structured multi-field update from the LLM (tool JSON args).
// Empty name and empty slices are ignored so the model can send partial patches.
func (m *Memory) ApplyPatch(name string, likes []string, notes []string) (string, error) {
	if m == nil {
		return "", fmt.Errorf("memory is nil")
	}
	changed := false
	if n := strings.TrimSpace(name); n != "" {
		m.setName(n)
		changed = true
	}
	for _, like := range likes {
		like = strings.TrimSpace(like)
		if like == "" {
			continue
		}
		m.addLike(like)
		changed = true
	}
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		m.addNote(note)
		changed = true
	}
	if !changed {
		return "", fmt.Errorf("empty patch: set name and/or likes and/or notes")
	}
	return fmt.Sprintf("profile patch applied | %s", m.ShortStatus()), nil
}

// ShortStatus is a one-line profile summary for logs/CLI.
func (m *Memory) ShortStatus() string {
	if m == nil {
		return "empty"
	}
	return m.shortStatus()
}

func (m *Memory) shortStatus() string {
	var parts []string
	if m.Name != "" {
		parts = append(parts, "name="+m.Name)
	}
	if len(m.Likes) > 0 {
		parts = append(parts, "likes="+strings.Join(m.Likes, ","))
	}
	parts = append(parts, fmt.Sprintf("notes=%d", len(m.Notes)))
	return strings.Join(parts, "; ")
}

func (m *Memory) setName(v string) {
	m.Name = clipRunes(v, maxNameRunes)
}

func (m *Memory) addLike(v string) {
	v = clipRunes(v, maxLikeRunes)
	if v == "" {
		return
	}
	for _, x := range m.Likes {
		if strings.EqualFold(x, v) {
			return
		}
	}
	m.Likes = append(m.Likes, v)
	if len(m.Likes) > maxLikes {
		m.Likes = m.Likes[len(m.Likes)-maxLikes:]
	}
}

func (m *Memory) addNote(v string) {
	v = clipRunes(v, maxNoteRunes)
	if v == "" {
		return
	}
	for _, x := range m.Notes {
		if x == v {
			return
		}
	}
	m.Notes = append(m.Notes, v)
	if len(m.Notes) > maxNotes {
		m.Notes = m.Notes[len(m.Notes)-maxNotes:]
	}
}

func clipRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

func isProfileMessage(m llm.Message) bool {
	return m.Role == llm.RoleSystem && strings.HasPrefix(m.Content, profileMarker)
}

// upsertProfile injects/replaces/removes the [user_profile] system block after the main system
// prompt (and after conversation_summary if present, still before user turns).
func upsertProfile(msgs []llm.Message, mem *Memory) []llm.Message {
	filtered := make([]llm.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		if isProfileMessage(m) {
			continue
		}
		filtered = append(filtered, m)
	}
	block := ""
	if mem != nil {
		block = mem.RenderSystemBlock()
	}
	if block == "" {
		return filtered
	}
	// Insert after leading system messages (main + optional summary), before first user.
	insertAt := 0
	for insertAt < len(filtered) && filtered[insertAt].Role == llm.RoleSystem {
		insertAt++
	}
	out := make([]llm.Message, 0, len(filtered)+1)
	out = append(out, filtered[:insertAt]...)
	out = append(out, llm.Message{Role: llm.RoleSystem, Content: block})
	out = append(out, filtered[insertAt:]...)
	return out
}
