# 示例知识库

本目录是 **本地文档问答** 场景的示例语料，供 `list_docs` / `read_doc` / `search_docs` 使用。

设置：

```bash
export AGENT_DOCS_ROOT=examples/kb
# 或在仓库根目录运行时，CLI 会在未设置时自动尝试 examples/kb
go run ./cmd/agent
```

示例提问：

- 年假有多少天？请先搜索知识库再回答
- onboarding 第一天要做什么？
- VPN 怎么连？

安全说明：工具只能访问 `AGENT_DOCS_ROOT` 沙箱内相对路径，禁止 `..` 逃逸。

检索评测（`eval.json`，grep 为基线）：

```bash
go run ./cmd/agent eval --retriever grep --root examples/kb
go run ./cmd/agent index --root examples/kb --hash --out .agent_index.json
go run ./cmd/agent eval --retriever vector --hash --index .agent_index.json --cases examples/kb/eval.json
```
