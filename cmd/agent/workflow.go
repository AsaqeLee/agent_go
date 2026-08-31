package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"strings"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/rag"
	"github.com/asaqelee/agent_go/retrieve"
	"github.com/asaqelee/agent_go/workflow"
)

func runWorkflowCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("workflow", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "docs root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 || rest[0] != "run" {
		fmt.Fprintln(os.Stderr, "usage: agent workflow run [-root dir] <question>")
		return 2
	}
	query := strings.Join(rest[1:], " ")
	if query == "" {
		fmt.Fprintln(os.Stderr, "error: empty question")
		return 2
	}
	docs := *root
	if docs == "" {
		docs = resolveDocsRoot()
	}
	if docs == "" {
		fmt.Fprintln(os.Stderr, "error: no docs root")
		return 2
	}
	abs, err := filepath.Abs(docs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	var retr retrieve.Retriever = retrieve.NewGrep(abs, retrieve.GrepLimits{})
	if retrieverKind() == "vector" {
		if idx, err := rag.Load(indexPath()); err == nil {
			retr = &rag.Vector{Index: idx, Embed: newProvider()}
		}
	}
	provider, mem, _ := buildProviderAndMemory()
	ch := &channel.Memory{}
	res, err := workflow.Run(ctx, query, retr, func(ctx context.Context, grounded string) (string, error) {
		a := &agent.Agent{
			Provider:     provider,
			Memory:       mem,
			SystemPrompt: "You answer only from the provided snippets. Cite paths. Be concise.",
			MaxTurns:     4,
			Verbose:      false,
		}
		return a.Run(ctx, grounded)
	}, ch)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: step=%s %v\n", res.Step, err)
		return 1
	}
	fmt.Printf("step=%s hits=%d\n", res.Step, len(res.Hits))
	for _, h := range res.Hits {
		fmt.Printf("  %s\t%s\n", h.Path, clip(h.Text, 80))
	}
	fmt.Println()
	fmt.Println(res.Answer)
	return 0
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
