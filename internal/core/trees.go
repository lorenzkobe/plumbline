package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Two sessions in one checkout (M14). Each session keeps its own snapshot
// of the checkout's changed files, and its next terminal command is blamed
// for whatever changed since. Without sharing, one session's edit showed up
// as the other's change at its next command. So every session's changes
// are recorded per checkout (which session changed which file), folded
// into the other sessions' snapshots, and left out of the other sessions'
// tasks. Tool calls that may change files are recorded while they run: a
// read-only command leaves what changed meanwhile to that call's own
// check, and two commands running at once both count what changed.

// changeMark: one session (and its task, if any) changed the file.
type changeMark struct {
	Session string    `json:"session"`
	Task    string    `json:"task,omitempty"`
	At      time.Time `json:"at"`
}

// busyMark: a tool call that may change files, between its pre and post
// hooks. Paths for an edit; Any for a command, which can change anything.
type busyMark struct {
	Session string    `json:"session"`
	Snap    string    `json:"snap"` // the snapshot id of the session or helper
	Paths   []string  `json:"paths,omitempty"`
	Any     bool      `json:"any,omitempty"`
	At      time.Time `json:"at"`
}

type changeRecord struct {
	Changes map[string][]changeMark `json:"changes,omitempty"`
	Busy    map[string]busyMark     `json:"busy,omitempty"`
}

// changeMarkFor: how long who-changed-what is kept. A task open longer sees
// older changes as its own again (fail closed).
const changeMarkFor = 30 * 24 * time.Hour

// maxMarksPerFile keeps a file's list short: one per session and task.
const maxMarksPerFile = 8

func treeChangesPath(root string) string {
	return filepath.Join(ProjectDir(root), "trees", treeKey(root)+".json")
}

func lockTree(root string) func() {
	return lockFile(filepath.Join(ProjectDir(root), "trees", treeKey(root)+".lock"))
}

// treeSeal ties the record to its checkout, so one copied from another
// checkout doesn't verify.
func treeSeal(root string, b []byte) string {
	return seal(append([]byte("tree\x00"+treeKey(root)+"\x00"), b...))
}

// loadTreeChanges reads the checkout's record. A missing, unreadable or
// edited one counts as empty: then no file is left out of a task and no
// change waits for another session (fail closed).
func loadTreeChanges(root string) *changeRecord {
	tc := &changeRecord{Changes: map[string][]changeMark{}, Busy: map[string]busyMark{}}
	raw, err := os.ReadFile(treeChangesPath(root))
	if err != nil {
		return tc
	}
	rest, ok := strings.CutPrefix(string(raw), sealLine)
	if !ok {
		return tc
	}
	line, data, _ := strings.Cut(rest, "\n")
	if line != treeSeal(root, []byte(data)) {
		return tc
	}
	var got changeRecord
	if json.Unmarshal([]byte(data), &got) != nil {
		return tc
	}
	if got.Changes != nil {
		tc.Changes = got.Changes
	}
	if got.Busy != nil {
		tc.Busy = got.Busy
	}
	return tc
}

func saveTreeChanges(root string, tc *changeRecord) {
	now := Now()
	for p, marks := range tc.Changes {
		keep := marks[:0]
		for _, m := range marks {
			if now.Sub(m.At) < changeMarkFor {
				keep = append(keep, m)
			}
		}
		if len(keep) == 0 {
			delete(tc.Changes, p)
		} else {
			tc.Changes[p] = keep
		}
	}
	for k, b := range tc.Busy {
		if now.Sub(b.At) > leadBusyFor {
			delete(tc.Busy, k) // refused by the user, or its hook never ran
		}
	}
	b, err := json.Marshal(tc)
	if err != nil {
		return
	}
	_ = writeFileAtomic(treeChangesPath(root), append([]byte(sealLine+treeSeal(root, b)+"\n"), b...))
}

// mark records that m's session changed p, replacing its earlier mark.
func (tc *changeRecord) mark(p string, m changeMark) {
	marks := tc.Changes[p]
	for i := range marks {
		if marks[i].Session == m.Session && marks[i].Task == m.Task {
			marks = append(marks[:i], marks[i+1:]...)
			break
		}
	}
	marks = append(marks, m)
	if len(marks) > maxMarksPerFile {
		marks = marks[len(marks)-maxMarksPerFile:]
	}
	tc.Changes[p] = marks
}

// othersBusy: the files other sessions' running tool calls name, and
// whether one of them is a command that can change any file.
func (tc *changeRecord) othersBusy(session string) (map[string]bool, bool) {
	paths, anyFile := map[string]bool{}, false
	for _, b := range tc.Busy {
		if b.Session == session || Now().Sub(b.At) > leadBusyFor {
			continue
		}
		anyFile = anyFile || b.Any
		for _, p := range b.Paths {
			paths[p] = true
		}
	}
	return paths, anyFile
}

// busySnaps: the snapshots whose session or helper has a call running.
func (tc *changeRecord) busySnaps() map[string]bool {
	out := map[string]bool{}
	for _, b := range tc.Busy {
		if Now().Sub(b.At) <= leadBusyFor {
			out[safeName(b.Snap)] = true
		}
	}
	return out
}

func busyKey(snap, toolUseID string) string { return safeName(snap) + "/" + toolUseID }

// otherSnaps: the snapshot files of this checkout that belong to other
// sessions (and their helpers), by snapshot id.
func otherSnaps(root, session string) map[string]string {
	suffix := "." + treeKey(root) + ".snap.json"
	files, _ := filepath.Glob(filepath.Join(DataDir(), "sessions", "*"+suffix))
	own := safeName(session)
	out := map[string]string{}
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), suffix)
		if id == own || strings.HasPrefix(id, own+"-") {
			continue
		}
		out[id] = f
	}
	return out
}

// editPath: the file an edit tool writes.
func (in *HookInput) editPath() string {
	if p := in.Str("file_path"); p != "" {
		return p
	}
	return in.Str("notebook_path")
}

func isEditTool(name string) bool {
	return name == "Edit" || name == "Write" || name == "MultiEdit" || name == "NotebookEdit"
}

// readOnlyLine: every part of the command was understood and only reads.
func readOnlyLine(segs []Segment) bool {
	if len(segs) == 0 {
		return false
	}
	for _, s := range segs {
		if s.Opaque != "" || !ReadOnly(s) {
			return false
		}
	}
	return true
}

// backgroundPath: when this session (the lead or any of its helpers) first
// started a command that may write and keeps running after its hooks
// (run_in_background, Monitor).
func backgroundPath(session, root string) string {
	return filepath.Join(DataDir(), "sessions", safeName(session)+"."+treeKey(root)+".background")
}

// noteBackground keeps the first start: its end is never seen, so every
// change from then on may be its doing.
func (c *hookCtx) noteBackground() {
	p := backgroundPath(c.in.SessionID, c.root)
	if _, err := os.Lstat(p); err == nil {
		return
	}
	_ = writeFileAtomic(p, []byte(Now().Format(time.RFC3339Nano)))
}

// launchesBackground: this call ran a command that may write and keeps
// running after its hooks: run_in_background, a Monitor, a line that ends a
// part with &, or a call Claude Code moved to the background (its timeout,
// or ctrl+b), whose result carries the task id. Asked in the post hook, so a
// call the user declined counts for nothing.
func (c *hookCtx) launchesBackground(segs []Segment) bool {
	cmd := c.in.Str("command")
	if (c.in.ToolName != "Bash" && c.in.ToolName != "Monitor") || strings.TrimSpace(cmd) == "" || readOnlyLine(segs) {
		return false
	}
	if bg, _ := c.in.Input()["run_in_background"].(bool); bg || c.in.ToolName == "Monitor" || HasBackground(cmd) {
		return true
	}
	var resp map[string]any
	if json.Unmarshal(c.in.ToolResponse, &resp) == nil {
		if id, _ := resp["backgroundTaskId"].(string); id != "" {
			return true
		}
	}
	return false
}

// backgroundSince: when this session's first background command that may
// write started. A record that can't be read counts from the beginning of
// time (fails closed).
func (c *hookCtx) backgroundSince() (time.Time, bool) {
	b, err := os.ReadFile(backgroundPath(c.in.SessionID, c.root))
	if os.IsNotExist(err) {
		return time.Time{}, false
	}
	at, perr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
	if err != nil || perr != nil {
		return time.Time{}, true
	}
	return at, true
}

// startsPath: when this session's read-only commands started, by tool use
// id, so their changed-files check can tell what changed before they ran.
func startsPath(snap, root string) string {
	return filepath.Join(DataDir(), "sessions", safeName(snap)+"."+treeKey(root)+".starts.json")
}

// noteStart records a read-only command's start (CC2).
func (c *hookCtx) noteStart() {
	unlock := lockTree(c.root)
	defer unlock()
	p := startsPath(c.snapID(), c.root)
	m := map[string]time.Time{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	for id, at := range m {
		if Now().Sub(at) > time.Hour { // a call whose post hook never ran
			delete(m, id)
		}
	}
	m[c.in.ToolUseID] = Now()
	b, _ := json.Marshal(m)
	_ = writeFileAtomic(p, b)
}

// takeStart returns and forgets a read-only command's start.
func (c *hookCtx) takeStart() time.Time {
	if c.in.ToolUseID == "" {
		return time.Time{}
	}
	unlock := lockTree(c.root)
	defer unlock()
	p := startsPath(c.snapID(), c.root)
	m := map[string]time.Time{}
	b, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(b, &m) != nil {
		return time.Time{}
	}
	at, ok := m[c.in.ToolUseID]
	if !ok {
		return time.Time{}
	}
	delete(m, c.in.ToolUseID)
	b, _ = json.Marshal(m)
	_ = writeFileAtomic(p, b)
	return at
}

// changedAfter keeps the files whose last change came at or after start. A
// read-only command or a saved check can't have changed a file before it
// ran: that was the user (a ! command, an editor) between Claude's commands
// (CC2). The change time counts too: a move, cp -p or unzip keeps an old
// modified time but not an old change time. The link itself is looked at,
// never what it points to. A removed file has no time of its own, so the
// nearest folder still there decides: removing an entry changes it.
func changedAfter(root string, files []string, start time.Time) []string {
	var keep []string
	for _, p := range files {
		abs := filepath.Join(root, filepath.FromSlash(p))
		fi, err := os.Lstat(abs)
		for err != nil && os.IsNotExist(err) && abs != root && IsInside(abs, root) {
			abs = filepath.Dir(abs)
			fi, err = os.Lstat(abs)
		}
		if err == nil {
			if last, ok := lastChange(fi); ok && last.Before(start.Add(-500*time.Millisecond)) {
				continue
			}
		}
		keep = append(keep, p)
	}
	return keep
}

// lastChange: the later of the modified and change times. A disk that keeps
// whole seconds (HFS+, ext3) or two (FAT) rounds a time down, so a time with
// no fraction may be up to two seconds later than it reads.
func lastChange(fi os.FileInfo) (time.Time, bool) {
	ct, ok := changeTime(fi)
	if !ok {
		return time.Time{}, false
	}
	return latestChange(fi.ModTime(), ct), true
}

func latestChange(mod, ct time.Time) time.Time {
	last := mod
	if ct.After(last) {
		last = ct
	}
	if last.Nanosecond() == 0 {
		last = last.Add(2 * time.Second)
	}
	return last
}

// startTimedLine: a line whose changed-files check leaves out what changed
// before it started. Each part only reads or is one of the project's saved
// checks, run in this checkout. Anything else (a script, the app, an extra
// allowed command, a part that writes) keeps the full check.
func (c *hookCtx) startTimedLine(segs []Segment) bool {
	if readOnlyLine(segs) {
		return true
	}
	if len(segs) == 0 || c.setup == nil || !c.setupOK {
		return false
	}
	checks := c.setup.savedChecks()
	if len(checks) == 0 {
		return false
	}
	cmd := strings.TrimSpace(c.in.Str("command"))
	for _, s := range checks {
		if cmd == s && foldPath(filepath.Clean(c.in.Cwd)) == foldPath(c.root) {
			return true
		}
	}
	for _, seg := range segs {
		if seg.Opaque == "" && ReadOnly(seg) {
			continue
		}
		if !c.savedCheckSegment(seg, checks) {
			return false
		}
	}
	return true
}

// savedCheckSegment: seg is exactly a saved check that is a single command,
// with nothing added, run at the checkout's top.
func (c *hookCtx) savedCheckSegment(seg Segment, checks []string) bool {
	if seg.Opaque != "" || len(seg.Env) > 0 || seg.unknownArgs() || !redirectsHarmless(seg) || seg.Dir == "" || foldPath(filepath.Clean(seg.Dir)) != foldPath(c.root) {
		return false
	}
	for _, s := range checks {
		ss := Segments(s)
		if len(ss) == 1 && wordsEqual(ss[0].Words, seg.Words) {
			return true
		}
	}
	return false
}

// noteBusy records a tool call that may change files, before it runs, so
// another session's read-only command leaves its changes to its own check.
func (c *hookCtx) noteBusy() {
	if c.root == "" || c.in.ToolUseID == "" {
		return
	}
	var m busyMark
	switch {
	case isEditTool(c.in.ToolName):
		abs := Abs(c.in.editPath(), c.in.Cwd)
		if !IsInside(abs, c.root) {
			return
		}
		m.Paths = []string{c.rel(abs)}
	case c.in.ToolName == "Bash" || c.in.ToolName == "Monitor":
		segs := ParseShell(c.in.Str("command"), c.in.Cwd)
		if readOnlyLine(segs) {
			c.noteStart()
			return
		}
		if c.startTimedLine(segs) {
			c.noteStart() // a saved check may still write: it stays marked running too
		}
		m.Any = true
	case strings.HasPrefix(c.in.ToolName, "mcp__") && c.mcpKind() != "":
		m.Any = true
	default:
		return
	}
	m.Session, m.Snap, m.At = c.in.SessionID, c.snapID(), Now()
	unlock := lockTree(c.root)
	defer unlock()
	tc := loadTreeChanges(c.root)
	tc.Busy[busyKey(m.Snap, c.in.ToolUseID)] = m
	saveTreeChanges(c.root, tc)
}

// takeChanges is the first half of the changed-files check: what changed
// since this session's snapshot, shared with the other sessions. A
// read-only command can't have changed anything, so what another session's
// running call may be changing waits for that call's own check. ambiguous
// holds the files another session changed while this command ran: they
// count for this session too. ok is false when git can't be read.
func (c *hookCtx) takeChanges(readOnly bool) (prev map[string]string, changed []string, ambiguous map[string]bool, ok bool) {
	unlock := lockTree(c.root)
	defer unlock()
	prev = loadSnap(c.snapID(), c.root)
	now, err := DirtyFiles(c.root)
	if err != nil {
		return nil, nil, nil, false
	}
	tc := loadTreeChanges(c.root)
	if prev != nil {
		changed = Changed(c.root, prev, now)
	}
	if readOnly && len(changed) > 0 {
		paths, anyFile := tc.othersBusy(c.in.SessionID)
		keep := changed[:0]
		for _, p := range changed {
			if anyFile || paths[p] {
				if v, had := prev[p]; had { // keep the old stamp: seen again next time
					now[p] = v
				} else {
					delete(now, p)
				}
				continue
			}
			keep = append(keep, p)
		}
		changed = keep
	}
	ambiguous = map[string]bool{}
	if started := tc.Busy[busyKey(c.snapID(), c.in.ToolUseID)].At; !started.IsZero() {
		for _, p := range changed {
			for _, m := range tc.Changes[p] {
				if m.Session != c.in.SessionID && !m.At.Before(started) {
					ambiguous[p] = true
				}
			}
		}
	}
	saveSnap(c.snapID(), c.root, now)
	c.share(tc, changed, now, false)
	return prev, changed, ambiguous, true
}

// shareEdit: after an edit tool, this session's snapshot takes in the
// checkout as it is, and the edited file is shared with the others.
func (c *hookCtx) shareEdit(abs string) {
	if c.root == "" {
		return
	}
	unlock := lockTree(c.root)
	defer unlock()
	now, err := DirtyFiles(c.root)
	if err != nil {
		return
	}
	saveSnap(c.snapID(), c.root, now)
	var changed []string
	if IsInside(abs, c.root) {
		if rel := c.rel(abs); !inWorktreesDir(rel) {
			changed = []string{rel}
		}
	}
	c.share(loadTreeChanges(c.root), changed, now, true)
}

// share ends this call's running mark, records who changed the files, and
// folds them into the other sessions' snapshots. An edit names its file, so
// it's folded in everywhere; a command's changes skip the snapshots of
// sessions whose own command is running, so the change counts for both.
func (c *hookCtx) share(tc *changeRecord, changed []string, now map[string]string, exact bool) {
	delete(tc.Busy, busyKey(c.snapID(), c.in.ToolUseID))
	if len(changed) > 0 {
		m := changeMark{Session: c.in.SessionID, At: Now()}
		if c.task != nil {
			m.Task = c.task.ID
		}
		for _, p := range changed {
			tc.mark(p, m)
		}
		busy := tc.busySnaps()
		for id, f := range otherSnaps(c.root, c.in.SessionID) {
			if !exact && busy[id] {
				continue
			}
			absorbInto(f, changed, now)
		}
	}
	saveTreeChanges(c.root, tc)
}

// absorbInto updates one snapshot file with the current stamps of paths.
func absorbInto(file string, paths []string, now map[string]string) {
	b, err := os.ReadFile(file)
	if err != nil {
		return
	}
	snap := map[string]string{}
	if json.Unmarshal(b, &snap) != nil {
		return
	}
	for _, p := range paths {
		if v, ok := now[p]; ok {
			snap[p] = v
		} else {
			delete(snap, p)
		}
	}
	out, _ := json.Marshal(snap)
	_ = writeFileAtomic(file, out)
}

// otherSessionFiles: files another session changed while this task was
// open, that this task doesn't own (not in its plan, not changed by it
// outside the plan). Marks of the task's own session or helpers, or of the
// task itself after an adopt, are the task's.
func (t *Task) otherSessionFiles() map[string]bool {
	out := map[string]bool{}
	if t == nil || t.Root == "" {
		return out
	}
	for p, marks := range loadTreeChanges(t.Root).Changes {
		if t.InPlan(p) {
			continue
		}
		if _, own := t.OutsidePlan[p]; own {
			continue
		}
		for _, m := range marks {
			if m.Task != t.ID && m.Session != t.SessionID && !m.At.Before(t.Created) {
				out[p] = true
				break
			}
		}
	}
	return out
}

// OthersWork: the files that aren't this task's: the user's own unsaved
// work at its start, and what another session changed while it was open.
func (t *Task) OthersWork() map[string]bool {
	out := t.otherSessionFiles()
	for p := range t.Preexisting {
		out[p] = true
	}
	return out
}

// otherSessionTask: an open task of another session in this checkout. Two
// tasks in one checkout would test each other's half-done code.
func (c *hookCtx) otherSessionTask() *Task {
	if c.root == "" {
		return nil
	}
	here := treeKey(c.root)
	var found []*Task
	for _, t := range ListTasks(c.root) {
		if t.Open() && treeKey(t.Root) == here && t.SessionID != c.in.SessionID {
			found = append(found, t)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Created.Before(found[j].Created) })
	return found[0]
}

// cleanupSnapshots drops snapshots no session touched for a week: a session
// that comes back after that starts its check afresh.
func cleanupSnapshots() {
	files, _ := filepath.Glob(filepath.Join(DataDir(), "sessions", "*.snap.json"))
	cut := time.Now().Add(-7 * 24 * time.Hour)
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil && fi.ModTime().Before(cut) {
			_ = os.Remove(f)
		}
	}
}

// endBusy ends this call's running mark without recording a change: an
// edit that failed changed nothing.
func (c *hookCtx) endBusy() {
	if c.root == "" {
		return
	}
	unlock := lockTree(c.root)
	defer unlock()
	tc := loadTreeChanges(c.root)
	if _, ok := tc.Busy[busyKey(c.snapID(), c.in.ToolUseID)]; ok {
		c.share(tc, nil, nil, true)
	}
}

// PostToolUseFailure runs after a tool call that failed, such as a command
// that exited non-zero (Claude Code skips PostToolUse for those). A failed
// command may still have changed files, so the same changed-files check
// runs now, and the call's running marks end, so neither another session
// nor this task's builders keep waiting on it.
func PostToolUseFailure(in *HookInput) Output {
	c := newCtx(in)
	defer c.close()
	if c.root == "" {
		return nil
	}
	if in.AgentID == "" && in.ToolName != "Read" && in.ToolName != "AskUserQuestion" {
		defer c.absorbIntoBuilders() // runs before close: still under the lock
	}
	var o Output
	switch {
	case isEditTool(in.ToolName):
		c.endBusy()
	case in.ToolName == "Bash" || in.ToolName == "Monitor":
		segs := ParseShell(in.Str("command"), in.Cwd)
		if in.ToolName == "Bash" {
			moved := c.followHead(segs, true) // git checkout main && npm test moved HEAD although the line failed
			// A read whose line ended in a grep that found nothing still
			// showed Claude those lines (RD1).
			if in.AgentID == "" {
				c.noteShellReads(segs)
				if onlyReads(segs) {
					c.noteSearchHits(responseText(in.ToolResponse)+"\n"+in.Error, shellDirs(segs, in.Cwd))
				}
			} else {
				c.noteHelperOpened(shellReadFiles(segs)...)
			}
			o = c.withTrackerCheck(c.withMainCheck(c.checkChanged(segs)))
			if moved != "" {
				o = addContext(o, moved)
			}
			break
		}
		o = c.withTrackerCheck(c.withMainCheck(c.checkChanged(segs)))
	case strings.HasPrefix(in.ToolName, "mcp__") && c.mcpKind() != "":
		o = c.withTrackerCheck(c.withMainCheck(c.checkChanged(nil)))
	}
	if h, ok := o["hookSpecificOutput"].(map[string]any); ok {
		h["hookEventName"] = "PostToolUseFailure"
	}
	return o
}

// endTurnBusy: at the end of the lead's turn its own tool calls are over,
// including one you declined at a prompt (no post hook runs for that), so
// their running marks end. Background helpers keep theirs.
func (c *hookCtx) endTurnBusy() {
	if c.root == "" || c.in.AgentID != "" {
		return
	}
	unlock := lockTree(c.root)
	defer unlock()
	tc := loadTreeChanges(c.root)
	own, ended := safeName(c.in.SessionID), false
	for k, b := range tc.Busy {
		if safeName(b.Snap) == own {
			delete(tc.Busy, k)
			ended = true
		}
	}
	if ended {
		saveTreeChanges(c.root, tc)
	}
}
