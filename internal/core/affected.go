package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Affected tests: the tests a dependency graph says the task's changes can
// reach. With Setup.AffectedFinal (the user's opt-in at setup) a passing run
// of them stands in for the full suite at the end of a task. The graph only
// sees imports: a test that builds a binary, runs one, or reads a file by
// path is outside it, which is why the user decides per repo.

// AffectedGraph names the graph the saved test command has: "go list" for
// plain go test, "jest" or "vitest" for their derived related commands.
// A saved related command, pytest's name match or a wrapper script has none.
func AffectedGraph(s *Setup) string {
	if s == nil || !RelatedIsDerived(s) {
		return ""
	}
	if _, ok := ParseGoTest(s.Commands.Test); ok {
		return "go list"
	}
	if c, ok := monorepoOf(s); ok {
		return c.graphName()
	}
	d := derivedRelated(s.Commands.Test)
	switch {
	case strings.HasSuffix(d, " --findRelatedTests {files}"):
		return "jest"
	case strings.Contains(d, " related --run"):
		return "vitest"
	}
	return ""
}

type goListEntry struct {
	ImportPath string
	Dir        string
	ForTest    string
	Deps       []string
	GoFiles    []string
	CgoFiles   []string
	TestGo     []string `json:"TestGoFiles"`
	XTestGo    []string `json:"XTestGoFiles"`
	Error      *struct{ Err string }
	DepsErrors []*struct{ Err string }
}

// goListTest: every package of the suite's patterns and its test binary,
// with their dependencies. No -e: a broken package makes go list fail, and
// any error it still reports counts as failing.
func goListTest(dir string, c GoTestCmd) ([]goListEntry, error) {
	args := []string{"list", "-test", "-json=ImportPath,Dir,ForTest,Deps,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,Error,DepsErrors"}
	for _, f := range c.Flags {
		if f == "-race" {
			args = append(args, f)
		}
	}
	args = append(args, c.Pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, errors.New(strings.TrimSpace(err.Error() + ": " + errb.String()))
	}
	var list []goListEntry
	dec := json.NewDecoder(&out)
	for {
		var e goListEntry
		err := dec.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if e.Error != nil || len(e.DepsErrors) > 0 || e.ImportPath == "" || e.Dir == "" {
			return nil, errors.New("go list reported a broken package: " + e.ImportPath)
		}
		list = append(list, e)
	}
	if len(list) == 0 {
		return nil, errors.New("go list listed no packages")
	}
	return list, nil
}

// GoAffectedPkgs: the changed files' packages plus every package that, or
// whose test binary, depends on one. GoRelatedPkgs' reasons for the full suite stand
// (why). graph is true only when every changed file mapped through go list:
// a package go list doesn't list (another module, a symlinked folder), a
// non-Go file outside the packages, or go list failing leaves it false.
// Without a full mapping, the changed packages alone are returned, as before.
func GoAffectedPkgs(root string, c GoTestCmd, files []string) (pkgs []string, graph bool, why string) {
	direct, why := GoRelatedPkgs(root, files)
	if why != "" {
		return nil, false, why
	}
	graph = true
	for _, f := range files {
		f = cleanRel(f)
		if f != "" && filepath.Ext(f) != ".go" && !isMarkdown(f) {
			graph = false // outside every package (GoRelatedPkgs refused the rest)
		}
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return direct, false, ""
	}
	list, err := goListTest(real, c)
	if err != nil {
		return direct, false, ""
	}
	dirOf := map[string]string{}
	pathAt := map[string]string{}
	for _, e := range list {
		if e.ForTest != "" || strings.Contains(e.ImportPath, " ") || strings.HasSuffix(e.ImportPath, ".test") {
			continue
		}
		// A .go file that links to another package's file compiles that
		// code with no import go list could show.
		for _, f := range append(append(append(append([]string{}, e.GoFiles...), e.CgoFiles...), e.TestGo...), e.XTestGo...) {
			if fi, err := os.Lstat(filepath.Join(e.Dir, f)); err != nil || !fi.Mode().IsRegular() {
				return direct, false, ""
			}
		}
		dirOf[e.ImportPath] = e.Dir
		pathAt[foldPath(filepath.Clean(e.Dir))] = e.ImportPath
	}
	changed := map[string]bool{}
	for _, p := range direct {
		rel := strings.TrimPrefix(p, "./")
		want := filepath.Clean(filepath.Join(real, rel))
		got, err := filepath.EvalSymlinks(want)
		if err != nil || foldPath(got) != foldPath(want) {
			return direct, false, "" // reached through a symlink
		}
		ip, ok := pathAt[foldPath(want)]
		if !ok {
			return direct, false, "" // not one of the suite's packages
		}
		changed[ip] = true
	}
	hit := map[string]bool{}
	for ip := range changed {
		hit[ip] = true
	}
	for _, e := range list {
		if e.ForTest != "" || strings.Contains(e.ImportPath, " ") {
			continue
		}
		// A test binary (p.test) or the package itself: one with no tests
		// still builds under go test, so a break in it shows.
		under := strings.TrimSuffix(e.ImportPath, ".test")
		if _, ok := dirOf[under]; !ok || hit[under] {
			continue
		}
		for _, d := range e.Deps {
			if i := strings.Index(d, " ["); i >= 0 {
				d = d[:i]
			}
			if changed[d] {
				hit[under] = true
				break
			}
		}
	}
	for ip := range hit {
		rel, err := filepath.Rel(real, dirOf[ip])
		if err != nil || climbsOut(rel) {
			return direct, false, ""
		}
		if rel == "." {
			pkgs = append(pkgs, ".")
		} else {
			pkgs = append(pkgs, "./"+filepath.ToSlash(rel))
		}
	}
	sort.Strings(pkgs)
	return pkgs, graph, ""
}

var jsSourceExt = set(".js .jsx .ts .tsx .mjs .cjs .mts .cts")

// reJSSetupName: config and setup files jest and vitest load by name, never
// through a test's imports. Setup files at other paths (setupFiles in the
// config) are a gap the setup question names.
var reJSSetupName = regexp.MustCompile(`(?i)^((jest|vitest|vite|babel)\.config|vitest\.workspace|(jest|vitest)\.setup|setuptests)\.`)

// JSAffectedFiles: why the changed files can't all be followed through
// jest's or vitest's import graph, or "" when they can. Markdown is skipped.
func JSAffectedFiles(root string, files []string) string {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "the project folder can't be read"
	}
	n := 0
	for _, f := range files {
		f = cleanRel(f)
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) || climbsOut(filepath.Clean(f)) {
			return f + " is outside the project"
		}
		f = filepath.ToSlash(filepath.Clean(f))
		if isMarkdown(f) {
			continue
		}
		base := filepath.Base(f)
		low := strings.ToLower(base)
		switch {
		case !jsSourceExt[strings.ToLower(filepath.Ext(f))]:
			return f + " isn't code the import graph follows"
		case strings.HasSuffix(low, ".d.ts"), strings.HasSuffix(low, ".d.mts"), strings.HasSuffix(low, ".d.cts"):
			return f + " declares types for every file and is never imported"
		case reJSSetupName.MatchString(base), strings.Contains("/"+f+"/", "/__mocks__/"):
			return f + " is loaded by name, not imported"
		}
		abs := filepath.Join(real, f)
		if _, err := os.Lstat(abs); err != nil {
			return f + " is gone, and the graph can't follow a deleted file"
		}
		if got, err := filepath.EvalSymlinks(abs); err != nil || foldPath(got) != foldPath(abs) || !plainFile(abs) {
			return f + " is a link"
		}
		n++
	}
	if n == 0 {
		return "no changed code files"
	}
	return ""
}

func isMarkdown(p string) bool { return strings.EqualFold(filepath.Ext(p), ".md") }

// TaskChangedFiles: every file changed since the task's start, as the full
// suite would see them (files dirty before the task or another session's
// included). Git failing, no start commit, or a name --files can't carry is
// an error.
func TaskChangedFiles(t *Task) ([]string, error) {
	if t == nil || t.StartCommit == "" {
		return nil, errors.New("the task has no start commit")
	}
	all, err := changedSince(t.Root, t.StartCommit)
	if err != nil {
		return nil, err
	}
	for _, p := range all {
		if strings.Contains(p, ",") || strings.HasPrefix(p, "-") {
			return nil, errors.New(p + " can't be passed to --files")
		}
	}
	return all, nil
}

// AffectedProof: the run that stands in for the full suite at the end, or
// nil. It is the newest related run on the current code that the graph
// mapped, given every file the task changed, and passing; and no test run
// on this code failed after it, nor any full suite on it at all.
func AffectedProof(t *Task, s *Setup, runs []*Run, fp string) *Run {
	graph := AffectedGraph(s)
	if t == nil || t.Hotfix() || !s.AffectedFinal || graph == "" || fp == "" {
		return nil // a hotfix's full suite comes right after its commit (W1)
	}
	files, err := TaskChangedFiles(t)
	if err != nil || len(files) == 0 {
		return nil
	}
	template := savedCommand(s, KindRelated)
	base := BaselineFor(runs, t.ID, KindTest)
	at := -1
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.OnBase || r.BeforeFix || r.TaskID != t.ID || r.Kind != KindRelated || r.PrintBefore != fp || r.Graph != graph || !commandMatches(KindRelated, template, r.Command) || !coversAll(r.Files, files) {
			continue
		}
		at = i
		break
	}
	// Without names on both sides failures are compared by count with the
	// full suite's baseline, which a smaller run passes by running fewer
	// tests.
	if at < 0 || runs[at].Failed > 0 && (unnamedFailures(runs[at]) > 0 || base != nil && unnamedFailures(base) > 0) || !JudgeNow(runs[at], base, s).OK {
		return nil
	}
	// Failure names in these tools are per unit (tests::two in two crates),
	// so a failure can't be matched to the baseline's: only a clean run counts.
	if _, mono := monorepoOf(s); mono && (runs[at].Failed > 0 || runs[at].Exit != 0) {
		return nil
	}
	for i, r := range runs {
		if r.OnBase || r.BeforeFix || r.TaskID != t.ID || r.PrintBefore != fp || r.Finished.IsZero() || r.Stopped || r.PrintAfter != r.PrintBefore {
			continue
		}
		if (r.Kind == KindTest || r.Kind == KindRelated && i > at) && !Judge(r, base).OK {
			return nil
		}
	}
	return runs[at]
}

// FinalAffectedRun: the affected run a final commit stands on when the full
// suite hasn't passed on the current code, for the commit's text.
func FinalAffectedRun(t *Task, s *Setup) *Run {
	fp, err := Fingerprint(t.Root, s.IgnoreChanges)
	if err != nil {
		return nil
	}
	runs := LoadRuns(t.Root)
	if JudgeNow(LatestFor(runs, t.ID, KindTest, "", s.Commands.Test, fp), BaselineFor(runs, t.ID, KindTest), s).OK {
		return nil
	}
	return AffectedProof(t, s, runs, fp)
}

func coversAll(have, need []string) bool {
	got := map[string]bool{}
	for _, f := range have {
		got[cleanRel(f)] = true
	}
	for _, f := range need {
		if !got[cleanRel(f)] {
			return false
		}
	}
	return true
}
