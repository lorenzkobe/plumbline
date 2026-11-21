package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for W1 (0.6.7): the hotfix flow, and the task's start commit
// following a checkout or pull until its first commit.

func hotfixHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("PLUMBLINE_SKIP_DOCKER", "1")
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.TestRelated = "./test.sh {files}"
	s.Commands.Lint = "./lint.sh"
	s.Commands.Build = "./build.sh"
	s.IgnoreChanges = []string{"coverage/**"}
	for _, f := range []string{"lint.sh", "build.sh"} {
		write(t, filepath.Join(h.repo, f), "#!/bin/sh\necho ok\n")
		must(t, os.Chmod(filepath.Join(h.repo, f), 0o755))
	}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json", "lint.sh", "build.sh")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	return h
}

func hotfixTask(t *testing.T) *harness {
	t.Helper()
	h := hotfixHarness(t)
	o := expand(h, "hotfix", "PROJ-12: checkout returns 500")
	if !strings.Contains(fmtOut(o), "hotfix task") {
		t.Fatalf("the hotfix didn't start: %s", fmtOut(o))
	}
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	return h
}

// lintBuild runs the saved lint and build on the current code.
func (h *harness) lintBuild() {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	_, err := Execute(h.repo, h.task(), KindLint, "", s.Commands.Lint, s, io.Discard)
	must(h.t, err)
	_, err = Execute(h.repo, h.task(), KindBuild, "", s.Commands.Build, s, io.Discard)
	must(h.t, err)
}

const hotfixPlan = goodPlan + "## Root cause\n- A returned 1 (src/a.go:3); this fix is the root-cause fix\n"

func approvedHotfix(t *testing.T) *harness {
	t.Helper()
	h := hotfixTask(t)
	h.docs(DocPlan, hotfixPlan)
	h.approve()
	h.redRun()
	return h
}

func TestHotfixStartsASmallTaskForThisSession(t *testing.T) {
	h := hotfixHarness(t)
	o := expand(h, "hotfix", "PROJ-12: checkout returns 500\nsince the 14:00 deploy")
	tk := h.task()
	if tk == nil {
		t.Fatalf("no task bound to the session: %s", fmtOut(o))
	}
	if !tk.Hotfix() || tk.Size != "small" || !tk.SizeByUser || tk.Ticket != "PROJ-12" || tk.Title != "PROJ-12: checkout returns 500" || tk.SessionID != h.sid {
		t.Fatalf("hotfix task: %+v", tk)
	}
	if tk.StartCommit != HeadCommit(h.repo) || tk.Phase != PhaseUnderstanding {
		t.Fatalf("start commit %q phase %q", tk.StartCommit, tk.Phase)
	}
	if _, err := os.Stat(TaskDocsDir(h.repo, tk.ID)); err != nil {
		t.Fatalf("no notes folder: %v", err)
	}
	line := fmtOut(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: "go"}))
	if !strings.Contains(line, "hotfix, small") || !strings.Contains(line, "Root cause") {
		t.Fatalf("the task line should say it's a hotfix and what it needs: %s", line)
	}
	// A second one in the same session is refused, and nothing new is made.
	if o := fmtOut(expand(h, "hotfix", "another outage")); !strings.Contains(o, "already has task "+tk.ID) {
		t.Fatalf("a second hotfix in the session should be refused: %s", o)
	}
	// Another session in the same checkout is refused too (M14).
	if o := fmtOut(expand(h.session("sess-2"), "hotfix", "another outage")); !strings.Contains(o, "another Claude window is working on") || !strings.Contains(o, "claude -w") { // LIVE1: that window is still open
		t.Fatalf("a hotfix beside another session's task should be refused: %s", o)
	}
	if n := len(ListTasks(h.repo)); n != 1 {
		t.Fatalf("refused hotfixes made tasks: %d", n)
	}
}

func TestHotfixRefusals(t *testing.T) {
	t.Setenv("PLUMBLINE_SKIP_DOCKER", "1")
	h := newHarness(t)
	if o := fmtOut(expand(h, "hotfix", "checkout is down")); !strings.Contains(o, "no Plumbline setup") {
		t.Fatalf("no setup: %s", o)
	}
	h = hotfixHarness(t)
	if o := fmtOut(expand(h, "hotfix", "  ")); !strings.Contains(o, "say what's broken") {
		t.Fatalf("empty description: %s", o)
	}
	must(t, SetPaused(h.repo, true))
	if o := fmtOut(expand(h, "hotfix", "checkout is down")); !strings.Contains(o, "paused") {
		t.Fatalf("paused: %s", o)
	}
	must(t, SetPaused(h.repo, false))
	p, _ := SetupPath(h.repo)
	write(t, p, `{"version":1,"kind":"personal","base_branch":"main","commands":{"test":"true"}}`)
	if o := fmtOut(expand(h, "hotfix", "checkout is down")); !strings.Contains(o, "changed since you confirmed") {
		t.Fatalf("unconfirmed setup: %s", o)
	}
	must(t, ConfirmSetup(h.repo))
	h.startTask("small")
	if o := fmtOut(expand(h, "hotfix", "checkout is down")); !strings.Contains(o, "already has task") {
		t.Fatalf("open task in this session: %s", o)
	}
	if o := fmtOut(UserPromptExpansion(&HookInput{Cwd: h.repo, CommandName: "/plumbline:hotfix", CommandArgs: "down"})); !strings.Contains(o, "which session") {
		t.Fatalf("no session id: %s", o)
	}
	out := t.TempDir()
	o := fmtOut(UserPromptExpansion(&HookInput{SessionID: "s9", Cwd: out, CommandName: "/plumbline:hotfix", CommandArgs: "down"}))
	if !strings.Contains(o, "not inside a git repo") {
		t.Fatalf("outside a repo: %s", o)
	}
	if n := len(ListTasks(h.repo)); n != 1 {
		t.Fatalf("refused hotfixes made tasks: %d", n)
	}
}

func TestHotfixTitleAndTicket(t *testing.T) {
	for _, c := range []struct{ in, title, ticket string }{
		{"checkout 500s (PROJ-12)", "checkout 500s (PROJ-12)", "PROJ-12"},
		{"checkout   is\tdown", "checkout is down", ""},
		{"line one\nPROJ-3 in the body", "line one", "PROJ-3"},
		{"proj-12 lower case isn't a ticket", "proj-12 lower case isn't a ticket", ""},
	} {
		if title, ticket := hotfixTitle(c.in, &Setup{}); title != c.title || ticket != c.ticket {
			t.Errorf("hotfixTitle(%q) = %q, %q; want %q, %q", c.in, title, ticket, c.title, c.ticket)
		}
	}
	long := strings.Repeat("é", 150)
	if title, _ := hotfixTitle(long, nil); len([]rune(title)) != hotfixTitleMax || !strings.HasSuffix(title, "...") {
		t.Errorf("a long title is cut to %d runes: %d", hotfixTitleMax, len([]rune(title)))
	}
	if _, ticket := hotfixTitle("fix for #482 now", &Setup{TicketRegex: `#\d+`}); ticket != "#482" {
		t.Errorf("the project's ticket pattern: %q", ticket)
	}
	if _, ticket := hotfixTitle("PROJ-1 down", &Setup{TicketRegex: `(`}); ticket != "" {
		t.Errorf("a broken ticket pattern finds no ticket: %q", ticket)
	}
}

func TestHotfixHasOneCheckpointAndARootCause(t *testing.T) {
	h := hotfixTask(t)
	cp1 := map[string]any{"questions": []any{map[string]any{"question": "Understood?", "header": "Checkpoint 1",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}}}}}
	h.expect(h.pre("AskUserQuestion", cp1), "deny", "one checkpoint")
	h.docs(DocPlan, goodPlan)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "Root cause")
	h.docs(DocPlan, goodPlan+"## Root cause\n\n## Checks\n- related tests\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "Root cause") // an empty section isn't one
	h.docs(DocPlan, hotfixPlan)
	h.approve() // small: no challenger needed
	// A normal task needs no root cause.
	n := groundedTask(t, "small")
	n.docs(DocPlan, goodPlan)
	n.approve()
}

func TestHotfixFirstCommitNeedsRelatedTestsNotTheSuite(t *testing.T) {
	h := approvedHotfix(t)
	s, _, _ := LoadSetup(h.repo)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "related tests")
	_, err := ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "lint")
	h.lintBuild()
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "hotfix commit")
	// The suite failing doesn't matter for this one; a failing related run does.
	write(t, filepath.Join(h.repo, "FAIL"), "x")
	h.post("Bash", map[string]any{"command": "touch FAIL"}, map[string]any{})
	_, err = ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "related tests")
	must(t, os.Remove(filepath.Join(h.repo, "FAIL")))
	h.post("Bash", map[string]any{"command": "rm FAIL"}, map[string]any{})
	_, err = ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)

	// The hotfix commit runs.
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "hotfix commit")
	run(t, h.repo, "git", "commit", "-qam", "fix: A returns 2")
	h.post("Bash", map[string]any{"command": `git commit -am "fix: A returns 2"`}, map[string]any{"stdout": "[main 1a2b3c4] fix: A returns 2\n"})
	if !h.task().HotfixCommitted() {
		t.Fatal("the hotfix commit wasn't recorded")
	}
	line := fmtOut(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: "deployed"}))
	if !strings.Contains(line, "Hotfix committed: run the full suite and every reviewer now") {
		t.Fatalf("after the commit the task line says what's next: %s", line)
	}
	// The finish check needs the full suite and the reviewer.
	if m := h.finishMissing(); !strings.Contains(m, "full test suite") || !strings.Contains(m, "plumbline:code-reviewer") {
		t.Fatalf("after the hotfix commit, the suite and the review are still needed: %s", m)
	}
	// Any later commit is a normal one: the last one needs the full suite.
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err = ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)
	h.lintBuild()
	h.expect(h.bash(`git commit -am "fix: A returns 3"`), "deny", "full test suite")
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", s, io.Discard)
	must(t, err)
	h.expect(h.bash(`git commit -am "fix: A returns 3"`), "ask", "last commit of the task")
}

// A normal task's last commit still needs the full suite, with related tests
// passing (the relaxation is the hotfix's alone).
func TestNormalLastCommitStillNeedsTheSuite(t *testing.T) {
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
	_, err := ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)
	h.lintBuild()
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "full test suite")
}

func TestHotfixReportNeedsTheRootCause(t *testing.T) {
	h := approvedHotfix(t)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.lintBuild()
	h.runHelper(HelperCodeReviewer, "# Code review 1\nnothing at 80 or more\n")
	report := "# Report\n" + reportEnd + "## Review\n- code-review-1: nothing at 80 or more\n## For you\n- nothing\n"
	h.docs(DocReport, report)
	if m := h.finishMissing(); !strings.Contains(m, `"## Root cause"`) {
		t.Fatalf("a hotfix report without the root cause should be refused: %s", m)
	}
	h.docs(DocReport, report+"## Root cause\n- A returned 1 (src/a.go:3): fixed here\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("a complete hotfix report: %s", m)
	}
}

// The start commit follows Claude's checkout, switch or pull until the
// task's first commit, so the diff holds only the task's own changes.
func TestStartCommitFollowsCheckoutUntilFirstCommit(t *testing.T) {
	h := newHarness(t)
	h.setup()
	mainHead := HeadCommit(h.repo)
	run(t, h.repo, "git", "checkout", "-q", "-b", "develop")
	write(t, filepath.Join(h.repo, "src", "dev.go"), "package src\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "feat: dev only")
	devHead := HeadCommit(h.repo)
	tk := h.startTask("small")
	tk.StartCommit, tk.StartTips = devHead, BranchTips(h.repo)
	must(t, SaveTask(tk))
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- nothing\n")
	h.docs(DocPlan, goodPlan)
	h.approve() // branches switch after the checkpoint

	claude := func(cmd string, gitArgs ...string) {
		t.Helper()
		if d, r := decision(h.bash(cmd)); d == "deny" {
			t.Fatalf("%s refused: %s", cmd, r)
		}
		if len(gitArgs) > 0 {
			run(t, h.repo, append([]string{"git"}, gitArgs...)...)
		}
		h.post("Bash", map[string]any{"command": cmd}, map[string]any{})
	}
	claude("git checkout -q main", "checkout", "-q", "main")
	if got := h.task().StartCommit; got != mainHead {
		t.Fatalf("start commit = %s, want main's %s", got, mainHead)
	}
	if ch := ChangedSince(h.repo, h.task().StartCommit); len(ch) != 0 {
		t.Fatalf("develop's commits count as the task's changes: %v", ch)
	}
	if h.task().HeadBefore != "" {
		t.Fatal("the HEAD noted before the command is cleared after it")
	}

	// A commit the user makes in their own terminal doesn't move it, whatever
	// Claude runs next.
	write(t, filepath.Join(h.repo, "src", "user.go"), "package src\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "feat: the user's own")
	claude("ls")
	claude("git checkout -- src/a.go", "checkout", "--", "src/a.go")
	if got := h.task().StartCommit; got != mainHead {
		t.Fatalf("the user's own commit moved the start commit: %s", got)
	}

	// A line that failed after its checkout still moved HEAD.
	cmd := "git checkout -q develop && false"
	if d, r := decision(h.bash(cmd)); d == "deny" {
		t.Fatalf("%s refused: %s", cmd, r)
	}
	run(t, h.repo, "git", "checkout", "-q", "develop")
	PostToolUseFailure(h.in("Bash", map[string]any{"command": cmd}, nil))
	if got := h.task().StartCommit; got != devHead {
		t.Fatalf("a failed line's checkout: start commit = %s, want %s", got, devHead)
	}

	// After the task's first commit it stays put.
	tk = h.task()
	tk.Commits = 1
	must(t, SaveTask(tk))
	claude("git checkout -q main", "checkout", "-q", "main")
	if got := h.task().StartCommit; got != devHead {
		t.Fatalf("after the task's commit the start commit moved: %s", got)
	}
}

// A checkout in another repo (git -C) doesn't move this task's start commit.
func TestStartCommitIgnoresOtherRepos(t *testing.T) {
	h := buildingTask(t)
	tk := h.task()
	start := HeadCommit(h.repo)
	tk.StartCommit = start
	must(t, SaveTask(tk))
	other := t.TempDir()
	run(t, other, "git", "init", "-q", "-b", "main")
	run(t, other, "git", "-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-q", "--allow-empty", "-m", "init")
	run(t, other, "git", "checkout", "-q", "-b", "x")
	cmd := "git -C " + other + " checkout -q main"
	h.bash(cmd)
	run(t, other, "git", "checkout", "-q", "main")
	// This repo's HEAD moves too, by the user, while that command ran.
	write(t, filepath.Join(h.repo, "src", "user.go"), "package src\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "feat: the user's own")
	h.post("Bash", map[string]any{"command": cmd}, map[string]any{})
	if got := h.task().StartCommit; got != start {
		t.Fatalf("another repo's checkout moved this task's start commit: %s", got)
	}
}

// buildingAt: a building task whose start commit and branch tips are
// recorded as plumbline task start records them.
func buildingAt(t *testing.T) *harness {
	t.Helper()
	h := buildingTask(t)
	tk := h.task()
	tk.StartCommit, tk.StartTips = HeadCommit(h.repo), BranchTips(h.repo)
	must(t, SaveTask(tk))
	return h
}

// claudeRuns is one of the lead's terminal commands, as the hooks see it:
// the gate, the command (run here as git with gitArgs), then the post hook.
func (h *harness) claudeRuns(cmd string, gitArgs ...string) {
	h.t.Helper()
	if d, r := decision(h.bash(cmd)); d == "deny" {
		h.t.Fatalf("%s refused: %s", cmd, r)
	}
	if len(gitArgs) > 0 {
		run(h.t, h.repo, append([]string{"git"}, gitArgs...)...)
	}
	h.post("Bash", map[string]any{"command": cmd}, map[string]any{})
}

// A stash holds the task's edits as a commit; checking it out (or a branch
// made from it) must not make it the start commit, or the edits drop out
// of the review.
func TestStartCommitDoesntFollowAStash(t *testing.T) {
	h := buildingAt(t)
	start := h.task().StartCommit
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.claudeRuns("git stash", "stash", "-q")
	h.claudeRuns("git checkout -q stash@{0}", "checkout", "-q", "stash@{0}")
	if got := h.task().StartCommit; got != start {
		t.Fatalf("the start commit followed a stash: %s", got)
	}
	h.claudeRuns("git branch tmp stash@{0}", "branch", "tmp", "stash@{0}")
	h.claudeRuns("git checkout -q tmp", "checkout", "-q", "tmp")
	if got := h.task().StartCommit; got != start {
		t.Fatalf("the start commit followed a branch made from a stash: %s", got)
	}
	if ch := ChangedSince(h.repo, h.task().StartCommit); !contains(ch, "src/a.go") {
		t.Fatalf("the task's edit must stay in its diff: %v", ch)
	}
}

// Teammates' commits a pull brings in are on the remote-tracking branch, so
// the start commit follows them and they aren't the task's.
func TestStartCommitFollowsAPull(t *testing.T) {
	h := buildingAt(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	run(t, h.repo, "git", "init", "-q", "--bare", remote)
	run(t, h.repo, "git", "remote", "add", "origin", remote)
	run(t, h.repo, "git", "push", "-q", "-u", "origin", "main")
	mate := filepath.Join(t.TempDir(), "mate")
	run(t, h.repo, "git", "clone", "-q", remote, mate)
	write(t, filepath.Join(mate, "src", "mate.go"), "package src\n")
	run(t, mate, "git", "add", "-A")
	run(t, mate, "git", "-c", "user.email=m@example.com", "-c", "user.name=M", "commit", "-q", "-m", "feat: a teammate's")
	run(t, mate, "git", "push", "-q", "origin", "main")
	h.claudeRuns("git pull -q", "pull", "-q", "--ff-only")
	if got, want := h.task().StartCommit, HeadCommit(mate); got != want {
		t.Fatalf("start commit = %s, want the pulled %s", got, want)
	}
	if ch := ChangedSince(h.repo, h.task().StartCommit); contains(ch, "src/mate.go") {
		t.Fatalf("a teammate's file counts as the task's: %v", ch)
	}
}

// A commit inside a line that then failed is counted, so the start commit
// stops following and a hotfix's relaxation isn't given twice.
func TestCommitInAFailedLineCounts(t *testing.T) {
	h := provenTask(t)
	cmd := `git commit -qam "fix: A returns 2" && false`
	h.expect(h.bash(cmd), "ask", "")
	run(t, h.repo, "git", "commit", "-qam", "fix: A returns 2")
	PostToolUseFailure(h.in("Bash", map[string]any{"command": cmd}, nil))
	if n := h.task().Commits; n != 1 {
		t.Fatalf("commits = %d, want 1", n)
	}
	// A failed line whose commit didn't happen counts nothing.
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.expect(h.bash(cmd), "ask", "")
	PostToolUseFailure(h.in("Bash", map[string]any{"command": cmd}, nil))
	if n := h.task().Commits; n != 1 {
		t.Fatalf("a commit that didn't happen was counted: %d", n)
	}
}

// A command running beside the checkout doesn't cancel its follow.
func TestStartCommitFollowWithAParallelCall(t *testing.T) {
	h := newHarness(t)
	h.setup()
	mainHead := HeadCommit(h.repo)
	run(t, h.repo, "git", "checkout", "-q", "-b", "develop")
	write(t, filepath.Join(h.repo, "src", "dev.go"), "package src\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "feat: dev only")
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- nothing\n")
	h.docs(DocPlan, goodPlan)
	h.approve()
	tk := h.task()
	tk.StartCommit, tk.StartTips = HeadCommit(h.repo), BranchTips(h.repo)
	must(t, SaveTask(tk))
	h.bash("git checkout -q main")
	h.bash("ls")
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	run(t, h.repo, "git", "checkout", "-q", "main")
	h.post("Bash", map[string]any{"command": "git checkout -q main"}, map[string]any{})
	if got := h.task().StartCommit; got != mainHead {
		t.Fatalf("a parallel ls cancelled the follow: %s", got)
	}
}
