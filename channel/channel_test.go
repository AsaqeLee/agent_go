package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMemoryTranscript(t *testing.T) {
	var m Memory
	if err := m.Send(context.Background(), Message{Text: "hi", Kind: "assistant"}); err != nil {
		t.Fatal(err)
	}
	got := m.Transcript()
	if len(got) != 1 || got[0].Text != "hi" {
		t.Fatalf("%+v", got)
	}
}

func TestWebhookPostsFeishuShape(t *testing.T) {
	var saw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		saw = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	wh := &Webhook{URL: srv.URL}
	if err := wh.Send(context.Background(), Message{Text: "hello", RunID: "r1", Kind: "assistant"}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(saw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["msg_type"] != "text" {
		t.Fatalf("%s", saw)
	}
	if len(wh.Transcript()) != 1 {
		t.Fatal("expected memory copy")
	}
}
