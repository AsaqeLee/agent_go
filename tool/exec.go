package tool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Result codes recorded on ExecResult (model still sees Content only).
const (
	CodeOK       = "ok"
	CodeError    = "error"
	CodeUnknown  = "unknown"
	CodeCanceled = "canceled"
	CodeDenied   = "denied"
	CodeTimeout  = "timeout"
)

// ExecResult is what the runtime observes for one tool call.
// Content is the string written back to the model (and history).
type ExecResult struct {
	Name     string
	Content  string
	Err      error
	Duration time.Duration
	Code     string
}

// Policy is optional runtime wrapping around Tool.Run.
// Zero value: no timeout, no approval, no redaction.
type Policy struct {
	Timeout  time.Duration
	Approver Approver
	Redact   func(name, content string) string
}

// Approver gates tool execution. Nil on Policy means allow.
type Approver interface {
	Approve(ctx context.Context, req Approval) (bool, error)
}

// Approval is one pending tool call presented to a human or policy engine.
type Approval struct {
	Name string
	Args string
	Meta Annotations
}

// Annotations are optional tool hints (MCP-style). Tools may implement Annotator.
type Annotations struct {
	Title         string
	ReadOnly      bool
	Destructive   bool
	OpenWorld     bool
	NeedsApproval bool
}

// Annotator is an optional Tool extra for permission / UX hints.
type Annotator interface {
	Annotations() Annotations
}

// Execute runs a tool by name. Unknown tools return an error string (never panic).
func (r *Registry) Execute(ctx context.Context, name, argsJSON string) string {
	return r.ExecuteDetail(ctx, name, argsJSON).Content
}

// ExecuteDetail runs a tool and returns timing / error code for logs, traces, and events.
func (r *Registry) ExecuteDetail(ctx context.Context, name, argsJSON string) (res ExecResult) {
	start := time.Now()
	res.Name = name
	defer func() { res.Duration = time.Since(start) }()

	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return canceledResult(name, err)
	}
	t, ok := r.byName[name]
	if !ok {
		res.Code = CodeUnknown
		res.Err = fmt.Errorf("unknown tool %q", name)
		res.Content = fmt.Sprintf("error: unknown tool %q", name)
		return res
	}

	meta := annotationsOf(t)
	if r.Policy.Approver != nil && meta.NeedsApproval {
		allow, err := r.Policy.Approver.Approve(ctx, Approval{Name: name, Args: argsJSON, Meta: meta})
		if err != nil {
			res.Code = CodeError
			res.Err = err
			res.Content = fmt.Sprintf("error: %v", err)
			return res
		}
		if !allow {
			res.Code = CodeDenied
			res.Err = fmt.Errorf("tool call denied")
			res.Content = "error: tool call denied"
			return res
		}
	}

	runCtx := ctx
	cancel := func() {}
	if r.Policy.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.Policy.Timeout)
	}
	defer cancel()

	result, err := t.Run(runCtx, argsJSON)
	if err != nil {
		res.Err = err
		res.Content = fmt.Sprintf("error: %v", err)
		switch {
		case errors.Is(err, context.DeadlineExceeded) || runCtx.Err() == context.DeadlineExceeded:
			res.Code = CodeTimeout
		case errors.Is(err, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled):
			res.Code = CodeCanceled
		default:
			res.Code = CodeError
		}
		return res
	}
	if r.Policy.Redact != nil {
		result = r.Policy.Redact(name, result)
	}
	res.Code = CodeOK
	res.Content = result
	return res
}

func canceledResult(name string, err error) ExecResult {
	code := CodeCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		code = CodeTimeout
	}
	return ExecResult{
		Name:    name,
		Code:    code,
		Err:     err,
		Content: fmt.Sprintf("error: %v", err),
	}
}

func annotationsOf(t Tool) Annotations {
	if a, ok := t.(Annotator); ok {
		return a.Annotations()
	}
	return Annotations{}
}
