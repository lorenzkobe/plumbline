package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Run kinds.
const (
	KindTest    = "test"    // the full suite
	KindRelated = "related" // tests that touch the changed code
	KindLint    = "lint"
	KindBuild   = "build"
	KindE2E     = "e2e"
	KindProof   = "proof" // real-world proof: ui, api, schema (Sub)
)

// Run is one recorded command run. Plumbline runs the command itself, so the
// record shows what really happened. Output is never stored, only counts.
type Run struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id,omitempty"`
	Kind        string    `json:"kind"`
	Sub         string    `json:"sub,omitempty"`
	Command     string    `json:"command"`
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished,omitempty"`
	Exit        int       `json:"exit"`
	PrintBefore string    `json:"print_before"`
	PrintAfter  string    `json:"print_after,omitempty"`
	Ran         int       `json:"ran"`    // tests that ran; -1 when the output could not be read
	Failed      int       `json:"failed"` // failing tests, or problems for lint/build
	FailedIDs   []string  `json:"failed_ids,omitempty"`
	Problems    int       `json:"problems,omitempty"`
	Baseline    bool      `json:"baseline,omitempty"` // run before any task edit
}

// Complete is true only when the run finished and the code did not change
// while it ran. A run that started but never finished is not proof.
func (r *Run) Complete() bool {
	return !r.Finished.IsZero() && r.PrintAfter != "" && r.PrintAfter == r.PrintBefore
}

func runsPath(root string) string { return filepath.Join(ProjectDir(root), "runs.jsonl") }

func appendRun(root string, r *Run) error {
	p := runsPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.Marshal(r)
	// Each record carries a seal made with the laptop key, so a line added by
	// hand, or by a version before 0.4.1, never counts as proof. Like the
	// task-state seal it doesn't stop a determined process that reads the
	// key (see sealKey).
	_, err = f.Write([]byte(seal(b) + " " + string(b) + "\n"))
	return err
}

// LoadRuns returns the latest record of every run id. Records without a
// valid seal (added by hand, or by a version before 0.4.1) are skipped, so
// they never count as proof.
func LoadRuns(root string) []*Run {
	f, err := os.Open(runsPath(root))
	if err != nil {
		return nil
	}
	defer f.Close()
	byID := map[string]*Run{}
	order := []string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		mark, data, ok := strings.Cut(sc.Text(), " ")
		if !ok || mark != seal([]byte(data)) {
			continue
		}
		var r Run
		if json.Unmarshal([]byte(data), &r) != nil {
			continue
		}
		if _, ok := byID[r.ID]; !ok {
			order = append(order, r.ID)
		}
		rr := r
		byID[r.ID] = &rr
	}
	out := make([]*Run, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id])
	}
	return out
}

// Execute runs a saved command through the shell, streams its output to the
// terminal, and records the result.
func Execute(root string, t *Task, kind, sub, command string, setup *Setup, out io.Writer) (*Run, error) {
	ignore := []string(nil)
	if setup != nil {
		ignore = setup.IgnoreChanges
	}
	before, err := Fingerprint(root, ignore)
	if err != nil {
		return nil, err
	}
	r := &Run{ID: newID() + "-" + kind, Kind: kind, Sub: sub, Command: command, Started: Now(), PrintBefore: before, Ran: -1}
	if t != nil {
		r.TaskID = t.ID
		r.Baseline = t.Phase != PhaseBuilding && t.LastCodeChange.IsZero()
	}
	if err := appendRun(root, r); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = root
	cmd.Stdout = io.MultiWriter(out, &buf)
	cmd.Stderr = io.MultiWriter(out, &buf)
	cmd.Env = os.Environ()
	runErr := cmd.Run()
	r.Exit = 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			r.Exit = ee.ExitCode()
		} else {
			r.Exit = 127
		}
	}
	r.Finished = Now()
	r.PrintAfter, _ = Fingerprint(root, ignore)
	text := buf.String()
	switch kind {
	case KindTest, KindRelated, KindE2E:
		pat := ""
		if setup != nil {
			pat = setup.TestsRanPattern
		}
		r.Ran, r.Failed, r.FailedIDs = ParseTests(text, pat)
		if r.Exit != 0 && r.Failed == 0 && r.Ran >= 0 {
			r.Failed = 1 // non-zero exit with no failure count: treat as failing
		}
	case KindLint, KindBuild:
		r.Problems = CountProblems(text)
		r.Ran = 1
	default:
		r.Ran = 1
	}
	return r, appendRun(root, r)
}

var (
	reJest       = regexp.MustCompile(`(?m)^Tests:\s+(.*?)(\d+) total`)
	reJestFailed = regexp.MustCompile(`(\d+) failed`)
	reVitest     = regexp.MustCompile(`(?m)^\s*Tests\s+(.*)\((\d+)\)`)
	rePytest     = regexp.MustCompile(`(?m)^=+ (.*?) in [\d.]+s`)
	rePyCount    = regexp.MustCompile(`(\d+) (passed|failed|error|errors|xfailed|xpassed)`)
	rePyFailedID = regexp.MustCompile(`(?m)^(?:FAILED|ERROR) (\S+)`)
	reGoOK       = regexp.MustCompile(`(?m)^ok\s+\S+\s+(?:\(cached\)|[\d.]+s)(.*)$`)
	reGoFailPkg  = regexp.MustCompile(`(?m)^FAIL\s+\S+\s+[\d.]+s`)
	reGoFailID   = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)
	reGoRun      = regexp.MustCompile(`(?m)^=== RUN\s+(\S+)`)
	reMocha      = regexp.MustCompile(`(?m)^\s*(\d+) passing`)
	reMochaFail  = regexp.MustCompile(`(?m)^\s*(\d+) failing`)
	rePHPUnitOK  = regexp.MustCompile(`OK \((\d+) tests?`)
	rePHPUnit    = regexp.MustCompile(`Tests: (\d+), Assertions: \d+(?:, (?:Errors|Failures): (\d+))?(?:, (?:Errors|Failures): (\d+))?`)
	reRSpec      = regexp.MustCompile(`(\d+) examples?, (\d+) failures?`)
	reCargo      = regexp.MustCompile(`test result: \w+\. (\d+) passed; (\d+) failed`)
	rePlaywright = regexp.MustCompile(`(?m)^\s*(\d+) passed \(`)
	rePWFailed   = regexp.MustCompile(`(?m)^\s*(\d+) failed`)
	reJestFailID = regexp.MustCompile(`(?m)^\s*● (.+?) › (.+)$`)
	reProblems   = regexp.MustCompile(`(?i)\b(error|warning)\b`)
)

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// ParseTests reads test output and returns how many tests ran, how many
// failed, and the failing test names when the format shows them. ran is -1
// when the output format is unknown: that is never counted as proof.
func ParseTests(text, custom string) (ran, failed int, ids []string) {
	if custom != "" {
		if re, err := regexp.Compile(custom); err == nil {
			if m := re.FindStringSubmatch(text); m != nil {
				for i, n := range re.SubexpNames() {
					switch n {
					case "ran":
						ran = atoi(m[i])
					case "failed":
						failed = atoi(m[i])
					}
				}
				return ran, failed, nil
			}
		}
	}
	if m := reJest.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		ran = atoi(last[2])
		if f := reJestFailed.FindStringSubmatch(last[1]); f != nil {
			failed = atoi(f[1])
		}
		for _, x := range reJestFailID.FindAllStringSubmatch(text, -1) {
			ids = append(ids, strings.TrimSpace(x[1])+" › "+strings.TrimSpace(x[2]))
		}
		return ran, failed, uniq(ids)
	}
	if m := reVitest.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		ran = atoi(last[2])
		if f := reJestFailed.FindStringSubmatch(last[1]); f != nil {
			failed = atoi(f[1])
		}
		return ran, failed, nil
	}
	if m := rePytest.FindAllStringSubmatch(text, -1); m != nil {
		summary := m[len(m)-1][1]
		if strings.Contains(summary, "no tests ran") {
			return 0, 0, nil
		}
		found := false
		for _, c := range rePyCount.FindAllStringSubmatch(summary, -1) {
			found = true
			n := atoi(c[1])
			ran += n
			if c[2] == "failed" || strings.HasPrefix(c[2], "error") {
				failed += n
			}
		}
		if found {
			for _, x := range rePyFailedID.FindAllStringSubmatch(text, -1) {
				ids = append(ids, x[1])
			}
			return ran, failed, uniq(ids)
		}
	}
	if m := rePHPUnitOK.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), 0, nil
	}
	if m := rePHPUnit.FindStringSubmatch(text); m != nil {
		return atoi(m[1]), atoi(m[2]) + atoi(m[3]), nil
	}
	if m := reRSpec.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		return atoi(last[1]), atoi(last[2]), nil
	}
	if m := reCargo.FindAllStringSubmatch(text, -1); m != nil {
		for _, x := range m {
			ran += atoi(x[1]) + atoi(x[2])
			failed += atoi(x[2])
		}
		return ran, failed, nil
	}
	if m := reMocha.FindStringSubmatch(text); m != nil {
		p := atoi(m[1])
		f := 0
		if mf := reMochaFail.FindStringSubmatch(text); mf != nil {
			f = atoi(mf[1])
		}
		return p + f, f, nil
	}
	if m := rePlaywright.FindStringSubmatch(text); m != nil {
		p := atoi(m[1])
		f := 0
		if mf := rePWFailed.FindStringSubmatch(text); mf != nil {
			f = atoi(mf[1])
		}
		return p + f, f, nil
	}
	// Go: count packages that really ran tests.
	oks := reGoOK.FindAllStringSubmatch(text, -1)
	fails := reGoFailPkg.FindAllString(text, -1)
	if len(oks) > 0 || len(fails) > 0 {
		pk := 0
		for _, o := range oks {
			if !strings.Contains(o[1], "[no tests to run]") {
				pk++
			}
		}
		runs := len(reGoRun.FindAllString(text, -1))
		for _, x := range reGoFailID.FindAllStringSubmatch(text, -1) {
			ids = append(ids, x[1])
		}
		ran = pk + len(fails)
		if runs > ran {
			ran = runs
		}
		failed = len(ids)
		if failed == 0 {
			failed = len(fails)
		}
		return ran, failed, uniq(ids)
	}
	if strings.Contains(text, "no test files") {
		return 0, 0, nil
	}
	return -1, 0, nil
}

func uniq(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	sort.Strings(s)
	out := s[:1]
	for _, x := range s[1:] {
		if x != out[len(out)-1] {
			out = append(out, x)
		}
	}
	return out
}

func CountProblems(text string) int {
	return len(reProblems.FindAllString(text, -1))
}

// Verdict says whether a run counts as proof against the baseline.
type Verdict struct {
	OK     bool
	Reason string
}

// Judge checks a run against the task's baseline: no new failures, and a run
// where no test actually ran is a failure.
func Judge(r *Run, baseline *Run) Verdict {
	if r == nil {
		return Verdict{false, "no run on the current code"}
	}
	if !r.Complete() {
		if r.Finished.IsZero() {
			return Verdict{false, "the last run started but never finished"}
		}
		return Verdict{false, "the code changed while the run was going"}
	}
	switch r.Kind {
	case KindTest, KindRelated, KindE2E:
		if r.Ran < 0 {
			return Verdict{false, "couldn't tell how many tests ran from the output (setup can save a tests_ran_pattern)"}
		}
		if r.Ran == 0 {
			return Verdict{false, "0 tests ran"}
		}
		if r.Failed == 0 && r.Exit == 0 {
			return Verdict{true, ""}
		}
		if baseline != nil && baseline.Failed > 0 {
			if len(r.FailedIDs) > 0 && len(baseline.FailedIDs) > 0 {
				base := map[string]bool{}
				for _, id := range baseline.FailedIDs {
					base[id] = true
				}
				var fresh []string
				for _, id := range r.FailedIDs {
					if !base[id] {
						fresh = append(fresh, id)
					}
				}
				if len(fresh) == 0 {
					return Verdict{true, "only failures that were there before the task"}
				}
				return Verdict{false, "new failing tests: " + strings.Join(fresh, ", ")}
			}
			if r.Failed <= baseline.Failed {
				return Verdict{true, "no more failures than before the task"}
			}
		}
		return Verdict{false, strconv.Itoa(r.Failed) + " failing"}
	case KindLint, KindBuild:
		if r.Exit == 0 {
			return Verdict{true, ""}
		}
		if baseline != nil && baseline.Exit != 0 && r.Problems <= baseline.Problems {
			return Verdict{true, "no new problems compared with before the task"}
		}
		return Verdict{false, r.Kind + " failed with new problems"}
	default:
		if r.Exit == 0 {
			return Verdict{true, ""}
		}
		return Verdict{false, "the command failed (exit " + strconv.Itoa(r.Exit) + ")"}
	}
}

// Commands that prove nothing about a change: they succeed whatever the code
// does.
var trivialProof = map[string]bool{"true": true, ":": true, "echo": true, "printf": true, "exit": true,
	"sleep": true, "test": true, "[": true, "cat": true, "ls": true, "pwd": true, "date": true, "false": true}

// TrivialProof reports a proof command that doesn't exercise anything.
func TrivialProof(command string) bool {
	segs := Segments(command)
	if len(segs) == 0 {
		return true
	}
	for _, s := range segs {
		if s.Name() != "" && !trivialProof[s.Name()] {
			return false
		}
	}
	return true
}

// LatestFor returns the task's newest run of a kind (and sub) on the given
// code. A non-empty command is the saved command the run must have used, so
// a run from an older setup, or another task's run, doesn't count.
func LatestFor(runs []*Run, taskID, kind, sub, command, print string) *Run {
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.TaskID != taskID || r.Kind != kind || (sub != "" && r.Sub != sub) || r.PrintBefore != print {
			continue
		}
		if command != "" && !commandMatches(kind, command, r.Command) {
			continue
		}
		return r
	}
	return nil
}

// commandMatches: the run used the saved command. The related-tests command
// is a template whose {files} part changes per run.
func commandMatches(kind, saved, ran string) bool {
	if kind == KindRelated {
		before, after, found := strings.Cut(saved, "{files}")
		if found {
			return len(ran) >= len(before)+len(after) && strings.HasPrefix(ran, before) && strings.HasSuffix(ran, after)
		}
	}
	return ran == saved
}

// BaselineFor returns the task's baseline run of a kind.
func BaselineFor(runs []*Run, taskID, kind string) *Run {
	for _, r := range runs {
		if r.TaskID == taskID && r.Kind == kind && r.Baseline && r.Complete() {
			return r
		}
	}
	return nil
}
