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

	"github.com/asaqelee/agent_go/agent"
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
