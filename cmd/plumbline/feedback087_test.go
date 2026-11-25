package main

// Blind QA for release 0.8.7, the part that only the CLI shows: task start
// names `plumbline builder clean` when a closed task's builder copy is still
// on disk (case C29 in brief-qa-1.md).

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lorenzkobe/plumbline/internal/core"
)

func TestF87_C29_TaskStartNamesBuilderClean(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	if out, code := plumbline(t, dir, home, `{"kind": "personal", "commands": {"test": "true"}}`, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	// Without a leftover copy, task start doesn't name the command.
	out, code := plumbline(t, dir, home, "", "task", "start", "--title", "first")
	if code != 0 {
		t.Fatalf("start: %d %s", code, out)
	}
	if strings.Contains(out, "plumbline builder clean") {
		t.Fatalf("no copies are left, yet task start names builder clean: %s", out)
	}
	first := ""
	for _, tk := range core.ListTasks(dir) {
		if tk.Title == "first" {
			first = tk.ID
		}
	}
	if first == "" {
		t.Fatal("the first task wasn't saved")
	}
	// The first task had a builder whose copy was never merged, then closed.
	wt := filepath.Join(dir, ".claude", "worktrees", "plumbline-old-1")
	if b, err := exec.Command("git", "-C", dir, "worktree", "add", "-q", "--detach", wt).CombinedOutput(); err != nil {
		t.Fatalf("worktree: %v %s", err, b)
	}
	tk, err := core.LoadTask(dir, first)
	if err != nil {
		t.Fatal(err)
	}
	tk.Phase = core.PhaseCancelled
	tk.Helpers = append(tk.Helpers, core.HelperRun{Kind: core.HelperBuilder, AgentID: "builder-1", Started: core.Now(), Finished: core.Now(), Worktree: wt})
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	out, code = plumbline(t, dir, home, "", "task", "start", "--title", "second")
	if code != 0 {
		t.Fatalf("second start: %d %s", code, out)
	}
	if !strings.Contains(out, "plumbline builder clean") {
		t.Fatalf("task start should name plumbline builder clean for the left copy: %s", out)
	}
}
