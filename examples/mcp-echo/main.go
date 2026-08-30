// Command mcp-echo is a tiny MCP server for local wiring tests.
//
//	go run ./examples/mcp-echo
//
// Pair with mcp.json:
//
//	{"servers":[{"name":"echo","command":"go","args":["run","./examples/mcp-echo"],"allow":["*"]}]}
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/asaqelee/agent_go/mcp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := mcp.Serve(ctx, os.Stdin, os.Stdout, mcp.ImplInfo{Name: "echo", Version: "0.1.0"}, []mcp.ServerTool{{
		Name:        "echo",
		Description: "Echo text back. Read-only demo tool.",
		ReadOnly:    true,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Text to echo"},
			},
			"required": []string{"text"},
		},
		Run: func(_ context.Context, args map[string]any) (string, error) {
			s, _ := args["text"].(string)
			return fmt.Sprintf("echo:%s", s), nil
		},
	}})
	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "mcp-echo: %v\n", err)
		os.Exit(1)
	}
}
