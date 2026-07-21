// Command agent is a minimal CLI for the educational Go agent.
//
//	Agent = LLM (brain) + Tools (hands) + Loop (scheduler)
//
// Environment (shell export, or project .env — see .env.example):
//
//	OPENAI_API_KEY              API key (optional for some local servers)
//	OPENAI_BASE_URL             default https://api.openai.com/v1
//	OPENAI_MODEL                default gpt-4o-mini
//	AGENT_VERBOSE               set to 0/false to hide turn logs (default: on)
//	AGENT_MAX_HISTORY_MESSAGES  session message cap; 0 = unlimited (default: 40)
//
// On startup, loadDotEnv(".env") fills missing vars only (does not override export).
//
// Usage:
//
//	go run ./cmd/agent "现在几点？请用工具查"
//	go run ./cmd/agent
//
// Interactive commands: /new, /history, quit
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

	a := &agent.Agent{
		Provider:           provider,
		Tools:              tool.DefaultTools(),
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

	fmt.Println("agent_go — multi-turn (quit | /new | /history)")
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
			fmt.Println("(session cleared)")
			continue
		case line == "/history":
			printHistory(a)
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

func printHistory(a *agent.Agent) {
	h := a.History()
	st := a.Stats()
	fmt.Println(st.FormatStats())
	if len(h) == 0 {
		fmt.Println("(empty session)")
		return
	}
	for i, m := range h {
		content := m.Content
		if len(m.ToolCalls) > 0 {
			content = fmt.Sprintf("<tool_calls:%d> %s", len(m.ToolCalls), content)
		}
		runes := utf8.RuneCountInString(content)
		if runes > 120 {
			content = string([]rune(content)[:120]) + "..."
		}
		fmt.Printf("%2d. %-10s %s\n", i+1, m.Role, content)
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
