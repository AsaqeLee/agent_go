package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/asaqelee/agent_go/tool"
)

type stdinApprover struct {
	mu  sync.Mutex
	in  *bufio.Reader
	out *os.File
}

func newStdinApprover() *stdinApprover {
	return &stdinApprover{in: bufio.NewReader(os.Stdin), out: os.Stderr}
}

func (s *stdinApprover) Approve(ctx context.Context, req tool.Approval) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.out, "\napprove %s %s ? [y/N] ", req.Name, compactArgs(req.Args))
	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		line, err := s.in.ReadString('\n')
		if err != nil {
			errCh <- err
			return
		}
		lineCh <- line
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case err := <-errCh:
		return false, err
	case line := <-lineCh:
		line = strings.ToLower(strings.TrimSpace(line))
		return line == "y" || line == "yes", nil
	}
}

func compactArgs(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "…"
	}
	return s
}

type allowAll struct{}

func (allowAll) Approve(context.Context, tool.Approval) (bool, error) { return true, nil }

type denyAll struct{}

func (denyAll) Approve(context.Context, tool.Approval) (bool, error) { return false, nil }

// workerApprover must not read stdin (task workers share the process with the REPL).
func workerApprover() tool.Approver {
	if envBool("AGENT_MCP_AUTO_APPROVE", false) {
		return allowAll{}
	}
	return denyAll{}
}
