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
	c := newCtx(in)
	defer c.close()
	if c.root == "" {
		return nil
	}
	if in.AgentID == "" && in.ToolName != "Read" && in.ToolName != "AskUserQuestion" {
		defer c.absorbIntoBuilders() // runs before close: still under the lock
	}
	switch {
	case in.ToolName == "Read":
		if in.AgentID != "" {
			c.noteHelperOpened(Abs(in.Str("file_path"), in.Cwd))
		}
		// Only the lead's own reads count: every file:line in its notes must
		// come from a file it opened, not from a helper's report.
		if c.task != nil && in.AgentID == "" {
			abs := Abs(in.Str("file_path"), in.Cwd)
			if h := fileHash(abs); h != "" {
				c.task.noteRead(abs, h)
				_ = SaveTask(c.task)
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
	case in.ToolName == "Bash":
		return c.withTrackerCheck(c.withMainCheck(c.afterBash()))
	case in.ToolName == "Monitor" && in.Str("command") != "":
		// Monitor runs its command in Bash's shell: the same check after it.
		// What it changes later is seen by the next check.
		return c.withTrackerCheck(c.withMainCheck(c.checkChanged(ParseShell(in.Str("command"), in.Cwd))))
	case in.ToolName == "AskUserQuestion":
		return c.afterQuestion()
	case strings.HasPrefix(in.ToolName, "mcp__") && c.mcpKind() != "":
		return c.withTrackerCheck(c.withMainCheck(c.checkChanged(nil)))
	case strings.HasPrefix(in.ToolName, "mcp__"):
		if c.task != nil && in.AgentID == "" { // a helper's searches aren't the lead's
			if fam := lookupFamily(in.ToolName); fam != "" {
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
		} else {
			c.noteHelperOpened(shellReadFiles(segs)...)
		}
		if moved != "" {
			return addContext(c.checkChanged(segs), moved)
		}
	}
	return c.checkChanged(segs)
}

// noteShellReads records the files a terminal command showed lines of, as
// the Read tool does: cat, sed -n, head, grep or rg on a named file. Only a
// command that changes nothing counts, so the file is still what was shown.
func (c *hookCtx) noteShellReads(segs []Segment) {
	files := shellReadFiles(segs)
	if len(files) == 0 {
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
		if !ReadOnly(s) {
			return nil
		}
		if s.Shown {
			files = append(files, PrintedFiles(s)...)
		}
	}
	return files
}

// noteHelperOpened records the files a reviewer opened in this run, so a
// withdrawn finding is known to have looked at what the lead cited (N5).
func (c *hookCtx) noteHelperOpened(files ...string) {
	if c.task == nil || len(files) == 0 {
		return
	}
	run := c.task.runByAgent(c.in.AgentID)
	if run == nil || !isReviewer(run.Kind) || !run.Finished.IsZero() {
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
	if c.task == nil || c.task.ID != id {
		c.close() // the lock held belongs to another task
		c.unlock = LockTask(c.root, id)
	}
	t, err := LoadTask(c.root, id)
	if err != nil || !t.Open() || mustBeNew && t.SessionID != "" {
		return
	}
	t.SessionID = c.in.SessionID
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
	var changed []string
	for _, p := range all {
		if c.setup != nil && MatchAny(c.setup.IgnoreChanges, p) {
			continue
		}
		if setupSaved && p == RepoSetupFile {
			continue
		}
		changed = append(changed, p)
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
		return StopAll(msg(fmt.Sprintf("a terminal command changed files before any plan was approved: %s. The work is stopped so you can look. If you changed these yourself, say so and Claude carries on. Otherwise tell Claude whether to keep or undo them.", listFiles(changed))))
	}
	// During building the user is often away: record and tell Claude; stop
	// only when the plan looks wrong.
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
	if len(outside)+len(theirs)+len(sessions)+len(risky) == 0 {
		return nil
	}
	_ = SaveTask(c.task)
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
		c.log("block", "outside-plan-cap", fmt.Sprintf("%d files", c.task.OutsideCount()))
		fmt.Fprintf(&b, "that command changed files outside the approved plan: %s. That's more than %d files outside the plan, so the plan was likely wrong. The work is stopped: the user decides whether to go back to the plan (plumbline task replan) or undo these.", listFiles(outside), OutsidePlanCap)
		return StopAll(msg(b.String()))
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

func (c *hookCtx) afterQuestion() Output {
	if c.task == nil {
		return nil
	}
	qs, _ := c.questions()
	for _, q := range qs {
		cp := checkpointOf(q)
		if cp == 0 {
			if isDirectionQ(q) {
				if o := c.recordDirection(q); o != nil {
					return o
				}
			}
			continue
		}
		ans := findAnswer(c.in.ToolResponse, q.Question)
		if os.Getenv("PLUMBLINE_DEBUG") != "" {
			_ = writeFileAtomic(filepath.Join(DataDir(), "debug", "askuser-response.json"), c.in.ToolResponse)
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
			t.ProofKinds = pc.ProofKinds
			t.ApprovedCmds = pc.Approved
			t.CaseTests = pc.CaseTests
			t.CasesStored = true
			t.BugTests = pc.BugTests
			t.BugStored = true
			t.Upgrade = upgradePlan(pc)
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
		next := "Make the plan next (plan.md), then the checkpoint."
		if t.Phase == PhaseBuilding {
			next = "Build and prove it to the end without stopping: a failing test first, then the fix, then the proof and the report. Don't wait on the user; anything that needs them goes under \"For you\" in the report."
			if files := t.bugCaseFiles(); len(files) > 0 {
				next += fmt.Sprintf(" Write the [bug] cases' tests before the fix and see them fail with plumbline run related --files %s: the finish check needs that failing run, taken while only tests had changed.", strings.Join(files, ","))
			}
		}
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
