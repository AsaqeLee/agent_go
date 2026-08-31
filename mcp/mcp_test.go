package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/tool"
)

func TestClientListAndCall(t *testing.T) {
	sr, cw := io.Pipe()
	cr, sw := io.Pipe()
	defer sr.Close()
	defer cw.Close()
	defer cr.Close()
	defer sw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		_ = Serve(ctx, sr, sw, ImplInfo{Name: "echo"}, []ServerTool{{
			Name:        "echo",
			Description: "echo text",
			ReadOnly:    true,
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string"},
				},
			},
			Run: func(_ context.Context, args map[string]any) (string, error) {
				s, _ := args["text"].(string)
				return "echo:" + s, nil
			},
		}})
	}()

	c := newClient("echo", cw, cr)
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("%+v", tools)
	}
	out, err := c.CallTool(ctx, "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo:hi" {
		t.Fatalf("out=%q", out)
	}
}

func TestServeEchoesStringID(t *testing.T) {
	sr, cw := io.Pipe()
	cr, sw := io.Pipe()
	defer sr.Close()
	defer cw.Close()
	defer cr.Close()
	defer sw.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = Serve(ctx, sr, sw, ImplInfo{Name: "echo"}, nil) }()

	req := []byte(`{"jsonrpc":"2.0","id":"abc","method":"ping"}`)
	if err := writeMsg(cw, json.RawMessage(req)); err != nil {
		t.Fatal(err)
	}
	raw, err := readMsg(bufio.NewReader(cr))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"id":"abc"`)) && !bytes.Contains(raw, []byte(`"id": "abc"`)) {
		t.Fatalf("id not echoed: %s", raw)
	}
}

func TestDialEchoCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skip exec dial in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	echo := filepath.Join(dir, "..", "examples", "mcp-echo")
	c, err := Dial(ctx, ServerConfig{Name: "echo", Command: "go", Args: []string{"run", echo}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.CallTool(ctx, "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "echo:hi" {
		t.Fatalf("out=%q", out)
	}
}

func TestAllowlistDefaultDeny(t *testing.T) {
	cfg := ServerConfig{Name: "s", Allow: nil}
	if allowed(cfg, "echo") {
		t.Fatal("empty allow should deny")
	}
	cfg.Allow = []string{"*"}
	if !allowed(cfg, "echo") {
		t.Fatal("star should allow")
	}
	cfg.Allow = []string{"other"}
	if allowed(cfg, "echo") {
		t.Fatal("unlisted should deny")
	}
	cfg.Allow = []string{"s__echo"}
	if !allowed(cfg, "echo") {
		t.Fatal("prefixed allow should match")
	}
}

func TestMcpToolNeedsApprovalUnlessReadOnly(t *testing.T) {
	write := mcpTool{prefix: "s", schema: toolSchema{Name: "rm"}}
	if !write.Annotations().NeedsApproval {
		t.Fatal("write tools need approval")
	}
	ro := mcpTool{prefix: "s", schema: toolSchema{
		Name:        "read",
		Annotations: &toolAnnot{ReadOnlyHint: true},
	}}
	if ro.Annotations().NeedsApproval {
		t.Fatal("read-only should skip approval")
	}
	if !strings.HasPrefix(write.Name(), "s__") {
		t.Fatalf("name=%s", write.Name())
	}
	_ = tool.Defs([]tool.Tool{write})
}
