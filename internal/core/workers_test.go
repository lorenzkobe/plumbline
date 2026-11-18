package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (h *harness) hi(id, kind, tool string, input any) Output {
	return PreToolUse(h.helperIn(id, kind, tool, input))
}

func (h *harness) helperPost(id, kind, tool string, input, resp any) Output {
	in := h.in(tool, input, resp)
	in.AgentID, in.AgentType = id, "plumbline:"+kind
	return PostToolUse(in)
}

func (h *harness) run(kind, id string) *HelperRun {
	h.t.Helper()
	r := h.task().runByAgent(id)
	if r == nil || r.Kind != kind {
		h.t.Fatalf("no %s run %s recorded", kind, id)
	}
	return r
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	must(t, err)
	return string(b)
}

// bigTask: a big task, challenged and approved, with nothing built yet.
func bigTask(t *testing.T) *harness {
	h := groundedTask(t, "big")
	h.docs(DocPlan, goodPlan+"## Team\n- lead, challenger, plan-reviewer, code-reviewer, qa\n## Challenge note\n- nothing changed\n")
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.approve()
	return h
}

// M9: a worktree is the same project as its main checkout.
func TestWorktreeSharesTheProject(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tk := h.startTask("small")
	for _, wt := range []string{filepath.Join(h.repo, ".claude", "worktrees", "w1"), filepath.Join(filepath.Dir(h.repo), "sibling")} {
		run(t, h.repo, "git", "worktree", "add", "-q", "--detach", wt)
		wt, _ = filepath.EvalSymlinks(wt)
		if got := ProjectRoot(wt); got != h.repo {
			t.Fatalf("ProjectRoot(%s) = %s, want %s", wt, got, h.repo)
		}
		if ProjectKey(wt) != ProjectKey(h.repo) {
			t.Fatal("a worktree must share the main checkout's key")
		}
		if _, ok, err := LoadSetup(wt); err != nil || !ok {
			t.Fatalf("a worktree should see the confirmed setup: %v %v", ok, err)
		}
		if got, err := ActiveTask(wt, h.sid, ""); err != nil || got == nil || got.ID != tk.ID {
			t.Fatalf("the session's task should be found from a worktree: %v %v", got, err)
		}
		if snapPath(h.sid, wt) == snapPath(h.sid, h.repo) {
			t.Fatal("each working tree keeps its own snapshot")
		}
	}
	must(t, SetPaused(h.repo, true))
	if !IsPaused(filepath.Join(h.repo, ".claude", "worktrees", "w1")) {
		t.Fatal("pausing the project pauses its worktrees")
	}
	// A plain checkout keeps the key it always had, so nothing moves.
	if ProjectKey(h.repo) != treeKey(h.repo) {
		t.Fatal("a plain checkout's key changed")
	}
	// The copy inside the repo is not a change to the main checkout.
	d, _ := DirtyFiles(h.repo)
	for p := range d {
		if strings.HasPrefix(p, ".claude") {
			t.Fatalf("a worktree showed up as a changed file: %s", p)
		}
	}
	if c := ChangedSince(h.repo, ""); len(c) != 0 {
		t.Fatalf("a worktree showed up as changed since HEAD: %v", c)
	}
}

func TestBuilderWorksInItsOwnCopy(t *testing.T) {
	h := buildingTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n") // the lead's own uncommitted work
	h.post("Edit", map[string]any{"file_path": a}, nil)
	before, _ := Fingerprint(h.repo, nil)

	id, o := h.startHelper(HelperBuilder)
	r := h.run(HelperBuilder, id)
	wt := r.Worktree
	if wt == "" || !strings.Contains(fmtOut(o), wt) || !strings.Contains(fmtOut(o), "build-1.md") {
		t.Fatalf("the builder should be told its copy and notes file: %v (%s)", o, r.Error)
	}
	// The copy starts from the lead's code, uncommitted work included, and
	// shows only the builder's own changes from there.
	if got := read(t, filepath.Join(wt, "src", "a.go")); !strings.Contains(got, "return 2") {
		t.Fatalf("the copy should hold the lead's code: %q", got)
	}
	if s := run(t, wt, "git", "status", "--porcelain"); strings.TrimSpace(s) != "" {
		t.Fatalf("a new copy should have no changes of its own: %s", s)
	}
	if after, _ := Fingerprint(h.repo, nil); after != before {
		t.Fatal("making a copy must not change the main checkout's fingerprint")
	}
	if d, _ := DirtyFiles(h.repo); len(d) != 1 {
		t.Fatalf("only the lead's own edit is a change in the main checkout: %v", d)
	}

	hi := func(tool string, input any) Output { return h.hi(id, HelperBuilder, tool, input) }
	wa := filepath.Join(wt, "src", "a.go")
	h.expect(hi("Edit", map[string]any{"file_path": wa}), "", "")
	h.expect(hi("Edit", map[string]any{"file_path": a}), "deny", "Edit "+wa)
	h.expect(hi("Write", map[string]any{"file_path": filepath.Join(wt, "src", "auth", "login.go")}), "deny", "risky")
	h.expect(hi("Write", map[string]any{"file_path": filepath.Join(wt, RepoSetupFile)}), "deny", "setup")
	h.expect(hi("Bash", map[string]any{"command": "sed -i '' s/2/3/ src/a.go"}), "deny", "cd "+wt)
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && sed -i '' s/2/3/ src/a.go"}), "", "")
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && git status && git diff"}), "", "")
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && git commit -am 'fix: a'"}), "deny", "leave git")
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && git stash"}), "deny", "leave git")
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && echo x > " + filepath.Join(h.repo, "src", "b.go")}), "deny", "outside your copy")
	h.expect(hi("Bash", map[string]any{"command": "cd " + wt + " && curl -X POST https://example.com/x"}), "deny", "runs without the user")
	h.expect(hi("Bash", map[string]any{"command": "plumbline task replan"}), "deny", "plumbline run")
	h.expect(hi("Bash", map[string]any{"command": "plumbline builder merge 1"}), "deny", "plumbline run")
	h.expect(hi("AskUserQuestion", checkpointQ()), "deny", "without the user")
	h.expect(hi("Agent", map[string]any{"subagent_type": "plumbline:tracer"}), "deny", "helpers can't start")
	h.expect(hi("Write", map[string]any{"file_path": h.findingsPath(id)}), "allow", "")

	// The lead leaves the copy alone.
	h.expect(h.pre("Edit", map[string]any{"file_path": wa}), "deny", "builder's copy")
	h.expect(h.bash("echo x > "+wa), "deny", "builder's copy")

	// The builder builds: the planned file, and one outside the plan.
	write(t, wa, "package src\n\nfunc A() int { return 3 }\n")
	h.helperPost(id, HelperBuilder, "Edit", map[string]any{"file_path": wa}, nil)
	wc := filepath.Join(wt, "src", "c.go")
	write(t, wc, "package src\n\nfunc C() int { return 4 }\n")
	h.helperPost(id, HelperBuilder, "Write", map[string]any{"file_path": wc}, nil)
	if _, ok := h.task().OutsidePlan["src/c.go"]; !ok {
		t.Fatal("a builder's file outside the plan must be recorded for the report")
	}

	// It must leave notes before it stops.
	if o := h.stopHelper(id, HelperBuilder); !strings.Contains(fmtOut(o), "block") {
		t.Fatalf("a builder without notes should be sent back: %v", o)
	}
	write(t, h.findingsPath(id), "# Build 1\n- src/a.go: returns 3\n")
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("stop: %v", o)
	}
	if m := h.finishMissing(); !strings.Contains(m, "plumbline builder merge 1") {
		t.Fatalf("an unmerged copy must hold the finish check: %s", m)
	}

	// Meanwhile the lead changed another file; the merge keeps both.
	b := filepath.Join(h.repo, "src", "b.go")
	write(t, b, "package src\n\nfunc B() int { return 20 }\n")
	tk := h.task()
	changed, _, err := MergeBuilder(tk, 1, nil)
	must(t, err)
	must(t, SaveTask(tk))
	if strings.Join(changed, ",") != "src/a.go,src/c.go" {
		t.Fatalf("merged files = %v", changed)
	}
	if !strings.Contains(read(t, a), "return 3") || !strings.Contains(read(t, filepath.Join(h.repo, "src", "c.go")), "return 4") || !strings.Contains(read(t, b), "return 20") {
		t.Fatal("the merge should bring the builder's changes and keep the lead's")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("a merged copy is removed")
	}
	if m := h.finishMissing(); strings.Contains(m, "builder") {
		t.Fatalf("a merged builder holds nothing: %s", m)
	}
	if _, _, err := MergeBuilder(h.task(), 1, nil); err == nil {
		t.Fatal("a builder merges once")
	}
	// Once merged, the run's hooks can't change anything.
	h.expect(hi("Edit", map[string]any{"file_path": wa}), "deny", "already merged")
}

func TestBuilderMergeRefusesOverlaps(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	write(t, h.findingsPath(id), "# Build 1\n")
	h.stopHelper(id, HelperBuilder)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 4 }\n")

	tk := h.task()
	_, conflicts, err := MergeBuilder(tk, 1, nil)
	if !errors.Is(err, ErrConflict) || strings.Join(conflicts, ",") != "src/a.go" {
		t.Fatalf("overlapping edits must be refused: %v %v", err, conflicts)
	}
	if !strings.Contains(read(t, a), "return 4") {
		t.Fatal("a refused merge writes nothing")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatal("a refused merge keeps the copy")
	}
	must(t, DropBuilder(tk, 1))
	must(t, SaveTask(tk))
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("drop removes the copy")
	}
	if m := h.finishMissing(); strings.Contains(m, "builder") {
		t.Fatalf("a dropped builder holds nothing: %s", m)
	}
}

func TestBuilderWithNoChangesLeavesNoCopy(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, h.findingsPath(id), "# Build 1\nnothing to change\n")
	h.stopHelper(id, HelperBuilder)
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("a copy with no changes is removed at stop")
	}
	if r := h.run(HelperBuilder, id); !r.Dropped {
		t.Fatal("it's recorded as dropped")
	}
}

func TestBuildersOnlyAfterApproval(t *testing.T) {
	h := groundedTask(t, "small")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:builder"}), "deny", "approved plan")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "tester"}), "deny", "approved plan")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:qa"}), "deny", "approved plan")
}

func TestBlindQA(t *testing.T) {
	h := bigTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	at := filepath.Join(h.repo, "src", "a_test.go")
	if m := h.finishMissing(); !strings.Contains(m, "big tasks need blind QA") {
		t.Fatalf("a big task needs blind QA: %s", m)
	}

	id, o := h.startHelper(HelperQA)
	if !strings.Contains(fmtOut(o), "qa-1.md") || !strings.Contains(fmtOut(o), "blind QA") {
		t.Fatalf("start note: %v", o)
	}
	// While QA writes tests, the lead's code waits; tests and builders don't.
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "blind QA")
	h.expect(h.bash("sed -i '' s/1/2/ src/a.go"), "deny", "blind QA")
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "b_test.go")}), "", "")
	bid, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, bid).Worktree

	qa := func(tool string, input any) Output { return h.hi(id, HelperQA, tool, input) }
	h.expect(qa("Edit", map[string]any{"file_path": a}), "deny", "only test files")
	h.expect(qa("Write", map[string]any{"file_path": at}), "", "")
	h.expect(qa("Read", map[string]any{"file_path": filepath.Join(wt, "src", "a.go")}), "deny", "builders' copies")
	h.expect(qa("Write", map[string]any{"file_path": filepath.Join(wt, "src", "a_test.go")}), "deny", "builders' copies")
	h.expect(qa("Bash", map[string]any{"command": "cat " + filepath.Join(wt, "src", "a.go")}), "deny", "builders' copies")
	rel, _ := filepath.Rel(h.repo, wt)
	h.expect(qa("Bash", map[string]any{"command": "git -C " + rel + " diff"}), "deny", "builders' copies")
	// Searches and patterns that start above the copies reach into them.
	h.expect(qa("Bash", map[string]any{"command": "cat .claude/work*/*/src/a.go"}), "deny", "reach into")
	h.expect(qa("Bash", map[string]any{"command": "grep -rn 'return 3' ."}), "deny", "builders' copies")
	h.expect(qa("Bash", map[string]any{"command": "find . -name a.go"}), "deny", "builders' copies")
	h.expect(qa("Glob", map[string]any{"pattern": ".claude/**/a.go"}), "deny", "reach into")
	h.expect(qa("Glob", map[string]any{"pattern": "**/*_test.go"}), "deny", "reach into")
	h.expect(qa("Bash", map[string]any{"command": "grep -rn A src && cat src/*.go"}), "", "")
	h.expect(qa("Grep", map[string]any{"pattern": "func A"}), "", "") // rg skips the ignored copies' folder
	h.expect(qa("Bash", map[string]any{"command": "plumbline run related --files src/a_test.go"}), "", "")
	h.expect(qa("Bash", map[string]any{"command": "./test.sh"}), "deny", "plumbline run related")
	h.expect(qa("Read", map[string]any{"file_path": filepath.Join(TaskDocsDir(h.repo, h.task().ID), "challenge-1.md")}), "deny", "independent")
	h.expect(qa("Read", map[string]any{"file_path": a}), "", "")
	if _, _, err := MergeBuilder(h.task(), 1, nil); err == nil || !strings.Contains(err.Error(), "blind QA") {
		t.Fatalf("no merge while QA runs: %v", err)
	}

	write(t, at, "package src\n")
	h.helperPost(id, HelperQA, "Write", map[string]any{"file_path": at}, nil)
	write(t, h.findingsPath(id), "# QA 1\n- [bug] A returns 2 -> src/a_test.go::TestA, fails now\n")
	if o := h.stopHelper(id, HelperQA); o != nil {
		t.Fatalf("stop: %v", o)
	}
	if r := h.run(HelperQA, id); !contains(r.Wrote, "src/a_test.go") {
		t.Fatalf("QA's test files are recorded: %v", r.Wrote)
	}

	// After QA, code changes go ahead; a second QA run wouldn't be blind.
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "", "")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:qa"}), "deny", "already changed")

	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code-review-1: fine\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "what blind QA's tests caught") || strings.Contains(m, "big tasks need blind QA") {
		t.Fatalf("the report must say what QA caught: %s", m)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- qa-1: TestA failed before the fix\n## For you\n- nothing\n")
	if m := h.finishMissing(); strings.Contains(m, "qa") {
		t.Fatalf("QA is satisfied: %s", m)
	}
}

func TestBlindQAWaiverAndSmallTasks(t *testing.T) {
	h := bigTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:qa"}), "deny", "waive")
	tk := h.task()
	tk.Waivers = append(tk.Waivers, Waiver{Check: "qa", Reason: "user said so", Print: "x", At: Now()})
	must(t, SaveTask(tk))
	if m := h.finishMissing(); strings.Contains(m, "blind QA") {
		t.Fatalf("a qa waiver holds even after code changes: %s", m)
	}
	s := buildingTask(t)
	if m := s.finishMissing(); strings.Contains(m, "blind QA") {
		t.Fatalf("small tasks don't need blind QA: %s", m)
	}
}

func TestTracerAndTester(t *testing.T) {
	h := groundedTask(t, "normal")
	b := filepath.Join(h.repo, "src", "b.go")
	id, o := h.startHelper(HelperTracer)
	if !strings.Contains(fmtOut(o), "trace-1.md") {
		t.Fatalf("start note: %v", o)
	}
	tr := func(tool string, input any) Output { return h.hi(id, HelperTracer, tool, input) }
	h.expect(tr("Edit", map[string]any{"file_path": b}), "deny", "read-only")
	h.expect(tr("Write", map[string]any{"file_path": h.findingsPath(id)}), "allow", "")
	h.expect(tr("Bash", map[string]any{"command": "rm src/b.go"}), "deny", "only runs commands that read")
	h.expect(tr("Bash", map[string]any{"command": "plumbline run test"}), "deny", "")
	// A helper's reads and searches aren't the lead's: the lead opens what it quotes.
	h.helperPost(id, HelperTracer, "Read", map[string]any{"file_path": b}, nil)
	h.helperPost(id, HelperTracer, "mcp__serena__find_symbol", nil, nil)
	tk := h.task()
	if _, ok := tk.Reads[b]; ok {
		t.Fatal("a tracer's read must not count as the lead's")
	}
	if tk.Lookups["serena"].Count != 1 {
		t.Fatalf("a tracer's search must not count as the lead's: %v", tk.Lookups)
	}
	h.post("Read", map[string]any{"file_path": b}, nil)
	if _, ok := h.task().Reads[b]; !ok {
		t.Fatal("the lead's read counts")
	}

	p := buildingTask(t)
	tid, _ := p.startHelper(HelperTester)
	te := func(tool string, input any) Output { return p.hi(tid, HelperTester, tool, input) }
	p.expect(te("Bash", map[string]any{"command": "plumbline run test && plumbline run lint"}), "", "")
	p.expect(te("Bash", map[string]any{"command": "plumbline run proof api -- curl -s localhost:3000"}), "deny", "plumbline run test|related")
	p.expect(te("Bash", map[string]any{"command": "./test.sh"}), "deny", "")
	p.expect(te("Edit", map[string]any{"file_path": filepath.Join(p.repo, "src", "a.go")}), "deny", "read-only")
	p.expect(te("Write", map[string]any{"file_path": p.findingsPath(tid)}), "allow", "")
}

func TestWorkerPartsCanBeSwitchedOff(t *testing.T) {
	h := buildingTask(t)
	s, _, _ := LoadSetup(h.repo)
	s.Parts = map[string]bool{HelperBuilder: false}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:builder"}), "deny", "switched off")
}

func TestTwoBuildersAndMergeKinds(t *testing.T) {
	h := buildingTask(t)
	id1, _ := h.startHelper(HelperBuilder)
	id2, _ := h.startHelper(HelperBuilder)
	w1, w2 := h.run(HelperBuilder, id1).Worktree, h.run(HelperBuilder, id2).Worktree
	if w1 == "" || w2 == "" || w1 == w2 {
		t.Fatalf("each builder gets its own copy: %q %q", w1, w2)
	}
	if h.run(HelperBuilder, id2).Findings != "build-2.md" {
		t.Fatal("notes files are numbered per run")
	}
	// Builder 1 deletes a file and adds an executable script;
	// builder 2 changes another file. Both merge.
	must(t, os.Remove(filepath.Join(w1, "src", "b.go")))
	write(t, filepath.Join(w1, "run.sh"), "#!/bin/sh\necho hi\n")
	must(t, os.Chmod(filepath.Join(w1, "run.sh"), 0o755))
	write(t, filepath.Join(w2, "src", "a.go"), "package src\n\nfunc A() int { return 9 }\n")
	for _, id := range []string{id1, id2} {
		write(t, h.findingsPath(id), "# Build\n")
		h.stopHelper(id, HelperBuilder)
	}
	tk := h.task()
	for n := 1; n <= 2; n++ {
		if _, _, err := MergeBuilder(tk, n, nil); err != nil {
			t.Fatalf("merge %d: %v", n, err)
		}
	}
	must(t, SaveTask(tk))
	if _, err := os.Stat(filepath.Join(h.repo, "src", "b.go")); !os.IsNotExist(err) {
		t.Fatal("a file the builder deleted is deleted")
	}
	if fi, err := os.Stat(filepath.Join(h.repo, "run.sh")); err != nil || fi.Mode()&0o111 == 0 {
		t.Fatalf("an executable stays executable: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(h.repo, "src", "a.go")), "return 9") {
		t.Fatal("the second builder's change is merged")
	}
	if s := run(t, h.repo, "git", "worktree", "list"); strings.Count(s, "\n") != 1 {
		t.Fatalf("both copies are gone from git's list: %s", s)
	}
}

// Review findings on the first 2b pass.

func TestBuilderCantReachTheMainCheckout(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	hi := func(cmd string) Output { return h.hi(id, HelperBuilder, "Bash", map[string]any{"command": cmd}) }
	for _, c := range []string{
		"ln -s ../../.. m && echo pwn > m/src/a.go",
		"ln ../../../src/a.go h.go",
		"make -C ../../.. all",
		"go build -o ../../../src/a.go ./...",
		"cp src/a.go " + filepath.Join(h.repo, "src", "a.go"),
		"tar -xf x.tar --directory=" + h.repo,
	} {
		h.expect(hi("cd "+wt+" && "+c), "deny", "outside your copy")
	}
	h.expect(hi("cd "+wt+" && go build -o bin/app ./... && ln -s src/a.go link.go"), "", "")
	// What no gate sees (code an interpreter runs) is caught afterwards.
	cmd := "cd " + wt + " && python3 -c 'print(1)'"
	h.expect(hi(cmd), "", "")
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src // pwn\n")
	o := h.helperPost(id, HelperBuilder, "Bash", map[string]any{"command": cmd}, map[string]any{"stdout": ""})
	if !strings.Contains(fmtOut(o), "main checkout changed") {
		t.Fatalf("a write to the main checkout must be reported: %v", o)
	}
	if why := h.task().Held["src/b.go"]; !strings.Contains(why, "builder") {
		t.Fatalf("it's held for the user's report: %q", why)
	}
}

func TestClaudeCodeWorktreesAreNotCopies(t *testing.T) {
	h := newHarness(t)
	h.setup()
	feat := filepath.Join(h.repo, ".claude", "worktrees", "feat")
	run(t, h.repo, "git", "worktree", "add", "-q", "-b", "feat", feat)
	feat, _ = filepath.EvalSymlinks(feat)
	if insideWorktrees(h.repo, filepath.Join(feat, "src", "a.go")) {
		t.Fatal("claude --worktree folders aren't builders' copies")
	}
	if !insideWorktrees(h.repo, filepath.Join(h.repo, ".claude", "worktrees", "plumbline-x-y", "a.go")) {
		t.Fatal("a plumbline- folder is a copy")
	}
	// A lead working in its own worktree: blind QA can read and write there.
	g := &harness{t: t, repo: feat, sid: "sess-2"}
	g.startTask("big")
	tk := g.task()
	tk.Phase, tk.Checkpoint2At, tk.ApprovalPrint = PhaseBuilding, Now(), ""
	must(t, SaveTask(tk))
	id, _ := g.startHelper(HelperQA) // approved before 0.5.0, no code changed: QA may start
	qa := func(tool string, input any) Output { return g.hi(id, HelperQA, tool, input) }
	g.expect(qa("Read", map[string]any{"file_path": filepath.Join(feat, "src", "a.go")}), "", "")
	g.expect(qa("Write", map[string]any{"file_path": filepath.Join(feat, "src", "a_test.go")}), "", "")
	g.expect(qa("Bash", map[string]any{"command": "cat src/a.go"}), "", "")
	g.expect(g.pre("Edit", map[string]any{"file_path": filepath.Join(feat, "src", "a.go")}), "deny", "blind QA")
}

func TestTestFileNames(t *testing.T) {
	c := &hookCtx{}
	for f, want := range map[string]bool{
		"src/a_test.go": true, "web/a.test.ts": true, "web/a.spec.tsx": true, "e2e/login.cy.ts": true,
		"tests/test_a.py": true, "pkg/a_test.py": true, "spec/user_spec.rb": true, "src/UserTest.java": true,
		"Tests/AppTests.swift": true, "__tests__/a.js": true, "src/foo_test.cc": true, "src/foo_unittest.cpp": true,
		"pkg/testdata/in.json": true, "conftest.py": true, "App/AppUITests.swift": true, "src/APITest.java": true,
		"MyApp.Tests/Calc.cs":  true,
		"Sources/Latest.swift": false, "src/Contest.java": false, "lib/Attest.php": false,
		"internal/spec/parser.go": false, "app/e2e/server.go": false, "src/a.go": false, "test.sh": false,
	} {
		if got := c.isTestFile(f); got != want {
			t.Errorf("isTestFile(%s) = %v, want %v", f, got, want)
		}
	}
}

func TestMergeKeepsFiltersModesAndFolders(t *testing.T) {
	h := buildingTask(t)
	write(t, filepath.Join(h.repo, ".gitattributes"), "*.txt eol=crlf\n")
	write(t, filepath.Join(h.repo, "run.sh"), "#!/bin/sh\n")
	must(t, os.Chmod(filepath.Join(h.repo, "run.sh"), 0o755))
	write(t, filepath.Join(h.repo, "lib", "x.go"), "package lib\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: fixtures")
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, filepath.Join(wt, "n.txt"), "one\ntwo\n")
	must(t, os.Chmod(filepath.Join(wt, "run.sh"), 0o644))
	must(t, os.RemoveAll(filepath.Join(wt, "lib")))
	write(t, filepath.Join(wt, "lib"), "now a file\n")
	write(t, h.findingsPath(id), "# Build 1\n")
	h.stopHelper(id, HelperBuilder)
	tk := h.task()
	if _, _, err := MergeBuilder(tk, 1, nil); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if got := read(t, filepath.Join(h.repo, "n.txt")); got != "one\r\ntwo\r\n" {
		t.Fatalf("the repo's eol filter must apply: %q", got)
	}
	if fi, _ := os.Stat(filepath.Join(h.repo, "run.sh")); fi.Mode()&0o111 != 0 {
		t.Fatal("a removed executable bit must be removed")
	}
	if got := read(t, filepath.Join(h.repo, "lib")); got != "now a file\n" {
		t.Fatalf("a folder replaced by a file: %q", got)
	}
}

func TestMergeRefusesUpFront(t *testing.T) {
	h := buildingTask(t)
	write(t, filepath.Join(h.repo, "lib", "x.go"), "package lib\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: lib")
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, filepath.Join(wt, "aaa.go"), "package a\n")
	must(t, os.RemoveAll(filepath.Join(wt, "lib")))
	write(t, filepath.Join(wt, "lib"), "a file\n")
	write(t, h.findingsPath(id), "# Build 1\n")
	h.stopHelper(id, HelperBuilder)
	// Meanwhile an ignored file sits in lib/ in the main checkout.
	write(t, filepath.Join(h.repo, "lib", "coverage", "out.txt"), "x\n")
	write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\n")
	changed, _, err := MergeBuilder(h.task(), 1, nil)
	if err == nil || len(changed) != 0 || !strings.Contains(err.Error(), "where a folder is") {
		t.Fatalf("the merge should refuse before writing: %v %v", changed, err)
	}
	if _, err := os.Stat(filepath.Join(h.repo, "aaa.go")); !os.IsNotExist(err) {
		t.Fatal("nothing is written when the merge is refused")
	}
}

func TestFinishWaitsForRunningBuilders(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	write(t, filepath.Join(h.run(HelperBuilder, id).Worktree, "src", "a.go"), "package src\n")
	if m := h.finishMissing(); !strings.Contains(m, "still running") {
		t.Fatalf("a running builder holds the finish check: %s", m)
	}
	o := UserPromptExpansion(&HookInput{SessionID: h.sid, Cwd: h.repo, CommandName: "plumbline:cancel"})
	if !strings.Contains(fmtOut(o), "unmerged copies") {
		t.Fatalf("cancel names the copies left: %v", o)
	}
}

// QA holds the lead's code while it works, however long; a run that went
// quiet (it died) or that the user waived lets go.
func TestQAHoldFollowsActivity(t *testing.T) {
	h := bigTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	id, _ := h.startHelper(HelperQA)
	set := func(started, seen time.Duration) {
		tk := h.task()
		r := tk.runByAgent(id)
		r.Started, r.LastSeen = Now().Add(-started), Now().Add(-seen)
		must(t, SaveTask(tk))
	}
	set(45*time.Minute, 5*time.Minute)
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "blind QA")
	h.hi(id, HelperQA, "Read", map[string]any{"file_path": a}) // its tool calls keep it running
	if r := h.run(HelperQA, id); Now().Sub(r.LastSeen) > time.Minute {
		t.Fatal("a QA tool call should record activity")
	}
	set(2*time.Hour, 40*time.Minute)
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "", "")
	set(45*time.Minute, time.Minute)
	tk := h.task()
	tk.Waivers = append(tk.Waivers, Waiver{Check: "qa", Reason: "user", Print: "x", At: Now()})
	must(t, SaveTask(tk))
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "", "")
}

// Second review of the 2b fixes.

func TestBuilderEscapesSecondPass(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	bash := func(cmd string, bg bool) Output {
		in := map[string]any{"command": cmd}
		if bg {
			in["run_in_background"] = true
		}
		return h.hi(id, HelperBuilder, "Bash", in)
	}
	parent := filepath.Dir(h.repo)
	for _, c := range []string{
		"ln -s " + parent + " m",
		"ln -s / r",
		"ln -s ~ hh",
		"ln -s " + strings.ToUpper(h.repo[:9]) + h.repo[9:] + " m",
		"make -C../../.. all",
		"cc -o../../../src/a.go x.c",
		"d=../../..; cp src/a.go $d/src/a.go",
		`cp src/a.go "$(cd ../../.. && pwd)/src/a.go"`,
		"echo x > $OUT",
	} {
		h.expect(bash("cd "+wt+" && "+c, false), "deny", "")
	}
	h.expect(bash("cd "+wt+" && go test ./... &", false), "deny", "foreground")
	h.expect(bash("cd "+wt+" && go test ./...", true), "deny", "foreground")
	h.expect(h.hi(id, HelperBuilder, "Monitor", map[string]any{"command": "cd " + wt + " && go test ./..."}), "deny", "foreground")
	h.expect(bash("cd "+wt+" && go test ./... && npm test && pytest -q && grep -rn x src && make -C sub all", false), "", "")

	// git's own files in the main checkout are watched too.
	h.expect(bash("cd "+wt+" && python3 -c 'print(1)'", false), "", "")
	write(t, filepath.Join(h.repo, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 0\n")
	o := h.helperPost(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && python3 -c 'print(1)'"}, map[string]any{"stdout": ""})
	if !strings.Contains(fmtOut(o), ".git/hooks/pre-commit") || !strings.Contains(h.task().Held[".git/hooks/pre-commit"], "check .git/hooks") {
		t.Fatalf("a change to git's hooks must be reported: %v", o)
	}
}

// The lead's own edits and merges while a builder works aren't the builder's.
func TestLeadChangesArentABuildersWrites(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	cmd := "cd " + wt + " && go vet ./..."
	h.expect(h.hi(id, HelperBuilder, "Bash", map[string]any{"command": cmd}), "", "")
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil) // the lead's own edit, meanwhile
	o := h.helperPost(id, HelperBuilder, "Bash", map[string]any{"command": cmd}, map[string]any{"stdout": ""})
	if strings.Contains(fmtOut(o), "main checkout changed") || h.task().Held["src/a.go"] != "" {
		t.Fatalf("the lead's edit must not be put on the builder: %v %v", o, h.task().Held)
	}
}

func TestBlindQASecondPass(t *testing.T) {
	h := bigTask(t)
	id, _ := h.startHelper(HelperQA)
	bid, _ := h.startHelper(HelperBuilder)
	_ = h.run(HelperBuilder, bid)
	copies := filepath.Join(h.repo, ".claude", "worktrees")
	qa := func(tool string, input any) Output { return h.hi(id, HelperQA, tool, input) }
	for _, c := range []string{
		"grep -rn SECRET",
		"rg -uu SECRET",
		"rg --no-ignore SECRET",
		"fd -u a.go",
		"cd .claude/worktrees && cd \"$(ls)\" && cat src/a.go",
		`git -C "$(git worktree list --porcelain | awk 'NR==5{print $2}')" diff`,
		"git cat-file --batch-all-objects --batch",
		"git log --all -p",
		"git fsck --lost-found",
	} {
		h.expect(qa("Bash", map[string]any{"command": c}), "deny", "")
	}
	h.expect(qa("Grep", map[string]any{"pattern": "SECRET", "path": copies}), "deny", "builders' copies")
	h.expect(qa("Grep", map[string]any{"pattern": "SECRET", "path": ".claude/worktrees"}), "deny", "builders' copies")
	h.expect(qa("Glob", map[string]any{"pattern": "src/**/*_test.go"}), "", "")
	h.expect(qa("Bash", map[string]any{"command": "grep -rn A src tests && rg A src && git log -3 && git diff"}), "", "")
}
