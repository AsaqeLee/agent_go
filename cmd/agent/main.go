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
	"unicode/utf8"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/task"
	"github.com/asaqelee/agent_go/tool"
)

const listPreviewRunes = 120

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if _, err := loadDotEnv(".env"); err != nil {
		fmt.Fprintf(os.Stderr, "dotenv: %v\n", err)
		os.Exit(1)
	}

	// Subcommand: task …
	if len(os.Args) > 1 && os.Args[1] == "task" {
		os.Exit(runTaskCLI(ctx, os.Args[2:]))
	}

	provider, mem, memPath := buildProviderAndMemory()
	a := newSyncAgent(provider, mem)

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
		ag := newSyncAgent(provider, mem2)
		ag.Verbose = envBool("AGENT_VERBOSE", false)
		return ag.Run(taskCtx, goal)
	}, task.Options{
		Workers:   envInt("AGENT_TASK_WORKERS", 2),
		QueueSize: envInt("AGENT_TASK_QUEUE", 64),
	})
	defer mgr.Stop()

	fmt.Println("agent_go — sync chat + async tasks")
	fmt.Println("  chat: type a message | /new | /new all | /history [full] | /memory | /memory clear")
	fmt.Println("  task: /task submit <goal> | /task list | /task status <id> | /task wait <id> | /task cancel <id>")
	fmt.Printf("model=%s base=%s max_history_messages=%d memory=%s workers=%d\n",
		provider.Model, provider.BaseURL, a.MaxHistoryMessages, memPath, envInt("AGENT_TASK_WORKERS", 2))

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

func newSyncAgent(provider *llm.OpenAI, mem *agent.Memory) *agent.Agent {
	return &agent.Agent{
		Provider:            provider,
		Memory:              mem,
		Tools:               tool.DefaultTools(mem),
		MaxTurns:            envInt("AGENT_MAX_TURNS", 8),
		MaxHistoryMessages:  envInt("AGENT_MAX_HISTORY_MESSAGES", 40),
		KeepRecentFullTurns: envInt("AGENT_KEEP_RECENT_FULL_TURNS", 1),
		DisableLLMSummary:   envBool("AGENT_DISABLE_LLM_SUMMARY", false),
		Verbose:             envBool("AGENT_VERBOSE", true),
	}
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
	answer, err := a.Run(ctx, question)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println(answer)
	return nil
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
