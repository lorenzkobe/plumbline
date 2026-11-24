package main

// Blind QA for release 0.8.3 (base runs in a temp worktree, and run checks
// while building), written from the plan's case table. Fields 0.8.3 adds
// (Task.BaseTree, Run.OnBase) are reached by reflection so the file
// compiles before the change.

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lorenzkobe/plumbline/internal/core"
)

func q83Field(t *testing.T, v any, name string) reflect.Value {
	t.Helper()
	f := reflect.ValueOf(v).Elem().FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("%T has no field %s (0.8.3 adds it)", v, name)
	}
	return f
}

func q83Write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

func q83Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// q83Suite: a pytest-style suite of three tests. Every test fails when the
// file "need" is missing (a dependency the checkout has, like
// node_modules). Otherwise test_old fails while old_bug exists, test_new
// while src/a.go says "broken", test_env while .env says MODE=work.
func q83Suite(need string) string {
	return `#!/bin/sh
if [ ! -f "` + need + `" ]; then
  echo 'FAILED tests/t.py::test_old'; echo 'FAILED tests/t.py::test_new'; echo 'FAILED tests/t.py::test_env'
  echo '== 3 failed in 0.1s =='; exit 1
fi
f=0
if [ -f old_bug ]; then echo 'FAILED tests/t.py::test_old'; f=$((f+1)); fi
if grep -q broken src/a.go; then echo 'FAILED tests/t.py::test_new'; f=$((f+1)); fi
if [ -f .env ] && grep -q MODE=work .env; then echo 'FAILED tests/t.py::test_env'; f=$((f+1)); fi
p=$((3-f))
if [ $f -gt 0 ]; then echo "== $f failed, $p passed in 0.1s =="; exit 1; fi
echo '== 3 passed in 0.1s =='
`
}

type q83Opts struct {
	need     string // the dependency file the suite needs
	ignore   string // .gitignore
	oldBug   bool   // test_old already fails on the approved code
	related  string // saved related-tests command
	trackEnv string // a tracked .env's content on the approved code
}

type q83Fix struct {
	home, dir, id, base string
	marker              string // the post-checkout hook's marker file
}

// q83Base: a repo whose approved code is committed, a task in building with
// BaseTree on that code, and the dependency file present (git-ignored) in
// the checkout.
func q83Base(t *testing.T, o q83Opts) *q83Fix {
	t.Helper()
	home, dir := t.TempDir(), gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	if o.need == "" {
		o.need = "node_modules/dep/index.js"
	}
	if o.ignore == "" {
		o.ignore = "node_modules/\n"
	}
	q83Write(t, filepath.Join(dir, "test.sh"), q83Suite(o.need))
	q83Write(t, filepath.Join(dir, "src", "a.go"), "package src\n\nfunc A() int { return 1 }\n")
	q83Write(t, filepath.Join(dir, ".gitignore"), o.ignore)
	if o.oldBug {
		q83Write(t, filepath.Join(dir, "old_bug"), "x\n")
	}
	if o.trackEnv != "" {
		q83Write(t, filepath.Join(dir, ".env"), o.trackEnv)
	}
	s := &core.Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.TestRelated = o.related
	s.SuiteSeconds = 1 // a short suite: a failing related run makes its base run in the same call
	if _, err := core.SaveSetup(dir, s); err != nil {
		t.Fatal(err)
	}
	q83Git(t, dir, "add", "-A")
	if o.trackEnv != "" {
		q83Git(t, dir, "add", "-f", ".env")
	}
	q83Git(t, dir, "commit", "-q", "-m", "approved code")
	// the dependency, present in the checkout only (git-ignored or untracked)
	q83Write(t, filepath.Join(dir, o.need), "module.exports = 1\n")
	// a repo hook a base worktree must not run
	marker := filepath.Join(t.TempDir(), "hook-ran")
	q83Write(t, filepath.Join(dir, ".git", "hooks", "post-checkout"), "#!/bin/sh\ntouch '"+marker+"'\n")

	fp, err := core.Fingerprint(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	tk := &core.Task{ID: core.NewID(), Title: "fix A", Size: "small", Root: dir, Branch: "main", Phase: core.PhaseBuilding,
		Created: core.Now(), StartCommit: core.HeadCommit(dir), PlanFiles: []string{"src/a.go"}, ApprovalPrint: fp}
	q83Field(t, tk, "BaseTree").SetString(fp)
	if err := os.MkdirAll(core.TaskDocsDir(dir, tk.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	return &q83Fix{home: home, dir: dir, id: tk.ID, base: fp, marker: marker}
}

// change edits the task's code and records it as the task's change.
func (f *q83Fix) change(t *testing.T, rel, body string) {
	t.Helper()
	q83Write(t, filepath.Join(f.dir, rel), body)
	tk, err := core.LoadTask(f.dir, f.id)
	if err != nil {
		t.Fatal(err)
	}
	tk.LastCodeChange = core.Now()
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
}

func (f *q83Fix) setBase(t *testing.T, tree string) {
	t.Helper()
	tk, err := core.LoadTask(f.dir, f.id)
	if err != nil {
		t.Fatal(err)
	}
	q83Field(t, tk, "BaseTree").SetString(tree)
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
}

func (f *q83Fix) run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	return plumbline(t, f.dir, f.home, "", append(args, "--task", f.id)...)
}

// baseRuns: the task's runs made on the approved tree in a temp worktree.
func (f *q83Fix) baseRuns(t *testing.T) []*core.Run {
	t.Helper()
	var out []*core.Run
	for _, r := range core.LoadRuns(f.dir) {
		if r.TaskID == f.id && q83Field(t, r, "OnBase").Bool() {
			out = append(out, r)
		}
	}
	return out
}

func (f *q83Fix) worktrees(t *testing.T) int {
	t.Helper()
	return strings.Count(q83Git(t, f.dir, "worktree", "list", "--porcelain"), "worktree ")
}

const q83Fixed = "package src\n\nfunc A() int { return 2 }\n"
const q83Broken = "package src\n\n// broken\nfunc A() int { return 2 }\n"

// C7, C10, C13
func TestQA083_C7_FailureAlsoOnBaseIsOld(t *testing.T) {
	f := q83Base(t, q83Opts{oldBug: true})
	f.change(t, "src/a.go", q83Fixed)
	out, code := f.run(t, "run", "test")
	if code != 0 || !strings.Contains(out, "test recorded: PASS") {
		t.Fatalf("test_old fails on the approved tree too, so it is old: exit %d\n%s", code, out)
	}
	base := f.baseRuns(t)
	if len(base) != 1 {
		t.Fatalf("one base run on the approved tree, got %d", len(base))
	}
	b := base[0]
	if !b.Baseline || b.Kind != core.KindTest || b.PrintBefore != f.base || b.PrintAfter != f.base || b.Command != "./test.sh" {
		t.Fatalf("the base run is a baseline on BaseTree %s with the same command: %+v", f.base, b)
	}
	if strings.Join(b.FailedIDs, ",") != "tests/t.py::test_old" {
		t.Fatalf("the base run ran with node_modules linked in, so only test_old failed there: %v", b.FailedIDs)
	}
	if n := f.worktrees(t); n != 1 {
		t.Fatalf("the base worktree is removed after the run (its command failed): %d worktrees", n)
	}
	if _, err := os.Stat(f.marker); err == nil {
		t.Fatal("the repo's post-checkout hook ran in the base worktree; hooks must be off")
	}
	if fi, err := os.Lstat(filepath.Join(f.dir, "node_modules")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("the checkout's own node_modules is untouched: %v %v", fi, err)
	}

	// a new failing name blocks, and the base run is reused, not made again (C13)
	f.change(t, "src/a.go", q83Broken)
	out, code = f.run(t, "run", "test")
	if code == 0 || !strings.Contains(out, "test recorded: FAIL") || !strings.Contains(out, "test_new") {
		t.Fatalf("test_new is new, so the run fails naming it: exit %d\n%s", code, out)
	}
	if n := len(f.baseRuns(t)); n != 1 {
		t.Fatalf("a second failing run reuses the base run: %d base runs", n)
	}
}

// C28
func TestQA083_C28_RelatedFailureJudgedOldByName(t *testing.T) {
	f := q83Base(t, q83Opts{oldBug: true, related: "./test.sh {files}"})
	f.change(t, "src/a.go", q83Fixed)
	out, code := f.run(t, "run", "related", "--files", "src/a.go")
	if code != 0 || !strings.Contains(out, "related recorded: PASS") {
		t.Fatalf("the related run's only failure also fails on the approved tree: exit %d\n%s", code, out)
	}
	base := f.baseRuns(t)
	if len(base) != 1 || base[0].Kind != core.KindTest || base[0].Command != "./test.sh" {
		t.Fatalf("the base run is the full suite: %+v", base)
	}
}

// C8
func TestQA083_C8_NoBaseRunWhenGitCantMakeIt(t *testing.T) {
	f := q83Base(t, q83Opts{oldBug: true})
	f.setBase(t, strings.Repeat("1", 40)) // a tree object that doesn't exist
	f.change(t, "src/a.go", q83Fixed)
	out, code := f.run(t, "run", "test")
	if code == 0 || !strings.Contains(out, "test recorded: FAIL") {
		t.Fatalf("without a base run every failure counts: exit %d\n%s", code, out)
	}
	if n := len(f.baseRuns(t)); n != 0 {
		t.Fatalf("no base run when git can't make one: %d", n)
	}
	i := strings.Index(out, "== 1 failed")
	if i < 0 {
		t.Fatalf("the suite's own output is missing: %s", out)
	}
	tail := out[i:]
	if !strings.Contains(strings.ToLower(tail), "base") {
		t.Fatalf("the output says why there is no base run: %s", tail)
	}
	if n := f.worktrees(t); n != 1 {
		t.Fatalf("no worktree is left behind: %d", n)
	}
}

// C9
func TestQA083_C9_BaseWhereEveryTestFailsIsNoBaseline(t *testing.T) {
	// local.cfg is git-ignored but not a linked kind, so the base lacks it
	f := q83Base(t, q83Opts{oldBug: true, need: "local.cfg", ignore: "local.cfg\n"})
	f.change(t, "src/a.go", q83Fixed)
	out, code := f.run(t, "run", "test")
	if code == 0 || !strings.Contains(out, "test recorded: FAIL") {
		t.Fatalf("a base where every test failed isn't a baseline, so test_old counts: exit %d\n%s", code, out)
	}
	if n := f.worktrees(t); n != 1 {
		t.Fatalf("no worktree is left behind: %d", n)
	}
}

// C10 (refused side): only git-ignored dependency folders are linked.
func TestQA083_C10_UntrackedNotIgnoredVenvIsNotLinked(t *testing.T) {
	f := q83Base(t, q83Opts{oldBug: true, need: "venv/ok", ignore: "node_modules/\n"})
	// The approved tree holds untracked files, so venv must appear after approval
	// for the base to lack it.
	venv := filepath.Join(f.dir, "venv", "ok")
	if err := os.Remove(venv); err != nil {
		t.Fatal(err)
	}
	fp, err := core.Fingerprint(f.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.setBase(t, fp)
	q83Write(t, venv, "module.exports = 1\n")
	f.change(t, "src/a.go", q83Fixed)
	out, code := f.run(t, "run", "test")
	if code == 0 {
		t.Fatalf("venv isn't git-ignored, so it isn't linked and the base has no baseline: %s", out)
	}
}

// C10 (refused side): a link never replaces a file the base tree has.
func TestQA083_C10_TrackedEnvIsNotReplaced(t *testing.T) {
	f := q83Base(t, q83Opts{ignore: "node_modules/\n.env\n", trackEnv: "MODE=base\n"})
	f.change(t, ".env", "MODE=work\n")
	out, code := f.run(t, "run", "test")
	if code == 0 || !strings.Contains(out, "test_env") {
		t.Fatalf("the base tree's own .env says MODE=base, so test_env is new: exit %d\n%s", code, out)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, ".env"))
	if err != nil || string(b) != "MODE=work\n" {
		t.Fatalf("the checkout's .env is untouched: %q %v", b, err)
	}
}

// C32 (CLI): a passing base run isn't reused by the next task on that tree.
func TestQA083_C32_PassingBaseRunNotReusedByAnotherTask(t *testing.T) {
	f := q83Base(t, q83Opts{})
	f.change(t, "src/a.go", q83Broken)
	if out, code := f.run(t, "run", "test"); code == 0 {
		t.Fatalf("test_new is new: %s", out)
	}
	base := f.baseRuns(t)
	if len(base) != 1 || base[0].Failed != 0 {
		t.Fatalf("one passing base run: %+v", base)
	}
	tk, _ := core.LoadTask(f.dir, f.id)
	tk.Phase = core.PhaseCancelled
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	q83Git(t, f.dir, "checkout", "--", "src/a.go")
	tk2 := &core.Task{ID: core.NewID(), Title: "next", Size: "small", Root: f.dir, Branch: "main", Phase: core.PhaseBuilding,
		Created: core.Now(), StartCommit: core.HeadCommit(f.dir), PlanFiles: []string{"src/a.go"}, ApprovalPrint: f.base}
	if err := os.MkdirAll(core.TaskDocsDir(f.dir, tk2.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveTask(tk2); err != nil {
		t.Fatal(err)
	}
	out, _ := plumbline(t, f.dir, f.home, "", "run", "test", "--task", tk2.ID)
	if strings.Contains(out, "reused the passing run") {
		t.Fatalf("a base run is never reused for another task: %s", out)
	}
}

// --- run checks while building ---

type q83Checks struct {
	home, dir, id string
}

// q83ChecksTask: a task in building whose setup saves a full suite, maybe
// a related command, and lint, build and security commands that print
// what ran.
func q83ChecksTask(t *testing.T, related bool) *q83Checks {
	t.Helper()
	home, dir := t.TempDir(), gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	q83Write(t, filepath.Join(dir, "full.sh"), "#!/bin/sh\necho full-suite-ran\necho '== 3 passed in 0.1s =='\n")
	q83Write(t, filepath.Join(dir, "rel.sh"), "#!/bin/sh\necho \"related-ran: $*\"\necho '== 1 passed in 0.1s =='\n")
	q83Write(t, filepath.Join(dir, "src", "a.go"), "package src\n\nfunc A() int { return 1 }\n")
	s := &core.Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./full.sh"
	if related {
		s.Commands.TestRelated = "./rel.sh {files}"
	}
	s.Commands.Lint = "echo lint-ran"
	s.Commands.Build = "echo build-ran"
	s.Commands.Security = "echo security-ran"
	if _, err := core.SaveSetup(dir, s); err != nil {
		t.Fatal(err)
	}
	q83Git(t, dir, "add", "-A")
	q83Git(t, dir, "commit", "-q", "-m", "approved code")
	fp, err := core.Fingerprint(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	tk := &core.Task{ID: core.NewID(), Title: "fix A", Size: "small", Root: dir, Branch: "main", Phase: core.PhaseBuilding,
		Created: core.Now(), StartCommit: core.HeadCommit(dir), PlanFiles: []string{"src/a.go", "src/a_test.go"}, ApprovalPrint: fp}
	if err := os.MkdirAll(core.TaskDocsDir(dir, tk.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	return &q83Checks{home: home, dir: dir, id: tk.ID}
}

func (c *q83Checks) checks(t *testing.T, args ...string) string {
	t.Helper()
	out, code := plumbline(t, c.dir, c.home, "", append([]string{"run", "checks", "--task", c.id}, args...)...)
	if code != 0 {
		t.Fatalf("run checks: exit %d\n%s", code, out)
	}
	return out
}

func (c *q83Checks) edit(t *testing.T, rel, body string) {
	t.Helper()
	q83Write(t, filepath.Join(c.dir, rel), body)
	tk, err := core.LoadTask(c.dir, c.id)
	if err != nil {
		t.Fatal(err)
	}
	tk.LastCodeChange = core.Now()
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
}

func (c *q83Checks) proofMissing(t *testing.T) string {
	t.Helper()
	tk, err := core.LoadTask(c.dir, c.id)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := core.LoadSetup(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(core.ProofMissing(tk, s, true), "; ")
}

// C14
func TestQA083_C14_RunChecksRelatedWhileBuilding(t *testing.T) {
	c := q83ChecksTask(t, true)
	if out := c.checks(t); !strings.Contains(out, "full-suite-ran") || strings.Contains(out, "related-ran") {
		t.Fatalf("nothing changed since the start commit: the full suite runs\n%s", out)
	}
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	out := c.checks(t)
	if !strings.Contains(out, "related-ran: ") || !strings.Contains(out, "src/a.go") || strings.Contains(out, "full-suite-ran") {
		t.Fatalf("while building, run checks runs the related tests on the changed files\n%s", out)
	}
	if out := c.checks(t, "--final"); !strings.Contains(out, "full-suite-ran") {
		t.Fatalf("--final runs the full suite\n%s", out)
	}
	n := c.proofMissing(t)
	if strings.Contains(n, "full test suite") {
		t.Fatalf("the --final run is the commit's proof: %s", n)
	}
}

// C14 (no related command)
func TestQA083_C14_NoRelatedCommandRunsFullSuite(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	if out := c.checks(t); !strings.Contains(out, "full-suite-ran") {
		t.Fatalf("with no related command, run checks runs the full suite\n%s", out)
	}
}

// C15, C16
func TestQA083_C15_BuildAndSecuritySkippedForTestOnlyChange(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	out := c.checks(t)
	for _, want := range []string{"lint-ran", "build-ran", "security-ran", "full-suite-ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the first run after the change runs every check (%s)\n%s", want, out)
		}
	}
	// only a test file changed since build and security passed
	c.edit(t, "src/a_test.go", "package src\n")
	out = c.checks(t)
	if !strings.Contains(out, "build-ran") || !strings.Contains(out, "security-ran") {
		t.Fatalf("builds type-check tests and secret scanners read them, so both rerun after a test change\n%s", out)
	}
	if !strings.Contains(out, "lint-ran") {
		t.Fatalf("lint reads tests, so a test change reruns it (C16)\n%s", out)
	}
	if m := c.proofMissing(t); m != "" {
		t.Fatalf("ProofMissing accepts the same skips: %s", m)
	}
	// only a note changed since their pass: markdown can feed lint (markdownlint,
	// prettier) and the build (docs sites), so those rerun; security doesn't read it
	c.edit(t, "README.md", "# notes\n")
	out = c.checks(t)
	if !strings.Contains(out, "lint-ran") || !strings.Contains(out, "build-ran") || !strings.Contains(out, "security-ran") {
		t.Fatalf("a notes-only change reruns lint, build and security\n%s", out)
	}
	if m := c.proofMissing(t); m != "" {
		t.Fatalf("ProofMissing accepts the notes-only skip: %s", m)
	}
	// a source change: nothing skipped, and ProofMissing wants new runs
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	m := c.proofMissing(t)
	for _, k := range []string{"build:", "security:", "lint:"} {
		if !strings.Contains(m, k) {
			t.Errorf("after a source change ProofMissing wants a new %s run: %s", k, m)
		}
	}
	out = c.checks(t)
	for _, want := range []string{"lint-ran", "build-ran", "security-ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("a source change reruns %s\n%s", want, out)
		}
	}
}

// C15 (gate file)
func TestQA083_C15_GateFileChangeNotSkipped(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	c.checks(t)
	c.edit(t, ".github/workflows/ci.yml", "on: push\n")
	if m := c.proofMissing(t); !strings.Contains(m, "build:") {
		t.Fatalf("a gate file change isn't a test or a note: build needs a new run: %s", m)
	}
	if out := c.checks(t); !strings.Contains(out, "build-ran") {
		t.Fatalf("a gate file change reruns the build\n%s", out)
	}
}

// C36
func TestQA083_C36_BuildSkipByTestFileName(t *testing.T) {
	// Builds type-check tests (next build, tsc -b), so no test change skips one (review H4).
	for _, rel := range []string{"src/a.test.ts", "src/a.spec.js", "tests/test_a.py", "src/b_test.go", "mocks/api.go", "test-utils/helpers.ts"} {
		t.Run("reruns "+rel, func(t *testing.T) {
			c := q83ChecksTask(t, false)
			c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
			c.checks(t)
			c.edit(t, rel, "x\n")
			if out := c.checks(t); !strings.Contains(out, "build-ran") {
				t.Fatalf("a change to %s reruns the build\n%s", rel, out)
			}
		})
	}
}
