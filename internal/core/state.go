package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// Phases a task moves through. Only your checkpoint answers move it past
// understanding and planning.
const (
	PhaseUnderstanding = "understanding" // reading the task and the code
	PhasePlanning      = "planning"      // optional checkpoint 1 approved
	PhaseBuilding      = "building"      // the plan was approved at the checkpoint
	PhaseDone          = "done"
	PhaseCancelled     = "cancelled"
)

type Lookup struct {
	At    time.Time `json:"at"`
	Count int       `json:"count"`
}

type Read struct {
	Hash string    `json:"hash"`
	At   time.Time `json:"at"`
}

type Sendback struct {
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

type Waiver struct {
	Check  string    `json:"check"`
	Reason string    `json:"reason"`
	Print  string    `json:"fingerprint"` // lapses when the code changes
	At     time.Time `json:"at"`
}

type Task struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Ticket    string    `json:"ticket,omitempty"`
	Size      string    `json:"size"` // small | normal | big
	SizeWhy   string    `json:"size_why,omitempty"`
	Root      string    `json:"root"`
	Branch    string    `json:"branch"`
	SessionID string    `json:"session_id"`
	Phase     string    `json:"phase"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`

	// Your own unsaved work at task start: never counted as the task's.
	Preexisting map[string]string `json:"preexisting,omitempty"`

	Lookups  map[string]Lookup `json:"lookups,omitempty"` // socraticode, mempalace, serena, context7
	ToolDown map[string]string `json:"tool_down,omitempty"`
	Reads    map[string]Read   `json:"reads,omitempty"`

	Checkpoint1At time.Time `json:"checkpoint1_at,omitempty"`
	Checkpoint2At time.Time `json:"checkpoint2_at,omitempty"`
	PlanHash      string    `json:"plan_hash,omitempty"`
	PlanFiles     []string  `json:"plan_files,omitempty"` // repo-relative
	ProofKinds    []string  `json:"proof_kinds,omitempty"`
	ApprovedCmds  []string  `json:"approved_cmds,omitempty"` // "Needs your OK" list in the approved plan
	// The test files the approved plan's cases link to, kept at approval so
	// deleting a case from plan.md later doesn't drop its test.
	CaseTests   []string `json:"case_tests,omitempty"`
	CasesStored bool     `json:"cases_stored,omitempty"`

	// Files touched outside the plan (allowed, must be in the report) and
	// files Claude was refused (must be under "For you" in the report).
	OutsidePlan map[string]string `json:"outside_plan,omitempty"`
	Held        map[string]string `json:"held,omitempty"`

	// Commit the task started from: the reviewer diffs against it, so slice
	// commits made during the task are reviewed too.
	StartCommit string `json:"start_commit,omitempty"`
	// Challenger and reviewer runs, as recorded by the SubagentStart and
	// SubagentStop hooks.
	Helpers []HelperRun `json:"helpers,omitempty"`
	// Reviewers the approved plan's Team names, kept at approval.
	Reviewers []string `json:"reviewers,omitempty"`

	LastCodeChange time.Time  `json:"last_code_change,omitempty"`
	Sendbacks      []Sendback `json:"sendbacks,omitempty"`
	Waivers        []Waiver   `json:"waivers,omitempty"`
	Reported       bool       `json:"reported,omitempty"`
	Commits        int        `json:"commits,omitempty"`
}

// NewID makes a short, sortable id.
func NewID() string { return newID() }

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return Now().Format("0102") + "-" + hex.EncodeToString(b)
}

func statePath(root, id string) string { return filepath.Join(TaskDir(root, id), "state.json") }

// legacySealPath: before 0.3.1 the seal was a second file, written by a
// second rename. A hook that read between the two renames saw the old seal
// beside the new state and called the task tampered.
func legacySealPath(root, id string) string { return filepath.Join(TaskDir(root, id), "state.seal") }

// sealLine starts state.json; the task's JSON follows it in the same file, so
// one rename replaces both together.
const sealLine = "plumbline-seal: "

// sealKey is a per-laptop key kept beside the data. It does not stop a
// determined edit, but any edit made outside Plumbline is detected.
func sealKey() []byte {
	p := filepath.Join(DataDir(), ".key")
	b, err := os.ReadFile(p)
	if err == nil && len(b) >= 32 {
		return b
	}
	b = make([]byte, 32)
	_, _ = rand.Read(b)
	_ = writeFileAtomic(p, b)
	return b
}

func seal(data []byte) string {
	h := sha256.New()
	h.Write(sealKey())
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

var ErrTampered = errors.New("task state was changed outside Plumbline")

func LoadTask(root, id string) (*Task, error) {
	raw, err := os.ReadFile(statePath(root, id))
	if err != nil {
		return nil, err
	}
	var b []byte
	var want string
	if rest, ok := strings.CutPrefix(string(raw), sealLine); ok {
		line, data, _ := strings.Cut(rest, "\n")
		want, b = line, []byte(data)
	} else {
		s, _ := os.ReadFile(legacySealPath(root, id))
		want, b = strings.TrimSpace(string(s)), raw
	}
	if want != seal(b) {
		return nil, ErrTampered
	}
	var t Task
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	for i := range t.Helpers {
		if t.Helpers[i].Kind == legacyReviewer {
			t.Helpers[i].Kind = HelperCodeReviewer
		}
	}
	return &t, nil
}

func SaveTask(t *Task) error {
	t.Updated = Now()
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	out := append([]byte(sealLine+seal(b)+"\n"), b...)
	if err := writeFileAtomic(statePath(t.Root, t.ID), out); err != nil {
		return err
	}
	_ = os.Remove(legacySealPath(t.Root, t.ID))
	return nil
}

// ListTasks returns every task of the project, newest first.
func ListTasks(root string) []*Task {
	dirs, _ := os.ReadDir(filepath.Join(ProjectDir(root), "tasks"))
	var out []*Task
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		t, err := LoadTask(root, d.Name())
		if err != nil {
			if errors.Is(err, ErrTampered) {
				out = append(out, &Task{ID: d.Name(), Root: root, Phase: "tampered"})
			}
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

func (t *Task) Open() bool {
	return t.Phase != PhaseDone && t.Phase != PhaseCancelled && t.Phase != "tampered"
}

// sessions/<session>.json binds a session to its task.
func sessionPath(sessionID string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+".json")
}

type SessionBinding struct {
	TaskID string `json:"task_id"`
	Root   string `json:"root"`
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "none"
	}
	return s
}

func BindSession(sessionID string, t *Task) error {
	b, _ := json.Marshal(SessionBinding{TaskID: t.ID, Root: t.Root})
	return writeFileAtomic(sessionPath(sessionID), b)
}

// ActiveTask finds the open task for this session in this repo. Without a
// session id (the CLI run through Bash), it picks the open task on the
// current branch, and asks for --task when there are several.
func ActiveTask(root, sessionID, explicit string) (*Task, error) {
	if root == "" {
		return nil, nil
	}
	if explicit != "" {
		t, err := LoadTask(root, explicit)
		if err != nil {
			return nil, err
		}
		if !t.Open() {
			return nil, fmt.Errorf("task %s is %s", t.ID, t.Phase)
		}
		return t, nil
	}
	if sessionID != "" {
		if b, err := os.ReadFile(sessionPath(sessionID)); err == nil {
			var sb SessionBinding
			if json.Unmarshal(b, &sb) == nil && sb.Root == root {
				t, err := LoadTask(root, sb.TaskID)
				if err != nil {
					return nil, err
				}
				if t.Open() {
					return t, nil
				}
			}
		}
		return nil, nil
	}
	branch := CurrentBranch(root)
	var match []*Task
	for _, t := range ListTasks(root) {
		if t.Phase == "tampered" {
			return nil, ErrTampered
		}
		if t.Open() && t.Branch == branch {
			match = append(match, t)
		}
	}
	if len(match) == 1 {
		return match[0], nil
	}
	if len(match) > 1 {
		ids := []string{}
		for _, m := range match {
			ids = append(ids, m.ID)
		}
		return nil, fmt.Errorf("several open tasks on %s (%s): pass --task <id>", branch, strings.Join(ids, ", "))
	}
	return nil, nil
}

// The Jira flag: the user's latest message asked for something to go to
// Jira. Only the prompt hook writes it, from the user's own words; it's
// sealed like the paused flag, and the next message replaces it.
func jiraAskedPath(sessionID string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+".jira")
}

func jiraSeal(sessionID, at string) string { return seal([]byte("jira\x00" + sessionID + "\x00" + at)) }

var reJiraAsk = regexp.MustCompile(`(?i)\b(jira|ticket|tickets|comment|comments|status|transition|post|posting)\b`)

// UserAsksJira: the user's own message talks about writing to Jira.
func UserAsksJira(prompt string) bool { return reJiraAsk.MatchString(prompt) }

func SetJiraAsked(sessionID string, on bool) error {
	if sessionID == "" {
		return nil
	}
	if on {
		at := Now().Format(time.RFC3339Nano)
		return writeFileAtomic(jiraAskedPath(sessionID), []byte(at+" "+jiraSeal(sessionID, at)+"\n"))
	}
	err := os.Remove(jiraAskedPath(sessionID))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func JiraAsked(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	b, err := os.ReadFile(jiraAskedPath(sessionID))
	if err != nil {
		return false
	}
	at, s, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	return ok && s == jiraSeal(sessionID, at)
}

// Project-level flags.
func pausedPath(root string) string { return filepath.Join(ProjectDir(root), "paused") }

// The paused flag is sealed: a flag file made any other way (a touch from
// the terminal) doesn't pause anything.
func pausedSeal(root, at string) string { return seal([]byte("paused\x00" + root + "\x00" + at)) }

func IsPaused(root string) bool {
	if root == "" {
		return false
	}
	b, err := os.ReadFile(pausedPath(root))
	if err != nil {
		return false
	}
	at, s, ok := strings.Cut(strings.TrimSpace(string(b)), " ")
	return ok && s == pausedSeal(root, at)
}

func SetPaused(root string, on bool) error {
	if on {
		at := Now().Format(time.RFC3339)
		return writeFileAtomic(pausedPath(root), []byte(at+" "+pausedSeal(root, at)+"\n"))
	}
	err := os.Remove(pausedPath(root))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (t *Task) Rel(abs string) string {
	r, err := filepath.Rel(t.Root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
}

// Approved reports whether a command is in the approved plan's list.
func (t *Task) Approved(words []string) bool {
	for _, a := range t.ApprovedCmds {
		for _, s := range Segments(a) {
			if wordsEqual(s.Words, words) {
				return true
			}
		}
	}
	return false
}

// OutsidePlanCap: more files than this outside the plan is a plan change,
// so it goes to the user instead of being Claude's call.
const OutsidePlanCap = 3

// OutsideCount counts files changed outside the plan that still exist.
func (t *Task) OutsideCount() int {
	n := 0
	for p := range t.OutsidePlan {
		if !gone(t.Root, p) {
			n++
		}
	}
	return n
}

func (t *Task) NoteOutside(rel, why string) {
	if t.OutsidePlan == nil {
		t.OutsidePlan = map[string]string{}
	}
	if _, ok := t.OutsidePlan[rel]; !ok {
		t.OutsidePlan[rel] = why
	}
}

func (t *Task) InPlan(rel string) bool {
	for _, f := range t.PlanFiles {
		if f == rel {
			return true
		}
		if strings.HasSuffix(f, "/") && strings.HasPrefix(rel, f) {
			return true
		}
		if ok, _ := filepath.Match(f, rel); ok {
			return true
		}
	}
	return false
}
