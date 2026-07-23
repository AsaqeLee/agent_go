package tool

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// GetTime returns the current local time (parameter-less tool demo).
type GetTime struct{}

func (GetTime) Name() string { return "get_time" }
func (GetTime) Description() string {
	return "Get the current local date and time. Use when the user asks what time/date it is."
}
func (GetTime) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (GetTime) Run(_ string) (string, error) {
	return time.Now().Format(time.RFC3339), nil
}

// Calculator evaluates a simple two-operand expression (parameterized tool demo).
// Supports only forms like "a + b", "a - b", "a * b", "a / b" — no external deps.
type Calculator struct{}

func (Calculator) Name() string { return "calculator" }
func (Calculator) Description() string {
	return "Evaluate a simple arithmetic expression with two numbers. Supports +, -, *, /. Example: 12.5 * 3"
}
func (Calculator) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expression": map[string]any{
				"type":        "string",
				"description": "Expression like \"2 + 3\" or \"10 / 4\"",
			},
		},
		"required": []string{"expression"},
	}
}

type calcArgs struct {
	Expression string `json:"expression"`
}

func (Calculator) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[calcArgs](argsJSON)
	if err != nil {
		return "", err
	}
	result, err := evalTwoOperand(args.Expression)
	if err != nil {
		return "", err
	}
	return strconv.FormatFloat(result, 'g', -1, 64), nil
}

func evalTwoOperand(expr string) (float64, error) {
	expr = strings.TrimSpace(expr)
	ops := []string{"+", "-", "*", "/"}
	// Start at index 1 so a leading minus on the left operand is allowed.
	for _, op := range ops {
		idx := strings.Index(expr[1:], op)
		if idx < 0 {
			continue
		}
		i := idx + 1
		left := strings.TrimSpace(expr[:i])
		right := strings.TrimSpace(expr[i+len(op):])
		a, err := strconv.ParseFloat(left, 64)
		if err != nil {
			return 0, fmt.Errorf("left operand: %w", err)
		}
		b, err := strconv.ParseFloat(right, 64)
		if err != nil {
			return 0, fmt.Errorf("right operand: %w", err)
		}
		switch op {
		case "+":
			return a + b, nil
		case "-":
			return a - b, nil
		case "*":
			return a * b, nil
		case "/":
			if b == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return a / b, nil
		}
	}
	if v, err := strconv.ParseFloat(expr, 64); err == nil {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid number")
		}
		return v, nil
	}
	return 0, fmt.Errorf("unsupported expression %q (want like \"2 + 3\")", expr)
}

// EchoNote appends free-form text to profile notes only (no name/like extraction).
type EchoNote struct {
	Store MemoryStore
}

func (EchoNote) Name() string { return "echo_note" }
func (EchoNote) Description() string {
	return "Append a free-text note to the durable profile notes list only. " +
		"Does NOT parse name or likes — you (the model) must classify fields yourself. " +
		"Prefer profile_update when you can fill structured name/likes/notes. " +
		"Use echo_note only for leftover free-form text that does not fit fields."
}
func (EchoNote) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Free-form note text (stored as notes only)",
			},
		},
		"required": []string{"text"},
	}
}

type noteArgs struct {
	Text string `json:"text"`
}

func (e EchoNote) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[noteArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Text) == "" {
		return "", fmt.Errorf("text is empty")
	}
	if e.Store == nil {
		return fmt.Sprintf("noted (ephemeral, no profile store): %s", args.Text), nil
	}
	return e.Store.Remember(args.Text), nil
}

// MemorySet writes one structured profile field (name | like | note).
type MemorySet struct {
	Store MemoryStore
}

func (MemorySet) Name() string { return "memory_set" }
func (MemorySet) Description() string {
	return "Set exactly one durable profile field. " +
		"field=name for the user's name, field=like for one preference, field=note for one fact. " +
		"You choose the field and value (LLM extraction) — the runtime does not regex-parse user text. " +
		"For multiple fields in one step prefer profile_update."
}
func (MemorySet) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"field": map[string]any{
				"type":        "string",
				"description": "One of: name, like, note",
				"enum":        []string{"name", "like", "note"},
			},
			"value": map[string]any{
				"type":        "string",
				"description": "Value you extracted for that field",
			},
		},
		"required": []string{"field", "value"},
	}
}

type memorySetArgs struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func (t MemorySet) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[memorySetArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if t.Store == nil {
		return "", fmt.Errorf("no profile store configured")
	}
	return t.Store.SetField(args.Field, args.Value)
}

// ProfileUpdate is the primary way for the LLM to write structured profile fields.
// The model fills JSON fields (schema); the runtime only stores them — no regex.
type ProfileUpdate struct {
	Store MemoryStore
}

func (ProfileUpdate) Name() string { return "profile_update" }
func (ProfileUpdate) Description() string {
	return "Update the durable user profile with structured fields you extract from the conversation. " +
		"Call this when the user states lasting facts (name, preferences, other notes). " +
		"Pass only fields you are confident about; omit unknown fields or use empty values. " +
		"Do not invent data. Profile survives history trim and is injected as [user_profile]."
}
func (ProfileUpdate) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "User's name if stated; omit or empty if unknown",
			},
			"likes": map[string]any{
				"type":        "array",
				"description": "Preferences/likes to add (each short string)",
				"items":       map[string]any{"type": "string"},
			},
			"notes": map[string]any{
				"type":        "array",
				"description": "Other durable facts as short notes",
				"items":       map[string]any{"type": "string"},
			},
		},
	}
}

type profileUpdateArgs struct {
	Name  string   `json:"name"`
	Likes []string `json:"likes"`
	Notes []string `json:"notes"`
}

func (t ProfileUpdate) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[profileUpdateArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if t.Store == nil {
		return "", fmt.Errorf("no profile store configured")
	}
	return t.Store.ApplyPatch(args.Name, args.Likes, args.Notes)
}

// WordCount counts whitespace-separated tokens (strings.Fields).
// Chinese/CJK without spaces is typically one token; punctuation may stick to tokens.
type WordCount struct{}

func (WordCount) Name() string { return "word_count" }
func (WordCount) Description() string {
	return "Count whitespace-separated tokens in text (split on spaces/tabs/newlines, same as Go strings.Fields). " +
		"Use when the user asks how many words or tokens a passage has and needs an exact count. " +
		"Important: text without whitespace (e.g. continuous Chinese/CJK) counts as ONE token, not per character. " +
		"Punctuation usually stays attached to the adjacent token (e.g. \"Hello,\" is one token)."
}
func (WordCount) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Full text to count. Tokens are whitespace-separated; no language-specific word segmentation.",
			},
		},
		"required": []string{"text"},
	}
}

type wordCountArgs struct {
	Text string `json:"text"`
}

func (WordCount) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[wordCountArgs](argsJSON)
	if err != nil {
		return "", err
	}
	words := strings.Fields(args.Text)
	return fmt.Sprintf("%d", len(words)), nil
}

// DefaultTools returns the built-in teaching toolset.
// Pass a MemoryStore (e.g. *agent.Memory) so echo_note / memory_set persist profile fields.
// store may be nil (notes become ephemeral).
func DefaultTools(store MemoryStore) []Tool {
	return []Tool{
		GetTime{},
		Calculator{},
		ProfileUpdate{Store: store},
		MemorySet{Store: store},
		EchoNote{Store: store},
		WordCount{},
	}
}
