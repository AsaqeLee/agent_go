# agent_go

[![CI](https://github.com/asaqelee/agent_go/actions/workflows/ci.yml/badge.svg)](https://github.com/asaqelee/agent_go/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev/)

用 **纯 Go 标准库** 实现的最小 AI Agent：可读、可跑、可改。

> **Agent = LLM（大脑）+ Tools（手脚）+ Loop（调度循环）**

适合作为学习材料：理解 tool calling 与 agent loop 后，再去读 Dive / SwarmGo 等更大项目会轻松很多。

---

## 特性

- **完整 Agent Loop**：`LLM → tool_calls → 执行 → 回写 → 再 LLM`
- **多轮会话**：`Agent` 跨 `Run` 保留历史；`Reset` / CLI `/new` 开新会话
- **工具结果截断**：写入 history 前按 rune 上限裁剪（默认 4096），防止撑爆 context
- **会话裁剪 + 有损摘要**：`MaxHistoryMessages` 按轮裁剪；`[conversation_summary]` 为有界 bullet
- **结构化 Memory**：`name` / `likes` / `notes` 由 LLM 通过 `profile_update`（或 `memory_set`）填 JSON 字段写入，**不用正则抽字段**；注入 `[user_profile]`，不随 trim 丢失；`/memory`
- **OpenAI 兼容**：官方 API / Ollama / DeepSeek / 任意 `/v1/chat/completions`
- **零第三方依赖**：仅 `net/http` + 标准库
- **教学用内置工具**：`get_time` · `calculator` · `echo_note`
- **可测**：mock Provider 覆盖 loop / 工具链 / MaxTurns
- **中文学习文档**：[docs/LEARNING.md](docs/LEARNING.md) · [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)

## 仓库结构

```
.
├── agent/           # Agent loop（核心）
├── llm/             # 消息类型 + OpenAI 兼容 Provider
├── tool/            # Tool 接口、注册表、内置工具
├── cmd/agent/       # CLI 入口
├── docs/            # 架构与学习指南
├── .github/workflows/ci.yml
└── README.md
```

## 快速开始

### 要求

- Go 1.22+
- 任意 OpenAI 兼容接口的 API Key（或本机 Ollama）

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

# 交互模式（多轮会话；/new 清空，/history 查看）
go run ./cmd/agent

# 编译二进制
go build -o bin/agent ./cmd/agent
./bin/agent "2 的 10 次方用计算器算"
```

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
	a := &agent.Agent{
		Provider: llm.NewOpenAI("", os.Getenv("OPENAI_API_KEY"), "gpt-4o-mini"),
		Memory:   mem,
		Tools:    tool.DefaultTools(mem),
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
3. `tool/builtin.go` — 工具示例  
4. **`agent/agent.go`** — **Loop（最重要）**  
5. `llm/openai.go` — HTTP 适配  
6. `cmd/agent/main.go` — CLI 组装  

详情见 [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) 与 [docs/LEARNING.md](docs/LEARNING.md)。

## 设计原则

| 做 | 不做（刻意） |
|----|-------------|
| 标准库、接口清晰 | 流式输出、向量库、MCP |
| 可 mock 的 Provider | 多 Agent handoff |
| MaxTurns 防死循环 | 权限沙箱 / 审批 UI |
| 中文学习文档 | 企业级可观测全家桶 |

先掌握 **loop + tools + messages**，再扩展其它能力。
