package agent

import (
	"time"

	"github.com/asaqelee/agent_go/llm"
)

// EventKind is a runtime observation the CLI / HTTP layer can subscribe to.
type EventKind string

const (
	EventToken     EventKind = "token"
	EventToolStart EventKind = "tool_start"
	EventToolEnd   EventKind = "tool_end"
	EventDone      EventKind = "done"
	EventError     EventKind = "error"
	EventApproval  EventKind = "approval"
)

// Event is one loop observation. Zero fields unused for a given Kind.
type Event struct {
	Kind       EventKind
	Token      string
	Tool       string
	ToolID     string
	Args       string
	Content    string
	Code       string
	Duration   time.Duration
	Err        string
	Usage      llm.Usage
	ApprovalID string
}

func (a *Agent) emit(ev Event) {
	if a == nil || a.OnEvent == nil {
		return
	}
	a.OnEvent(ev)
}
