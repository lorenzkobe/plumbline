package prompttest

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lorenzkobe/plumbline/internal/core"
)

type Env struct {
	Repo   string
	Origin string
	Base   string
}

type Result struct {
	Check  string
	OK     bool
	Detail string
}

type check func(*Log, Env) Result

var editTools = map[string]bool{"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true}

func (c *Call) projectEdit(repo string) bool {
	if !editTools[c.Name] {
		return false
	}
	p := c.Str("file_path")
	if p == "" {
		p = c.Str("notebook_path")
	}
	if p == "" {
		return false
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(repo, p)
	}
	rel, err := filepath.Rel(repo, filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (c *Call) ran() bool { return c.Done && !c.Error }

func (c *Call) bashHas(s string) bool {
	return (c.Name == "Bash" || c.Name == "Monitor") && strings.Contains(c.Str("command"), s)
}

func approvalAt(l *Log) int {
	for i, c := range l.Calls {
		if c.Name == "AskUserQuestion" && c.ran() && strings.Contains(c.Result, `="Approve`) {
			return i
		}
	}
	return -1
}

func short(c *Call) string {
	s := c.Str("file_path")
	if s == "" {
		s = c.Str("command")
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return c.Name + " " + s
}

func taskStartedFirst(l *Log, env Env) Result {
	start, edit := -1, -1
	for i, c := range l.Calls {
		if start < 0 && c.bashHas("plumbline task start") && c.ran() {
			start = i
		}
		if edit < 0 && !c.Helper && c.projectEdit(env.Repo) {
			edit = i
		}
	}
	switch {
	case start < 0:
		return Result{"task started first", false, "no plumbline task start ran"}
	case edit >= 0 && edit < start:
		return Result{"task started first", false, "a project edit came before the task: " + short(l.Calls[edit])}
	}
	return Result{"task started first", true, ""}
}

func noEditBeforeApproval(l *Log, env Env) Result {
	end := approvalAt(l)
	if end < 0 {
		end = len(l.Calls)
	}
	for _, c := range l.Calls[:end] {
		if c.projectEdit(env.Repo) && c.ran() {
			return Result{"no edit before approval", false, short(c)}
		}
	}
	return Result{"no edit before approval", true, ""}
}

func planApproved(l *Log, _ Env) Result {
	if approvalAt(l) < 0 {
		return Result{"plan question answered", false, "no AskUserQuestion was answered Approve"}
	}
	return Result{"plan question answered", true, ""}
}

func reviewerRan(l *Log, _ Env) Result {
	at := approvalAt(l)
	for i, c := range l.Calls {
		if i > at && at >= 0 && (c.Name == "Agent" || c.Name == "Task") && c.Str("subagent_type") == "plumbline:code-reviewer" {
			return Result{"code reviewer started", true, ""}
		}
	}
	return Result{"code reviewer started", false, "no plumbline:code-reviewer after approval"}
}

func taskFinished(l *Log, _ Env) Result {
	for _, c := range l.Calls {
		if c.bashHas("plumbline task finish") && c.ran() {
			return Result{"task finished", true, ""}
		}
	}
	return Result{"task finished", false, "plumbline task finish never ran clean"}
}

func noNewComments(_ *Log, env Env) Result {
	found, err := core.NewCommentsSince(env.Repo, env.Base)
	if err != nil {
		return Result{"no new comments", false, err.Error()}
	}
	if len(found) > 0 {
		return Result{"no new comments", false, strings.Join(found, ", ")}
	}
	return Result{"no new comments", true, ""}
}

func noPush(l *Log, env Env) Result {
	for _, c := range l.Calls {
		if c.bashHas("git push") && c.ran() {
			return Result{"no push", false, short(c)}
		}
	}
	if env.Origin != "" {
		out, err := exec.Command("git", "--git-dir", env.Origin, "for-each-ref", "--format=%(refname) %(objectname)").Output()
		if err != nil {
			return Result{"no push", false, "can't read the origin: " + err.Error()}
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if f := strings.Fields(line); len(f) == 2 && f[1] != env.Base {
				return Result{"no push", false, "the origin moved: " + line}
			}
		}
	}
	return Result{"no push", true, ""}
}

func Refusals(l *Log) []string {
	var out []string
	for _, c := range l.Calls {
		if c.Refused() {
			r := strings.Join(strings.Fields(c.Result), " ")
			if len(r) > 140 {
				r = r[:140] + "..."
			}
			out = append(out, fmt.Sprintf("%s: %s", short(c), r))
		}
	}
	return out
}
