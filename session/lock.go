package session

import (
	"path/filepath"
	"sync"
)

var pathMu sync.Map // abs path → *sync.Mutex

func lockPath(path string) func() {
	if path == "" {
		return func() {}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	v, _ := pathMu.LoadOrStore(abs, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
