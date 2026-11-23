package core

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func shortLimits(t *testing.T, d time.Duration) {
	t.Helper()
	floor, def := RunLimitFloor, RunLimitDefault
	RunLimitFloor, RunLimitDefault = d, d
	t.Cleanup(func() { RunLimitFloor, RunLimitDefault = floor, def })
}

func within(t *testing.T, d time.Duration, f func() (*Run, error)) *Run {
	t.Helper()
	type res struct {
		r   *Run
		err error
	}
	ch := make(chan res, 1)
	go func() {
		r, err := f()
		ch <- res{r, err}
	}()
	select {
	case got := <-ch:
		must(t, got.err)
		return got.r
	case <-time.After(d):
		t.Fatalf("the run didn't return within %s", d)
	}
	return nil
}

func procGone(pid int) bool {
	for i := 0; i < 40; i++ {
		if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func childPID(t *testing.T, p string) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			n, err := strconv.Atoi(strings.TrimSpace(string(b)))
			must(t, err)
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the command never wrote its child's pid")
	return 0
}

func TestHungRunIsStoppedAtItsLimit(t *testing.T) {
	h := newHarness(t)
	shortLimits(t, time.Second)
	var out bytes.Buffer
	r := within(t, 15*time.Second, func() (*Run, error) {
		return Execute(h.repo, nil, KindTest, "", "echo starting; sleep 999", &Setup{}, &out)
	})
	if !r.TimedOut || r.Exit != 124 {
		t.Fatalf("a hung run is recorded as timed out with exit 124: %+v", r)
	}
	if v := Judge(r, nil); v.OK || !strings.Contains(v.Reason, "time limit") {
		t.Fatalf("a timed-out run is a failure that says why: %+v", v)
	}
	if s := out.String(); !strings.Contains(s, "likely hangs") || !strings.Contains(s, "starting") {
		t.Fatalf("the output says it likely hangs and shows the last lines: %q", s)
	}
	runs := LoadRuns(h.repo)
	if last := runs[len(runs)-1]; !last.TimedOut || last.Finished.IsZero() {
		t.Fatalf("the stored record is finished and timed out: %+v", last)
	}
}

func TestHungChildIsKilledWithTheRun(t *testing.T) {
	h := newHarness(t)
	shortLimits(t, time.Second)
	pidFile := filepath.Join(t.TempDir(), "pid")
	within(t, 15*time.Second, func() (*Run, error) {
		return Execute(h.repo, nil, KindTest, "", "sleep 999 & echo $! > "+pidFile+"; wait", &Setup{}, io.Discard)
	})
	if pid := childPID(t, pidFile); !procGone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the hung child %d was left running", pid)
	}
}

func TestStoppingPlumblineStopsTheRun(t *testing.T) {
	h := newHarness(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	done := make(chan *Run, 1)
	go func() {
		r, _ := Execute(h.repo, nil, KindTest, "", "sleep 999 & echo $! > "+pidFile+"; wait", &Setup{}, io.Discard)
		done <- r
	}()
	pid := childPID(t, pidFile)
	must(t, syscall.Kill(os.Getpid(), syscall.SIGTERM))
	select {
	case r := <-done:
		if r == nil || r.TimedOut || r.Exit == 0 {
			t.Fatalf("a stopped run is a failure, not a time-out: %+v", r)
		}
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("SIGTERM didn't end the run")
	}
	if !procGone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the child %d outlived a stopped run", pid)
	}
}

func TestRunInTimeIsUnchanged(t *testing.T) {
	h := newHarness(t)
	var out bytes.Buffer
	r, err := Execute(h.repo, nil, KindTest, "", "echo 'ok  \texample.com/r/src\t0.1s'", &Setup{SuiteSeconds: 60}, &out)
	must(t, err)
	if r.TimedOut || r.Exit != 0 || r.Ran != 1 || r.Failed != 0 || !Judge(r, nil).OK {
		t.Fatalf("a run that ends in time records as before: %+v", r)
	}
	if strings.Contains(out.String(), "likely hangs") {
		t.Fatalf("no hang message on a normal run: %q", out.String())
	}
	r, err = Execute(h.repo, nil, KindLint, "", "exit 3", nil, io.Discard)
	must(t, err)
	if r.TimedOut || r.Exit != 3 || Judge(r, nil).OK {
		t.Fatalf("a failing check stays a plain failure: %+v", r)
	}
}

func TestRunLimits(t *testing.T) {
	s := func(sec int) *Setup { return &Setup{SuiteSeconds: sec} }
	for _, c := range []struct {
		kind  string
		setup *Setup
		want  time.Duration
	}{
		{KindTest, nil, time.Hour},
		{KindTest, s(0), time.Hour},
		{KindTest, s(60), 10 * time.Minute},
		{KindTest, s(200), 10 * time.Minute},
		{KindTest, s(201), 603 * time.Second},
		{KindRelated, s(550), 1650 * time.Second},
		{KindE2E, s(60), time.Hour},
		{KindProof, s(550), time.Hour},
		{KindLint, s(550), time.Hour},
	} {
		if got := RunLimit(c.kind, c.setup); got != c.want {
			t.Errorf("RunLimit(%s, %+v) = %s, want %s", c.kind, c.setup, got, c.want)
		}
	}
}

func TestTimedOutRunIsntSeenFailingFirst(t *testing.T) {
	h := bugTask(t, "src/a_test.go")
	h.writeTest()
	fake := t.TempDir()
	write(t, filepath.Join(fake, "grep"), "#!/bin/sh\necho '--- FAIL: TestA (0.00s)'\necho FAIL\necho 'FAIL\texample.com/r/src\t0.1s'\nexec sleep 999\n")
	must(t, os.Chmod(filepath.Join(fake, "grep"), 0o755))
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	shortLimits(t, 3*time.Second)
	s, _, _ := LoadSetup(h.repo)
	r := within(t, 20*time.Second, func() (*Run, error) {
		return ExecuteRelated(h.repo, h.task(), "./test.sh src/a_test.go", []string{"src/a_test.go"}, s, io.Discard)
	})
	if !r.TimedOut || r.Failed == 0 {
		t.Fatalf("the hung run printed a failure, then timed out: %+v", r)
	}
	if m := bugMissing(h); !strings.Contains(m, "no recorded run shows it failing") {
		t.Fatalf("a timed-out run must not count as the test seen failing: %q", m)
	}
}

func TestPassingRunWithALeftoverChild(t *testing.T) {
	h := newHarness(t)
	pidFile := filepath.Join(t.TempDir(), "pid")
	r := within(t, 30*time.Second, func() (*Run, error) {
		return Execute(h.repo, nil, KindLint, "", "sleep 999 & echo $! > "+pidFile+"; echo done", nil, io.Discard)
	})
	if r.Exit != 0 || r.TimedOut || !Judge(r, nil).OK {
		t.Fatalf("the command itself passed, so the run passes: %+v", r)
	}
	if pid := childPID(t, pidFile); !procGone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("the leftover child %d holding the output is stopped", pid)
	}
}

func TestStoppedRunIsntProof(t *testing.T) {
	h := bugTask(t, "src/a_test.go")
	h.writeTest()
	fake := t.TempDir()
	pidFile := filepath.Join(fake, "pid")
	write(t, filepath.Join(fake, "grep"), "#!/bin/sh\necho '--- FAIL: TestA (0.00s)'\necho FAIL\necho 'FAIL\texample.com/r/src\t0.1s'\nsleep 999 &\necho $! > "+pidFile+"\nwait\n")
	must(t, os.Chmod(filepath.Join(fake, "grep"), 0o755))
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, _, _ := LoadSetup(h.repo)
	done := make(chan *Run, 1)
	go func() {
		r, _ := ExecuteRelated(h.repo, h.task(), "./test.sh src/a_test.go", []string{"src/a_test.go"}, s, io.Discard)
		done <- r
	}()
	pid := childPID(t, pidFile)
	must(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	var r *Run
	select {
	case r = <-done:
	case <-time.After(15 * time.Second):
		_ = syscall.Kill(pid, syscall.SIGKILL)
		t.Fatal("Ctrl-C didn't end the run")
	}
	if !r.Stopped || r.TimedOut || r.Failed == 0 {
		t.Fatalf("the run printed a failure, then was stopped: %+v", r)
	}
	if v := Judge(r, nil); v.OK || !strings.Contains(v.Reason, "stopped before it finished") {
		t.Fatalf("a stopped run is no pass and says why: %+v", v)
	}
	if m := bugMissing(h); !strings.Contains(m, "no recorded run shows it failing") {
		t.Fatalf("a stopped run must not count as the test seen failing: %q", m)
	}
}

func TestInterruptedRunsAreNoProof(t *testing.T) {
	for _, timedOut := range []bool{true, false} {
		h := newHarness(t)
		r := &Run{ID: newID() + "-test", TaskID: "t1", Kind: KindTest, Command: "x", Started: Now(), Finished: Now(),
			PrintBefore: "p", PrintAfter: "p", Exit: 0, Ran: 5, Baseline: true, TimedOut: timedOut, Stopped: !timedOut}
		must(t, appendRun(h.repo, r))
		runs := LoadRuns(h.repo)
		if Judge(runs[0], nil).OK || BaselineFor(runs, "t1", KindTest) != nil || brokenBaseline(runs, "t1", KindTest) {
			t.Fatalf("an interrupted run is never a pass or a starting point, and doesn't make one look broken: %+v", runs[0])
		}
		if got := len(baselineRuns(runs, "t1", KindTest)); timedOut && got != 1 || !timedOut && got != 0 {
			t.Fatalf("a timed-out run records how the checks stood (they hang), so the first edit isn't blocked; a stopped one doesn't: timed out %v, %d", timedOut, got)
		}
	}
}
