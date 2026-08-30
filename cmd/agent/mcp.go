package main

import (
	"context"
	"fmt"
	"os"

	"github.com/asaqelee/agent_go/mcp"
	"github.com/asaqelee/agent_go/tool"
)

func lookupMCPConfig() string {
	if v := env("AGENT_MCP_CONFIG", ""); v != "" {
		if v == "off" || v == "-" {
			return ""
		}
		return v
	}
	if st, err := os.Stat("mcp.json"); err == nil && !st.IsDir() {
		return "mcp.json"
	}
	return ""
}

func startMCP(ctx context.Context) ([]tool.Tool, func()) {
	path := lookupMCPConfig()
	if path == "" {
		return nil, func() {}
	}
	cfg, err := mcp.LoadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp config %s: %v\n", path, err)
		os.Exit(1)
	}
	if len(cfg.Servers) == 0 {
		return nil, func() {}
	}
	b, err := mcp.Connect(ctx, cfg.Servers)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcp: %v\n", err)
		os.Exit(1)
	}
	tools := b.Tools()
	fmt.Fprintf(os.Stderr, "mcp: %s servers=%d tools=%d (default-deny allowlist)\n", path, len(cfg.Servers), len(tools))
	for _, t := range tools {
		fmt.Fprintf(os.Stderr, "  mcp tool %s\n", t.Name())
	}
	return tools, b.Close
}
