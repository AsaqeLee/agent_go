// Command agent is a minimal CLI for the educational Go agent.
//
//	Agent = LLM (brain) + Tools (hands) + Loop (scheduler) + structured Memory
//
// Environment (shell export, or project .env — see .env.example):
//
//	OPENAI_API_KEY              API key (optional for some local servers)
//	OPENAI_BASE_URL             default https://api.openai.com/v1
//	OPENAI_MODEL                default gpt-4o-mini
//	AGENT_VERBOSE               set to 0/false to hide turn logs (default: on)
//	AGENT_MAX_HISTORY_MESSAGES  session message cap; 0 = unlimited (default: 40)
//
// Interactive: quit | /new | /new all | /history [full] | /memory | /memory clear
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

	provider := llm.NewOpenAI(
		env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		env("OPENAI_API_KEY", ""),
		env("OPENAI_MODEL", "gpt-4o-mini"),
	)

	mem := agent.NewMemory()
	a := &agent.Agent{
		Provider:           provider,
		Memory:             mem,
		Tools:              tool.DefaultTools(mem),
		MaxTurns:           8,
		MaxHistoryMessages: envInt("AGENT_MAX_HISTORY_MESSAGES", 40),
		Verbose:            envBool("AGENT_VERBOSE", true),
	}

	if len(os.Args) > 1 {
		question := strings.Join(os.Args[1:], " ")
		if err := ask(ctx, a, question); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("agent_go — quit | /new | /new all | /history [full] | /memory | /memory clear")
	fmt.Printf("model=%s base=%s max_history_messages=%d\n",
		provider.Model, provider.BaseURL, a.MaxHistoryMessages)

	in := bufio.NewScanner(os.Stdin)
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
		fmt.Println("(list preview ≤120 runes/msg; summary/profile blocks full in messages when present; /history full)")
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
