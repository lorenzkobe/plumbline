package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func (h *harness) haltBuilder(id, wt string) Output {
	h.t.Helper()
	cmd := map[string]any{"command": "./gen.sh"}
	if real, err := filepath.EvalSymlinks(wt); err == nil {
		wt = real
	}
	RefreshSnapshot(h.sid+"-"+id, wt)
	in := h.helperIn(id, HelperBuilder, "Bash", cmd)
	in.Cwd = wt
	h.expect(PreToolUse(in), "", "")
	for _, f := range []string{"x1.go", "x2.go", "x3.go", "x4.go"} {
		write(h.t, filepath.Join(wt, "src", f), "package src\n\nvar x = 1\n")
	}
	in = h.helperIn(id, HelperBuilder, "Bash", cmd)
	in.Cwd, in.ToolResponse = wt, []byte("{}")
	return PostToolUse(in)
}

func TestBuilderHaltedByPlumblineIsStopped(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	if o := h.haltBuilder(id, wt); !stopped(o) {
		t.Fatalf("four files outside the plan should halt the builder: %v", o)
	}
	r := h.run(HelperBuilder, id)
	if r.Finished.IsZero() || r.Status != "stopped by Plumbline" || !strings.Contains(r.Error, "outside the approved plan") {
		t.Fatalf("a halted builder must be recorded as stopped, with why: finished %v, status %q, error %q", r.Finished, r.Status, r.Error)
	}
	if r.alive() {
		t.Fatal("a halted builder isn't running")
	}
	tk := h.task()
	changed, _, err := MergeBuilder(tk, 1, nil)
	if err != nil {
		t.Fatalf("a halted builder's copy can be merged right away: %v", err)
	}
	must(t, SaveTask(tk))
	if len(changed) != 4 {
		t.Fatalf("merged %v", changed)
	}
}

func TestLeadHaltedLeavesHelpersAlone(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	h.post("Bash", map[string]any{"command": "true"}, nil)
	for _, f := range []string{"y1.go", "y2.go", "y3.go", "y4.go"} {
		write(t, filepath.Join(h.repo, "src", f), "package src\n\nvar y = 1\n")
	}
	if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, nil); !stopped(o) {
		t.Fatalf("the lead past the cap is stopped: %v", o)
	}
	if r := h.run(HelperBuilder, id); !r.Finished.IsZero() || r.Status != "" {
		t.Fatalf("the lead's stop must not touch a builder: %+v", r)
	}
}

func TestResumedBuilderReopensItsRun(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	first := h.run(HelperBuilder, id)
	wt, findings := first.Worktree, first.Findings
	h.haltBuilder(id, wt)

	start := func() Output {
		return SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	}
	o := start()
	tk := h.task()
	if n := len(tk.helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("a resume must not add a run: %d runs", n)
	}
	r := tk.runByAgent(id)
	if r.Worktree != wt || r.Findings != findings || r.Error != "" || r.Status != "" || !r.Finished.IsZero() || r.PrintAfter != "" {
		t.Fatalf("the run should be reopened on its own copy: %+v", r)
	}
	if strings.Contains(fmtOut(o), "already exists") || !strings.Contains(fmtOut(o), wt) {
		t.Fatalf("the resumed builder should be told its copy: %s", fmtOut(o))
	}
	if !r.alive() {
		t.Fatal("a reopened builder is running")
	}
	if _, _, err := MergeBuilder(tk, 1, nil); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("a reopened builder can't be merged under it: %v", err)
	}

	start()
	if n := len(h.task().helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("a second resume must not add a run either: %d runs", n)
	}

	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/x1.go: added\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("stop: %v", o)
	}
	tk = h.task()
	if _, _, err := MergeBuilder(tk, 1, nil); err != nil {
		t.Fatalf("the resumed builder's stop finishes its run, so it merges: %v", err)
	}
}

func TestResumeFindsTheRunWithTheCopy(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	tk := h.task()
	tk.Helpers = append(tk.Helpers, HelperRun{Kind: HelperBuilder, AgentID: id, Started: Now(), Finished: Now(),
		Findings: findingsName(HelperBuilder, 2), Status: "blocked", Error: "couldn't make the copy: " + wt + " already exists"})
	must(t, SaveTask(tk))

	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	if strings.Contains(fmtOut(o), "already exists") {
		t.Fatalf("the copy that exists is reused: %s", fmtOut(o))
	}
	tk = h.task()
	if n := len(tk.helperRuns(HelperBuilder)); n != 2 {
		t.Fatalf("no new run on resume: %d runs", n)
	}
	if r := tk.helperRuns(HelperBuilder)[0]; !r.Finished.IsZero() || r.Worktree != wt {
		t.Fatalf("the run with the copy is the one reopened: %+v", r)
	}
	if r := tk.runByAgent(id); r.Worktree != wt {
		t.Fatalf("the resumed builder's stop must find the run with the copy: %+v", r)
	}
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	h.helperPost(id, HelperBuilder, "Write", map[string]any{"file_path": filepath.Join(wt, "src", "a.go")}, nil)
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: 3\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("stop: %v", o)
	}
	tk = h.task()
	if _, _, err := MergeBuilder(tk, 1, nil); err != nil {
		t.Fatalf("after its stop, the reopened builder merges: %v", err)
	}
}

func TestResumedReviewerKeepsItsNewRun(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperCodeReviewer})
	tk := h.task()
	runs := tk.helperRuns(HelperCodeReviewer)
	if len(runs) != 2 || tk.runByAgent(id).Findings != runs[1].Findings {
		t.Fatalf("a restarted reviewer's hooks find its newest run: %+v", runs)
	}
}

func TestReopenedAfterRestartIsRunning(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	tk := h.task()
	r := tk.runByAgent(id)
	r.Started = Now().Add(-time.Hour)
	r.Session = "old-session"
	must(t, SaveTask(tk))
	step := fixedNow(t)
	sessionStarted(h.sid, "resume")
	write(t, sessionEndedPath("old-session"), Now().Add(-time.Minute).Format(time.RFC3339)+"\n")
	step(2 * time.Second)

	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	r = h.task().runByAgent(id)
	if r.Session != h.sid || !r.alive() {
		t.Fatalf("a builder reopened after a restart runs in the new session: session %q alive %v", r.Session, r.alive())
	}
}

func TestMergedBuilderStartedAgainGetsANewCopy(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	h.helperPost(id, HelperBuilder, "Write", map[string]any{"file_path": filepath.Join(wt, "src", "a.go")}, nil)
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: 3\n"))
	h.stopHelper(id, HelperBuilder)
	tk := h.task()
	_, _, err := MergeBuilder(tk, 1, nil)
	must(t, err)
	must(t, SaveTask(tk))

	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	tk = h.task()
	if n := len(tk.helperRuns(HelperBuilder)); n != 2 {
		t.Fatalf("a merged builder started again is a new run: %d runs", n)
	}
	r := tk.runByAgent(id)
	if r.Worktree == "" || r.Error != "" || r.Merged {
		t.Fatalf("it gets a new copy: %+v %s", r, fmtOut(o))
	}
	if _, err := os.Stat(r.Worktree); err != nil {
		t.Fatalf("the new copy exists: %v", err)
	}
}
