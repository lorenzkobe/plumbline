package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WF4: a [bug] case's test must be seen failing before the fix. The proof
// is a recorded run in which that test failed, taken while nothing but
// tests (and untracked notes no code names) had changed since the plan was
// first approved.

// bugCaseTests: the approved plan's [bug] cases' test links ("file" or
// "file::name"), each once. Tasks approved before 0.6.10 read plan.md.
func (t *Task) bugCaseTests() []string {
	refs := t.BugTests
	if !t.BugStored {
		if text, _, err := ReadDoc(t, DocPlan); err == nil {
			refs = CheckPlan(text).BugTests
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, ref := range refs {
		file, name, _ := strings.Cut(ref, "::")
		file = cleanRel(file)
		ref = file
		if name = strings.TrimSpace(name); name != "" {
			ref += "::" + name
		}
		if file != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// bugCaseFiles: the files of the [bug] cases' tests, each once.
func (t *Task) bugCaseFiles() []string {
	var out []string
	for _, ref := range t.bugCaseTests() {
		if f, _, _ := strings.Cut(ref, "::"); !contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// failedNamed: one of the failing tests' names is the named test, as the
// runner prints it: alone (Go's TestRefund, or TestRefund/twice for a
// subtest), or after its file, class or describe block (a.py::test_x,
// Suite › refunds twice, a.test.ts > refunds twice), with a parameter
// after it (test_x[1]). A longer name that starts the same
// (TestRefundTwice) isn't it.
// testNames: a run's failing names as tests are named in a case: cargo's
// without the test binary before them, which would match a case named
// after the package.
func testNames(r *Run) []string {
	if !r.CargoNames {
		return r.FailedIDs
	}
	out := make([]string, len(r.FailedIDs))
	for i, id := range r.FailedIDs {
		_, path, ok := strings.Cut(id, ": ")
		if !ok {
			path = id
		}
		out[i] = path
	}
	return out
}

func failedNamed(ids []string, name string) bool {
	re, err := regexp.Compile(`(^|::| › | > |/|\.| )` + regexp.QuoteMeta(name) + `($|/|\[|::| › | > | )`)
	if err != nil {
		return false
	}
	for _, id := range ids {
		if re.MatchString(id) {
			return true
		}
	}
	return false
}

// bugRedMissing: each [bug] case's test has a recorded failing run from
// before the fix: a complete run of the saved test or related command,
// taken while only test files (or the plan's case tests, or untracked
// notes no code names) had changed since the first approval, that ran tests and failed. The failure is the case
// test's when its named test is among the failing names, or, when the case
// names only a file or the runner prints no names, when the run was given
// the file, its output names the file, or it's the full suite in a project
// whose related command can't take files, and its failures weren't all
// there before the task. The test file must be the same now as in that run,
// so a test that always failed can't stand in for the real one. `plumbline
// waive bug-test` (the user's) lets it go on the current code.
func bugRedMissing(t *Task, s *Setup, fp string, runs []*Run) []string {
	refs := t.bugCaseTests()
	if len(refs) == 0 || waived(t, "bug-test", fp) {
		return nil
	}
	if t.ApprovalPrint == "" {
		// Approved before 0.5.0: what changed since approval can't be known.
		return nil
	}
	cases := t.caseTestFiles()
	// notes: changed files that are untracked markdown notes, judged as
	// they are now (untracked, plain, named by no code); a note the stash
	// took away with the fix doesn't make a run "after the fix" (0.8.7).
	notes := map[string]bool{}
	judged := map[string]bool{}
	testsOnly := map[string][]string{}
	// besidesTests: what changed since approval in a run's code, other than
	// tests and untracked notes; a git error is a change.
	besidesTests := func(print string) []string {
		if v, ok := testsOnly[print]; ok {
			return v
		}
		changed, err := treeChanges(t.Root, t.ApprovalPrint, print)
		if err != nil {
			testsOnly[print] = []string{"(git couldn't compare it with the approved code)"}
			return testsOnly[print]
		}
		var fresh []string
		for _, f := range changed {
			if !TestFile(f, s) && !contains(cases, f) && !judged[f] {
				judged[f] = true
				fresh = append(fresh, f)
			}
		}
		if len(fresh) > 0 {
			for f := range untrackedNotes(t.Root, existing(t.Root, fresh)) {
				notes[f] = true
			}
		}
		var other []string
		for _, f := range changed {
			if !TestFile(f, s) && !contains(cases, f) && !notes[f] {
				other = append(other, f)
			}
		}
		testsOnly[print] = other
		return other
	}
	// sinceRun: the files that changed between a run's code and now.
	sinceRun := map[string][]string{}
	changedSince := func(print, file string) bool {
		changed, ok := sinceRun[print]
		if !ok {
			var err error
			if changed, err = treeChanges(t.Root, print, fp); err != nil {
				changed = []string{file} // can't tell: count it as changed
			}
			sinceRun[print] = changed
		}
		return contains(changed, file)
	}
	takesFiles := strings.Contains(savedCommand(s, KindRelated), "{files}")
	base := BaselineFor(runs, t.ID, KindTest)
	var miss []string
	for _, ref := range refs {
		file, name, _ := strings.Cut(ref, "::")
		red, edited := false, false
		var blocked []string // set when the newest failing run didn't count for a non-test change, edited when for the test's
		for _, r := range runs {
			if r.TaskID != t.ID || r.Control || !r.Complete() || r.TimedOut || r.Ran <= 0 || r.Failed <= 0 {
				continue
			}
			if (r.Kind != KindTest && r.Kind != KindRelated) || !commandMatches(r.Kind, savedCommand(s, r.Kind), r.Command) {
				continue
			}
			if name != "" && len(r.FailedIDs) > 0 {
				if !failedNamed(testNames(r), name) {
					continue
				}
			} else {
				ranIt := contains(r.Files, file) || contains(r.Seen, file) || (!takesFiles && r.Kind == KindTest)
				if !ranIt || Judge(r, base).OK {
					continue
				}
			}
			if other := besidesTests(r.PrintBefore); len(other) > 0 {
				blocked, edited = other, false
				continue
			}
			if changedSince(r.PrintBefore, file) {
				blocked, edited = nil, true
				continue
			}
			red = true
			break
		}
		show := fmt.Sprintf("Show it with plumbline run related --before-fix --files %s: it runs the tests on the approved code with your current tests, in a temporary copy, so nothing in the checkout is set aside. If it can't fail without the fix, stop and ask the user, who can waive it (plumbline waive bug-test)", file)
		switch {
		case red:
		case len(blocked) > 0:
			names := strings.Join(blocked, ", ")
			if len(blocked) > 5 {
				names = strings.Join(blocked[:5], ", ") + fmt.Sprintf(" and %d more", len(blocked)-5)
			}
			miss = append(miss, fmt.Sprintf("[bug] case test %s: its failing run doesn't count, because besides tests %s had changed since approval, so the fix (or part of it) may have been there. %s", ref, names, show))
		case edited:
			miss = append(miss, fmt.Sprintf("[bug] case test %s: it changed after the run that saw it failing, so the test as it is now was never seen failing before the fix. %s", ref, show))
		default:
			miss = append(miss, fmt.Sprintf("[bug] case test %s: no recorded run shows it failing before the fix, taken while only tests had changed since approval. %s", ref, show))
		}
	}
	return miss
}

// existing: the files of rels that are on disk now (plainFile counts a
// missing file as plain).
func existing(root string, rels []string) []string {
	var out []string
	for _, r := range rels {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(r))); err == nil {
			out = append(out, r)
		}
	}
	return out
}
