# agent_go

[![CI](https://github.com/asaqelee/agent_go/actions/workflows/ci.yml/badge.svg)](https://github.com/asaqelee/agent_go/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev/)

用 **纯 Go 标准库** 实现的 AI Agent 运行时：可读、可跑、可嵌进产品。

> **Agent = LLM（大脑）+ Tools（手脚）+ Loop（调度循环）**  
> RAG / MCP / HTTP / 任务落盘都是插在现有 seam 上的 adapter，不进 loop。

内核仍适合当教材；检索、MCP、HTTP、追踪、handoff 按需打开。

---

## 特性

| 能力 | 说明 |
|------|------|
| **Agent Loop** | `LLM → tool_calls → 执行 → 回写 → 再 LLM`，`MaxTurns` 防死循环 |
| **多轮会话** | 跨 `Run` 保留历史；`Reset` / CLI `/new` |
| **工具结果截断** | 写入 history 前按 rune 上限裁剪（默认 4096） |
| **会话裁剪 + 有损摘要** | 按完整 user-turn 裁；`[conversation_summary]` 有界 bullets |
| **轨迹折叠** | 仅最近 N 轮保留完整 tool 链 |
| **结构化 Memory** | `name` / `likes` / `notes` 由 LLM 经 `profile_update` 写入；落盘 + `[user_profile]` |
| **本地知识库** | `list_docs` / `search_docs` / `read_doc`，目录沙箱；`search_docs` 走 `retrieve.Retriever` |
| **向量 RAG** | `rag`：切分 + `/v1/embeddings`（或离线 hash）+ JSON 余弦索引；`agent index` / `agent eval` |
| **MCP** | stdio JSON-RPC 客户端；工具名 `server__tool`；默认拒绝，allowlist 放行；写工具需审批 |
| **流式输出** | OpenAI 兼容 SSE；CLI 打 token；`POST /v1/runs` `stream:true` |
| **会话落盘** | `session.Store`：原子 JSON；跨进程恢复 history |
| **异步任务** | 队列 + Worker；`AGENT_TASK_STORE` 原子 JSON，重启可恢复 |
| **HTTP** | `agent serve`：`POST /v1/runs`、SSE、`POST /v1/approvals/{id}` |
| **可观测** | usage + JSONL span（`agent.run` / `llm.chat` / `tool.*`） |
| **Handoff** | `handoff` 工具把子任务交给 specialist（默认 KB `docs`） |
| **插件目录** | `agent.json` / `agent plugin list`：retriever、MCP、specialists |
| **OpenAI 兼容** | 官方 API / Ollama / DeepSeek 等 `/v1/chat/completions` + `/embeddings` |
| **零第三方依赖** | 仅 `net/http` + 标准库 |
| **可测** | mock Provider 覆盖 loop / 工具链 / MaxTurns / 知识库沙箱；httptest 覆盖 Provider |
| **用量可观测** | 解析兼容端点的 `usage`；`LastUsage` / `SessionUsage`；CLI `/usage` |
| **瞬时重试** | 429 / 5xx / 传输错误有界重试（默认 2 次，可关） |
| **任务背压** | 队列满时 `Submit` 立即失败，不阻塞 |
| **同轮并行工具** | 同一 `tool_calls` 批次 fan-out/join；结果按 call 顺序回写；Memory 互斥 |

## 仓库结构

```
.
├── agent/           # Loop、会话策略、Memory、handoff
├── llm/             # Provider + Streamer + Embedder
├── tool/            # Tool 契约、内置工具、KB、ExecResult / Policy
├── retrieve/        # Retriever 接口 + grep adapter
├── rag/             # 切分、向量索引、评测
├── mcp/             # MCP stdio client / 测试用 server
├── session/         # 会话 Store
├── task/            # 异步任务 + 持久化 Store
├── httpapi/         # POST /v1/runs SSE
├── obs/             # JSONL tracer
├── plugin/          # agent.json 目录
├── cmd/agent/       # CLI：chat / index / eval / serve / plugin / task
├── examples/kb/     # 语料 + eval.json
├── examples/mcp-echo/
└── docs/
```

## 快速开始

### 要求

- Go 1.22+
- 任意 OpenAI 兼容接口的 API Key（或本机 Ollama，且模型支持 function calling）

### 安装 / 运行

```bash
git clone https://github.com/asaqelee/agent_go.git
cd agent_go

# 配置方式二选一：
# 1) 复制 .env.example → .env 后编辑（启动时自动加载，不覆盖已 export 的变量）
cp .env.example .env
# 编辑 .env 填入 OPENAI_API_KEY 等

# 2) 或直接 export
# export OPENAI_API_KEY=sk-...
# export OPENAI_MODEL=gpt-4o-mini

# 单次提问
go run ./cmd/agent "现在几点？请用工具查"
go run ./cmd/agent "帮我算 123 * 456"

# 交互模式（多轮 + 知识库 + 异步任务）
# 仓库根目录运行时，若未设 AGENT_DOCS_ROOT，会自动尝试 examples/kb
go run ./cmd/agent
# 试：年假有多少天？请先 search_docs 再回答
# /task submit 帮我算 12*34
# /task list
# /memory
# /usage

# 异步任务（默认落盘 .agent_tasks.json）
go run ./cmd/agent task submit "帮我算 123 * 456"

# 检索评测 / 向量索引
go run ./cmd/agent eval --retriever grep --root examples/kb
go run ./cmd/agent index --root examples/kb --hash
go run ./cmd/agent eval --retriever vector --hash --cases examples/kb/eval.json

# HTTP
go run ./cmd/agent serve --addr :8080
# POST /v1/runs  {"input":"...","session_id":"s1","stream":true}

# 插件目录
go run ./cmd/agent plugin list

# 编译二进制
go build -o bin/agent ./cmd/agent
./bin/agent "2 的 10 次方用计算器算"
```

### 知识库 Demo

```bash
# 可选显式指定（默认同目录下 examples/kb 存在即启用）
export AGENT_DOCS_ROOT=examples/kb
go run ./cmd/agent "入职满一年年假几天？请查知识库"
```

语料、沙箱与 `eval.json` 见 [examples/kb/README.md](examples/kb/README.md)。  
向量检索：`AGENT_RETRIEVER=vector` 且已 `agent index`。

### MCP

复制 [mcp.json.example](mcp.json.example) 为 `mcp.json`（或写入 `agent.json` 的 `mcp.servers`）。默认 **deny**：`allow` 为空则不注册任何远程工具。写工具会在 CLI 询问 `y/N`，HTTP 走 `POST /v1/approvals/{id}`。`AGENT_MCP_AUTO_APPROVE=true` 跳过审批（仅本地）。

### 使用 Ollama

```bash
export OPENAI_BASE_URL=http://localhost:11434/v1
export OPENAI_API_KEY=ollama
export OPENAI_MODEL=qwen2.5:7b   # 需支持 function calling

go run ./cmd/agent "帮我算 12 * 34"
```

### 环境变量

支持 **shell export** 与项目根目录 **`.env`**（零依赖解析；文件不存在则跳过）。  
优先级：**已有环境变量 > `.env` > 代码默认值**。

| 变量 | 默认 | 说明 |
|------|------|------|
| `OPENAI_API_KEY` | _(空)_ | API Key |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | 兼容端点 |
| `OPENAI_MODEL` | `gpt-4o-mini` | 模型名 |
| `AGENT_VERBOSE` | `true` | 打印每一轮 tool call |
| `AGENT_MAX_HISTORY_MESSAGES` | `40`（CLI） | 会话消息上限；`0` 不限制 |
| `AGENT_MEMORY_PATH` | `.agent_memory.json` | 档案 JSON 路径；空字符串关闭落盘 |
| `AGENT_KEEP_RECENT_FULL_TURNS` | `1` | 保留完整 tool 轨迹的最近轮数；`-1` 关闭折叠 |
| `AGENT_DISABLE_LLM_SUMMARY` | `false` | `true` 时 trim 摘要不做 LLM 压缩 |
| `AGENT_DOCS_ROOT` | 自动 `examples/kb`（若存在） | 本地知识库根目录；无效则禁用 KB 工具 |
| `AGENT_RETRIEVER` | `grep` | `grep` 或 `vector` |
| `AGENT_INDEX_PATH` | `.agent_index.json` | 向量索引 |
| `OPENAI_EMBED_MODEL` | `text-embedding-3-small` | `/v1/embeddings` 模型 |
| `AGENT_SESSION_DIR` | `.agent_sessions` | 会话 JSON 目录；`off` 关闭 |
| `AGENT_STREAM` | `true` | 尝试 SSE 流式 |
| `AGENT_TASK_STORE` | `.agent_tasks.json` | 任务落盘；`off` 仅内存 |
| `AGENT_TASK_WORKERS` | `2` | 异步任务 worker 数 |
| `AGENT_TASK_QUEUE` | `64` | 异步任务队列容量；满则 `Submit` 立刻失败 |
| `AGENT_LLM_MAX_RETRIES` | `2` | Chat 瞬时失败额外重试次数；`-1` 关闭 |
| `AGENT_TOOL_TIMEOUT_MS` | `60000` | 单次工具超时；`0` 关闭 |
| `AGENT_TOOL_CONCURRENCY` | `8` | 同轮工具并发上限 |
| `AGENT_OTEL_JSONL` | _(空)_ | span JSONL 路径 |
| `AGENT_MCP_CONFIG` | 若存在则 `mcp.json` | MCP 服务器列表 |
| `AGENT_CONFIG` | 若存在则 `agent.json` | 插件目录 |
| `AGENT_HTTP_ADDR` | `:8080` | `agent serve` 监听地址 |

## 作为库使用

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
	// 第二个参数为知识库根目录；"" 表示不挂载 list/read/search_docs
	a := &agent.Agent{
		Provider: llm.NewOpenAI("", os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini"),
		Memory:   mem,
		Tools:    tool.DefaultTools(mem, "examples/kb"),
		MaxTurns: 8,
	}
	out, err := a.Run(context.Background(), "现在几点？")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(out)
}
```

> 若你 fork 到其它路径，请把 `go.mod` 里的 `module` 与 import 路径改成你的仓库地址。

## 开发

```bash
go test ./...
go vet ./...
go build -o bin/agent ./cmd/agent
```

## 阅读源码顺序

1. `llm/types.go` — 消息与接口  
2. `tool/tool.go` — 工具契约  
3. `retrieve/retrieve.go` · `tool/docs.go` — 检索 seam  
4. **`agent/agent.go`** — **Loop（最重要）**  
5. `mcp/bridge.go` · `rag/vector.go` — 能力 adapter  
6. `httpapi/server.go` — 对外 HTTP  
7. `cmd/agent/main.go` — CLI 组装  

详情见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) 与 [docs/LEARNING.md](docs/LEARNING.md)。

## 设计原则

| 做 | 不做 |
|----|------|
| 标准库、接口清晰 | 用 LangChain 类框架当内核 |
| RAG / MCP 作为 Tool / Retriever adapter | 在 `agent.Run` 里写死向量或协议 |
| 可 mock 的 Provider | 把检索策略泄漏给所有调用方 |
| MaxTurns / 路径沙箱 / 结果截断 / 审批 | 无 allowlist 地把 MCP 工具全塞给模型 |

先掌握 **loop + tools + messages + 边界**，再插 adapter。
