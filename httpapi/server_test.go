package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/asaqelee/agent_go/agent"
	"github.com/asaqelee/agent_go/channel"
	"github.com/asaqelee/agent_go/llm"
	"github.com/asaqelee/agent_go/tool"
)

type scripted struct {
	responses []llm.Response
	i         int
}

func (s *scripted) Chat(_ context.Context, _ llm.Request) (llm.Response, error) {
	if s.i >= len(s.responses) {
		return llm.Response{}, context.Canceled
	}
	r := s.responses[s.i]
	s.i++
	return r, nil
}

func TestHealthzAndJSONRun(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{
			Provider: &scripted{responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "hello"}},
			}},
			MaxTurns: 2,
		}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}

	res, err = http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"hi","session_id":"s1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%d %s", res.StatusCode, b)
	}
	var got runResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Output != "hello" || got.SessionID != "s1" {
		t.Fatalf("%+v", got)
	}
}

func TestSSERunEmitsDone(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{
			Provider: &scripted{responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
			}},
			MaxTurns: 2,
		}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"hi","stream":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("ct=%s", ct)
	}
	sc := bufio.NewScanner(res.Body)
	var events []string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			events = append(events, strings.TrimPrefix(line, "event: "))
		}
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "done") {
		t.Fatalf("events=%s", joined)
	}
}

func TestToolCallJSON(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{
			Provider: &scripted{responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{
					ID: "c1", Type: "function",
					Function: llm.FunctionCall{Name: "calculator", Arguments: `{"expression":"2 + 2"}`},
				}}}},
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "4"}},
			}},
			Tools:    []tool.Tool{tool.Calculator{}},
			MaxTurns: 4,
		}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"2+2"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got runResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Output != "4" {
		t.Fatalf("%+v", got)
	}
}

type blocking struct {
	started chan struct{}
	release chan struct{}
}

func (b *blocking) Chat(ctx context.Context, _ llm.Request) (llm.Response, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	select {
	case <-b.release:
		return llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: "late"}}, nil
	case <-ctx.Done():
		return llm.Response{}, ctx.Err()
	}
}

func TestRunRegistryGetAndConflict(t *testing.T) {
	b := &blocking{started: make(chan struct{}, 1), release: make(chan struct{})}
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{Provider: b, MaxTurns: 2}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	waitFalse := `{"input":"hi","session_id":"s1","wait":false}`
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(waitFalse)))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status=%d %s", res.StatusCode, body)
	}
	var first runResponse
	if err := json.NewDecoder(res.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	select {
	case <-b.started:
	case <-time.After(2 * time.Second):
		t.Fatal("not started")
	}

	res, err = http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"two","session_id":"s1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d %s", res.StatusCode, body)
	}

	gres, err := http.Get(srv.URL + "/v1/runs/" + first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var got runResponse
	_ = json.NewDecoder(gres.Body).Decode(&got)
	gres.Body.Close()
	if got.Status != "running" || got.RunID != first.RunID {
		t.Fatalf("%+v", got)
	}

	cres, err := http.Post(srv.URL+"/v1/runs/"+first.RunID+"/cancel", "application/json", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(cres.Body).Decode(&got)
	cres.Body.Close()
	if got.Status != "cancelled" {
		t.Fatalf("%+v", got)
	}
}

func TestMetricsAndRequestID(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{
			Provider: &scripted{responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
			}},
			MaxTurns: 2,
		}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/runs", bytes.NewReader([]byte(`{"input":"hi"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-Id", "abc123")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("X-Request-Id") != "abc123" {
		t.Fatalf("rid=%s", res.Header.Get("X-Request-Id"))
	}
	mres, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(mres.Body)
	mres.Body.Close()
	s := string(body)
	if !strings.Contains(s, "agent_runs_started 1") || !strings.Contains(s, "agent_runs_succeeded 1") {
		t.Fatalf("metrics=%s", s)
	}
}

type failCh struct{}

func (failCh) Send(context.Context, channel.Message) error { return errDown }
func (failCh) Transcript() []channel.Message               { return nil }

var errDown = errString("down")

type errString string

func (e errString) Error() string { return string(e) }

func TestChannelErrorRecordedOnRun(t *testing.T) {
	h := Handler(Deps{
		Channel: failCh{},
		NewAgent: func(string) *agent.Agent {
			return &agent.Agent{
				Provider: &scripted{responses: []llm.Response{
					{Message: llm.Message{Role: llm.RoleAssistant, Content: "pong"}},
				}},
				MaxTurns: 2,
			}
		},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"ping","session_id":"c1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got runResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "succeeded" || got.Output != "pong" {
		t.Fatalf("%+v", got)
	}
	// OnDone annotates after Start returns the snapshot; GET sees channel_error.
	gres, err := http.Get(srv.URL + "/v1/runs/" + got.RunID)
	if err != nil {
		t.Fatal(err)
	}
	defer gres.Body.Close()
	var stored runResponse
	_ = json.NewDecoder(gres.Body).Decode(&stored)
	if stored.ChannelError == "" {
		t.Fatalf("expected channel_error on stored run: %+v", stored)
	}
}

func TestMessagesUsesChannel(t *testing.T) {
	ch := &channel.Memory{}
	h := Handler(Deps{
		Channel: ch,
		NewAgent: func(string) *agent.Agent {
			return &agent.Agent{
				Provider: &scripted{responses: []llm.Response{
					{Message: llm.Message{Role: llm.RoleAssistant, Content: "pong"}},
				}},
				MaxTurns: 2,
			}
		},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/messages", "application/json", bytes.NewReader([]byte(`{"text":"ping","session_id":"im","user":"u"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got runResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Output != "pong" {
		t.Fatalf("%+v", got)
	}
	tr := ch.Transcript()
	if len(tr) < 2 {
		t.Fatalf("transcript=%+v", tr)
	}
}

func TestHTTPTokenRequired(t *testing.T) {
	h := Handler(Deps{
		Token: "secret",
		NewAgent: func(string) *agent.Agent {
			return &agent.Agent{
				Provider: &scripted{responses: []llm.Response{
					{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
				}},
				MaxTurns: 2,
			}
		},
	})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status=%d", res.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/runs", bytes.NewReader([]byte(`{"input":"hi"}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Token", "secret")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status=%d", res.StatusCode)
	}
	hz, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	hz.Body.Close()
	if hz.StatusCode != 200 {
		t.Fatalf("healthz=%d", hz.StatusCode)
	}
}

func TestGeneratedSessionIDIsNotDefault(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{
			Provider: &scripted{responses: []llm.Response{
				{Message: llm.Message{Role: llm.RoleAssistant, Content: "ok"}},
			}},
			MaxTurns: 2,
		}
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"hi"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var got runResponse
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.SessionID == "" || got.SessionID == "default" {
		t.Fatalf("session_id=%q", got.SessionID)
	}
	if !strings.HasPrefix(got.SessionID, "s_") {
		t.Fatalf("session_id=%q", got.SessionID)
	}
}

func TestChatErrorIncrementsMetric(t *testing.T) {
	h := Handler(Deps{NewAgent: func(string) *agent.Agent {
		return &agent.Agent{Provider: &scripted{}, MaxTurns: 2} // empty → canceled
	}})
	srv := httptest.NewServer(h)
	defer srv.Close()
	res, err := http.Post(srv.URL+"/v1/runs", "application/json", bytes.NewReader([]byte(`{"input":"x"}`)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	mres, _ := http.Get(srv.URL + "/metrics")
	body, _ := io.ReadAll(mres.Body)
	mres.Body.Close()
	if !strings.Contains(string(body), "agent_runs_failed 1") {
		t.Fatalf("%s", body)
	}
}
