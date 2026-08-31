// Package channel is the IM/message exit (and optional entry) for Agent events.
package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Message is one IM-shaped payload.
type Message struct {
	User      string `json:"user,omitempty"`
	Text      string `json:"text"`
	RunID     string `json:"run_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Kind      string `json:"kind,omitempty"` // user | assistant | tool | system
}

// Channel delivers messages to an IM or test sink.
type Channel interface {
	Send(ctx context.Context, msg Message) error
	Transcript() []Message
}

// Memory is an in-process transcript (tests / CLI).
type Memory struct {
	mu   sync.Mutex
	msgs []Message
}

func (m *Memory) Send(_ context.Context, msg Message) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.msgs = append(m.msgs, msg)
	m.mu.Unlock()
	return nil
}

func (m *Memory) Transcript() []Message {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.msgs))
	copy(out, m.msgs)
	return out
}

// Webhook POSTs a Feishu-like JSON body. Failures are returned to the caller.
type Webhook struct {
	URL        string
	HTTPClient *http.Client
	Memory
}

func (w *Webhook) Send(ctx context.Context, msg Message) error {
	_ = w.Memory.Send(ctx, msg)
	if w == nil || w.URL == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": msg.Text},
		"run_id":   msg.RunID,
		"session":  msg.SessionID,
		"kind":     msg.Kind,
	})
	if err != nil {
		return err
	}
	client := w.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("channel webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("channel webhook: status %d", resp.StatusCode)
	}
	return nil
}
