package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store persists task records across process restarts.
type Store interface {
	Put(Task) error
	Get(id string) (Task, bool, error)
	List() ([]Task, error)
}

// MemoryStore is an in-process Store (tests).
type MemoryStore struct {
	mu    sync.Mutex
	tasks map[string]Task
	order []string
}

// NewMemoryStore returns an empty durable-looking store that still dies with the process.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tasks: map[string]Task{}}
}

func (s *MemoryStore) Put(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = map[string]Task{}
	}
	if _, ok := s.tasks[t.ID]; !ok {
		s.order = append(s.order, t.ID)
	}
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Get(id string) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	return t, ok, nil
}

func (s *MemoryStore) List() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.order))
	for _, id := range s.order {
		if t, ok := s.tasks[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// FileStore is an atomic JSON document of all tasks (single-node durability).
type FileStore struct {
	Path string
	mu   sync.Mutex
}

type fileDoc struct {
	Tasks []Task `json:"tasks"`
}

func (s *FileStore) Put(t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.read()
	if err != nil {
		return err
	}
	found := false
	for i, x := range doc.Tasks {
		if x.ID == t.ID {
			doc.Tasks[i] = t
			found = true
			break
		}
	}
	if !found {
		doc.Tasks = append(doc.Tasks, t)
	}
	return s.write(doc)
}

func (s *FileStore) Get(id string) (Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.read()
	if err != nil {
		return Task{}, false, err
	}
	for _, t := range doc.Tasks {
		if t.ID == id {
			return t, true, nil
		}
	}
	return Task{}, false, nil
}

func (s *FileStore) List() ([]Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, err := s.read()
	if err != nil {
		return nil, err
	}
	return doc.Tasks, nil
}

func (s *FileStore) read() (fileDoc, error) {
	if s.Path == "" {
		return fileDoc{}, fmt.Errorf("task: empty store path")
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return fileDoc{}, nil
		}
		return fileDoc{}, err
	}
	if len(data) == 0 {
		return fileDoc{}, nil
	}
	var doc fileDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fileDoc{}, fmt.Errorf("task store: %w", err)
	}
	return doc, nil
}

func (s *FileStore) write(doc fileDoc) error {
	if s.Path == "" {
		return fmt.Errorf("task: empty store path")
	}
	if dir := filepath.Dir(s.Path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".tasks-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.Path)
}
