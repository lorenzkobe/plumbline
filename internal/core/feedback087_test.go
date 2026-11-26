package core

// Blind QA for release 0.8.7 (courtly feedback), written from the plan's
// case table (brief-qa-1.md), not from the new code. Test names carry the
// case id. New names used, as the lead gave them: RunBeforeFix, Run.BeforeFix
// and CleanCopies.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// --- helpers ---

// f87Reapprove moves the approval fingerprint to the code as it is now, as
// if the user had approved with these files already there.
func (h *harness) f87Reapprove() {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(h.t, err)
	tk := h.task()
	tk.ApprovalPrint = fp
	if tk.BaseTree != "" {
		tk.BaseTree = fp
	}
	must(h.t, SaveTask(tk))
}

func (h *harness) f87BeforeFix(files ...string) (*Run, error) {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	return RunBeforeFix(h.repo, h.task(), files, s, io.Discard)
}

func f87Worktrees(t *testing.T, repo string) int {
	t.Helper()
	return strings.Count(run(t, repo, "git", "worktree", "list", "--porcelain"), "worktree ")
}

// f87IsBeforeFix reads Run.BeforeFix (0.8.7) by reflection, so the rest of
// this file compiles and runs before the field lands.
func f87IsBeforeFix(r *Run) bool {
	f := reflect.ValueOf(r).Elem().FieldByName("BeforeFix")
	return f.IsValid() && f.Kind() == reflect.Bool && f.Bool()
}

func f87RunByID(root, id string) *Run {
	for _, r := range LoadRuns(root) {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// --- (1) bug-test: untracked notes and the message ---

// C1 (the courtly reproduction): the user approved with an untracked
// release note in the checkout; git stash push --include-untracked took it
// away with the fix, so the failing run's tree lacked it.
func TestF87_C1_UntrackedNoteStashedStillCountsAsSeenFailing(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	note := filepath.Join(h.repo, "RELEASE-redesign.md")
	write(t, note, "# Release redesign\n")
	h.f87Reapprove()
	must(t, os.Remove(note)) // stashed along with the fix
	h.writeTest()
	if r := h.related("src/a_test.go"); r.Failed == 0 {
		t.Fatal("the test should fail on the approved code")
	}
	write(t, note, "# Release redesign\n") // git stash pop
	h.fix("return 2")
	h.related("src/a_test.go")
	if m := bugMissing(h); m != "" {
		t.Fatalf("a failing run that only lacked an untracked note should count: %s", m)
	}
}

// C2: a non-test code file, or a tracked note, set aside differently from
// the approval disqualifies the run, and the message names that file.
func TestF87_C2_NonTestChangeDisqualifiesAndIsNamed(t *testing.T) {
	t.Run("code file", func(t *testing.T) {
		h := bugTask(t, "src/a_test.go::TestA")
		b := filepath.Join(h.repo, "src", "b.go")
		write(t, b, "package src\n\nfunc B() int { return 3 }\n")
		h.writeTest()
		if r := h.related("src/a_test.go"); r.Failed == 0 {
			t.Fatal("the test should fail without the fix")
		}
		run(t, h.repo, "git", "checkout", "--", "src/b.go")
		h.fix("return 2")
		m := bugMissing(h)
		if m == "" {
			t.Fatal("a run taken while src/b.go differed from the approval counted")
		}
		if !strings.Contains(m, "src/b.go") {
			t.Fatalf("the message should name the file that disqualified the run: %s", m)
		}
		if strings.Contains(m, "it changed after the run that saw it failing") {
			t.Fatalf("the test didn't change after its run; the message misleads: %s", m)
		}
	})
	t.Run("tracked note", func(t *testing.T) {
		h := bugTask(t, "src/a_test.go::TestA")
		note := filepath.Join(h.repo, "docs", "old.md")
		write(t, note, "# Old\n")
		run(t, h.repo, "git", "add", "docs/old.md")
		run(t, h.repo, "git", "commit", "-q", "-m", "docs: old")
		h.f87Reapprove()
		write(t, note, "# Old, edited\n")
		h.writeTest()
		if r := h.related("src/a_test.go"); r.Failed == 0 {
			t.Fatal("the test should fail without the fix")
		}
		write(t, note, "# Old\n")
		h.fix("return 2")
		m := bugMissing(h)
		if m == "" || !strings.Contains(m, "docs/old.md") {
			t.Fatalf("a tracked note set aside disqualifies the run and is named: %q", m)
		}
		if strings.Contains(m, "it changed after the run that saw it failing") {
			t.Fatalf("misleading changed-after text: %s", m)
		}
	})
}

// C3: an untracked note that tracked code names isn't a plain note.
func TestF87_C3_UntrackedNoteNamedInCodeStillDisqualifies(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n\n// The rollout steps are in PLAN-notes.md.\n")
	run(t, h.repo, "git", "add", "src/c.go")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: c")
	note := filepath.Join(h.repo, "PLAN-notes.md")
	write(t, note, "# Plan notes\n")
	h.f87Reapprove()
	must(t, os.Remove(note))
	h.writeTest()
	if r := h.related("src/a_test.go"); r.Failed == 0 {
		t.Fatal("the test should fail without the fix")
	}
	write(t, note, "# Plan notes\n")
	h.fix("return 2")
	if m := bugMissing(h); m == "" || !strings.Contains(m, "PLAN-notes.md") {
		t.Fatalf("a note named in code still disqualifies the run, and is named: %q", m)
	}
}

// --- (1) before-fix runs ---

// C4 (with C9's worktree check): the before-fix run sees the case test fail
// on the approved code, with the fix still in the checkout.
func TestF87_C4_BeforeFixRunProvesTheBugTestWithoutStash(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 2")
	if m := bugMissing(h); m == "" {
		t.Fatal("premise: no failing run yet")
	}
	status := run(t, h.repo, "git", "status", "--porcelain")
	r, err := h.f87BeforeFix("src/a_test.go")
	if err != nil {
		t.Fatalf("before-fix run refused: %v", err)
	}
	if !f87IsBeforeFix(r) || r.Failed == 0 || !failedNamed(r.FailedIDs, "TestA") {
		t.Fatalf("the before-fix run should fail TestA on the approved code: %+v", r)
	}
	if strings.Join(r.Files, ",") != "src/a_test.go" {
		t.Fatalf("the run records the tests it was given: %v", r.Files)
	}
	if got := f87RunByID(h.repo, r.ID); got == nil || !f87IsBeforeFix(got) {
		t.Fatalf("the before-fix run isn't recorded as one: %+v", got)
	}
	if got := read(t, filepath.Join(h.repo, "src", "a.go")); !strings.Contains(got, "return 2") {
		t.Fatalf("the checkout's fix was touched: %q", got)
	}
	if after := run(t, h.repo, "git", "status", "--porcelain"); after != status {
		t.Fatalf("the checkout changed:\nbefore %s\nafter %s", status, after)
	}
	if s := run(t, h.repo, "git", "stash", "list"); strings.TrimSpace(s) != "" {
		t.Fatalf("no stash should be made: %s", s)
	}
	if n := f87Worktrees(t, h.repo); n != 1 {
		t.Fatalf("the temporary worktree is left behind: %d worktrees", n)
	}
	h.related("src/a_test.go")
	if m := bugMissing(h); m != "" {
		t.Fatalf("the before-fix run should satisfy the bug-test check: %s", m)
	}
}

// C5: refused with no building task, no related command, no files, or a
// file that isn't a test; nothing is recorded.
func TestF87_C5_BeforeFixRefusals(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 2")
	s, _, _ := LoadSetup(h.repo)
	runs := len(LoadRuns(h.repo))
	try := func(name string, tk *Task, files []string, st *Setup) {
		t.Helper()
		var err error
		func() {
			defer func() {
				if p := recover(); p != nil {
					err = fmt.Errorf("panic: %v", p)
					t.Errorf("%s: panicked: %v", name, p)
				}
			}()
			_, err = RunBeforeFix(h.repo, tk, files, st, io.Discard)
		}()
		if err == nil {
			t.Errorf("%s: should be refused", name)
		}
		if n := len(LoadRuns(h.repo)); n != runs {
			t.Errorf("%s: a run was recorded (%d -> %d)", name, runs, n)
			runs = n
		}
	}
	try("no task", nil, []string{"src/a_test.go"}, s)
	unapproved := *h.task()
	unapproved.Phase = PhaseUnderstanding
	try("task not building", &unapproved, []string{"src/a_test.go"}, s)
	done := *h.task()
	done.Phase = PhaseDone
	try("task done", &done, []string{"src/a_test.go"}, s)
	noRelated := *s
	noRelated.Commands.TestRelated = ""
	try("no related command", h.task(), []string{"src/a_test.go"}, &noRelated)
	try("no files", h.task(), nil, s)
	try("empty files", h.task(), []string{}, s)
	try("a code file", h.task(), []string{"src/a.go"}, s)
	try("a test and a code file", h.task(), []string{"src/a_test.go", "src/a.go"}, s)
	if n := f87Worktrees(t, h.repo); n != 1 {
		t.Fatalf("a refused run left a worktree: %d", n)
	}
}

// C40: a related template without {files} can't run just the given tests.
func TestF87_C40_BeforeFixNeedsFilesInTheTemplate(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	s, _, _ := LoadSetup(h.repo)
	whole := *s
	whole.Commands.TestRelated = "./test.sh"
	before := len(LoadRuns(h.repo))
	if _, err := RunBeforeFix(h.repo, h.task(), []string{"src/a_test.go"}, &whole, io.Discard); err == nil {
		t.Fatal("a related command without {files} should refuse --before-fix")
	}
	if len(LoadRuns(h.repo)) != before {
		t.Fatal("a refused run was recorded")
	}
}

// C6: a test file deleted since approval is gone from the before-fix tree;
// a git error building the tree refuses.
func TestF87_C6_BeforeFixTreeDropsDeletedTestsAndRefusesOnGitError(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA", true) // tests/old failed TestOld at approval
	must(t, os.Remove(filepath.Join(h.repo, "tests", "old")))
	h.writeTest()
	h.fix("return 2")
	r, err := h.f87BeforeFix("src/a_test.go")
	if err != nil {
		t.Fatalf("before-fix run refused: %v", err)
	}
	if got := strings.Join(r.FailedIDs, ","); got != "TestA" {
		t.Fatalf("tests/old was deleted since approval, so only TestA should fail: %q", got)
	}

	tk := h.task()
	tk.ApprovalPrint = "1111111111111111111111111111111111111111"
	if tk.BaseTree != "" {
		tk.BaseTree = tk.ApprovalPrint
	}
	must(t, SaveTask(tk))
	before := len(LoadRuns(h.repo))
	if _, err := h.f87BeforeFix("src/a_test.go"); err == nil {
		t.Fatal("an approved tree git can't read should refuse")
	}
	if len(LoadRuns(h.repo)) != before {
		t.Fatal("a run was recorded after the git error")
	}
	if n := f87Worktrees(t, h.repo); n != 1 {
		t.Fatalf("a worktree is left after the git error: %d", n)
	}
}

// C7: a passing before-fix run whose tree equals the current code is still
// no related proof.
func TestF87_C7_BeforeFixRunIsNeverTheTaskProof(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	write(t, filepath.Join(h.repo, "src", "c_test.go"), "package src\n")
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	r, err := h.f87BeforeFix("src/c_test.go")
	if err != nil {
		t.Fatalf("before-fix run refused: %v", err)
	}
	if r.Failed != 0 || r.Exit != 0 || !r.Complete() {
		t.Fatalf("premise: the before-fix run passes: %+v", r)
	}
	if r.PrintBefore != fp {
		t.Fatalf("premise: only a test was added, so the before-fix tree is the current code (%s vs %s)", r.PrintBefore, fp)
	}
	runs := LoadRuns(h.repo)
	for _, cmd := range []string{"", savedCommand(s, KindRelated)} {
		if got := LatestFor(runs, h.task().ID, KindRelated, "", cmd, fp); got != nil && f87IsBeforeFix(got) {
			t.Fatalf("LatestFor(%q) returned the before-fix run as the related proof", cmd)
		}
	}
	if b := BaselineFor(runs, h.task().ID, KindRelated); b != nil && f87IsBeforeFix(b) {
		t.Fatal("the before-fix run is a baseline")
	}
	// The same tests run normally do count.
	h.related("src/c_test.go")
	if got := LatestFor(LoadRuns(h.repo), h.task().ID, KindRelated, "", "", fp); got == nil || f87IsBeforeFix(got) {
		t.Fatalf("a normal related run on the same code should be found: %+v", got)
	}
}

// C8: the case test edited after its before-fix run was never seen failing.
func TestF87_C8_TestEditedAfterBeforeFixRunNoLongerCounts(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 2")
	if r, err := h.f87BeforeFix("src/a_test.go"); err != nil || r.Failed == 0 {
		t.Fatalf("before-fix run: %+v %v", r, err)
	}
	p := filepath.Join(h.repo, "src", "a_test.go")
	write(t, p, "package src\n\nimport \"testing\"\n\n// rewritten\nfunc TestA(t *testing.T) {}\n")
	h.post("Edit", map[string]any{"file_path": p}, nil)
	if m := bugMissing(h); m == "" || !strings.Contains(m, "src/a_test.go::TestA") {
		t.Fatalf("the test changed after its before-fix run: %q", m)
	}
}

// C9: the temporary worktree goes after a passing and after a failing run.
func TestF87_C9_BeforeFixWorktreeRemoved(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	write(t, filepath.Join(h.repo, "src", "c_test.go"), "package src\n")
	if r, err := h.f87BeforeFix("src/c_test.go"); err != nil || r.Failed != 0 {
		t.Fatalf("passing before-fix run: %+v %v", r, err)
	}
	if n := f87Worktrees(t, h.repo); n != 1 {
		t.Fatalf("after a passing run: %d worktrees", n)
	}
	h.writeTest()
	h.fix("return 2") // review round 2: the tests must pass with the fix in a copy too
	if r, err := h.f87BeforeFix("src/a_test.go"); err != nil || r.Failed == 0 {
		t.Fatalf("failing before-fix run: %+v %v", r, err)
	}
	if n := f87Worktrees(t, h.repo); n != 1 {
		t.Fatalf("after a failing run: %d worktrees", n)
	}
	if out := run(t, h.repo, "git", "worktree", "prune", "--dry-run", "-v"); strings.TrimSpace(out) != "" {
		t.Fatalf("stale worktree records left: %s", out)
	}
}

// C39: a before-fix run is never a baseline, so a later failure isn't
// excused as one that was there before.
func TestF87_C39_BeforeFixRunIsNeverABaseline(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 2")
	r, err := h.f87BeforeFix("src/a_test.go")
	if err != nil || r.Failed == 0 {
		t.Fatalf("before-fix run: %+v %v", r, err)
	}
	if r.Baseline {
		t.Fatal("a before-fix run is marked as a baseline")
	}
	runs := LoadRuns(h.repo)
	for _, k := range []string{KindTest, KindRelated} {
		if b := BaselineFor(runs, h.task().ID, k); b != nil && b.ID == r.ID {
			t.Fatalf("the before-fix run is the %s baseline", k)
		}
	}
	h.fix("return 1 // broken again")
	later := h.related("src/a_test.go")
	if later.Failed == 0 {
		t.Fatal("premise: the related run fails again")
	}
	runs = LoadRuns(h.repo)
	for _, k := range []string{KindTest, KindRelated} {
		if v := Judge(later, BaselineFor(runs, h.task().ID, k)); v.OK {
			t.Fatalf("a failing related run was excused against the %s baseline", k)
		}
	}
}

// --- (2) citations ---

func f87RouteHarness(t *testing.T) (*harness, func(string) string) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	body := strings.Repeat("// line\n", 320)
	abs := func(dir string) string { return filepath.Join(h.repo, "src", "app", "api", dir, "route.ts") }
	for _, d := range []string{"a", "b", "c"} {
		write(t, abs(d), body)
	}
	return h, abs
}

// C10, C43: an ambiguous bare name lists the opened matches, sorted and
// from the repo top.
func TestF87_C10_C43_AmbiguousBareCitationListsMatches(t *testing.T) {
	h, abs := f87RouteHarness(t)
	h.post("Read", map[string]any{"file_path": abs("b")}, nil) // opened out of order
	h.post("Read", map[string]any{"file_path": abs("a")}, nil)
	p := CheckRefs(h.task(), "The handler checks the role (route.ts:310).")
	want := "route.ts matches 2 opened files: src/app/api/a/route.ts, src/app/api/b/route.ts"
	if len(p) != 1 || !strings.Contains(p[0], want) || !strings.Contains(p[0], "write the path from the repo's top") {
		t.Fatalf("got %q, want one problem with %q", p, want)
	}
	if strings.Contains(p[0], h.repo) {
		t.Fatalf("the matches should be shown from the repo top: %s", p[0])
	}
	// C43: a search hit on a third route.ts doesn't settle the ambiguity.
	tk := h.task()
	if tk.Seen == nil {
		tk.Seen = map[string]Seen{}
	}
	tk.Seen[abs("c")] = Seen{Hash: fileHash(abs("c")), Lines: []int{310}, At: Now()}
	must(t, SaveTask(tk))
	p = CheckRefs(h.task(), "The handler checks the role (route.ts:310).")
	if len(p) != 1 || !strings.Contains(p[0], "matches 2 opened files") {
		t.Fatalf("a search hit resolved an ambiguous name: %q", p)
	}
}

// C11: one opened match still resolves.
func TestF87_C11_UniqueBareCitationResolves(t *testing.T) {
	h, abs := f87RouteHarness(t)
	h.post("Read", map[string]any{"file_path": abs("a")}, nil)
	if p := CheckRefs(h.task(), "The handler checks the role (route.ts:310)."); len(p) != 0 {
		t.Fatalf("a bare name with one opened match should resolve: %q", p)
	}
	if p := CheckRefs(h.task(), "Past the end (route.ts:400)."); len(p) != 1 || !strings.Contains(p[0], "only 320 lines") {
		t.Fatalf("the resolved file is still checked: %q", p)
	}
}

// --- (3) outside-plan cap ---

// f87CapTask: an approved small task (plan: src/a.go) with three files
// already changed outside the plan, so one more meets the cap. committed
// files are in the start commit; dirty files are the user's own work.
func f87CapTask(t *testing.T, committed, dirty map[string]string) *harness {
	t.Helper()
	h := newHarness(t)
	for p, b := range committed {
		write(t, filepath.Join(h.repo, p), b)
	}
	if len(committed) > 0 {
		run(t, h.repo, "git", "add", "-A")
		run(t, h.repo, "git", "commit", "-q", "-m", "chore: files")
	}
	for p, b := range dirty {
		write(t, filepath.Join(h.repo, p), b)
	}
	h.setup()
	h.startTask("small")
	h.ground()
	h.docs(DocPlan, goodPlan)
	h.approve()
	tk := h.task()
	for _, f := range []string{"src/c.go", "src/d.go", "src/e.go"} {
		write(t, filepath.Join(h.repo, f), "package src\n")
		tk.NoteOutside(f, "edited")
	}
	must(t, SaveTask(tk))
	return h
}

// C12: removing a folder the task made is not a file outside the plan.
func TestF87_C12_RemovingTaskCreatedFolderSkipsCap(t *testing.T) {
	h := f87CapTask(t, nil, nil)
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "deny", "plan missed files")
	for _, cmd := range []string{"rm -r src/gen", "rm -rf src/gen/", "rm src/gen/x.go"} {
		write(t, filepath.Join(h.repo, "src", "gen", "x.go"), "package gen\n")
		write(t, filepath.Join(h.repo, "src", "gen", "deep", "y.go"), "package deep\n")
		if d, r := decision(h.bash(cmd)); d == "deny" {
			t.Errorf("%s: removing what the task made was refused: %s", cmd, r)
		}
	}
}

// C13: a tracked file, or a folder holding a start-commit file or the
// user's own work, still meets the cap.
func TestF87_C13_RemovingTrackedOrUsersFilesMeetsCap(t *testing.T) {
	h := f87CapTask(t, map[string]string{"src/sub/t.go": "package sub\n"}, map[string]string{"src/mine/w.go": "package mine\n"})
	write(t, filepath.Join(h.repo, "src", "sub", "new.go"), "package sub\n")
	for _, cmd := range []string{"rm src/b.go", "rm -r src/sub", "rm -rf src/sub/", "rm -r src/mine"} {
		if d, r := decision(h.bash(cmd)); d != "deny" {
			t.Errorf("%s: should still be refused, got %q (%s)", cmd, d, r)
		}
	}
	h.expect(h.bash("rm src/b.go"), "deny", "plan missed files")
}

// C14: when git can't list the start commit, the cap stays.
func TestF87_C14_GitErrorKeepsCap(t *testing.T) {
	h := f87CapTask(t, nil, nil)
	write(t, filepath.Join(h.repo, "src", "gen", "x.go"), "package gen\n")
	tk := h.task()
	tk.StartCommit = "2222222222222222222222222222222222222222"
	must(t, SaveTask(tk))
	if d, r := decision(h.bash("rm -r src/gen")); d != "deny" {
		t.Fatalf("a git error must keep the cap: %q (%s)", d, r)
	}
}

// C16: other letter case, or a symlinked folder, doesn't reach a tracked
// tree past the cap.
func TestF87_C16_CaseAndSymlinkDontBypassCap(t *testing.T) {
	h := f87CapTask(t, map[string]string{"src/Flaky/x.go": "package flaky\n"}, nil)
	if _, err := os.Stat(filepath.Join(h.repo, "src", "flaky")); err == nil {
		for _, cmd := range []string{"rm -r src/flaky", "rm src/flaky/x.go", "rm -r src/FLAKY/"} {
			if d, r := decision(h.bash(cmd)); d != "deny" {
				t.Errorf("%s: a tracked folder in other letter case got %q (%s)", cmd, d, r)
			}
		}
	} else {
		t.Log("case-sensitive file system: letter-case part not run")
	}
	must(t, os.Symlink("Flaky", filepath.Join(h.repo, "src", "lnk")))
	for _, cmd := range []string{"rm src/lnk/x.go", "rm -r src/lnk/"} {
		if d, r := decision(h.bash(cmd)); d != "deny" {
			t.Errorf("%s: a tracked file through a symlink got %q (%s)", cmd, d, r)
		}
	}
}

// C15: git-ignored files and untracked notes written with Write/Edit don't
// count toward the cap.
func TestF87_C15_IgnoredFilesAndNotesSkipCap(t *testing.T) {
	h := f87CapTask(t, nil, nil)
	for _, rel := range []string{"coverage/lcov.info", "docs/scratch-notes.md"} {
		p := filepath.Join(h.repo, rel)
		for _, tool := range []string{"Write", "Edit"} {
			if d, r := decision(h.pre(tool, map[string]any{"file_path": p, "content": "x\n"})); d == "deny" {
				t.Errorf("%s %s past the cap was refused: %s", tool, rel, r)
			}
			write(t, p, "x\n")
			h.post(tool, map[string]any{"file_path": p}, nil)
		}
	}
	tk := h.task()
	if _, ok := tk.OutsidePlan["coverage/lcov.info"]; ok {
		t.Error("a git-ignored file is recorded as outside the plan")
	}
	s, _, _ := LoadSetup(h.repo)
	if n := tk.OutsideCapCount(s); n != 3 {
		t.Errorf("only the three code files count toward the cap: %d", n)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "deny", "plan missed files")
}

// --- (4) plan folders ---

const f87FolderPlan = "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n%FILES%## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n## Challenge note\n- nothing changed\n"

func f87FolderTask(t *testing.T, entry string) *harness {
	t.Helper()
	h := newHarness(t)
	write(t, filepath.Join(h.repo, "src", "app", "api", "superadmin", "users", "route.ts"), "export const GET = 1\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: api")
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.IgnoreChanges = []string{"coverage/**"}
	s.RiskyPaths = []string{"src/app/api/superadmin/money/**"}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("normal")
	h.ground()
	plan := strings.Replace(f87FolderPlan, "%FILES%", "- "+entry+"\n", 1)
	h.docs(DocPlan, plan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, plan)
	h.approve()
	return h
}

// C17: dir/** reads as the folder and covers files at any depth.
func TestF87_C17_DoubleStarEntryCoversTheFolder(t *testing.T) {
	h := f87FolderTask(t, "src/app/api/superadmin/**")
	tk := h.task()
	for _, rel := range []string{"src/app/api/superadmin/users/route.ts", "src/app/api/superadmin/x.ts", "src/app/api/superadmin/a/b/c/new.ts"} {
		if !tk.InPlan(rel) {
			t.Errorf("%s should be in the plan's folder", rel)
		}
	}
	for _, rel := range []string{"src/app/api/superadminx/y.ts", "src/app/api/other.ts", "src/app/api/superadmin"} {
		if tk.InPlan(rel) {
			t.Errorf("%s is outside the folder", rel)
		}
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "app", "api", "superadmin", "users", "route.ts")}), "", "")
	if d, r := decision(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "app", "api", "superadmin", "a", "b", "c", "new.ts"), "content": "x\n"})); d == "deny" {
		t.Fatalf("a deep new file in the plan's folder was refused: %s", r)
	}
}

// C18: a folder entry never covers a risky or check-deciding file; small
// plans still refuse folders and patterns.
func TestF87_C18_FolderNeverCoversRiskyOrGateFiles(t *testing.T) {
	for _, entry := range []string{"src/app/api/superadmin/**", "src/app/api/superadmin/"} {
		h := f87FolderTask(t, entry)
		base := filepath.Join(h.repo, "src", "app", "api", "superadmin")
		tk := h.task()
		for _, rel := range []string{"auth/route.ts", "migrations/001_init.sql", "money/rate.ts", "vitest.config.ts", "package.json"} {
			full := "src/app/api/superadmin/" + rel
			if tk.InPlan(full) {
				t.Errorf("%s: InPlan(%s) is true; a folder never covers a risky or gate file", entry, full)
			}
			if d, r := decision(h.pre("Write", map[string]any{"file_path": filepath.Join(base, filepath.FromSlash(rel)), "content": "x\n"})); d != "deny" {
				t.Errorf("%s: writing %s got %q (%s), want refused", entry, full, d, r)
			}
		}
		// The post-tool changed-files check holds one a command wrote.
		h.post("Bash", map[string]any{"command": "true"}, map[string]any{})
		write(t, filepath.Join(base, "auth", "session.ts"), "export const s = 1\n")
		h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{})
		tk = h.task()
		_, held := tk.Held["src/app/api/superadmin/auth/session.ts"]
		_, outside := tk.OutsidePlan["src/app/api/superadmin/auth/session.ts"]
		if !held && !outside {
			t.Errorf("%s: a risky file a command changed under the plan's folder is neither held nor outside the plan", entry)
		}
	}

	h := groundedTask(t, "small")
	write(t, filepath.Join(h.repo, "src", "x", "y.go"), "package x\n")
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- src/x/**\n", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "not folders or patterns")
}

// --- (5) helpers editing their findings ---

func f87Challengers(t *testing.T) (*harness, string, string) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, strings.Replace(goodPlan, "## Guards loosened\n- none\n", "## Guards loosened\n- none\n## Challenge note\n- nothing changed\n", 1))
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	first := filepath.Join(TaskDocsDir(h.repo, h.task().ID), "challenge-1.md")
	id, _ := h.startHelper(HelperChallenger)
	own := h.findingsPath(id)
	write(t, own, "# Challenge 2\n- [85] draft\n")
	return h, id, first
}

// C19: a read-only helper may Edit its own findings file, nothing else.
func TestF87_C19_HelperEditsOwnFindingsOnly(t *testing.T) {
	h, id, first := f87Challengers(t)
	own := h.findingsPath(id)
	hi := func(tool, p string) Output { return h.hi(id, HelperChallenger, tool, map[string]any{"file_path": p}) }
	h.expect(hi("Edit", own), "allow", "")
	h.expect(hi("MultiEdit", own), "allow", "")
	h.expect(hi("Write", own), "allow", "")
	h.expect(hi("Edit", filepath.Join(h.repo, "src", "a.go")), "deny", "")
	h.expect(hi("Edit", filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan)), "deny", "")
	if _, err := os.Stat(first); err != nil {
		write(t, first, "# Challenge 1\nnothing at 80 or more\n")
	}
	h.expect(hi("Edit", first), "deny", "")
	h.expect(hi("MultiEdit", first), "deny", "")
	h.expect(hi("NotebookEdit", own), "deny", "")
}

// C44: a findings path that is a link, or spelled in other letter case,
// isn't editable through Edit.
func TestF87_C44_LinkedOrRecasedFindingsNotEditable(t *testing.T) {
	h, id, _ := f87Challengers(t)
	own := h.findingsPath(id)
	hi := func(p string) Output { return h.hi(id, HelperChallenger, "Edit", map[string]any{"file_path": p}) }

	recased := filepath.Join(filepath.Dir(own), strings.ToUpper(filepath.Base(own)))
	h.expect(hi(recased), "deny", "")

	other := filepath.Join(t.TempDir(), "elsewhere.md")
	must(t, os.Link(own, other)) // the findings file gets a second name
	h.expect(hi(other), "deny", "")
	h.expect(hi(own), "deny", "")
	must(t, os.Remove(other))

	must(t, os.Remove(own))
	must(t, os.Symlink(filepath.Join(h.repo, "src", "a.go"), own))
	h.expect(hi(own), "deny", "")
	must(t, os.Remove(own))
	h.expect(hi(own), "deny", "") // missing: nothing to edit
}

// C20: the 10 read-only agents no longer disallow Edit/MultiEdit and say to
// change findings with Edit, not the terminal.
func TestF87_C20_ReadOnlyAgentsMayEditFindings(t *testing.T) {
	for _, name := range []string{"arbiter", "challenger", "code-reviewer", "data-reviewer", "guard-reviewer", "plan-reviewer", "security-reviewer", "ux-reviewer", "tracer", "tester"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "agents", name+".md"))
		must(t, err)
		text := string(b)
		parts := strings.SplitN(text, "---", 3)
		if len(parts) < 3 {
			t.Fatalf("%s: no frontmatter", name)
		}
		front, body := parts[1], parts[2]
		for _, l := range strings.Split(front, "\n") {
			if !strings.HasPrefix(l, "disallowedTools:") {
				continue
			}
			tools := map[string]bool{}
			for _, x := range strings.Split(strings.TrimPrefix(l, "disallowedTools:"), ",") {
				tools[strings.TrimSpace(x)] = true
			}
			if tools["Edit"] || tools["MultiEdit"] {
				t.Errorf("%s still disallows Edit/MultiEdit: %s", name, l)
			}
			if !tools["NotebookEdit"] || !tools["Agent"] {
				t.Errorf("%s dropped more than Edit/MultiEdit: %s", name, l)
			}
		}
		if !strings.Contains(body, "Edit") || !strings.Contains(body, "terminal") {
			t.Errorf("%s doesn't say to change findings with Edit, never the terminal", name)
		}
	}
}

// --- (7) full suite that never finished ---

func f87NeverFinished(t *testing.T, pid int, started time.Time) *harness {
	h := buildingTask(t)
	h.fix("return 2")
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	must(t, appendRun(h.repo, &Run{ID: newID() + "-test", TaskID: h.task().ID, Kind: KindTest, Command: "./test.sh",
		Started: started, PID: pid, PrintBefore: fp, Ran: -1, Root: h.repo}))
	return h
}

func f87DeadPID(t *testing.T) int {
	c := exec.Command("true")
	must(t, c.Run())
	return c.Process.Pid
}

// C21
func TestF87_C21_NeverFinishedLiveRunSaysWait(t *testing.T) {
	started := Now().Add(-2 * time.Minute)
	h := f87NeverFinished(t, os.Getpid(), started)
	m := proofMissingNow(h, true)
	for _, want := range []string{"still running (started ", "wait for it", "don't start another"} {
		if !strings.Contains(m, want) {
			t.Fatalf("missing %q: %s", want, m)
		}
	}
	if !strings.Contains(m, started.Format("15:04")) && !strings.Contains(m, started.Local().Format("15:04")) {
		t.Errorf("the start time isn't given: %s", m)
	}
}

// C22
func TestF87_C22_NeverFinishedDeadRunSaysRerun(t *testing.T) {
	h := f87NeverFinished(t, f87DeadPID(t), Now().Add(-2*time.Minute))
	m := proofMissingNow(h, true)
	if !strings.Contains(m, "stopped before it finished (its process is gone") || !strings.Contains(m, "run it again in the background") {
		t.Fatalf("a dead run should say it stopped and to rerun in the background: %s", m)
	}
	if strings.Contains(m, "still running") {
		t.Fatalf("a dead run isn't still running: %s", m)
	}
}

// C41: a live pid started longer ago than the time limit plus a minute is
// a reused pid or a hung run, not one to wait for.
func TestF87_C41_OldLivePIDReadsAsStopped(t *testing.T) {
	s := &Setup{}
	s.Commands.Test = "./test.sh"
	old := Now().Add(-(RunLimit(KindTest, s) + 2*time.Minute))
	h := f87NeverFinished(t, os.Getpid(), old)
	m := proofMissingNow(h, true)
	if strings.Contains(m, "still running") || !strings.Contains(m, "stopped before it finished") {
		t.Fatalf("a run older than its limit reads as stopped: %s", m)
	}
}

// --- stats ---

func f87Line(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// C23: send-back reasons per rule, digits scrubbed.
func TestF87_C23_StatsSendBackReasons(t *testing.T) {
	newHarness(t)
	for _, d := range []string{
		"full test suite: the last run started but never finished (started 10:42); report: the code changed after report.md was written; update it",
		"full test suite: the last run started but never finished (started 11:05)",
	} {
		LogEvent(Event{Task: "t1", Type: "sendback", Rule: firstRule([]string{d}), Detail: d})
	}
	LogEvent(Event{Task: "t1", Type: "sendback", Rule: "report", Detail: "report: the code changed after report.md was written; update it"})
	out := Stats(1)
	for _, want := range []string{"the last run started but never finished", "the code changed after report.md was written"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats should give the send-back reason %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "10:42") || strings.Contains(out, "11:05") {
		t.Errorf("digits should be scrubbed from the reasons:\n%s", out)
	}
}

// C27: the top confirm-prompt details per rule.
func TestF87_C27_StatsAskDetails(t *testing.T) {
	newHarness(t)
	for i := 0; i < 3; i++ {
		LogEvent(Event{Task: "t1", Type: "ask", Rule: "secret-read", Detail: "a listing feeds xargs grep -n"})
	}
	LogEvent(Event{Task: "t1", Type: "ask", Rule: "secret-read", Detail: "reads .env"})
	out := Stats(1)
	if !strings.Contains(out, "a listing feeds xargs grep -n") {
		t.Fatalf("the top ask detail isn't printed:\n%s", out)
	}
}

// C30: a rule is flagged only with 3+ wrong marks and wrong marks at half
// or more of its blocks plus send-backs.
func TestF87_C30_StatsFlagsNoisyRules(t *testing.T) {
	newHarness(t)
	log := func(rule, typ string, n int) {
		for i := 0; i < n; i++ {
			LogEvent(Event{Task: "t1", Type: typ, Rule: rule})
		}
	}
	log("two-wrong", "block", 2)
	log("two-wrong", "wrong", 2)
	log("one-wrong", "block", 1)
	log("one-wrong", "wrong", 1)
	log("three-wrong", "block", 6)
	log("three-wrong", "wrong", 3)
	log("many-blocks", "block", 10)
	log("many-blocks", "wrong", 3)
	log("sendback-rule", "sendback", 4)
	log("sendback-rule", "wrong", 3)
	log("mixed", "block", 3)
	log("mixed", "sendback", 4)
	log("mixed", "wrong", 3) // 6 < 7: not half
	out := Stats(1)
	line := f87Line(out, "Rules to look at")
	flagged := map[string]bool{}
	if i := strings.Index(line, ": "); i >= 0 {
		for _, r := range strings.Split(line[i+2:], ",") {
			flagged[strings.TrimSpace(r)] = true
		}
	}
	for r, want := range map[string]bool{"two-wrong": false, "one-wrong": false, "three-wrong": true, "many-blocks": false, "sendback-rule": true, "mixed": false} {
		if flagged[r] != want {
			t.Errorf("%s flagged %v, want %v\n%s", r, flagged[r], want, out)
		}
	}
}

// --- (8) listings fed to a reader ---

func f87ListingHarness(t *testing.T) *harness {
	h := newHarness(t)
	for _, p := range []string{"src/app/api/users/route.ts", "src/app/api/health/route.ts", "src/app/api/lib/db.ts"} {
		write(t, filepath.Join(h.repo, filepath.FromSlash(p)), "export const x = 1 // TODO\n")
	}
	return h
}

// C24
func TestF87_C24_NamesOnlyListingWithoutSecretsRuns(t *testing.T) {
	h := f87ListingHarness(t)
	for _, cmd := range []string{
		"grep -rl TODO src/app/api | xargs grep -n export",
		"grep -rl TODO src/app/api | xargs cat",
		"egrep -rl TODO src/app/api | xargs cat",
		"grep -r --files-with-matches TODO src/app/api | xargs cat",
		"grep -rL NOPE src/app/api | xargs cat",
		"rg -l TODO src/app/api | xargs cat",
		"rg --files-with-matches TODO src/app/api | xargs cat",
		"grep -rl TODO src/app/api | sort | uniq | head -5 | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l export | xargs cat",
		"grep -rl TODO src/app/api src/app/api/lib | xargs grep -n export",
	} {
		h.expect(h.bash(cmd), "allow", "")
	}
}

// C25
func TestF87_C25_ListingOverDoubtfulTreeStillAsks(t *testing.T) {
	h := f87ListingHarness(t)
	src := func(p string) string { return filepath.Join(h.repo, "src", filepath.FromSlash(p)) }
	write(t, src("s1/a.ts"), "x\n")
	write(t, src("s1/.env"), "KEY=1\n")
	write(t, src("s2/a.ts"), "x\n")
	write(t, src("s2/.ENV"), "KEY=1\n")
	write(t, src("s3/deep/er/.env.local"), "KEY=1\n")
	write(t, src("s4/a.ts"), "x\n")
	must(t, os.Symlink(src("s1"), src("s4/link")))
	write(t, src("s5/a.ts"), "x\n")
	must(t, os.Link(src("s5/a.ts"), src("s5/b.ts")))
	for _, cmd := range []string{
		"grep -rl KEY src/s1 | xargs cat",
		"grep -rl KEY src/s2 | xargs cat",
		"grep -rl KEY src/s3 | xargs cat",
		"grep -rl KEY src/s4 | xargs cat",
		"grep -rl KEY src/s5 | xargs cat",
		"grep -rl KEY src/nothere | xargs cat",
		"grep -rl KEY src/app/api src/s1 | xargs cat",
		`cd "$SOMEWHERE" && grep -rl KEY src/app/api | xargs cat`,
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
	for i := 0; i < 20001; i++ {
		p := src(fmt.Sprintf("big/d%02d/f%05d.ts", i%50, i))
		if i < 50 {
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		}
		must(t, os.WriteFile(p, []byte("x\n"), 0o644))
	}
	h.expect(h.bash("grep -rl KEY src/big | xargs cat"), "ask", "")
}

// C26
func TestF87_C26_ListingWithoutNamesOnlyStillAsks(t *testing.T) {
	h := f87ListingHarness(t)
	for _, cmd := range []string{
		"grep -r TODO src/app/api | xargs cat",
		"grep -rn TODO src/app/api | xargs cat",
		"grep -rl TODO src/app/api | sed s/route/x/ | xargs cat",
		"grep -rl TODO src/app/api | awk '{print}' | xargs cat",
		"grep -rl TODO src/app/api | tr a b | xargs cat",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
}

// C35: rg -L follows symlinks; it isn't a names-only flag.
func TestF87_C35_RgFollowFlagIsNotNamesOnly(t *testing.T) {
	h := f87ListingHarness(t)
	h.expect(h.bash("rg -l TODO src/app/api | xargs cat"), "allow", "")
	h.expect(h.bash("rg -L TODO src/app/api | xargs cat"), "ask", "")
}

// C36: names xargs would split keep the ask.
func TestF87_C36_OddFileNamesKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	for i, name := range []string{"a b.ts", "a'b.ts", "a\"b.ts", `a\b.ts`, "a\nb.ts", "a\tb.ts"} {
		dir := fmt.Sprintf("w%d", i)
		write(t, filepath.Join(h.repo, "src", dir, "ok.ts"), "x\n")
		write(t, filepath.Join(h.repo, "src", dir, name), "x\n")
		h.expect(h.bash("grep -rl TODO src/"+dir+" | xargs cat"), "ask", "")
	}
}

// C37: a middle filter that names files or reads patterns, or a listing
// with no path, keeps the ask.
func TestF87_C37_MiddleFilterAndPathlessListingKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	write(t, filepath.Join(h.repo, "pats.txt"), "x\n")
	for _, cmd := range []string{
		"grep -rl TODO src/app/api | xargs grep -l B .env | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l B .env",
		"grep -rl TODO src/app/api | xargs grep -l -f pats.txt | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -rl B | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -Rl B | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l --include=*.ts B | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l --file=pats.txt | xargs cat",
		"grep -rl TODO | xargs cat",
		"rg -l TODO | xargs grep -n B",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
}

// --- (9) builder copies ---

// C28, C29: builder clean removes a closed or cancelled task's copies,
// never an open one's, and the hook asks first; closing names the command.
func TestF87_C28_C29_BuilderCleanRemovesClosedTasksCopies(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	if wt == "" {
		t.Fatal("premise: the builder has a copy")
	}
	h.expect(h.bash("plumbline builder clean"), "ask", "")
	removed, err := CleanCopies(h.repo)
	must(t, err)
	if len(removed) != 0 {
		t.Fatalf("an open task's copy was removed: %v", removed)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("an open task's copy is gone: %v", err)
	}
	tk := h.task()
	tk.Phase = PhaseDone
	must(t, SaveTask(tk))
	// Review fix: a builder still running keeps its copy after the close.
	if removed, err = CleanCopies(h.repo); err != nil || len(removed) != 0 {
		t.Fatalf("a running builder's copy was removed: %v %v", removed, err)
	}
	// The builder finishes (its copy holds a change, so it stays on disk).
	tk, err = LoadTask(h.repo, tk.ID)
	must(t, err)
	for i := range tk.Helpers {
		if tk.Helpers[i].AgentID == id {
			tk.Helpers[i].Finished = Now()
		}
	}
	must(t, SaveTask(tk))
	removed, err = CleanCopies(h.repo)
	must(t, err)
	if len(removed) != 1 || filepath.Clean(removed[0]) != filepath.Clean(wt) {
		t.Fatalf("the done task's copy should be removed: %v (want %s)", removed, wt)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("the copy is still on disk: %v", err)
	}
	if strings.Contains(run(t, h.repo, "git", "worktree", "list", "--porcelain"), wt) {
		t.Fatal("git still lists the removed copy")
	}

	c := buildingTask(t)
	cid, _ := c.startHelper(HelperBuilder)
	cwt := c.run(HelperBuilder, cid).Worktree
	write(t, filepath.Join(cwt, "src", "built.go"), "package src\n")
	cid0 := c.task().ID
	o := c.closeAnswer("Close it")
	if !strings.Contains(fmtOut(o), "plumbline builder clean") {
		t.Fatalf("closing with a copy left should name plumbline builder clean: %s", fmtOut(o))
	}
	if removed, err = CleanCopies(c.repo); err != nil || len(removed) != 0 {
		t.Fatalf("a builder still running after the close keeps its copy: %v %v", removed, err)
	}
	ct, err := LoadTask(c.repo, cid0)
	must(t, err)
	for i := range ct.Helpers {
		if ct.Helpers[i].AgentID == cid {
			ct.Helpers[i].Finished = Now()
		}
	}
	must(t, SaveTask(ct))
	removed, err = CleanCopies(c.repo)
	must(t, err)
	if len(removed) != 1 || filepath.Clean(removed[0]) != filepath.Clean(cwt) {
		t.Fatalf("the cancelled task's copy should be removed: %v (want %s)", removed, cwt)
	}
}

// C42: a git worktree the user made, and copies behind a symlinked
// .claude/worktrees, are left alone.
func TestF87_C42_BuilderCleanLeavesUserWorktreesAndSymlinkedFolder(t *testing.T) {
	h := buildingTask(t)
	mine := filepath.Join(h.repo, ".claude", "worktrees", "plumbline-mine")
	run(t, h.repo, "git", "worktree", "add", "-q", "--detach", mine)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	tk := h.task()
	tk.Phase = PhaseCancelled
	must(t, SaveTask(tk))

	dir := filepath.Join(h.repo, ".claude", "worktrees")
	realDir := filepath.Join(filepath.Dir(h.repo), "wt-real")
	must(t, os.Rename(dir, realDir))
	must(t, os.Symlink(realDir, dir))
	removed, err := CleanCopies(h.repo)
	if err == nil && len(removed) != 0 {
		t.Fatalf("copies behind a symlinked worktrees folder were removed: %v", removed)
	}
	for _, p := range []string{filepath.Join(realDir, filepath.Base(wt)), filepath.Join(realDir, "plumbline-mine")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s should be left: %v", p, err)
		}
	}
	must(t, os.Remove(dir))
	must(t, os.Rename(realDir, dir))
	removed, err = CleanCopies(h.repo)
	must(t, err)
	for _, p := range removed {
		if filepath.Clean(p) == filepath.Clean(mine) {
			t.Fatal("a worktree the user made was removed")
		}
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("the user's worktree is gone: %v", err)
	}
}

// --- notes before approval ---

func f87NoteTask(t *testing.T) *harness {
	h := noteHarness(t, map[string]string{"README.md": "# Readme\n", "src/c.go": "package src\n\n// steps in docs/named.md\n"})
	h.startTask("small")
	h.ground()
	return h
}

// C31, C38: an untracked note is written before approval, the changed-files
// check doesn't ask, and approval still sets BaseTree.
func TestF87_C31_C38_UntrackedNoteBeforeApproval(t *testing.T) {
	h := f87NoteTask(t)
	if h.task().Phase == PhaseBuilding {
		t.Fatal("premise: before approval")
	}
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.mustAllow(h.writeTool("docs/sprint-23.md"), "Write of an untracked note before approval")
	write(t, filepath.Join(h.repo, "docs", "sprint-23.md"), "# notes\n")
	if o := h.post("Write", map[string]any{"file_path": filepath.Join(h.repo, "docs", "sprint-23.md")}, map[string]any{}); reported(o) {
		t.Fatalf("the note stopped the work: %v", o)
	}
	if o := h.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); reported(o) {
		t.Fatalf("the changed-files check asked about the note: %v", o)
	}
	if lc := h.task().LastCodeChange; !lc.IsZero() {
		t.Fatalf("a note isn't a code change: %v", lc)
	}
	h.docs(DocPlan, goodPlan)
	h.approve()
	if h.task().BaseTree == "" {
		t.Fatal("approval should still set BaseTree")
	}
}

// C32: before approval, a note the plan lists, a tracked note, a note named
// in code, and linked notes are still refused.
func TestF87_C32_NotesStillRefusedBeforeApproval(t *testing.T) {
	h := f87NoteTask(t)
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- docs/listed.md (new)\n", 1))
	must(t, os.MkdirAll(filepath.Join(h.repo, "docs"), 0o755))
	must(t, os.Symlink(filepath.Join("..", "src", "new.go"), filepath.Join(h.repo, "docs", "sym.md")))
	must(t, os.Link(filepath.Join(h.repo, "src", "a.go"), filepath.Join(h.repo, "docs", "hard.md")))
	for _, rel := range []string{"docs/listed.md", "README.md", "docs/named.md", "docs/sym.md", "docs/hard.md", "src/x.go"} {
		h.expect(h.writeTool(rel), "deny", "")
	}
	h.mustAllow(h.writeTool("docs/free.md"), "a plain untracked note")
}

// C33: sed -i on a notes file points at the Edit tool.
func TestF87_C33_SedOnNotesPointsAtEdit(t *testing.T) {
	h := buildingTask(t)
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan)
	h.expect(h.bash("sed -i '' 's/A/B/' "+p), "deny", "Edit")
}

// --- docs ---

// C34
func TestF87_C34_DocsDescribe087(t *testing.T) {
	top := filepath.Join("..", "..")
	readTop := func(p string) string {
		b, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(p)))
		must(t, err)
		return string(b)
	}
	// plugin.json and the newest entry moved on with 0.8.8 (review088_test.go).
	if !strings.Contains(readTop("CHANGELOG.md"), "\n## 0.8.7") {
		t.Error("CHANGELOG has no 0.8.7 entry")
	}
	checks := map[string][]string{
		"skills/task/SKILL.md":        {"codemod", "plumbline status", "tracer"},
		"README.md":                   {"--before-fix", "builder clean"},
		"internal/core/rules-task.md": {"before-fix"},
	}
	files := make([]string, 0, len(checks))
	for f := range checks {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		text := readTop(f)
		for _, w := range checks[f] {
			if !strings.Contains(text, w) {
				t.Errorf("%s doesn't mention %q", f, w)
			}
		}
	}
	cp := strings.ToLower(readTop("skills/task/checkpoints.md"))
	if !strings.Contains(cp, "sentences") || !strings.Contains(cp, "above") {
		t.Error("checkpoints.md doesn't say the question is 2-3 sentences with the plan above")
	}
}

// Lead's addition: rm with a pattern can expand to tracked files, so it
// keeps the cap even when the pattern itself names nothing tracked.
func TestF87_RmPatternKeepsCap(t *testing.T) {
	h := f87CapTask(t, map[string]string{"src/sub/t.go": "package sub\n"}, nil)
	for _, cmd := range []string{"rm src/sub/*", "rm -r src/s?b", "rm -r 'src/[s]ub'"} {
		if d, r := decision(h.bash(cmd)); d != "deny" {
			t.Errorf("%s: a pattern rm should meet the cap, got %q (%s)", cmd, d, r)
		}
	}
}

// Review fixes (round 1): flags, words and filters the listing check can't
// read keep the ask.
func TestF87_ListingReviewShapesKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	write(t, filepath.Join(h.repo, "list"), ".env\n")
	write(t, filepath.Join(h.repo, "sec", ".env"), "KEY=1\n")
	for _, cmd := range []string{
		"grep -rl TODO src/app/api | sort --files0-from=list | xargs cat",
		"grep -rl TODO src/app/api | head -c 4 | xargs cat",
		"grep -rl TODO src/app/api | uniq -c | xargs cat",
		"grep -rl --regex=KEY sec src/app/api | xargs cat",
		"rg -l --files sec src/app/api | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l --directories=recurse --regex=. sec | xargs cat",
		"grep -rl $(echo KEY sec) src/app/api | xargs cat",
		"grep -rl $SOMEVAR src/app/api | xargs cat",
		"grep -rl * src/app/api | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l $(echo . sec/.env) | xargs cat",
		`grep -rl TODO src/app/api | xargs grep -l "$SOMEVAR" | xargs cat`,
		"grep -rl TODO src/app/api | xargs grep -l * | xargs cat",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
	h.expect(h.bash("grep -rli TODO src/app/api | sort -u | head -n 5 | xargs cat"), "allow", "")
}

// Review fix: a redirect on an rm line is a new file, not a removal.
func TestF87_RmRedirectMeetsCap(t *testing.T) {
	h := f87CapTask(t, nil, nil)
	h.expect(h.bash("rm -f nothing.txt > src/extra9.go"), "deny", "plan missed files")
}

// Review fix: a name several search hits share lists them too.
func TestF87_AmbiguousSearchHitListsMatches(t *testing.T) {
	h, abs := f87RouteHarness(t)
	tk := h.task()
	if tk.Seen == nil {
		tk.Seen = map[string]Seen{}
	}
	for _, d := range []string{"a", "b"} {
		tk.Seen[abs(d)] = Seen{Hash: fileHash(abs(d)), Lines: []int{12}, At: Now()}
	}
	must(t, SaveTask(tk))
	p := CheckRefs(h.task(), "See route.ts:12.")
	if len(p) != 1 || !strings.Contains(p[0], "matches 2 files a search showed: src/app/api/a/route.ts, src/app/api/b/route.ts") {
		t.Fatalf("got %q", p)
	}
}

// Review fix: the newest failing run decides the reason given.
func TestF87_ReasonIsTheNewestRunsReason(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 1 // partly")
	h.related("src/a_test.go") // fails, but a code file changed
	h.fix("return 2")
	if _, err := h.f87BeforeFix("src/a_test.go"); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(h.repo, "src", "a_test.go")
	write(t, p, "package src\n\nimport \"testing\"\n\n// edited\nfunc TestA(t *testing.T) {}\n")
	h.post("Edit", map[string]any{"file_path": p}, nil)
	m := bugMissing(h)
	if !strings.Contains(m, "it changed after the run that saw it failing") || strings.Contains(m, "besides tests") {
		t.Fatalf("the before-fix run was the newest and only the test changed since: %s", m)
	}
}

// Review fix: a before-fix run shows the bug only when tests ran and failed.
func TestF87_BeforeFixShowsOnlyRealFailures(t *testing.T) {
	for _, c := range []struct {
		r    Run
		want bool
		says string
	}{
		{Run{Ran: 3, Failed: 1, FailedIDs: []string{"TestA"}}, true, "TestA"},
		{Run{Ran: 3}, false, "don't fail without the fix"},
		{Run{Ran: -1, Exit: 1}, false, "doesn't say how many tests ran"},
		{Run{Ran: 0, Exit: 1}, false, "0 tests run"},
		{Run{Ran: 3, Failed: 1, TimedOut: true}, false, "time limit"},
	} {
		got, why := BeforeFixShows(&c.r)
		if got != c.want || !strings.Contains(why, c.says) {
			t.Errorf("%+v: got %v %q, want %v with %q", c.r, got, why, c.want, c.says)
		}
	}
}

// Review fix: the message names at most five files besides tests.
func TestF87_BlockedRunNamesAtMostFive(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	for i := 0; i < 7; i++ {
		write(t, filepath.Join(h.repo, "src", fmt.Sprintf("x%d.go", i)), "package src\n")
	}
	h.writeTest()
	if r := h.related("src/a_test.go"); r.Failed == 0 {
		t.Fatal("premise: the test fails")
	}
	m := bugMissing(h)
	if !strings.Contains(m, "and 2 more") || strings.Contains(m, "src/x5.go") {
		t.Fatalf("seven files besides tests: five named and the rest counted: %s", m)
	}
}

// Review round 2: a test that fails in the temporary copy for a git-ignored
// file it lacks shows nothing; the control run on the current code catches
// it, and no failing run counts.
func TestF87_BeforeFixControlCatchesTheCopysGaps(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	write(t, filepath.Join(h.repo, ".gitignore"), "fixtures/\n")
	write(t, filepath.Join(h.repo, "fixtures", "big.dat"), "x\n")
	write(t, filepath.Join(h.repo, "test.sh"), "#!/bin/sh\nif [ ! -f fixtures/big.dat ]; then echo \"--- FAIL: TestA (0.00s)\"; echo FAIL; echo \"FAIL\texample.com/r/src\t0.1s\"; exit 1; fi\necho \"ok  \texample.com/r/src\t0.1s\"\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: fixture test")
	h.f87Reapprove()
	h.writeTest()
	h.fix("return 2")
	if _, err := h.f87BeforeFix("src/a_test.go"); err == nil || !strings.Contains(err.Error(), "don't pass in a temporary copy of the current code") {
		t.Fatalf("a failure from the copy's missing ignored file should be refused: %v", err)
	}
	if m := bugMissing(h); m == "" {
		t.Fatal("a failure that only the copy's gaps caused counted as seen failing")
	}
}

// Review round 2: the [bug] test named in the plan must be the one failing.
func TestF87_BeforeFixMissingNamesTheCase(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestZ")
	h.writeTest()
	h.fix("return 2")
	r, err := h.f87BeforeFix("src/a_test.go")
	if err != nil || r.Failed == 0 {
		t.Fatalf("before-fix run: %+v %v", r, err)
	}
	s, _, _ := LoadSetup(h.repo)
	miss, err := BeforeFixMissing(h.task(), s, r.Files)
	must(t, err)
	if strings.Join(miss, ",") != "src/a_test.go::TestZ" {
		t.Fatalf("TestA failed, not the case's TestZ: %v", miss)
	}
}

// Review round 2: a still-running suite isn't followed by a hint to run it.
func TestF87_StillRunningHasNoRunHint(t *testing.T) {
	h := f87NeverFinished(t, os.Getpid(), Now().Add(-2*time.Minute))
	m := proofMissingNow(h, true)
	if !strings.Contains(m, "still running") || strings.Contains(m, "(run: plumbline run test)") {
		t.Fatalf("the wait message ends with a run hint: %s", m)
	}
}

// Review round 2: brace words and env prefixes keep the listing's ask.
func TestF87_ListingBracesAndEnvKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	write(t, filepath.Join(h.repo, "sec", ".env"), "KEY=1\n")
	for _, cmd := range []string{
		"grep -rl {KEY,sec} src/app/api | xargs cat",
		"grep -rl -e {KEY,sec} src/app/api | xargs cat",
		"rg -l {KEY,sec} src/app/api | xargs cat",
		"grep -rl TODO src/app/api | xargs grep -l {.,-r,sec} | xargs cat",
		"GREP_OPTIONS=-e. grep -rl sec src/app/api | xargs cat",
		"grep -rl TODO src/app/api | GREP_OPTIONS=-r xargs grep -l . | xargs cat",
		"RIPGREP_CONFIG_PATH=x rg -l TODO src/app/api | xargs cat",
		"grep -rl TODO ~ | xargs cat",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
}

// Review round 2: an auth file changed with no plan line still calls for
// the security reviewer (the changed files count).
func TestF87_UnplannedRiskyChangeCallsForSecurity(t *testing.T) {
	h := groundedTask(t, "normal")
	tk := h.task()
	tk.StartCommit = HeadCommit(h.repo)
	must(t, SaveTask(tk))
	plan := goodPlan + "## Challenge note\n- nothing changed\n"
	h.docs(DocPlan, goodPlan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing\n")
	h.docs(DocPlan, plan)
	h.approve()
	write(t, filepath.Join(h.repo, "src", "auth", "session.go"), "package auth\n") // e.g. kept at a changed-files ask
	tk = h.task()
	if _, ok := tk.OutsidePlan["src/auth/session.go"]; ok || tk.InPlan("src/auth/session.go") {
		t.Fatal("premise: the file is in neither the plan nor the outside-plan list")
	}
	s, _, _ := LoadSetup(h.repo)
	if got := strings.Join(RequiredReviewers(tk, s), ","); !strings.Contains(got, HelperSecurity) {
		t.Fatalf("a changed auth file needs the security reviewer: %s", got)
	}
}

// Review round 3: the before-fix control run isn't the case test's passing
// run on the current code.
func TestF87_ControlRunIsNoCaseProof(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	h.fix("return 2")
	if _, err := h.f87BeforeFix("src/a_test.go"); err != nil {
		t.Fatal(err)
	}
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	if m := strings.Join(caseRunMissing(h.task(), s, fp, LoadRuns(h.repo), ""), "; "); !strings.Contains(m, "src/a_test.go") {
		t.Fatalf("only before-fix runs exist, so the case test has no passing run in the checkout: %q", m)
	}
}

// Review round 3: an old failure in the same tests (in the baseline) doesn't
// make the control refuse.
func TestF87_ControlJudgedAgainstTheBaseline(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA", true) // TestOld fails before the task
	h.writeTest()
	h.fix("return 2")
	if r, err := h.f87BeforeFix("src/a_test.go"); err != nil || !failedNamed(r.FailedIDs, "TestA") {
		t.Fatalf("TestOld was failing before the task; the control shouldn't refuse: %+v %v", r, err)
	}
}

// Review round 3: files that aren't a [bug] case's test count for nothing.
func TestF87_BeforeFixMissingNeedsACaseFile(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	s, _, _ := LoadSetup(h.repo)
	if _, err := BeforeFixMissing(h.task(), s, []string{"src/c_test.go"}); err == nil {
		t.Fatal("a file that isn't a [bug] case's test should say it counts for nothing")
	}
}

// Review round 3: exempt targets don't count as pending outside-plan files.
func TestF87_ExemptTargetsArentPending(t *testing.T) {
	h := f87CapTask(t, nil, nil)
	tk := h.task()
	delete(tk.OutsidePlan, "src/e.go")
	must(t, os.Remove(filepath.Join(h.repo, "src", "e.go")))
	must(t, SaveTask(tk))
	write(t, filepath.Join(h.repo, "src", "gen", "x.go"), "package gen\n")
	for _, cmd := range []string{"rm -r src/gen && echo y > src/n1.go", "echo x > NOTES-tmp.md && echo y > src/n1.go"} {
		if d, r := decision(h.bash(cmd)); d == "deny" {
			t.Errorf("%s: only src/n1.go is a third file outside the plan: %s", cmd, r)
		}
	}
}

// Review round 3: another part of the line could plant a link in the
// listed folder before the listing runs.
func TestF87_ListingWithAWriterOnTheLineAsks(t *testing.T) {
	h := buildingTask(t)
	for _, p := range []string{"src/app/api/users/route.ts", "src/app/api/lib/db.ts"} {
		write(t, filepath.Join(h.repo, filepath.FromSlash(p)), "export const x = 1 // TODO\n")
	}
	write(t, filepath.Join(h.repo, ".env"), "KEY=1\n")
	h.expect(h.bash("grep -rl KEY src/app/api | xargs cat"), "", "") // premise: clean alone
	for _, cmd := range []string{
		"ln -s ../../../.env src/app/api/k.ts; grep -Rl KEY src/app/api | xargs cat",
		"ln .env src/app/api/k.ts; grep -rl KEY src/app/api | xargs cat",
		"mv .env src/app/api/k.ts && grep -rl KEY src/app/api | xargs cat",
	} {
		if d, r := decision(h.bash(cmd)); d != "ask" && d != "deny" {
			t.Errorf("%s: got %q (%s), want the secret ask or a refusal", cmd, d, r)
		}
	}
}

// Review round 4: the reader's own xargs options can take names from a file
// or cut them differently, so only plain xargs (or -0, -r) skips the ask.
func TestF87_ReaderXargsOptionsKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	write(t, filepath.Join(h.repo, "list.txt"), ".env\n")
	for _, cmd := range []string{
		"grep -rl TODO src/app/api | xargs -a list.txt cat",
		"grep -rl TODO src/app/api | xargs --arg-file=list.txt cat",
		"grep -rl TODO src/app/api | xargs -d x cat",
		"grep -rl TODO src/app/api | xargs --delimiter=x cat",
		"grep -rl TODO src/app/api | xargs -I{} cat other/{}",
		"grep -rl TODO src/app/api | xargs -I % cat other/%",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
	for _, cmd := range []string{"grep -rl TODO src/app/api | xargs -r cat", "grep -rlZ TODO src/app/api | xargs -0 cat"} {
		if d, r := decision(h.bash(cmd)); d == "ask" && cmd == "grep -rl TODO src/app/api | xargs -r cat" {
			t.Errorf("%s: plain xargs -r should still go through: %s", cmd, r)
		}
	}
}

// Review round 5: stdin redirects on the reader's xargs, and an xargs fed
// by another xargs, keep the ask.
func TestF87_XargsStdinAndNestingKeepTheAsk(t *testing.T) {
	h := f87ListingHarness(t)
	write(t, filepath.Join(h.repo, "list.txt"), ".env\n")
	for _, cmd := range []string{
		"grep -rl TODO src/app/api | xargs cat < list.txt",
		"grep -rl TODO src/app/api | xargs cat 0< list.txt",
		"grep -rl TODO src/app/api | xargs cat <<< .env",
		"grep -rl TODO src/app/api | xargs -a list.txt xargs cat",
		"grep -rl TODO src/app/api | xargs -d a xargs cat",
		"grep -rl TODO src/app/api | xargs xargs cat",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
}

// Review round 5: under the user's bug-test waiver the run counts for nothing.
func TestF87_BeforeFixMissingUnderWaiver(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	tk := h.task()
	tk.Waivers = append(tk.Waivers, Waiver{Check: "bug-test", Reason: "user", Print: fp, At: Now()})
	must(t, SaveTask(tk))
	if _, err := BeforeFixMissing(h.task(), s, []string{"src/a_test.go"}); err == nil || !strings.Contains(err.Error(), "waived") {
		t.Fatalf("under the waiver the run counts for nothing: %v", err)
	}
}
