package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/task"
)

// runTaskCLI handles one-shot process commands:
//
//	task submit [--no-wait] <goal>   # default: wait until terminal status
//	task status|list|wait|cancel     # only useful with --no-wait if you attach elsewhere;
//	                                 # in one-shot CLI, prefer submit (waits) or interactive /task
func runTaskCLI(ctx context.Context, args []string) int {
	if len(args) < 1 {
		printTaskUsage()
		return 2
	}
	cmd := args[0]
	rest := args[1:]

	mgr, cleanup := newTaskManager(ctx)
	defer cleanup()

	switch cmd {
	case "submit", "run":
		noWait := false
		filtered := make([]string, 0, len(rest))
		for _, a := range rest {
			if a == "--no-wait" || a == "-n" {
				noWait = true
				continue
			}
			filtered = append(filtered, a)
		}
		goal := strings.Join(filtered, " ")
		tk, err := mgr.Submit(goal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("submitted id=%s status=%s\n", tk.ID, tk.Status)
		if noWait {
			fmt.Println("note: in-memory tasks die with this process; use interactive /task for multi-command polling")
			fmt.Println("      default submit waits in-process — omit --no-wait to block until done")
			return 0
		}
		fmt.Println("waiting for worker …")
		done, err := mgr.Wait(ctx, tk.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		printTask(done)
		if done.Status != task.StatusSucceeded {
			return 1
		}
		return 0

	case "status", "get", "list", "wait", "cancel":
		fmt.Fprintln(os.Stderr, "error: status/list/wait/cancel need a long-lived process")
		fmt.Fprintln(os.Stderr, "  use: go run ./cmd/agent")
		fmt.Fprintln(os.Stderr, "       then /task submit … /task list /task status <id>")
		fmt.Fprintln(os.Stderr, "  or:  go run ./cmd/agent task submit \"goal\"   # waits in same process")
		return 2

	default:
		fmt.Fprintf(os.Stderr, "unknown task command %q\n", cmd)
		printTaskUsage()
		return 2
	}
}

func printTaskUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  agent task submit [--no-wait] <goal>   run async job (default waits for result)
  agent                                 interactive /task submit|list|status|wait|cancel`)
}

func newTaskManager(parent context.Context) (*task.Manager, func()) {
	provider := llm.NewOpenAI(
		env("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		env("OPENAI_API_KEY", ""),
		env("OPENAI_MODEL", "gpt-4o-mini"),
	)
	memPath := env("AGENT_MEMORY_PATH", agent.DefaultMemoryPath)
	mem, err := agent.LoadMemory(memPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "memory load: %v (using empty)\n", err)
		mem = agent.NewMemory()
		mem.Path = memPath
	}

	extra, stopMCP := startMCP(parent)
	runner := func(ctx context.Context, goal string) (string, error) {
		m := mem
		if memPath != "" {
			if m2, err := agent.LoadMemory(memPath); err == nil {
				m = m2
			}
		}
		docs := resolveDocsRoot()
		a := newSyncAgent(provider, m, docs, extra)
		attachRoster(a, docs)
		a.Approver = workerApprover()
		a.Verbose = envBool("AGENT_VERBOSE", false)
		return a.Run(ctx, goal)
	}

	mgr := task.NewManager(parent, runner, task.Options{
		Workers:   envInt("AGENT_TASK_WORKERS", 2),
		QueueSize: envInt("AGENT_TASK_QUEUE", 64),
		Store:     resolveTaskStore(),
	})
	return mgr, func() {
		mgr.Stop()
		stopMCP()
	}
}

func printTask(tk task.Task) {
	fmt.Printf("id:        %s\n", tk.ID)
	fmt.Printf("status:    %s\n", tk.Status)
	fmt.Printf("goal:      %s\n", tk.Goal)
	if tk.Progress != "" {
		fmt.Printf("progress:  %s\n", tk.Progress)
	}
	if !tk.CreatedAt.IsZero() {
		fmt.Printf("created:   %s\n", tk.CreatedAt.Format(time.RFC3339))
	}
	if !tk.StartedAt.IsZero() {
		fmt.Printf("started:   %s\n", tk.StartedAt.Format(time.RFC3339))
	}
	if !tk.FinishedAt.IsZero() {
		fmt.Printf("finished:  %s\n", tk.FinishedAt.Format(time.RFC3339))
	}
	if tk.Error != "" {
		fmt.Printf("error:     %s\n", tk.Error)
	}
	if tk.Result != "" {
		fmt.Printf("result:\n%s\n", tk.Result)
	}
}

func printTaskLine(tk task.Task) {
	goal := tk.Goal
	if r := []rune(goal); len(r) > 48 {
		goal = string(r[:48]) + "…"
	}
	fmt.Printf("%s  %-10s  %s\n", tk.ID, tk.Status, goal)
}
