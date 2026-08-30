package tool

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecuteDetailUnknown(t *testing.T) {
	r := NewRegistry(nil)
	res := r.ExecuteDetail(context.Background(), "nope", `{}`)
	if res.Code != CodeUnknown || res.Err == nil {
		t.Fatalf("%+v", res)
	}
	if !strings.HasPrefix(res.Content, "error:") {
		t.Fatalf("content=%q", res.Content)
	}
	if r.Execute(context.Background(), "nope", `{}`) != res.Content {
		t.Fatal("Execute should equal ExecuteDetail.Content")
	}
}

func TestExecuteDetailOKDuration(t *testing.T) {
	r := NewRegistry([]Tool{GetTime{}})
	res := r.ExecuteDetail(context.Background(), "get_time", `{}`)
	if res.Code != CodeOK || res.Content == "" || res.Duration < 0 {
		t.Fatalf("%+v", res)
	}
}

type denyAll struct{}

func (denyAll) Approve(context.Context, Approval) (bool, error) { return false, nil }

type needsOK struct{}

func (needsOK) Name() string                   { return "needs_ok" }
func (needsOK) Description() string            { return "x" }
func (needsOK) Parameters() map[string]any     { return map[string]any{"type": "object"} }
func (needsOK) Run(context.Context, string) (string, error) {
	return "ran", nil
}
func (needsOK) Annotations() Annotations {
	return Annotations{NeedsApproval: true}
}

func TestExecuteDetailDenied(t *testing.T) {
	r := NewRegistry([]Tool{needsOK{}})
	r.Policy.Approver = denyAll{}
	res := r.ExecuteDetail(context.Background(), "needs_ok", `{}`)
	if res.Code != CodeDenied {
		t.Fatalf("%+v", res)
	}
}

type slowTool struct{}

func (slowTool) Name() string               { return "slow" }
func (slowTool) Description() string        { return "x" }
func (slowTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (slowTool) Run(ctx context.Context, _ string) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func TestExecuteDetailTimeout(t *testing.T) {
	r := NewRegistry([]Tool{slowTool{}})
	r.Policy.Timeout = 20 * time.Millisecond
	res := r.ExecuteDetail(context.Background(), "slow", `{}`)
	if res.Code != CodeTimeout {
		t.Fatalf("code=%s err=%v", res.Code, res.Err)
	}
}

func TestExecuteDetailRedact(t *testing.T) {
	r := NewRegistry([]Tool{GetTime{}})
	r.Policy.Redact = func(name, content string) string { return "redacted:" + name }
	res := r.ExecuteDetail(context.Background(), "get_time", `{}`)
	if res.Content != "redacted:get_time" || res.Code != CodeOK {
		t.Fatalf("%+v", res)
	}
}
