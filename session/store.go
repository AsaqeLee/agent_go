// Package session persists agent chat history across process restarts.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/asaqelee/agent_go/llm"
)

// Store loads and saves one session's messages by id.
type Store interface {
	Load(ctx context.Context, id string) ([]llm.Message, error)
	Save(ctx context.Context, id string, msgs []llm.Message) error
	Delete(ctx context.Context, id string) error
}

// Memory is a process-local Store (tests / default when disk is off).
type Memory struct {
	mu   sync.Mutex
	data map[string][]llm.Message
}

// NewMemory returns an empty in-memory session store.
func NewMemory() *Memory {
	return &Memory{data: map[string][]llm.Message{}}
}

func (m *Memory) Load(_ context.Context, id string) ([]llm.Message, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	msgs := m.data[normalizeID(id)]
	if len(msgs) == 0 {
		return nil, nil
	}
	out := make([]llm.Message, len(msgs))
	copy(out, msgs)
	return out, nil
}

func (m *Memory) Save(_ context.Context, id string, msgs []llm.Message) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]llm.Message{}
	}
	cp := make([]llm.Message, len(msgs))
	copy(cp, msgs)
	m.data[normalizeID(id)] = cp
	return nil
}

func (m *Memory) Delete(_ context.Context, id string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, normalizeID(id))
	return nil
}

// File stores one JSON file per session id under Dir.
type File struct {
	Dir string
}

type fileDoc struct {
	Messages []llm.Message `json:"messages"`
}

// Load returns nil, nil when the session file is missing.
func (f *File) Load(_ context.Context, id string) ([]llm.Message, error) {
	if f == nil || strings.TrimSpace(f.Dir) == "" {
		return nil, fmt.Errorf("session: empty dir")
	}
	path, err := f.path(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var doc fileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("session load: %w", err)
	}
	return doc.Messages, nil
}

func (f *File) Save(_ context.Context, id string, msgs []llm.Message) error {
	if f == nil || strings.TrimSpace(f.Dir) == "" {
		return fmt.Errorf("session: empty dir")
	}
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return err
	}
	path, err := f.path(id)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(fileDoc{Messages: msgs}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicWrite(path, data, 0o600)
}

func (f *File) Delete(_ context.Context, id string) error {
	path, err := f.path(id)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (f *File) path(id string) (string, error) {
	id, err := safeID(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(f.Dir, id+".json"), nil
}

func normalizeID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "default"
	}
	return id
}

func safeID(id string) (string, error) {
	id = normalizeID(id)
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return "", fmt.Errorf("session: invalid id %q", id)
	}
	return id, nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
