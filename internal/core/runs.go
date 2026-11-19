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
	// Optional checks the setup may save (WF3). Like lint and build, each
	// passes on exit 0, or with no more problems than before the task.
	KindFormat    = "format"
	KindTypecheck = "typecheck"
	KindSecurity  = "security"
)

// CheckKinds: the saved checks that every commit needs when the setup has
// them, in the order they're reported.
var CheckKinds = []string{KindLint, KindBuild, KindFormat, KindTypecheck, KindSecurity}

// testKind: a run of tests, read for counts; the other kinds are checks.
func testKind(kind string) bool {
	return kind == KindTest || kind == KindRelated || kind == KindE2E
}

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
	Skipped     int       `json:"skipped,omitempty"` // skipped, todo or pending tests
	// Files: the files a related run was given. Seen: the task's case
	// tests that the run's output names (the file, or its Go package).
	Files    []string `json:"files,omitempty"`
	Seen     []string `json:"seen,omitempty"`
	Problems int      `json:"problems,omitempty"`
	Baseline bool     `json:"baseline,omitempty"` // run before any task edit
	// Rebased: a baseline taken on the code a checkout or pull after the
	// approval landed on (Task.BaselinePrint); these replace earlier ones.
	Rebased bool `json:"rebased,omitempty"`
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
	return execute(root, t, kind, sub, command, nil, setup, out)
}

// ExecuteRelated runs the related-tests command for the given files and
// records which files it was given.
func ExecuteRelated(root string, t *Task, command string, files []string, setup *Setup, out io.Writer) (*Run, error) {
	return execute(root, t, KindRelated, "", command, files, setup, out)
}

func execute(root string, t *Task, kind, sub, command string, files []string, setup *Setup, out io.Writer) (*Run, error) {
	ignore := []string(nil)
	if setup != nil {
		ignore = setup.IgnoreChanges
	}
	before, err := Fingerprint(root, ignore)
	if err != nil {
		return nil, err
	}
	r := &Run{ID: newID() + "-" + kind, Kind: kind, Sub: sub, Command: command, Started: Now(), PrintBefore: before, Ran: -1}
	for _, f := range files {
		if f = cleanRel(f); f != "" {
			r.Files = append(r.Files, f)
		}
	}
	if t != nil {
		r.TaskID = t.ID
		// Before any change: before approval, or after it on the code as
		// approved (SEC1: the checks are run then, before the first edit).
		r.Baseline = t.LastCodeChange.IsZero() && (t.Phase != PhaseBuilding || t.ApprovalPrint != "" && before == t.ApprovalPrint)
		if t.BaselinePrint != "" && before == t.BaselinePrint {
			r.Baseline, r.Rebased = true, true
		}
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
		c := ParseTestOutput(text, pat)
		r.Ran, r.Failed, r.FailedIDs, r.Skipped = c.Ran, c.Failed, c.IDs, c.Skipped
		if t != nil {
			r.Seen = CaseTestsSeen(root, text, t.caseTestFiles())
		}
		if r.Exit != 0 && r.Failed == 0 && r.Ran >= 0 {
			r.Failed = 1 // non-zero exit with no failure count: treat as failing
		}
	case KindLint, KindBuild, KindFormat, KindTypecheck, KindSecurity:
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

// TestCounts is what a test run's output says: tests that ran, failed and
// were skipped, and the failing tests' names when the format shows them.
// Ran is -1 when the output format is unknown: that is never counted as
// proof.
type TestCounts struct {
	Ran, Failed, Skipped int
	IDs                  []string
}

// ParseTests reads test output and returns how many tests ran, how many
// failed, and the failing test names when the format shows them.
func ParseTests(text, custom string) (ran, failed int, ids []string) {
	c := ParseTestOutput(text, custom)
	return c.Ran, c.Failed, c.IDs
}

var (
	reSkippedN   = regexp.MustCompile(`(\d+) (?:skipped|todo|pending|ignored)\b`)
	reVitestFail = regexp.MustCompile(`(?m)^\s*FAIL\s+(\S+)\s+>\s+(.+?)\s*$`)
	// A test file that failed as a whole (an import error): "FAIL  x.test.ts [ x.test.ts ]".
	reVitestFileFail = regexp.MustCompile(`(?m)^\s*FAIL\s+(\S+)\s*(?:\[[^\]]*\])?\s*$`)
	// Cypress prints a box per spec: "│ Tests:  3 │", "│ Failing:  1 │".
	reCyTests     = regexp.MustCompile(`│\s*Tests:\s+(\d+)`)
	reCyFailing   = regexp.MustCompile(`│\s*Failing:\s+(\d+)`)
	reCySkipped   = regexp.MustCompile(`│\s*(?:Pending|Skipped):\s+(\d+)`)
	reRSpecFail   = regexp.MustCompile(`(?m)^rspec \S+ # (.+)$`)
	reCargoFail   = regexp.MustCompile(`(?m)^test (\S+) \.\.\. FAILED\s*$`)
	rePHPUnitFail = regexp.MustCompile(`(?m)^\d+\) ([\w\\]+::\w+)`)
	rePHPUnitSkip = regexp.MustCompile(`(?:Skipped|Incomplete|Risky): (\d+)`)
	reMochaItem   = regexp.MustCompile(`^\s+\d+\) (.*)$`)
	reGoSkip      = regexp.MustCompile(`(?m)^\s*--- SKIP: `)
	reCargoResult = regexp.MustCompile(`test result: .*`)
	reMochaPend   = regexp.MustCompile(`(?m)^\s*(\d+) pending`)
	rePWSkipped   = regexp.MustCompile(`(?m)^\s*(\d+) skipped`)
)

// sumSkipped adds up every "N skipped" (todo, pending, ignored) in a
// summary line.
func sumSkipped(summary string) int {
	n := 0
	for _, m := range reSkippedN.FindAllStringSubmatch(summary, -1) {
		n += atoi(m[1])
	}
	return n
}

// mochaFailures: the names under mocha's "N failing" list, each "1) suite
// test:" possibly split over several indented lines.
func mochaFailures(text string, at int) []string {
	var ids []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			ids = append(ids, strings.TrimSuffix(strings.Join(cur, " "), ":"))
			cur = nil
		}
	}
	for _, l := range strings.Split(text[at:], "\n") {
		if m := reMochaItem.FindStringSubmatch(l); m != nil {
			flush()
			cur = []string{strings.TrimSpace(m[1])}
			if strings.HasSuffix(cur[0], ":") {
				flush()
			}
			continue
		}
		if cur != nil {
			t := strings.TrimSpace(l)
			if t == "" {
				flush()
				continue
			}
			cur = append(cur, t)
			if strings.HasSuffix(t, ":") {
				flush()
			}
		}
	}
	flush()
	return ids
}

// ParseTestOutput reads test output (Jest, Vitest, pytest, PHPUnit, RSpec,
// cargo, mocha, Playwright, go test), or the setup's tests_ran_pattern.
func ParseTestOutput(text, custom string) TestCounts {
	var c TestCounts
	if custom != "" {
		if re, err := regexp.Compile(custom); err == nil {
			if m := re.FindStringSubmatch(text); m != nil {
				for i, n := range re.SubexpNames() {
					switch n {
					case "ran":
						c.Ran = atoi(m[i])
					case "failed":
						c.Failed = atoi(m[i])
					case "skipped":
						c.Skipped = atoi(m[i])
					}
				}
				return c
			}
		}
	}
	if m := reJest.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		c.Ran = atoi(last[2])
		if f := reJestFailed.FindStringSubmatch(last[1]); f != nil {
			c.Failed = atoi(f[1])
		}
		c.Skipped = sumSkipped(last[1])
		for _, x := range reJestFailID.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, strings.TrimSpace(x[1])+" › "+strings.TrimSpace(x[2]))
		}
		c.IDs = uniq(c.IDs)
		return c
	}
	if m := reVitest.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		c.Ran = atoi(last[2])
		if f := reJestFailed.FindStringSubmatch(last[1]); f != nil {
			c.Failed = atoi(f[1])
		}
		c.Skipped = sumSkipped(last[1])
		for _, x := range reVitestFail.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, x[1]+" > "+x[2])
		}
		for _, x := range reVitestFileFail.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, x[1]) // the whole file failed
		}
		c.IDs = uniq(c.IDs)
		return c
	}
	if m := rePytest.FindAllStringSubmatch(text, -1); m != nil {
		summary := m[len(m)-1][1]
		if strings.Contains(summary, "no tests ran") {
			c.Skipped = sumSkipped(summary)
			return c
		}
		found := false
		for _, x := range rePyCount.FindAllStringSubmatch(summary, -1) {
			found = true
			n := atoi(x[1])
			c.Ran += n
			if x[2] == "failed" || strings.HasPrefix(x[2], "error") {
				c.Failed += n
			}
		}
		if found {
			c.Skipped = sumSkipped(summary)
			for _, x := range rePyFailedID.FindAllStringSubmatch(text, -1) {
				c.IDs = append(c.IDs, x[1])
			}
			c.IDs = uniq(c.IDs)
			return c
		}
	}
	phpSkipped := func() int {
		n := 0
		for _, m := range rePHPUnitSkip.FindAllStringSubmatch(text, -1) {
			n += atoi(m[1])
		}
		return n
	}
	if m := rePHPUnitOK.FindStringSubmatch(text); m != nil {
		return TestCounts{Ran: atoi(m[1]), Skipped: phpSkipped()}
	}
	if m := rePHPUnit.FindStringSubmatch(text); m != nil {
		c = TestCounts{Ran: atoi(m[1]), Failed: atoi(m[2]) + atoi(m[3]), Skipped: phpSkipped()}
		for _, x := range rePHPUnitFail.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, x[1])
		}
		c.IDs = uniq(c.IDs)
		return c
	}
	if m := reRSpec.FindAllStringSubmatch(text, -1); m != nil {
		last := m[len(m)-1]
		c = TestCounts{Ran: atoi(last[1]), Failed: atoi(last[2])}
		if i := strings.LastIndex(text, last[0]); i >= 0 {
			line, _, _ := strings.Cut(text[i:], "\n")
			c.Skipped = sumSkipped(line)
		}
		for _, x := range reRSpecFail.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, strings.TrimSpace(x[1]))
		}
		c.IDs = uniq(c.IDs)
		return c
	}
	if m := reCargo.FindAllStringSubmatch(text, -1); m != nil {
		for _, x := range m {
			c.Ran += atoi(x[1]) + atoi(x[2])
			c.Failed += atoi(x[2])
		}
		for _, line := range reCargoResult.FindAllString(text, -1) {
			c.Skipped += sumSkipped(line)
		}
		for _, x := range reCargoFail.FindAllStringSubmatch(text, -1) {
			c.IDs = append(c.IDs, x[1])
		}
		c.IDs = uniq(c.IDs)
		return c
	}
	if m := reMocha.FindStringSubmatch(text); m != nil {
		p := atoi(m[1])
		f := 0
		if loc := reMochaFail.FindStringSubmatchIndex(text); loc != nil {
			f = atoi(text[loc[2]:loc[3]])
			c.IDs = uniq(mochaFailures(text, loc[1]))
		}
		if pm := reMochaPend.FindStringSubmatch(text); pm != nil {
			c.Skipped = atoi(pm[1])
		}
		c.Ran, c.Failed = p+f, f
		return c
	}
	if m := rePlaywright.FindStringSubmatch(text); m != nil {
		p := atoi(m[1])
		f := 0
		if mf := rePWFailed.FindStringSubmatch(text); mf != nil {
			f = atoi(mf[1])
		}
		if sm := rePWSkipped.FindStringSubmatch(text); sm != nil {
			c.Skipped = atoi(sm[1])
		}
		c.Ran, c.Failed = p+f, f
		return c
	}
	if m := reCyTests.FindAllStringSubmatch(text, -1); m != nil {
		for _, x := range m {
			c.Ran += atoi(x[1])
		}
		for _, x := range reCyFailing.FindAllStringSubmatch(text, -1) {
			c.Failed += atoi(x[1])
		}
		for _, x := range reCySkipped.FindAllStringSubmatch(text, -1) {
			c.Skipped += atoi(x[1])
		}
		return c
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
			c.IDs = append(c.IDs, x[1])
		}
		c.Ran = pk + len(fails)
		if runs > c.Ran {
			c.Ran = runs
		}
		// Without -v, Ran counts packages, so Failed does too (a package's
		// subtests would otherwise outnumber what ran); the names stay.
		c.Failed = len(c.IDs)
		if c.Failed == 0 || (runs == 0 && len(fails) > 0) {
			c.Failed = len(fails)
		}
		c.IDs = uniq(c.IDs)
		c.Skipped = len(reGoSkip.FindAllString(text, -1))
		return c
	}
	if strings.Contains(text, "no test files") {
		return c
	}
	return TestCounts{Ran: -1}
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
				// A failure without a name (a file that didn't load) can't be
				// matched by name, so those may not grow either.
				if len(fresh) == 0 && unnamedFailures(r) > unnamedFailures(baseline) {
					return Verdict{false, "more failures without a test name than before the task (a test file that didn't load?)"}
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
	case KindLint, KindBuild, KindFormat, KindTypecheck, KindSecurity:
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

// unitRunners run tests in a test process, not the app.
var unitRunners = set("vitest jest mocha ava jasmine karma tap pytest rspec phpunit")

// UnitTestProof reports a ui proof command that only runs unit or
// component tests (vitest on a test file, npm test, go test): they render
// parts in a fake page, so they don't show the real app works (PR1).
// Anything else in the line (playwright, a script) makes it count.
func UnitTestProof(command string) bool {
	segs := Segments(command)
	unit := false
	for _, s := range segs {
		w := s.Words
		if len(w) == 0 || trivialProof[s.Name()] {
			continue
		}
		// npx vitest, pnpm exec jest, yarn dlx mocha, bunx vitest
		i := 0
		switch filepath.Base(w[0]) {
		case "npx", "bunx", "pnpx":
			i = 1
		case "pnpm", "yarn", "bun":
			if len(w) > 1 && (w[1] == "exec" || w[1] == "dlx") {
				i = 2
			}
		}
		for i < len(w) && strings.HasPrefix(w[i], "-") {
			i++
		}
		if i >= len(w) {
			return false
		}
		name := filepath.Base(w[i])
		rest := w[i+1:]
		switch {
		case unitRunners[name]:
			unit = true
		case (name == "go" || name == "cargo") && len(rest) > 0 && rest[0] == "test":
			unit = true
		case (name == "python" || name == "python3") && len(rest) > 1 && rest[0] == "-m" && rest[1] == "pytest":
			unit = true
		case (name == "npm" || name == "pnpm" || name == "yarn" || name == "bun") && len(rest) > 0 &&
			(rest[0] == "test" || rest[0] == "t" || rest[0] == "run" && len(rest) > 1 && rest[1] == "test"):
			unit = true
		default:
			return false // something that isn't a unit test runs too
		}
	}
	return unit
}

// FetchProof reports a ui proof command that only fetches over HTTP (curl,
// wget, httpie): it downloads a page or a file, but nothing renders it or
// clicks anything, so it doesn't show the screen works (PR2: curl of a
// source file from the dev server was recorded as ui proof).
func FetchProof(command string) bool {
	segs := Segments(command)
	fetch := false
	for _, s := range segs {
		switch name := s.Name(); {
		case name == "" || trivialProof[name]:
		case name == "curl" || name == "wget" || name == "http" || name == "https" || name == "xh":
			fetch = true
		default:
			return false
		}
	}
	return fetch
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

// unnamedFailures: failures the output gave no name for.
func unnamedFailures(r *Run) int {
	if n := r.Failed - len(r.FailedIDs); n > 0 {
		return n
	}
	return 0
}

// baselineRuns: the task's complete runs of a kind taken before its first
// edit, oldest first. Once one was taken on the code a branch switch or
// pull after the approval landed on (Rebased), only the ones on the newest
// such code count: the ones from a branch the task was on before don't
// describe this code (M16).
func baselineRuns(runs []*Run, taskID, kind string) []*Run {
	var out []*Run
	landed := ""
	for _, r := range runs {
		if r.TaskID == taskID && r.Kind == kind && r.Baseline && r.Complete() {
			out = append(out, r)
			if r.Rebased {
				landed = r.PrintBefore
			}
		}
	}
	if landed == "" {
		return out
	}
	var rebased []*Run
	for _, r := range out {
		if r.Rebased && r.PrintBefore == landed {
			rebased = append(rebased, r)
		}
	}
	return rebased
}

// brokenBaseline: every test failed in each run before the first edit, so
// none of them is a starting point (BaselineFor).
func brokenBaseline(runs []*Run, taskID, kind string) bool {
	b := baselineRuns(runs, taskID, kind)
	return len(b) > 0 && BaselineFor(runs, taskID, kind) == nil
}

// BaselineFor returns the task's baseline run of a kind: of the runs taken
// before the first edit, the one with the fewest failures (the newest on a
// tie), so fixing a broken environment and running again replaces it, but
// a worse run never excuses more failures. A test run where every test
// failed says the environment was broken, not how the code stood, so it
// isn't one.
func BaselineFor(runs []*Run, taskID, kind string) *Run {
	var best *Run
	for _, r := range baselineRuns(runs, taskID, kind) {
		test := testKind(kind)
		if test && r.Ran > 1 && r.Failed >= r.Ran {
			continue
		}
		bad, bestBad := r.Failed, 0
		if !test {
			bad = r.Problems
		}
		if best != nil {
			bestBad = best.Failed
			if !test {
				bestBad = best.Problems
			}
		}
		if best == nil || bad <= bestBad {
			best = r
		}
	}
	return best
}
