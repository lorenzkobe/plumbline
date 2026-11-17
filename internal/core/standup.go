package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Standup notes: what you worked on during one day, starting from the repo
// you're in. It takes your own work only (your tasks, your commits by your
// git email) and follows a ticket across the other repos it touched, so a
// ticket that spans a backend and a frontend shows as one item. Work in
// repos with nothing to do with this one (a personal project the same day)
// stays out.

type StandupTask struct {
	ID, Title, Ticket, Repo, Phase string
}

type StandupCommit struct {
	Repo, Hash, Subject string
}

type StandupItem struct {
	Ticket  string // "" for a task without a ticket
	Title   string // the task title when there's no ticket
	Tasks   []StandupTask
	Commits []StandupCommit
	Read    map[string]int // repo name -> files opened that day (no changes)
}

type StandupReport struct {
	Day     time.Time
	Anchor  string // repo name the standup starts from
	Emails  []string
	Items   []*StandupItem
	Loose   []StandupCommit // your commits that day in this repo with no ticket and no task
	Open    []StandupTask   // still open: what's next
	Tickets []string
}

// AllTasks loads every task Plumbline holds on this laptop, across projects.
// A task whose project folder moved, or whose state was tampered with, is
// skipped.
func AllTasks() []*Task {
	var out []*Task
	projects, _ := os.ReadDir(filepath.Join(DataDir(), "projects"))
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		tasks, _ := os.ReadDir(filepath.Join(DataDir(), "projects", p.Name(), "tasks"))
		for _, d := range tasks {
			if !d.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(DataDir(), "projects", p.Name(), "tasks", d.Name(), "state.json"))
			if err != nil {
				continue
			}
			if rest, ok := strings.CutPrefix(string(b), sealLine); ok {
				_, data, _ := strings.Cut(rest, "\n")
				b = []byte(data)
			}
			// Only the root is read here; LoadTask checks the seal.
			var peek struct {
				Root string `json:"root"`
			}
			_ = json.Unmarshal(b, &peek)
			root := peek.Root
			if root == "" || ProjectKey(root) != p.Name() {
				continue
			}
			if t, err := LoadTask(root, d.Name()); err == nil {
				out = append(out, t)
			}
		}
	}
	return out
}

// activeOn: the task did something during [from, to): started, a logged
// event, a file read, a helper run, a checkpoint, a code change.
func activeOn(t *Task, from, to time.Time, events map[string][]time.Time) bool {
	in := func(at time.Time) bool { return !at.IsZero() && !at.Before(from) && at.Before(to) }
	if in(t.Created) || in(t.Checkpoint1At) || in(t.Checkpoint2At) || in(t.LastCodeChange) || in(t.DirectionAt) {
		return true
	}
	for _, r := range t.Reads {
		if in(r.At) {
			return true
		}
	}
	for _, h := range t.Helpers {
		if in(h.Started) || in(h.Finished) {
			return true
		}
	}
	for _, at := range events[t.ID] {
		if in(at) {
			return true
		}
	}
	return false
}

// eventTimes: when each task logged something, from the month logs that
// cover [from, to).
func eventTimes(from, to time.Time) map[string][]time.Time {
	out := map[string][]time.Time{}
	seen := map[string]bool{}
	for d := from.UTC(); d.Before(to.UTC().Add(time.Second)); d = d.AddDate(0, 0, 1) {
		month := d.Format("2006-01")
		if seen[month] {
			continue
		}
		seen[month] = true
		files, _ := filepath.Glob(filepath.Join(logsDir(), month, "*.jsonl"))
		for _, f := range files {
			for _, e := range readEvents(f) {
				if e.Task != "" && !e.At.Before(from) && e.At.Before(to) {
					out[e.Task] = append(out[e.Task], e.At)
				}
			}
		}
	}
	return out
}

// myCommits: commits authored by email in repo during [from, to), on any
// branch, merges left out.
func myCommits(repo, email string, from, to time.Time) []StandupCommit {
	if email == "" {
		return nil
	}
	// --since stops at the first older commit, which a rebase or a
	// cherry-pick can put on top; --since-as-filter (git 2.37+) looks at
	// all of them. Older git: the last 2000 commits, filtered here.
	// Branches, remotes and tags, not --all: a stash is a commit too.
	args := []string{"log", "--branches", "--remotes", "--tags", "HEAD", "--no-merges", "--fixed-strings", "--author=" + email, "--format=%h%x1f%aI%x1f%ae%x1f%s"}
	out, err := gitOut(repo, append(args, "--since-as-filter="+from.Add(-24*time.Hour).Format(time.RFC3339))...)
	if err != nil {
		if out, err = gitOut(repo, append(args, "--max-count=2000")...); err != nil {
			return nil
		}
	}
	var cs []StandupCommit
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.SplitN(line, "\x1f", 4)
		if len(f) != 4 || !strings.EqualFold(f[2], email) {
			continue // --author matches part of a name or email; keep exact matches
		}
		at, err := time.Parse(time.RFC3339, f[1])
		if err != nil || at.Before(from) || !at.Before(to) {
			continue
		}
		cs = append(cs, StandupCommit{Repo: filepath.Base(repo), Hash: f[0], Subject: f[3]})
	}
	return cs
}

func gitEmail(repo string) string {
	out, err := gitOut(repo, "config", "user.email")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// siblingRepos: git repos next to root (other services in the same parent
// folder), at most 200 looked at.
func siblingRepos(root string) []string {
	parent := filepath.Dir(root)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var out []string
	for i, e := range entries {
		if i >= 200 {
			break
		}
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(parent, e.Name())
		if _, err := os.Stat(filepath.Join(p, ".git")); err == nil && p != root {
			out = append(out, p)
		}
	}
	return out
}

// Standup builds the notes for one local day, starting from anchor (a path
// in the repo you're in).
func Standup(anchor string, day time.Time) (*StandupReport, error) {
	root := RepoRoot(anchor)
	if root == "" {
		return nil, errors.New("not inside a git repository")
	}
	root = ProjectRoot(root)
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	to := from.AddDate(0, 0, 1)
	setup, _, _ := LoadSetup(root)
	var reTicket *regexp.Regexp
	if setup != nil && setup.Kind == "company" {
		reTicket, _ = regexp.Compile(`\b(` + setup.TicketPattern() + `)\b`)
	}
	rep := &StandupReport{Day: from, Anchor: filepath.Base(root)}

	events := eventTimes(from, to)
	all := AllTasks()
	byProject := map[string][]*Task{}
	for _, t := range all {
		p := ProjectRoot(t.Root)
		byProject[p] = append(byProject[p], t)
	}

	items := map[string]*StandupItem{}
	item := func(ticket, title string) *StandupItem {
		key := "ticket:" + ticket
		if ticket == "" {
			key = "task:" + title
		}
		if it, ok := items[key]; ok {
			return it
		}
		it := &StandupItem{Ticket: ticket, Title: title, Read: map[string]int{}}
		items[key] = it
		return it
	}
	tickets := map[string]bool{}
	addTask := func(t *Task) *StandupItem {
		it := item(t.Ticket, t.Title)
		it.Tasks = append(it.Tasks, StandupTask{ID: t.ID, Title: t.Title, Ticket: t.Ticket, Repo: filepath.Base(ProjectRoot(t.Root)), Phase: t.Phase})
		return it
	}
	// Files a task opened that day outside its own repo: "read in <repo>".
	noteReads := func(t *Task, it *StandupItem) {
		own := ProjectRoot(t.Root)
		for p, r := range t.Reads {
			if r.At.Before(from) || !r.At.Before(to) || IsInside(p, own) || isInsideFold(p, DataDir()) {
				continue
			}
			if rr := RepoRoot(existingDir(p)); rr != "" {
				it.Read[filepath.Base(ProjectRoot(rr))]++
			}
		}
	}

	// 1. This repo: your tasks active that day, and your commits.
	email := gitEmail(root)
	rep.Emails = appendUniq(rep.Emails, email)
	var included []*Task
	for _, t := range byProject[root] {
		if activeOn(t, from, to, events) {
			included = append(included, t)
			if t.Ticket != "" {
				tickets[t.Ticket] = true
			}
		}
	}
	var anchorCommits []StandupCommit
	for _, c := range myCommits(root, email, from, to) {
		anchorCommits = append(anchorCommits, c)
		if reTicket != nil {
			for _, m := range reTicket.FindAllString(c.Subject, -1) {
				tickets[m] = true
			}
		}
	}

	// 2. Other repos: only what carries one of those tickets.
	candidates := map[string]bool{}
	for p := range byProject {
		candidates[p] = true
	}
	for _, p := range siblingRepos(root) {
		candidates[ProjectRoot(p)] = true
	}
	if setup != nil && setup.FrontendRepo != "" {
		fr := setup.FrontendRepo
		if !filepath.IsAbs(fr) {
			fr = filepath.Join(root, fr)
		}
		if r := RepoRoot(fr); r != "" {
			candidates[ProjectRoot(r)] = true
		}
	}
	delete(candidates, root)
	var others []string
	for p := range candidates {
		others = append(others, p)
	}
	sort.Strings(others)
	// Only ids that look like tickets are followed into other repos: a
	// free-text ticket ("42") would match unrelated commits and tasks.
	pattern := `[A-Z][A-Z0-9]+-\d+`
	if setup != nil {
		pattern = setup.TicketPattern()
	}
	follow := map[string]bool{}
	if reID, err := regexp.Compile(`^(` + pattern + `)$`); err == nil {
		for tk := range tickets {
			if reID.MatchString(tk) {
				follow[tk] = true
			}
		}
	}
	var otherCommits []StandupCommit
	if len(follow) > 0 {
		for _, p := range others {
			if _, err := os.Stat(p); err != nil {
				continue
			}
			for _, t := range byProject[p] {
				if t.Ticket != "" && follow[t.Ticket] && activeOn(t, from, to, events) {
					included = append(included, t)
				}
			}
			e := gitEmail(p)
			for _, c := range myCommits(p, e, from, to) {
				for tk := range follow {
					if mentionsTicket(c.Subject, tk) {
						otherCommits = append(otherCommits, c)
						rep.Emails = appendUniq(rep.Emails, e)
						break
					}
				}
			}
		}
	}

	for _, t := range included {
		noteReads(t, addTask(t))
	}
	for _, c := range append(anchorCommits, otherCommits...) {
		placed := false
		for _, tk := range sortedKeys(tickets) {
			if mentionsTicket(c.Subject, tk) {
				it := item(tk, "")
				it.Commits = append(it.Commits, c)
				placed = true
				break
			}
		}
		if !placed {
			rep.Loose = append(rep.Loose, c)
		}
	}

	// 3. What's next: tasks still open here, and open tasks on those tickets.
	for p, ts := range byProject {
		for _, t := range ts {
			if !t.Open() {
				continue
			}
			if p == root || (t.Ticket != "" && follow[t.Ticket]) {
				rep.Open = append(rep.Open, StandupTask{ID: t.ID, Title: t.Title, Ticket: t.Ticket, Repo: filepath.Base(p), Phase: t.Phase})
			}
		}
	}
	sort.Slice(rep.Open, func(i, j int) bool { return rep.Open[i].ID < rep.Open[j].ID })

	for _, it := range items {
		rep.Items = append(rep.Items, it)
	}
	sort.Slice(rep.Items, func(i, j int) bool {
		a, b := rep.Items[i], rep.Items[j]
		if (a.Ticket == "") != (b.Ticket == "") {
			return a.Ticket != ""
		}
		return a.Ticket+a.Title < b.Ticket+b.Title
	})
	for tk := range tickets {
		rep.Tickets = append(rep.Tickets, tk)
	}
	sort.Strings(rep.Tickets)
	return rep, nil
}

// mentionsTicket: the ticket id as a whole word (PROJ-1 isn't PROJ-12).
func mentionsTicket(text, ticket string) bool { return mentionsWord(text, ticket) }

// mentionsWord: phrase appears in text as whole words, ignoring case
// ("Direction A" isn't "direction and", PROJ-1 isn't PROJ-12).
func mentionsWord(text, phrase string) bool {
	if strings.TrimSpace(phrase) == "" {
		return false
	}
	re, err := regexp.Compile(`(?i)(^|[^\pL\pN])` + regexp.QuoteMeta(phrase) + `([^\pL\pN]|$)`)
	return err == nil && re.MatchString(text)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func appendUniq(s []string, v string) []string {
	if v == "" || contains(s, v) {
		return s
	}
	return append(s, v)
}

// String is the plain listing the standup skill turns into notes.
func (r *StandupReport) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Standup for %s, from %s. Your commits only (%s).\n", r.Day.Format("Mon 2006-01-02"), r.Anchor, orNoneDash(strings.Join(r.Emails, ", ")))
	b.WriteString("\nWorked on:\n")
	if len(r.Items) == 0 && len(r.Loose) == 0 {
		b.WriteString("- nothing recorded by Plumbline or git for that day\n")
	}
	for _, it := range r.Items {
		if it.Ticket != "" {
			fmt.Fprintf(&b, "- %s\n", it.Ticket)
		} else {
			fmt.Fprintf(&b, "- %q (no ticket)\n", it.Title)
		}
		for _, t := range it.Tasks {
			fmt.Fprintf(&b, "  - task %s %q in %s: %s\n", t.ID, t.Title, t.Repo, t.Phase)
		}
		for _, c := range it.Commits {
			fmt.Fprintf(&b, "  - commit in %s: %s %s\n", c.Repo, c.Hash, c.Subject)
		}
		var repos []string
		for repo := range it.Read {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		for _, repo := range repos {
			fmt.Fprintf(&b, "  - read in %s: %d files\n", repo, it.Read[repo])
		}
	}
	if len(r.Loose) > 0 {
		b.WriteString("- commits with no ticket or task:\n")
		for _, c := range r.Loose {
			fmt.Fprintf(&b, "  - %s: %s %s\n", c.Repo, c.Hash, c.Subject)
		}
	}
	b.WriteString("\nStill open (what's next):\n")
	if len(r.Open) == 0 {
		b.WriteString("- none\n")
	}
	for _, t := range r.Open {
		tk := ""
		if t.Ticket != "" {
			tk = t.Ticket + " "
		}
		fmt.Fprintf(&b, "- %s%q in %s: %s step\n", tk, t.Title, t.Repo, t.Phase)
	}
	fmt.Fprintf(&b, "\nTickets: %s\n", orNoneDash(strings.Join(r.Tickets, ", ")))
	fmt.Fprintf(&b, "Repos with your work that day: %s\n", orNoneDash(strings.Join(r.repos(), ", ")))
	return b.String()
}

// repos: the repos holding a task, a commit or a read in the report.
func (r *StandupReport) repos() []string {
	var out []string
	for _, it := range r.Items {
		for _, t := range it.Tasks {
			out = appendUniq(out, t.Repo)
		}
		for _, c := range it.Commits {
			out = appendUniq(out, c.Repo)
		}
		for repo := range it.Read {
			out = appendUniq(out, repo)
		}
	}
	for _, c := range r.Loose {
		out = appendUniq(out, c.Repo)
	}
	sort.Strings(out)
	return out
}

func orNoneDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// The sprint file: a local tracker you keep per project. Its path is
// remembered in Plumbline's data folder; edits to it always ask you.
func trackerFile(root string) string { return filepath.Join(ProjectDir(root), "tracker") }

// TrackerPath is the project's sprint file, or "" when none is set.
func TrackerPath(root string) string {
	if root == "" {
		return ""
	}
	b, err := os.ReadFile(trackerFile(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetTracker remembers the sprint file. One inside a repo must be ignored
// by git, so it never ends up in a commit or the proof's fingerprint.
func SetTracker(root, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("give the sprint file's path")
	}
	abs := Abs(path, root)
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		return "", errors.New("that's a folder; give the sprint file itself")
	}
	if IsSecretFile(abs) {
		return "", errors.New("that's a secret file, not a sprint file")
	}
	if r := RepoRoot(existingDir(abs)); r != "" {
		// Without --no-index, a file git already tracks isn't "ignored" even
		// when a pattern matches it.
		if _, err := gitOut(r, "check-ignore", "-q", abs); err != nil {
			return "", fmt.Errorf("%s is inside the repo %s but git doesn't ignore it (or already tracks it). Keep it local: add it to .git/info/exclude or .gitignore (the user does this), or keep it outside the repo", abs, r)
		}
	}
	return abs, writeFileAtomic(trackerFile(root), []byte(abs+"\n"))
}
