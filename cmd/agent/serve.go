package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/httpapi"
	"github.com/asaqelee/agent_go/session"
)

func runServeCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", env("AGENT_HTTP_ADDR", ":8080"), "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mcpTools, stopMCP := startMCP(ctx)
	defer stopMCP()
	provider, mem, _ := buildProviderAndMemory()
	docsRoot := resolveDocsRoot()
	park := httpapi.NewPark()

	h := httpapi.Handler(httpapi.Deps{
		Park: park,
		NewAgent: func(sessionID string) *agent.Agent {
			a := newSyncAgent(provider, mem, docsRoot, mcpTools)
			a.SessionID = sessionID
			dir := env("AGENT_SESSION_DIR", ".agent_sessions")
			if dir != "off" && dir != "-" {
				a.Sessions = &session.File{Dir: dir}
			}
			a.Verbose = envBool("AGENT_VERBOSE", false)
			a.Approver = park
			return a
		},
	})
	fmt.Fprintf(os.Stderr, "agent_go listen %s  POST /v1/runs  GET /healthz\n", *addr)
	if err := httpapi.ListenAndServe(ctx, *addr, h); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
