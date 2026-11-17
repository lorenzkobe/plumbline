package core

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Hooks run at the same time (parallel reads and lookups, a background
// helper). Each must see the others' saves: no record is lost.
func TestConcurrentHooksLoseNothing(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	const n = 12
	var files []string
	for i := 0; i < n; i++ {
		f := filepath.Join(h.repo, "src", fmt.Sprintf("f%d.go", i))
		write(t, f, "package src\n")
		files = append(files, f)
	}
	var wg sync.WaitGroup
	for _, f := range files {
		wg.Add(1)
		go func(f string) {
			defer wg.Done()
			h.post("Read", map[string]any{"file_path": f}, nil)
		}(f)
	}
	wg.Wait()
	tk := h.task()
	for _, f := range files {
		if _, ok := tk.Reads[f]; !ok {
			t.Errorf("read of %s was lost", filepath.Base(f))
		}
	}
}
