package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func gitRepo(t *testing.T, dir string) string {
	t.Helper()
	must(t, os.MkdirAll(dir, 0o755))
	run(t, dir, "git", "init", "-q", "-b", "main")
	run(t, dir, "git", "config", "user.email", "me@example.com")
	run(t, dir, "git", "config", "user.name", "Me")
	write(t, filepath.Join(dir, "README.md"), "x\n")
	commitAt(t, dir, "init", "me@example.com", time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local))
	d, _ := filepath.EvalSymlinks(dir)
	return d
}

// commitAt makes a commit by email at a fixed time.
func commitAt(t *testing.T, dir, msg, email string, at time.Time) {
	t.Helper()
	f := filepath.Join(dir, "log.txt")
	b, _ := os.ReadFile(f)
	write(t, f, string(b)+msg+"\n")
	run(t, dir, "git", "add", "-A")
	c := exec.Command("git", "-c", "user.email="+email, "-c", "user.name=X", "commit", "-q", "-m", msg)
	c.Dir = dir
	ts := at.Format(time.RFC3339)
	c.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+ts, "GIT_COMMITTER_DATE="+ts)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}
}

func saveSetupKind(t *testing.T, repo, kind string) {
	t.Helper()
	_, err := SaveSetup(repo, &Setup{Kind: kind})
	must(t, err)
}

func taskAt(t *testing.T, repo, ticket, title, phase string, created time.Time) *Task {
	t.Helper()
	tk := &Task{ID: NewID(), Title: title, Ticket: ticket, Size: "normal", Root: repo, Phase: phase, Created: created}
	must(t, SaveTask(tk))
	return tk
}

func TestStandupFollowsYourTicketsAcrossRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	parent := filepath.Join(home, "work")
	be := gitRepo(t, filepath.Join(parent, "orders-service"))
	fe := gitRepo(t, filepath.Join(parent, "web"))
	other := gitRepo(t, filepath.Join(parent, "billing"))
	personal := gitRepo(t, filepath.Join(home, "side", "hobby"))
	saveSetupKind(t, be, "company")
	saveSetupKind(t, personal, "personal")

	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local)
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }

	// The ticket's task, in the backend; it also opened a file in the frontend.
	tk := taskAt(t, be, "PROJ-1", "fix the order filter", PhaseBuilding, at(9))
	feFile := filepath.Join(fe, "README.md")
	tk.Reads = map[string]Read{feFile: {Hash: "h", At: at(11)}, filepath.Join(be, "README.md"): {Hash: "h", At: at(10)}}
	must(t, SaveTask(tk))
	// Another open ticket here, untouched that day: what's next, not what was done.
	taskAt(t, be, "PROJ-2", "add export", PhasePlanning, day.AddDate(0, 0, -3))
	// A finished task from the day before: neither.
	taskAt(t, be, "PROJ-0", "old thing", PhaseDone, day.AddDate(0, 0, -1))
	// A personal project's task the same day: not related.
	taskAt(t, personal, "", "new landing page", PhaseBuilding, at(14))

	commitAt(t, be, "PROJ-1: fix the order filter", "me@example.com", at(15))
	commitAt(t, be, "PROJ-1: their change", "teammate@example.com", at(16)) // not mine
	commitAt(t, be, "chore: bump go", "me@example.com", at(17))             // mine, no ticket
	commitAt(t, be, "PROJ-1: yesterday's", "me@example.com", at(-3))        // other day
	commitAt(t, fe, "PROJ-1: show the new filter", "me@example.com", at(13))
	commitAt(t, fe, "PROJ-9: unrelated", "me@example.com", at(14))      // other ticket
	commitAt(t, fe, "PROJ-12: looks alike", "me@example.com", at(14))   // PROJ-1 isn\'t PROJ-12
	commitAt(t, other, "fix: something else", "me@example.com", at(12)) // no ticket, not this work
	commitAt(t, personal, "feat: hero", "me@example.com", at(18))

	r, err := Standup(be, day)
	must(t, err)
	out := r.String()
	for _, want := range []string{
		"Mon 2026-11-16", "from orders-service",
		"- PROJ-1\n", `task ` + tk.ID + ` "fix the order filter" in orders-service: building`,
		"commit in orders-service:", "PROJ-1: fix the order filter",
		"commit in web:", "PROJ-1: show the new filter",
		"read in web: 1 files",
		"commits with no ticket or task:", "orders-service:", "chore: bump go",
		`PROJ-2 "add export" in orders-service: planning step`,
		"Tickets: PROJ-1\n", "Repos with your work: orders-service, web\n",
		"MemPalace window: since=2026-11-16T00:00:00 before=2026-11-17T00:00:00\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("standup is missing %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"their change", "yesterday's", "PROJ-9", "something else", "hero", "landing page", "old thing", "read in orders-service", "hobby", "billing", "looks alike"} {
		if strings.Contains(out, not) {
			t.Errorf("standup should leave out %q:\n%s", not, out)
		}
	}

	// From the personal project: its own work only.
	p, err := Standup(personal, day)
	must(t, err)
	pout := p.String()
	if !strings.Contains(pout, `"new landing page" (no ticket)`) || !strings.Contains(pout, "feat: hero") {
		t.Errorf("personal standup is missing its own work:\n%s", pout)
	}
	for _, not := range []string{"PROJ-1", "orders-service", "web"} {
		if strings.Contains(pout, not) {
			t.Errorf("personal standup should leave out %q:\n%s", not, pout)
		}
	}

	// A day with nothing.
	e, err := Standup(be, day.AddDate(0, 0, 5))
	must(t, err)
	if !strings.Contains(e.String(), "nothing recorded") {
		t.Errorf("empty day:\n%s", e.String())
	}
}

func TestStandupLeavesOutStashesAndLooseTickets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	parent := filepath.Join(home, "code")
	app := gitRepo(t, filepath.Join(parent, "app"))
	lib := gitRepo(t, filepath.Join(parent, "lib"))
	saveSetupKind(t, app, "personal")
	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local)
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }

	// A stash made that day is a commit, but not work.
	write(t, filepath.Join(app, "README.md"), "half done\n")
	c := exec.Command("git", "stash", "push", "-m", "wip")
	c.Dir = app
	c.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+at(10).Format(time.RFC3339), "GIT_COMMITTER_DATE="+at(10).Format(time.RFC3339))
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("stash: %v\n%s", err, out)
	}
	// A free-text ticket ("42") isn't followed into other repos.
	taskAt(t, app, "42", "fix issue 42", PhaseBuilding, at(9))
	commitAt(t, lib, "chore: bump to v1.42.0", "me@example.com", at(11))
	commitAt(t, lib, "fix #42 in lib", "me@example.com", at(12))

	r, err := Standup(app, day)
	must(t, err)
	out := r.String()
	if !strings.Contains(out, `task`) || !strings.Contains(out, "fix issue 42") {
		t.Errorf("the task itself is missing:\n%s", out)
	}
	for _, not := range []string{"wip", "index on", "v1.42.0", "fix #42 in lib", "lib:"} {
		if strings.Contains(out, not) {
			t.Errorf("standup should leave out %q:\n%s", not, out)
		}
	}
}

func TestStandupSkipsTamperedAndMovedTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	be := gitRepo(t, filepath.Join(home, "be"))
	saveSetupKind(t, be, "company")
	day := time.Date(2026, 11, 16, 0, 0, 0, 0, time.Local)
	tk := taskAt(t, be, "PROJ-5", "forged", PhaseBuilding, day.Add(9*time.Hour))
	p := statePath(be, tk.ID)
	b, _ := os.ReadFile(p)
	must(t, os.WriteFile(p, []byte(strings.Replace(string(b), "forged", "edited", 1)), 0o600))
	r, err := Standup(be, day)
	must(t, err)
	if strings.Contains(r.String(), "PROJ-5") {
		t.Errorf("a tampered task was reported:\n%s", r.String())
	}
}

func TestSprintFileTracker(t *testing.T) {
	h := newHarness(t)
	h.setup()
	// Inside the repo it must be ignored by git, so it stays local.
	_, err := SetTracker(h.repo, "sprint.md")
	if err == nil || !strings.Contains(err.Error(), "doesn't ignore it") {
		t.Fatalf("an unignored sprint file in the repo was accepted: %v", err)
	}
	_, err = SetTracker(h.repo, ".env")
	if err == nil {
		t.Fatal("a secret file was accepted as the sprint file")
	}
	// Ignored by a pattern but already tracked: it would end up in commits.
	tracked := filepath.Join(h.repo, "coverage", "TRACKED.md")
	write(t, tracked, "x\n")
	run(t, h.repo, "git", "add", "-f", "coverage/TRACKED.md")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: tracked")
	if _, err := SetTracker(h.repo, tracked); err == nil {
		t.Fatal("a tracked file was accepted as the sprint file")
	}
	must(t, os.MkdirAll(filepath.Join(h.repo, "coverage"), 0o755))
	sprint := filepath.Join(h.repo, "coverage", "sprint.md") // .gitignore has coverage/
	write(t, sprint, "- [ ] PROJ-1\n")
	got, err := SetTracker(h.repo, "coverage/sprint.md")
	must(t, err)
	if got != sprint || TrackerPath(h.repo) != sprint {
		t.Fatalf("tracker = %q / %q, want %q", got, TrackerPath(h.repo), sprint)
	}

	// Setting it asks; reading the standup doesn't.
	h.expect(h.bash("plumbline standup tracker --set coverage/sprint.md"), "ask", "sprint file")
	h.expect(h.bash("plumbline standup --date 2026-11-16"), "allow", "")
	h.expect(h.bash("plumbline standup tracker"), "allow", "")

	// Terminal writes to it ask too.
	h.expect(h.bash("echo '- done' >> coverage/sprint.md"), "ask", "sprint file")

	// No task: edits to the sprint file ask, other files stay refused.
	h.expect(h.pre("Edit", map[string]any{"file_path": sprint}), "ask", "sprint file")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "deny", "no task")

	// During a task, a sprint file edit isn't the task's change.
	b := buildingTask(t)
	bs := filepath.Join(b.repo, "coverage", "sprint.md")
	must(t, os.MkdirAll(filepath.Dir(bs), 0o755))
	write(t, bs, "x\n")
	_, err = SetTracker(b.repo, bs)
	must(t, err)
	b.expect(b.pre("Edit", map[string]any{"file_path": bs}), "ask", "sprint file")
	b.expect(b.bash("echo x >> "+bs), "ask", "sprint file")
	b.post("Edit", map[string]any{"file_path": bs}, nil)
	if _, ok := b.task().OutsidePlan["coverage/sprint.md"]; ok {
		t.Fatal("a sprint file edit was counted as the task's change")
	}
}
