package obs

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Metrics is a process-local counter set for /metrics.
type Metrics struct {
	StartedAt time.Time

	RunsStarted   atomic.Int64
	RunsSucceeded atomic.Int64
	RunsFailed    atomic.Int64
	RunsCancelled atomic.Int64
	ChatErrors    atomic.Int64
	ToolErrors    atomic.Int64
	LLMChatMsLast atomic.Int64
	InFlight      atomic.Int64
}

// NewMetrics starts the uptime clock.
func NewMetrics() *Metrics {
	return &Metrics{StartedAt: time.Now()}
}

// ObserveRun increments counters from a terminal run status string.
func (m *Metrics) ObserveRun(status string) {
	if m == nil {
		return
	}
	switch status {
	case "succeeded":
		m.RunsSucceeded.Add(1)
	case "failed":
		m.RunsFailed.Add(1)
	case "cancelled":
		m.RunsCancelled.Add(1)
	}
}

// Format is Prometheus-like text for /metrics.
func (m *Metrics) Format(inFlight int) string {
	if m == nil {
		return ""
	}
	uptime := int64(time.Since(m.StartedAt).Seconds())
	if inFlight < 0 {
		inFlight = int(m.InFlight.Load())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "agent_uptime_seconds %d\n", uptime)
	fmt.Fprintf(&b, "agent_runs_started %d\n", m.RunsStarted.Load())
	fmt.Fprintf(&b, "agent_runs_succeeded %d\n", m.RunsSucceeded.Load())
	fmt.Fprintf(&b, "agent_runs_failed %d\n", m.RunsFailed.Load())
	fmt.Fprintf(&b, "agent_runs_cancelled %d\n", m.RunsCancelled.Load())
	fmt.Fprintf(&b, "agent_runs_in_flight %d\n", inFlight)
	fmt.Fprintf(&b, "agent_chat_errors %d\n", m.ChatErrors.Load())
	fmt.Fprintf(&b, "agent_tool_errors %d\n", m.ToolErrors.Load())
	fmt.Fprintf(&b, "agent_llm_chat_ms_last %d\n", m.LLMChatMsLast.Load())
	return b.String()
}
