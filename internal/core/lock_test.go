package core

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Hooks of the lead and of a background helper run at once. Each must see
// the others' saves: no read record, helper finish or refused file is lost.
func TestConcurrentHooksLoseNothing(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, goodPlan)
	id, _ := h.startHelper(HelperChallenger)
	write(t, h.findingsPath(id), "x\n")
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
	wg.Add(1)
	go func() { defer wg.Done(); h.stopHelper(id, HelperChallenger) }()
	wg.Wait()
	tk := h.task()
	for _, f := range files {
		if _, ok := tk.Reads[f]; !ok {
			t.Errorf("read of %s was lost", filepath.Base(f))
		}
	}
	if r := tk.runByAgent(id); r == nil || r.Finished.IsZero() {
		t.Error("the helper's finish was lost")
	}
}
