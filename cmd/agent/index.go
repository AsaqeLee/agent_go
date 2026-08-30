package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/rag"
)

func runIndexCLI(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("index", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	root := fs.String("root", "", "docs root (default AGENT_DOCS_ROOT or examples/kb)")
	out := fs.String("out", "", "index JSON path (default AGENT_INDEX_PATH or .agent_index.json)")
	useHash := fs.Bool("hash", false, "use offline HashEmbedder instead of /v1/embeddings")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	docs := *root
	if docs == "" {
		docs = resolveDocsRoot()
	}
	if docs == "" {
		fmt.Fprintln(os.Stderr, "error: no docs root (pass --root or set AGENT_DOCS_ROOT)")
		return 2
	}
	path := *out
	if path == "" {
		path = indexPath()
	}

	var embed llm.Embedder
	if *useHash {
		embed = rag.HashEmbedder{}
		fmt.Fprintln(os.Stderr, "index: using HashEmbedder (offline; not a production embedding)")
	} else {
		p := newProvider()
		p.EmbedModel = env("OPENAI_EMBED_MODEL", "text-embedding-3-small")
		embed = p
		fmt.Fprintf(os.Stderr, "index: embeddings model=%s base=%s\n", p.EmbedModel, p.BaseURL)
	}

	idx, err := rag.Build(ctx, docs, embed, rag.ChunkOpts{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if *useHash {
		idx.Model = "hash"
	} else if p, ok := embed.(*llm.OpenAI); ok && p.EmbedModel != "" {
		idx.Model = p.EmbedModel
	}
	if err := idx.Save(path); err != nil {
		fmt.Fprintf(os.Stderr, "error: save %s: %v\n", path, err)
		return 1
	}
	fmt.Printf("wrote %s chunks=%d dim=%d\n", path, len(idx.Items), idx.Dim)
	return 0
}

func newProvider() *llm.OpenAI {
	p := llm.NewOpenAI(
		env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		env("OPENAI_API_KEY", ""),
		env("OPENAI_MODEL", "gpt-4o-mini"),
	)
	p.EmbedModel = env("OPENAI_EMBED_MODEL", "text-embedding-3-small")
	p.MaxRetries = envInt("AGENT_LLM_MAX_RETRIES", llm.DefaultMaxRetries)
	return p
}
