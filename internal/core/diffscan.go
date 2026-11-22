package core

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// fileDiff is one file of a unified diff: its path (the old one when the
// file was deleted) and its added and removed lines.
type fileDiff struct {
	Path    string
	OldPath string
	Deleted bool
	Lines   []diffLine
}

type diffLine struct {
	N    int // line number in the new file (added) or the old one (removed)
	Add  bool
	Text string
}

// parseDiff reads a unified diff. Hunk headers give the line counts, so a
// removed line that starts with "-- " (a SQL comment) or an added one that
// starts with "++" is never read as a file header.
func parseDiff(diff string) []fileDiff {
	var out []fileDiff
	var cur *fileDiff
	oldPath := ""
	oldLeft, newLeft, oldN, newN := 0, 0, 0, 0
	for _, l := range strings.Split(diff, "\n") {
		if oldLeft > 0 || newLeft > 0 {
			switch {
			case strings.HasPrefix(l, "+") && newLeft > 0:
				cur.Lines = append(cur.Lines, diffLine{N: newN, Add: true, Text: l[1:]})
				newN++
				newLeft--
				continue
			case strings.HasPrefix(l, "-") && oldLeft > 0:
				cur.Lines = append(cur.Lines, diffLine{N: oldN, Text: l[1:]})
				oldN++
				oldLeft--
				continue
			case strings.HasPrefix(l, " "):
				oldN++
				newN++
				oldLeft--
				newLeft--
				continue
			case strings.HasPrefix(l, `\`): // "\ No newline at end of file"
				continue
			}
			oldLeft, newLeft = 0, 0 // a short hunk: fall through to headers
		}
		switch {
		case strings.HasPrefix(l, "--- "):
			oldPath = diffPath(strings.TrimPrefix(l, "--- "), "a/")
		case strings.HasPrefix(l, "+++ "):
			p := diffPath(strings.TrimPrefix(l, "+++ "), "b/")
			f := fileDiff{Path: p, OldPath: oldPath}
			if p == "" {
				f.Path, f.Deleted = oldPath, true
			}
			out = append(out, f)
			cur = &out[len(out)-1]
			oldPath = ""
		case strings.HasPrefix(l, "@@") && cur != nil:
			oldN, oldLeft, newN, newLeft = hunkRange(l)
		}
	}
	return out
}

// diffPath: a header path without its a/ or b/ prefix; "" for /dev/null.
func diffPath(p, prefix string) string {
	p = strings.TrimRight(p, "\t\r")
	if p == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			p = u
		}
	}
	return strings.TrimPrefix(p, prefix)
}

var reHunk = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// hunkRange reads "@@ -a,b +c,d @@" (a count left out is 1).
func hunkRange(l string) (oldStart, oldCount, newStart, newCount int) {
	m := reHunk.FindStringSubmatch(l)
	if m == nil {
		return 0, 0, 0, 0
	}
	count := func(s string) int {
		if s == "" {
			return 1
		}
		return atoi(s)
	}
	return atoi(m[1]), count(m[2]), atoi(m[3]), count(m[4])
}

// addedFileDiff is a new, untracked file written as a diff that adds every
// line, so the same scans read it.
func addedFileDiff(rel string, content []byte) string {
	text := strings.TrimSuffix(string(content), "\n")
	lines := strings.Split(text, "\n")
	if text == "" {
		lines = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", rel, len(lines))
	for _, l := range lines {
		b.WriteString("+" + l + "\n")
	}
	return b.String()
}

// readableNewFile: an untracked file small enough to scan and not binary.
func readableNewFile(root, rel string) ([]byte, bool) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() || fi.Size() >= 1<<20 {
		return nil, false
	}
	b, err := os.ReadFile(full)
	if err != nil || bytes.IndexByte(b, 0) >= 0 {
		return nil, false
	}
	return b, true
}

// taskChanges: what the task changed, as a diff against the commit it
// started from (so its own slice commits count), new files included. The
// user's own unsaved work at task start, what other sessions changed, the
// setup's ignore_changes and the builders' copies are left out.
func taskChanges(t *Task, ignore []string) ([]fileDiff, error) {
	base := t.StartCommit
	if base == "" {
		base = "HEAD"
	}
	others := t.OthersWork()
	keep := func(p string) bool {
		return p != "" && !others[p] && !inWorktreesDir(p) && !MatchAny(ignore, p)
	}
	// -M: a moved test file isn't read as a deleted one.
	diff, err := gitOut(t.Root, "-c", "core.quotePath=false", "diff", "-U0", "--no-color", "--no-ext-diff", "-M", "--src-prefix=a/", "--dst-prefix=b/", base, "--")
	if err != nil {
		return nil, fmt.Errorf("couldn't read the task's changes since %s: %v", short(base), err)
	}
	if out, err := gitOut(t.Root, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		for _, p := range strings.Split(out, "\x00") {
			if !keep(p) {
				continue
			}
			if b, ok := readableNewFile(t.Root, p); ok {
				diff += "\n" + addedFileDiff(p, b)
			}
		}
	}
	var out []fileDiff
	for _, f := range parseDiff(diff) {
		if keep(f.Path) {
			out = append(out, f)
		}
	}
	return out, nil
}

// Weakening is one place where the diff makes a test or check weaker.
type Weakening struct {
	File string
	Line int // 0 for the whole file
	What string
}

func (w Weakening) String() string {
	if w.Line > 0 {
		return fmt.Sprintf("%s:%d %s", w.File, w.Line, w.What)
	}
	return w.File + " " + w.What
}

var (
	// Prose files mention these words without doing them.
	reProse = regexp.MustCompile(`(?i)\.(md|mdx|markdown|txt|rst|adoc)$`)
	reSkip  = regexp.MustCompile(`\b(?:it|test|describe|context|suite|specify)\.(?:skip|only|todo)\s*\(|\b(?:xit|xdescribe|xtest|xcontext|fit|fdescribe)\s*\(|\bt\.Skip(?:f|Now)?\s*\(|@pytest\.mark\.(?:skip|skipif|xfail)\b|\bpytest\.skip\s*\(|@unittest\.skip|#\[ignore\]|@(?:Disabled|Ignore)\b|\bmarkTestSkipped\s*\(|^\s*(?:skip|pending)\s*\(?\s*['"]`)
	// Lint, type and coverage checks switched off for a line or a file.
	reSilence = regexp.MustCompile(`(?i)eslint-disable|@ts-(?:ignore|nocheck|expect-error)|#\s*noqa\b|#\s*type:\s*ignore|//\s*nolint\b|#\s*pylint:\s*disable|rubocop:disable|@SuppressWarnings\b|#!?\[allow\(|(?:istanbul|c8|v8)\s+ignore|#\s*pragma:\s*no\s*cover|phpcs:(?:ignore|disable)|@phpstan-ignore|swiftlint:disable`)
	reCILoose = regexp.MustCompile(`(?i)continue-on-error:\s*true|allow_failure:\s*true|--no-verify\b|\[(?:skip ci|ci skip|no ci)\]`)
	reOrTrue  = regexp.MustCompile(`\|\|\s*(?:true\b|:(?:\s|;|$))`)
	// Product code that behaves differently under CI or in tests.
	reEnvBranch = regexp.MustCompile(`process\.env\.CI\b|process\.env\[['"]CI['"]\]|os\.(?:Getenv|LookupEnv)\("CI"\)|os\.environ(?:\.get)?\s*[\[(]\s*['"]CI['"]|\bENV\[['"]CI['"]\]|NODE_ENV['"]?\]?\s*[!=]==?\s*['"]test['"]|['"]test['"]\s*[!=]==?\s*process\.env\.NODE_ENV`)
	reAssert    = regexp.MustCompile(`\b(?:expect|assert\w*|should|require\.\w+|t\.(?:Error|Errorf|Fatal|Fatalf|Fail|FailNow)|assertThat|XCTAssert\w*)\s*[(.]`)
	// A threshold with its number: coverage minimums, fail-under levels.
	reThreshold = regexp.MustCompile(`(?i)((?:cov(?:erage)?|threshold|fail[_-]under|branches|functions|lines|statements)[\w.-]*)\W{0,4}(\d+(?:\.\d+)?)`)
	// A line that is only a comment: an assertion there doesn't run.
	reCommentOnly = regexp.MustCompile(`^\s*(?://|#|/\*|\*|--|<!--)`)
	reGolden      = regexp.MustCompile(`(^|/)__snapshots__/|\.(snap|golden)$`)
	reToolConfig  = regexp.MustCompile(`(?i)(^|/)[^/]+\.config\.[cm]?[jt]s$`)
)

// WeakenedTests finds where a diff makes tests or checks weaker: skipped or
// focused tests, silenced lint or type checks, CI steps allowed to fail,
// deleted test files, fewer assertions, lowered thresholds, snapshots or
// golden files changed without their test, and product code that branches
// on CI or NODE_ENV=test. Each is for the report to name, with why.
func WeakenedTests(changes []fileDiff) []Weakening { return weakenedTests(changes, nil) }

// weakenedTests is WeakenedTests with the setup's test_files counted.
func weakenedTests(changes []fileDiff, s *Setup) []Weakening {
	var out []Weakening
	changed := map[string]bool{}
	for _, f := range changes {
		changed[f.Path] = true
	}
	for _, f := range changes {
		p := filepath.ToSlash(f.Path)
		isTest := TestFile(p, s)
		if f.Deleted && isTest {
			out = append(out, Weakening{File: p, What: "a test file is deleted"})
			continue
		}
		if reGolden.MatchString(p) && !goldenTestChanged(p, changed) {
			out = append(out, Weakening{File: p, What: "a snapshot or golden output changed without its test"})
		}
		if reProse.MatchString(p) {
			continue
		}
		ci := ciFile(p)
		base := strings.ToLower(path.Base(p))
		config := GateFile(p) || riskyNames[base] || base == "makefile" || reToolConfig.MatchString(p)
		product := !isTest && !config
		removedAsserts, addedAsserts := 0, 0
		before := map[string]float64{} // removed thresholds, by name
		for _, l := range f.Lines {
			if !l.Add {
				if reAssert.MatchString(l.Text) && !reCommentOnly.MatchString(l.Text) {
					removedAsserts++
				}
				if m := reThreshold.FindStringSubmatch(l.Text); m != nil && config {
					before[strings.ToLower(m[1])] = atof(m[2])
				}
				continue
			}
			add := func(what string) { out = append(out, Weakening{File: p, Line: l.N, What: what}) }
			if reAssert.MatchString(l.Text) {
				if reCommentOnly.MatchString(l.Text) {
					if isTest {
						add("an assertion is commented out")
					}
				} else {
					addedAsserts++
				}
			}
			switch {
			case isTest && reSkip.MatchString(l.Text):
				add("a test is skipped or focused (only)")
			case reSilence.MatchString(l.Text):
				add("a lint, type or coverage check is silenced")
			case reCILoose.MatchString(l.Text), (ci || config) && reOrTrue.MatchString(l.Text):
				add("a failing step is allowed to pass")
			case product && reEnvBranch.MatchString(l.Text):
				add("product code behaves differently under CI or in tests")
			}
			if m := reThreshold.FindStringSubmatch(l.Text); m != nil && config {
				if was, ok := before[strings.ToLower(m[1])]; ok && atof(m[2]) < was {
					add(fmt.Sprintf("a threshold is lowered (%s %s to %s)", m[1], strconv.FormatFloat(was, 'f', -1, 64), m[2]))
				}
			}
		}
		if isTest && removedAsserts > addedAsserts {
			out = append(out, Weakening{File: p, What: fmt.Sprintf("has fewer assertions (%d removed, %d added)", removedAsserts, addedAsserts)})
		}
	}
	return out
}

func atof(s string) float64 { f, _ := strconv.ParseFloat(s, 64); return f }

// goldenTestChanged: the test that owns a snapshot or golden file changed
// too. A Jest snapshot belongs to the test it's named after; a golden file
// in testdata/ to the tests beside that folder; any other to the tests in
// its own folder.
func goldenTestChanged(p string, changed map[string]bool) bool {
	dir := path.Dir(p)
	if path.Base(dir) == "__snapshots__" {
		return changed[path.Join(path.Dir(dir), strings.TrimSuffix(path.Base(p), ".snap"))]
	}
	if path.Base(dir) == "testdata" {
		dir = path.Dir(dir)
	}
	for c := range changed {
		if path.Dir(c) == dir && reTestFile.MatchString(c) && !reGolden.MatchString(c) && !strings.Contains(c, "/testdata/") {
			return true
		}
	}
	return false
}

// The debug tag for temporary logs; built from two parts so this file
// doesn't carry it.
const DebugTag = "[PLB-" + "DEBUG]"

// DebugLeftovers: added lines that still carry the temporary debug tag.
func DebugLeftovers(changes []fileDiff) []string {
	var out []string
	for _, f := range changes {
		if reProse.MatchString(f.Path) {
			continue
		}
		for _, l := range f.Lines {
			if l.Add && strings.Contains(l.Text, DebugTag) {
				out = append(out, fmt.Sprintf("%s:%d", f.Path, l.N))
			}
		}
	}
	return out
}

// Files that decide which checks run or how strict they are (N8): CI,
// git hooks, test, lint and type config, AI instructions, plugin parts and
// Plumbline's own setup. A one-line change there can switch a check off,
// so they always get the full review.
var reGateFile = regexp.MustCompile(`(?i)(^|/)(\.github/workflows/|\.circleci/|\.buildkite/|\.husky/|\.githooks/|\.claude/|\.claude-plugin/|\.cursor/)|(^|/)(\.gitlab-ci\.ya?ml|jenkinsfile|azure-pipelines\.ya?ml|bitbucket-pipelines\.ya?ml|\.travis\.ya?ml|lefthook\.ya?ml|\.pre-commit-config\.ya?ml|\.lintstagedrc(\.\w+)?|(jest|vitest|playwright|cypress|karma|ava|mocha)\.config\.[cm]?[jt]s|\.mocharc(\.\w+)?|eslint\.config\.[cm]?[jt]s|\.eslintrc(\.\w+)?|tsconfig(\.[\w-]+)?\.json|\.golangci\.ya?ml|pytest\.ini|tox\.ini|setup\.cfg|\.coveragerc|phpunit\.xml(\.dist)?|\.rspec|\.rubocop\.yml|\.flake8|\.?ruff\.toml|mypy\.ini|\.prettierrc(\.\w+)?|biome\.jsonc?|codecov\.ya?ml|\.nycrc(\.\w+)?|claude(\.local)?\.md|agents\.md|gemini\.md|\.cursorrules|\.github/copilot-instructions\.md|\.plumbline\.json)$|(^|/)skills/[^/]+/skill\.md$|(^|/)agents/[^/]+\.md$|(^|/)hooks/hooks\.json$`)

// GateFile: rel is a file that decides which checks run (see reGateFile).
func GateFile(rel string) bool { return reGateFile.MatchString(filepath.ToSlash(rel)) }

// gateOutsidePlan: a gate file that is refused outside the plan. The
// project setup is left out (its own confirm prompt guards it), and so are
// Claude Code's settings in .claude/, which Claude can't edit (config-protect)
// and Claude Code itself writes when you allow a tool for good.
func gateOutsidePlan(rel string) bool {
	low := strings.ToLower(filepath.ToSlash(rel))
	if low == RepoSetupFile || strings.HasSuffix("/"+low, "/.claude/settings.json") || strings.HasSuffix("/"+low, "/.claude/settings.local.json") {
		return false
	}
	return GateFile(rel)
}

var reCIFile = regexp.MustCompile(`(?i)(^|/)(\.github/workflows/|\.circleci/|\.buildkite/)|(^|/)(\.gitlab-ci\.ya?ml|jenkinsfile|azure-pipelines\.ya?ml|bitbucket-pipelines\.ya?ml|\.travis\.ya?ml)$`)

func ciFile(rel string) bool { return reCIFile.MatchString(rel) }

// weakenedLine: the report's weakened-tests text: a "Weakened tests" line
// with what's on it and the lines nested under it, or, when the line ends at
// its colon, the list that follows it; or a "### Weakened tests" heading and
// its lines. Blank lines in between don't end it.
func weakenedLine(report string) string { return labeledLines(report, "weakened") }

func labeledLines(report, label string) string {
	var out []string
	lines := strings.Split(report, "\n")
	for i := 0; i < len(lines); i++ {
		raw := strings.TrimRight(lines[i], " \t\r")
		trim := strings.TrimSpace(raw)
		if !strings.HasPrefix(strings.ToLower(plainLine(trim)), label) {
			continue
		}
		out = append(out, raw)
		heading := strings.HasPrefix(trim, "#")
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		openList := strings.HasSuffix(plainLine(trim), ":")
		for i+1 < len(lines) {
			next := strings.TrimRight(lines[i+1], " \t\r")
			nt := strings.TrimSpace(next)
			if nt == "" {
				i++
				continue
			}
			if strings.HasPrefix(nt, "#") {
				break
			}
			ni := len(next) - len(strings.TrimLeft(next, " \t"))
			item := strings.HasPrefix(nt, "- ") || strings.HasPrefix(nt, "* ") || strings.HasPrefix(nt, "+ ")
			if !heading && ni <= indent && !(openList && item && ni == indent) {
				break
			}
			out = append(out, next)
			i++
		}
	}
	return strings.Join(out, "\n")
}

// debugMissing (N9): a temporary debug log is never left in.
func debugMissing(changes []fileDiff) []string {
	if left := DebugLeftovers(changes); len(left) > 0 {
		return []string{fmt.Sprintf("debug logs: remove the temporary %s lines before finishing: %s", DebugTag, listSome(left, 8))}
	}
	return nil
}

// weakenedMissing (P3): every file the diff weakens is named on the
// report's Weakened tests line.
func weakenedMissing(changes []fileDiff, report string, s *Setup) []string {
	var miss []string
	listed := weakenedLine(report)
	var unlisted []string
	seen := map[string]bool{}
	for _, w := range weakenedTests(changes, s) {
		if !strings.Contains(listed, w.File) {
			unlisted = append(unlisted, w.String())
			seen[w.File] = true
		}
	}
	if len(unlisted) > 0 {
		miss = append(miss, fmt.Sprintf(`weakened tests: the diff weakens %d file(s); undo it, or name each file on the "Weakened tests:" line under ## Proof with why it's right: %s`, len(seen), listSome(unlisted, 8)))
	}
	return miss
}

// listSome joins up to n items and counts the rest.
func listSome(items []string, n int) string {
	sort.Strings(items)
	if len(items) <= n {
		return strings.Join(items, "; ")
	}
	return strings.Join(items[:n], "; ") + fmt.Sprintf("; and %d more", len(items)-n)
}
