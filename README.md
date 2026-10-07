# agent_go

[![CI](https://github.com/AsaqeLee/agent_go/actions/workflows/ci.yml/badge.svg)](https://github.com/AsaqeLee/agent_go/actions/workflows/ci.yml)

Pure Go **standard-library** AI agent runtime: readable, runnable, and embeddable. The core loop is intentional teaching material; RAG, MCP, HTTP, tracing, and handoff attach as adapters at existing seams—they do not live inside the loop.

> **Agent = LLM + Tools + Loop**  
> RAG / MCP / HTTP / task persistence are adapters.

Module path: `github.com/asaqelee/agent_go` (clone from `AsaqeLee/agent_go`).

## Features / scope

| Capability | Notes |
|------------|-------|
| Agent loop | `LLM → tool_calls → execute → append → LLM`, bounded by `MaxTurns` |
| Multi-turn sessions | History across `Run`; `Reset` / CLI `/new` |
| Tool-result truncation | Rune limit before history write (default 4096) |
| Trim + lossy summary | User-turn aware trim; bounded `[conversation_summary]` bullets |
| Trajectory folding | Keep full tool chains for the last N turns only |
| Structured memory | `name` / `likes` / `notes` via `profile_update`; disk + `[user_profile]` |
| Local knowledge base | `list_docs` / `search_docs` / `read_doc` with directory sandbox |
| Vector RAG | Chunking + `/v1/embeddings` (or offline hash) + JSON cosine index; `agent index` / `agent eval` |
| MCP | stdio JSON-RPC client; names `server__tool`; deny-by-default allowlist; write tools need approval |
| Streaming | OpenAI-compatible SSE; CLI tokens; `POST /v1/runs` with `stream:true` |
| Session store | Atomic JSON under `session.Store` |
| Async tasks | Queue + workers; `AGENT_TASK_STORE` JSON recovery |
| HTTP serve | `agent serve`: runs (sync/async/SSE), cancel, messages, `/healthz`, `/metrics` |
| Observability | Usage (incl. failed attempts), `X-Request-Id`, JSONL spans, `/metrics` |
| Handoff | `handoff` tool to specialists (default KB `docs`) |
| Plugin catalog | `agent.json` / `agent plugin list` |
| OpenAI-compatible APIs | Official API, Ollama, DeepSeek, etc. |
| Zero third-party deps | `net/http` + stdlib only |
| Parallel tools | Same-turn fan-out/join with ordered write-back |

## Requirements

- Go 1.22+
- An OpenAI-compatible API key (or local Ollama with function-calling support)

## Getting started

```bash
git clone https://github.com/AsaqeLee/agent_go.git
cd agent_go

cp .env.example .env
# edit OPENAI_API_KEY / OPENAI_MODEL, or export them

go run ./cmd/agent "What time is it? Use a tool."
go run ./cmd/agent                 # interactive
go run ./cmd/agent serve --addr :8080
go build -o bin/agent ./cmd/agent
```

Priority for configuration: **existing environment variables > `.env` > code defaults**.

### Knowledge base demo

```bash
export AGENT_DOCS_ROOT=examples/kb   # optional; auto-detected if present
go run ./cmd/agent "How many annual leave days after one year? Search the docs."
```

### MCP

Copy [`mcp.json.example`](mcp.json.example) to `mcp.json` (or set `mcp.servers` in `agent.json`). Default policy is deny. Write tools prompt in the CLI (`y/N`) or use `POST /v1/approvals/{id}` over HTTP. `AGENT_MCP_AUTO_APPROVE=true` skips approval (local only).

### Ollama

```bash
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_API_KEY=ollama
export OPENAI_MODEL=qwen2.5:7b   # must support function calling
go run ./cmd/agent "Compute 12 * 34"
```

### Selected environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `OPENAI_API_KEY` | _(empty)_ | API key |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | Compatible base URL |
| `OPENAI_MODEL` | `gpt-4o-mini` | Chat model |
| `AGENT_DOCS_ROOT` | auto `examples/kb` | KB root |
| `AGENT_RETRIEVER` | `grep` | `grep` or `vector` |
| `AGENT_SESSION_DIR` | `.agent_sessions` | Session JSON dir; `off` disables |
| `AGENT_TASK_STORE` | `.agent_tasks.json` | Task persistence; `off` = memory |
| `AGENT_HTTP_ADDR` | `:8080` | `agent serve` listen address |
| `AGENT_MCP_CONFIG` | `mcp.json` if present | MCP servers |
| `AGENT_CONFIG` | `agent.json` if present | Plugin catalog |

See the previous Chinese README history and `.env.example` for the full list.

## Library usage

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func main() {
	mem := agent.NewMemory()
	a := &agent.Agent{
		Provider: llm.NewOpenAI("", os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini"),
		Memory:   mem,
		Tools:    tool.DefaultTools(mem, "examples/kb"),
		MaxTurns: 8,
	}
	out, err := a.Run(context.Background(), "What time is it?")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
}
```

If you fork under another module path, update `go.mod` and imports accordingly.

## Project layout

```text
agent/      # loop, session policy, memory, handoff
llm/        # provider, streamer, embedder
tool/       # tool contract, builtins, KB
retrieve/   # Retriever + grep adapter
rag/        # chunking, vector index, eval
mcp/        # stdio MCP client
session/    # session store
task/       # async tasks
httpapi/    # HTTP API
cmd/agent/  # CLI
examples/
docs/
```

## Development

```bash
go test ./...
go vet ./...
go build -o bin/agent ./cmd/agent
```

Suggested source reading order: `llm/types.go` → `tool/tool.go` → `agent/agent.go` → MCP/RAG adapters → `httpapi` → `cmd/agent`. See [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) and [`docs/LEARNING.md`](docs/LEARNING.md).

## Status / limitations

Educational / experimental runtime. Deliberately **not** multi-instance HA, full IM platform integration, or Kafka. Single-process implementations prove the seams; production boundaries and troubleshooting notes live in [`docs/PRODUCTION.md`](docs/PRODUCTION.md). No root LICENSE file is present at the time of this rewrite.
