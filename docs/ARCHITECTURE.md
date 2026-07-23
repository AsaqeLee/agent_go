# Architecture

## What is an agent here?

```
Chat completion:  user → LLM → text

Agent:            user → LLM ⇄ tools → … → text
                       ↑____________|
                         agent loop
```

This repository implements the second shape with three packages:

| Package | Role |
|---------|------|
| `llm` | Model types + OpenAI-compatible HTTP provider |
| `tool` | Tool interface, registry, built-in demos |
| `agent` | The loop that schedules LLM + tools |
| `cmd/agent` | Thin CLI wiring |

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
| **Structured Memory** | Fields `name` / `likes[]` / `notes[]`. **LLM writes fields via tool JSON** (`profile_update` multi-field, or `memory_set` one field). Runtime does **not** regex-parse free text into name/likes. `echo_note` only appends notes. Injected each Chat as `[user_profile]`. Survives trim and `/new`. |
| `Stats()` / `/history` / `/memory` | Session size + profile field dump |CLI default: `AGENT_MAX_HISTORY_MESSAGES=40` (override via env / `.env`).

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
3. [`tool/builtin.go`](../tool/builtin.go) — concrete tools
4. **[`agent/agent.go`](../agent/agent.go)** — the loop (most important)
5. [`llm/openai.go`](../llm/openai.go) — HTTP to `/v1/chat/completions`
6. [`cmd/agent/main.go`](../cmd/agent/main.go) — CLI assembly

## Intentionally out of scope

Streaming, long-term memory / RAG, multi-agent handoffs, MCP, sandboxes, and permission UIs.
Master the loop first; those are plugins on top.
