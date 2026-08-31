# Production notes

This runtime is a **single-process** Agent core. The seams are intentional; the adapters are not a HA platform.

| Today | If this were production |
|-------|-------------------------|
| Session files under `.agent_sessions/` | Redis / SQL with per-session lock |
| Run registry in memory | Shared store + worker lease; cancel via context propagated over RPC |
| Task store = atomic JSON | SQLite / queue (SQS, RocketMQ) |
| JSONL spans | OTLP traces + request_id in every log line |
| MCP stdio JSON-RPC subset | Official SDK, SSE/HTTP transports, secret vault |
| Channel webhook (Feishu-shaped JSON) | Real IM adapter with retry, idempotency, mention/card |
| Model router on one OpenAI-compatible URL | Gateway (豆包 / Ark / internal) with quota and circuit breaker |
| Same-session second Run → 409 | Optional fair queue with timeout |

## How to debug a failed HTTP Run

1. Read `X-Request-Id` from the response header (or the `request_id` field on `/healthz` for that request).
2. `GET /v1/runs/{run_id}` for status, error, usage.
3. `GET /metrics` for `agent_runs_failed`, `agent_chat_errors`, `agent_runs_in_flight`.
4. If `AGENT_OTEL_JSONL` is set, grep that file for `request_id` / `trace_id`.

A cancelled Run is `status=cancelled` (client `POST /v1/runs/{id}/cancel` or parent context done). A 409 means the session still has a running Run — wait or cancel it.
