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
| `llm` | Model types + OpenAI-compatible HTTP provider |
| `tool` | Tool interface, registry, built-ins, **sandboxed knowledge-base tools** |
| `agent` | The loop that schedules LLM + tools; session trim/fold/summary; structured Memory |
| `task` | In-process async job queue + workers |
| `cmd/agent` | Thin CLI wiring (`AGENT_DOCS_ROOT`, `/task`, `/memory`) |
| `examples/kb` | Sample policy/FAQ corpus for the vertical demo |

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
    for each call in reply.tool_calls:
        result = tools.Execute(call)
        result = cap(result, MaxToolResultChars)  # default 4096 runes
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
| **Local knowledge base** | `list_docs` / `search_docs` / `read_doc` rooted at `AGENT_DOCS_ROOT` (or auto `examples/kb`). Paths are sandboxed (no `..` / absolute escape). Read and search are capped. **Not** vector RAG—tool-mediated file access for a vertical demo. |

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
3. [`tool/builtin.go`](../tool/builtin.go) · [`tool/docs.go`](../tool/docs.go) — concrete + KB tools
4. **[`agent/agent.go`](../agent/agent.go)** — the loop (most important)
5. [`agent/memory.go`](../agent/memory.go) — structured profile
6. [`llm/openai.go`](../llm/openai.go) — HTTP to `/v1/chat/completions`
7. [`task/task.go`](../task/task.go) — minimal async manager
8. [`cmd/agent/main.go`](../cmd/agent/main.go) — CLI assembly

## Intentionally out of scope

Streaming, durable task DB across processes, **vector** RAG, multi-agent handoffs, MCP, and permission UIs.
Path sandboxing for the local docs root **is** in scope (minimal, intentional).
Master the loop first; those other items are plugins on top.
