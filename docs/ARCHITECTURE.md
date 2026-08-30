# Architecture

## What is an agent here?

```
Chat completion:  user → LLM → text

Agent:            user → LLM ⇄ tools → … → text
                       ↑____________|
                         agent loop
```

This repository implements the second shape with these packages:

| Package | Role |
|---------|------|
| `llm` | Model types + OpenAI-compatible HTTP provider (`Provider`, optional `Streamer`, `Embedder`) |
| `tool` | Tool interface, registry, built-ins, sandboxed KB tools, `ExecResult` / `Policy` |
| `retrieve` | `Retriever` seam; grep is the zero-dep adapter |
| `rag` | Chunking, vector index, eval gold runner |
| `mcp` | MCP stdio JSON-RPC client → `[]tool.Tool` |
| `session` | Durable chat history store |
| `agent` | The loop; trim/fold/summary; Memory; handoff roster |
| `task` | Async job queue + workers + optional file store |
| `httpapi` | `POST /v1/runs` (JSON or SSE) and approvals |
| `obs` | OTEL-shaped JSONL tracer |
| `plugin` | `agent.json` catalog |
| `cmd/agent` | CLI wiring |
| `examples/kb` | Sample corpus + `eval.json` |

## Loop (pseudocode)

```
# Multi-turn: history lives on Agent across Run() calls.
messages = copy(history) or [system]
messages.append(user)
for turn in 1..MaxTurns:
    reply = LLM.Chat(messages, tools)
    messages.append(reply)
    if reply has no tool_calls:
        history = messages   # commit only on success
        return reply.content
    # same-turn batch: fan-out, then join (stdlib goroutines + WaitGroup)
    start all reply.tool_calls concurrently   # each result is capped (default 4096 runes)
    wait for every call
    for each call in original order:          # not completion order
        messages.append(tool message with call id)
return error: max turns exceeded   # history unchanged
```

`Agent.Reset()` clears history for a new session. CLI: `/new`, `/history`.

Tool results are capped **before** they enter `messages` / session history so one fat log cannot blow the context window. Set `MaxToolResultChars < 0` to disable.

### Session context controls

| Knob | Meaning |
|------|---------|
| `MaxToolResultChars` | Cap each tool result (default 4096 runes) |
| `MaxHistoryMessages` | After each successful `Run`, drop **oldest complete user-turns** until `len(history) <= N` (0 = unlimited). A user-turn is `user` + following messages until the next `user`. Never splits `tool_calls` from their `tool` replies. |
| **Trim summary (lossy)** | Dropped turns are **not fully archived**. Only short high-signal facts become bullets in `[conversation_summary]` (≤12 bullets, ≤512 runes). |
| **Structured Memory** | Fields `name` / `likes[]` / `notes[]`. **LLM writes via tool JSON** (`profile_update` / `memory_set`). Optional disk: `.agent_memory.json`. Injected as `[user_profile]`. Survives trim and `/new`. |
| **Trajectory fold** | Keep only N newest user-turns with full tool chains (`KeepRecentFullTurns`, default 1); older turns collapse to `user` + final `assistant`. |
| **LLM summary (optional)** | After trim, extractive draft may be compressed via a tool-less LLM call when draft is large; failure falls back to extractive. |
| **Tool ctx** | `Tool.Run(ctx, args)` — cancellation propagates from Agent.Run. |
| `Stats()` / `/history` / `/memory` / `/usage` | Session size + profile dump + token usage |
| **Async tasks** | Package `task`: in-process queue + workers (`queued→running→succeeded\|failed\|cancelled`). Each job runs a **fresh** Agent. State is **process-local** (not a DB). A full queue **rejects** `Submit` immediately (`task: queue full`) instead of blocking. |
| **Token usage** | Provider parses optional `usage` from `/chat/completions`. Agent records last-run and session totals (committed only on successful `Run`; `Reset` clears them). CLI: `/usage`. |
| **Transient retries** | `llm.OpenAI` retries 429 / 5xx / transport errors (default 2 extra attempts). 4xx other than 429 is not retried. |
| **Same-turn parallel tools** | One assistant `tool_calls` batch fans out with goroutines and joins before the next Chat. Tool messages stay in **model call order**. Shared Memory writes are mutex-serialized (`profile_update` / `memory_set` / `echo_note`). A single call stays sequential (no extra goroutine). |
| **Local knowledge base** | `list_docs` / `search_docs` / `read_doc` rooted at `AGENT_DOCS_ROOT` (or auto `examples/kb`). Paths are sandboxed. `search_docs` calls `retrieve.Retriever` (grep by default; vector when `AGENT_RETRIEVER=vector` and an index exists). |
| **Streaming** | If `Agent.Stream` and the provider implements `llm.Streamer`, tokens go to `OnEvent`. |
| **Session store** | Optional `session.Store` saves history after a successful Run. |
| **MCP** | Optional stdio servers; tools named `{server}__{tool}`; empty allowlist denies all. |
| **Handoff** | `handoff` tool clones the parent Agent with a specialist prompt and tool allowlist. |

CLI default: `AGENT_MAX_HISTORY_MESSAGES=40` (override via env / `.env`).

```text
go run ./cmd/agent task submit "goal"     # submit + wait in one process
go run ./cmd/agent                        # /task … ; KB demo if examples/kb present
export AGENT_DOCS_ROOT=examples/kb        # optional explicit root
```

### Knowledge-base tools (`tool/docs.go`)

| Tool | Behavior |
|------|----------|
| `list_docs` | List text files under the root (optional relative subdir) |
| `search_docs` | Case-insensitive substring walk; hit/file caps |
| `read_doc` | Read one relative path; max bytes; UTF-8 only |

`tool.DefaultTools(store, docsRoot)` appends these only when `docsRoot` is non-empty and resolves to an existing directory.

## Message roles

| Role | Written by | Purpose |
|------|------------|---------|
| `system` | you | Persona / rules |
| `user` | end user | Question |
| `assistant` | model | Answer or `tool_calls` |
| `tool` | your runtime | Tool result (`tool_call_id` required) |

## Reading order (learn the code)

1. [`llm/types.go`](../llm/types.go) — messages, tools schema, `Provider`
2. [`tool/tool.go`](../tool/tool.go) — `Tool` contract + registry
3. [`retrieve/retrieve.go`](../retrieve/retrieve.go) — retrieval seam
4. **[`agent/agent.go`](../agent/agent.go)** — the loop (most important)
5. [`mcp/bridge.go`](../mcp/bridge.go) · [`rag/vector.go`](../rag/vector.go) — adapters
6. [`httpapi/server.go`](../httpapi/server.go) — HTTP/SSE
7. [`cmd/agent/main.go`](../cmd/agent/main.go) — CLI assembly

## Adapter seams (keep the loop small)

RAG, MCP, HTTP, tracing, and handoff sit **behind** `Retriever` / `Tool` / `OnEvent` / `session.Store`. They must not grow `Agent.Run`.

Still out of scope: embedding a third-party agent framework as the kernel, marketplace websites, multi-tenant IAM. Path sandboxing for the local docs root **is** in scope.
