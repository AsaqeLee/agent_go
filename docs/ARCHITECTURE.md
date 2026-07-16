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
        messages.append(tool message with call id)
return error: max turns exceeded   # history unchanged
```

`Agent.Reset()` clears history for a new session. CLI: `/new`, `/history`.

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
