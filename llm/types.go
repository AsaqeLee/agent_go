// Package llm defines the minimal chat/completion abstraction used by the agent.
// The agent depends only on these types and does not care which vendor implements them.
package llm

import "context"

// Role is a chat message role (OpenAI Chat Completions style).
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in the conversation.
// When the model wants tools, the assistant message carries ToolCalls.
// Tool results are returned as RoleTool messages with ToolCallID set.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"` // always "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and raw JSON arguments.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef describes a tool to the model (JSON Schema parameters).
type ToolDef struct {
	Type     string             `json:"type"` // always "function"
	Function ToolFunctionSchema `json:"function"`
}

// ToolFunctionSchema is the function metadata sent to the model.
type ToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Request is one completion request.
type Request struct {
	Model    string
	Messages []Message
	Tools    []ToolDef
}

// Response is one completion result.
type Response struct {
	Message Message
}

// Provider is the model backend: given history (+ tools), return the next assistant message.
type Provider interface {
	Chat(ctx context.Context, req Request) (Response, error)
}
