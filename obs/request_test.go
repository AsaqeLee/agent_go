package obs

import (
	"context"
	"strings"
	"testing"
)

func TestWithIDRoundTrip(t *testing.T) {
	ctx, id := WithID(context.Background(), "abc")
	if id != "abc" || IDFrom(ctx) != "abc" {
		t.Fatalf("%s %s", id, IDFrom(ctx))
	}
	ctx2, id2 := WithID(context.Background(), "")
	if id2 == "" || IDFrom(ctx2) != id2 {
		t.Fatal("expected generated id")
	}
}

func TestMetricsFormat(t *testing.T) {
	m := NewMetrics()
	m.RunsStarted.Add(2)
	m.ObserveRun("succeeded")
	m.ObserveRun("failed")
	s := m.Format(3)
	for _, want := range []string{"agent_runs_started 2", "agent_runs_succeeded 1", "agent_runs_failed 1", "agent_runs_in_flight 3"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
}
