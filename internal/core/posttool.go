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

func snapPath(sessionID string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+".snap.json")
}

func loadSnap(sessionID string) map[string]string {
	b, err := os.ReadFile(snapPath(sessionID))
	if err != nil {
		return nil
	}
	m := map[string]string{}
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return m
}

func saveSnap(sessionID string, m map[string]string) {
	b, _ := json.Marshal(m)
	_ = writeFileAtomic(snapPath(sessionID), b)
}

// RefreshSnapshot records the dirty files as they are now, so the next
// terminal-command check only sees what changed after this point.
func RefreshSnapshot(sessionID, root string) {
	if root == "" {
		return
	}
	if d, err := DirtyFiles(root); err == nil {
		saveSnap(sessionID, d)
	}
}

var reTaskStarted = regexp.MustCompile(`plumbline-task-id: ([0-9a-f-]+)`)

// PostToolUse records what happened: files read, lookups made, code changed,
// checkpoint answers, and files changed by terminal commands.
func PostToolUse(in *HookInput) Output {
	c := newCtx(in)
	if c.root == "" {
		return nil
	}
	switch {
	case in.ToolName == "Read":
		if c.task != nil {
			abs := Abs(in.Str("file_path"), in.Cwd)
			if h := fileHash(abs); h != "" {
				if c.task.Reads == nil {
					c.task.Reads = map[string]Read{}
				}
				c.task.Reads[abs] = Read{Hash: h, At: Now()}
				_ = SaveTask(c.task)
			}
		}
	case in.ToolName == "Edit" || in.ToolName == "Write" || in.ToolName == "MultiEdit" || in.ToolName == "NotebookEdit":
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		abs := Abs(p, in.Cwd)
		if abs == filepath.Join(c.root, RepoSetupFile) {
			_ = ConfirmSetup(c.root) // you allowed the change on the confirm prompt
		}
		if c.task != nil && IsInside(abs, c.root) {
			if rel := c.task.Rel(abs); c.task.Phase == PhaseBuilding && !c.task.InPlan(rel) {
				c.task.NoteOutside(rel, "edited")
			}
			c.task.LastCodeChange = Now()
			c.task.Reported = false
			if c.task.Reads != nil {
				if h := fileHash(abs); h != "" {
					c.task.Reads[abs] = Read{Hash: h, At: Now()} // Claude just wrote it, so it knows it
				}
			}
			_ = SaveTask(c.task)
		}
		RefreshSnapshot(in.SessionID, c.root)
	case in.ToolName == "Bash":
		return c.afterBash()
	case in.ToolName == "AskUserQuestion":
		return c.afterQuestion()
	case strings.HasPrefix(in.ToolName, "mcp__"):
		if c.task != nil {
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
	low := strings.ToLower(tool)
	for _, f := range []string{"socraticode", "mempalace", "serena", "context7"} {
		if strings.Contains(low, f) {
			return f
		}
	}
	return ""
}

func (c *hookCtx) afterBash() Output {
	in := c.in
	cmd := in.Str("command")
	out := responseText(in.ToolResponse)
	// Bind a task that was just started to this session.
	if m := reTaskStarted.FindStringSubmatch(out); m != nil && strings.Contains(cmd, "plumbline") {
		if t, err := LoadTask(c.root, m[1]); err == nil && t.SessionID == "" {
			t.SessionID = in.SessionID
			_ = SaveTask(t)
			_ = BindSession(in.SessionID, t)
			c.task = t
		}
	}
	if m := reAdopted.FindStringSubmatch(out); m != nil && strings.Contains(cmd, "plumbline") {
		if t, err := LoadTask(c.root, m[1]); err == nil && t.Open() {
			t.SessionID = in.SessionID
			_ = SaveTask(t)
			_ = BindSession(in.SessionID, t)
			c.task = t
		}
	}
	if c.task != nil && strings.Contains(cmd, "git") && strings.Contains(cmd, "commit") && !strings.Contains(out, "nothing to commit") {
		for _, s := range Segments(cmd) {
			if sub, _ := GitSub(s); sub == "commit" {
				c.task.Commits++
				_ = SaveTask(c.task)
				c.log("commit", "commit", "")
				break
			}
		}
	}
	// Changed-files check.
	prev := loadSnap(in.SessionID)
	now, err := DirtyFiles(c.root)
	if err != nil {
		return nil
	}
	saveSnap(in.SessionID, now)
	if prev == nil || c.paused {
		return nil
	}
	var changed []string
	for _, p := range Changed(prev, now) {
		if c.setup != nil && MatchAny(c.setup.IgnoreChanges, p) {
			continue
		}
		changed = append(changed, p)
	}
	if len(changed) == 0 {
		return nil
	}
	if c.task != nil {
		c.task.LastCodeChange = Now()
		c.task.Reported = false
		_ = SaveTask(c.task)
	}
	if c.task == nil || c.task.Phase != PhaseBuilding {
		c.log("block", "changed-before-approval", fmt.Sprintf("%d files", len(changed)))
		return StopAll(msg(fmt.Sprintf("a terminal command changed files before any plan was approved: %s. The work is stopped so you can look. If you changed these yourself, say so and Claude carries on. Otherwise tell Claude whether to keep or undo them.", listFiles(changed))))
	}
	// During building the user is often away: record and tell Claude, don't stop.
	var outside, theirs []string
	for _, p := range changed {
		if _, mine := c.task.Preexisting[p]; mine {
			theirs = append(theirs, p)
			if c.task.Held == nil {
				c.task.Held = map[string]string{}
			}
			c.task.Held[p] = "a terminal command changed it, and it held the user's own unsaved work"
			continue
		}
		if !c.task.InPlan(p) {
			outside = append(outside, p)
			c.task.NoteOutside(p, "changed by a terminal command")
		}
	}
	if len(outside)+len(theirs) == 0 {
		return nil
	}
	_ = SaveTask(c.task)
	c.log("note", "changed-outside-plan", fmt.Sprintf("%d files", len(outside)+len(theirs)))
	var b strings.Builder
	if len(theirs) > 0 {
		fmt.Fprintf(&b, "that command changed files that held the user's own unsaved work: %s. Don't change them further; put them first under \"For you\" in the report. ", listFiles(theirs))
	}
	if len(outside) > 0 && c.task.OutsideCount() > OutsidePlanCap {
		fmt.Fprintf(&b, "that command changed files outside the approved plan: %s. That's more than %d files outside the plan, so the plan was likely wrong: stop and ask the user (with your suggestion) whether to go back to the plan (plumbline task replan) or undo these.", listFiles(outside), OutsidePlanCap)
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

// findAnswer looks for the user's answer to a question in the tool response,
// whatever shape Claude Code uses for it.
func findAnswer(raw json.RawMessage, question string) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if a := walkAnswer(v, question); a != "" {
			return a
		}
	}
	text := responseText(raw)
	// "Question"="Answer" style
	q := regexp.QuoteMeta(question)
	if m := regexp.MustCompile(`"` + q + `"\s*=\s*"([^"]*)"`).FindStringSubmatch(text); m != nil {
		return m[1]
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
			t.PlanHash = sha256Hex([]byte(text))
		default:
			continue
		}
		_ = SaveTask(t)
		c.log("phase", t.Phase, "")
		RefreshSnapshot(c.in.SessionID, c.root)
		next := "Make the plan next (plan.md), then the checkpoint."
		if t.Phase == PhaseBuilding {
			next = "Build and prove it to the end without stopping: a failing test first, then the fix, then the proof and the report. Don't wait on the user; anything that needs them goes under \"For you\" in the report."
		}
		return Context("PostToolUse", msg(fmt.Sprintf("the user approved. Task %s is now at the %s step. %s", t.ID, t.Phase, next)))
	}
	return nil
}
