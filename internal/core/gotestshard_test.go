package core

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type shardModule struct {
	t     *testing.T
	repo  string
	pids  string
	locks string
}

func newShardModule(t *testing.T) *shardModule {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	clearServiceEnv(t)
	repo := filepath.Join(home, "repo")
	pids := filepath.Join(home, "pids")
	locks := filepath.Join(home, "locks")
	must(t, os.MkdirAll(pids, 0o755))
	must(t, os.MkdirAll(locks, 0o755))
	must(t, os.MkdirAll(repo, 0o755))
	write(t, filepath.Join(repo, "go.mod"), "module example.com/shard\n\ngo 1.21\n")
	return &shardModule{t: t, repo: repo, pids: pids, locks: locks}
}

func (m *shardModule) pkg(name string, n int, body func(i int) string, extra string) {
	m.t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"strconv\"\n\t\"testing\"\n\t\"time\"\n)\n\n", name)
	fmt.Fprintf(&b, "var _ = time.Second\n\nfunc recordPID(t *testing.T) {\n\t_ = os.WriteFile(filepath.Join(%q, strconv.Itoa(os.Getpid())), nil, 0o644)\n}\n\n", filepath.Join(m.pids, name))
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "func TestCase%03d(t *testing.T) {\n\trecordPID(t)\n%s}\n\n", i, body(i))
	}
	b.WriteString(extra)
	must(m.t, os.MkdirAll(filepath.Join(m.pids, name), 0o755))
	write(m.t, filepath.Join(m.repo, name, name+".go"), "package "+name+"\n\nvar Hook = func() string { return \"\" }\n")
	write(m.t, filepath.Join(m.repo, name, name+"_test.go"), b.String())
}

func (m *shardModule) commit(test string) {
	m.t.Helper()
	run(m.t, m.repo, "git", "init", "-q", "-b", "main")
	run(m.t, m.repo, "git", "config", "user.email", "t@example.com")
	run(m.t, m.repo, "git", "config", "user.name", "T")
	run(m.t, m.repo, "git", "add", "-A")
	run(m.t, m.repo, "git", "commit", "-q", "-m", "init")
	m.repo, _ = filepath.EvalSymlinks(m.repo)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = test
	_, err := SaveSetup(m.repo, s)
	must(m.t, err)
}

func (m *shardModule) processes(name string) int {
	es, _ := os.ReadDir(filepath.Join(m.pids, name))
	return len(es)
}

func (m *shardModule) clearPIDs(name string) {
	must(m.t, os.RemoveAll(filepath.Join(m.pids, name)))
	must(m.t, os.MkdirAll(filepath.Join(m.pids, name), 0o755))
}

func (m *shardModule) runTest() (string, *Run) {
	m.t.Helper()
	out, _ := cliIn(m.t, m.repo, nil, "run", "test")
	runs := LoadRuns(m.repo)
	if len(runs) == 0 {
		m.t.Fatalf("no run recorded:\n%s", out)
	}
	return out, runs[len(runs)-1]
}

func plainGoTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("go", append([]string{"test"}, args...)...)
	c.Dir = dir
	out, _ := c.CombinedOutput()
	return string(out)
}

func noBody(int) string { return "" }

var (
	reOKLine   = regexp.MustCompile(`(?m)^ok\s+\S+`)
	reFailLine = regexp.MustCompile(`(?m)^FAIL\s+\S+\s+[\d.]+s`)
)

func TestShardedGoRunCountsLikeAPlainRun(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, func(i int) string {
		if i == 17 || i == 63 {
			return "\tt.Fatal(\"boom\")\n"
		}
		return ""
	}, "")
	m.pkg("small", 5, noBody, "")
	m.commit("go test -count=1 ./...")

	plain := plainGoTest(t, m.repo, "-count=1", "./...")
	m.clearPIDs("big")
	sharded, code := cliIn(t, m.repo, nil, "gotest-shard", "--", "go", "test", "-count=1", "./...")
	if code == 0 {
		t.Fatalf("a sharded run with failures must fail:\n%s", sharded)
	}
	if m.processes("big") < 2 {
		t.Fatalf("a package of 90 tests should run in shards, got %d process(es):\n%s", m.processes("big"), sharded)
	}
	p, s := ParseTestOutput(plain, ""), ParseTestOutput(sharded, "")
	if p.Ran != s.Ran || p.Failed != s.Failed || strings.Join(p.IDs, ",") != strings.Join(s.IDs, ",") {
		t.Fatalf("counts differ: plain %+v, sharded %+v\nplain:\n%s\nsharded:\n%s", p, s, plain, sharded)
	}
	if strings.Join(s.IDs, ",") != "TestCase017,TestCase063" {
		t.Fatalf("failed names: %v", s.IDs)
	}
	if len(reOKLine.FindAllString(sharded, -1)) != 1 || len(reFailLine.FindAllString(sharded, -1)) != 1 {
		t.Fatalf("one ok or FAIL line per package, like go test:\n%s", sharded)
	}
}

func TestShardedVerboseRunCountsLikeAPlainRun(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "")
	m.commit("go test -count=1 -v ./...")
	plain := plainGoTest(t, m.repo, "-count=1", "-v", "./...")
	m.clearPIDs("big")
	sharded, code := cliIn(t, m.repo, nil, "gotest-shard", "--", "go", "test", "-count=1", "-v", "./...")
	if code != 0 {
		t.Fatalf("a passing sharded run failed: %d\n%s", code, sharded)
	}
	if m.processes("big") < 2 {
		t.Fatalf("not sharded: %d process(es)", m.processes("big"))
	}
	p, s := ParseTestOutput(plain, ""), ParseTestOutput(sharded, "")
	if p.Ran != 90 || p.Ran != s.Ran || s.Failed != 0 {
		t.Fatalf("counts differ: plain %+v, sharded %+v", p, s)
	}
}

func TestPlumblineRunTestShardsAGoSuite(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "")
	m.commit("go test -count=1 ./...")
	out, r := m.runTest()
	if m.processes("big") < 2 {
		t.Fatalf("plumbline run test should shard a plain go test: %d process(es)\n%s", m.processes("big"), out)
	}
	if r.Command != "go test -count=1 ./..." {
		t.Fatalf("the recorded command should stay the saved one: %q", r.Command)
	}
	if r.Exit != 0 || r.Failed != 0 || r.Ran <= 0 {
		t.Fatalf("a passing sharded suite should be recorded as passing: %+v\n%s", r, out)
	}
}

func lockBody(dir string) func(int) string {
	return func(int) string {
		return fmt.Sprintf("\tf, err := os.OpenFile(filepath.Join(%q, \"lock\"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)\n\tif err != nil {\n\t\tt.Fatal(\"busy: another process holds the lock\")\n\t}\n\tf.Close()\n\tdefer os.Remove(filepath.Join(%q, \"lock\"))\n\ttime.Sleep(40 * time.Millisecond)\n", dir, dir)
	}
}

func TestTestsThatFailOnlyInParallelPassAloneAndRunAloneNextTime(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, lockBody(m.locks), "")
	m.commit("go test -count=1 ./...")

	m.clearPIDs("big")
	out, r := m.runTest()
	if m.processes("big") < 2 {
		t.Fatalf("the first run should be sharded: %d process(es)\n%s", m.processes("big"), out)
	}
	if r.Exit != 0 || r.Failed != 0 {
		t.Fatalf("tests that pass alone should be recorded as passed: %+v\n%s", r, out)
	}
	if !strings.Contains(out, "parallel") || !strings.Contains(out, "TestCase") {
		t.Fatalf("the tests that fail only in parallel should be named: %s", out)
	}

	clean := false
	for i := 2; i <= 6 && !clean; i++ {
		m.clearPIDs("big")
		out, r = m.runTest()
		if r.Exit != 0 || r.Failed != 0 {
			t.Fatalf("run %d: should pass: %+v\n%s", i, r, out)
		}
		clean = !strings.Contains(out, "failed only when run in parallel")
	}
	if !clean {
		t.Fatalf("once remembered, the parallel-unsafe tests run on their own and stop colliding: %s", out)
	}
	if b, err := os.ReadFile(goShardStatePath(m.repo)); err != nil || strings.Contains(string(b), "off") {
		t.Fatalf("parallel runs never turn off for the project: %s %v", b, err)
	}
}

func TestATestThatFailsAloneTooIsRecordedAsFailed(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "func TestAlwaysFails(t *testing.T) {\n\tt.Fatal(\"always\")\n}\n")
	m.commit("go test -count=1 ./...")
	out, r := m.runTest()
	if m.processes("big") < 2 {
		t.Fatalf("not sharded: %d process(es)\n%s", m.processes("big"), out)
	}
	if r.Exit == 0 || r.Failed == 0 || !contains(r.FailedIDs, "TestAlwaysFails") {
		t.Fatalf("a test that fails alone too is a real failure: %+v\n%s", r, out)
	}
	if v := Judge(r, nil); v.OK {
		t.Fatalf("the failing run judged as passing: %+v", r)
	}
}

func TestShardedRunWithAVetErrorFailsLikeGoTest(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "func TestVet(t *testing.T) {\n\tt.Logf(\"%d\", \"not a number\")\n}\n")
	m.commit("go test -count=1 ./...")
	plain := plainGoTest(t, m.repo, "-count=1", "./...")
	if !strings.Contains(plain, "FAIL") {
		t.Fatalf("plain go test should fail on the vet error:\n%s", plain)
	}
	out, r := m.runTest()
	if r.Exit == 0 || Judge(r, nil).OK {
		t.Fatalf("a vet error must fail the sharded run like plain go test: %+v\n%s", r, out)
	}
	if !strings.Contains(out, "wrong type") && !strings.Contains(out, "vet") && !strings.Contains(out, "format %d") {
		t.Fatalf("the vet message should be in the output:\n%s", out)
	}
}

func TestUnchangedShardsComeFromGosCache(t *testing.T) {
	m := newShardModule(t)
	var b strings.Builder
	b.WriteString("package cached\n\nimport \"testing\"\n\n")
	for i := 0; i < 90; i++ {
		fmt.Fprintf(&b, "func TestPure%03d(t *testing.T) {\n\tif %d+1 != %d {\n\t\tt.Fatal()\n\t}\n}\n\n", i, i, i+1)
	}
	write(t, filepath.Join(m.repo, "cached", "cached.go"), "package cached\n")
	write(t, filepath.Join(m.repo, "cached", "cached_test.go"), b.String())
	m.commit("go test ./...")
	first, code := cliIn(t, m.repo, nil, "gotest-shard", "--", "go", "test", "./...")
	if code != 0 {
		t.Fatalf("first run failed: %s", first)
	}
	second, code := cliIn(t, m.repo, nil, "gotest-shard", "--", "go", "test", "./...")
	if code != 0 || !strings.Contains(second, "(cached)") {
		t.Fatalf("an unchanged sharded package should come back from go's test cache: %d\n%s", code, second)
	}
	if c := ParseTestOutput(second, ""); c.Ran <= 0 || c.Failed != 0 {
		t.Fatalf("a cached run should still count as a passing run: %+v\n%s", c, second)
	}
}

func TestCommandsThatCantBeShardedRunAsSaved(t *testing.T) {
	for _, c := range []struct {
		name, test string
	}{
		{"unknown flag", "go test -count=1 -failfast ./..."},
		{"pipe", "go test -count=1 ./... | cat"},
		{"and", "go test -count=1 ./... && true"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newShardModule(t)
			m.pkg("big", 90, noBody, "")
			m.commit(c.test)
			out, r := m.runTest()
			if m.processes("big") != 1 {
				t.Fatalf("%q should run as saved, in one process per package: %d\n%s", c.test, m.processes("big"), out)
			}
			if r.Command != c.test || r.Exit != 0 {
				t.Fatalf("the saved command should run and pass: %+v\n%s", r, out)
			}
		})
	}
}

func TestBuildErrorRunsAsSavedAndFails(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "func TestBroken(t *testing.T) {\n\tvar x int = \"no\"\n\t_ = x\n}\n")
	m.commit("go test -count=1 ./...")
	plain := plainGoTest(t, m.repo, "-count=1", "./...")
	out, r := m.runTest()
	if m.processes("big") != 0 {
		t.Fatalf("a package that doesn't build can't run any test: %d", m.processes("big"))
	}
	if r.Exit == 0 || Judge(r, nil).OK {
		t.Fatalf("a build error must fail: %+v\n%s", r, out)
	}
	if strings.Contains(plain, "[build failed]") && !strings.Contains(out, "[build failed]") {
		t.Fatalf("the build failure should show as go test shows it:\nplain:\n%s\nrun:\n%s", plain, out)
	}
}

func TestShardsThatShareEnvAndPackageFunctionsDontClash(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, func(i int) string {
		return fmt.Sprintf("\tname := %q\n\tt.Setenv(\"SHARED_VALUE\", name)\n\told := Hook\n\tHook = func() string { return name }\n\tdefer func() { Hook = old }()\n\ttime.Sleep(5 * time.Millisecond)\n\tif os.Getenv(\"SHARED_VALUE\") != name || Hook() != name {\n\t\tt.Fatalf(\"clash: env %%q hook %%q\", os.Getenv(\"SHARED_VALUE\"), Hook())\n\t}\n\t_ = strconv.Itoa\n", "case"+strconv.Itoa(i))
	}, "")
	m.commit("go test -count=1 ./...")
	out, r := m.runTest()
	if m.processes("big") < 2 {
		t.Fatalf("not sharded: %d process(es)\n%s", m.processes("big"), out)
	}
	if r.Exit != 0 || r.Failed != 0 {
		t.Fatalf("shards in separate processes must not see each other's env or package functions: %+v\n%s", r, out)
	}
}

func TestTimeLimitStopsAHungShard(t *testing.T) {
	m := newShardModule(t)
	pidFile := filepath.Join(m.pids, "hung.pid")
	m.pkg("big", 90, noBody, fmt.Sprintf("func TestHangs(t *testing.T) {\n\t_ = os.WriteFile(%q, []byte(strconv.Itoa(os.Getpid())), 0o644)\n\ttime.Sleep(10 * time.Minute)\n}\n", pidFile))
	m.commit("go test -count=1 ./...")
	bin := plumblineBinary(t)
	t.Setenv("PATH", filepath.Dir(bin)+":"+os.Getenv("PATH"))
	floor := RunLimitFloor
	RunLimitFloor = 20 * time.Second
	t.Cleanup(func() { RunLimitFloor = floor })
	s, _, _ := LoadSetup(m.repo)
	s.SuiteSeconds = 1
	var out bytes.Buffer
	r, err := Execute(m.repo, nil, KindTest, "", s.Commands.Test, s, &out)
	must(t, err)
	if !r.TimedOut {
		t.Fatalf("the hung run should stop at its time limit: %+v\n%s", r, out.String())
	}
	if v := Judge(r, nil); v.OK || !strings.Contains(v.Reason, "time limit") {
		t.Fatalf("a stopped run must not pass: %+v", v)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the hung shard (pid %d) is still running after the time limit", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestAShardThatCrashesIsNeverRescued(t *testing.T) {
	m := newShardModule(t)
	lock := lockBody(m.locks)
	m.pkg("big", 90, func(i int) string {
		if i == 50 {
			return fmt.Sprintf("\tf, err := os.OpenFile(filepath.Join(%q, \"lock\"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)\n\tif err != nil {\n\t\tpanic(\"busy\")\n\t}\n\tf.Close()\n\tdefer os.Remove(filepath.Join(%q, \"lock\"))\n\ttime.Sleep(40 * time.Millisecond)\n", m.locks, m.locks)
		}
		return lock(i)
	}, "")
	m.commit("go test -count=1 ./...")
	for attempt := 0; attempt < 3; attempt++ {
		m.clearPIDs("big")
		out, r := m.runTest()
		if !strings.Contains(out, "panic: busy") {
			continue
		}
		if r.Exit == 0 || Judge(r, nil).OK {
			t.Fatalf("a shard that panicked must fail the run, even when other shards pass alone: %+v\n%s", r, out)
		}
		return
	}
	t.Skip("the panicking test never collided in three runs")
}

func TestGoTestShardRunsOnlyPlainGoTest(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 5, noBody, "")
	m.commit("go test ./...")
	marker := filepath.Join(m.repo, "PWNED")
	out, code := cliIn(t, m.repo, nil, "gotest-shard", "--", "touch", marker)
	if code == 0 || !strings.Contains(out, "nothing ran") {
		t.Fatalf("gotest-shard must refuse anything but a plain go test: %d %s", code, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("gotest-shard ran a shell command")
	}
}

func TestClaudeCantCallGoTestShardDirectly(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	h.expect(h.bash("plumbline gotest-shard -- go test ./..."), "deny", "Run those instead")
}

func TestRaceReportsAreNeverRescued(t *testing.T) {
	for _, out := range []string{
		"==================\nWARNING: DATA RACE\nRead at 0x00c\n--- FAIL: TestX (0.01s)\n    testing.go:1465: race detected during execution of test\n",
		"--- FAIL: TestX (0.00s)\npanic: boom\n",
	} {
		if !unrescuable(out) {
			t.Fatalf("this output must never be rescued: %q", out)
		}
	}
	if unrescuable("--- FAIL: TestX (0.00s)\n    x_test.go:3: busy\n") {
		t.Fatal("a plain failure can be rescued")
	}
}

func TestTestNamesTheListCantReadAreNeverLeftOut(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "func TestÉcole(t *testing.T) {\n\tt.Fatal(\"this test must run\")\n}\n")
	m.commit("go test -count=1 ./...")
	out, r := m.runTest()
	if r.Exit == 0 || Judge(r, nil).OK {
		t.Fatalf("a test whose name the shard list can't read must still run and fail: %+v\n%s", r, out)
	}
}

func TestRememberedAloneTestsInTwoPackagesDontDeadlock(t *testing.T) {
	m := newShardModule(t)
	m.pkg("one", 90, noBody, "")
	m.pkg("two", 90, noBody, "")
	m.commit("go test -count=1 ./...")
	must(t, saveGoShardState(m.repo, goShardState{Alone: map[string][]string{
		"example.com/shard/one": {"TestCase001", "TestCase002"},
		"example.com/shard/two": {"TestCase003"},
	}}))
	done := make(chan struct{})
	var out string
	var r *Run
	go func() { out, r = m.runTest(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Minute):
		t.Fatal("two packages with remembered tests deadlocked")
	}
	if r.Exit != 0 || r.Failed != 0 || len(reOKLine.FindAllString(out, -1)) != 2 {
		t.Fatalf("both packages should pass with one ok line each: %+v\n%s", r, out)
	}
}

func TestARememberedTestThatFailsAloneIsAFailure(t *testing.T) {
	m := newShardModule(t)
	m.pkg("big", 90, noBody, "func TestAlwaysFails(t *testing.T) {\n\tt.Fatal(\"always\")\n}\n")
	m.commit("go test -count=1 ./...")
	must(t, saveGoShardState(m.repo, goShardState{Alone: map[string][]string{"example.com/shard/big": {"TestAlwaysFails"}}}))
	out, r := m.runTest()
	if r.Exit == 0 || Judge(r, nil).OK || !contains(r.FailedIDs, "TestAlwaysFails") {
		t.Fatalf("a remembered test that fails on its own is a real failure: %+v\n%s", r, out)
	}
}
