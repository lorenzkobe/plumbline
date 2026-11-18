package core

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// HR1: a reviewer listing the notes folder sees only file names; reading
// another reviewer's findings or the report is still refused.
func TestReviewerListsNotesFolder(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	docs := TaskDocsDir(h.repo, h.task().ID)
	hb := func(cmd string) Output {
		return PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}))
	}
	for _, cmd := range []string{"ls " + docs, "ls -la " + docs + "/", "git diff; ls " + docs, "sed -n 1,20p src/a.go; ls " + docs} {
		if d, r := decision(hb(cmd)); d == "deny" {
			t.Fatalf("%q refused: %s", cmd, r)
		}
	}
	for _, cmd := range []string{"cat " + docs + "/plan-review-1.md", "grep -r x " + docs, "ls " + docs + " && cat " + docs + "/report.md", "ls " + docs + "/*", "head " + docs + " " + docs + "/report.md"} {
		h.expect(hb(cmd), "deny", "")
	}
}

// FR1: the lead's lookups just before the task starts, in the same
// session, count for the checkpoint; older ones and a helper's don't.
func TestLookupsBeforeStartCount(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.post("mcp__socraticode__codebase_search", nil, nil)
	h.post("mcp__mempalace__mempalace_search", nil, nil)
	tk := h.startTask("normal")
	tk, _ = LoadTask(h.repo, tk.ID)
	if tk.Lookups["socraticode"].Count != 1 || tk.Lookups["mempalace"].Count != 1 {
		t.Fatalf("lookups before the start didn't count: %v", tk.Lookups)
	}
	if _, ok := tk.Lookups["serena"]; ok {
		t.Fatalf("a lookup that never ran counted: %v", tk.Lookups)
	}
	// They're taken once: the next task in this session starts clean.
	h.post("Bash", map[string]any{"command": "plumbline task finish"}, nil)
	tk.Phase = PhaseDone
	must(t, SaveTask(tk))
	next := h.startTask("normal")
	if len(next.Lookups) != 0 {
		t.Fatalf("the first task's lookups carried over: %v", next.Lookups)
	}

	// Older than the window: not counted.
	h2 := newHarness(t)
	h2.setup()
	old := Now
	Now = func() time.Time { return old().Add(-2 * time.Hour) }
	h2.post("mcp__socraticode__codebase_search", nil, nil)
	Now = old
	if tk := h2.startTask("normal"); len(tk.Lookups) != 0 {
		t.Fatalf("a lookup from two hours ago counted: %v", tk.Lookups)
	}
}

// FR2: brew's read-only subcommands only read.
func TestBrewReadsOnly(t *testing.T) {
	h := groundedTask(t, "normal") // before approval: only reading runs
	for _, cmd := range []string{"brew --prefix postgresql@14", `ls "$(brew --prefix postgresql@14)/bin/postgres"`, "brew list --versions", "brew info node"} {
		if d, r := decision(h.pre("Bash", map[string]any{"command": cmd})); d == "deny" {
			t.Fatalf("%q refused: %s", cmd, r)
		}
	}
	for _, cmd := range []string{"brew install jq", "brew upgrade", "brew services start postgresql"} {
		h.expect(h.pre("Bash", map[string]any{"command": cmd}), "deny", "")
	}
}

// SR10: with no task open, every message's note says a change starts by
// asking, however small, before any edit. CM2: the rules say to ask before
// finishing with the change uncommitted.
func TestNoTaskNoteSaysAskFirst(t *testing.T) {
	h := newHarness(t)
	h.setup()
	note := contextOf(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: "rename x"}))
	// 0.7.0 (UX1): the task starts without a question; the plan question is the one stop.
	for _, want := range []string{"start it right away", "one-line", "Don't try the edit first", "one plan question"} {
		if !strings.Contains(note, want) {
			t.Fatalf("no-task note lacks %q: %s", want, note)
		}
	}
	for _, want := range []string{"Start the task right away", "however small", "Don't try an edit before they approve", "ask the user whether to commit first", "Never show the user task ids"} {
		if !strings.Contains(coreRules, want) {
			t.Fatalf("rules.md lacks %q", want)
		}
	}
}

// HR1: a helper writing its findings with the shell is told to use Edit or
// Write, not that "cat changes something".
func TestHelperShellWriteSaysUseEdit(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	docs := TaskDocsDir(h.repo, h.task().ID)
	o := PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat >> " + docs + "/code-review-1.md <<'EOF'\n- x\nEOF"}))
	_, r := decision(o)
	if !strings.Contains(r, "Edit or Write tool") || strings.Contains(r, `"cat" changes`) {
		t.Fatalf("shell write message: %s", r)
	}
	o = PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": "python3 fix.py 2>/dev/null"}))
	if _, r := decision(o); !strings.Contains(r, "Edit or Write tool") || strings.Contains(r, "with the shell") {
		t.Fatalf("python3 message lacks the Edit/Write hint: %s", r)
	}
}

// PL1: "test: does not apply, because ..." is a case with no test, not a
// test file named "does".
func TestTestDoesNotApplyIsNotALink(t *testing.T) {
	pc := CheckPlan("## Files\n- src/a.go\n## Cases\n- [edge] a typo leaves an undefined name -> test: does not apply, because tsc refuses it\n- [normal] A works -> test: `src/a_test.go::TestA`\n")
	for _, p := range pc.Problems {
		if strings.Contains(p, "case") {
			t.Fatalf("case problem: %v", p)
		}
	}
	if len(pc.CaseTests) != 1 || pc.CaseTests[0] != "src/a_test.go" {
		t.Fatalf("case tests: %v", pc.CaseTests)
	}
}
