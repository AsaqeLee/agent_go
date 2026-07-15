// Package tool defines callable tools for the agent.
// A tool is: name + description + JSON Schema parameters + Run implementation.
package tool

import (
	"encoding/json"
	"fmt"

	"github.com/asaqelee/agent_go/llm"
)

// Tool is a function the model may select and the runtime executes.
type Tool interface {
	// Name must be unique; the model uses it to select the tool.
	Name() string
	// Description tells the model when to use this tool.
	Description() string
	// Parameters is a JSON Schema object describing arguments.
	Parameters() map[string]any
	// Run executes the tool with the model-provided JSON arguments.
	Run(argsJSON string) (string, error)
}

// Defs converts tools into definitions sent to the LLM.
func Defs(tools []Tool) []llm.ToolDef {
	out := make([]llm.ToolDef, 0, len(tools))
	for _, t := range tools {
		params := t.Parameters()
		if params == nil {
			params = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		out = append(out, llm.ToolDef{
			Type: "function",
			Function: llm.ToolFunctionSchema{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  params,
			},
		})
	}
	return out
}

// Registry looks up tools by name and executes them.
type Registry struct {
	byName map[string]Tool
}

// NewRegistry builds a registry from a tool list.
func NewRegistry(tools []Tool) *Registry {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Registry{byName: m}
}

// Execute runs a tool by name. Unknown tools return an error string (never panic).
func (r *Registry) Execute(name, argsJSON string) string {
	t, ok := r.byName[name]
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", name)
	}
	result, err := t.Run(argsJSON)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return result
}

// ParseArgs unmarshals model-provided JSON into a typed struct.
func ParseArgs[T any](argsJSON string) (T, error) {
	var v T
	if argsJSON == "" {
		argsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(argsJSON), &v); err != nil {
		return v, fmt.Errorf("invalid arguments json: %w", err)
	}
	return v, nil
}
