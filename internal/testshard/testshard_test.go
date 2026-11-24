package testshard

import (
	"bufio"
	"encoding/json"
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

const fixturePkg = "./testdata/fixture"

var fixtureTests = []string{"TestAlpha", "TestBeta", "TestGamma", "TestLongOnly", "TestDelta", "TestReadsFile"}

// goTest runs go test on the fixture package with env added to a clean copy
// of this process's environment.
func goTest(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("go", append(append([]string{"test"}, args...), fixturePkg)...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, EnvShards+"=") && !strings.HasPrefix(kv, envChild+"=") && !strings.HasPrefix(kv, "FIXTURE_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("go test: %v\n%s", err, out)
		}
		code = ee.ExitCode()
	}
	return string(out), code
}

var rePid = regexp.MustCompile(`(?m)^pid (\S+) (\d+)$`)

// pids maps each test that printed its pid to it.
func pids(out string) map[string]string {
	m := map[string]string{}
	for _, s := range rePid.FindAllStringSubmatch(out, -1) {
		m[s[1]] = s[2]
	}
	return m
}

func distinct(m map[string]string, names []string) int {
	seen := map[string]bool{}
	for _, n := range names {
		seen[m[n]] = true
	}
	return len(seen)
}

type event struct {
	Action, Test, Output string
}

func events(t *testing.T, out string) []event {
	t.Helper()
	var evs []event
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("not a json event: %q\n%s", sc.Text(), out)
		}
		evs = append(evs, e)
	}
	return evs
}

// actions counts each test's (or the package's, under "") result actions.
func actions(evs []event) map[string][]string {
	m := map[string][]string{}
	for _, e := range evs {
		switch e.Action {
		case "pass", "fail", "skip":
			m[e.Test] = append(m[e.Test], e.Action)
		}
	}
	return m
}

func TestEachTestRunsInItsOwnProcess(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=3"}, "-count=1", "-v")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	p := pids(out)
	for _, n := range fixtureTests {
		if p[n] == "" {
			t.Fatalf("%s did not run\n%s", n, out)
		}
	}
	if d := distinct(p, fixtureTests); d != len(fixtureTests) {
		t.Fatalf("%d processes for %d tests, want one each: %v", d, len(fixtureTests), p)
	}
	if p["TestGamma/sub"] != p["TestGamma"] {
		t.Fatalf("a subtest ran apart from its parent: %v", p)
	}
	if strings.Contains(out, "benchmark ran") {
		t.Fatalf("a benchmark ran without -bench\n%s", out)
	}
	if !regexp.MustCompile(`(?m)^ok\s+\S+fixture\s`).MatchString(out) || strings.Count(out, "\nPASS\n") != 1 {
		t.Fatalf("want one PASS and an ok line\n%s", out)
	}
	plain, code := goTest(t, []string{EnvShards + "=3"}, "-count=1")
	if code != 0 || !regexp.MustCompile(`(?m)^ok\s+\S+fixture\s`).MatchString(plain) {
		t.Fatalf("without -v: exit %d\n%s", code, plain)
	}
}

func TestJSONReportsEachTestOnce(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=3"}, "-count=1", "-json")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := actions(events(t, out))
	for _, n := range append(append([]string{}, fixtureTests...), "TestGamma/sub") {
		if len(got[n]) != 1 || got[n][0] != "pass" {
			t.Errorf("%s: %v, want one pass", n, got[n])
		}
	}
	if len(got[""]) != 1 || got[""][0] != "pass" {
		t.Errorf("package: %v, want one pass", got[""])
	}
}

func TestFailingTestFailsThePackage(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=3", "FIXTURE_MODE=fail-alpha"}, "-count=1", "-json")
	if code == 0 {
		t.Fatalf("exit 0 with a failing test\n%s", out)
	}
	got := actions(events(t, out))
	if len(got["TestAlpha"]) != 1 || got["TestAlpha"][0] != "fail" {
		t.Fatalf("TestAlpha: %v\n%s", got["TestAlpha"], out)
	}
	for _, n := range fixtureTests[1:] {
		if len(got[n]) != 1 || got[n][0] != "pass" {
			t.Errorf("%s: %v, want it to run and pass", n, got[n])
		}
	}
	if len(got[""]) != 1 || got[""][0] != "fail" {
		t.Errorf("package: %v, want one fail", got[""])
	}
	if !strings.Contains(out, "alpha failed on purpose") {
		t.Errorf("failure message lost\n%s", out)
	}
}

func TestCrashedTestFailsThePackageAndKeepsItsOutput(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=3", "FIXTURE_MODE=panic-beta"}, "-count=1", "-v")
	if code == 0 || !strings.Contains(out, "beta panicked on purpose") || !strings.Contains(out, "--- PASS: TestDelta") {
		t.Fatalf("panic: exit %d\n%s", code, out)
	}
	out, code = goTest(t, []string{EnvShards + "=3", "FIXTURE_MODE=exit-beta"}, "-count=1", "-v")
	if code == 0 || !regexp.MustCompile(`TestBeta.*exit status 3`).MatchString(out) || !strings.Contains(out, "--- PASS: TestDelta") {
		t.Fatalf("os.Exit(3): exit %d\n%s", code, out)
	}
}

func TestFlagsThatPickTestsRunInOneProcess(t *testing.T) {
	sharded := []string{EnvShards + "=3"}
	out, code := goTest(t, sharded, "-count=1", "-v", "-run", "TestAlpha|TestBeta")
	if p := pids(out); code != 0 || len(p) != 2 || distinct(p, []string{"TestAlpha", "TestBeta"}) != 1 {
		t.Fatalf("-run: exit %d, pids %v\n%s", code, p, out)
	}
	out, code = goTest(t, sharded, "-list", ".")
	if code != 0 || len(pids(out)) != 0 || !strings.Contains(out, "TestDelta\n") || !strings.Contains(out, "BenchmarkNever\n") {
		t.Fatalf("-list: exit %d\n%s", code, out)
	}
	out, code = goTest(t, sharded, "-count=1", "-run", "^$", "-bench", "Never", "-benchtime", "1x")
	if code != 0 || !strings.Contains(out, "benchmark ran") {
		t.Fatalf("-bench: exit %d\n%s", code, out)
	}
	out, code = goTest(t, sharded, "-count=1", "-v", "-cover")
	if p := pids(out); code != 0 || len(p) < len(fixtureTests) || distinct(p, fixtureTests) != 1 || !strings.Contains(out, "coverage:") {
		t.Fatalf("-cover: exit %d, pids %v\n%s", code, p, out)
	}
	out, code = goTest(t, []string{EnvShards + "=1"}, "-count=1", "-v")
	if p := pids(out); code != 0 || len(p) < len(fixtureTests) || distinct(p, fixtureTests) != 1 {
		t.Fatalf("%s=1: exit %d, pids %v\n%s", EnvShards, code, p, out)
	}
}

func TestTimeoutAppliesToEachTest(t *testing.T) {
	start := time.Now()
	out, code := goTest(t, []string{EnvShards + "=3", "FIXTURE_MODE=hang-gamma"}, "-count=1", "-json", "-timeout", "5s")
	if code == 0 || !strings.Contains(out, "test timed out") {
		t.Fatalf("hung test: exit %d\n%s", code, out)
	}
	if took := time.Since(start); took > 45*time.Second {
		t.Fatalf("took %s, the hung test's process wasn't stopped by -timeout", took)
	}
	got := actions(events(t, out))
	for _, n := range []string{"TestAlpha", "TestDelta", "TestReadsFile"} {
		if len(got[n]) != 1 || got[n][0] != "pass" {
			t.Errorf("%s: %v, want pass", n, got[n])
		}
	}
	if len(got[""]) != 1 || got[""][0] != "fail" {
		t.Errorf("package: %v, want one fail", got[""])
	}
}

// The parent has no m.Run of its own, so it keeps the whole run to
// -timeout itself and stops the tests still running.
func TestTimeoutCapsTheWholeRun(t *testing.T) {
	start := time.Now()
	out, code := goTest(t, []string{EnvShards + "=2", "FIXTURE_MODE=slow-others", "FIXTURE_SLEEP=2500ms"}, "-count=1", "-v", "-timeout", "4s")
	if code == 0 || !strings.Contains(out, "test timed out after 4s") || !regexp.MustCompile(`running tests:\n\s+Test`).MatchString(out) {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("took %s", took)
	}
	time.Sleep(200 * time.Millisecond)
	for name, p := range pids(out) {
		pid, _ := strconv.Atoi(p)
		if syscall.Kill(pid, 0) == nil {
			t.Errorf("%s's process %d still runs after go test ended", name, pid)
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// With every test already handed out when the time is up, the workers end
// as soon as their children are killed; the run still must not pass.
func TestTimeoutWithNothingQueuedNeverPasses(t *testing.T) {
	for i := range 6 {
		out, code := goTest(t, []string{EnvShards + "=8", "FIXTURE_MODE=slow-others", "FIXTURE_SLEEP=5s"}, "-count=1", "-v", "-timeout", "1s")
		if code == 0 || strings.Contains(out, "\nPASS\n") || !strings.Contains(out, "test timed out after 1s") {
			t.Fatalf("run %d: exit %d\n%s", i, code, out)
		}
	}
}

func TestFailfastStopsHandingOutTests(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=2", "FIXTURE_MODE=fail-alpha,slow-others"}, "-count=1", "-v", "-failfast")
	p := pids(out)
	if code == 0 || p["TestAlpha"] == "" {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, n := range []string{"TestDelta", "TestReadsFile"} {
		if p[n] != "" {
			t.Errorf("%s ran after the first failure with -failfast\n%s", n, out)
		}
	}
}

func TestShortAndSkipReachEachTest(t *testing.T) {
	out, code := goTest(t, []string{EnvShards + "=3"}, "-count=1", "-json", "-short", "-skip", "TestDelta")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := actions(events(t, out))
	if len(got["TestLongOnly"]) != 1 || got["TestLongOnly"][0] != "skip" {
		t.Errorf("-short: TestLongOnly %v, want skip", got["TestLongOnly"])
	}
	if len(got["TestDelta"]) != 0 {
		t.Errorf("-skip: TestDelta %v, want not run", got["TestDelta"])
	}
	if len(got["TestAlpha"]) != 1 || got["TestAlpha"][0] != "pass" {
		t.Errorf("TestAlpha %v", got["TestAlpha"])
	}
}

// The go command caches a passing package keyed on the files and env vars
// its tests read, which it learns from -test.testlogfile. A changed file a
// test reads must run the tests again. go rechecks only files inside the
// module and won't cache a file changed in the last 2s, so the input sits in
// the fixture's folder with its time set back.
func TestCacheSeesWhatEveryTestRead(t *testing.T) {
	f, err := os.CreateTemp(fixturePkg, "input-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	read, _ := filepath.Abs(f.Name())
	t.Cleanup(func() { os.Remove(read) })
	put := func(s string, age time.Duration) {
		t.Helper()
		if err := os.WriteFile(read, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age)
		if err := os.Chtimes(read, at, at); err != nil {
			t.Fatal(err)
		}
	}
	put("one", 2*time.Hour)
	env := []string{EnvShards + "=3", "FIXTURE_READ=" + read}
	if out, code := goTest(t, env); code != 0 || strings.Contains(out, "(cached)") {
		t.Fatalf("first run: exit %d\n%s", code, out)
	}
	if out, code := goTest(t, env); code != 0 || !strings.Contains(out, "(cached)") {
		t.Fatalf("second run should come from the cache: exit %d\n%s", code, out)
	}
	put("two, longer", time.Hour)
	if out, code := goTest(t, env); code != 0 || strings.Contains(out, "(cached)") {
		t.Fatalf("a file a test read changed, but the cached pass was used: exit %d\n%s", code, out)
	}
}

func TestStripResult(t *testing.T) {
	for _, c := range []struct{ in, body string }{
		{"=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\n", "=== RUN   TestA\n--- PASS: TestA (0.00s)\n"},
		{"--- FAIL: TestA (0.00s)\nFAIL\n", "--- FAIL: TestA (0.00s)\n"},
		{"\x16=== RUN   TestA\n\x16--- PASS: TestA (0.00s)\n\x16PASS\n", "\x16=== RUN   TestA\n\x16--- PASS: TestA (0.00s)\n"},
		{"PASS\n", ""},
		{"panic: boom\ngoroutine 1 [running]:\n", "panic: boom\ngoroutine 1 [running]:\n"},
		{"    t.go:3: PASS\n", "    t.go:3: PASS\n"},
		{"", ""},
	} {
		if body := stripResult([]byte(c.in)); string(body) != c.body {
			t.Errorf("stripResult(%q) = %q, want %q", c.in, body, c.body)
		}
	}
}

func TestParseListRefusesWhatItCantRead(t *testing.T) {
	names, err := parseList("TestA\nExampleB\nFuzzC\nBenchmarkD\n")
	if err != nil || strings.Join(names, ",") != "TestA,ExampleB,FuzzC" {
		t.Fatalf("names %v, err %v", names, err)
	}
	for _, bad := range []string{"", "\n", "BenchmarkOnly\n", "TestA\nsome warning\n", "ok  \tpkg\t0.1s\n"} {
		if names, err := parseList(bad); err == nil {
			t.Errorf("parseList(%q) = %v, want an error", bad, names)
		}
	}
}

func TestMergeLogs(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(a, []byte("# test log\ngetenv X\nopen /a\n"), 0o644))
	must(os.WriteFile(b, []byte("# test log\nopen /b\n"), 0o644))
	dst := filepath.Join(dir, "merged")
	must(mergeLogs(dst, []string{a, b}))
	got, err := os.ReadFile(dst)
	must(err)
	wd, _ := os.Getwd()
	if want := "# test log\nchdir " + wd + "\ngetenv X\nopen /a\nchdir " + wd + "\nopen /b\n"; string(got) != want {
		t.Fatalf("merged %q", got)
	}
	for name, logs := range map[string][]string{
		"missing":  {a, filepath.Join(dir, "none")},
		"no magic": {a, writeTemp(t, dir, "open /c\n")},
		"cut off":  {a, writeTemp(t, dir, "# test log\nopen /c")},
	} {
		dst := filepath.Join(dir, "out-"+strings.ReplaceAll(name, " ", "-"))
		if err := mergeLogs(dst, logs); err == nil {
			t.Errorf("%s: merged without an error", name)
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			t.Errorf("%s: wrote %s, so go could cache a pass it can't key", name, dst)
		}
	}
}

func writeTemp(t *testing.T, dir, s string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "log-*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
