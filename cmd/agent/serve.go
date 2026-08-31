package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/httpapi"
	"github.com/asaqelee/agent_go/obs"
	"github.com/asaqelee/agent_go/run"
	"time"

	"github.com/asaqelee/agent_go/session"
)

func runServeCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defAddr := env("AGENT_HTTP_ADDR", "")
	if defAddr == "" {
		defAddr = catalog.HTTP.Addr
	}
	if defAddr == "" {
		defAddr = ":8080"
	}
	addr := fs.String("addr", defAddr, "listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	mcpTools, stopMCP := startMCP(ctx)
	defer stopMCP()
	provider, mem, _ := buildProviderAndMemory()
	docsRoot := resolveDocsRoot()
	park := httpapi.NewPark()
	var ch channel.Channel = &channel.Memory{}
	if u := env("AGENT_CHANNEL_WEBHOOK", ""); u != "" {
		ch = &channel.Webhook{URL: u}
	}
	reg := run.NewRegistry()
	if ms := envInt("AGENT_RUN_TIMEOUT_MS", 180_000); ms > 0 {
		reg.Timeout = time.Duration(ms) * time.Millisecond
	}
	sessDir := env("AGENT_SESSION_DIR", ".agent_sessions")

	h := httpapi.Handler(httpapi.Deps{
		Park:       park,
		Runs:       reg,
		Metrics:    obs.NewMetrics(),
		Channel:    ch,
		SessionDir: sessDir,
		Token:      env("AGENT_HTTP_TOKEN", ""),
		NewAgent: func(sessionID string) *agent.Agent {
			a := newSyncAgent(provider, mem, docsRoot, mcpTools)
			attachRoster(a, docsRoot)
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
	fmt.Fprintf(os.Stderr, "agent_go listen %s  POST /v1/runs  POST /v1/messages  GET /healthz /metrics\n", *addr)
	if err := httpapi.ListenAndServe(ctx, *addr, h); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
