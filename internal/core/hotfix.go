package core

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The hotfix flow (W1): production is broken and the smallest fix that
// stops the damage goes out first. Only the user starts one, by typing
// /plumbline:hotfix; Claude has no command that makes one. It's a small task
// with one combined checkpoint, whose plan says what the root cause is (or
// that this fix is it). Its first commit needs the related tests, lint and
// build instead of the full suite; after it, the full suite and every
// reviewer come next, and the finish check holds the task until they ran.
const KindHotfix = "hotfix"

func (t *Task) Hotfix() bool { return t.Kind == KindHotfix }

// HotfixCommitted: the hotfix is committed, so the full suite and the
// reviews are what's next.
func (t *Task) HotfixCommitted() bool { return t.Hotfix() && t.Commits > 0 }

// hotfixNote is the reminder the task line carries for a hotfix.
func hotfixNote(t *Task) string {
	if !t.Hotfix() {
		return ""
	}
	if t.HotfixCommitted() {
		return ". Hotfix committed: run the full suite and every reviewer now, before anything else"
	}
	return ". Hotfix: one checkpoint, the smallest fix that stops the damage, a \"## Root cause\" in plan.md; its first commit needs the related tests, lint and build"
}

// NewTask starts the task t describes (title, ticket, size, kind, and the
// session when a hook starts it) in root, and saves it once: the user's
// unsaved work and the commit it starts from are noted, and its notes
// folder made.
func NewTask(root string, t *Task) error {
	pre, _ := DirtyFiles(root)
	t.ID, t.Root, t.Branch, t.Phase = newID(), root, CurrentBranch(root), PhaseUnderstanding
	t.Created, t.Preexisting, t.StartCommit = Now(), pre, HeadCommit(root)
	t.StartTips = BranchTips(root)
	if err := os.MkdirAll(TaskDocsDir(root, t.ID), 0o700); err != nil {
		return err
	}
	if err := SaveTask(t); err != nil {
		return err
	}
	LogEvent(Event{Type: "task", Task: t.ID, Size: t.Size, Project: ProjectKey(root), Rule: "start"})
	return nil
}

// DockerReady: the Docker daemon answers (PLUMBLINE_SKIP_DOCKER=1 is for
// Plumbline's own tests).
func DockerReady() bool {
	if os.Getenv("PLUMBLINE_SKIP_DOCKER") == "1" {
		return true
	}
	return DockerUp()
}

// userHotfix is /plumbline:hotfix: it starts the hotfix task and binds it to
// this session. Typing the command is the user's go-ahead.
func userHotfix(c *hookCtx, args string) string {
	what := strings.TrimSpace(args)
	switch {
	case c.root == "":
		return "not inside a git repo, so there's no project to fix here."
	case c.in.SessionID == "":
		return "Claude Code didn't say which session this is, so the hotfix task couldn't be bound to it. Type /plumbline:hotfix again."
	case c.paused:
		return "Plumbline is paused in this project, so a hotfix task would have no gates. Type /plumbline:resume, then /plumbline:hotfix again."
	case c.setupErr != nil:
		return "this project has no Plumbline setup yet, so its test commands aren't known. Run /plumbline:setup first."
	case !c.setupOK:
		return "the project setup changed since you confirmed it, so its commands won't run. Re-confirm it with /plumbline:setup, then type /plumbline:hotfix again."
	case c.taskErr != nil:
		return "couldn't read this session's task (" + c.taskErr.Error() + "), so no hotfix was started."
	case c.task != nil:
		return fmt.Sprintf("this session already has task %s (\"%s\", %s step) open, so no hotfix was started. Close it first with /plumbline:cancel %s (its changes stay in the working tree), or start the hotfix in its own worktree (a new session with claude -w).", c.task.ID, c.task.Title, c.task.Phase, c.task.ID)
	case what == "":
		return "say what's broken: /plumbline:hotfix <what's broken, or the ticket id>."
	}
	if o := c.otherSessionTask(); o != nil {
		return fmt.Sprintf("task %s (\"%s\", %s step) is open in this checkout for another Claude session, and two tasks in one checkout would test each other's half-done code. Start the hotfix in its own worktree (a new session with claude -w), or close that task with /plumbline:cancel %s.", o.ID, o.Title, o.Phase, o.ID)
	}
	if !DockerReady() {
		return "the Docker app isn't running. It's required (SocratiCode and the test database). Start it, then type /plumbline:hotfix again."
	}
	title, ticket := hotfixTitle(what, c.setup)
	// SizeByUser: the user chose the fast path, so a risky file keeps it
	// small; files that decide which checks run still aren't.
	t := &Task{Title: title, Ticket: ticket, Size: "small", SizeWhy: "hotfix, started by the user", SizeByUser: true, Kind: KindHotfix, SessionID: c.in.SessionID}
	if err := NewTask(c.root, t); err != nil {
		return "couldn't start the hotfix task: " + err.Error() + "."
	}
	if err := BindSession(c.in.SessionID, t); err != nil {
		t.Phase = PhaseCancelled // unbound, it would only block the checkout
		_ = SaveTask(t)
		return "couldn't bind the hotfix task to this session: " + err.Error() + ". Type /plumbline:hotfix again."
	}
	RefreshSnapshot(c.in.SessionID, c.root)
	LogEvent(Event{Type: "task", Task: t.ID, Size: t.Size, Project: c.projectKey(), Rule: "hotfix-start"})
	tk := ""
	if ticket != "" {
		tk = ", ticket " + ticket
	}
	return fmt.Sprintf("hotfix task %s \"%s\" started by the user (small%s, branch %s). Notes folder: %s. One combined checkpoint; plan.md needs \"## Root cause\". The first commit needs the related tests, lint and build; the full suite and every reviewer run right after it, before anything else.", t.ID, t.Title, tk, t.Branch, TaskDocsDir(t.Root, t.ID))
}

const hotfixTitleMax = 100

// hotfixTitle: the first line of what the user typed, as the title, and
// the first word shaped like the project's ticket ids, as the ticket.
func hotfixTitle(what string, s *Setup) (title, ticket string) {
	first, _, _ := strings.Cut(what, "\n")
	title = strings.Join(strings.Fields(first), " ")
	if utf8.RuneCountInString(title) > hotfixTitleMax {
		title = string([]rune(title)[:hotfixTitleMax-3]) + "..."
	}
	re, err := regexp.Compile(`^(` + s.TicketPattern() + `)$`)
	if err != nil {
		return title, ""
	}
	for _, w := range strings.Fields(what) {
		if w = strings.Trim(w, ".,:;()[]\"'"); re.MatchString(w) {
			return title, w
		}
	}
	return title, ""
}

// hotfixProblems: a hotfix's plan names the root cause, so the quick fix
// stays apart from the real one.
func hotfixProblems(t *Task, plan string) []string {
	if !t.Hotfix() || len(Section(plan, "root cause")) > 0 {
		return nil
	}
	return []string{`a hotfix's plan.md needs "## Root cause": what really broke (file:line), and either "this fix is the root-cause fix" or the follow-up task that fixes it (a normal task, after the hotfix)`}
}

// hotfixReportMissing: the report repeats the root cause, so the follow-up
// isn't lost once the damage is stopped.
func hotfixReportMissing(t *Task, report string) []string {
	if !t.Hotfix() || len(Section(report, "root cause")) > 0 {
		return nil
	}
	return []string{`report: a hotfix's report needs "## Root cause": what really broke, and either "fixed here" or the follow-up task to open (also under "For you")`}
}

// movesHead: a git checkout, switch or pull in this repo, which can move
// HEAD to another commit.
func movesHead(segs []Segment, root string) bool {
	return gitSubIn(segs, root, "checkout", "switch", "pull")
}

// commitsIn: a git commit in this repo.
func commitsIn(segs []Segment, root string) bool { return gitSubIn(segs, root, "commit") }

func gitSubIn(segs []Segment, root string, subs ...string) bool {
	for _, s := range segs {
		sub, _ := GitSub(s)
		if !contains(subs, sub) {
			continue
		}
		if d := gitDir(s); d != "" && RepoRoot(d) != root {
			continue
		}
		return true
	}
	return false
}

// BranchTips: the commits the repo's local branches point to, recorded when
// a task starts.
func BranchTips(root string) []string {
	out, err := gitOut(root, "for-each-ref", "--format=%(objectname)", "refs/heads")
	if err != nil {
		return nil
	}
	return uniq(strings.Fields(out))
}

// followable: a commit the start commit may move to holds nothing made
// during the task. It's on a remote-tracking branch (teammates' commits, a
// fetch or pull brought them), or was already part of a local branch when
// the task started (checking out local main). A stash, a branch made from
// one, or a commit made during the task is neither, so the start commit
// stays and the review covers more, never less.
func followable(t *Task, commit string) bool {
	if out, err := gitOut(t.Root, "for-each-ref", "--contains", commit, "--count=1", "--format=%(refname)", "refs/remotes"); err == nil && strings.TrimSpace(out) != "" {
		return true
	}
	for _, tip := range t.StartTips {
		if tip == commit {
			return true
		}
		if _, err := gitOut(t.Root, "merge-base", "--is-ancestor", commit, tip); err == nil {
			return true
		}
	}
	return false
}

// noteHeadBefore keeps the commit HEAD stands on before the lead's checkout,
// switch, pull or commit, so the post hook can tell whether that command
// moved it.
func (c *hookCtx) noteHeadBefore() {
	t := c.task
	if t == nil || c.in.AgentID != "" || c.in.ToolName != "Bash" || !t.Open() {
		return
	}
	segs := ParseShell(c.in.Str("command"), c.in.Cwd)
	if !movesHead(segs, t.Root) && !commitsIn(segs, t.Root) {
		return
	}
	t.HeadBefore = HeadCommit(t.Root)
	_ = SaveTask(t)
}

// followHead: until the task's first commit, the commit it started from
// follows a checkout, switch or pull that moved HEAD to a followable commit
// (a hotfix branches from the production branch; the task workflow pulls
// the base branch after the task starts), so its diff holds only the task's
// own changes. A commit the user makes in their own terminal doesn't move
// it. failed: the line exited non-zero, so the success path didn't count a
// commit in it; a HEAD that moved under a git commit counts here.
func (c *hookCtx) followHead(segs []Segment, failed bool) {
	t := c.task
	if t == nil || c.in.AgentID != "" || t.HeadBefore == "" {
		return
	}
	moves, commits := movesHead(segs, t.Root), commitsIn(segs, t.Root)
	if !moves && !commits {
		return // another call running beside it: leave its note
	}
	before := t.HeadBefore
	t.HeadBefore = ""
	now := HeadCommit(t.Root)
	switch {
	case now == "" || now == before:
	case commits && failed:
		t.Commits++
		c.log("commit", "commit", "in a line that failed")
	case commits:
		// counted by the success path
	case t.Commits == 0 && followable(t, now):
		c.log("task", "start-commit-moved", abbrev(t.StartCommit)+" -> "+abbrev(now))
		t.StartCommit = now
	}
	_ = SaveTask(t)
}

func abbrev(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}
