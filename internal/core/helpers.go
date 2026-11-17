package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The two helpers: the challenger attacks the understanding and the plan
// before the checkpoint, the reviewer checks the change after building. Both
// are read-only plugin agents. Plumbline records when each run starts and
// stops, with the code fingerprint at both ends, so a run only counts when it
// finished on code that didn't change under it.
const (
	HelperChallenger = "challenger"
	HelperReviewer   = "reviewer"

	MaxChallengeRounds = 2
	MaxReviews         = 3 // the first review plus 2 fix rounds

	// A run with no stop recorded after this long is treated as dead.
	helperAlive = 30 * time.Minute
)

type HelperRun struct {
	Kind        string    `json:"kind"`
	AgentID     string    `json:"agent_id"`
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished,omitempty"`
	PrintBefore string    `json:"print_before"`
	PrintAfter  string    `json:"print_after,omitempty"`
	Findings    string    `json:"findings"`           // file name in the task's docs folder
	SawDocs     bool      `json:"saw_docs,omitempty"` // understanding.md and plan.md existed at the start
}

// Complete: the run finished and the code didn't change while it ran.
func (r HelperRun) Complete() bool {
	return !r.Finished.IsZero() && r.PrintAfter != "" && r.PrintAfter == r.PrintBefore
}

// helperKind maps an agent type to a Plumbline helper. Hook input carries the
// plugin-scoped name; the lead may pass the bare name to the Agent tool.
func helperKind(agentType string, allowBare bool) string {
	switch agentType {
	case "plumbline:" + HelperChallenger:
		return HelperChallenger
	case "plumbline:" + HelperReviewer:
		return HelperReviewer
	}
	if allowBare && (agentType == HelperChallenger || agentType == HelperReviewer) {
		return agentType
	}
	return ""
}

var reFindings = regexp.MustCompile(`^(challenge|review)-[0-9]+\.md$`)

func findingsName(kind string, n int) string {
	prefix := "challenge"
	if kind == HelperReviewer {
		prefix = "review"
	}
	return fmt.Sprintf("%s-%d.md", prefix, n)
}

func (t *Task) helperRuns(kind string) []HelperRun {
	var out []HelperRun
	for _, r := range t.Helpers {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// alive: started recently and not stopped yet.
func (r HelperRun) alive() bool {
	return r.Finished.IsZero() && Now().Sub(r.Started) < helperAlive
}

// HelperRunning: some helper of this task is running right now.
func (t *Task) HelperRunning() bool {
	for _, r := range t.Helpers {
		if r.alive() {
			return true
		}
	}
	return false
}

func (t *Task) runByAgent(agentID string) *HelperRun {
	for i := len(t.Helpers) - 1; i >= 0; i-- {
		if t.Helpers[i].AgentID == agentID && agentID != "" {
			return &t.Helpers[i]
		}
	}
	return nil
}

func (t *Task) findingsExist(r HelperRun) bool {
	fi, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), r.Findings))
	return err == nil && fi.Size() > 0
}

// lastFinished is the newest helper run of a kind that has stopped.
func (t *Task) lastFinished(kind string) *HelperRun {
	runs := t.helperRuns(kind)
	for i := len(runs) - 1; i >= 0; i-- {
		if !runs[i].Finished.IsZero() {
			r := runs[i]
			return &r
		}
	}
	return nil
}

func (c *hookCtx) ignore() []string {
	if c.setup == nil {
		return nil
	}
	return c.setup.IgnoreChanges
}

func docExists(t *Task, name string) bool {
	_, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), name))
	return err == nil
}

// gateHelperStart runs when the lead starts a helper (PreToolUse on Agent).
func (c *hookCtx) gateHelperStart(kind string) Output {
	if c.paused {
		return nil
	}
	t := c.task
	if t == nil {
		return c.deny("helper-no-task", fmt.Sprintf("the %s works on a task; start one first.", kind))
	}
	switch kind {
	case HelperChallenger:
		if t.Phase == PhaseBuilding {
			return c.deny("challenger-phase", "the challenger is for before the checkpoint, and the plan is already approved. After building, run the reviewer (plumbline:reviewer).")
		}
		if !docExists(t, DocUnderstanding) || !docExists(t, DocPlan) {
			return c.deny("challenger-docs", "write understanding.md and plan.md first: the challenger attacks both.")
		}
		if n := len(t.helperRuns(HelperChallenger)); n >= MaxChallengeRounds {
			return c.deny("challenger-rounds", fmt.Sprintf("the challenger already ran %d times, the most a task gets. Write the \"## Challenge note\" in plan.md with what is still open, and go to the checkpoint.", n))
		}
	case HelperReviewer:
		if t.Phase != PhaseBuilding {
			return c.deny("reviewer-phase", fmt.Sprintf("the reviewer checks the built change, and task %s is at the %s step. Before the checkpoint, the challenger (plumbline:challenger) is the one to run.", t.ID, t.Phase))
		}
		if n := len(t.helperRuns(HelperReviewer)); n >= MaxReviews {
			return c.deny("reviewer-rounds", fmt.Sprintf("the reviewer already ran %d times (the first review plus 2 fix rounds). Stop and bring what's left to the user, with your suggestion.", n))
		}
	}
	return nil
}

// gateHelper keeps the helpers read-only: they read the code, run commands
// that only read, and write their own findings file and nothing else.
func (c *hookCtx) gateHelper(kind string) Output {
	in := c.in
	switch in.ToolName {
	case "Edit", "MultiEdit", "NotebookEdit":
		return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only. Put what should change in your findings file.", kind))
	case "Write":
		abs := Abs(in.Str("file_path"), in.Cwd)
		var run *HelperRun
		if c.task != nil {
			run = c.task.runByAgent(in.AgentID)
		}
		if run == nil {
			return c.deny("helper-read-only", fmt.Sprintf("the %s can only write its findings file, and Plumbline has no record of this run (no open task found). Report your findings in your final message instead.", kind))
		}
		want := filepath.Join(TaskDocsDir(c.task.Root, c.task.ID), run.Findings)
		if abs != Abs(want, "/") {
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only: the only file it can write is its findings file, %s.", kind, want))
		}
		return allow("helper findings")
	case "AskUserQuestion":
		return c.deny("helper-no-questions", fmt.Sprintf("the %s runs without the user. Put open questions in your findings file; the lead decides what to ask.", kind))
	case "Bash":
		return c.helperBash(kind, in.Str("command"))
	}
	if strings.HasPrefix(in.ToolName, "mcp__") {
		switch MCPKind(in.ToolName) {
		case "shell":
			if cmd, ok := in.Input()["command"].(string); ok && cmd != "" {
				return c.helperBash(kind, cmd)
			}
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only, and %s runs code.", kind, in.ToolName))
		case "write":
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only. Put what should change in your findings file.", kind))
		}
	}
	return nil
}

func (c *hookCtx) helperBash(kind, cmd string) Output {
	for _, s := range ParseShell(cmd, c.in.Cwd) {
		if ReadOnly(s) || plumblineReads(s) {
			continue
		}
		what := s.Name()
		if s.Opaque != "" {
			what = s.Opaque
		}
		return c.deny("helper-read-only", fmt.Sprintf("the %s only runs commands that read (git diff, git log, grep, cat ...). %q changes something or isn't known to be read-only. The lead runs tests and proof.", kind, what))
	}
	return nil
}

// SubagentStart records a helper run and tells the helper where its notes
// are and which file it writes.
func SubagentStart(in *HookInput) Output {
	kind := helperKind(in.AgentType, false)
	if kind == "" {
		return nil
	}
	c := newCtx(in)
	defer c.close()
	if c.task == nil {
		return Context("SubagentStart", msg(fmt.Sprintf("no open task was found, so this %s run isn't recorded and can't write a findings file. Give your findings in your final message.", kind)))
	}
	t := c.task
	n := len(t.helperRuns(kind)) + 1
	run := HelperRun{Kind: kind, AgentID: in.AgentID, Started: Now(), Findings: findingsName(kind, n),
		SawDocs: docExists(t, DocUnderstanding) && docExists(t, DocPlan)}
	run.PrintBefore, _ = Fingerprint(t.Root, c.ignore())
	t.Helpers = append(t.Helpers, run)
	_ = SaveTask(t)
	c.log("helper", kind+"-start", "")
	docs := TaskDocsDir(t.Root, t.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "you are the %s for task %s \"%s\" (%s). Notes folder: %s.", kind, t.ID, t.Title, t.Size, docs)
	if kind == HelperChallenger {
		fmt.Fprintf(&b, " Attack %s and %s there. This is round %d of at most %d.", DocUnderstanding, DocPlan, n, MaxChallengeRounds)
	} else {
		base := t.StartCommit
		if base == "" {
			base = "HEAD"
		}
		fmt.Fprintf(&b, " Review the change against the approved %s: `git diff %s` plus new files from `git status --porcelain`. This is review %d of at most %d.", DocPlan, base, n, MaxReviews)
	}
	if n > 1 {
		fmt.Fprintf(&b, " Earlier findings: %s; check whether each was really fixed, then look at what changed since.", findingsName(kind, n-1))
	}
	fmt.Fprintf(&b, " Write your findings to %s as you go: it is the only file you can write. Terminal commands must only read.", filepath.Join(docs, run.Findings))
	return Context("SubagentStart", msg(b.String()))
}

// SubagentStop records the end of a helper run. A helper that wrote no
// findings is sent back once to write them.
func SubagentStop(in *HookInput) Output {
	kind := helperKind(in.AgentType, false)
	if kind == "" {
		return nil
	}
	c := newCtx(in)
	defer c.close()
	if c.task == nil {
		return nil
	}
	t := c.task
	run := t.runByAgent(in.AgentID)
	if run == nil || !run.Finished.IsZero() {
		return nil
	}
	if !t.findingsExist(*run) && !in.StopHookActive {
		return Output{"decision": "block", "reason": msg(fmt.Sprintf("write your findings to %s before you stop (if nothing scored 80 or more, say so there, with what you checked).", filepath.Join(TaskDocsDir(t.Root, t.ID), run.Findings)))}
	}
	run.Finished = Now()
	run.PrintAfter, _ = Fingerprint(t.Root, c.ignore())
	_ = SaveTask(t)
	detail := ""
	if !t.findingsExist(*run) {
		detail = "no findings file"
	}
	c.log("helper", kind+"-stop", detail)
	return nil
}

// challengeProblems: before the checkpoint, a finished challenger run on both
// documents and a challenge note in the plan. Small tasks skip it.
func (c *hookCtx) challengeProblems(plan string) []string {
	t := c.task
	if t.Size == "small" || !c.setup.PartOn(HelperChallenger) {
		return nil
	}
	var p []string
	ok := false
	for _, r := range t.helperRuns(HelperChallenger) {
		if r.Complete() && r.SawDocs && t.findingsExist(r) {
			ok = true
		}
	}
	switch {
	case ok:
	case t.HelperRunning():
		p = append(p, "the challenger is still running; wait for its findings")
	default:
		p = append(p, "the challenger hasn't attacked the understanding and the plan yet: start the Agent tool with subagent_type plumbline:challenger (it needs both files written first, and must finish with a findings file)")
	}
	if len(Section(plan, "challenge note")) == 0 {
		p = append(p, `plan.md needs a "## Challenge note": what the challenger questioned, what you changed, and anything you disagreed with and why`)
	}
	return p
}

// reviewMissing: the finish check needs a finished review of the current code
// and a "## Review" section in a report written after it.
func reviewMissing(t *Task, s *Setup, fp, report string, reportTime time.Time) []string {
	if !s.PartOn(HelperReviewer) {
		return nil
	}
	runs := t.helperRuns(HelperReviewer)
	last := t.lastFinished(HelperReviewer)
	var miss []string
	switch {
	case len(runs) == 0:
		miss = append(miss, "review: run the reviewer on the finished change (Agent tool, subagent_type plumbline:reviewer), after the proof")
	case runs[len(runs)-1].alive():
		miss = append(miss, "review: the reviewer is still running; wait for its findings")
	case last == nil && len(runs) < MaxReviews:
		miss = append(miss, "review: the reviewer never finished; run it again")
	case last == nil:
		miss = append(miss, fmt.Sprintf("review: no review finished and all %d are used; stop and ask the user how to go on", MaxReviews))
	case !last.Complete() || last.PrintAfter != fp || !t.findingsExist(*last):
		if len(runs) >= MaxReviews {
			miss = append(miss, fmt.Sprintf("review: the code changed after the last review and all %d reviews are used (the first plus 2 fix rounds); stop and ask the user how to go on", MaxReviews))
		} else {
			miss = append(miss, "review: the code changed after the last review (or during it, or it left no findings file); run the reviewer again to check the fixes")
		}
	}
	if report != "" {
		if len(Section(report, "review")) == 0 {
			miss = append(miss, `report: add a "## Review" section: what the reviewer found, what you checked against the code, and what you fixed or pushed back on`)
		} else if last != nil && reportTime.Before(last.Finished) {
			miss = append(miss, "report: the last review finished after report.md was written; update it with the review")
		}
	}
	return miss
}
