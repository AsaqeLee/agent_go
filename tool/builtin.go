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

// EchoNote records a short note and confirms it (tiny side-effect demo).
type EchoNote struct{}

func (EchoNote) Name() string { return "echo_note" }
func (EchoNote) Description() string {
	return "Save a short note and confirm it was recorded. Use when the user asks to remember or note something."
}
func (EchoNote) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The note content",
			},
		},
		"required": []string{"text"},
	}
}

type noteArgs struct {
	Text string `json:"text"`
}

func (EchoNote) Run(argsJSON string) (string, error) {
	args, err := ParseArgs[noteArgs](argsJSON)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Text) == "" {
		return "", fmt.Errorf("text is empty")
	}
	return fmt.Sprintf("noted: %s", args.Text), nil
}

// DefaultTools returns the built-in teaching toolset.
func DefaultTools() []Tool {
	return []Tool{
		GetTime{},
		Calculator{},
		EchoNote{},
	}
}
