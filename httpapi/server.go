package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

// Deps wires the HTTP surface to the agent runtime.
type Deps struct {
	// NewAgent builds a fully configured agent for one session id.
	NewAgent func(sessionID string) *agent.Agent
	Park     *Park
}

// Handler serves /healthz, /v1/runs, /v1/approvals/{id}.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/v1/runs", d.handleRuns)
	mux.HandleFunc("/v1/approvals/", d.handleApprovals)
	return mux
}

type runRequest struct {
	Input     string `json:"input"`
	SessionID string `json:"session_id"`
	Stream    bool   `json:"stream"`
}

type runResponse struct {
	Output    string    `json:"output"`
	SessionID string    `json:"session_id"`
	Usage     llm.Usage `json:"usage"`
}

func (d Deps) handleRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req runRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	req.Input = strings.TrimSpace(req.Input)
	if req.Input == "" {
		http.Error(w, "empty input", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if d.NewAgent == nil {
		http.Error(w, "agent factory missing", http.StatusInternalServerError)
		return
	}
	a := d.NewAgent(req.SessionID)
	if a == nil {
		http.Error(w, "agent factory returned nil", http.StatusInternalServerError)
		return
	}
	a.SessionID = req.SessionID
	_ = a.RestoreSession(r.Context())
	if req.Stream {
		d.streamRun(w, r, a, req)
		return
	}
	out, err := a.Run(r.Context(), req.Input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runResponse{
		Output:    out,
		SessionID: req.SessionID,
		Usage:     a.LastUsage(),
	})
}

func (d Deps) streamRun(w http.ResponseWriter, r *http.Request, a *agent.Agent, req runRequest) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	write := func(event string, payload any) {
		data, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}
	if d.Park != nil {
		prev := d.Park.OnAsk
		d.Park.OnAsk = func(id string, ap tool.Approval) {
			write("approval", map[string]any{"id": id, "tool": ap.Name, "args": ap.Args})
			if prev != nil {
				prev(id, ap)
			}
		}
		defer func() { d.Park.OnAsk = prev }()
		a.Approver = d.Park
	}
	a.Stream = true
	prev := a.OnEvent
	a.OnEvent = func(e agent.Event) {
		if prev != nil {
			prev(e)
		}
		switch e.Kind {
		case agent.EventToken:
			write("token", map[string]string{"text": e.Token})
		case agent.EventToolStart:
			write("tool_start", map[string]string{"name": e.Tool, "id": e.ToolID, "args": e.Args})
		case agent.EventToolEnd:
			write("tool_end", map[string]any{"name": e.Tool, "id": e.ToolID, "code": e.Code, "content": e.Content})
		case agent.EventError:
			write("error", map[string]string{"error": e.Err})
		}
	}
	out, err := a.Run(r.Context(), req.Input)
	if err != nil {
		write("error", map[string]string{"error": err.Error()})
		return
	}
	write("done", runResponse{Output: out, SessionID: req.SessionID, Usage: a.LastUsage()})
}

func (d Deps) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/approvals/")
	id = strings.Trim(id, "/")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if d.Park == nil {
		http.Error(w, "approvals disabled", http.StatusNotFound)
		return
	}
	var body struct {
		Allow bool `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if err := d.Park.Decide(id, body.Allow); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// ListenAndServe is a thin wrapper for cmd/agent serve.
func ListenAndServe(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
