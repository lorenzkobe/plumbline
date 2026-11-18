package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tests for release 3d (0.6.6), M14: two sessions in one checkout. Changes are attributed to the
// session that made them; a change made while both sessions' commands ran
// counts for both; a task leaves out what another session changed; and a
// second task can't start in a checkout that has one open.

// session: another Claude session in the same checkout.
func (h *harness) session(sid string) *harness { return &harness{t: h.t, repo: h.repo, sid: sid} }

// call runs one hook with a tool use id, so a call in flight can be told apart.
func (h *harness) call(pre bool, tool, id string, input, resp any) Output {
	in := h.in(tool, input, resp)
	in.ToolUseID = id
	if pre {
		return PreToolUse(in)
	}
	return PostToolUse(in)
}

func stopped(o Output) bool { return o["continue"] == false }

// The original M14: one session's edits stopped the other.
func TestOtherSessionsChangesDontStopThisOne(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}) // b's snapshot
	a := filepath.Join(h.repo, "src", "a.go")
	h.call(true, "Edit", "e1", map[string]any{"file_path": a}, nil)
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.call(false, "Edit", "e1", map[string]any{"file_path": a}, map[string]any{})
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("the other session's edit stopped this one: %v", o)
	}
	// A terminal command of the building session too.
	h.call(true, "Bash", "c1", map[string]any{"command": "./test.sh"}, nil)
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.call(false, "Bash", "c1", map[string]any{"command": "./test.sh"}, map[string]any{})
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("the other session's command stopped this one: %v", o)
	}
	// The other session's own change still stops it.
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	if o := b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{}); !stopped(o) {
		t.Fatalf("a change after this session's own command must still stop it: %v", o)
	}
}

// What another session changed while the task was open isn't the task's.
func TestOtherSessionsFilesAreNotTheTasks(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 9 }\n")
	if o := b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{}); !stopped(o) {
		t.Fatalf("the change must stop the session whose command made it: %v", o)
	}
	if o := h.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); o != nil {
		t.Fatalf("the other session's change was put on this task: %v", fmtOut(o))
	}
	tk := h.task()
	if _, ok := tk.OutsidePlan["src/b.go"]; ok {
		t.Fatal("src/b.go was listed outside the plan though another session changed it")
	}
	if !tk.OthersWork()["src/b.go"] {
		t.Fatalf("src/b.go isn't counted as another session's: %v", tk.OthersWork())
	}
	changes, err := taskChanges(tk, nil)
	must(t, err)
	for _, d := range changes {
		if d.Path == "src/b.go" {
			t.Fatal("the task's diff holds another session's file")
		}
	}
	if contains(reviewedFiles(tk, nil), "src/b.go") {
		t.Fatal("the code reviewer would have to cover another session's file")
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}), "deny", "another Claude session")
	if _, held := h.task().Held["src/b.go"]; !held {
		t.Fatal("the refused file isn't held for the report")
	}
	// A file in the plan stays the task's, whoever changed it last.
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 5 }\n")
	b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{})
	if h.task().OthersWork()["src/a.go"] {
		t.Fatal("a planned file changed by another session left the task's review")
	}
}

// A terminal command that changes another session's file: held, not the task's.
func TestCommandOnOtherSessionsFileIsHeld(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	p := filepath.Join(h.repo, "src", "b.go")
	write(t, p, "package src\n\nfunc B() int { return 9 }\n")
	b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, p, "package src\n\nfunc B() int { return 10 }\n")
	o := h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{})
	if !strings.Contains(fmtOut(o), "another Claude session") {
		t.Fatalf("a command changing another session's file must say so: %v", fmtOut(o))
	}
	tk := h.task()
	if _, held := tk.Held["src/b.go"]; !held {
		t.Fatal("not held for the report")
	}
	if _, out := tk.OutsidePlan["src/b.go"]; out {
		t.Fatal("listed as the task's own outside-the-plan change")
	}
}

// Commands of both sessions running at once: the change counts for both.
func TestOverlappingCommandsCountForBoth(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	if d, r := decision(h.call(true, "Bash", "h1", map[string]any{"command": "./gen.sh"}, nil)); d == "deny" {
		t.Fatalf("setup: %s", r)
	}
	if d, r := decision(b.call(true, "Bash", "b1", map[string]any{"command": "plumbline run test"}, nil)); d == "deny" {
		t.Fatalf("setup: %s", r)
	}
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 9 }\n")
	if o := b.call(false, "Bash", "b1", map[string]any{"command": "plumbline run test"}, map[string]any{}); !stopped(o) {
		t.Fatalf("the change must count for the session that finished first: %v", o)
	}
	o := h.call(false, "Bash", "h1", map[string]any{"command": "./gen.sh"}, map[string]any{})
	if !strings.Contains(fmtOut(o), "src/b.go") {
		t.Fatalf("the change must count for the other running session too: %v", fmtOut(o))
	}
	if _, ok := h.task().OutsidePlan["src/b.go"]; !ok {
		t.Fatal("not recorded on the task")
	}
	if h.task().OthersWork()["src/b.go"] {
		t.Fatal("a change that counts for this task too was left out of it")
	}
}

// A read-only command can't have made a change: what another session's
// running command changes waits for that command's own check.
func TestReadOnlyCommandWaitsForTheRunningSession(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.call(true, "Bash", "h1", map[string]any{"command": "./gen.sh"}, nil)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 2 }\n")
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("a read-only command was blamed for the other session's running one: %v", o)
	}
	h.call(false, "Bash", "h1", map[string]any{"command": "./gen.sh"}, map[string]any{})
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("the change came back after the other session's check took it: %v", o)
	}
	// A command whose check never ran stops counting as running after a
	// while, and its changes count again (fail closed).
	h.call(true, "Bash", "h2", map[string]any{"command": "./gen.sh"}, nil)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	old := Now
	Now = func() time.Time { return old().Add(leadBusyFor + time.Minute) }
	defer func() { Now = old }()
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); !stopped(o) {
		t.Fatalf("a change from a command long gone must count again: %v", o)
	}
}

// Changes made while paused are still attributed, so resuming stops no one.
func TestPausedChangesStillAttributed(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	must(t, SetPaused(h.repo, true))
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, map[string]any{})
	must(t, SetPaused(h.repo, false))
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("an edit made while paused stopped the other session after resume: %v", o)
	}
}

// A changes record edited outside Plumbline counts for nothing: every change
// is the task's again.
func TestTamperedChangesRecordCountsForNothing(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 9 }\n")
	b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{})
	if !h.task().OthersWork()["src/b.go"] {
		t.Fatal("setup: the other session's change wasn't recorded")
	}
	p := treeChangesPath(h.repo)
	raw, err := os.ReadFile(p)
	must(t, err)
	must(t, os.WriteFile(p, []byte(strings.Replace(string(raw), "sess-2", "sess-9", 1)), 0o600))
	if h.task().OthersWork()["src/b.go"] {
		t.Fatal("an edited record still left a file out of the task")
	}
}

// A second task can't start in a checkout with another session's open task.
func TestSecondTaskInCheckoutRefused(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tk := h.startTask("normal")
	b := h.session("sess-2")
	h.expect(b.bash("plumbline task start --title 'other work'"), "deny", "claude -w")
	_, r := decision(b.bash("plumbline task start --title 'other work'"))
	if !strings.Contains(r, tk.ID) || !strings.Contains(r, "plumbline task adopt") {
		t.Fatalf("the refusal must name the open task and the ways on: %s", r)
	}
	if d, r := decision(h.bash("plumbline task start --title 'next'")); d == "deny" {
		t.Fatalf("the session owning the open task was refused: %s", r)
	}
	// Its own worktree is a separate checkout.
	wt := filepath.Join(filepath.Dir(h.repo), "wt")
	run(t, h.repo, "git", "worktree", "add", "-q", wt)
	wt, _ = filepath.EvalSymlinks(wt)
	w := &harness{t: t, repo: wt, sid: "sess-3"}
	if d, r := decision(w.bash("plumbline task start --title 'other work'")); d == "deny" {
		t.Fatalf("a task in its own worktree was refused: %s", r)
	}
	// Once the task is closed, the checkout is free again.
	tk = h.task()
	tk.Phase = PhaseCancelled
	must(t, SaveTask(tk))
	if d, r := decision(b.bash("plumbline task start --title 'other work'")); d == "deny" {
		t.Fatalf("refused after the open task was closed: %s", r)
	}
}

// Old snapshots of sessions long gone are cleaned up, so they don't keep
// every hook busy sharing changes with them.
func TestCleanupDropsOldSnapshots(t *testing.T) {
	h := newHarness(t)
	old := snapPath("gone", h.repo)
	fresh := snapPath("here", h.repo)
	saveSnap("gone", h.repo, map[string]string{})
	saveSnap("here", h.repo, map[string]string{})
	past := time.Now().Add(-8 * 24 * time.Hour)
	must(t, os.Chtimes(old, past, past))
	Cleanup()
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("an 8-day-old snapshot was kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatal("a fresh snapshot was removed")
	}
}

// The task's own changes are never another session's: not those made while
// paused (no outside-the-plan note then), nor after another session adopts it.
func TestOwnChangesStayTheTasks(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	must(t, SetPaused(h.repo, true))
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{})
	must(t, SetPaused(h.repo, false))
	tk := h.task()
	if tk.OthersWork()["src/c.go"] {
		t.Fatal("the task's own change while paused was counted as another session's")
	}
	// Another session adopts the task: what the first session did stays the task's.
	tk.SessionID = "sess-3"
	must(t, SaveTask(tk))
	if tk.OthersWork()["src/c.go"] {
		t.Fatal("after an adopt, the task's earlier change was counted as another session's")
	}
	// A hook of the task's own session that couldn't find the task (no task
	// id on its record) is still the task's.
	write(t, filepath.Join(h.repo, "src", "d.go"), "package src\n")
	now, err := DirtyFiles(h.repo)
	must(t, err)
	c := &hookCtx{in: &HookInput{SessionID: "sess-3", ToolUseID: "x"}, root: h.repo}
	c.share(loadTreeChanges(h.repo), []string{"src/d.go"}, now, true)
	if tk.OthersWork()["src/d.go"] {
		t.Fatal("a change by the task's own session without a task id was counted as another session's")
	}
	changes, err := taskChanges(tk, nil)
	must(t, err)
	found := false
	for _, d := range changes {
		found = found || d.Path == "src/c.go"
	}
	if !found {
		t.Fatal("the task's own file is missing from its diff")
	}
}

// A command that exits non-zero gets PostToolUseFailure, not PostToolUse:
// its changes are checked, and its running mark ends.
func TestFailedCommandIsChecked(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.call(true, "Bash", "f1", map[string]any{"command": "./gen.sh"}, nil)
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	in := h.in("Bash", map[string]any{"command": "./gen.sh"}, nil)
	in.ToolUseID = "f1"
	o := PostToolUseFailure(in)
	if !strings.Contains(fmtOut(o), "src/c.go") || !strings.Contains(fmtOut(o), "PostToolUseFailure") {
		t.Fatalf("the failed command's change wasn't checked: %v", fmtOut(o))
	}
	if _, ok := h.task().OutsidePlan["src/c.go"]; !ok {
		t.Fatal("not recorded on the task")
	}
	if len(loadTreeChanges(h.repo).Busy) != 0 {
		t.Fatal("the failed command still counts as running")
	}
	if o := b.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); stopped(o) {
		t.Fatalf("the failed command's change stopped the other session: %v", o)
	}
	// A failed edit changed nothing: its mark ends, nothing is recorded.
	a := filepath.Join(h.repo, "src", "a.go")
	h.call(true, "Edit", "f2", map[string]any{"file_path": a}, nil)
	in = h.in("Edit", map[string]any{"file_path": a}, nil)
	in.ToolUseID = "f2"
	PostToolUseFailure(in)
	tc := loadTreeChanges(h.repo)
	if len(tc.Busy) != 0 || len(tc.Changes["src/a.go"]) != 0 {
		t.Fatalf("a failed edit left a mark: %+v", tc)
	}
}

// A command declined at a prompt gets no post hook: the end of the turn
// ends its running mark, so the other session's changes aren't put on it
// for the next 10 minutes. A background helper's mark stays.
func TestTurnEndEndsDeclinedCommand(t *testing.T) {
	h := buildingTask(t)
	b := h.session("sess-2")
	b.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	h.call(true, "Bash", "d1", map[string]any{"command": "./gen.sh"}, nil) // declined: no post
	tc := loadTreeChanges(h.repo)
	tc.Busy[busyKey(h.sid+"-agent-1", "x")] = busyMark{Session: h.sid, Snap: h.sid + "-agent-1", Any: true, At: Now()}
	saveTreeChanges(h.repo, tc)
	Stop(&HookInput{SessionID: h.sid, Cwd: h.repo})
	tc = loadTreeChanges(h.repo)
	if _, ok := tc.Busy[busyKey(h.sid, "d1")]; ok {
		t.Fatal("the declined command still counts as running after the turn ended")
	}
	if _, ok := tc.Busy[busyKey(h.sid+"-agent-1", "x")]; !ok {
		t.Fatal("a background helper's running command was ended by the lead's turn")
	}
	// The other session's change no longer lands on this session.
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	b.post("Bash", map[string]any{"command": "./tool.sh"}, map[string]any{})
	if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{}); strings.Contains(fmtOut(o), "src/c.go") {
		t.Fatalf("the other session's change was put on this one: %v", fmtOut(o))
	}
}
