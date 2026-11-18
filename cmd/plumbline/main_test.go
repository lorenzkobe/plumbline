package main

import (
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
	for _, want := range []string{"plan.md", "Write tool", "Drop it", "plumbline run checks"} {
		if !strings.Contains(out, want) {
			t.Fatalf("small checklist lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "understanding.md") || strings.Contains(out, "Start the challenger") {
		t.Fatalf("small checklist asks for what small skips: %s", out)
	}
	out, _ = plumbline(t, dir, home, "", "task", "start", "--title", "y", "--size", "normal", "--why", "shared helper")
	if !strings.Contains(out, "plan-for-you.md") || !strings.Contains(out, "understanding.md") {
		t.Fatalf("normal checklist lacks the plain plan or the understanding: %s", out)
	}
}
