# 学习指南

## 五个基础概念

### 1. Chat ≠ Agent

| | Chat 补全 | Agent |
|---|---|---|
| 调用次数 | 1 | 循环多次 |
| 能力 | 只说话 | 说话 + 调工具 |
| 状态 | 无 | 消息历史在增长 |

### 2. 三块积木

- **LLM**：决定说什么，或调哪个工具  
- **Tool**：真正执行（算数、时间、业务 API…）  
- **Loop**：把工具结果塞回对话，再问 LLM  

### 3. 消息角色

`system` / `user` / `assistant` / `tool` —— 见 [ARCHITECTURE.md](./ARCHITECTURE.md)。

### 4. Agent Loop

模型返回 `tool_calls` 时，**不能**当最终答案；必须执行工具再继续。

### 5. Tool = 说明书 + 执行器

发给模型的是 JSON Schema 定义；本地实现的是 `Run(argsJSON) string`。

## 推荐练习

1. ~~把 `messages` 提到 `Agent` 上，支持多轮对话~~（已完成：`history` + `Reset` / `/new`）  
2. 新增工具 `word_count`，数一段文字的词数  
3. 改 system prompt，强制数学必须用 `calculator`  
4. 设 `MaxTurns=1`，观察工具链中断  
5. 给工具结果做长度截断，防止撑爆 context  

## 读源码顺序

见 [ARCHITECTURE.md § Reading order](./ARCHITECTURE.md#reading-order-learn-the-code)。
