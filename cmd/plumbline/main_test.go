package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lorenzkobe/plumbline/internal/core"
)

// bin is the plumbline binary built once for these tests: each command runs
// in its own process, as Claude Code and the terminal run it.
var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "plumbline-bin-*")
	if err != nil {
		panic(err)
	}
	bin = filepath.Join(dir, "plumbline")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		panic(string(out))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func plumbline(t *testing.T, dir, home, stdin string, args ...string) (string, int) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "PLUMBLINE_HOME="+home)
	c.Stdin = strings.NewReader(stdin)
	out, err := c.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

// A hook that can't read its input blocks a gate (pre), lets a search go
// through (pre-search), and leaves a crash report either way.
func TestHookFailureBlocksOnlyGates(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	out, code := plumbline(t, dir, home, "not json", "hook", "pre")
	if code != 2 || !strings.Contains(out, "Report:") {
		t.Fatalf("pre on bad input: exit %d, %s", code, out)
	}
	out, code = plumbline(t, dir, home, "not json", "hook", "pre-search")
	if code != 0 || !strings.Contains(out, "Report:") {
		t.Fatalf("pre-search on bad input should let the search through: exit %d, %s", code, out)
	}
	reports, _ := os.ReadDir(filepath.Join(home, "crashes"))
	if len(reports) != 2 {
		t.Fatalf("want 2 crash reports, got %d", len(reports))
	}
	b, _ := os.ReadFile(filepath.Join(home, "crashes", reports[0].Name()))
	if strings.Contains(string(b), "not json") {
		t.Fatalf("a crash report must not hold the hook's input: %s", b)
	}
	// A search's hook input that parses runs the usual check (nothing to say for the lead).
	out, code = plumbline(t, dir, home, `{"session_id":"s","cwd":"`+dir+`","tool_name":"Grep","tool_input":{"pattern":"x"}}`, "hook", "pre-search")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("a normal search: exit %d, %q", code, out)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "T"}, {"commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	dir, _ = filepath.EvalSymlinks(dir)
	return dir
}

func TestTaskSize(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home) // for the core calls below
	// SizeByUser: the user typed /plumbline:size small; Claude's own resize
	// below takes that back.
	tk := &core.Task{ID: core.NewID(), Title: "fix A", Size: "small", SizeByUser: true, Root: repo, Branch: core.CurrentBranch(repo), Phase: core.PhaseUnderstanding, Created: core.Now()}
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, repo, home, "", "task", "size", "huge", "--why", "x"); code == 0 {
		t.Fatalf("an unknown size should fail: %s", out)
	}
	if out, code := plumbline(t, repo, home, "", "task", "size", "normal"); code == 0 || !strings.Contains(out, "--why") {
		t.Fatalf("a size change needs a reason: %s", out)
	}
	if out, code := plumbline(t, repo, home, "", "task", "size", "normal", "--why", "touches auth"); code != 0 {
		t.Fatalf("resize before approval: exit %d, %s", code, out)
	}
	got, err := core.LoadTask(repo, tk.ID)
	if err != nil || got.Size != "normal" || got.SizeWhy != "touches auth" || got.SizeByUser {
		t.Fatalf("size not saved: %+v %v", got, err)
	}
	// Once the plan is approved, the size is fixed.
	got.Phase = core.PhaseBuilding
	if err := core.SaveTask(got); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, repo, home, "", "task", "size", "small", "--why", "tiny"); code == 0 || !strings.Contains(out, "fixed once the plan is approved") {
		t.Fatalf("resize after approval should fail: %s", out)
	}
}

// plumbline builder: list, merge and drop a builder's copy, as the lead runs
// them from the terminal.
func TestBuilderCommands(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tk := &core.Task{ID: core.NewID(), Title: "fix A", Size: "small", Root: repo, Branch: core.CurrentBranch(repo), Phase: core.PhaseBuilding, Created: core.Now()}
	if out, _ := plumbline(t, repo, home, "", "builder", "list"); !strings.Contains(out, "no open task") {
		t.Fatalf("no task: %s", out)
	}
	wt := core.BuilderWorktree(repo, tk.ID, "b1")
	base, tree, err := core.MakeWorktree(repo, nil, wt)
	if err != nil {
		t.Fatal(err)
	}
	tk.Helpers = []core.HelperRun{{Kind: core.HelperBuilder, AgentID: "b1", Started: core.Now().Add(-time.Hour), Finished: core.Now(), Worktree: wt, BaseCommit: base, PrintBefore: tree, Findings: "build-1.md"}}
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, repo, home, "", "builder", "list"); code != 0 || !strings.Contains(out, "1 files changed, not merged yet") {
		t.Fatalf("list: exit %d, %s", code, out)
	}
	if out, code := plumbline(t, repo, home, "", "builder", "merge"); code == 0 || !strings.Contains(out, "which builder") {
		t.Fatalf("merge needs a number: %s", out)
	}
	if out, code := plumbline(t, repo, home, "", "builder", "merge", "2"); code == 0 || !strings.Contains(out, "no builder 2") {
		t.Fatalf("an unknown builder: %s", out)
	}
	if out, code := plumbline(t, repo, home, "", "builder", "merge", "1"); code != 0 || !strings.Contains(out, "a.txt") {
		t.Fatalf("merge: exit %d, %s", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "a.txt")); string(b) != "two\n" {
		t.Fatalf("merged content = %q", b)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("the copy should be removed after the merge")
	}
	if out, code := plumbline(t, repo, home, "", "builder", "drop", "1"); code == 0 || !strings.Contains(out, "already merged") {
		t.Fatalf("drop after merge: %s", out)
	}
	if out, _ := plumbline(t, repo, home, "", "builder", "list"); !strings.Contains(out, "builder 1: merged") {
		t.Fatalf("list after merge: %s", out)
	}
}

// --files takes paths: one that starts with "-" would reach the runner as a
// flag (--deselect a case test) while the run still names the file.
func TestRelatedFilesRefuseFlags(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	s := &core.Setup{Kind: "personal"}
	s.Commands.Test = "true"
	s.Commands.TestRelated = "echo {files}"
	if _, err := core.SaveSetup(repo, s); err != nil {
		t.Fatal(err)
	}
	if err := core.ConfirmSetup(repo); err != nil {
		t.Fatal(err)
	}
	out, code := plumbline(t, repo, home, "", "run", "related", "--files", "tests/test_x.py,--deselect,tests/test_x.py::test_case")
	if code == 0 || !strings.Contains(out, "not flags") {
		t.Fatalf("a flag in --files should be refused: %d %s", code, out)
	}
}

// WF3: the saved format, typecheck and security commands run and are
// recorded as their own kinds; an unsaved one says so.
func TestRunTheSavedChecks(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	s := &core.Setup{Kind: "personal"}
	s.Commands.Test = "true"
	s.Commands.Format = "echo formatted"
	s.Commands.Typecheck = "echo 'error: x is a string'; exit 2"
	if _, err := core.SaveSetup(repo, s); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, repo, home, "", "run", "format"); code != 0 || !strings.Contains(out, "format recorded: PASS") {
		t.Fatalf("format: %d %s", code, out)
	}
	if out, code := plumbline(t, repo, home, "", "run", "typecheck"); code != 2 || !strings.Contains(out, "typecheck recorded: FAIL") {
		t.Fatalf("typecheck: %d %s", code, out)
	}
	if out, code := plumbline(t, repo, home, "", "run", "security"); code == 0 || !strings.Contains(out, "no security command is saved") {
		t.Fatalf("security isn't saved: %d %s", code, out)
	}
	kinds := map[string]int{}
	for _, r := range core.LoadRuns(repo) {
		kinds[r.Kind]++
	}
	if kinds[core.KindFormat] != 1 || kinds[core.KindTypecheck] != 1 || len(kinds) != 2 {
		t.Fatalf("recorded runs: %v", kinds)
	}
	// A format command that rewrites files can't be saved.
	out, code := plumbline(t, repo, home, `{"kind": "personal", "commands": {"test": "true", "format": "npx prettier --write ."}}`, "setup", "save")
	if code == 0 || !strings.Contains(out, "rewrites files") {
		t.Fatalf("a rewriting format command was saved: %d %s", code, out)
	}
}

// WF4: bug-test is a check the user may waive.
func TestWaiveBugTest(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	out, code := plumbline(t, repo, home, "", "waive", "bug-test", "--reason", "needs a card terminal")
	if code == 0 || strings.Contains(out, "can be waived") {
		t.Fatalf("bug-test should pass the check-name test (and fail only for having no task): %d %s", code, out)
	}
	if out, _ := plumbline(t, repo, home, "", "waive", "tests", "--reason", "x"); !strings.Contains(out, "bug-test") {
		t.Fatalf("the refusal should list bug-test: %s", out)
	}
}

// RP1: --help or -h on a subcommand prints the usage and does nothing:
// `plumbline task replan --help` sent a smoke-run task back to planning.
func TestSubcommandHelpDoesNothing(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	if out, code := plumbline(t, dir, home, `{"kind": "personal", "commands": {"test": "true"}}`, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	out, code := plumbline(t, dir, home, "", "task", "start", "--title", "x")
	if code != 0 {
		t.Fatalf("start: %d %s", code, out)
	}
	for _, args := range [][]string{{"task", "replan", "--help"}, {"task", "finish", "-h"}, {"task", "start", "--title", "y", "--help"}, {"pause", "--help"}, {"setup", "save", "-h"}} {
		out, code := plumbline(t, dir, home, "", args...)
		if code != 0 || !strings.Contains(out, "plumbline task start") {
			t.Fatalf("%v: exit %d, no usage: %s", args, code, out)
		}
	}
	out, _ = plumbline(t, dir, home, "", "status")
	if !strings.Contains(out, "understanding step") || strings.Contains(out, "paused") {
		t.Fatalf("a --help call changed the task: %s", out)
	}
	// After "--" a -h belongs to the command being run.
	if out, _ := plumbline(t, dir, home, "", "run", "proof", "api", "--", "true", "-h"); strings.Contains(out, "plumbline task start --title") {
		t.Fatalf("-h after -- was read as Plumbline's: %s", out)
	}
}

// UX2: one command runs every saved check (not related or e2e), each
// recorded as if run on its own, and keeps going past a failing one.
func TestRunChecksRunsEverySavedCheck(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	setup := `{"kind": "personal", "commands": {"test": "echo tests-ran", "lint": "echo lint-ran; exit 1", "typecheck": "echo types-ran", "e2e": "echo e2e-ran"}}`
	if out, code := plumbline(t, dir, home, setup, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	out, code := plumbline(t, dir, home, "", "run", "checks")
	for _, want := range []string{"tests-ran", "lint-ran", "types-ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%s didn't run: %s", want, out)
		}
	}
	if strings.Contains(out, "e2e-ran") {
		t.Fatalf("e2e needs the app running; run checks must not start it: %s", out)
	}
	if code == 0 {
		t.Fatalf("a failing check must fail the whole run: %s", out)
	}
	if !strings.Contains(out, "lint: FAIL") || !strings.Contains(out, "test: PASS") {
		t.Fatalf("no summary line per check: %s", out)
	}
}

// UX2: task start tells Claude what the plan question will need, by size.
func TestTaskStartChecklist(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	if out, code := plumbline(t, dir, home, `{"kind": "personal", "commands": {"test": "true"}}`, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	out, _ := plumbline(t, dir, home, "", "task", "start", "--title", "x", "--size", "small", "--why", "one local rename")
	for _, want := range []string{"plan.md", "## Goal", "Write tool", "Drop it", "plumbline run checks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("small checklist lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "understanding.md") || strings.Contains(out, "Start the challenger") {
		t.Fatalf("small checklist asks for what small skips: %s", out)
	}
	out, _ = plumbline(t, dir, home, "", "task", "start", "--title", "y", "--size", "normal", "--why", "shared helper")
	if !strings.Contains(out, "in the chat") || strings.Contains(out, "plan-for-you.md") || !strings.Contains(out, "understanding.md") {
		t.Fatalf("normal checklist lacks the plain plan or the understanding: %s", out)
	}
	if strings.Contains(out, "No size was given") {
		t.Fatalf("a task started with a size was told it had none: %s", out)
	}
	// SZ1: started without a size, it's normal, and Claude is told to judge
	// it; switching to small prints the small steps.
	out, _ = plumbline(t, dir, home, "", "task", "start", "--title", "z")
	if !strings.Contains(out, "(normal,") || !strings.Contains(out, "No size was given") || !strings.Contains(out, "plumbline task size small") {
		t.Fatalf("a task without a size should say so: %s", out)
	}
	out, code := plumbline(t, dir, home, "", "task", "size", "small", "--why", "a label", "--task", taskID(out))
	if code != 0 || !strings.Contains(out, "Next (small)") || strings.Contains(out, "Start the challenger") {
		t.Fatalf("task size small should print the small steps: %d %s", code, out)
	}
}

// PR1: a unit test runner isn't accepted as the real-app proof; the
// plan question asks Claude to show its suggestions.
func TestUIProofNeedsTheApp(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	if out, code := plumbline(t, dir, home, `{"kind": "personal", "commands": {"test": "true"}}`, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	out, _ := plumbline(t, dir, home, "", "task", "start", "--title", "x", "--size", "small", "--why", "a label")
	if !strings.Contains(out, "## Suggestions") {
		t.Fatalf("the checklist doesn't mention suggestions: %s", out)
	}
	out, code := plumbline(t, dir, home, "", "run", "proof", "ui", "--task", taskID(out), "--", "npx", "vitest", "run", "a.test.tsx")
	if code == 0 || !strings.Contains(out, "aren't the real app") {
		t.Fatalf("vitest as ui proof should be refused: %d %s", code, out)
	}
	out, code = plumbline(t, dir, home, "", "run", "proof", "ui", "--", "curl", "-s", "http://localhost:5173/src/a.tsx")
	if code == 0 || !strings.Contains(out, "doesn't show the screen works") {
		t.Fatalf("curl as ui proof should be refused: %d %s", code, out)
	}
}

// taskID: the id that plumbline task start printed.
func taskID(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "plumbline-task-id: ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "plumbline-task-id: "))
		}
	}
	return ""
}

// FL1: a failed run names its failing tests in its last line, and run
// checks names them in its summary, so | tail -3 still shows them.
func TestFailedRunNamesTests(t *testing.T) {
	home := t.TempDir()
	repo := gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	s := &core.Setup{Kind: "personal"}
	s.Commands.Test = "printf 'FAILED tests/test_x.py::TestSignOut - assert 1\\n===== 1 failed, 1 passed in 0.10s =====\\n'; exit 1"
	s.Commands.Lint = "true"
	if _, err := core.SaveSetup(repo, s); err != nil {
		t.Fatal(err)
	}
	out, code := plumbline(t, repo, home, "", "run", "checks")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if code == 0 || !strings.Contains(last, "test: FAIL (") || !strings.Contains(last, "TestSignOut") || !strings.Contains(last, "lint: PASS") {
		t.Fatalf("summary should name the failing test: %d %q\n%s", code, last, out)
	}
	if !strings.Contains(out, "Failed: ") {
		t.Fatalf("the test run's own line should name it: %s", out)
	}
}

func TestRunChecksDocsOnlySkipsSuite(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	setup := `{"kind": "personal", "commands": {"test": "echo tests-ran", "lint": "echo lint-ran"}}`
	if out, code := plumbline(t, dir, home, setup, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", "setup").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	newTask := func(files ...string) string {
		tk := &core.Task{ID: core.NewID(), Title: "docs", Size: "small", Root: dir, Branch: "main", Phase: core.PhaseBuilding, Created: core.Now(), StartCommit: core.HeadCommit(dir), PlanFiles: files}
		if err := os.MkdirAll(core.TaskDocsDir(dir, tk.ID), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := core.SaveTask(tk); err != nil {
			t.Fatal(err)
		}
		return tk.ID
	}
	id := newTask("docs/guide.md")
	out, code := plumbline(t, dir, home, "", "run", "checks", "--task", id)
	if code != 0 || strings.Contains(out, "tests-ran") || !strings.Contains(out, "lint-ran") || !strings.Contains(out, "test: not needed") {
		t.Fatalf("a docs-only task's checks run lint but not the suite: %d %s", code, out)
	}
	tk, err := core.LoadTask(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	tk.Phase = core.PhaseCancelled
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	id = newTask("docs/guide.md", "src/a.go")
	out, _ = plumbline(t, dir, home, "", "run", "checks", "--task", id)
	if !strings.Contains(out, "tests-ran") {
		t.Fatalf("a task with code runs the suite: %s", out)
	}
}

func TestEnvNamesRefusesOtherFiles(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	for _, name := range []string{"id_rsa", "a.pem", "notes.txt", ".envrc", "env"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("TOPSECRET=hunter2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		out, code := plumbline(t, dir, home, "", "env-names", name)
		if code == 0 || strings.Contains(out, "TOPSECRET") || strings.Contains(out, "hunter2") {
			t.Errorf("%s: exit %d, output %q", name, code, out)
		}
	}
	out, code := plumbline(t, dir, home, "", "env-names", ".env")
	if code == 0 || strings.Contains(out, "TOPSECRET") {
		t.Errorf("missing .env: exit %d, output %q", code, out)
	}
	if out, code := plumbline(t, dir, home, "", "env-names"); code == 0 {
		t.Errorf("no file: exit %d, output %q", code, out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TOPSECRET=hunter2\nexport B='x'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, dir, home, "", "env-names", ".env"); code != 0 || out != "TOPSECRET\nB\n" {
		t.Errorf(".env: exit %d, output %q", code, out)
	}
}

func TestRunChecksCommandsOnly(t *testing.T) {
	home, dir := t.TempDir(), gitRepo(t)
	t.Setenv("PLUMBLINE_HOME", home)
	setup := `{"kind": "personal", "commands": {"test": "echo tests-ran", "lint": "echo lint-ran"}}`
	if out, code := plumbline(t, dir, home, setup, "setup", "save"); code != 0 {
		t.Fatalf("setup: %d %s", code, out)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		t.Fatalf("git add: %s", out)
	}
	if out, err := exec.Command("git", "-C", dir, "commit", "-q", "-m", "setup").CombinedOutput(); err != nil {
		t.Fatalf("git commit: %s", out)
	}
	tk := &core.Task{ID: core.NewID(), Title: "clear the cache", Size: "normal", Root: dir, Branch: "main", Phase: core.PhaseBuilding, Created: core.Now(), StartCommit: core.HeadCommit(dir), CommandsOnly: true}
	if err := os.MkdirAll(core.TaskDocsDir(dir, tk.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	out, code := plumbline(t, dir, home, "", "run", "checks", "--task", tk.ID)
	if code != 0 || strings.Contains(out, "tests-ran") || strings.Contains(out, "lint-ran") || !strings.Contains(out, "commands only") {
		t.Fatalf("a commands-only task's checks run nothing and say why: %d %s", code, out)
	}
	out, _ = plumbline(t, dir, home, "", "status", "--task", tk.ID)
	if !strings.Contains(out, "Commands only") || strings.Contains(out, "Reviewers needed") || strings.Contains(out, "full test suite") {
		t.Fatalf("status says commands only and lists no suite or reviewers: %s", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = plumbline(t, dir, home, "", "run", "checks", "--task", tk.ID)
	if !strings.Contains(out, "tests-ran") || strings.Contains(out, "commands only") {
		t.Fatalf("with a file changed, the checks run again: %s", out)
	}
}

// --- 0.8.8 blind QA (brief-qa-1.md): run checks in a fix round ---

// r88Reviewed records a finished, counted code review on the code as it is
// now (a hand-made HelperRun with the fields counts() reads), so the task is
// in a fix round from here on.
func (c *q83Checks) r88Reviewed(t *testing.T, findings string) {
	t.Helper()
	tk, err := core.LoadTask(c.dir, c.id)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := core.Fingerprint(c.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	name := "code-review-1.md"
	q83Write(t, filepath.Join(core.TaskDocsDir(c.dir, c.id), name), findings)
	tk.Helpers = append(tk.Helpers, core.HelperRun{Kind: core.HelperCodeReviewer, AgentID: "code-reviewer-1", Started: core.Now().Add(-time.Minute),
		Finished: core.Now(), PrintBefore: fp, PrintAfter: fp, Findings: name})
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
}

const r88NextLine = "next: write self-review.md (Write or Edit), then start the reviewers"

// C16, C49
func TestR88_C16_C49_FixRoundSkipsLintAndBuild(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	c.checks(t)
	c.r88Reviewed(t, "# Code review 1\n## Findings\n- none\n")
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	out := c.checks(t)
	if strings.Contains(out, "lint-ran") || strings.Contains(out, "build-ran") {
		t.Fatalf("a fix round doesn't run lint or build\n%s", out)
	}
	for _, want := range []string{"lint: runs at --final (fix round)", "build: runs at --final (fix round)", "security-ran", "full-suite-ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in a fix round's run checks\n%s", want, out)
		}
	}
}

// C49: format and typecheck run every round too.
func TestR88_C49_FormatAndTypecheckRunEveryRound(t *testing.T) {
	c := q83ChecksTask(t, false)
	s, _, err := core.LoadSetup(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Commands.Format, s.Commands.Typecheck = "echo format-ran", "echo typecheck-ran"
	if _, err := core.SaveSetup(c.dir, s); err != nil {
		t.Fatal(err)
	}
	q83Git(t, c.dir, "add", "-A")
	q83Git(t, c.dir, "commit", "-q", "-m", "more checks")
	tk, _ := core.LoadTask(c.dir, c.id)
	tk.StartCommit = core.HeadCommit(c.dir)
	tk.ApprovalPrint, _ = core.Fingerprint(c.dir, nil)
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	c.checks(t)
	c.r88Reviewed(t, "# Code review 1\n## Findings\n- none\n")
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	out := c.checks(t)
	for _, want := range []string{"format-ran", "typecheck-ran", "security-ran"} {
		if !strings.Contains(out, want) {
			t.Fatalf("%s runs in a fix round\n%s", want, out)
		}
	}
}

// C17, C18
func TestR88_C17_C18_FirstRoundAndFinalRunLintAndBuild(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	out := c.checks(t)
	if !strings.Contains(out, "lint-ran") || !strings.Contains(out, "build-ran") {
		t.Fatalf("the first run checks (no review yet) run lint and build\n%s", out)
	}
	c.r88Reviewed(t, "# Code review 1\n## Findings\n- none\n")
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	c.checks(t)
	// C18: lint and build haven't run on the final code, so the commit
	// proof still asks for them.
	if n := c.proofMissing(t); !strings.Contains(n, "lint") || !strings.Contains(n, "build") {
		t.Fatalf("the commit proof needs lint and build on the final code: %s", n)
	}
	out = c.checks(t, "--final")
	if !strings.Contains(out, "lint-ran") || !strings.Contains(out, "build-ran") {
		t.Fatalf("--final runs lint and build in a fix round\n%s", out)
	}
	if n := c.proofMissing(t); strings.Contains(n, "lint") || strings.Contains(n, "build") {
		t.Fatalf("after --final, lint and build are proven: %s", n)
	}
}

// C46
func TestR88_C46_FailedLintStillRunsInAFixRound(t *testing.T) {
	c := q83ChecksTask(t, false)
	s, _, err := core.LoadSetup(c.dir)
	if err != nil {
		t.Fatal(err)
	}
	s.Commands.Lint = "if [ -f LINTFAIL ]; then echo lint-failed; exit 1; fi; echo lint-ran"
	if _, err := core.SaveSetup(c.dir, s); err != nil {
		t.Fatal(err)
	}
	q83Git(t, c.dir, "add", "-A")
	q83Git(t, c.dir, "commit", "-q", "-m", "lint")
	tk, _ := core.LoadTask(c.dir, c.id)
	tk.StartCommit = core.HeadCommit(c.dir)
	tk.ApprovalPrint, _ = core.Fingerprint(c.dir, nil)
	if err := core.SaveTask(tk); err != nil {
		t.Fatal(err)
	}
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	q83Write(t, filepath.Join(c.dir, "LINTFAIL"), "x\n")
	if out, code := plumbline(t, c.dir, c.home, "", "run", "checks", "--task", c.id); code == 0 || !strings.Contains(out, "lint-failed") {
		t.Fatalf("fixture: lint fails: %d\n%s", code, out)
	}
	c.r88Reviewed(t, "# Code review 1\n## Findings\n- none\n")
	if err := os.Remove(filepath.Join(c.dir, "LINTFAIL")); err != nil {
		t.Fatal(err)
	}
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	out := c.checks(t)
	if !strings.Contains(out, "lint-ran") {
		t.Fatalf("a lint whose last run failed runs again in a fix round\n%s", out)
	}
	if strings.Contains(out, "build-ran") {
		t.Fatalf("build passed before, so it waits for --final\n%s", out)
	}
}

// C21
func TestR88_C21_PassingChecksNameSelfReviewNext(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	out := c.checks(t)
	if !strings.Contains(out, r88NextLine) {
		t.Fatalf("passing checks with no self-review end with the next step\n%s", out)
	}
}

// C22
func TestR88_C22_NoNextLineWhenFailedCurrentOrNotBuilding(t *testing.T) {
	t.Run("a check failed", func(t *testing.T) {
		c := q83ChecksTask(t, false)
		s, _, _ := core.LoadSetup(c.dir)
		s.Commands.Lint = "echo lint-failed; exit 1"
		if _, err := core.SaveSetup(c.dir, s); err != nil {
			t.Fatal(err)
		}
		c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
		out, code := plumbline(t, c.dir, c.home, "", "run", "checks", "--task", c.id)
		if code == 0 || strings.Contains(out, r88NextLine) {
			t.Fatalf("a failed check: no next-step line: %d\n%s", code, out)
		}
	})
	t.Run("self-review current", func(t *testing.T) {
		c := q83ChecksTask(t, false)
		c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
		text := "# Self-review\n## Checked\n- src/a.go: the checklist, links and case\n## Fixed\n- none\n"
		p := filepath.Join(core.TaskDocsDir(c.dir, c.id), "self-review.md")
		q83Write(t, p, text)
		sum := sha256.Sum256([]byte(text))
		fp, err := core.Fingerprint(c.dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		tk, _ := core.LoadTask(c.dir, c.id)
		tk.SelfReview = &core.SelfReview{Print: fp, Hash: hex.EncodeToString(sum[:]), At: core.Now()}
		if err := core.SaveTask(tk); err != nil {
			t.Fatal(err)
		}
		if out := c.checks(t); strings.Contains(out, r88NextLine) {
			t.Fatalf("a current self-review: no next-step line\n%s", out)
		}
	})
	t.Run("not building", func(t *testing.T) {
		c := q83ChecksTask(t, false)
		tk, _ := core.LoadTask(c.dir, c.id)
		tk.Phase = core.PhaseUnderstanding
		if err := core.SaveTask(tk); err != nil {
			t.Fatal(err)
		}
		q83Write(t, filepath.Join(c.dir, "src", "a.go"), "package src\n\nfunc A() int { return 2 }\n")
		out, _ := plumbline(t, c.dir, c.home, "", "run", "checks", "--task", c.id)
		if strings.Contains(out, r88NextLine) {
			t.Fatalf("outside building: no next-step line\n%s", out)
		}
	})
}

// C40
func TestR88_C40_StatusCountsStillOpenItems(t *testing.T) {
	c := q83ChecksTask(t, false)
	c.edit(t, "src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	c.r88Reviewed(t, "# Code review 2\n## Findings\n- [90] must-fix: still open from run 1: A should return 3 (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns 2.\n- [40] should-fix: still open from run 1: A has no doc comment (src/a.go:3 `func A() int`).\n- [80] must-fix: B is never called (src/a.go:1 `package src`). Fails when: x -> y.\n")
	out, _ := plumbline(t, c.dir, c.home, "", "status", "--task", c.id)
	found := false
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "still open") && strings.Contains(l, "code-reviewer") && strings.Contains(l, "2") {
			found = true
		}
	}
	if !found {
		t.Fatalf("status lists 2 still-open items for code-reviewer\n%s", out)
	}
}

// --- 0.8.8 blind QA: skill text from another Plumbline version ---

// r88ReadHook sends a PostToolUse for a Read of path to the given binary.
func r88ReadHook(t *testing.T, binary, dir, home, root, session, path string) string {
	t.Helper()
	in, err := json.Marshal(map[string]any{
		"session_id": session, "cwd": dir, "hook_event_name": "PostToolUse", "tool_name": "Read",
		"tool_input": map[string]any{"file_path": path}, "tool_response": map[string]any{"type": "text"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(binary, "hook", "post")
	c.Dir = dir
	c.Env = append(os.Environ(), "PLUMBLINE_HOME="+home, "CLAUDE_PLUGIN_ROOT="+root, "CLAUDE_CONFIG_DIR="+filepath.Join(home, "claude"))
	c.Stdin = strings.NewReader(string(in))
	out, err := c.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return string(out)
}

// C36, C37
func TestR88_C36_C37_OldSkillTextNote(t *testing.T) {
	tmp := t.TempDir()
	v888 := filepath.Join(tmp, "plumbline")
	if out, err := exec.Command("go", "build", "-ldflags", "-X main.version=0.8.8", "-o", v888, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	home, dir := t.TempDir(), gitRepo(t)
	cache := filepath.Join(home, "plugins", "cache", "plumbline", "plumbline")
	root := filepath.Join(cache, "0.8.8")
	old := filepath.Join(cache, "0.8.4", "skills", "task", "checkpoints.md")
	q83Write(t, old, "# Checkpoints\n")
	q83Write(t, filepath.Join(root, "skills", "task", "SKILL.md"), "# Task\n")

	out := r88ReadHook(t, v888, dir, home, root, "s-old", old)
	for _, want := range []string{"this is Plumbline 0.8.4's skill text, but the hooks are 0.8.8", filepath.Join(root, "skills", "task", "SKILL.md")} {
		if !strings.Contains(out, want) {
			t.Fatalf("C36: reading 0.8.4's skill text under a 0.8.8 binary should name the current skill (%q)\n%s", want, out)
		}
	}
	// C37: once per session.
	if out := r88ReadHook(t, v888, dir, home, root, "s-old", old); strings.Contains(out, "skill text") {
		t.Fatalf("C37: the note comes once a session\n%s", out)
	}
	// C37: the current version's skill text needs no note.
	cur := filepath.Join(root, "skills", "task", "SKILL.md")
	if out := r88ReadHook(t, v888, dir, home, root, "s-cur", cur); strings.Contains(out, "skill text") {
		t.Fatalf("C37: the current version's skill: no note\n%s", out)
	}
	// C37: a dev build (not a plain x.y.z) adds no note.
	if out := r88ReadHook(t, bin, dir, home, root, "s-dev", old); strings.Contains(out, "skill text") {
		t.Fatalf("C37: a dev build adds no note\n%s", out)
	}
}
