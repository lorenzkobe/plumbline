package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 0.7.2: fixes from the 0.7.1 retest (step A).

// The lead may write its own notes from the terminal, as the Write tool may
// (red box 1 of the retest). Helpers' findings and Plumbline's state stay
// refused, and so do helpers.
func TestLeadNotesFromTerminal(t *testing.T) {
	h := groundedTask(t, "normal")
	d := TaskDocsDir(h.repo, h.task().ID)
	for _, c := range []string{
		"cat >> " + filepath.Join(d, DocPlan) + " <<'EOF'\n## Challenge note\n- nothing changed\nEOF",
		"cat > " + filepath.Join(d, DocForYou) + " <<EOF\n# The plan, in plain words\nEOF",
		"printf '%s\\n' x >> " + filepath.Join(d, DocUnderstanding),
		"echo '- none' | tee -a " + filepath.Join(d, DocReport),
	} {
		h.expect(h.bash(c), "allow", "")
	}
	for _, c := range []string{
		"cat > " + filepath.Join(d, "challenge-1.md") + " <<EOF\nx\nEOF",                                    // a helper's findings
		"cat > " + filepath.Join(filepath.Dir(d), "state.json") + " <<EOF\n{}\nEOF",                         // the task's state
		"cat > " + filepath.Join(d, "..", "state.json") + " <<EOF\n{}\nEOF",                                 // the same, by ..
		"cat > " + filepath.Join(d, "sub", "x.md") + " <<EOF\nx\nEOF",                                       // not a note
		"cat > " + filepath.Join(d, "plan.txt") + " <<EOF\nx\nEOF",                                          // not markdown
		"python3 -c 'print(1)' > " + filepath.Join(d, DocPlan),                                              // not a plain writer
		"cat " + filepath.Join(h.repo, "src", "a.go") + " > " + filepath.Join(d, DocPlan) + "; rm -rf " + d, // another part writes there
	} {
		h.expect(h.bash(c), "deny", "")
	}
	// A secret copied into the notes still asks.
	write(t, filepath.Join(h.repo, ".env"), "KEY=x\n")
	h.expect(h.bash("cat .env >> "+filepath.Join(d, DocPlan)), "ask", ".env")
	// A helper writes its notes with Write or Edit, never the terminal.
	o := PreToolUse(h.helperIn("ux-1", HelperUX, "Bash", map[string]any{"command": "cat >> " + filepath.Join(d, DocPlan) + " <<EOF\nx\nEOF"}))
	if dd, _ := decision(o); dd != "deny" {
		t.Fatalf("a helper's terminal write to the plan: %q", dd)
	}
}

// ENV1: a grep or rg file-name filter is a pattern, not .env. One that
// names a secret file still picks it to read.
func TestNameFilterIsNotASecretRead(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.repo, ".env"), "KEY=x\n")
	for _, c := range []string{
		`grep -rn "may be behind" docs src --include=*`,
		`grep -rn foo --include '*' src`,
		`grep -rn foo src --include=*.ts --exclude=*.test.ts`,
		`rg foo -g '*.ts' src`,
		`rg foo --glob=* src`,
		`rg foo -g'*' src`,
		`rg foo -g '!.env' .`,
		`grep -rn foo . --exclude=.env --exclude-dir=.git`,
		`grep -rn foo . --exclude .env`,
	} {
		if d, r := decision(h.bash(c)); d == "ask" || d == "deny" {
			t.Fatalf("%s: %s %s", c, d, r)
		}
	}
	for _, c := range []string{
		`grep -rn KEY . --include=.env`,
		`grep -rn KEY . --include .env`,
		`grep -rn KEY . --include=.env*`,
		`grep -rn BEGIN . --include=*.pem`,
		`rg KEY -g .env .`,
		`rg KEY --glob=.env.* .`,
		`grep KEY .env`,
		`grep KEY --include=* .env`,
	} {
		h.expect(h.bash(c), "ask", "")
	}
}

// One commit stop: after the user answers "Commit it" to the commit
// question, a commit of exactly that code doesn't ask again. Anything else
// still asks.
func TestCommitItIsOneStop(t *testing.T) {
	h := hotfixHarness(t)
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- nothing\n")
	h.docs(DocPlan, goodPlan)
	h.approve()
	s, _, _ := LoadSetup(h.repo)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", s, io.Discard)
	must(t, err)
	h.lintBuild()
	commit := `git commit -am "fix: A returns 2"`
	h.expect(h.bash(commit), "ask", "last commit of the task")

	q := func(answer string) {
		in := map[string]any{"questions": []any{map[string]any{"question": "Commit it now?", "header": "Commit",
			"options": []any{map[string]any{"label": "Commit it"}, map[string]any{"label": "Leave uncommitted"}}}}}
		h.post("AskUserQuestion", in, map[string]any{"answers": map[string]any{"Commit it now?": answer}})
	}
	q("Leave uncommitted")
	h.expect(h.bash(commit), "ask", "last commit")
	q("yes go ahead and commit") // typed text, not the option
	h.expect(h.bash(commit), "ask", "last commit")
	q("Commit it")
	h.expect(h.bash(commit), "", "")
	h.expect(h.bash(`git add -A && git commit -m "fix: A returns 2"`), "", "")
	// Two commits in one line: the answer was for one.
	h.expect(h.bash(commit+` && git commit --allow-empty -m "chore: more"`), "ask", "")
	// The proof and the message are still checked.
	h.expect(h.bash(`git commit -am "fixed stuff"`), "deny", "")

	// The code changed after the answer: ask again.
	write(t, a, "package src\n\nfunc A() int { return 2 } // two\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", s, io.Discard)
	must(t, err)
	h.lintBuild()
	h.expect(h.bash(commit), "ask", "last commit")

	// After a commit, the answer is used up (HEAD moved).
	q("Commit it")
	h.expect(h.bash(commit), "", "")
	run(t, h.repo, "git", "commit", "-qam", "fix: A returns 2")
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n")
	h.post("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}, nil)
	must(t, os.Remove(filepath.Join(h.repo, "src", "b.go")))
	if h.task().CommitOK == commitPrint(h.repo, nil) {
		t.Fatal("the answer outlived the commit it was for")
	}
}

// RD1: lines Claude saw count as read even when the line ended in a grep
// that found nothing (Claude Code then runs PostToolUseFailure), and files
// read just before the task started count for it, as they were then.
func TestReadsThatCount(t *testing.T) {
	h := groundedTask(t, "small")
	a := filepath.Join(h.repo, "src", "b.go")
	write(t, a, "package src\n\nfunc B() int { return 1 }\n")
	if p := CheckRefs(h.task(), "B (src/b.go:3)"); len(p) == 0 {
		t.Fatal("an unread file passed")
	}
	PostToolUseFailure(h.in("Bash", map[string]any{"command": "sed -n 1,3p src/b.go && grep -n nothing src/b.go"}, nil))
	if p := CheckRefs(h.task(), "B (src/b.go:3)"); len(p) > 0 {
		t.Fatalf("a read in a line that ended in a failed grep: %v", p)
	}

	// Before the task: a Read and a terminal read, then the task starts.
	g := newHarness(t)
	g.setup()
	c := filepath.Join(g.repo, "src", "c.go")
	write(t, c, "package src\n\nfunc C() int { return 1 }\n")
	g.post("Read", map[string]any{"file_path": filepath.Join(g.repo, "src", "a.go")}, nil)
	PostToolUseFailure(g.in("Bash", map[string]any{"command": "cat src/c.go; grep -n nothing src/c.go"}, nil))
	g.startTask("small")
	if p := CheckRefs(g.task(), "A (src/a.go:3), C (src/c.go:3)"); len(p) > 0 {
		t.Fatalf("reads just before the task: %v", p)
	}
	// Changed since: it no longer counts.
	write(t, c, "package src\n\nfunc C() int { return 2 }\n")
	if p := CheckRefs(g.task(), "C (src/c.go:3)"); len(p) == 0 || !strings.Contains(p[0], "changed") {
		t.Fatalf("a file changed after the pre-task read: %v", p)
	}
	// A helper's reads before the task aren't the lead's.
	k := newHarness(t)
	k.setup()
	PostToolUse(k.helperIn("r-1", HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(k.repo, "src", "a.go")}))
	k.startTask("small")
	if p := CheckRefs(k.task(), "A (src/a.go:3)"); len(p) == 0 {
		t.Fatal("a helper's read counted for the lead")
	}
}

// CF1: the commit question waits until the finish check has nothing left
// but the commit, and writing the report says what's left.
func TestCommitQuestionComesLast(t *testing.T) {
	h := provenTask(t)
	commitQ := map[string]any{"questions": []any{map[string]any{"question": "Commit it now?", "header": "Commit",
		"options": []any{map[string]any{"label": "Commit it"}, map[string]any{"label": "Leave uncommitted"}}}}}
	h.expect(h.pre("AskUserQuestion", commitQ), "deny", "plumbline:code-reviewer")

	id, _ := h.startHelper(HelperCodeReviewer)
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Review 1\nnothing at 80 or more\n"))
	h.stopHelper(id, HelperCodeReviewer)

	// A report that misses a section: the post hook names it.
	p := h.docs(DocReport, "# Report\nDone.\n")
	if o := fmtOut(h.post("Write", map[string]any{"file_path": p}, nil)); !strings.Contains(o, "still needs") || !strings.Contains(o, "Verdict") {
		t.Fatalf("after an incomplete report: %s", o)
	}
	h.expect(h.pre("AskUserQuestion", commitQ), "deny", "Verdict")

	p = h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- review-1: nothing at 80 or more\n## For you\n- nothing\n")
	if o := fmtOut(h.post("Write", map[string]any{"file_path": p}, nil)); !strings.Contains(o, "nothing left but the commit") {
		t.Fatalf("after a complete report: %s", o)
	}
	h.expect(h.pre("AskUserQuestion", commitQ), "", "")

	// Claude can't answer it for the user.
	pre := map[string]any{"questions": commitQ["questions"], "answers": map[string]any{"Commit it now?": "Commit it"}}
	h.expect(h.pre("AskUserQuestion", pre), "deny", "pre-answered")
}

// PR1: unit and component tests aren't the real app.
func TestUnitTestIsNotUIProof(t *testing.T) {
	for _, c := range []string{
		"npx vitest run tests/components/RefreshButton.pending.test.tsx",
		"vitest run", "npx jest src", "pnpm exec jest", "npm test", "npm run test", "yarn test",
		"go test ./...", "python -m pytest tests/ui", "npx --yes vitest run x.test.tsx",
	} {
		if !UnitTestProof(c) {
			t.Errorf("%q should not count as ui proof", c)
		}
	}
	for _, c := range []string{
		"npx playwright test e2e/refresh.spec.ts", "node scripts/click-refresh.mjs",
		"npm run e2e", "npx playwright test && npx vitest run", "npm run test:e2e",
	} {
		if UnitTestProof(c) {
			t.Errorf("%q drives the app and should count", c)
		}
	}
}

// The goal: a plan needs one, the approval keeps it, and the report says
// whether it's met. A task approved before 0.7.2 (no goal) isn't held.
func TestGoalLine(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, strings.Replace(goodPlan, "## Goal\nDone when: A works as planned.\n", "", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "## Goal")

	h = provenTask(t)
	if h.task().Goal != "Done when: A works as planned." {
		t.Fatalf("goal not kept at approval: %q", h.task().Goal)
	}
	id, _ := h.startHelper(HelperCodeReviewer)
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Review 1\nnothing at 80 or more\n"))
	h.stopHelper(id, HelperCodeReviewer)
	full := "# Report\n" + reportEnd + "## Review\n- review-1: nothing at 80 or more\n## For you\n- nothing\n"
	h.docs(DocReport, strings.Replace(full, "Goal: met\n", "", 1))
	if m := h.finishMissing(); !strings.Contains(m, "Goal: met") || !strings.Contains(m, "A works as planned") {
		t.Fatalf("a report without the goal line: %s", m)
	}
	h.docs(DocReport, strings.Replace(full, "Goal: met\n", "**Goal:** not met: B is still 1\n", 1))
	if m := h.finishMissing(); strings.Contains(m, "goal") {
		t.Fatalf("a not-met goal is still an answer: %s", m)
	}
	tk := h.task()
	tk.Goal = "" // approved before 0.7.2
	must(t, SaveTask(tk))
	h.docs(DocReport, strings.Replace(full, "Goal: met\n", "", 1))
	if m := h.finishMissing(); m != "" {
		t.Fatalf("an older task without a goal: %s", m)
	}
}

// PL2: "test:" must link a file. "same test: at 120 s" was read as a test
// file named "at", and the approved plan could never finish.
func TestCaseTestLinkNamesAFile(t *testing.T) {
	plan := func(cs string) PlanCheck {
		return CheckPlan("## Goal\nDone when: x.\n## Files\n- src/a.go\n## Cases\n" + cs + "## Real-world proof\n- does not apply, because none\n## Needs your OK\n- none\n")
	}
	pc := plan("- [boundary] the 4th wait is 120 s, same test: at 120 s\n")
	if len(pc.Problems) != 1 || !strings.Contains(pc.Problems[0], `"at" isn't a test file`) || len(pc.CaseTests) != 0 {
		t.Fatalf("a word after test: %v %v", pc.Problems, pc.CaseTests)
	}
	// A real link later in the line is the one.
	pc = plan("- [boundary] same test: at 120 s. test: tests/sync/sync-manager.test.ts\n")
	if len(pc.Problems) != 0 || len(pc.CaseTests) != 1 || pc.CaseTests[0] != "tests/sync/sync-manager.test.ts" {
		t.Fatalf("the file link after a word: %v %v", pc.Problems, pc.CaseTests)
	}
	for _, ok := range []string{"test: test.sh", "test: `src/a_test.go::TestA returns 2`", "test: a_test.go", "test: tests/x"} {
		if pc := plan("- [normal] x -> " + ok + "\n"); len(pc.Problems) != 0 || len(pc.CaseTests) != 1 {
			t.Errorf("%q: %v", ok, pc.Problems)
		}
	}
}

// RD2: a line a search showed counts for a file the plan only mentions; a
// file the plan changes must be opened.
func TestSearchHitsCount(t *testing.T) {
	h := groundedTask(t, "normal")
	q := filepath.Join(h.repo, "src", "queue.go")
	write(t, q, "package src\n\n// x\n// x\n// x\nconst Max = 5\n// x\n// x\n// x\n// x\n// x\n// x\n// x\n// x\nconst Other = 1\n")
	h.post("Bash", map[string]any{"command": "grep -rn Max src"}, map[string]any{"stdout": "src/queue.go:6:const Max = 5\n"})
	h.docs(DocPlan, goodPlan)
	if p := CheckRefs(h.task(), "the queue also caps at 5 (src/queue.go:6)"); len(p) > 0 {
		t.Fatalf("a line the search showed: %v", p)
	}
	if p := CheckRefs(h.task(), "src/queue.go:15"); len(p) == 0 || !strings.Contains(p[0], "other lines") {
		t.Fatalf("a line the search didn't show: %v", p)
	}
	// The plan changes it: open it.
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- src/queue.go\n", 1))
	if p := CheckRefs(h.task(), "src/queue.go:6"); len(p) == 0 || !strings.Contains(p[0], "open it") {
		t.Fatalf("a file the plan changes, only searched: %v", p)
	}
	h.docs(DocPlan, goodPlan)
	// Changed since the search: no longer counts.
	write(t, q, "package src\n\n// x\n// x\n// x\nconst Max = 2\n")
	if p := CheckRefs(h.task(), "src/queue.go:6"); len(p) == 0 || !strings.Contains(p[0], "changed") {
		t.Fatalf("changed after the search: %v", p)
	}
	// A failed line (grep that found nothing at the end) and SocratiCode chunks count too.
	w := filepath.Join(h.repo, "src", "w.go")
	write(t, w, "package src\n\nconst W = 1\n")
	PostToolUseFailure(&HookInput{SessionID: h.sid, Cwd: h.repo, ToolName: "Bash",
		ToolInput: rawJSON(map[string]any{"command": "grep -rn W src; grep -rn nothing docs"}), Error: "Exit code 1\nsrc/w.go:3:const W = 1\n"})
	if p := CheckRefs(h.task(), "src/w.go:3"); len(p) > 0 {
		t.Fatalf("a hit in a failed line: %v", p)
	}
	s := filepath.Join(h.repo, "src", "s.go")
	write(t, s, "package src\n\nfunc S() {}\n")
	h.post("mcp__socraticode__codebase_search", map[string]any{"query": "s"}, "--- src/s.go (lines 1-3) [go] score: 0.5 ---\nfunc S() {}\n")
	if p := CheckRefs(h.task(), "src/s.go:3"); len(p) > 0 {
		t.Fatalf("a SocratiCode chunk: %v", p)
	}
	// A command that writes isn't a search: its output proves nothing.
	v := filepath.Join(h.repo, "src", "v.go")
	write(t, v, "package src\n\nconst V = 1\n")
	h.post("Bash", map[string]any{"command": "echo 'src/v.go:3:const V = 1' > out.txt; cat out.txt"}, map[string]any{"stdout": "src/v.go:3:const V = 1\n"})
	if p := CheckRefs(h.task(), "src/v.go:3"); len(p) == 0 {
		t.Fatal("output of a command that writes counted as a search")
	}
	// Claude's own notes printed aren't a search.
	h.post("Bash", map[string]any{"command": "cat " + filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan)}, map[string]any{"stdout": "src/v.go:3:const V = 1\n"})
	h.post("Bash", map[string]any{"command": "grep -rn V " + TaskDocsDir(h.repo, h.task().ID)}, map[string]any{"stdout": "src/v.go:3:const V = 1\n"})
	if p := CheckRefs(h.task(), "src/v.go:3"); len(p) == 0 {
		t.Fatal("printing the notes counted as a search")
	}
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// CC2: the user's own `! git restore` between Claude's commands was blamed
// on Claude's next read-only command, and the work stopped.
func TestUserChangeBeforeAReadIsntBlamed(t *testing.T) {
	h := newHarness(t)
	h.setup()
	a := filepath.Join(h.repo, "src", "a.go")
	call := func(id, cmd string, pre bool) string {
		in := &HookInput{SessionID: h.sid, Cwd: h.repo, ToolName: "Bash", ToolUseID: id, ToolInput: rawJSON(map[string]any{"command": cmd})}
		if pre {
			PreToolUse(in)
		}
		in.ToolResponse = rawJSON(map[string]any{"stdout": ""})
		return fmtOut(PostToolUse(in))
	}
	write(t, a, "package src\n\nfunc A() int { return 9 }\n")
	call("t0", "git status --short", true) // the snapshot holds the dirty file

	// The user restores it, then Claude reads: no stop.
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	past := time.Now().Add(-3 * time.Second)
	must(t, os.Chtimes(a, past, past))
	if o := call("t1", "git status --short", true); strings.Contains(o, "changed files") {
		t.Fatalf("the user's own restore stopped the work: %s", o)
	}
	// A change made while the read ran still stops it (it may not be read-only after all).
	in := &HookInput{SessionID: h.sid, Cwd: h.repo, ToolName: "Bash", ToolUseID: "t2", ToolInput: rawJSON(map[string]any{"command": "git status --short"})}
	PreToolUse(in)
	write(t, a, "package src\n\nfunc A() int { return 7 }\n")
	in.ToolResponse = rawJSON(map[string]any{"stdout": ""})
	if o := fmtOut(PostToolUse(in)); !strings.Contains(o, "changed files") {
		t.Fatalf("a change during the read should stop the work: %s", o)
	}
	// A command that writes keeps the full check, whenever the change came.
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	must(t, os.Chtimes(a, past, past))
	if o := call("t3", "touch notes.txt", true); !strings.Contains(o, "src/a.go") {
		t.Fatalf("a writing command should still be checked: %s", o)
	}
}
