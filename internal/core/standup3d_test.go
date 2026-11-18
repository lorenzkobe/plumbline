package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setNow pins Plumbline's clock for one test.
func setNow(t *testing.T, at *time.Time) {
	t.Helper()
	old := Now
	Now = func() time.Time { return at.UTC() }
	t.Cleanup(func() { Now = old })
}

func contextOf(o Output) string {
	h, _ := o["hookSpecificOutput"].(map[string]any)
	s, _ := h["additionalContext"].(string)
	return s
}

// S1: a file opened Monday and again Tuesday counts for both days, and so
// does a code change on each day, through the real hooks.
func TestStandupKeepsEachDaysReadsAndChanges(t *testing.T) {
	h := buildingTask(t)
	be := h.repo
	saveSetupKind(t, be, "company")
	fe := gitRepo(t, filepath.Join(filepath.Dir(be), "web"))
	feFile := filepath.Join(fe, "README.md")
	mon := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local)
	tue, wed := mon.AddDate(0, 0, 1), mon.AddDate(0, 0, 2)
	clock := mon.Add(10 * time.Hour)
	setNow(t, &clock)

	tk := h.task()
	tk.Created, tk.Checkpoint1At, tk.Checkpoint2At = mon.AddDate(0, 0, -7), time.Time{}, time.Time{}
	tk.LastCodeChange, tk.ChangeDays, tk.Reads = time.Time{}, nil, nil
	must(t, SaveTask(tk))

	a := filepath.Join(be, "src", "a.go")
	h.post("Read", map[string]any{"file_path": feFile}, map[string]any{})
	clock = mon.Add(11 * time.Hour) // a code change on Monday
	write(t, a, "package src\n\nfunc A() int { return 5 }\n")
	h.post("Edit", map[string]any{"file_path": a}, map[string]any{})
	clock = mon.Add(15 * time.Hour) // a second read the same day adds no day
	h.post("Read", map[string]any{"file_path": feFile}, map[string]any{})
	if r := h.task().Reads[feFile]; len(r.Days) != 0 {
		t.Fatalf("a second read the same day was kept as another day: %v", r.Days)
	}
	clock = tue.Add(9 * time.Hour) // Tuesday: read again from the terminal
	h.post("Bash", map[string]any{"command": "cat " + feFile}, map[string]any{"stdout": "x"})
	r := h.task().Reads[feFile]
	if len(r.Days) != 1 || !r.Days[0].Equal(mon.Add(15*time.Hour)) || !r.At.Equal(tue.Add(9*time.Hour)) {
		t.Fatalf("reads = %+v, want Monday 15:00 kept and Tuesday 09:00 last", r)
	}
	clock = tue.Add(11 * time.Hour) // and changed again
	write(t, a, "package src\n\nfunc A() int { return 6 }\n")
	h.post("Edit", map[string]any{"file_path": a}, map[string]any{})
	if got := h.task().ChangeDays; len(got) != 1 || !got[0].Equal(mon.Add(11*time.Hour)) {
		t.Fatalf("change days = %v, want Monday 11:00", got)
	}

	// Strip the logged events, so only reads and changes make it active.
	must(t, os.RemoveAll(logsDir()))
	for _, d := range []time.Time{mon, tue} {
		rep, err := Standup(be, d)
		must(t, err)
		out := rep.String()
		if !strings.Contains(out, tk.ID) || !strings.Contains(out, "read in web: 1 files") {
			t.Errorf("%s is missing the task or its read:\n%s", d.Format("Mon"), out)
		}
	}
	rep, err := Standup(be, wed)
	must(t, err)
	if strings.Contains(rep.String(), "read in web") || strings.Contains(rep.String(), "- task "+tk.ID) {
		t.Errorf("Wednesday shows work from other days:\n%s", rep.String())
	}

	// Only a change's own day counts: a task changed Monday only.
	tk2 := taskAt(t, be, "PROJ-7", "monday only", PhaseDone, mon.AddDate(0, 0, -3))
	clock = mon.Add(9 * time.Hour)
	tk2.noteCodeChange()
	must(t, SaveTask(tk2))
	for d, want := range map[time.Time]bool{mon: true, tue: false} {
		rep, err := Standup(be, d)
		must(t, err)
		if got := strings.Contains(rep.String(), "monday only"); got != want {
			t.Errorf("%s: task changed Monday listed = %v, want %v", d.Format("Mon"), got, want)
		}
	}
}

func TestDayTimesKeepsAtMostKeepDays(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local)
	var days []time.Time
	last := start
	for i := 1; i <= keepDays+10; i++ {
		now := start.AddDate(0, 0, i)
		days = dayTimes(days, last, now)
		last = now
	}
	if len(days) != keepDays {
		t.Fatalf("kept %d days, want %d", len(days), keepDays)
	}
	if want := start.AddDate(0, 0, keepDays+9); !days[len(days)-1].Equal(want) {
		t.Errorf("newest kept = %v, want %v", days[len(days)-1], want)
	}
	if got := dayTimes(nil, time.Time{}, start); got != nil {
		t.Errorf("no earlier read made days %v", got)
	}
	// An old task's read (no days field) still counts on its day.
	r := Read{Hash: "h", At: start}
	if !readIn(r, start.Add(-time.Hour), start.Add(time.Hour)) || readIn(r, start.Add(time.Hour), start.Add(2*time.Hour)) {
		t.Error("a read without days isn't judged by its time")
	}
}

// The window: since the last standup in this project, up to now.
func TestStandupSinceTheLastOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	be := gitRepo(t, filepath.Join(home, "work", "be"))
	other := gitRepo(t, filepath.Join(home, "work", "other"))
	tue9 := time.Date(2026, 11, 17, 9, 0, 0, 0, time.Local)

	// None yet: from the start of yesterday.
	from, note := StandupSince(be, tue9)
	if want := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local); !from.Equal(want) || !strings.Contains(note, "yesterday") {
		t.Fatalf("first standup from %v (%s), want %v", from, note, want)
	}
	must(t, RecordStandup(be, from, tue9))

	// Asked again the same morning: the same standup, the same start.
	if f, n := StandupSince(be, tue9.Add(40*time.Minute)); !f.Equal(from) || !strings.Contains(n, "again") {
		t.Errorf("a redo starts at %v (%s), want %v", f, n, from)
	}
	// The next day: from where the last one was asked.
	wed9 := tue9.AddDate(0, 0, 1)
	if f, n := StandupSince(be, wed9); !f.Equal(tue9) || !strings.Contains(n, "last standup") {
		t.Errorf("next day starts at %v (%s), want %v", f, n, tue9)
	}
	// Another project keeps its own.
	if f, _ := StandupSince(other, wed9); !f.Equal(tue9.Add(-9 * time.Hour)) {
		t.Errorf("another project started at %v, want the start of its yesterday", f)
	}
	// After a long break: the last 7 days at most.
	later := tue9.AddDate(0, 0, 30)
	if f, n := StandupSince(be, later); !f.Equal(later.AddDate(0, 0, -standupMaxDays)) || !strings.Contains(n, "7 days") {
		t.Errorf("after a break: %v (%s)", f, n)
	}
	// A broken or future record counts as none.
	for _, bad := range []string{"{not json", `{"from":"2030-01-01T00:00:00Z","at":"2030-01-02T00:00:00Z"}`, `{"from":"2026-11-18T00:00:00Z","at":"2026-11-17T00:00:00Z"}`, `{}`} {
		must(t, os.WriteFile(standupMarkFile(be), []byte(bad), 0o600))
		if _, n := StandupSince(be, wed9); !strings.Contains(n, "yesterday") {
			t.Errorf("record %q was used: %s", bad, n)
		}
	}

	// The listing covers [from, to): a commit just before from is out.
	commitAt(t, be, "feat: before", "me@example.com", tue9.Add(-time.Minute))
	commitAt(t, be, "feat: after", "me@example.com", tue9.Add(time.Hour))
	commitAt(t, be, "feat: this morning", "me@example.com", wed9.Add(-time.Hour))
	rep, err := StandupBetween(be, tue9, wed9)
	must(t, err)
	rep.Note = "since your last standup here"
	out := rep.String()
	for _, want := range []string{"Standup from Tue 2026-11-17 09:00 to Wed 2026-11-18 09:00 (since your last standup here)", "feat: after", "feat: this morning", "MemPalace window: since=2026-11-17T09:00:00 before=2026-11-18T09:00:00"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "feat: before") {
		t.Errorf("a commit before the window was listed:\n%s", out)
	}
}

// S4: a recursive delete or an extraction over the sprint file's folder
// asks, and a change made where Plumbline couldn't see it coming is put in
// front of Claude after the command.
func TestSprintFileChangesAreSeen(t *testing.T) {
	h := buildingTask(t)
	sp := filepath.Join(h.repo, "coverage", "sprint.md")
	write(t, sp, "- [ ] PROJ-1 filter\n- [ ] PROJ-2 export\n")
	_, err := SetTracker(h.repo, sp)
	must(t, err)

	h.expect(h.bash("rm -rf coverage"), "ask", "sprint file")
	h.expect(h.bash("rm -r "+filepath.Join(h.repo, "coverage")), "ask", "sprint file")
	h.expect(h.bash("tar -xf a.tar -C coverage"), "ask", "sprint file")
	for _, cmd := range []string{"rm coverage/other.md", "rm -rf coverage/old", "cp a.txt coverage/"} {
		if _, r := decision(h.bash(cmd)); strings.Contains(r, "sprint file") {
			t.Errorf("%s asked about the sprint file: %s", cmd, r)
		}
	}

	bash := map[string]any{"command": `python3 -c "open('coverage/sprint.md','w').write('x')"`}
	copies := trackerBeforeDir(h.repo)
	// Changed where the hook couldn't see it: Claude is told, with the change.
	h.call(true, "Bash", "tu1", bash, nil)
	if _, err := os.Stat(filepath.Join(copies, "tu1.json")); err != nil {
		t.Fatalf("no copy before the command: %v", err)
	}
	write(t, sp, "- [x] PROJ-1 filter\n- [ ] PROJ-2 export\n")
	ctx := contextOf(h.call(false, "Bash", "tu1", bash, map[string]any{}))
	for _, want := range []string{"sprint file changed (sprint.md)", "wasn't asked", "Tell the user first", "-- [ ] PROJ-1 filter", "+- [x] PROJ-1 filter"} {
		if !strings.Contains(ctx, want) {
			t.Errorf("after the command, missing %q:\n%s", want, ctx)
		}
	}
	if strings.Contains(ctx, copies) || strings.Contains(ctx, ".before") {
		t.Errorf("the note names Plumbline's copy:\n%s", ctx)
	}
	if _, err := os.Stat(filepath.Join(copies, "tu1.json")); !os.IsNotExist(err) {
		t.Error("the copy was kept after the command")
	}

	// Unchanged: nothing said.
	h.call(true, "Bash", "tu2", bash, nil)
	if ctx := contextOf(h.call(false, "Bash", "tu2", bash, map[string]any{})); strings.Contains(ctx, "sprint file") {
		t.Errorf("an unchanged sprint file was reported: %s", ctx)
	}

	// The prompt named it: the user was asked, so nothing more.
	echo := map[string]any{"command": "echo '- [ ] PROJ-3' >> coverage/sprint.md"}
	h.expect(h.call(true, "Bash", "tu3", echo, nil), "ask", "sprint file")
	write(t, sp, "- [x] PROJ-1 filter\n- [ ] PROJ-2 export\n- [ ] PROJ-3\n")
	if ctx := contextOf(h.call(false, "Bash", "tu3", echo, map[string]any{})); strings.Contains(ctx, "sprint file") {
		t.Errorf("a change the user was asked about was reported again: %s", ctx)
	}

	// A command that failed still gets the check, and a deletion says so.
	h.call(true, "Bash", "tu4", bash, nil)
	must(t, os.Remove(sp))
	in := h.in("Bash", bash, map[string]any{})
	in.ToolUseID = "tu4"
	if ctx := contextOf(PostToolUseFailure(in)); !strings.Contains(ctx, "was deleted or moved (sprint.md)") {
		t.Errorf("a deletion by a failed command wasn't reported: %s", ctx)
	}

	// Read-only lines and odd tool use ids keep no copy.
	write(t, sp, "x\n")
	h.call(true, "Bash", "tu5", map[string]any{"command": "ls coverage"}, nil)
	h.call(true, "Bash", "../escape", bash, nil)
	entries, _ := os.ReadDir(copies)
	if len(entries) != 0 {
		t.Errorf("copies kept: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(copies), "escape.json")); !os.IsNotExist(err) {
		t.Error("a tool use id with a path wrote outside the copies folder")
	}

	// A copy whose post hook never ran is dropped a day later.
	stale := filepath.Join(copies, "old.json")
	write(t, stale, "{}")
	past := time.Now().Add(-25 * time.Hour)
	must(t, os.Chtimes(stale, past, past))
	h.call(true, "Bash", "tu6", bash, nil)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a day-old copy wasn't dropped")
	}
}

// With no sprint file set, commands keep no copy and say nothing.
func TestNoSprintFileNoCheck(t *testing.T) {
	h := buildingTask(t)
	bash := map[string]any{"command": `python3 -c "print(1)"`}
	h.call(true, "Bash", "tu1", bash, nil)
	if _, err := os.Stat(trackerBeforeDir(h.repo)); !os.IsNotExist(err) {
		t.Error("a copies folder was made without a sprint file")
	}
	if ctx := contextOf(h.call(false, "Bash", "tu1", bash, map[string]any{})); strings.Contains(ctx, "sprint") {
		t.Errorf("reported without a sprint file: %s", ctx)
	}
}

func TestAddContextKeepsOtherOutput(t *testing.T) {
	o := addContext(Output{"decision": "block", "reason": "r"}, "note")
	if o["decision"] != "block" || contextOf(o) != "note" {
		t.Errorf("a block lost its note or its decision: %v", o)
	}
	o = addContext(Context("PostToolUse", "a"), "b")
	if contextOf(o) != "a b" {
		t.Errorf("context = %q", contextOf(o))
	}
	o = addContext(Output{"hookSpecificOutput": map[string]any{"hookEventName": "PostToolUse"}}, "c")
	if contextOf(o) != "c" {
		t.Errorf("an empty context wasn't filled: %v", o)
	}
	if contextOf(addContext(nil, "d")) != "d" {
		t.Error("nil output")
	}
}

// A helper can't talk to the user: it's told to hand the change to the lead.
func TestSprintFileChangeByHelperGoesToTheLead(t *testing.T) {
	h := buildingTask(t)
	sp := filepath.Join(h.repo, "coverage", "sprint.md")
	write(t, sp, "- [ ] PROJ-1\n")
	_, err := SetTracker(h.repo, sp)
	must(t, err)
	bash := map[string]any{"command": "./scripts/gen.sh"}
	h.call(true, "Bash", "tu1", bash, nil)
	write(t, sp, "- [x] PROJ-1\n")
	in := h.in("Bash", bash, map[string]any{})
	in.ToolUseID, in.AgentID, in.AgentType = "tu1", "agent-1", "plumbline:builder"
	c := newCtx(in)
	note := c.trackerChanged()
	c.close()
	if !strings.Contains(note, "so the lead tells the user") || strings.Contains(note, "Tell the user first") {
		t.Errorf("a helper's note: %s", note)
	}
}
