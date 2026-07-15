// Command agent is a minimal CLI for the educational Go agent.
//
//	Agent = LLM (brain) + Tools (hands) + Loop (scheduler)
//
// Environment:
//
//	OPENAI_API_KEY   API key (optional for some local servers)
//	OPENAI_BASE_URL  default https://api.openai.com/v1; Ollama: http://localhost:11434/v1
//	OPENAI_MODEL     default gpt-4o-mini; Ollama e.g. qwen2.5:7b
//	AGENT_VERBOSE    set to 0/false to hide turn logs (default: on)
//
// Usage:
//
//	go run ./cmd/agent "现在几点？请用工具查"
//	go run ./cmd/agent "帮我算 123 * 456"
//	go run ./cmd/agent
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	provider := llm.NewOpenAI(
		env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		env("OPENAI_API_KEY", ""),
		env("OPENAI_MODEL", "gpt-4o-mini"),
	)

	a := &agent.Agent{
		Provider: provider,
		Tools:    tool.DefaultTools(),
		MaxTurns: 8,
		Verbose:  envBool("AGENT_VERBOSE", true),
	}

	if len(os.Args) > 1 {
		question := strings.Join(os.Args[1:], " ")
		if err := ask(ctx, a, question); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Println("agent_go — minimal Go agent (type quit to exit)")
	fmt.Printf("model=%s base=%s\n", provider.Model, provider.BaseURL)

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
		if line == "quit" || line == "exit" || line == "q" {
			return
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
