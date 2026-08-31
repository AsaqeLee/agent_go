package agent

import (
	"path/filepath"
	"sync"
)

var memoryPathMu sync.Map // abs path → *sync.Mutex

func lockMemoryPath(path string) func() {
	if path == "" {
		return func() {}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	v, _ := memoryPathMu.LoadOrStore(abs, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
