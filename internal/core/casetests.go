package core

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// cleanRel: a repo-relative path as written on the command line or in the
// plan, with slashes and without a leading "./".
func cleanRel(p string) string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	return p
}

// caseTestFiles: the test files the approved plan's cases link to, each
// once. Tasks approved before 0.3.1 read them from plan.md.
func (t *Task) caseTestFiles() []string {
	tests := t.CaseTests
	if !t.CasesStored {
		if text, _, err := ReadDoc(t, DocPlan); err == nil {
			tests = CheckPlan(text).CaseTests
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range tests {
		if f = cleanRel(f); f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

var reGoOKPkg = regexp.MustCompile(`(?m)^ok\s+(\S+)\s`)

// CaseTestsSeen: the case test files a run's output names. A file counts
// when its path shows (or its last parts do, for a runner started in a
// subfolder), or, for a Go test file, when its package has an "ok" line.
func CaseTestsSeen(root, text string, tests []string) []string {
	if len(tests) == 0 || text == "" {
		return nil
	}
	okPkgs := map[string]bool{}
	for _, m := range reGoOKPkg.FindAllStringSubmatch(text, -1) {
		okPkgs[m[1]] = true
	}
	var out []string
	for _, tf := range tests {
		if strings.HasSuffix(tf, "_test.go") {
			if pkg := goPackage(root, tf); pkg != "" && okPkgs[pkg] {
				out = append(out, tf)
			}
			continue
		}
		if pathShown(text, tf) {
			out = append(out, tf)
		}
	}
	return out
}

// pathShown: the file's path is in the text on path boundaries, whole or
// from its second-to-last folder on (a runner started in backend/ prints
// tests/test_x.py for backend/tests/test_x.py).
func pathShown(text, rel string) bool {
	parts := strings.Split(rel, "/")
	tries := []string{rel}
	if len(parts) > 2 {
		tries = append(tries, strings.Join(parts[len(parts)-2:], "/"))
	}
	for _, p := range tries {
		re := regexp.MustCompile(`(^|[^\w.-])` + regexp.QuoteMeta(p) + `($|[^\w.-])`)
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// goPackage: the import path of the package a Go file is in, from the
// nearest go.mod at or above its folder, inside root.
func goPackage(root, rel string) string {
	dir := path.Dir(rel)
	for d := dir; ; d = path.Dir(d) {
		if mod := goModule(filepath.Join(root, filepath.FromSlash(d), "go.mod")); mod != "" {
			sub := strings.TrimPrefix(strings.TrimPrefix(dir, d), "/")
			if d == "." {
				sub = dir
			}
			if sub == "." || sub == "" {
				return mod
			}
			return mod + "/" + sub
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

func goModule(gomod string) string {
	f, err := os.Open(gomod)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// savedCommand: the setup's command for a run kind, which a run must have
// used to count.
func savedCommand(s *Setup, kind string) string {
	switch kind {
	case KindTest:
		return s.Commands.Test
	case KindRelated:
		return s.Commands.TestRelated
	case KindE2E:
		return s.Commands.E2E
	case KindLint:
		return s.Commands.Lint
	case KindBuild:
		return s.Commands.Build
	}
	return ""
}

// caseRunMissing: each case's test ran and passed on the current code, not
// just exists (N7). A run proves a case test when it passed with nothing
// failing and names the file in its output or was given it with --files.
// A run that skipped more tests than before the task proves it only when
// the report's Weakened tests line names the file. A project whose related
// command can't take files ({files}) has no way to run one file, so there
// a full suite that passes against the baseline counts for every case.
func caseRunMissing(t *Task, s *Setup, fp string, runs []*Run, report string) []string {
	var miss []string
	weak := weakenedLine(report)
	takesFiles := strings.Contains(s.Commands.TestRelated, "{files}")
	base := BaselineFor(runs, t.ID, KindTest)
	extraSkips := func(r *Run) int {
		if r.Kind == KindTest && base != nil {
			return r.Skipped - base.Skipped // skips that were there before aren't this task's
		}
		return r.Skipped
	}
	for _, tf := range t.caseTestFiles() {
		if _, err := os.Stat(filepath.Join(t.Root, filepath.FromSlash(tf))); err != nil {
			miss = append(miss, fmt.Sprintf("case test %s doesn't exist yet", tf))
			continue
		}
		proven, skipped := false, 0
		for _, r := range runs {
			if r.TaskID != t.ID || r.PrintBefore != fp || !r.Complete() || r.Ran <= 0 {
				continue
			}
			if (r.Kind != KindTest && r.Kind != KindRelated && r.Kind != KindE2E) || !commandMatches(r.Kind, savedCommand(s, r.Kind), r.Command) {
				continue
			}
			named := contains(r.Files, tf) || contains(r.Seen, tf)
			clean := r.Exit == 0 && r.Failed == 0
			if !named && !takesFiles && r.Kind == KindTest {
				named, clean = true, Judge(r, base).OK
			}
			if !named || !clean {
				continue
			}
			if n := extraSkips(r); n > 0 {
				skipped = n
				continue
			}
			proven = true
			break
		}
		switch {
		case proven:
		case skipped > 0 && strings.Contains(weak, tf):
		case skipped > 0:
			miss = append(miss, fmt.Sprintf(`case test %s: the passing run that ran it skipped %d more test(s) than before the task, and a skipped test hasn't run: run plumbline run related --files %s with none skipped, or name the file on the report's "Weakened tests:" line with why`, tf, skipped, tf))
		case takesFiles:
			miss = append(miss, fmt.Sprintf("case test %s: no passing run on the current code shows it ran (a file that only exists proves nothing): run plumbline run related --files %s", tf, tf))
		default:
			miss = append(miss, fmt.Sprintf("case test %s: no passing full suite on the current code (run: plumbline run test)", tf))
		}
	}
	return miss
}
