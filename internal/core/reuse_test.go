package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	cliOnce sync.Once
	cliPath string
	cliErr  error
)

func plumblineBinary(t *testing.T) string {
	t.Helper()
	cliOnce.Do(func() {
		dir, err := os.MkdirTemp("", "plumbline-core-bin-*")
		if err != nil {
			cliErr = err
			return
		}
		cliPath = filepath.Join(dir, "plumbline")
		out, err := exec.Command("go", "build", "-o", cliPath, "../../cmd/plumbline").CombinedOutput()
		if err != nil {
			cliErr = &buildError{string(out)}
		}
	})
	if cliErr != nil {
		t.Fatalf("building plumbline: %v", cliErr)
	}
	return cliPath
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }

func cliIn(t *testing.T, dir string, env []string, args ...string) (string, int) {
	t.Helper()
	c := exec.Command(plumblineBinary(t), args...)
	c.Dir = dir
	c.Env = append(os.Environ(), env...)
	out, err := c.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return string(out), code
}

type reuseProject struct {
	t       *testing.T
	repo    string
	counter string
}

func newReuseProject(t *testing.T) *reuseProject {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	clearServiceEnv(t)
	repo := filepath.Join(home, "repo")
	counter := filepath.Join(home, "counts")
	must(t, os.MkdirAll(counter, 0o755))
	must(t, os.MkdirAll(filepath.Join(repo, "src"), 0o755))
	run(t, repo, "git", "init", "-q", "-b", "main")
	run(t, repo, "git", "config", "user.email", "t@example.com")
	run(t, repo, "git", "config", "user.name", "T")
	write(t, filepath.Join(repo, "src", "a.go"), "package src\n\nfunc A() int { return 1 }\n")
	write(t, filepath.Join(repo, "tests", "t.py"), "def test_t():\n    pass\n")
	write(t, filepath.Join(repo, ".gitignore"), "coverage/\n")
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "init")
	repo, _ = filepath.EvalSymlinks(repo)
	p := &reuseProject{t: t, repo: repo, counter: counter}
	p.saveSetup(nil)
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "chore: setup")
	return p
}

func (p *reuseProject) bump(kind string) string {
	return "echo x >> " + filepath.Join(p.counter, kind)
}

func (p *reuseProject) baseSetup() *Setup {
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = p.bump("test") + "; echo '== 3 passed in 0.1s =='"
	s.Commands.TestRelated = p.bump("related") + "; echo {files}; echo '== 1 passed in 0.1s =='"
	s.Commands.Lint = p.bump("lint")
	s.Commands.Build = p.bump("build")
	s.Commands.E2E = p.bump("e2e") + "; echo '== 2 passed in 0.1s =='"
	s.Commands.Security = p.bump("security")
	s.IgnoreChanges = []string{"coverage/**"}
	return s
}

func (p *reuseProject) saveSetup(change func(*Setup)) *Setup {
	p.t.Helper()
	s := p.baseSetup()
	if change != nil {
		change(s)
	}
	_, err := SaveSetup(p.repo, s)
	must(p.t, err)
	return s
}

func (p *reuseProject) count(kind string) int {
	b, err := os.ReadFile(filepath.Join(p.counter, kind))
	if err != nil {
		return 0
	}
	return strings.Count(string(b), "\n")
}

func (p *reuseProject) newTask(root string, change func(*Task)) *Task {
	p.t.Helper()
	s, _, err := LoadSetup(root)
	must(p.t, err)
	fp, err := Fingerprint(root, s.IgnoreChanges)
	must(p.t, err)
	tk := &Task{ID: NewID(), Title: "reuse", Size: "small", Root: root, Branch: CurrentBranch(root), Phase: PhaseBuilding,
		Created: Now(), StartCommit: HeadCommit(root), PlanFiles: []string{"src/a.go"}, ApprovalPrint: fp,
		CaseTests: []string{"tests/t.py"}, CasesStored: true}
	if change != nil {
		change(tk)
	}
	must(p.t, os.MkdirAll(TaskDocsDir(root, tk.ID), 0o700))
	must(p.t, SaveTask(tk))
	return tk
}

func (p *reuseProject) close(tk *Task) {
	p.t.Helper()
	got, err := LoadTask(tk.Root, tk.ID)
	must(p.t, err)
	got.Phase = PhaseCancelled
	must(p.t, SaveTask(got))
}

func (p *reuseProject) runKind(root string, tk *Task, kind string) string {
	p.t.Helper()
	out, _ := cliIn(p.t, root, nil, "run", kind, "--task", tk.ID)
	return out
}

func (p *reuseProject) runOf(taskID, kind string) *Run {
	var found *Run
	for _, r := range LoadRuns(p.repo) {
		if r.TaskID == taskID && r.Kind == kind {
			found = r
		}
	}
	return found
}

func (p *reuseProject) rewrite(r *Run, change func(*Run)) {
	p.t.Helper()
	c := *r
	change(&c)
	must(p.t, appendRun(p.repo, &c))
}

func (p *reuseProject) firstTaskBaseline(kind string) (*Task, *Run) {
	p.t.Helper()
	t1 := p.newTask(p.repo, nil)
	out := p.runKind(p.repo, t1, kind)
	if p.count(kind) != 1 {
		p.t.Fatalf("the first task's %s run should run the command: %s", kind, out)
	}
	src := p.runOf(t1.ID, kind)
	if src == nil || !src.Complete() {
		p.t.Fatalf("the first task's %s run wasn't recorded: %+v\n%s", kind, src, out)
	}
	p.close(t1)
	return t1, src
}

func runJSON(r *Run) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func TestSecondTaskReusesPassingBaselinesOnUnchangedCode(t *testing.T) {
	p := newReuseProject(t)
	t1 := p.newTask(p.repo, nil)
	for _, k := range []string{"test", "lint", "build"} {
		p.runKind(p.repo, t1, k)
	}
	if p.count("test") != 1 || p.count("lint") != 1 || p.count("build") != 1 {
		t.Fatalf("the first task should run each check once: test %d lint %d build %d", p.count("test"), p.count("lint"), p.count("build"))
	}
	p.close(t1)

	t2 := p.newTask(p.repo, nil)
	for _, k := range []string{"test", "lint", "build"} {
		out := p.runKind(p.repo, t2, k)
		if p.count(k) != 1 {
			t.Fatalf("%s ran again on unchanged code: %s", k, out)
		}
		if !strings.Contains(out, "reused the passing run") || !strings.Contains(out, "nothing ran") {
			t.Fatalf("%s: the reuse should be printed: %s", k, out)
		}
		if !strings.Contains(out, k+" recorded: PASS") {
			t.Fatalf("%s: the reused run should be recorded as passing: %s", k, out)
		}
		src, got := p.runOf(t1.ID, k), p.runOf(t2.ID, k)
		if got == nil {
			t.Fatalf("%s: no run recorded for the second task", k)
		}
		if got.ID == src.ID || got.PID != 0 || !got.Baseline || !got.Complete() {
			t.Fatalf("%s: the reused record should be a new, complete baseline with no process: %+v (source %+v)", k, got, src)
		}
		if BaselineFor(LoadRuns(p.repo), t2.ID, k) == nil {
			t.Fatalf("%s: the reused run should be the second task's baseline", k)
		}
	}
}

func TestRunChecksReusesEachPassingCheck(t *testing.T) {
	p := newReuseProject(t)
	t1 := p.newTask(p.repo, nil)
	out, _ := cliIn(t, p.repo, nil, "run", "checks", "--task", t1.ID)
	if p.count("test") != 1 || p.count("lint") != 1 || p.count("build") != 1 {
		t.Fatalf("the first task's checks should run once each: %s", out)
	}
	p.close(t1)
	t2 := p.newTask(p.repo, nil)
	out, code := cliIn(t, p.repo, nil, "run", "checks", "--task", t2.ID)
	if p.count("test") != 1 || p.count("lint") != 1 || p.count("build") != 1 {
		t.Fatalf("run checks ran a check again on unchanged code: %s", out)
	}
	if !strings.Contains(out, "reused the passing run") {
		t.Fatalf("run checks should print the reuse: %s", out)
	}
	if code != 0 {
		t.Fatalf("reused passing checks should pass: %d %s", code, out)
	}
}

func TestNoReuseWhenAnythingDiffers(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(p *reuseProject, src *Run) (root string, change func(*Task))
	}{
		{"code differs by one byte", func(p *reuseProject, src *Run) (string, func(*Task)) {
			write(p.t, filepath.Join(p.repo, "src", "a.go"), "package src\n\nfunc A() int { return 2 }\n")
			return p.repo, nil
		}},
		{"command text differs", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.saveSetup(func(s *Setup) { s.Commands.Test = p.bump("test") + ";  echo '== 3 passed in 0.1s =='" })
			return p.repo, nil
		}},
		{"ignore list differs", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.saveSetup(func(s *Setup) { s.IgnoreChanges = []string{"coverage/**", "tmp/**"} })
			return p.repo, nil
		}},
		{"another checkout", func(p *reuseProject, src *Run) (string, func(*Task)) {
			wt := filepath.Join(p.t.TempDir(), "wt")
			run(p.t, p.repo, "git", "worktree", "add", "-q", "--detach", wt)
			wt, _ = filepath.EvalSymlinks(wt)
			return wt, nil
		}},
		{"older than 6 hours", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.rewrite(src, func(r *Run) {
				r.Started = Now().Add(-7 * time.Hour)
				r.Finished = r.Started.Add(time.Second)
			})
			return p.repo, nil
		}},
		{"stopped", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.rewrite(src, func(r *Run) { r.Stopped = true })
			return p.repo, nil
		}},
		{"timed out", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.rewrite(src, func(r *Run) { r.TimedOut, r.Exit = true, 124 })
			return p.repo, nil
		}},
		{"all tests failed", func(p *reuseProject, src *Run) (string, func(*Task)) {
			p.rewrite(src, func(r *Run) { r.Ran, r.Failed, r.Exit = 3, 3, 1 })
			return p.repo, nil
		}},
		{"hotfix task", func(p *reuseProject, src *Run) (string, func(*Task)) {
			return p.repo, func(tk *Task) { tk.Kind = KindHotfix }
		}},
		{"task already changed code", func(p *reuseProject, src *Run) (string, func(*Task)) {
			return p.repo, func(tk *Task) { tk.LastCodeChange = Now().Add(-time.Minute) }
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newReuseProject(t)
			_, src := p.firstTaskBaseline("test")
			root, change := c.prepare(p, src)
			t2 := p.newTask(root, change)
			out := p.runKind(root, t2, "test")
			if p.count("test") != 2 {
				t.Fatalf("the suite should run again: ran %d times\n%s", p.count("test"), out)
			}
			if strings.Contains(out, "reused the passing run") {
				t.Fatalf("a run was reused: %s", out)
			}
		})
	}
}

func TestReuseWindowIsSixHours(t *testing.T) {
	for _, c := range []struct {
		age    time.Duration
		reused bool
	}{
		{5*time.Hour + 59*time.Minute, true},
		{6*time.Hour + time.Minute, false},
	} {
		t.Run(c.age.String(), func(t *testing.T) {
			p := newReuseProject(t)
			_, src := p.firstTaskBaseline("test")
			p.rewrite(src, func(r *Run) {
				r.Finished = Now().Add(-c.age)
				r.Started = r.Finished.Add(-time.Second)
			})
			t2 := p.newTask(p.repo, nil)
			out := p.runKind(p.repo, t2, "test")
			ran := p.count("test")
			if c.reused && (ran != 1 || !strings.Contains(out, "reused the passing run")) {
				t.Fatalf("a run %s old should be reused: ran %d\n%s", c.age, ran, out)
			}
			if !c.reused && (ran != 2 || strings.Contains(out, "reused the passing run")) {
				t.Fatalf("a run %s old should not be reused: ran %d\n%s", c.age, ran, out)
			}
		})
	}
}

func TestReusingTwiceKeepsTheSourceRun(t *testing.T) {
	p := newReuseProject(t)
	_, src := p.firstTaskBaseline("test")
	before := runJSON(src)

	t2 := p.newTask(p.repo, nil)
	p.runKind(p.repo, t2, "test")
	p.close(t2)
	t3 := p.newTask(p.repo, nil)
	p.runKind(p.repo, t3, "test")
	if p.count("test") != 1 {
		t.Fatalf("both later tasks should reuse: ran %d", p.count("test"))
	}
	r2, r3 := p.runOf(t2.ID, KindTest), p.runOf(t3.ID, KindTest)
	if r2 == nil || r3 == nil {
		t.Fatalf("each reuse should leave its own record: %v %v", r2, r3)
	}
	if r2.ID == r3.ID || r2.ID == src.ID || r3.ID == src.ID {
		t.Fatalf("each record needs its own id: source %s, %s, %s", src.ID, r2.ID, r3.ID)
	}
	var after *Run
	for _, r := range LoadRuns(p.repo) {
		if r.ID == src.ID {
			after = r
		}
	}
	if after == nil || runJSON(after) != before {
		t.Fatalf("the source run changed:\nbefore %s\nafter  %s", before, runJSON(after))
	}
}

func TestRunsFromBeforeThisVersionAreNeverReused(t *testing.T) {
	p := newReuseProject(t)
	_, src := p.firstTaskBaseline("test")
	known := map[string]bool{"id": true, "task_id": true, "kind": true, "sub": true, "command": true, "started": true,
		"finished": true, "exit": true, "print_before": true, "print_after": true, "ran": true, "failed": true,
		"failed_ids": true, "skipped": true, "files": true, "seen": true, "problems": true, "baseline": true,
		"rebased": true, "timed_out": true, "stopped": true, "pid": true}
	var m map[string]any
	must(t, json.Unmarshal([]byte(runJSON(src)), &m))
	for k := range m {
		if !known[k] {
			delete(m, k)
		}
	}
	b, _ := json.Marshal(m)
	var old Run
	must(t, json.Unmarshal(b, &old))
	must(t, appendRun(p.repo, &old))

	t2 := p.newTask(p.repo, nil)
	out := p.runKind(p.repo, t2, "test")
	if p.count("test") != 2 || strings.Contains(out, "reused the passing run") {
		t.Fatalf("a run without the checkout and ignore fields was reused: ran %d\n%s", p.count("test"), out)
	}
}

func TestReusedRunIsNeverCaseTestProof(t *testing.T) {
	p := newReuseProject(t)
	_, src := p.firstTaskBaseline("test")
	p.rewrite(src, func(r *Run) {
		r.Seen = []string{"tests/t.py"}
		r.Files = []string{"tests/t.py"}
	})
	t2 := p.newTask(p.repo, nil)
	out := p.runKind(p.repo, t2, "test")
	if p.count("test") != 1 {
		t.Fatalf("the run should be reused: %s", out)
	}
	got := p.runOf(t2.ID, KindTest)
	if got == nil || len(got.Seen) != 0 || len(got.Files) != 0 {
		t.Fatalf("a reused run must name no case tests and no files: %+v", got)
	}
	s, _, _ := LoadSetup(p.repo)
	fp, err := Fingerprint(p.repo, s.IgnoreChanges)
	must(t, err)
	tk, err := LoadTask(p.repo, t2.ID)
	must(t, err)
	if m := strings.Join(caseRunMissing(tk, s, fp, LoadRuns(p.repo), ""), "; "); !strings.Contains(m, "tests/t.py") {
		t.Fatalf("a reused run proved a case test: %q", m)
	}
}

func TestE2EAndSecurityRunsAreNeverReused(t *testing.T) {
	p := newReuseProject(t)
	t1 := p.newTask(p.repo, nil)
	p.runKind(p.repo, t1, "e2e")
	p.runKind(p.repo, t1, "security")
	p.close(t1)
	t2 := p.newTask(p.repo, nil)
	e2e := p.runKind(p.repo, t2, "e2e")
	sec := p.runKind(p.repo, t2, "security")
	if p.count("e2e") != 2 || p.count("security") != 2 {
		t.Fatalf("e2e and security depend on more than the code and must run again: e2e %d security %d\n%s\n%s", p.count("e2e"), p.count("security"), e2e, sec)
	}
	if strings.Contains(e2e+sec, "reused the passing run") {
		t.Fatalf("e2e or security was reused: %s %s", e2e, sec)
	}
}

func TestUnfinishedOrFutureRunsAreNeverReused(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(*Run)
	}{
		{"unfinished", func(r *Run) { r.Finished = time.Time{}; r.PrintAfter = "" }},
		{"finished in the future", func(r *Run) {
			r.Finished = Now().Add(time.Hour)
			r.Started = r.Finished.Add(-time.Second)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newReuseProject(t)
			_, src := p.firstTaskBaseline("test")
			p.rewrite(src, c.change)
			t2 := p.newTask(p.repo, nil)
			out := p.runKind(p.repo, t2, "test")
			if p.count("test") != 2 || strings.Contains(out, "reused the passing run") {
				t.Fatalf("%s run was reused: ran %d\n%s", c.name, p.count("test"), out)
			}
		})
	}
}

func TestNoReuseAfterALocalEnvFileChanges(t *testing.T) {
	p := newReuseProject(t)
	write(t, filepath.Join(p.repo, ".env"), "DB=one\n")
	_, _ = p.firstTaskBaseline("test")
	write(t, filepath.Join(p.repo, ".env"), "DB=two\n")
	t2 := p.newTask(p.repo, nil)
	out := p.runKind(p.repo, t2, "test")
	if p.count("test") != 2 || strings.Contains(out, "reused the passing run") {
		t.Fatalf("a changed .env must stop reuse: ran %d\n%s", p.count("test"), out)
	}
}

func TestARunThatPassedOnlyAloneIsNeverReused(t *testing.T) {
	p := newReuseProject(t)
	_, src := p.firstTaskBaseline("test")
	p.rewrite(src, func(r *Run) { r.Rescued = true })
	t2 := p.newTask(p.repo, nil)
	out := p.runKind(p.repo, t2, "test")
	if p.count("test") != 2 || strings.Contains(out, "reused the passing run") {
		t.Fatalf("a run rescued by the one-at-a-time rerun must not be reused: ran %d\n%s", p.count("test"), out)
	}
}
