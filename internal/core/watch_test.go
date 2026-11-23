package core

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixedNow(t *testing.T) func(time.Duration) {
	t.Helper()
	real := Now
	base := real()
	Now = func() time.Time { return base }
	t.Cleanup(func() { Now = real })
	return func(d time.Duration) {
		base = base.Add(d)
	}
}

func liveRun(t *testing.T, root, kind string, started, output time.Time) *Run {
	t.Helper()
	r := &Run{ID: newID() + "-" + kind, Kind: kind, Command: "go test ./...", Started: started, PID: os.Getpid(), Ran: -1}
	must(t, appendRun(root, r))
	p := liveOutputPath(root, r.ID)
	write(t, p, "")
	must(t, os.Chtimes(p, output, output))
	return r
}

func endRun(t *testing.T, root string, r *Run) {
	t.Helper()
	r.Finished, r.Exit, r.Ran = Now(), 0, 12
	must(t, appendRun(root, r))
	_ = os.Remove(liveOutputPath(root, r.ID))
}

func joined(lines []string) string { return strings.Join(lines, "\n") }

func TestWatchQuietWhileAllIsWell(t *testing.T) {
	h := buildingTask(t)
	fixedNow(t)
	r := liveRun(t, h.repo, KindTest, Now().Add(-time.Minute), Now())
	id, _ := h.startHelper(HelperBuilder)
	w := NewWatch(h.repo, h.task().ID)
	if lines, busy := w.Check(); len(lines) != 0 || !busy {
		t.Fatalf("nothing to say while a run prints and a helper works: %q busy %v", lines, busy)
	}
	endRun(t, h.repo, r)
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- nothing\n"))
	h.stopHelper(id, HelperBuilder)
	lines, busy := w.Check()
	s := joined(lines)
	if busy || !strings.Contains(s, "test finished") || !strings.Contains(s, "builder 1 finished") {
		t.Fatalf("one line for each end, then done: %q busy %v", s, busy)
	}
}

func TestWatchFlagsAQuietRun(t *testing.T) {
	h := newHarness(t)
	step := fixedNow(t)
	liveRun(t, h.repo, KindTest, Now().Add(-6*time.Minute), Now().Add(-5*time.Minute))
	w := NewWatch(h.repo, "")
	lines, busy := w.Check()
	if !busy || len(lines) != 1 || !strings.Contains(lines[0], "no output for 5 min") || !strings.Contains(lines[0], "may be hung") {
		t.Fatalf("a run quiet for 5 minutes is flagged once: %q", lines)
	}
	if lines, _ := w.Check(); len(lines) != 0 {
		t.Fatalf("not again right away: %q", lines)
	}
	step(5 * time.Minute)
	if lines, _ := w.Check(); len(lines) != 1 || !strings.Contains(lines[0], "no output for 10 min") {
		t.Fatalf("again after 5 more minutes: %q", lines)
	}
}

func TestWatchFlagsALongRun(t *testing.T) {
	h := newHarness(t)
	h.setup()
	fixedNow(t)
	s, _, _ := LoadSetup(h.repo)
	s.SuiteSeconds = 600
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	must(t, ConfirmSetup(h.repo))
	liveRun(t, h.repo, KindTest, Now().Add(-14*time.Minute), Now())
	w := NewWatch(h.repo, "")
	if lines, _ := w.Check(); len(lines) != 0 {
		t.Fatalf("1.4x the usual time is fine: %q", lines)
	}
	liveRun(t, h.repo, KindRelated, Now().Add(-15*time.Minute), Now())
	lines, _ := w.Check()
	if len(lines) != 1 || !strings.Contains(lines[0], "taking longer than usual: 15 min, usually 10 min") {
		t.Fatalf("1.5x the usual time is flagged: %q", lines)
	}

	s.SuiteSeconds = 0
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	must(t, ConfirmSetup(h.repo))
	liveRun(t, h.repo, KindTest, Now().Add(-4*time.Hour), Now())
	if lines, _ := NewWatch(h.repo, "").Check(); strings.Contains(joined(lines), "longer than usual") {
		t.Fatalf("no usual time saved, no such line: %q", lines)
	}
}

func TestWatchFlagsQuietAndStoppedHelpers(t *testing.T) {
	h := buildingTask(t)
	step := fixedNow(t)
	id, _ := h.startHelper(HelperBuilder)
	tk := h.task()
	r := tk.runByAgent(id)
	r.Started, r.LastSeen = Now().Add(-time.Hour), Now().Add(-10*time.Minute)
	must(t, SaveTask(tk))
	w := NewWatch(h.repo, tk.ID)
	lines, busy := w.Check()
	if !busy || len(lines) != 1 || !strings.Contains(lines[0], "builder 1 has been quiet for 10 min") {
		t.Fatalf("a helper quiet for 10 minutes is flagged: %q", lines)
	}
	step(time.Minute)
	if lines, _ := w.Check(); len(lines) != 0 {
		t.Fatalf("not again right away: %q", lines)
	}
	tk = h.task()
	r = tk.runByAgent(id)
	r.Finished, r.Status, r.Error = Now(), BuilderStoppedByPlumbline, "that command changed files outside the approved plan"
	must(t, SaveTask(tk))
	lines, busy = w.Check()
	if busy || len(lines) != 1 || !strings.Contains(lines[0], "builder 1 was stopped by Plumbline, so it is finished") || !strings.Contains(lines[0], "outside the approved plan") {
		t.Fatalf("a stopped helper gets one line with why: %q busy %v", lines, busy)
	}
}

func TestTwoWatchersSeeTheSame(t *testing.T) {
	h := buildingTask(t)
	fixedNow(t)
	liveRun(t, h.repo, KindTest, Now().Add(-6*time.Minute), Now().Add(-5*time.Minute))
	id := h.task().ID
	state := read(t, TaskDir(h.repo, id)+"/state.json")
	a, b := NewWatch(h.repo, id), NewWatch(h.repo, id)
	la, _ := a.Check()
	lb, _ := b.Check()
	if joined(la) != joined(lb) || len(la) != 1 {
		t.Fatalf("two watchers print the same: %q vs %q", la, lb)
	}
	if read(t, TaskDir(h.repo, id)+"/state.json") != state {
		t.Fatal("watching changed the task")
	}
}

func TestWatchWithNothingRunning(t *testing.T) {
	h := newHarness(t)
	step := fixedNow(t)
	w := NewWatch(h.repo, "")
	if lines, busy := w.Check(); len(lines) != 0 || !busy {
		t.Fatalf("a run started in the same breath may not be recorded yet, so wait: %q busy %v", lines, busy)
	}
	step(WatchGrace)
	lines, busy := w.Check()
	if busy || len(lines) != 1 || !strings.Contains(lines[0], "nothing is running") {
		t.Fatalf("nothing to watch after the grace time: %q busy %v", lines, busy)
	}
	r := &Run{ID: newID() + "-test", Kind: KindTest, Command: "x", Started: Now().Add(-time.Hour), PID: 0, Ran: -1}
	must(t, appendRun(h.repo, r))
	w = NewWatch(h.repo, "")
	step(WatchGrace)
	if lines, busy := w.Check(); busy || !strings.Contains(joined(lines), "nothing is running") {
		t.Fatalf("an old unfinished record with no process isn't running: %q", lines)
	}
}

func TestWatchStartedBeforeTheRun(t *testing.T) {
	h := newHarness(t)
	step := fixedNow(t)
	w := NewWatch(h.repo, "")
	w.Check()
	step(10 * time.Second)
	r := liveRun(t, h.repo, KindTest, Now(), Now())
	if lines, busy := w.Check(); len(lines) != 0 || !busy {
		t.Fatalf("the run that started after the watcher is watched: %q busy %v", lines, busy)
	}
	step(WatchGrace)
	endRun(t, h.repo, r)
	if lines, busy := w.Check(); busy || !strings.Contains(joined(lines), "test finished") {
		t.Fatalf("its end is reported: %q busy %v", lines, busy)
	}
}

func TestWatchIgnoresAStaleRecord(t *testing.T) {
	h := newHarness(t)
	h.setup()
	step := fixedNow(t)
	liveRun(t, h.repo, KindTest, Now().Add(-RunLimitDefault-2*time.Minute), Now())
	w := NewWatch(h.repo, "")
	step(WatchGrace)
	if lines, busy := w.Check(); busy || !strings.Contains(joined(lines), "nothing is running") {
		t.Fatalf("a record older than its time limit is a dead run whose process id was reused: %q busy %v", lines, busy)
	}
}

func TestWatchSaysHowARunEnded(t *testing.T) {
	h := newHarness(t)
	step := fixedNow(t)
	r := liveRun(t, h.repo, KindTest, Now(), Now())
	w := NewWatch(h.repo, "")
	w.Check()
	r.Finished, r.TimedOut, r.Exit = Now(), true, 124
	must(t, appendRun(h.repo, r))
	if lines, _ := w.Check(); !strings.Contains(joined(lines), "test was stopped at its time limit: something likely hangs") {
		t.Fatalf("a timed-out run says so: %q", lines)
	}

	cmd := exec.Command("sleep", "30")
	must(t, cmd.Start())
	gone := &Run{ID: newID() + "-related", Kind: KindRelated, Command: "y", Started: Now(), PID: cmd.Process.Pid, Ran: -1}
	must(t, appendRun(h.repo, gone))
	if _, busy := w.Check(); !busy {
		t.Fatal("a live run is watched")
	}
	must(t, cmd.Process.Kill())
	_ = cmd.Wait()
	if lines, busy := w.Check(); len(lines) != 0 || !busy {
		t.Fatalf("a minute of grace for the final record: %q", lines)
	}
	step(30 * time.Second)
	if lines, busy := w.Check(); len(lines) != 0 || !busy {
		t.Fatalf("still within the grace: %q", lines)
	}
	step(30 * time.Second)
	if lines, busy := w.Check(); busy || !strings.Contains(joined(lines), "related ended without a recorded result") {
		t.Fatalf("a run whose process died unrecorded says so: %q busy %v", lines, busy)
	}
}

func TestWatchSeesARunByItsLiveFile(t *testing.T) {
	h := newHarness(t)
	fixedNow(t)
	r := &Run{ID: newID() + "-test", Kind: KindTest, Command: "z", Started: Now(), PID: 1 << 30, Ran: -1}
	must(t, appendRun(h.repo, r))
	write(t, liveOutputPath(h.repo, r.ID), "")
	if _, busy := NewWatch(h.repo, "").Check(); !busy {
		t.Fatal("a run whose process this shell can't see but whose live file is there is running")
	}
}

func TestRunsTailSkipsTheCutLine(t *testing.T) {
	h := newHarness(t)
	var last *Run
	for i := 0; i < 40; i++ {
		last = &Run{ID: newID() + "-lint", Kind: KindLint, Command: strings.Repeat("x", 100), Started: Now(), Ran: -1}
		must(t, appendRun(h.repo, last))
	}
	all := LoadRuns(h.repo)
	tail := loadRunsTail(h.repo, 1000)
	if len(tail) == 0 || len(tail) >= len(all) || tail[len(tail)-1].ID != last.ID {
		t.Fatalf("the tail holds the newest whole records only: %d of %d", len(tail), len(all))
	}
}

func TestLiveOutputTouchedOncePerSecond(t *testing.T) {
	p := filepath.Join(t.TempDir(), "live")
	write(t, p, "")
	old := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(p, old, old))
	w := &liveWriter{path: p}
	_, _ = w.Write([]byte("a\n"))
	if fi, _ := os.Stat(p); !fi.ModTime().After(old.Add(time.Minute)) {
		t.Fatal("the first output marks the time")
	}
	must(t, os.Chtimes(p, old, old))
	for i := 0; i < 1000; i++ {
		_, _ = w.Write([]byte("b\n"))
	}
	if fi, _ := os.Stat(p); !fi.ModTime().Equal(old) {
		t.Fatal("more output within the same second doesn't touch the file again")
	}
}

func TestLiveOutputIsCheap(t *testing.T) {
	h := newHarness(t)
	r, err := Execute(h.repo, nil, KindLint, "", "i=0; while [ $i -lt 3000 ]; do echo line $i; i=$((i+1)); done", nil, io.Discard)
	must(t, err)
	if _, err := os.Stat(liveOutputPath(h.repo, r.ID)); !os.IsNotExist(err) {
		t.Fatal(fmt.Sprintf("the live output file is removed when the run ends: %v", err))
	}
	if r.PID == 0 {
		t.Fatal("the run records its process")
	}
}

func TestWatcherMayRunAnytime(t *testing.T) {
	h := groundedTask(t, "small")
	mon := map[string]any{"command": "plumbline status --watch", "description": "hung runs and quiet helpers", "timeout_ms": 1800000}
	allowed := func(when string) {
		t.Helper()
		if s := fmtOut(h.pre("Monitor", mon)); strings.Contains(s, `"deny"`) || strings.Contains(s, `"ask"`) {
			t.Fatalf("the watcher must run %s: %s", when, s)
		}
	}
	allowed("before approval")
	h.docs(DocPlan, goodPlan)
	h.approve()
	allowed("while building")
	if o := h.post("Monitor", mon, map[string]any{}); stopped(o) {
		t.Fatalf("starting the watcher changes nothing: %v", o)
	}
}

func TestBackgroundWorkSuggestsTheWatcher(t *testing.T) {
	h := buildingTask(t)
	note := func(tool string, input map[string]any) bool {
		return strings.Contains(fmtOut(h.post(tool, input, map[string]any{})), "plumbline status --watch")
	}
	if !note("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go", "run_in_background": true}) {
		t.Error("a reviewer in the background suggests the watcher")
	}
	if !note("Bash", map[string]any{"command": "plumbline run checks", "run_in_background": true}) {
		t.Error("a suite in the background suggests the watcher")
	}
	if note("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}) {
		t.Error("a helper in the foreground needs no watcher")
	}
	if note("Bash", map[string]any{"command": "ls", "run_in_background": true}) {
		t.Error("other background commands aren't Plumbline's to watch")
	}
	if note("Agent", map[string]any{"subagent_type": "Explore", "prompt": "go", "run_in_background": true}) {
		t.Error("other agents aren't Plumbline's to watch")
	}
}
