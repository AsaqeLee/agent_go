package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/rag"
	"github.com/asaqelee/agent_go/retrieve"
)

func runEvalCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	kind := fs.String("retriever", "", "grep or vector (default AGENT_RETRIEVER or grep)")
	casesPath := fs.String("cases", "", "eval JSON (default <docs>/eval.json)")
	root := fs.String("root", "", "docs root for grep")
	indexFile := fs.String("index", "", "vector index JSON")
	k := fs.Int("k", 8, "top-k")
	useHash := fs.Bool("hash", false, "vector query embeddings via HashEmbedder")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	docs := *root
	if docs == "" {
		docs = resolveDocsRoot()
	}
	gold := *casesPath
	if gold == "" && docs != "" {
		gold = filepath.Join(docs, "eval.json")
	}
	if gold == "" {
		fmt.Fprintln(os.Stderr, "error: pass --cases")
		return 2
	}
	cases, err := rag.LoadCases(gold)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	rkind := *kind
	if rkind == "" {
		rkind = retrieverKind()
	}

	var retr retrieve.Retriever
	switch rkind {
	case "grep":
		if docs == "" {
			fmt.Fprintln(os.Stderr, "error: grep eval needs --root")
			return 2
		}
		abs, err := filepath.Abs(docs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		retr = retrieve.NewGrep(abs, retrieve.GrepLimits{})
	case "vector":
		ip := *indexFile
		if ip == "" {
			ip = indexPath()
		}
		idx, err := rag.Load(ip)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: load index: %v\n", err)
			return 1
		}
		var embed llm.Embedder
		if *useHash {
			embed = rag.HashEmbedder{}
		} else {
			embed = newProvider()
		}
		retr = &rag.Vector{Index: idx, Embed: embed}
	default:
		fmt.Fprintf(os.Stderr, "error: unknown retriever %q (grep|vector)\n", rkind)
		return 2
	}

	rep := rag.Evaluate(ctx, retr, cases, *k)
	fmt.Println(rep.Format())
	for _, it := range rep.Items {
		mark := "HIT"
		if !it.Hit {
			mark = "MISS"
		}
		top1 := "-"
		if it.Top1 {
			top1 = "top1"
		}
		fmt.Printf("  %-5s %-5s %-16s path=%s score=%.3f %s\n", mark, top1, it.ID, it.Path, it.Score, it.Reason)
	}
	if rep.Hits < rep.Total {
		return 1
	}
	return 0
}
