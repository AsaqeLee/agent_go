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

// EchoNote records free-form text into the structured MemoryStore (notes + heuristics).
type EchoNote struct {
	Store MemoryStore
}

func (EchoNote) Name() string { return "echo_note" }
func (e EchoNote) Description() string {
	return "Save a free-form note into the durable user profile (survives chat history trim). " +
		"Use when the user asks to remember something loosely. " +
		"For clear fields prefer memory_set (name|like|note). " +
		"May also detect name/likes from phrases like 我叫… / 喜欢…"
}
func (EchoNote) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The note content to persist into profile notes",
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
	return "Set a durable user profile field that survives history trim. " +
		"Use field=name for the user's name, field=like for a preference, field=note for other facts. " +
		"Prefer this over echo_note when the field is clear."
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
				"description": "Value to store for that field",
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
		EchoNote{Store: store},
		MemorySet{Store: store},
		WordCount{},
	}
}
