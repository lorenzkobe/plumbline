package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// l83Task: a building task on the harness repo's approved code, with a
// BaseTree and a first change made.
func l83Task(t *testing.T) (*harness, *Task, string) {
	t.Helper()
	h := newHarness(t)
	fp, err := Fingerprint(h.repo, nil)
	must(t, err)
	tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Phase: PhaseBuilding, Created: Now(),
		StartCommit: HeadCommit(h.repo), ApprovalPrint: fp, BaseTree: fp, LastCodeChange: Now(), Rules083: true}
	must(t, os.MkdirAll(TaskDocsDir(h.repo, tk.ID), 0o700))
	must(t, SaveTask(tk))
	return h, tk, fp
}

func l83Run(tk *Task, kind, cmd, tree string, exit, ran, failed, skipped int) *Run {
	return &Run{ID: NewID() + "-" + kind, TaskID: tk.ID, Kind: kind, Command: cmd, Started: Now(), Finished: Now(),
		PrintBefore: tree, PrintAfter: tree, Exit: exit, Ran: ran, Failed: failed, Skipped: skipped}
}

// C27: a passing full suite with skips asks for a base run; a related run
// with skips doesn't, and none once a test baseline exists.
func TestL83PassingSuiteWithSkipsGetsABaseRun(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Test = "./test.sh"
	if !baseRunWanted(h.repo, tk, l83Run(tk, KindTest, "./test.sh", fp, 0, 5, 0, 2), s) {
		t.Fatal("a passing suite with skipped tests and no baseline needs a base run, so old skips aren't the task's")
	}
	if baseRunWanted(h.repo, tk, l83Run(tk, KindTest, "./test.sh", fp, 0, 5, 0, 0), s) {
		t.Fatal("a clean pass needs no base run")
	}
	if baseRunWanted(h.repo, tk, l83Run(tk, KindRelated, "./rel.sh x", fp, 0, 5, 0, 2), s) {
		t.Fatal("a passing related run's skips aren't compared")
	}
	b := l83Run(tk, KindTest, "./test.sh", fp, 0, 5, 0, 2)
	b.Baseline = true
	must(t, appendRun(h.repo, b))
	if baseRunWanted(h.repo, tk, l83Run(tk, KindTest, "./test.sh", fp, 1, 5, 1, 0), s) {
		t.Fatal("with a test baseline there is nothing to compare on the base")
	}
}

// C9, C11: no base run for a timed-out run, a hotfix, a task with no
// BaseTree, or before the task's first change.
func TestL83BaseRunNotWanted(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Test = "./test.sh"
	failing := func() *Run { return l83Run(tk, KindTest, "./test.sh", fp, 1, 5, 1, 0) }
	r := failing()
	r.TimedOut = true
	if baseRunWanted(h.repo, tk, r, s) {
		t.Fatal("a timed-out run isn't compared on the base")
	}
	for name, change := range map[string]func(*Task){
		"hotfix":       func(x *Task) { x.Kind = KindHotfix },
		"no BaseTree":  func(x *Task) { x.BaseTree = "" },
		"no change":    func(x *Task) { x.LastCodeChange = time.Time{} },
		"not building": func(x *Task) { x.Phase = PhasePlanning },
	} {
		cp := *tk
		change(&cp)
		if baseRunWanted(h.repo, &cp, failing(), s) {
			t.Errorf("%s: no base run", name)
		}
	}
}

// C10: a link is never made through a symlinked folder.
func TestL83NoLinkThroughASymlinkedFolder(t *testing.T) {
	wt := t.TempDir()
	outside := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(wt, "pkg"), 0o755))
	must(t, os.Symlink(outside, filepath.Join(wt, "linked")))
	if !plainDirs(wt, filepath.Join(wt, "pkg")) || !plainDirs(wt, wt) {
		t.Fatal("real folders inside the worktree are fine")
	}
	if plainDirs(wt, filepath.Join(wt, "linked")) || plainDirs(wt, filepath.Join(wt, "linked", "deeper")) {
		t.Fatal("a symlinked folder (or one under it) must not get a link")
	}
	if plainDirs(wt, filepath.Join(wt, "..")) {
		t.Fatal("a folder outside the worktree must not get a link")
	}
}

// C15, C16, C18, C36: the shared skip rule. Only the typecheck skips, and
// only after markdown notes changed.
func TestL83CheckCarries(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Build, s.Commands.Lint, s.Commands.Typecheck, s.Commands.Security = "make build", "make lint", "tsc --noEmit", "make audit"
	var runs []*Run
	for _, k := range []string{KindBuild, KindLint, KindTypecheck, KindSecurity} {
		runs = append(runs, l83Run(tk, k, savedCommand(s, k), fp, 0, 1, 0, 0))
	}
	at := func(rel, body string) string {
		write(t, filepath.Join(h.repo, rel), body)
		now, err := Fingerprint(h.repo, nil)
		must(t, err)
		return now
	}
	note := at("docs/notes.md", "# notes\n")
	if !CheckCarries(tk, s, runs, KindTypecheck, note) {
		t.Fatal("the typecheck carries over a markdown-only change")
	}
	for _, k := range []string{KindBuild, KindLint, KindSecurity} {
		if CheckCarries(tk, s, runs, k, note) {
			t.Errorf("%s reads markdown (docs builds, markdownlint, secret scanners), so it never carries", k)
		}
	}
	hot := *tk
	hot.Kind = KindHotfix
	if CheckCarries(&hot, s, runs, KindTypecheck, note) {
		t.Fatal("a hotfix's first commit needs every saved check, so nothing carries")
	}
	failedNow := l83Run(tk, KindTypecheck, "tsc --noEmit", note, 2, 1, 0, 0)
	if CheckCarries(tk, s, append(append([]*Run{}, runs...), failedNow), KindTypecheck, note) {
		t.Fatal("a failing run on the current code wins over an older pass")
	}
	if CheckCarries(tk, s, []*Run{l83Run(tk, KindTypecheck, "tsc --noEmit", strings.Repeat("1", 40), 0, 1, 0, 0)}, KindTypecheck, note) {
		t.Fatal("git can't diff a missing tree, so nothing carries")
	}
	if CheckCarries(tk, s, []*Run{l83Run(tk, KindTypecheck, "tsc --noEmit", fp, 2, 1, 0, 0)}, KindTypecheck, note) {
		t.Fatal("only a pass carries")
	}
	onBase := l83Run(tk, KindTypecheck, "tsc --noEmit", fp, 0, 1, 0, 0)
	onBase.OnBase = true
	if CheckCarries(tk, s, []*Run{onBase}, KindTypecheck, note) {
		t.Fatal("a base run is never the task's pass")
	}
	if code := at("src/a_test.go", "package src\n"); CheckCarries(tk, s, runs, KindTypecheck, code) {
		t.Fatal("tsc reads tests, so a test change reruns the typecheck")
	}
	for name, want := range map[string]bool{"notes.md": true, "x.markdown": true, "requirements.txt": false, "page.mdx": false} {
		if got := markdownNote(name); got != want {
			t.Errorf("markdownNote(%q) = %v, want %v", name, got, want)
		}
	}
}

// review H4: only test runs get a base run, and a stopped one may be made again.
func TestL83BaseRunsAreForTestsOnly(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Test, s.Commands.Lint = "./test.sh", "make lint"
	if baseRunWanted(h.repo, tk, l83Run(tk, KindLint, "make lint", fp, 1, 1, 0, 0), s) {
		t.Fatal("a failing lint isn't compared on the copy: it may fail there for what the copy lacks")
	}
	stopped := l83Run(tk, KindTest, "./test.sh", tk.BaseTree, 0, -1, 0, 0)
	stopped.OnBase, stopped.Baseline, stopped.Stopped = true, true, true
	must(t, appendRun(h.repo, stopped))
	if !baseRunWanted(h.repo, tk, l83Run(tk, KindTest, "./test.sh", fp, 1, 5, 1, 0), s) {
		t.Fatal("a stopped base run doesn't block a new one")
	}
}

// review H4: a dependency folder that reaches the repo's own code stops the base run.
func TestL83WorkspaceAndEditableInstallsStopTheBaseRun(t *testing.T) {
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules")
	must(t, os.MkdirAll(filepath.Join(root, "packages", "b"), 0o755))
	must(t, os.MkdirAll(filepath.Join(nm, "left-pad"), 0o755))
	if why := pointsIntoRepo(root, nm); why != "" {
		t.Fatalf("plain packages are fine: %s", why)
	}
	must(t, os.MkdirAll(filepath.Join(nm, ".pnpm", "react@18", "node_modules", "react"), 0o755))
	must(t, os.Symlink(filepath.Join(".pnpm", "react@18", "node_modules", "react"), filepath.Join(nm, "react")))
	if why := pointsIntoRepo(root, nm); why != "" {
		t.Fatalf("pnpm's links into its own store are packages, not the repo's code: %s", why)
	}
	must(t, os.MkdirAll(filepath.Join(nm, "@acme"), 0o755))
	must(t, os.Symlink(filepath.Join("..", "..", "packages", "b"), filepath.Join(nm, "@acme", "b")))
	if why := pointsIntoRepo(root, nm); !strings.Contains(why, "@acme/b") {
		t.Fatalf("a scoped workspace link into the repo stops it: %q", why)
	}
	venv := filepath.Join(root, ".venv")
	sp := filepath.Join(venv, "lib", "python3.12", "site-packages")
	must(t, os.MkdirAll(sp, 0o755))
	write(t, filepath.Join(sp, "distutils-precedence.pth"), "import os\n")
	write(t, filepath.Join(sp, "own.pth"), filepath.Join(sp, "vendored")+"\n")
	if why := pointsIntoRepo(root, venv); why != "" {
		t.Fatalf("paths inside the virtualenv are its own packages: %s", why)
	}
	write(t, filepath.Join(sp, "__editable__.app-0.1.pth"), filepath.Join(root, "src")+"\n")
	if why := pointsIntoRepo(root, venv); !strings.Contains(why, "editable") {
		t.Fatalf("an editable install of the repo stops it: %q", why)
	}
}

// A base run on the approved tree is never the task's proof, even when the
// code is back to that tree.
func TestL83BaseRunIsNeverProof(t *testing.T) {
	_, tk, fp := l83Task(t)
	b := l83Run(tk, KindTest, "./test.sh", fp, 0, 5, 0, 0)
	b.OnBase, b.Baseline = true, true
	if LatestFor([]*Run{b}, tk.ID, KindTest, "", "./test.sh", fp) != nil {
		t.Fatal("LatestFor must skip base runs")
	}
}

// A replanned task approved before 0.8.3 keeps the old reviewer rules on
// both sides of its next approval.
func TestL83ReplannedOldTaskKeepsOldRules(t *testing.T) {
	_, tk, _ := l83Task(t)
	tk.Rules083, tk.Phase = false, PhasePlanning
	if tk.newReviewRules() {
		t.Fatal("an approved-before task in a replan keeps the old rules")
	}
	tk.ApprovalPrint = ""
	if !tk.newReviewRules() {
		t.Fatal("a never-approved task gets the 0.8.3 rules")
	}
}

// H2.1: a failing run in a builder's copy never makes the task's base run.
func TestL83NoBaseRunFromABuilderCopy(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Test = "./test.sh"
	failing := l83Run(tk, KindTest, "./test.sh", fp, 1, 5, 1, 0)
	if !baseRunWanted(h.repo, tk, failing, s) {
		t.Fatal("fixture: the main checkout's failing run wants a base run")
	}
	copyRoot := BuilderWorktree(h.repo, tk.ID, "agent1")
	if baseRunWanted(copyRoot, tk, failing, s) {
		t.Fatal("a builder copy's failing run must not make the task's base run")
	}
	if baseRunWanted(filepath.Join(t.TempDir(), "elsewhere"), tk, failing, s) {
		t.Fatal("a run in another checkout must not make the task's base run")
	}
}

// before083: mark the harness task as one approved before 0.8.3 (no
// approved tree for base runs, the old reviewer rules), for tests of the
// rules such tasks and hotfixes keep.
func (h *harness) before083() {
	h.t.Helper()
	tk := h.task()
	tk.Rules083, tk.BaseTree = false, ""
	must(h.t, SaveTask(tk))
}

// review H4: after a branch switch, the note to run every check first
// comes only when there is no approved tree to compare on.
func TestL83SwitchNoteOnlyWithoutABaseTree(t *testing.T) {
	for _, keep := range []bool{true, false} {
		h := q83Branched(t)
		if !keep {
			h.before083()
		}
		cmd := "git switch other"
		h.mustAllow(h.bash(cmd), cmd)
		run(t, h.repo, "git", "switch", "-q", "other")
		note := fmtOut(h.post("Bash", map[string]any{"command": cmd}, map[string]any{"stdout": ""}))
		says := strings.Contains(note, "saved checks now, before any edit")
		if keep && (says || !strings.Contains(note, "other than the tests")) {
			t.Fatalf("with an approved tree the switch asks only for the checks other than the tests: %s", note)
		}
		if !keep && !says {
			t.Fatalf("without an approved tree the switch note asks for the checks first: %s", note)
		}
	}
}

// review H5: "commit when green" never covers an amend, or a report whose
// verdict leaves something for the user.
func TestL83CommitWhenGreenAsksForAmendsAndLimits(t *testing.T) {
	h := q83GreenTask(t, q83Green)
	if d, r := decision(h.bash(`git commit --amend -am "fix: A returns 2"`)); d != "ask" && d != "deny" {
		t.Fatalf("an amend rewrites a commit the user made, so it asks: %q (%s)", d, r)
	}
	limited := strings.Replace(reportEnd, "Ready: proven", "Ready with noted limitations: one finding is your call", 1)
	h.docs(DocReport, "# Report\n"+limited+"## Review\n- code-review-1: nothing at 80 or more\n## For you\n- the finding is your call\n")
	if d, r := decision(h.bash(`git commit -am "fix: A returns 2"`)); d != "ask" && d != "deny" {
		t.Fatalf("a verdict that isn't plain Ready leaves the user a call, so the commit asks: %q (%s)", d, r)
	}
}

// review H5: a finding that starts with "None of ..." isn't a clean review.
func TestL83NoneOfIsAFinding(t *testing.T) {
	h := q83Carry(t)
	sec := h.task().lastCounted(HelperSecurity)
	if sec == nil {
		t.Fatal("fixture: the security review counts")
	}
	write(t, filepath.Join(TaskDocsDir(h.repo, h.task().ID), sec.Findings), "# Security review 1\n## Findings\n- None of the new handlers check the session before reading the account (src/a.go:3 `func A() int`). Fails when: no cookie -> account returned.\n")
	h.editA("3")
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") {
		t.Fatalf("a real finding reads as one, so the specialist reruns: %s", m)
	}
}

// review H6: after a switch, the old branch's lint run isn't this code's
// baseline, so the first edit waits for one on the new code.
func TestL83SwitchNeedsTheOtherChecksAgain(t *testing.T) {
	h := newHarness(t)
	h.setup()
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Lint = "echo lint ok"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: lint saved")
	run(t, h.repo, "git", "switch", "-q", "-c", "other")
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 20 }\n")
	run(t, h.repo, "git", "commit", "-q", "-am", "other B")
	run(t, h.repo, "git", "switch", "-q", "main")
	tk := h.startTask("small")
	tk.StartCommit, tk.StartTips = HeadCommit(h.repo), BranchTips(h.repo)
	must(t, SaveTask(tk))
	h.ground()
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	var kinds []string
	for _, k := range CheckKinds {
		if savedCommand(s, k) != "" {
			kinds = append(kinds, k)
			h.runCheck(k)
		}
	}
	if len(kinds) == 0 {
		t.Fatal("fixture: the harness setup saves no lint or build")
	}
	edit := map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}
	h.mustAllow(h.pre("Edit", edit), "the checks ran on the approved code")
	cmd := "git switch other"
	h.mustAllow(h.bash(cmd), cmd)
	run(t, h.repo, "git", "switch", "-q", "other")
	h.post("Bash", map[string]any{"command": cmd}, map[string]any{"stdout": ""})
	if _, r := decision(h.pre("Edit", edit)); !strings.Contains(r, "plumbline run "+kinds[0]) {
		t.Fatalf("the runs from the branch before don't describe this code: %s", r)
	}
	for _, k := range kinds {
		h.runCheck(k)
	}
	h.mustAllow(h.pre("Edit", edit), "the checks ran on the new code")
}

// review H7: a failing related run makes the full-suite base run in its own
// call only when the saved suite time says it's short.
func TestL83RelatedRunLeavesALongBaseRunToTheSuite(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{SuiteSeconds: 900}
	s.Commands.Test, s.Commands.TestRelated = "./test.sh", "./test.sh {files}"
	r := l83Run(tk, KindRelated, "./test.sh src/a.go", fp, 1, 3, 1, 0)
	var out strings.Builder
	baseRunAfter(h.repo, tk, r, s, &out)
	if !strings.Contains(out.String(), "plumbline run test in the background") {
		t.Fatalf("a long suite's base run is left to plumbline run test: %s", out.String())
	}
	for _, b := range LoadRuns(h.repo) {
		if b.OnBase {
			t.Fatalf("no base run from the related run's call: %+v", b)
		}
	}
}

// review H8: a verdict that names limitations after other words isn't plain Ready.
func TestL83LimitedVerdictWordingsAsk(t *testing.T) {
	for _, v := range []string{"Ready for production with noted limitations: live check not run", "Ready, with noted limitations: x", "Ready with limitations: x", "Ready — but the deploy is manual"} {
		h := q83GreenTask(t, q83Green)
		end := strings.Replace(reportEnd, "Ready: proven", v, 1)
		h.docs(DocReport, "# Report\n"+end+"## Review\n- code-review-1: nothing at 80 or more\n## For you\n- the live check\n")
		if reportPlainReady(h.task()) {
			t.Fatalf("%q isn't plain Ready", v)
		}
	}
	h := q83GreenTask(t, q83Green)
	if !reportPlainReady(h.task()) {
		t.Fatal("fixture: plain Ready is plain Ready")
	}
}

// review H9: a passing suite with skips isn't announced as a failure.
func TestL83SkipsAreNotCalledAFailure(t *testing.T) {
	h, tk, fp := l83Task(t)
	s := &Setup{}
	s.Commands.Test = "./test.sh"
	var out strings.Builder
	baseRunAfter(h.repo, tk, l83Run(tk, KindTest, "./test.sh", fp, 0, 5, 0, 2), s, &out)
	if strings.Contains(out.String(), "test failed") || !strings.Contains(out.String(), "passed with 2 skipped") {
		t.Fatalf("the message says what happened: %s", out.String())
	}
}
