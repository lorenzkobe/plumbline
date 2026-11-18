package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// Fixes from the second smoke run (0.6.13).

// TB2: piping plumbline's output into a filter that only drops lines (tail,
// head) still binds the task, for start and adopt alike.
func TestTaskStartPipedThroughFilterBinds(t *testing.T) {
	for _, tail := range []string{" 2>&1 | tail -30", " | tail -n 30", " | head -20", " 2>&1 | tail -30 && git status", " | tail -n +2", " | head -n5 | tail -1"} {
		h := newHarness(t)
		tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(tk))
		h.post("Bash", map[string]any{"command": "plumbline task start --title x" + tail}, map[string]any{"stdout": "plumbline-task-id: " + tk.ID + "\nTask started.\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != tk.ID {
			t.Fatalf("%q: the started task wasn't bound", tail)
		}
	}
}

func TestTaskAdoptPipedThroughFilterBinds(t *testing.T) {
	for _, tail := range []string{" 2>&1 | tail -20", " | head -5"} {
		h := newHarness(t)
		tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(tk))
		h.post("Bash", map[string]any{"command": "plumbline task adopt " + tk.ID + tail}, map[string]any{"stdout": "plumbline-adopt-id: " + tk.ID + "\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != tk.ID {
			t.Fatalf("%q: the adopted task wasn't bound", tail)
		}
	}
}

// TB2: a pipe stage that can print text of its own (a file it reads, a
// rewrite, a script) still binds nothing.
func TestPipedPrinterBindsNothing(t *testing.T) {
	stages := []string{" | tail -30 notes.txt", " | sed s/a/b/", " | awk '{print}'", " | tee out.txt", " | tail -30 > out.txt", " | cat - notes.txt", " | xargs echo", " | sort --files0-from=list", " | tail -n 5 notes.txt", " | head --lines=5 notes.txt"}
	for _, tail := range stages {
		for _, verb := range []string{"start --title x", "adopt "} {
			h := newHarness(t)
			other := &Task{ID: NewID(), Title: "other", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
			must(t, SaveTask(other))
			cmd, line := "plumbline task "+verb, "plumbline-task-id: "
			if verb == "adopt " {
				cmd += other.ID
				line = "plumbline-adopt-id: "
			}
			o := h.post("Bash", map[string]any{"command": cmd + tail}, map[string]any{"stdout": line + other.ID + "\n"})
			if got, _ := ActiveTask(h.repo, h.sid, ""); got != nil {
				t.Fatalf("%q%q bound the session to %s", cmd, tail, got.ID)
			}
			if !strings.Contains(contextOf(o), "on its own line") {
				t.Fatalf("%q%q: no word that the task isn't tied: %v", cmd, tail, o)
			}
		}
	}
}

// CC1: the user committed their changes themselves (`! git commit`). The
// files leave git status with their content untouched, so the next command
// changed nothing; a real revert still counts.
func TestCommittedFilesAreNotAChange(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 5 }\n")
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot: a.go dirty
	run(t, h.repo, "git", "commit", "-q", "-am", "mine")
	if o := h.post("Bash", map[string]any{"command": "git remote -v"}, map[string]any{}); o["continue"] == false {
		t.Fatalf("a commit by the user stopped the work as a change: %v", o)
	}
	// A revert rewrites the file (another size here): still a change.
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 777 }\n")
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot: b.go dirty
	run(t, h.repo, "git", "checkout", "--", "src/b.go")
	o := h.post("Bash", map[string]any{"command": "git checkout -- src/b.go"}, map[string]any{})
	if o["continue"] != false || !strings.Contains(fmtOut(o), "src/b.go") {
		t.Fatalf("a revert before approval should stop the work: %v", o)
	}
}
