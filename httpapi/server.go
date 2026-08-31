package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/obs"
	"github.com/asaqelee/agent_go/run"
	"github.com/asaqelee/agent_go/tool"
)

// Deps wires the HTTP surface to the agent runtime.
type Deps struct {
	// NewAgent builds a fully configured agent for one session id.
	NewAgent func(sessionID string) *agent.Agent
	Park     *Park
	Runs     *run.Registry
	Metrics  *obs.Metrics
	Channel  channel.Channel
}

// Handler serves health, metrics, runs, approvals, and IM messages.
func Handler(d Deps) http.Handler {
	if d.Runs == nil {
		d.Runs = run.NewRegistry()
	}
	if d.Metrics == nil {
		d.Metrics = obs.NewMetrics()
	}
	if d.Runs.OnDone == nil {
		d.Runs.OnDone = func(rec run.Record) {
			d.observe(rec)
			d.emitChannel(context.Background(), rec)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", d.handleHealthz)
	mux.HandleFunc("/metrics", d.handleMetrics)
	mux.HandleFunc("/v1/runs", d.handleRuns)
	mux.HandleFunc("/v1/runs/", d.handleRunItem)
	mux.HandleFunc("/v1/approvals/", d.handleApprovals)
	mux.HandleFunc("/v1/messages", d.handleMessages)
	return withRequestID(mux)
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, id := obs.WithID(r.Context(), r.Header.Get("X-Request-Id"))
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (d Deps) handleHealthz(w http.ResponseWriter, r *http.Request) {
	inFlight := 0
	if d.Runs != nil {
		inFlight = d.Runs.InFlight()
	}
	uptime := 0
	if d.Metrics != nil && !d.Metrics.StartedAt.IsZero() {
		uptime = int(time.Since(d.Metrics.StartedAt).Seconds())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":         true,
		"in_flight":  inFlight,
		"uptime_s":   uptime,
		"request_id": obs.IDFrom(r.Context()),
	})
}

func (d Deps) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	inFlight := 0
	if d.Runs != nil {
		inFlight = d.Runs.InFlight()
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(d.Metrics.Format(inFlight)))
}

type runRequest struct {
	Input     string `json:"input"`
	SessionID string `json:"session_id"`
	Stream    bool   `json:"stream"`
	Wait      *bool  `json:"wait"`
}

type runResponse struct {
	RunID     string    `json:"run_id,omitempty"`
	Output    string    `json:"output,omitempty"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status,omitempty"`
	Usage     llm.Usage `json:"usage"`
	Error     string    `json:"error,omitempty"`
}

func waitDefault(req runRequest) bool {
	if req.Wait == nil {
		return true
	}
	return *req.Wait
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
	if req.Stream {
		d.streamRun(w, r, req)
		return
	}
	wait := waitDefault(req)
	rec, err := d.startAgentRun(r.Context(), req.SessionID, req.Input, wait, nil)
	if errors.Is(err, run.ErrConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !wait {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(recordResponse(rec))
		return
	}
	writeTerminal(w, rec)
}

func (d Deps) handleRunItem(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(rest, "/")
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		rec, ok := d.Runs.Get(id)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(recordResponse(rec))
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		rec, err := d.Runs.Cancel(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(recordResponse(rec))
		return
	}
	http.NotFound(w, r)
}

func (d Deps) startAgentRun(ctx context.Context, sessionID, input string, wait bool, onEvent func(agent.Event)) (run.Record, error) {
	if d.NewAgent == nil {
		return run.Record{}, fmt.Errorf("agent factory missing")
	}
	rec, err := d.Runs.Start(ctx, sessionID, input, wait, func(runCtx context.Context, rec run.Record) (string, llm.Usage, error) {
		a := d.NewAgent(sessionID)
		if a == nil {
			return "", llm.Usage{}, fmt.Errorf("agent factory returned nil")
		}
		a.SessionID = sessionID
		_ = a.RestoreSession(runCtx)
		if d.Park != nil {
			a.Approver = d.Park
		}
		prev := a.OnEvent
		a.OnEvent = func(e agent.Event) {
			if prev != nil {
				prev(e)
			}
			if onEvent != nil {
				onEvent(e)
			}
			if d.Metrics != nil && e.Kind == agent.EventToolEnd && e.Code != "" && e.Code != tool.CodeOK {
				d.Metrics.ToolErrors.Add(1)
			}
		}
		out, err := a.Run(runCtx, input)
		return out, a.LastUsage(), err
	})
	if errors.Is(err, run.ErrConflict) {
		return rec, err
	}
	if err != nil {
		return rec, err
	}
	if d.Metrics != nil {
		d.Metrics.RunsStarted.Add(1)
	}
	return rec, nil
}

func (d Deps) observe(rec run.Record) {
	if d.Metrics == nil {
		return
	}
	d.Metrics.ObserveRun(string(rec.Status))
	if rec.Status == run.StatusFailed && strings.Contains(rec.Err, "llm") {
		d.Metrics.ChatErrors.Add(1)
	}
}

func (d Deps) emitChannel(ctx context.Context, rec run.Record) {
	if d.Channel == nil {
		return
	}
	text := rec.Output
	kind := "assistant"
	if rec.Status != run.StatusSucceeded {
		text = rec.Err
		kind = "system"
	}
	if strings.TrimSpace(text) == "" {
		return
	}
	_ = d.Channel.Send(ctx, channel.Message{
		Text:      text,
		RunID:     rec.ID,
		SessionID: rec.SessionID,
		Kind:      kind,
	})
}

func writeTerminal(w http.ResponseWriter, rec run.Record) {
	w.Header().Set("Content-Type", "application/json")
	if rec.Status == run.StatusFailed {
		w.WriteHeader(http.StatusBadGateway)
	}
	_ = json.NewEncoder(w).Encode(recordResponse(rec))
}

func recordResponse(rec run.Record) runResponse {
	return runResponse{
		RunID:     rec.ID,
		Output:    rec.Output,
		SessionID: rec.SessionID,
		Status:    string(rec.Status),
		Usage:     rec.Usage,
		Error:     rec.Err,
	}
}

func (d Deps) streamRun(w http.ResponseWriter, r *http.Request, req runRequest) {
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
	}
	rec, err := d.startAgentRun(r.Context(), req.SessionID, req.Input, true, func(e agent.Event) {
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
	})
	if errors.Is(err, run.ErrConflict) {
		write("error", map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		write("error", map[string]string{"error": err.Error()})
		return
	}
	write("done", recordResponse(rec))
}

func (d Deps) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Text      string `json:"text"`
		SessionID string `json:"session_id"`
		User      string `json:"user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	body.Text = strings.TrimSpace(body.Text)
	if body.Text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	if body.SessionID == "" {
		body.SessionID = "default"
	}
	if d.Channel != nil {
		_ = d.Channel.Send(r.Context(), channel.Message{
			User: body.User, Text: body.Text, SessionID: body.SessionID, Kind: "user",
		})
	}
	rec, err := d.startAgentRun(r.Context(), body.SessionID, body.Text, true, nil)
	if errors.Is(err, run.ErrConflict) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeTerminal(w, rec)
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
