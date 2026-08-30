// Command agent is a minimal CLI for the educational Go agent.
//
//	Agent = LLM + Tools + Loop + Memory + optional async tasks
//
// Sync usage:
//
//	go run ./cmd/agent "现在几点？"
//	go run ./cmd/agent                 # interactive REPL
//
// Async tasks (in-process queue + workers; state is not shared across processes):
//
//	go run ./cmd/agent task submit "调研并总结…"     # submit + wait
//	go run ./cmd/agent task submit --no-wait "…"     # print id (use interactive to poll)
//
// Interactive: quit | /new | /memory | /history | /task …
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/obs"
	"github.com/asaqelee/agent_go/plugin"
	"github.com/asaqelee/agent_go/rag"
	"github.com/asaqelee/agent_go/retrieve"
	"github.com/asaqelee/agent_go/session"
	"github.com/asaqelee/agent_go/task"
	"github.com/asaqelee/agent_go/tool"
)

const listPreviewRunes = 120

var catalog plugin.Config

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(os.Stderr, "dotenv: %v\n", err)
		os.Exit(1)
	}
	loadCatalog()

	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "task":
			os.Exit(runTaskCLI(ctx, os.Args[2:]))
		case "index":
			os.Exit(runIndexCLI(ctx, os.Args[2:]))
		case "eval":
			os.Exit(runEvalCLI(ctx, os.Args[2:]))
		case "serve":
			os.Exit(runServeCLI(ctx, os.Args[2:]))
		case "plugin":
			os.Exit(runPluginCLI(ctx, os.Args[2:]))
		}
	}

	mcpTools, stopMCP := startMCP(ctx)
	defer stopMCP()

	provider, mem, memPath := buildProviderAndMemory()
	docsRoot := resolveDocsRoot()
	a := newSyncAgent(provider, mem, docsRoot, mcpTools)
	attachRoster(a, docsRoot)
	attachSession(a)

	// One-shot sync question
	if len(os.Args) > 1 {
		question := strings.Join(os.Args[1:], " ")
		if err := ask(ctx, a, question); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Interactive: long-lived task manager in the same process.
	mgr := task.NewManager(ctx, func(taskCtx context.Context, goal string) (string, error) {
		// Fresh agent per task (empty chat history); shared durable memory path.
		mem2 := mem
		if memPath != "" {
			if m, err := agent.LoadMemory(memPath); err == nil {
				mem2 = m
			}
		}
		ag := newSyncAgent(provider, mem2, docsRoot, mcpTools)
		attachRoster(ag, docsRoot)
		ag.Verbose = envBool("AGENT_VERBOSE", false)
		return ag.Run(taskCtx, goal)
	}, task.Options{
		Workers:   envInt("AGENT_TASK_WORKERS", 2),
		QueueSize: envInt("AGENT_TASK_QUEUE", 64),
		Store:     resolveTaskStore(),
	})
	defer mgr.Stop()

	fmt.Println("agent_go — chat + memory + knowledge base + async tasks")
	fmt.Println("  chat: message | /new | /new all | /history [full] | /usage | /memory | /memory clear")
	fmt.Println("  task: /task submit|list|status|wait|cancel")
	fmt.Printf("model=%s base=%s max_history=%d memory=%s session=%s docs=%s retriever=%s workers=%d\n",
		provider.Model, provider.BaseURL, a.MaxHistoryMessages, memPath, displaySession(a), displayDocs(docsRoot), retrieverKind(), envInt("AGENT_TASK_WORKERS", 2))
	if docsRoot != "" {
		fmt.Println("  kb demo: try「年假有多少天？请先 search_docs 再回答」")
	}

	in := bufio.NewScanner(os.Stdin)
	// Allow long goals / pastes
	buf := make([]byte, 0, 64*1024)
	in.Buffer(buf, 1024*1024)

	for {
		fmt.Print("\n> ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		switch {
		case line == "quit" || line == "exit" || line == "q":
			return
		case line == "/new" || line == "/reset" || line == "/clear":
			a.Reset()
			fmt.Println("(chat cleared; profile memory kept — use /new all to wipe profile)")
			continue
		case line == "/new all" || line == "/reset all":
			a.ResetAll()
			fmt.Println("(chat + profile memory cleared)")
			continue
		case line == "/history" || line == "/history full":
			printHistory(a, line == "/history full")
			continue
		case line == "/usage":
			printUsage(a)
			continue
		case line == "/memory":
			printMemory(a)
			continue
		case line == "/memory clear":
			a.ResetMemory()
			fmt.Println("(profile memory cleared)")
			continue
		case line == "/task" || strings.HasPrefix(line, "/task "):
			handleInteractiveTask(ctx, mgr, strings.TrimSpace(strings.TrimPrefix(line, "/task")))
			continue
		}
		if err := ask(ctx, a, line); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func buildProviderAndMemory() (*llm.OpenAI, *agent.Memory, string) {
	provider := llm.NewOpenAI(
		env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		env("OPENAI_API_KEY", ""),
		env("OPENAI_MODEL", "gpt-4o-mini"),
	)
	memPath := env("AGENT_MEMORY_PATH", agent.DefaultMemoryPath)
	mem, err := agent.LoadMemory(memPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory load %s: %v\n", memPath, err)
		os.Exit(1)
	}
	if !mem.Empty() {
		fmt.Fprintf(os.Stderr, "loaded profile from %s (%s)\n", memPath, mem.ShortStatus())
	}
	return provider, mem, memPath
}

func newSyncAgent(provider *llm.OpenAI, mem *agent.Memory, docsRoot string, extra []tool.Tool) *agent.Agent {
	provider.MaxRetries = envInt("AGENT_LLM_MAX_RETRIES", llm.DefaultMaxRetries)
	provider.EmbedModel = env("OPENAI_EMBED_MODEL", "text-embedding-3-small")
	tools := tool.DefaultToolsWith(mem, docsRoot, resolveRetriever(docsRoot, provider))
	if len(extra) > 0 {
		tools = append(tools, extra...)
	}
	a := &agent.Agent{
		Provider:            provider,
		Memory:              mem,
		Tools:               tools,
		MaxTurns:            envInt("AGENT_MAX_TURNS", 8),
		MaxHistoryMessages:  envInt("AGENT_MAX_HISTORY_MESSAGES", 40),
		KeepRecentFullTurns: envInt("AGENT_KEEP_RECENT_FULL_TURNS", 1),
		DisableLLMSummary:   envBool("AGENT_DISABLE_LLM_SUMMARY", false),
		Verbose:             envBool("AGENT_VERBOSE", true),
	}
	if envBool("AGENT_MCP_AUTO_APPROVE", false) {
		a.Approver = allowAll{}
	} else {
		a.Approver = newStdinApprover()
	}
	a.Redact = tool.RedactSecrets
	if ms := envInt("AGENT_TOOL_TIMEOUT_MS", 60_000); ms > 0 {
		a.ToolTimeout = time.Duration(ms) * time.Millisecond
	}
	a.MaxToolConcurrency = envInt("AGENT_TOOL_CONCURRENCY", 8)
	if p := env("AGENT_OTEL_JSONL", ""); p != "" && p != "off" {
		if tr, err := obs.File(p); err != nil {
			fmt.Fprintf(os.Stderr, "otel jsonl: %v\n", err)
		} else {
			a.Tracer = tr
		}
	}
	if docsRoot != "" {
		// Explicit system prompt merges profile + sandboxed KB guidance.
		a.SystemPrompt = defaultPromptWithDocs(docsRoot)
	}
	return a
}

func defaultPromptWithDocs(docsRoot string) string {
	return strings.TrimSpace(`You are a helpful assistant with tools.
- Use tools when they help answer accurately (time, math, profile, local docs).
- Prefer calculator for arithmetic; do not guess multiplications.

Durable user profile (survives chat history trim):
- When the user states durable facts about themselves, extract fields and call profile_update (or memory_set).
- echo_note only appends free-text notes. Never invent profile data.
- Trust [user_profile] over older chat when they conflict.

Local knowledge base (sandboxed directory at ` + docsRoot + `):
- Tools: list_docs, search_docs, read_doc. Always search or list before answering policy/FAQ questions from docs.
- Cite file paths from tool results. If docs lack the answer, say you do not know from the knowledge base.
- Never attempt path traversal; only relative paths under the docs root.

- After tools return, give a concise final answer to the user.
- Reply in the same language the user uses.`)
}

// resolveDocsRoot returns AGENT_DOCS_ROOT, or examples/kb if present in cwd.
func resolveDocsRoot() string {
	if v := strings.TrimSpace(os.Getenv("AGENT_DOCS_ROOT")); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			return v
		}
		fmt.Fprintf(os.Stderr, "warning: AGENT_DOCS_ROOT=%q not usable, kb tools disabled\n", v)
		return ""
	}
	if st, err := os.Stat("examples/kb"); err == nil && st.IsDir() {
		return "examples/kb"
	}
	return ""
}

func resolveTaskStore() task.Store {
	path := env("AGENT_TASK_STORE", ".agent_tasks.json")
	if path == "" || path == "off" || path == "-" {
		return nil
	}
	return &task.FileStore{Path: path}
}

func loadCatalog() {
	p := plugin.LookupDefault()
	if p == "" {
		return
	}
	cfg, err := plugin.Load(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "catalog %s: %v\n", p, err)
		os.Exit(1)
	}
	catalog = cfg
	fmt.Fprintf(os.Stderr, "loaded catalog %s\n", p)
}

func retrieverKind() string {
	if v := os.Getenv("AGENT_RETRIEVER"); v != "" {
		return v
	}
	if catalog.Retriever != "" {
		return catalog.Retriever
	}
	return "grep"
}

func indexPath() string {
	if v := os.Getenv("AGENT_INDEX_PATH"); v != "" {
		return v
	}
	if catalog.IndexPath != "" {
		return catalog.IndexPath
	}
	return ".agent_index.json"
}

func resolveRetriever(docsRoot string, provider *llm.OpenAI) retrieve.Retriever {
	if retrieverKind() != "vector" {
		return nil
	}
	path := indexPath()
	idx, err := rag.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vector index %s: %v (falling back to grep)\n", path, err)
		return nil
	}
	var embed llm.Embedder = provider
	if idx.Model == "hash" || envBool("AGENT_EMBED_HASH", false) {
		embed = rag.HashEmbedder{}
	}
	fmt.Fprintf(os.Stderr, "retriever=vector index=%s model=%s chunks=%d\n", path, idx.Model, len(idx.Items))
	return &rag.Vector{Index: idx, Embed: embed}
}

func displayDocs(root string) string {
	if root == "" {
		return "(off)"
	}
	return root
}

func handleInteractiveTask(ctx context.Context, mgr *task.Manager, rest string) {
	if rest == "" || rest == "help" {
		fmt.Println("usage: /task submit <goal> | list | status <id> | wait <id> | cancel <id>")
		return
	}
	parts := strings.Fields(rest)
	cmd := parts[0]
	args := strings.TrimSpace(rest[len(cmd):])

	switch cmd {
	case "submit", "run":
		if args == "" {
			fmt.Println("usage: /task submit <goal>")
			return
		}
		tk, err := mgr.Submit(args)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return
		}
		fmt.Printf("submitted id=%s status=%s (use /task wait %s or /task status %s)\n",
			tk.ID, tk.Status, tk.ID, tk.ID)

	case "list":
		list := mgr.List()
		if len(list) == 0 {
			fmt.Println("(no tasks)")
			return
		}
		for _, tk := range list {
			printTaskLine(tk)
		}

	case "status", "get":
		id := strings.Fields(args)
		if len(id) < 1 {
			fmt.Println("usage: /task status <id>")
			return
		}
		tk, ok := mgr.Get(id[0])
		if !ok {
			fmt.Fprintf(os.Stderr, "error: not found %s\n", id[0])
			return
		}
		printTask(tk)

	case "wait":
		id := strings.Fields(args)
		if len(id) < 1 {
			fmt.Println("usage: /task wait <id>")
			return
		}
		fmt.Printf("waiting for %s …\n", id[0])
		tk, err := mgr.Wait(ctx, id[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return
		}
		printTask(tk)

	case "cancel":
		id := strings.Fields(args)
		if len(id) < 1 {
			fmt.Println("usage: /task cancel <id>")
			return
		}
		tk, err := mgr.Cancel(id[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return
		}
		printTask(tk)

	default:
		fmt.Printf("unknown /task command %q\n", cmd)
	}
}

func ask(ctx context.Context, a *agent.Agent, question string) error {
	streamed := false
	prev := a.OnEvent
	a.Stream = envBool("AGENT_STREAM", true)
	a.OnEvent = func(e agent.Event) {
		if prev != nil {
			prev(e)
		}
		if e.Kind == agent.EventToken {
			streamed = true
			fmt.Print(e.Token)
		}
	}
	answer, err := a.Run(ctx, question)
	a.OnEvent = prev
	if err != nil {
		return err
	}
	fmt.Println()
	if !streamed {
		fmt.Println(answer)
	}
	return nil
}

func attachSession(a *agent.Agent) {
	dir := env("AGENT_SESSION_DIR", ".agent_sessions")
	if dir == "off" || dir == "-" {
		return
	}
	a.SessionID = env("AGENT_SESSION_ID", "default")
	a.Sessions = &session.File{Dir: dir}
	if err := a.RestoreSession(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "session restore: %v\n", err)
		return
	}
	if n := len(a.History()); n > 0 {
		fmt.Fprintf(os.Stderr, "restored session id=%s messages=%d\n", a.SessionID, n)
	}
}

func displaySession(a *agent.Agent) string {
	if a.Sessions == nil {
		return "(off)"
	}
	id := a.SessionID
	if id == "" {
		id = "default"
	}
	return id
}

func printUsage(a *agent.Agent) {
	fmt.Println("last    " + a.LastUsage().Format())
	fmt.Println("session " + a.SessionUsage().Format())
}

func printMemory(a *agent.Agent) {
	if a.Memory == nil || a.Memory.Empty() {
		fmt.Println("(empty profile)")
		return
	}
	s := a.Memory.Snapshot()
	fmt.Println("structured profile (survives /new and history trim):")
	if s.Name != "" {
		fmt.Printf("  name:  %s\n", s.Name)
	}
	if len(s.Likes) > 0 {
		fmt.Printf("  likes: %s\n", strings.Join(s.Likes, "; "))
	}
	if len(s.Notes) > 0 {
		fmt.Println("  notes:")
		for _, n := range s.Notes {
			fmt.Printf("    - %s\n", n)
		}
	}
}

func printHistory(a *agent.Agent, full bool) {
	h := a.History()
	st := a.Stats()
	fmt.Println(st.FormatStats())
	if u := a.SessionUsage(); u.Calls > 0 || u.TotalTokens > 0 {
		fmt.Println("usage: " + u.Format())
	}
	if a.Memory != nil && !a.Memory.Empty() {
		fmt.Println("profile: " + a.Memory.ShortStatus())
	}
	if !full {
		fmt.Println("(list preview ≤120 runes/msg; summary/profile full; /history full)")
	}
	if len(h) == 0 {
		fmt.Println("(empty session)")
		return
	}
	for i, m := range h {
		content := m.Content
		if len(m.ToolCalls) > 0 {
			content = fmt.Sprintf("<tool_calls:%d> %s", len(m.ToolCalls), content)
		}
		label := string(m.Role)
		isSummary := strings.HasPrefix(m.Content, "[conversation_summary]")
		isProfile := strings.HasPrefix(m.Content, "[user_profile]")
		if isSummary {
			label = "summary"
		}
		if isProfile {
			label = "profile"
		}
		if !full && !isSummary && !isProfile {
			runes := utf8.RuneCountInString(content)
			if runes > listPreviewRunes {
				content = string([]rune(content)[:listPreviewRunes]) + "..."
			}
		}
		lines := strings.Split(content, "\n")
		fmt.Printf("%2d. %-10s %s\n", i+1, label, lines[0])
		for _, line := range lines[1:] {
			fmt.Printf("    %s\n", line)
		}
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return fallback
	}
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
