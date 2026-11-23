package core

import (
	"fmt"
	"regexp"
	"strings"
)

// WF4: a [bug] case's test must be seen failing before the fix. The proof
// is a recorded run in which that test failed, taken while nothing but
// tests had changed since the plan was first approved.

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
// taken while only test files (or the plan's case tests) had changed since
// the first approval, that ran tests and failed. The failure is the case
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
	testsOnly := map[string]bool{}
	onlyTests := func(print string) bool {
		if v, ok := testsOnly[print]; ok {
			return v
		}
		changed, err := treeChanges(t.Root, t.ApprovalPrint, print)
		ok := err == nil
		for _, f := range changed {
			if !TestFile(f, s) && !contains(cases, f) {
				ok = false
				break
			}
		}
		testsOnly[print] = ok
		return ok
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
		for _, r := range runs {
			if r.TaskID != t.ID || !r.Complete() || r.TimedOut || r.Ran <= 0 || r.Failed <= 0 {
				continue
			}
			if (r.Kind != KindTest && r.Kind != KindRelated) || !commandMatches(r.Kind, savedCommand(s, r.Kind), r.Command) {
				continue
			}
			if name != "" && len(r.FailedIDs) > 0 {
				if !failedNamed(r.FailedIDs, name) {
					continue
				}
			} else {
				ranIt := contains(r.Files, file) || contains(r.Seen, file) || (!takesFiles && r.Kind == KindTest)
				if !ranIt || Judge(r, base).OK {
					continue
				}
			}
			if !onlyTests(r.PrintBefore) {
				continue
			}
			if changedSince(r.PrintBefore, file) {
				edited = true
				continue
			}
			red = true
			break
		}
		switch {
		case red:
		case edited:
			miss = append(miss, fmt.Sprintf("[bug] case test %s: it changed after the run that saw it failing, so the test as it is now was never seen failing before the fix. Set the changes that aren't tests aside again (git stash push --include-untracked -- <those files>), run plumbline run related --files %s and see it fail, then git stash pop. If it can't fail without the fix, stop and ask the user, who can waive it (plumbline waive bug-test)", ref, file))
		default:
			miss = append(miss, fmt.Sprintf("[bug] case test %s: no recorded run shows it failing before the fix, taken while only tests had changed since approval. Show it now: set the changes that aren't tests aside (git stash push --include-untracked -- <those files>), run plumbline run related --files %s and see it fail, then git stash pop. If it can't fail without the fix, stop and ask the user, who can waive it (plumbline waive bug-test)", ref, file))
		}
	}
	return miss
}
