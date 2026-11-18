package core

import (
	"context"
	_ "embed"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

//go:embed rules.md
var coreRules string

func CoreRules() string { return coreRules }

// DockerUp reports whether the Docker daemon answers within 3 seconds.
func DockerUp() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}").Run() == nil
}

func taskLine(t *Task) string {
	return fmt.Sprintf("task %s \"%s\" (%s, %s step, branch %s). Notes folder: %s", t.ID, t.Title, t.Size, t.Phase, t.Branch, TaskDocsDir(t.Root, t.ID))
}

// SessionStart loads the rules, the project's state and leftover tasks.
// On compact it loads them again, since that's when drift usually starts.
func SessionStart(in *HookInput) Output {
	Cleanup()
	c := newCtx(in)
	defer c.close()
	var b strings.Builder
	b.WriteString(coreRules)
	b.WriteString("\n## Right now\n")
	failed := FailureNote()
	if failed != "" {
		b.WriteString("- " + failed + " Tell the user this first, with the report's path.\n")
	}
	out := func() Output {
		o := Context("SessionStart", b.String())
		if failed != "" {
			o["systemMessage"] = msg(failed)
		}
		return o
	}
	if c.root == "" {
		b.WriteString("- Not inside a git repo: Question mode only. Code changes need a project with git.\n")
		return out()
	}
	if c.paused {
		b.WriteString("- Plumbline is PAUSED in this project by the user. Task gates are off; safety rules (production, secrets, push, Jira posts) stay on. Only the user can resume (/plumbline:resume).\n")
	}
	switch {
	case c.setupErr != nil:
		b.WriteString("- This project has no Plumbline setup yet. Before the first task, suggest /plumbline:setup.\n")
	case !c.setupOK:
		b.WriteString("- The project setup changed since the user confirmed it, so saved commands won't run. Ask the user to re-confirm with /plumbline:setup.\n")
	default:
		fmt.Fprintf(&b, "- Project setup: %s project, base branch %s.\n", c.setup.Kind, orDash(c.setup.BaseBranch))
	}
	if in.Source == "startup" || in.Source == "resume" || in.Source == "" {
		if DockerUp() {
			b.WriteString("- Docker: running.\n")
		} else {
			b.WriteString("- Docker: NOT running. It's required (SocratiCode and the test database). Try to start it once, then ask the user.\n")
		}
	}
	if c.task != nil {
		b.WriteString("- This session's " + taskLine(c.task) + "\n")
	}
	var left []string
	for _, t := range ListTasks(c.root) {
		if t.Phase == "tampered" {
			left = append(left, fmt.Sprintf("%s (state changed outside Plumbline; the user should drop it)", t.ID))
			continue
		}
		if t.Open() && (c.task == nil || t.ID != c.task.ID) && t.SessionID != in.SessionID {
			left = append(left, fmt.Sprintf("%s \"%s\" (%s step, branch %s)", t.ID, t.Title, t.Phase, t.Branch))
		}
	}
	if len(left) > 0 {
		b.WriteString("- Open tasks of other sessions (earlier ones, or another window still working on them): " + strings.Join(left, "; ") + ". Ask the user, in one message, whether to continue each here (plumbline task adopt <id>) or close it (only they can, with /plumbline:cancel <id>), unless another window is still on it. A new task can't start in a checkout while another session's task is open there: it needs its own worktree (a new session with claude -w).\n")
	}
	RefreshSnapshot(in.SessionID, c.root)
	return out()
}

func orDash(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}

// UserPromptSubmit adds a one-line reminder to every message you send.
func UserPromptSubmit(in *HookInput) Output {
	_ = SetJiraAsked(in.SessionID, UserAsksJira(in.Prompt))
	ResetBlocks(in.SessionID) // a new message from you: earlier refusals no longer count as "in a row"
	c := newCtx(in)
	defer c.close()
	if c.task != nil && len(c.task.Sendbacks) > 0 {
		c.task.Sendbacks = nil // a new message from you resets the send-back count
		_ = SaveTask(c.task)
	}
	var line string
	switch {
	case c.root == "":
		line = "Plumbline: Question mode (not a git repo). Answer from the real code, with file:line."
	case c.paused:
		line = "Plumbline is paused here by the user. Safety rules stay on."
	case c.task != nil:
		line = "Plumbline: " + taskLine(c.task) + ". Stay in scope; claims need file:line or a run; no done without proof."
	default:
		line = "Plumbline: no task active. Pick the mode (Question / Explore / Task) and say it in one line. If this asks for a change, ask before starting the task workflow."
	}
	return Context("UserPromptSubmit", line)
}

// UserPromptExpansion runs the user-only commands. These fire only when you
// type the command yourself, so Claude can't pause, resume, cancel, mark a
// block wrong or keep a risky change small.
func UserPromptExpansion(in *HookInput) Output {
	name := strings.ToLower(strings.TrimPrefix(in.CommandName, "/"))
	switch {
	case strings.HasPrefix(name, "plumbline:"):
		name = strings.TrimPrefix(name, "plumbline:")
	case in.CommandSource == "plugin" && strings.HasPrefix(strings.ToLower(in.Prompt), "/plumbline:"):
	default:
		return nil
	}
	c := newCtx(in)
	defer c.close()
	say := func(s string) Output { return Context("UserPromptExpansion", msg(s)) }
	switch name {
	case "pause", "resume":
		if c.root == "" {
			return say("not inside a git repo, so there is nothing to " + name + ".")
		}
		if err := SetPaused(c.root, name == "pause"); err != nil {
			return say("couldn't " + name + ": " + err.Error())
		}
		c.log(name, name, "")
		if name == "pause" {
			return say("paused in this project by the user. Task gates and the finish check are off. Safety rules stay on: no production changes, no secrets printed, no pushes, no Jira posts without the user's OK.")
		}
		return say("resumed in this project. All gates are on again.")
	case "cancel":
		if c.root == "" {
			return say("not inside a git repo.")
		}
		id := strings.TrimSpace(in.CommandArgs)
		var t *Task
		if id != "" {
			t, _ = LoadTask(c.root, id)
		} else {
			t = c.task
		}
		if t == nil {
			open := []string{}
			for _, x := range ListTasks(c.root) {
				if x.Open() {
					open = append(open, x.ID)
				}
			}
			if len(open) == 1 {
				t, _ = LoadTask(c.root, open[0])
			} else {
				return say("which task? Open tasks: " + strings.Join(open, ", ") + ". Type /plumbline:cancel <id>.")
			}
		}
		if t == nil {
			return say("no such task.")
		}
		wasOpen := t.Open()
		t.Phase = PhaseCancelled
		_ = SaveTask(t)
		LogEvent(Event{Type: "phase", Task: t.ID, Rule: PhaseCancelled, Size: t.Size, Project: c.projectKey()})
		if wasOpen { // a closed task logged its tokens already
			LogTokens(t, c.projectKey())
		}
		note := ""
		if left := LeftCopies(t); len(left) > 0 {
			note = fmt.Sprintf(" Builders' unmerged copies are left on disk, nothing from them is in your code: %s (remove one with git worktree remove --force <path>).", strings.Join(left, ", "))
		}
		return say(fmt.Sprintf("task %s was closed by the user. Its changes stay in the working tree; nothing was undone.%s", t.ID, note))
	case "size":
		return say(userSize(c, in.CommandArgs))
	case "wrong":
		e, err := MarkLastBlockWrong(in.CommandArgs)
		if err != nil {
			return say(err.Error() + ".")
		}
		return say(fmt.Sprintf("marked the last block (%s) as wrong. It counts against that rule in /plumbline:stats.", e.Rule))
	}
	return nil
}

// userSize is /plumbline:size small (R4): the user keeps a low-impact change
// to a risky file small (copy on a login page). Only before approval, and
// files that decide which checks run still need the full review.
func userSize(c *hookCtx, args string) string {
	f := strings.Fields(args)
	if len(f) == 0 || strings.ToLower(f[0]) != "small" {
		return "only /plumbline:size small is yours to type; for normal or big, Claude runs plumbline task size normal|big --why \"...\"."
	}
	if c.root == "" || c.task == nil {
		return "no task is open here, so there's nothing to size."
	}
	t := c.task
	if t.Phase != PhaseUnderstanding && t.Phase != PhasePlanning {
		return fmt.Sprintf("task %s is at the %s step; the size is fixed once the plan is approved (Claude can replan to change it).", t.ID, t.Phase)
	}
	why := strings.TrimSpace(strings.Join(f[1:], " "))
	if why == "" {
		why = "kept small by the user"
	}
	t.Size, t.SizeWhy, t.SizeByUser = "small", why, true
	if err := SaveTask(t); err != nil {
		return "couldn't save the size: " + err.Error() + "."
	}
	LogEvent(Event{Type: "task", Task: t.ID, Size: t.Size, Project: c.projectKey(), Rule: "size-by-user"})
	return fmt.Sprintf("task %s is small, set by the user: risky files in the plan won't make it normal, so there's no challenger and only the code reviewer plus any specialist the files call for. Files that decide which checks run (CI, git hooks, test or lint config, AI instructions) still need the full review. Ask the checkpoint again.", t.ID)
}
