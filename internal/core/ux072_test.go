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
		return CheckPlan("## Goal\nDone when: x.\n## Files\n- src/a.go\n## Cases\n" + cs + "## Real-world proof\n- does not apply, because none\n## Needs your OK\n- none\n## Guards loosened\n- none\n")
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

	// The user restores it, then Claude reads: no stop. A change time
	// can't be set back, so the hooks' clock moves forward instead.
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	old := Now
	defer func() { Now = old }()
	shift := 3 * time.Second
	Now = func() time.Time { return old().Add(shift) }
	if o := call("t1", "git status --short", true); strings.Contains(o, "changed files") {
		t.Fatalf("the user's own restore stopped the work: %s", o)
	}
	// A change made while the read ran still stops it (it may not be read-only after all).
	shift = 0
	in := &HookInput{SessionID: h.sid, Cwd: h.repo, ToolName: "Bash", ToolUseID: "t2", ToolInput: rawJSON(map[string]any{"command": "git status --short"})}
	PreToolUse(in)
	write(t, a, "package src\n\nfunc A() int { return 7 }\n")
	in.ToolResponse = rawJSON(map[string]any{"stdout": ""})
	if o := fmtOut(PostToolUse(in)); !strings.Contains(o, "changed files") {
		t.Fatalf("a change during the read should stop the work: %s", o)
	}
	// A command that writes keeps the full check, whenever the change came.
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	shift += 3 * time.Second
	if o := call("t3", "touch notes.txt", true); !strings.Contains(o, "src/a.go") {
		t.Fatalf("a writing command should still be checked: %s", o)
	}
}

// RV1: after a test or doc fix, only the code reviewer looks again; a change
// to the app's code, or to a file that decides which checks run, sends
// every reviewer back.
func TestTestOnlyFixNeedsOnlyTheCodeReviewer(t *testing.T) {
	h := provenTask(t)
	tk := h.task()
	tk.Size = "normal" // the plan reviewer is required too (tasks before 0.8.3)
	must(t, SaveTask(tk))
	h.before083()
	for _, k := range []string{HelperCodeReviewer, HelperPlanReviewer} {
		id, _ := h.startHelper(k)
		write(t, h.findingsPath(id), h.complete(k, "# Review 1\nnothing at 80 or more\n"))
		h.stopHelper(id, k)
	}
	if m := h.finishMissing(); strings.Contains(m, "run these again") {
		t.Fatalf("fresh reviews: %s", m)
	}
	again := func() string {
		m := h.finishMissing()
		i := strings.Index(m, "run these again")
		if i < 0 {
			return ""
		}
		return strings.SplitN(m[i:], ";", 2)[0]
	}
	// A test comment and a doc: the code reviewer only.
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n\n// fixed comment\n")
	write(t, filepath.Join(h.repo, "README.md"), "# fixed\n")
	if a := again(); !strings.Contains(a, "plumbline:code-reviewer") || strings.Contains(a, "plan-reviewer") {
		t.Fatalf("after a test and doc fix: %q", a)
	}
	// A file that decides which checks run: everyone.
	write(t, filepath.Join(h.repo, "CLAUDE.md"), "# rules\n")
	if a := again(); !strings.Contains(a, "plan-reviewer") {
		t.Fatalf("after a CLAUDE.md change: %q", a)
	}
	must(t, os.Remove(filepath.Join(h.repo, "CLAUDE.md")))
	// The app's code: everyone.
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 2 } // x\n")
	if a := again(); !strings.Contains(a, "plan-reviewer") || !strings.Contains(a, "code-reviewer") {
		t.Fatalf("after a code change: %q", a)
	}
}

// DATA1, BR1: in the browser, what could save, send or delete asks first
// while the local app uses real data (or the setup doesn't say): clicks whose
// note names a change or that have no note, Enter, Delete and key
// combinations, and page scripts that could change data. Looking, opening a
// list, Escape, typing and picking a value don't.
func TestBrowserOnRealData(t *testing.T) {
	h := newHarness(t)
	h.setup() // no local_real_data: counts as real
	chrome := func(tool string, in map[string]any) string {
		d, _ := decision(h.pre("mcp__claude-in-chrome__"+tool, in))
		return d
	}
	for _, c := range []struct {
		tool string
		in   map[string]any
	}{
		{"computer", map[string]any{"action": "left_click", "coordinate": []int{10, 10}}},
		{"computer", map[string]any{"action": "left_click", "ref": "ref_9", "action_summary": "Saves the new bill"}},
		{"computer", map[string]any{"action": "left_click", "ref": "ref_9", "action_summary": "Deletes the selected group"}},
		{"computer", map[string]any{"action": "double_click", "coordinate": []int{1, 1}, "action_summary": "Signs out of the app"}},
		{"computer", map[string]any{"action": "type", "text": "x\n"}},
		{"computer", map[string]any{"action": "key", "text": "Enter"}},
		{"computer", map[string]any{"action": "key", "text": "Tab Tab Return"}},
		{"computer", map[string]any{"action": "key", "text": "cmd+s"}},
		{"computer", map[string]any{"action": "key", "text": "ctrl+Enter"}},
		{"computer", map[string]any{"action": "key", "text": "shift+Return"}},
		{"form_input", map[string]any{"ref": "a", "value": true, "action_summary": "Turns on auto-pay"}},
		{"browser_batch", map[string]any{"actions": []any{
			map[string]any{"name": "computer", "input": map[string]any{"action": "screenshot"}},
			map[string]any{"name": "computer", "input": map[string]any{"action": "left_click", "ref": "ref_2", "action_summary": "Submits the payment"}},
		}}},
		{"browser_batch", map[string]any{"actions": []any{"not a step"}}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "await fetch('/rest/v1/bills', {method: 'DELETE'})"}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "localStorage.setItem('x', '1')"}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "document.querySelector('button').click()"}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "document.querySelector('input').value = 'x'"}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "window['fet' + 'ch']('/x')"}},
		{"browser_batch", map[string]any{}},
	} {
		if d := chrome(c.tool, c.in); d != "ask" {
			t.Errorf("%s %v on real data: %q, want ask", c.tool, c.in, d)
		}
	}
	for _, c := range []struct {
		tool string
		in   map[string]any
	}{
		{"computer", map[string]any{"action": "screenshot"}},
		// BR1 (step 11c): opening a list, Escape, typing and picking a value
		// don't save anything by themselves.
		{"computer", map[string]any{"action": "left_click", "ref": "ref_34", "action_summary": "Opens the bill category dropdown list"}},
		{"computer", map[string]any{"action": "left_click", "coordinate": []int{240, 141}, "action_summary": "Opens the bill category dropdown list"}},
		{"computer", map[string]any{"action": "key", "text": "Escape", "action_summary": "Closes the category dropdown without choosing"}},
		{"computer", map[string]any{"action": "key", "text": "Tab ArrowDown"}},
		{"computer", map[string]any{"action": "key", "text": "shift+Tab"}},
		{"computer", map[string]any{"action": "type", "text": "Lunch", "action_summary": "Types Lunch into the bill title field"}},
		{"form_input", map[string]any{"ref": "a", "value": "food", "action_summary": "Picks Food in the category list"}},
		{"browser_batch", map[string]any{"actions": []any{
			map[string]any{"name": "computer", "input": map[string]any{"action": "left_click", "ref": "ref_34", "action_summary": "Opens the category list"}},
			map[string]any{"name": "mcp__claude-in-chrome__computer", "input": map[string]any{"action": "screenshot"}},
			map[string]any{"name": "computer", "input": map[string]any{"action": "key", "text": "Escape", "action_summary": "Closes the list"}},
		}}},
		{"computer", map[string]any{"action": "zoom", "region": []int{0, 0, 10, 10}}},
		{"computer", map[string]any{"action": "scroll"}},
		{"navigate", map[string]any{"url": "http://localhost:5173/app"}},
		{"read_page", map[string]any{}},
		{"get_page_text", map[string]any{}},
		{"resize_window", map[string]any{"width": 360, "height": 780}},
		{"tabs_context_mcp", map[string]any{}},
		// Measuring the page only reads it (the step 11b scripts).
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "const h = document.querySelector('header'); ({w: h.getBoundingClientRect().width, sw: document.documentElement.scrollWidth})"}},
		{"javascript_tool", map[string]any{"action": "javascript_exec", "text": "document.querySelector('header').style.width = ''; getComputedStyle(document.body).fontSize === '16px'"}},
	} {
		if d := chrome(c.tool, c.in); d == "ask" || d == "deny" {
			t.Errorf("%s %v only looks: %q", c.tool, c.in, d)
		}
	}
	// A test database: no prompts.
	no := false
	s, _, _ := LoadSetup(h.repo)
	s.LocalRealData = &no
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	must(t, ConfirmSetup(h.repo))
	if d := chrome("computer", map[string]any{"action": "left_click"}); d == "ask" {
		t.Fatal("a click asked with a test database")
	}
}

// PR2: a screenshot of the local app after the last edit is the ui proof;
// fetching a file over HTTP isn't.
func TestBrowserScreenshotIsUIProof(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, strings.Replace(goodPlan, "- does not apply, because no API or UI\n", "- ui: the header shows the line at 360px\n", 1))
	h.approve()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	ui := func() bool { return strings.Contains(h.finishMissing(), "real-world proof (ui)") }
	if !ui() {
		t.Fatal("the ui proof should be missing")
	}
	shot := func() { h.post("mcp__claude-in-chrome__computer", map[string]any{"action": "screenshot"}, "ok") }
	h.post("mcp__claude-in-chrome__navigate", map[string]any{"url": "https://example.com"}, "ok")
	shot()
	if !ui() {
		t.Fatal("a screenshot of another site counted")
	}
	h.post("mcp__claude-in-chrome__navigate", map[string]any{"url": "http://localhost:5173/app"}, "ok")
	shot()
	if ui() {
		t.Fatalf("a screenshot of the local app after the edit: %s", h.finishMissing())
	}
	// The code changes after it: a new screenshot is needed.
	write(t, a, "package src\n\nfunc A() int { return 2 } // x\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	if !ui() {
		t.Fatal("a screenshot of older code counted")
	}

	for _, c := range []string{"curl -s http://localhost:5173/src/components/common/RefreshButton.tsx -o /tmp/x.js", "wget -qO- http://localhost:5173/app", "http :5173/app"} {
		if !FetchProof(c) {
			t.Errorf("%q should not count as ui proof", c)
		}
	}
	for _, c := range []string{"npx playwright test", "curl -s http://localhost:5173/app && npx playwright test"} {
		if FetchProof(c) {
			t.Errorf("%q drives the app", c)
		}
	}
}

// FP3: "4.6:1" (a contrast ratio) isn't a file reference.
func TestNumbersAreNotFileRefs(t *testing.T) {
	refs := Refs("contrast 4.6:1 and 2.48:1, see src/a.go:3")
	if len(refs) != 1 || refs[0].Path != "src/a.go" {
		t.Fatalf("refs: %+v", refs)
	}
}

// Writing plan.md says what the plan question still needs (Serena, lines
// not opened), so it's done before the question, not after a refusal.
func TestPlanWriteSaysWhatsMissing(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	h.post("mcp__socraticode__search", nil, nil)
	h.post("mcp__mempalace__search", nil, nil)
	p := h.docs(DocPlan, smallPlan("src/a.go"))
	o := fmtOut(h.post("Write", map[string]any{"file_path": p}, nil))
	if !strings.Contains(o, "before the plan question") || !strings.Contains(o, "Serena") || !strings.Contains(o, "src/a.go:3") {
		t.Fatalf("after plan.md without Serena or the file opened: %s", o)
	}
	h.post("mcp__serena__find_referencing_symbols", nil, nil)
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	if o := fmtOut(h.post("Write", map[string]any{"file_path": p}, nil)); strings.Contains(o, "before the plan question") {
		t.Fatalf("nothing left, but: %s", o)
	}
}

// Step 10b: with no task open, a terminal command that writes a project
// file is refused like an edit (Monitor too). Git-ignored and ignore_changes
// files, the scratchpad, /tmp and a paused project aren't.
func TestNoTaskTerminalWrites(t *testing.T) {
	h := newHarness(t)
	h.setup()
	for _, c := range []string{
		"sed -i '' 's/a/b/' src/a.go", "echo x > index.html", "cp src/a.go src/b.go", "mv src/a.go src/c.go", "tee -a src/a.go",
	} {
		h.expect(h.bash(c), "deny", "no task")
	}
	h.expect(h.pre("Monitor", map[string]any{"command": "sed -i '' 's/a/b/' src/a.go"}), "deny", "no task")
	write(t, filepath.Join(h.repo, ".gitignore"), "out/\n")
	for _, c := range []string{"echo x > out/log.txt", "echo x > " + filepath.Join(os.TempDir(), "plb-x.txt"), "echo x > coverage/x.json", "git status"} {
		if d, r := decision(h.bash(c)); d == "deny" {
			t.Errorf("%q: %s", c, r)
		}
	}
	// A task open: the usual before-approval rule.
	h.startTask("small")
	h.expect(h.bash("echo x > index.html"), "deny", "plan isn't approved")
}

// QA1: on a big task the lead's code edits wait until blind QA has written
// its tests; tests, docs and a waiver don't wait, and builders work in their
// own copies.
func TestBigTaskQAFirst(t *testing.T) {
	h := groundedTask(t, "big")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	h.runHelper(HelperChallenger, "# Challenge 1\n## Findings\n- nothing\n")
	h.approve()
	a := filepath.Join(h.repo, "src", "a.go")
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "blind QA writes the tests from the plan first")
	h.expect(h.bash("sed -i '' 's/1/2/' src/a.go"), "deny", "blind QA")
	for _, f := range []string{"src/a_test.go", "README.md"} {
		if d, r := decision(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, f)})); d == "deny" {
			t.Errorf("%s: %s", f, r)
		}
	}
	h.runHelper(HelperQA, "# QA 1\n## Tests\n- src/a_test.go: fails now\n")
	if d, r := decision(h.pre("Edit", map[string]any{"file_path": a})); d == "deny" {
		t.Fatalf("after QA wrote its tests: %s", r)
	}
	// A waiver lets the code change without QA.
	g := groundedTask(t, "big")
	g.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	g.runHelper(HelperChallenger, "# Challenge 1\n## Findings\n- nothing\n")
	g.approve()
	tk := g.task()
	tk.Waivers = append(tk.Waivers, Waiver{Check: "qa", Reason: "user skipped it"})
	must(t, SaveTask(tk))
	if d, r := decision(g.pre("Edit", map[string]any{"file_path": filepath.Join(g.repo, "src", "a.go")})); d == "deny" {
		t.Fatalf("with QA waived: %s", r)
	}
}

// Looking at systems outside the laptop doesn't ask (the user's call,
// 2026-11-19); changing them, or printing a secret, does.
func TestOutsideReadsDontAsk(t *testing.T) {
	h := newHarness(t)
	h.setup()
	for _, c := range []string{
		"gh pr view 12", "gh pr list --state open", "gh run view 123 --log", "gh api repos/o/r/pulls", "gh api -X GET repos/o/r",
		"aws s3 ls s3://bucket", "aws ec2 describe-instances", "aws logs tail /aws/lambda/x", "aws sts get-caller-identity",
		"gcloud run services list", "gcloud logging read 'severity>=ERROR'", "az webapp show -n x -g y",
		"kubectl get pods -n prod", "kubectl logs deploy/api", "kubectl describe pod x",
		"vercel ls", "vercel logs https://x.vercel.app", "fly status", "heroku logs --tail -a x",
		"terraform plan", "terraform state list", "supabase projects list", "supabase functions list",
		`psql "$PROD_URL" -c "select count(*) from bills"`, `psql "$PROD_URL" -c "\dt"`, `mysql -h db.example.com -e "show tables"`,
	} {
		if d, r := decision(h.bash(c)); d == "ask" || d == "deny" {
			t.Errorf("%q only looks: %s %s", c, d, r)
		}
	}
	for _, c := range []string{
		"gh pr merge 12", "gh pr create --fill", "gh api repos/o/r/issues -f title=x", "gh api -X DELETE repos/o/r",
		"aws s3 rm s3://bucket/x", "aws ec2 terminate-instances --instance-ids i-1", "aws secretsmanager get-secret-value --secret-id x",
		"aws ssm get-parameter --name x --with-decryption",
		"kubectl delete pod x", "kubectl apply -f k.yaml", "kubectl get secret db -o yaml", "kubectl exec -it x -- sh",
		"gcloud secrets versions access latest --secret x", "vercel env pull", "heroku config -a x", "terraform apply", "terraform output",
		"supabase db push", "supabase secrets set X=1",
		`psql "$PROD_URL" -c "delete from bills"`, `psql "$PROD_URL" -c "select setval('s', 1)"`, `psql "$PROD_URL" -c "explain analyze select 1"`,
		`psql "$PROD_URL"`, `psql "$PROD_URL" -f fix.sql`, `psql "$PROD_URL" -c "with x as (delete from a returning *) select * from x"`,
		"gh pr list > prs.txt",
	} {
		if d, _ := decision(h.bash(c)); d != "ask" && d != "deny" {
			t.Errorf("%q changes something or shows a secret: %q, want ask", c, d)
		}
	}
	// Inside a task, before approval, a look runs like a local read.
	h.startTask("small")
	if d, r := decision(h.bash("gh pr view 12")); d == "deny" || d == "ask" {
		t.Fatalf("a look before approval: %s %s", d, r)
	}
}

// BLD1: the lead's cd into a builder's copy left the shell there; it's
// refused, and git -C is the way to look.
func TestLeadDoesntCdIntoABuilderCopy(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	if wt == "" {
		t.Fatal("no builder copy")
	}
	h.expect(h.bash("cd "+wt+" && git status --short && git diff"), "deny", "git -C")
	if d, r := decision(h.bash("git -C " + wt + " diff")); d == "deny" {
		t.Fatalf("git -C into the copy: %s", r)
	}
	if d, r := decision(PreToolUse(h.helperIn(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && git status"}))); d == "deny" && strings.Contains(r, "don't cd into a builder's copy") {
		t.Fatalf("the builder in its own copy: %s", r)
	}
}

// BLD2 (step 11c): the lead applied a builder's diff by hand instead of
// merging it, so the overlap check never ran. While a copy waits for the
// merge, hand merges are refused; after a merge refuses, they're the way.
func TestLeadMergesBuildersWithTheCommand(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	if wt == "" {
		t.Fatal("no builder copy")
	}
	h.expect(h.bash("git -C "+wt+" diff HEAD -- src/a.go > /tmp/partA.patch; git apply /tmp/partA.patch"), "deny", "plumbline builder merge 1")
	h.expect(h.bash("git diff "+wt+" | patch -p1"), "deny", "plumbline builder merge 1")
	h.expect(h.bash("cp "+wt+"/src/a.go src/a.go"), "deny", "plumbline builder merge 1")
	for _, ok := range []string{"git apply --check /tmp/partA.patch", "git -C " + wt + " diff", "cp src/a.go /tmp/a.go"} {
		if d, r := decision(h.bash(ok)); d == "deny" && strings.Contains(r, "builder merge") {
			t.Fatalf("%q isn't a hand merge: %s", ok, r)
		}
	}
	// The builder itself isn't held to it.
	if d, r := decision(PreToolUse(h.helperIn(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && git apply /tmp/x.patch"}))); strings.Contains(r, "builder merge") {
		t.Fatalf("the builder in its own copy: %s %s", d, r)
	}
	// After the merge refused overlapping changes, bringing parts over by
	// hand is what the merge itself says to do.
	tk := h.task()
	r, err := tk.BuilderRun(1)
	must(t, err)
	r.MergeRefused = true
	must(t, SaveTask(tk))
	if d, r := decision(h.bash("git apply /tmp/partA.patch")); d == "deny" && strings.Contains(r, "builder merge") {
		t.Fatalf("after a refused merge: %s", r)
	}
}

// BLD2: the lead and the builder are told the copy holds the lead's work.
func TestBuilderCopyHoldsTheLeadsWork(t *testing.T) {
	h := buildingTask(t)
	_, o := h.startHelper(HelperBuilder)
	if s := contextOf(o); !strings.Contains(s, "all its uncommitted changes (blind QA's tests too)") {
		t.Fatalf("builder start note: %s", s)
	}
	note := contextOf(h.post("Agent", map[string]any{"subagent_type": "plumbline:builder", "prompt": "build part A"}, nil))
	if !strings.Contains(note, "plumbline builder merge <n>") || !strings.Contains(note, "already holds your uncommitted work") {
		t.Fatalf("lead note after starting a builder: %q", note)
	}
}

// RV2 (step 11c): after a test-only fix the lead reran every reviewer. A
// review that still counts can't run again; one the change sends back can.
func TestReviewThatStillCountsDoesntRunAgain(t *testing.T) {
	h := provenTask(t)
	tk := h.task()
	tk.Size = "normal"
	must(t, SaveTask(tk))
	for _, k := range []string{HelperCodeReviewer, HelperPlanReviewer} {
		id, _ := h.startHelper(k)
		write(t, h.findingsPath(id), h.complete(k, "# Review 1\nnothing at 80 or more\n"))
		h.stopHelper(id, k)
	}
	start := func(kind string) Output {
		return h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + kind, "prompt": "Review the change against plan.md."})
	}
	// A test-only fix: the code reviewer runs, the plan reviewer doesn't.
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n\n// fixed comment\n")
	h.testsForReview()
	h.selfReview()
	h.expect(start(HelperPlanReviewer), "deny", "only tests or docs changed")
	if d, r := decision(start(HelperCodeReviewer)); d == "deny" {
		t.Fatalf("the code reviewer after a test fix: %s", r)
	}
	// A code change: both run.
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 2 } // x\n")
	h.testsForReview()
	h.selfReview()
	if d, r := decision(start(HelperPlanReviewer)); d == "deny" {
		t.Fatalf("the plan reviewer after a code change: %s", r)
	}
}

// HLP1 (step 11c): helpers are told the quote rule and their write tool up
// front, and a send-back names the tool they have.
func TestHelpersToldTheRulesUpFront(t *testing.T) {
	h := provenTask(t)
	id, o := h.startHelper(HelperCodeReviewer)
	note := contextOf(o)
	for _, want := range []string{"few words of that line in backticks", "with the Write tool"} {
		if !strings.Contains(note, want) {
			t.Fatalf("reviewer start note lacks %q: %s", want, note)
		}
	}
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: wrong value (src/a.go:3)\n"))
	if out := fmtOut(h.stopHelper(id, HelperCodeReviewer)); !strings.Contains(out, "has no quote") || !strings.Contains(out, "rewrite it whole with the Write tool") {
		t.Fatalf("send-back: %s", out)
	}
	b := buildingTask(t)
	_, qo := b.startHelper(HelperQA)
	if s := contextOf(qo); !strings.Contains(s, "npx, npm and the like are refused") || strings.Contains(s, "few words of that line") {
		t.Fatalf("QA start note: %s", s)
	}
	if s := writeHint(HelperQA); !strings.Contains(s, "Edit or Write") {
		t.Fatalf("QA has Edit: %s", s)
	}
}
