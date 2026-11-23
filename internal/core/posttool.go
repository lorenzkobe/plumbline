package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func jsonUnmarshal(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

// The snapshot is per session and repo: a command run in another repo is
// compared with that repo's own earlier state.
// It's keyed by the working tree, not the project, so a builder's copy and
// the main checkout keep separate snapshots; helpers keep their own too.
func snapPath(sessionID, root string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+"."+treeKey(root)+".snap.json")
}

// snapID: the lead's snapshot is the session's; a helper's is its own.
func (c *hookCtx) snapID() string {
	if c.in.AgentID != "" {
		return c.in.SessionID + "-" + c.in.AgentID
	}
	return c.in.SessionID
}

func loadSnap(sessionID, root string) map[string]string {
	b, err := os.ReadFile(snapPath(sessionID, root))
	if err != nil {
		return nil
	}
	m := map[string]string{}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func saveSnap(sessionID, root string, m map[string]string) {
	b, _ := json.Marshal(m)
	_ = writeFileAtomic(snapPath(sessionID, root), b)
}

// RefreshSnapshot records the dirty files as they are now, so the next
// terminal-command check only sees what changed after this point.
func RefreshSnapshot(sessionID, root string) {
	if root == "" {
		return
	}
	if d, err := DirtyFiles(root); err == nil {
		saveSnap(sessionID, root, d)
	}
}

var reTaskStarted = regexp.MustCompile(`plumbline-task-id: ([0-9a-f-]+)`)

// PostToolUse records what happened: files read, lookups made, code changed,
// checkpoint answers, and files changed by terminal commands.
func PostToolUse(in *HookInput) Output {
	if in.ToolName == "AskUserQuestion" && os.Getenv("PLUMBLINE_DEBUG") != "" {
		b := in.Raw
		if len(b) == 0 {
			b, _ = json.Marshal(in)
		}
		_ = writeFileAtomic(filepath.Join(DataDir(), "debug", "askuser-post.json"), b)
	}
	c := newCtx(in)
	defer c.close()
	if c.root == "" {
		return nil
	}
	if in.AgentID == "" && in.ToolName != "Read" && in.ToolName != "Grep" && in.ToolName != "AskUserQuestion" {
		defer c.absorbIntoBuilders() // runs before close: still under the lock
	}
	c.noteBrowserAllowed()
	switch {
	case in.ToolName == "Read":
		if in.AgentID != "" {
			c.noteHelperOpened(Abs(in.Str("file_path"), in.Cwd))
		}
		// Only the lead's own reads count: every file:line in its notes must
		// come from a file it opened, not from a helper's report.
		if in.AgentID == "" {
			abs := Abs(in.Str("file_path"), in.Cwd)
			if h := fileHash(abs); h != "" {
				if c.task == nil {
					noteSessionRead(in.SessionID, abs, h) // counts for the task that follows (RD1)
				} else {
					c.task.noteRead(abs, h)
					_ = SaveTask(c.task)
				}
			}
		}
	case in.ToolName == "Edit" || in.ToolName == "Write" || in.ToolName == "MultiEdit" || in.ToolName == "NotebookEdit":
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		abs := Abs(p, in.Cwd)
		if abs == filepath.Join(ProjectRoot(c.root), RepoSetupFile) && in.AgentID == "" {
			_ = ConfirmSetup(c.root) // you allowed the change on the confirm prompt
		}
		if c.task != nil && IsInside(abs, c.root) && abs != TrackerPath(c.root) {
			rel := c.rel(abs)
			if c.task.Phase == PhaseBuilding && !c.task.InPlan(rel) {
				c.task.NoteOutside(rel, "edited")
			}
			if run := c.task.runByAgent(in.AgentID); run != nil && run.Kind == HelperQA && !contains(run.Wrote, rel) {
				run.Wrote = append(run.Wrote, rel)
			}
			c.task.noteCodeChange()
			c.task.Reported = false
			if c.task.Reads != nil && in.AgentID == "" {
				if h := fileHash(abs); h != "" {
					c.task.noteRead(abs, h) // Claude just wrote it, so it knows it
				}
			}
			_ = SaveTask(c.task)
		}
		c.shareEdit(abs)
		if o := c.designReminder(abs); o != nil {
			return o
		}
		if o := c.afterSelfReview(abs); o != nil {
			return o
		}
		if o := c.afterReport(abs); o != nil {
			return o
		}
		if o := c.afterPlan(abs); o != nil {
			return o
		}
		if o := c.afterPlanEdit(abs); o != nil {
			return o
		}
	case in.ToolName == "Bash":
		o := c.watchNote(c.withTrackerCheck(c.withMainCheck(c.afterBash())))
		if note := c.planWrittenByShell(); note != "" {
			o = addContext(o, note)
		}
		return o
	case in.ToolName == "Monitor" && in.Str("command") != "":
		// Monitor runs its command in Bash's shell: the same check after it.
		// What it changes later is seen by the next check.
		return c.withTrackerCheck(c.withMainCheck(c.checkChanged(ParseShell(in.Str("command"), in.Cwd))))
	case in.ToolName == "AskUserQuestion":
		return c.afterQuestion()
	case strings.HasPrefix(in.ToolName, "mcp__") && c.mcpKind() != "":
		return c.withTrackerCheck(c.withMainCheck(c.checkChanged(nil)))
	case (in.ToolName == "Agent" || in.ToolName == "Task") && in.AgentID == "" && c.task != nil && helperKind(in.Str("subagent_type"), true) == HelperBuilder:
		// BLD2 (step 11c): the lead didn't know the copy holds its work, sent
		// the builders to copy QA's tests in, and merged by hand.
		return c.watchNote(Context("PostToolUse", "Plumbline: this builder's copy already holds your uncommitted work, blind QA's tests too, so nothing needs copying into it. When it stops, read its notes, then bring it in with plumbline builder merge <n> (plumbline builder list shows the numbers): it checks the builders didn't change the same lines. Don't apply its diff by hand."))
	case in.ToolName == "Agent" || in.ToolName == "Task":
		return c.watchNote(nil)
	case in.ToolName == "Grep":
		c.noteSearchHits(responseText(in.ToolResponse), []string{in.Cwd, c.root})
	case strings.HasPrefix(in.ToolName, "mcp__"):
		c.afterBrowser()
		if strings.Contains(strings.ToLower(in.ToolName), "socraticode") {
			c.noteSearchHits(responseText(in.ToolResponse), []string{c.root, in.Cwd})
		}
		if fam := lookupFamily(in.ToolName); fam != "" && in.AgentID == "" { // a helper's searches aren't the lead's
			if c.task == nil {
				noteSessionLookup(in.SessionID, fam)
				return nil
			}
			if c.task.Lookups == nil {
				c.task.Lookups = map[string]Lookup{}
			}
			l := c.task.Lookups[fam]
			l.Count++
			l.At = Now()
			c.task.Lookups[fam] = l
			_ = SaveTask(c.task)
		}
	}
	return nil
}

func lookupFamily(tool string) string {
	if jira, read := JiraTool(tool); jira && read {
		return "jira"
	}
	low := strings.ToLower(tool)
	for _, f := range []string{"socraticode", "mempalace", "serena", "context7"} {
		if strings.Contains(low, f) {
			return f
		}
	}
	return ""
}

func (c *hookCtx) afterBash() (res Output) {
	in := c.in
	cmd := in.Str("command")
	out := responseText(in.ToolResponse)
	segs := ParseShell(cmd, in.Cwd)
	c.noteDevDBAllowed(segs)
	// Bind a task to this session only on a real plumbline task start or
	// adopt: printing the id line some other way binds nothing. Beside the
	// start, the line may hold only commands whose output can't carry an id
	// line someone wrote (TB1).
	onlyPlumbline, startTail := len(segs) > 0, len(segs) > 0
	var start, adopt *Segment
	for i := range segs {
		s := &segs[i]
		switch {
		case isPlumblineBin(s.Name()) && s.Arg(1) == "task" && s.Arg(2) == "start":
			start = s
		case isPlumblineBin(s.Name()) && s.Arg(1) == "task" && s.Arg(2) == "adopt":
			adopt = s
		case isPlumblineBin(s.Name()), s.Name() == "cd", s.Name() == "" && len(s.Env) == 0:
		case dropsPipedLines(*s):
			// `| tail -30` only trims what the commands before it printed,
			// and those are judged on their own (TB2).
		default:
			onlyPlumbline = false
			if !startTailReads(*s) {
				startTail = false
			}
		}
	}
	// note: a real start or adopt whose line kept it from binding. Without
	// it Claude sees the task started and every edit refused (TB2).
	note := ""
	if id := oneID(reTaskStarted, out); id != "" && start != nil {
		if startTail {
			c.bind(id, true)
		} else {
			note = notTiedNote(id)
		}
	}
	if m := reAdopted.FindStringSubmatch(out); m != nil && adopt != nil && contains(adopt.Words[3:], m[1]) {
		if onlyPlumbline {
			c.bind(m[1], false)
		} else {
			note = notTiedNote(m[1])
		}
	}
	if note != "" {
		defer func() { res = addContext(res, note) }()
	}
	if c.task != nil {
		for _, s := range segs {
			if sub, _ := GitSub(s); sub == "commit" && !strings.Contains(out, "nothing to commit") {
				c.task.Commits++
				_ = SaveTask(c.task)
				c.log("commit", "commit", "")
				break
			}
		}
		c.followBranch(segs)
		moved := c.followHead(segs, false)
		if in.AgentID == "" {
			c.noteShellReads(segs)
			if onlyReads(segs) {
				c.noteSearchHits(out, shellDirs(segs, in.Cwd))
			}
		} else {
			c.noteHelperOpened(shellReadFiles(segs)...)
		}
		if moved != "" {
			return addContext(c.checkChanged(segs), moved)
		}
	}
	return c.checkChanged(segs)
}

// approvalNext: what Claude does after the user approves, by step.
func approvalNext(t *Task) string {
	next := "Make the plan next (plan.md), then the checkpoint."
	if t.Phase == PhaseBuilding {
		next = "First run plumbline run checks (it records every saved check on the approved code; the first edit waits for it). Then build and prove it to the end without stopping: a failing test first where there's a bug, then the change, plumbline run checks again at the end, the review and the report. Don't wait on the user; anything that needs them goes under \"For you\" in the report."
		if files := t.bugCaseFiles(); len(files) > 0 {
			next += fmt.Sprintf(" Write the [bug] cases' tests before the fix and see them fail with plumbline run related --files %s: the finish check needs that failing run, taken while only tests had changed.", strings.Join(files, ","))
		}
		if t.Size != "small" {
			next += " " + builderModelNote
		}
	}
	return next
}

// builderModelNote (BLD3, step 13): builders for one-line label changes
// ran on the session's model; simple parts cost less on Sonnet.
const builderModelNote = "If you hand parts to builders, start each simple, clear part (a label, a rename, a small local change) with model: \"sonnet\" on the Agent call; keep the session's model for tricky or risky parts."

// noteShellReads records the files a terminal command showed lines of, as
// the Read tool does: cat, sed -n, head, grep or rg on a named file. Only a
// command that changes nothing counts, so the file is still what was shown.
func (c *hookCtx) noteShellReads(segs []Segment) {
	files := shellReadFiles(segs)
	if len(files) == 0 {
		return
	}
	if c.task == nil {
		for _, f := range files {
			if h := fileHash(f); h != "" {
				noteSessionRead(c.in.SessionID, f, h) // counts for the task that follows (RD1)
			}
		}
		return
	}
	for _, f := range files {
		if h := fileHash(f); h != "" {
			c.task.noteRead(f, h)
		}
	}
	_ = SaveTask(c.task)
}

// shellReadFiles: the files a command that changes nothing showed lines of.
func shellReadFiles(segs []Segment) []string {
	var files []string
	for _, s := range segs {
		if plumblineBookkeeping(s) {
			continue
		}
		if !ReadOnly(s) {
			return nil
		}
		if s.Shown {
			files = append(files, PrintedFiles(s)...)
		}
	}
	return files
}

// plumblineBookkeeping (RD3): a plumbline task command that writes only
// Plumbline's own records, never the project's files, so a file shown on the
// same line is still what was shown. Merges, runs and finishing aren't here.
func plumblineBookkeeping(s Segment) bool {
	if !isPlumblineBin(s.Name()) || s.Arg(1) != "task" {
		return false
	}
	switch s.Arg(2) {
	case "start", "tool-down", "size", "replan", "adopt":
		return true
	}
	return false
}

// noteHelperOpened records the files a reviewer opened in this run, so a
// withdrawn finding is known to have looked at what the lead cited (N5).
func (c *hookCtx) noteHelperOpened(files ...string) {
	if c.task == nil || len(files) == 0 {
		return
	}
	run := c.task.runByAgent(c.in.AgentID)
	if run == nil || !checked(run.Kind) || !run.Finished.IsZero() {
		return
	}
	added := false
	for _, f := range files {
		if f != "" && !contains(run.Opened, f) {
			run.Opened = append(run.Opened, f)
			added = true
		}
	}
	if added {
		_ = SaveTask(c.task)
	}
}

func notTiedNote(id string) string {
	return "Plumbline: task " + id + " is NOT tied to this session, because other commands shared its line. Run `plumbline task adopt " + id + "` on its own line, with nothing piped after it."
}

// dropsPipedLines: head or tail reading only the pipe, with at most a line
// or byte count. It can drop lines of what came before but never add any.
func dropsPipedLines(s Segment) bool {
	if !s.Piped || s.Opaque != "" || len(s.Redirects) > 0 || len(s.Inputs) > 0 || s.Stdin != nil || s.MoreArgs {
		return false
	}
	if n := s.Name(); n != "head" && n != "tail" {
		return false
	}
	for i := 1; i < len(s.Words); i++ {
		a := s.Words[i]
		if s.dyn(i) || s.glob(i) {
			return false
		}
		switch {
		case a == "-n" || a == "-c":
			if i++; i >= len(s.Words) || !isCount(s.Words[i]) || s.dyn(i) {
				return false
			}
		case a == "-q":
		case strings.HasPrefix(a, "-n") || strings.HasPrefix(a, "-c"):
			if !isCount(a[2:]) {
				return false
			}
		case strings.HasPrefix(a, "-"):
			if !isCount(a[1:]) {
				return false
			}
		default:
			return false // a file it prints instead of the pipe
		}
	}
	return true
}

// isCount: a line or byte count as head and tail take it: 30, +2 or -5.
func isCount(a string) bool {
	a = strings.TrimLeft(a, "+-")
	return a != "" && strings.Trim(a, "0123456789") == ""
}

// startTailReads: a command that may share a line with plumbline task
// start: it only reads, and prints nothing a command or file could have put
// an id line in (no echo, cat, grep, git log).
func startTailReads(s Segment) bool {
	if s.Opaque != "" || len(s.Redirects) > 0 || s.Stdin != nil || s.Piped || !ReadOnly(s) {
		return false
	}
	switch s.Name() {
	case "pwd", "ls", "true":
		return true
	case "git":
		sub, _ := GitSub(s)
		return (sub == "status" || sub == "rev-parse" || sub == "branch") && gitReadOnlySeg(s)
	}
	return false
}

// oneID: the id the output's id lines name, or "" when there are none or
// they name different tasks.
func oneID(re *regexp.Regexp, out string) string {
	id := ""
	for _, m := range re.FindAllStringSubmatch(out, -1) {
		if id != "" && m[1] != id {
			return ""
		}
		id = m[1]
	}
	return id
}

// bind binds a task to this session, under that task's lock. A new task
// must not be bound to another session already.
func (c *hookCtx) bind(id string, mustBeNew bool) {
	if prev := c.task; mustBeNew && prev != nil && prev.ID != id && prev.Open() && prev.SessionID == c.in.SessionID &&
		(prev.Phase == PhaseUnderstanding || prev.Phase == PhasePlanning) {
		// One stop (UX1): the user moved on without answering this task's
		// plan question. Nothing was changed for it yet, so the new task
		// replaces it instead of leaving it open in the checkout.
		prev.Phase = PhaseCancelled
		_ = SaveTask(prev)
		LogEvent(Event{Type: "phase", Task: prev.ID, Rule: PhaseCancelled, Size: prev.Size, Project: c.projectKey()})
		LogTokens(prev, c.projectKey())
	}
	if c.task == nil || c.task.ID != id {
		c.close() // the lock held belongs to another task
		c.unlock = LockTask(c.root, id)
	}
	t, err := LoadTask(c.root, id)
	if err != nil || !t.Open() || mustBeNew && t.SessionID != "" {
		return
	}
	t.SessionID = c.in.SessionID
	pre := takeSessionLookups(c.in.SessionID)
	reads := takeSessionReads(c.in.SessionID)
	if mustBeNew { // a new task: the files read just before it count, as they were then (RD1)
		for f, r := range reads {
			if !IsInside(f, t.Root) {
				continue
			}
			if old, ok := t.Reads[f]; !ok || r.At.After(old.At) {
				if t.Reads == nil {
					t.Reads = map[string]Read{}
				}
				t.Reads[f] = Read{Hash: r.Hash, At: r.At}
			}
		}
	}
	if mustBeNew { // a new task: the searches that led to it count (FR1)
		for fam, l := range pre {
			if t.Lookups == nil {
				t.Lookups = map[string]Lookup{}
			}
			had := t.Lookups[fam]
			had.Count += l.Count
			if l.At.After(had.At) {
				had.At = l.At
			}
			t.Lookups[fam] = had
		}
	}
	_ = SaveTask(t)
	_ = BindSession(c.in.SessionID, t)
	c.task = t
}

// followBranch keeps the task on its branch after git checkout -b or switch,
// so the plumbline commands (which find the task by branch) still find it.
func (c *hookCtx) followBranch(segs []Segment) {
	moved := false
	for _, s := range segs {
		sub, _ := GitSub(s)
		if sub == "checkout" || sub == "switch" || sub == "branch" || s.Name() == "gh" {
			moved = true
		}
	}
	if !moved {
		return
	}
	br := CurrentBranch(c.root)
	if br == "" || br == "HEAD" || br == c.task.Branch {
		return
	}
	c.task.Branch = br
	_ = SaveTask(c.task)
}

// checkChanged is the changed-files check after a terminal command or an MCP
// tool that may change files.
func (c *hookCtx) checkChanged(segs []Segment) Output {
	prev, all, ambiguous, ok := c.takeChanges(readOnlyLine(segs))
	if !ok || prev == nil || c.paused {
		return nil
	}
	setupSaved := false
	for _, s := range segs {
		if isPlumblineBin(s.Name()) && s.Arg(1) == "setup" && s.Arg(2) == "save" {
			setupSaved = true // asked for and allowed; not a change to stop for
		}
	}
	var notes map[string]bool
	if c.task == nil {
		notes = untrackedNotes(c.root, all)
	}
	var changed []string
	for _, p := range all {
		if c.setup != nil && MatchAny(c.setup.IgnoreChanges, p) {
			continue
		}
		if setupSaved && p == RepoSetupFile {
			continue
		}
		if notes[p] {
			continue
		}
		changed = append(changed, p)
	}
	if readOnlyLine(segs) {
		if start := c.takeStart(); !start.IsZero() {
			changed = changedAfter(c.root, changed, start)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	if c.task != nil {
		c.task.noteCodeChange()
		c.task.Reported = false
		_ = SaveTask(c.task)
	}
	if c.task == nil || c.task.Phase != PhaseBuilding {
		c.log("block", "changed-before-approval", fmt.Sprintf("%d files", len(changed)))
		reason := fmt.Sprintf("a terminal command changed files before any plan was approved: %s. The work is stopped so you can look. If you changed these yourself, say so and Claude carries on. Otherwise tell Claude whether to keep or undo them.", listFiles(changed))
		c.helperHalted(reason)
		return StopAll(msg(reason))
	}
	var outside, theirs, sessions, risky []string
	others := c.task.otherSessionFiles()
	held := func(p, why string) {
		if c.task.Held == nil {
			c.task.Held = map[string]string{}
		}
		c.task.Held[p] = why
	}
	for _, p := range changed {
		if c.task.InPlan(p) {
			continue
		}
		if _, mine := c.task.Preexisting[p]; mine {
			theirs = append(theirs, p)
			held(p, "a terminal command changed it, and it held the user's own unsaved work")
			continue
		}
		if others[p] && !ambiguous[p] {
			sessions = append(sessions, p)
			held(p, "a terminal command changed it, and another Claude session had changed it while this task was open")
			continue
		}
		if foldPath(c.root) == foldPath(c.task.Root) && c.task.BackToStart(p) {
			delete(c.task.OutsidePlan, p)
			continue
		}
		if RiskyPath(p, c.setup) {
			risky = append(risky, p)
			held(p, "a terminal command changed this risky file outside the plan")
			continue
		}
		if gateOutsidePlan(p) {
			risky = append(risky, p)
			held(p, "a terminal command changed a file that decides which checks run, outside the plan")
			continue
		}
		outside = append(outside, p)
		c.task.NoteOutside(p, "changed by a terminal command")
	}
	_ = SaveTask(c.task)
	if len(outside)+len(theirs)+len(sessions)+len(risky) == 0 {
		return nil
	}
	c.log("note", "changed-outside-plan", fmt.Sprintf("%d files", len(outside)+len(theirs)+len(sessions)+len(risky)))
	var b strings.Builder
	if len(theirs) > 0 {
		fmt.Fprintf(&b, "that command changed files that held the user's own unsaved work: %s. Don't change them further; put them first under \"For you\" in the report. ", listFiles(theirs))
	}
	if len(sessions) > 0 {
		fmt.Fprintf(&b, "that command changed files another Claude session in this checkout changed while this task was open: %s. They aren't this task's: don't change them further, and list them under \"For you\" in the report. ", listFiles(sessions))
	}
	if len(risky) > 0 {
		fmt.Fprintf(&b, "that command changed risky files (or files that decide which checks run) outside the approved plan: %s. Undo those changes unless the plan is changed, and list them under \"For you\" in the report. ", listFiles(risky))
	}
	if len(outside) > 0 && c.task.OutsideCount() > OutsidePlanCap {
		if c.in.AgentID != "" {
			c.log("block", "outside-plan-cap", fmt.Sprintf("%d files", c.task.OutsideCount()))
			fmt.Fprintf(&b, "that command changed files outside the approved plan: %s. That's more than %d files outside the plan, so this builder is stopped. The lead decides without the user: add the files that belong to this change to plan.md's ## Files and merge the copy, or resume this builder to undo the others in its copy, then merge.", listFiles(outside), OutsidePlanCap)
			c.helperHalted(b.String())
			return StopAll(msg(b.String()))
		}
		c.log("note", "outside-plan-cap", fmt.Sprintf("%d files", c.task.OutsideCount()))
		fmt.Fprintf(&b, "that command changed files outside the approved plan: %s. That's more than %d files outside the plan, so the plan missed them. Don't stop or ask the user: add the ones that belong to this change to plan.md's ## Files (Write or Edit tool) with a line on why, undo the ones that don't, and keep going. They stay on the report's \"Outside the plan\" list.", listFiles(outside), OutsidePlanCap)
	} else if len(outside) > 0 {
		fmt.Fprintf(&b, "that command changed files outside the approved plan: %s. If they belong to this change, keep going and list them under \"Outside the plan\" in the report; if not, undo them. Generated files (coverage, caches) belong in ignore_changes in the project setup.", listFiles(outside))
	}
	return Context("PostToolUse", msg(b.String()))
}

var reAdopted = regexp.MustCompile(`plumbline-adopt-id: ([0-9a-f-]+)`)

func listFiles(f []string) string {
	if len(f) > 8 {
		return strings.Join(f[:8], ", ") + fmt.Sprintf(" and %d more", len(f)-8)
	}
	return strings.Join(f, ", ")
}

// responseText flattens a tool response to text (stdout/stderr or a string).
func responseText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		var b strings.Builder
		for _, k := range []string{"stdout", "stderr", "output", "result", "content"} {
			if v, ok := m[k].(string); ok {
				b.WriteString(v)
				b.WriteString("\n")
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	return string(raw)
}

// findAnswer looks for the user's answer to a question in the tool
// response's answers map. Text that reads like "Question"="Answer" is never
// read: Claude Code sends the map (V1), and a free-text answer could carry
// that text (L6).
func findAnswer(raw json.RawMessage, question string) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		return walkAnswer(v, question)
	}
	return ""
}

func walkAnswer(v any, question string) string {
	switch x := v.(type) {
	case map[string]any:
		if ans, ok := x["answers"]; ok {
			switch a := ans.(type) {
			case map[string]any:
				if s, ok := a[question]; ok {
					return answerString(s)
				}
			case []any:
				for _, item := range a {
					if m, ok := item.(map[string]any); ok {
						if q, _ := m["question"].(string); q == question {
							return answerString(firstOf(m, "answer", "answers", "selected", "label"))
						}
					}
				}
			}
		}
		for _, k := range []string{"result", "data", "output", "response"} {
			if sub, ok := x[k]; ok {
				if a := walkAnswer(sub, question); a != "" {
					return a
				}
			}
		}
	case string:
		var inner any
		if json.Unmarshal([]byte(x), &inner) == nil {
			return walkAnswer(inner, question)
		}
	}
	return ""
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

func answerString(v any) string {
	switch a := v.(type) {
	case string:
		return a
	case []any:
		parts := []string{}
		for _, x := range a {
			parts = append(parts, answerString(x))
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		return answerString(firstOf(a, "label", "answer", "value"))
	}
	return ""
}

// isApproval: the answer must be exactly one of the question's own options
// whose label starts with "Approve". Free text never counts as approval.
func isApproval(answer string, q question) bool {
	a := strings.TrimSpace(answer)
	for _, o := range q.Options {
		l := strings.TrimSpace(o.Label)
		if a == l && strings.HasPrefix(strings.ToLower(l), "approve") {
			return true
		}
	}
	return false
}

// isDrop: the answer is the question's own option starting with "Drop"
// (one stop, UX1). Free text never drops a task.
func isDrop(answer string, q question) bool {
	a := strings.TrimSpace(answer)
	for _, o := range q.Options {
		l := strings.TrimSpace(o.Label)
		if a == l && strings.HasPrefix(strings.ToLower(l), "drop") {
			return true
		}
	}
	return false
}

// afterPlan: once the lead writes plan.md, say which lookups, file:line
// checks and plan rules the plan question still needs, so it's done before
// the question instead of after a refusal (Serena was refused four runs in
// a row; CASE1, a case left without its test after the challenge).
func (c *hookCtx) afterPlan(abs string) Output {
	t := c.task
	if t == nil || c.in.AgentID != "" || (t.Phase != PhasePlanning && t.Phase != PhaseUnderstanding) {
		return nil
	}
	docs := Abs(TaskDocsDir(c.root, t.ID), "/")
	if foldPath(abs) != foldPath(filepath.Join(docs, DocPlan)) {
		return nil
	}
	p := c.groundingProblems()
	plan := ""
	for _, d := range []string{DocUnderstanding, DocPlan} {
		if text, _, err := ReadDoc(t, d); err == nil {
			p = append(p, CheckRefs(t, text)...)
			if d == DocPlan {
				plan = text
				p = append(p, c.planRuleProblems(text)...) // CASE1: the plan's own rules too
			}
		}
	}
	if len(p) == 0 {
		if len(c.challengeProblems(plan)) > 0 {
			return nil // the challenger is next; the task skill says so
		}
		// PLAN3, HID2: the plan the user reads is plain, and the question
		// repeats the gist because it can cover the chat.
		shape := `then Title Case headings (The Problem, The Fix, What You'll Notice, Risks, How It's Checked, What I Need From You), full sentences, plain words, no file or code names (plan.md's path on the last line holds those)`
		if t.Size == "small" {
			shape = "then two or three full sentences in plain words, no file or code names"
		}
		return Context("PostToolUse", msg(`plan.md has what the plan question needs. Post the plan in the chat first, as checkpoints.md's "The plan in chat" shows: "Done When: ..." first, `+shape+`. Then ask: AskUserQuestion, header "Checkpoint", options "Approve", "Change something", "Drop it", with the question itself saying the problem, the fix and the risk in two or three sentences, because it can cover the chat.`))
	}
	return Context("PostToolUse", msg("before the plan question, still needed: "+strings.Join(uniq(p), "; ")+". Do these now, so the question isn't refused."))
}

func (c *hookCtx) planPath() string {
	return filepath.Join(Abs(TaskDocsDir(c.root, c.task.ID), "/"), DocPlan)
}

func (c *hookCtx) planWrittenByShell() string {
	if c.task == nil || c.in.AgentID != "" || c.task.Phase != PhaseBuilding {
		return ""
	}
	plan := foldPath(c.planPath())
	for _, s := range ParseShell(c.in.Str("command"), c.in.Cwd) {
		for _, w := range WriteTargets(s) {
			if abs := targetAbs(s, w); abs != "" && foldPath(abs) == plan {
				return c.planEditNote()
			}
		}
	}
	return ""
}

func (c *hookCtx) afterPlanEdit(abs string) Output {
	if c.task == nil || foldPath(abs) != foldPath(c.planPath()) {
		return nil
	}
	if note := c.planEditNote(); note != "" {
		return Context("PostToolUse", note)
	}
	return nil
}

func dirAt(root, rel string) bool {
	fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && fi.IsDir()
}

func unusablePlanPath(f string) bool {
	return filepath.IsAbs(f) || f == ".." || strings.HasPrefix(f, "../") || strings.Contains(f, "/../") || strings.HasPrefix(f, "~")
}

func (c *hookCtx) planEditNote() string {
	t := c.task
	if t == nil || c.in.AgentID != "" || t.Phase != PhaseBuilding || c.paused {
		return ""
	}
	text, _, err := ReadDoc(t, DocPlan)
	if err != nil {
		return ""
	}
	others := t.otherSessionFiles()
	var added, refused []string
	pc := CheckPlan(text)
	for _, f := range pc.Files {
		if t.InPlan(f) {
			continue
		}
		_, theirs := t.Preexisting[f]
		switch {
		case strings.HasSuffix(f, "/") || strings.ContainsAny(f, "*?[\\"):
			refused = append(refused, f+" (a folder or pattern: only exact files can be added while building)")
		case unusablePlanPath(f):
			refused = append(refused, f+" (not a path inside the repo: write it relative to the repo root)")
		case dirAt(t.Root, f):
			refused = append(refused, f+" (a folder: only exact files can be added while building)")
		case theirs:
			refused = append(refused, f+" (it held the user's own unsaved work)")
		case others[f]:
			refused = append(refused, f+" (another Claude session changed it)")
		case RiskyPath(f, c.setup) || gateOutsidePlan(f):
			refused = append(refused, f+" (a risky file, or one that decides which checks run)")
		case !contains(pc.NewFiles, f) && gone(t.Root, f):
			refused = append(refused, f+" (not in the repo: mark a file the change creates with \"(new)\" after it, and keep notes that aren't files out of ## Files)")
		default:
			t.PlanFiles = append(t.PlanFiles, f)
			t.NoteOutside(f, "added to the plan while building")
			added = append(added, f)
		}
	}
	if len(added) == 0 && len(refused) == 0 {
		return ""
	}
	if len(added) > 0 {
		if err := SaveTask(t); err != nil {
			return msg(fmt.Sprintf("plan.md's new files couldn't be recorded (%v), so they still count as outside the plan. Write plan.md again.", err))
		}
		c.log("note", "plan-files-added", fmt.Sprintf("%d files", len(added)))
	}
	var b strings.Builder
	if len(added) > 0 {
		fmt.Fprintf(&b, "added to the plan's files: %s. Keep going; list them under \"Outside the plan\" in the report with why. ", listFiles(added))
	}
	if len(refused) > 0 {
		fmt.Fprintf(&b, "not added, so they stay outside the plan: %s. Don't change them; list them under \"For you\" in the report with what you'd change and why (a risky file can join the plan only through plumbline task replan and the user's approval).", strings.Join(refused, ", "))
	}
	return msg(strings.TrimSpace(b.String()))
}

// afterReport: once the lead writes report.md, say what the finish check
// still needs, so the commit question comes last and isn't refused (CF1).
func (c *hookCtx) afterReport(abs string) Output {
	if c.task == nil || c.in.AgentID != "" || c.task.Phase != PhaseBuilding {
		return nil
	}
	if foldPath(abs) != foldPath(Abs(filepath.Join(TaskDocsDir(c.root, c.task.ID), DocReport), "/")) {
		return nil
	}
	if c.task.Hotfix() && c.task.Commits == 0 {
		return nil
	}
	if miss := c.commitNotReady(); len(miss) > 0 {
		return Context("PostToolUse", msg("before the commit question, the finish check still needs: "+strings.Join(miss, "; ")+". Do these first (update the report if they change anything), then ask."))
	}
	return Context("PostToolUse", msg(`the finish check has nothing left but the commit. If the task's changes aren't committed, say in one line "Checks: all pass. Review: clean. Goal: met." (or what isn't), then ask the commit question: AskUserQuestion, header "Commit", options "Commit it" and "Leave uncommitted".`))
}

func isCloseQ(q question) bool {
	return strings.EqualFold(strings.TrimSpace(q.Header), "close task")
}

func closeQuestionPlain(qs []question, q question) bool {
	if len(qs) != 1 || len(q.Options) != 2 {
		return false
	}
	want := []string{"close it", "keep it open"}
	for i, o := range q.Options {
		if !strings.EqualFold(strings.TrimSpace(o.Label), want[i]) || strings.TrimSpace(o.Description) != "" {
			return false
		}
	}
	return true
}

func isCloseYes(answer string, q question) bool {
	a := strings.TrimSpace(answer)
	for _, o := range q.Options {
		l := strings.TrimSpace(o.Label)
		if a == l && strings.EqualFold(l, "close it") {
			return true
		}
	}
	return false
}

func (c *hookCtx) recordClose(q question) Output {
	t := c.task
	qs, _ := c.questions()
	if c.in.AgentID != "" || !t.Open() || !closeQuestionPlain(qs, q) || !isCloseYes(findAnswer(c.in.ToolResponse, q.Question), q) {
		return nil
	}
	var owed []string
	if t.Phase == PhaseBuilding && c.setup != nil {
		owed = FinishMissing(t, c.setup)
	}
	note, err := closeTask(t, c.projectKey(), "closed at the user's answer")
	if err != nil {
		return Context("PostToolUse", msg("the user chose to close this task, but saving that failed ("+err.Error()+"), so it is still open. Tell the user, and suggest /plumbline:cancel."))
	}
	if len(owed) > 0 {
		note += " What the finish check still wanted, now left unchecked: " + strings.Join(owed, "; ") + "."
	}
	return Context("PostToolUse", msg("the user chose to close this task, so it's closed. Its changes stay in the working tree; nothing was undone."+note+" Tell the user in one plain line, without the task id, and name anything left unchecked."))
}

func closeTask(t *Task, project, detail string) (string, error) {
	wasOpen := t.Open()
	var running []string
	for _, r := range t.Helpers {
		if r.alive() {
			running = append(running, r.Kind)
		}
	}
	phase := t.Phase
	t.Phase = PhaseCancelled
	if err := SaveTask(t); err != nil {
		t.Phase = phase
		return "", err
	}
	LogEvent(Event{Type: "phase", Task: t.ID, Rule: PhaseCancelled, Size: t.Size, Project: project, Detail: detail})
	if wasOpen {
		LogTokens(t, project)
	}
	note := ""
	if len(running) > 0 {
		note += " Helpers still running, whose work now counts for nothing: " + strings.Join(running, ", ") + "."
	}
	if left := LeftCopies(t); len(left) > 0 {
		note += fmt.Sprintf(" Builders' unmerged copies are left on disk, nothing from them is in your code: %s (remove one with git worktree remove --force <path>).", strings.Join(left, ", "))
	}
	return note, nil
}

// isCommitQ: the question Claude asks before committing (header "Commit").
func isCommitQ(q question) bool {
	return strings.EqualFold(strings.TrimSpace(q.Header), "commit")
}

// isCommitYes: the answer is the question's own "Commit it" option. Free
// text never counts, like the plan approval.
func isCommitYes(answer string, q question) bool {
	a := strings.TrimSpace(answer)
	for _, o := range q.Options {
		l := strings.TrimSpace(o.Label)
		if a == l && (strings.EqualFold(l, "commit it") || strings.EqualFold(l, "commit") || strings.EqualFold(l, "commit now")) {
			return true
		}
	}
	return false
}

// commitPrint: HEAD and the code, so the user's "Commit it" covers exactly
// what they saw: a later edit or commit changes it.
func commitPrint(root string, ignore []string) string {
	p, err := Fingerprint(root, ignore)
	if err != nil || p == "" {
		return ""
	}
	return HeadCommit(root) + ":" + p
}

// recordCommitAnswer keeps the user's "Commit it", so the commit that
// follows doesn't ask them again (one stop). Any other answer drops it.
func (c *hookCtx) recordCommitAnswer(q question) {
	ok := ""
	if isCommitYes(findAnswer(c.in.ToolResponse, q.Question), q) {
		ok = commitPrint(c.root, c.ignore())
	}
	if c.task.CommitOK == ok {
		return
	}
	c.task.CommitOK = ok
	_ = SaveTask(c.task)
}

func (c *hookCtx) afterQuestion() Output {
	if c.task == nil {
		return nil
	}
	qs, _ := c.questions()
	for _, q := range qs {
		cp := checkpointOf(q)
		if cp == 0 {
			if isCloseQ(q) {
				if o := c.recordClose(q); o != nil {
					return o
				}
				continue
			}
			if isReviewCapQ(q) {
				if o := c.recordReviewCap(q); o != nil {
					return o
				}
				continue
			}
			if isCommitQ(q) && c.task.Phase == PhaseBuilding {
				c.recordCommitAnswer(q)
				continue
			}
			if isDirectionQ(q) {
				if o := c.recordDirection(q); o != nil {
					return o
				}
			}
			continue
		}
		ans := findAnswer(c.in.ToolResponse, q.Question)
		if isDrop(ans, q) {
			note, err := closeTask(c.task, c.projectKey(), "dropped at the plan question")
			if err != nil {
				return Context("PostToolUse", msg("the user dropped this task at the plan question, but saving that failed ("+err.Error()+"), so it is still open. Tell the user, and suggest /plumbline:cancel."))
			}
			return Context("PostToolUse", msg("the user dropped this task at the plan question, so it's closed. Any changes it made stay in the working tree; nothing was undone."+note+" Tell the user in one plain line, without the task id."))
		}
		if !isApproval(ans, q) {
			c.log("checkpoint", fmt.Sprintf("checkpoint%d-not-approved", cp), "")
			if ans == "" {
				return Context("PostToolUse", msg(fmt.Sprintf("couldn't read an approval at the checkpoint (%d), so the task stays at the %s step. If the user did approve, tell them Plumbline didn't see it (run with PLUMBLINE_DEBUG=1 to capture the answer shape).", cp, c.task.Phase)))
			}
			return Context("PostToolUse", msg(fmt.Sprintf("the checkpoint (%d) was not approved. Work on what the user asked, then ask again.", cp)))
		}
		if problems := c.checkpointProblems(cp); len(problems) > 0 {
			c.log("block", fmt.Sprintf("checkpoint%d", cp), "approved but not ready")
			return Context("PostToolUse", msg(fmt.Sprintf("the approval can't be recorded, because the checkpoint isn't ready any more: %s. Fix that and ask the checkpoint again.", strings.Join(problems, "; "))))
		}
		t := c.task
		switch {
		case cp == 1 && t.Phase == PhaseUnderstanding:
			t.Phase = PhasePlanning
			t.Checkpoint1At = Now()
		case cp == 2 && (t.Phase == PhasePlanning || t.Phase == PhaseUnderstanding):
			text, _, err := ReadDoc(t, DocPlan)
			if err != nil {
				return Context("PostToolUse", msg("plan.md is missing, so the approval can't be recorded."))
			}
			pc := CheckPlan(text)
			if t.Phase == PhaseUnderstanding {
				t.Checkpoint1At = Now()
			}
			t.Phase = PhaseBuilding
			t.Checkpoint2At = Now()
			t.PlanFiles = pc.Files
			t.CommandsOnly = pc.CommandsOnly
			t.Goal = pc.Goal
			t.ProofKinds = pc.ProofKinds
			t.ApprovedCmds = pc.Approved
			t.CaseTests = pc.CaseTests
			t.CasesStored = true
			t.BugTests = pc.BugTests
			t.BugStored = true
			t.Upgrade = upgradePlan(pc, c.root)
			t.Reviewers = TeamReviewers(text)
			t.PlanHash = sha256Hex([]byte(text))
			if t.ApprovalPrint == "" {
				// The code at the first approval: blind QA may start only while
				// nothing but tests has changed since (a replan keeps the first).
				t.ApprovalPrint, _ = Fingerprint(t.Root, c.ignore())
			}
		default:
			continue
		}
		_ = SaveTask(t)
		c.log("phase", t.Phase, "")
		RefreshSnapshot(c.snapID(), c.root)
		next := approvalNext(t)
		return Context("PostToolUse", msg(fmt.Sprintf("the user approved. Task %s is now at the %s step. %s", t.ID, t.Phase, next)))
	}
	return nil
}

// withMainCheck adds the builder's main-checkout check to a post-command
// result.
func (c *hookCtx) withMainCheck(o Output) Output {
	if helperKind(c.in.AgentType, false) != HelperBuilder {
		return o
	}
	note := c.builderReachedMain()
	if note == "" {
		return o
	}
	return addContext(o, msg(note))
}

const watchNoteText = "this runs in the background. Unless a watcher already runs, start `plumbline status --watch` with the Monitor tool (timeout_ms 1800000; start it again when it expires while work still runs), and tell the user in one line what you're waiting on and about how long. It prints a line only when a run goes quiet, runs long or ends, or a helper goes quiet, finishes or is stopped: look, then tell the user. A run stopped at its time limit likely hangs: report it, don't re-run it as flaky."

func (c *hookCtx) watchNote(o Output) Output {
	in := c.in
	if c.task == nil || in.AgentID != "" || in.Input()["run_in_background"] != true {
		return o
	}
	switch in.ToolName {
	case "Agent", "Task":
		if helperKind(in.Str("subagent_type"), true) == "" {
			return o
		}
	case "Bash":
		run := false
		for _, s := range ParseShell(in.Str("command"), in.Cwd) {
			run = run || isPlumblineBin(s.Name()) && s.Arg(1) == "run"
		}
		if !run {
			return o
		}
	default:
		return o
	}
	return addContext(o, msg(watchNoteText))
}
