package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
	tk := &core.Task{ID: core.NewID(), Title: "fix A", Size: "small", Root: repo, Branch: core.CurrentBranch(repo), Phase: core.PhaseUnderstanding, Created: core.Now()}
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
	if err != nil || got.Size != "normal" || got.SizeWhy != "touches auth" {
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
