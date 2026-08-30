package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/plugin"
)

func runPluginCLI(_ context.Context, args []string) int {
	cmd := "list"
	if len(args) > 0 {
		cmd = args[0]
	}
	if cmd != "list" {
		fmt.Fprintln(os.Stderr, "usage: agent plugin list")
		return 2
	}
	fmt.Println("plugin catalog")
	if p := plugin.LookupDefault(); p != "" {
		fmt.Printf("  config: %s\n", p)
		if cfg, err := plugin.Load(p); err != nil {
			fmt.Printf("  load error: %v\n", err)
		} else {
			fmt.Printf("  retriever: %s\n", emptyDash(cfg.Retriever))
			fmt.Printf("  index: %s\n", emptyDash(cfg.IndexPath))
			fmt.Printf("  http: %s\n", emptyDash(cfg.HTTP.Addr))
			if len(cfg.MCP.Servers) == 0 {
				fmt.Println("  mcp: (none)")
			} else {
				fmt.Println("  mcp:")
				for _, s := range cfg.MCP.Servers {
					allow := strings.Join(s.Allow, ",")
					if allow == "" {
						allow = "(deny all)"
					}
					fmt.Printf("    - %s cmd=%s allow=%s\n", s.Name, s.Command, allow)
				}
			}
			if len(cfg.Specialists) == 0 {
				fmt.Println("  specialists: (none)")
			} else {
				fmt.Println("  specialists:")
				for _, s := range cfg.Specialists {
					fmt.Printf("    - %s tools=%s\n", s.Name, strings.Join(s.Tools, ","))
				}
			}
		}
	} else {
		fmt.Println("  config: (none — copy agent.json.example → agent.json)")
	}
	if p := lookupMCPConfig(); p != "" {
		fmt.Printf("  mcp.json: %s\n", p)
	}
	fmt.Printf("  env retriever=%s index=%s\n", env("AGENT_RETRIEVER", "grep"), env("AGENT_INDEX_PATH", ".agent_index.json"))
	return 0
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func attachRoster(a *agent.Agent, docsRoot string) {
	if a == nil {
		return
	}
	specs := catalog.Specialists
	if len(specs) == 0 && docsRoot != "" {
		specs = []agent.Specialist{{
			Name:        "docs",
			Description: "Answer only from the local knowledge base and cite paths.",
			Prompt:      "You are a docs specialist. Use list_docs, search_docs, and read_doc only. Cite relative paths. If the corpus lacks the answer, say you do not know from the knowledge base.",
			Tools:       []string{"list_docs", "search_docs", "read_doc"},
		}}
	}
	if len(specs) == 0 {
		return
	}
	a.Tools = append(a.Tools, agent.NewRoster(a, specs).Tool())
}
