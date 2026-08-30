package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONLParentChild(t *testing.T) {
	var buf bytes.Buffer
	tr := NewJSONL(&buf)
	ctx, root := tr.Start(context.Background(), "agent.run")
	root.Set("input", "hi")
	_, child := tr.Start(ctx, "llm.chat")
	child.End()
	root.End()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines=%d %s", len(lines), buf.String())
	}
	var a, b map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &a)
	_ = json.Unmarshal([]byte(lines[1]), &b)
	if a["name"] != "llm.chat" || b["name"] != "agent.run" {
		t.Fatalf("%v %v", a, b)
	}
	if a["trace_id"] != b["trace_id"] {
		t.Fatalf("trace mismatch")
	}
	if a["parent_span_id"] != b["span_id"] {
		t.Fatalf("parent=%v root span=%v", a["parent_span_id"], b["span_id"])
	}
}

func TestNop(t *testing.T) {
	ctx, sp := Nop().Start(context.Background(), "x")
	sp.Set("a", 1)
	sp.End()
	_ = ctx
}
