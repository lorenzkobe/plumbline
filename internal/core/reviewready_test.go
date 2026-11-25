package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// greenTests records a passing full-suite run on the code as it is now.
func (h *harness) greenTests() {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	r, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", s, io.Discard)
	must(h.t, err)
	if r.Failed != 0 || r.Exit != 0 {
		h.t.Fatalf("the suite should pass: %+v", r)
	}
}

func (h *harness) editFile(rel, text string) {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	write(h.t, p, text)
	h.post("Edit", map[string]any{"file_path": p}, nil)
}

// relatedTask: selfReviewTask with a saved related-tests template, and both
// src/a.go and src/b.go changed.
func relatedTask(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.TestRelated = "./test.sh {files}"
	s.IgnoreChanges = []string{"coverage/**"}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("normal")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, goodPlan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	h.approve()
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	h.editFile("src/b.go", "package src\n\nfunc B() int { return 3 }\n")
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- src/b.go: the checklist\n## Fixed\n- none\n")
	return h
}

func (h *harness) relatedRun(command string, files []string) {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	_, err := ExecuteRelated(h.repo, h.task(), command, files, s, io.Discard)
	must(h.t, err)
}

// C1, C3, C4
func TestReviewerStartNeedsTestsOnTheCurrentCode(t *testing.T) {
	h := selfReviewTask(t)
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 5 }\n")
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "plumbline run checks")

	write(t, filepath.Join(h.repo, "FAIL"), "x\n")
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	must(t, os.Remove(filepath.Join(h.repo, "FAIL")))
	h.writeSelfReview(goodSelfReview + "- again\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "plumbline run checks")

	write(t, filepath.Join(h.repo, "FAIL"), "x\n")
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "tests on the current code")

	must(t, os.Remove(filepath.Join(h.repo, "FAIL")))
	h.greenTests()
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

// C8
func TestTestsAreCheckedBeforeTheSelfReview(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 6 }\n")
	o := h.startReviewer(HelperCodeReviewer)
	h.expect(o, "deny", "plumbline run checks")
	if strings.Contains(reasonOf(o), DocSelfReview) {
		t.Fatalf("the tests refusal comes first, so a test fix doesn't make a fresh self-review stale: %s", reasonOf(o))
	}
	h.greenTests()
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the code changed after self-review.md was written (src/a.go)")
}

// C2, C5, C15
func TestRelatedRunMustCoverEveryChangedFile(t *testing.T) {
	h := relatedTask(t)
	h.relatedRun("./test.sh src/a.go", []string{"src/a.go"})
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "src/b.go")

	h.relatedRun("./test.sh ", nil)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "plumbline run checks")

	h.relatedRun("./test.sh src/a.go src/b.go", []string{"src/a.go", "src/b.go"})
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

// C6
func TestFailureTheApprovedCodeHadDoesNotRefuse(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, goodPlan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	write(t, filepath.Join(h.repo, "FAIL"), "x\n")
	h.approve()
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	r, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	if r.Failed == 0 {
		t.Fatal("the run should fail as the approved code did")
	}
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- FAIL: the marker the approved code had\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

// C7
func TestNoTestCommandNeedsNoTestRun(t *testing.T) {
	h := selfReviewTask(t)
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Test = ""
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "update-index", "--assume-unchanged", ".plumbline.json")
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 7 }\n")
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

// C9, C10, C11, C13
func TestFixRoundSelfReviewNeedsReach(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	h.editFile("src/a.go", "package src\n\nfunc A() int { return 8 }\n")
	h.editFile("src/b.go", "package src\n\nfunc B() int { return 9 }\n")
	h.greenTests()
	checked := "# Self-review\n## Checked\n- src/a.go: the checklist\n- src/b.go: the checklist\n## Fixed\n- the finding\n"
	h.writeSelfReview(checked)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "## Reach")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "src/a.go")

	h.writeSelfReview(checked + "## Reach\n- src/b.go: B's callers, checked\n")
	o := h.startReviewer(HelperCodeReviewer)
	h.expect(o, "deny", "src/a.go")
	if strings.Contains(reasonOf(o), "src/b.go") {
		t.Fatalf("src/b.go is in ## Reach, so only src/a.go is missing: %s", reasonOf(o))
	}

	h.writeSelfReview(checked + "## Reach\n- src/a.go: A's callers and the messages that show it, checked\n- src/b.go: B's callers, checked\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

// C12, C16
func TestReachSkipsTestAndDocFilesButNotGateFiles(t *testing.T) {
	h := selfReviewTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")

	h.editFile("src/a_test.go", "package src\n")
	h.editFile("README.md", "# Readme\n")
	h.greenTests()
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- src/a_test.go: the test\n- README.md: the words\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	if !GateFile(".claude-plugin/notes.md") || !docFile(".claude-plugin/notes.md") {
		t.Fatal("fixture: the file must be both a gate file and prose")
	}
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- none\n")
	write(t, filepath.Join(h.repo, ".claude-plugin", "notes.md"), "# Notes\n")
	h.greenTests()
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- src/a_test.go: the test\n- README.md: the words\n- .claude-plugin/notes.md: the gate file\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", ".claude-plugin/notes.md")
}

// C14
func TestReachFailsClosedOnGitError(t *testing.T) {
	h := selfReviewTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	tk := h.task()
	for i := range tk.Helpers {
		if tk.Helpers[i].Kind == HelperCodeReviewer {
			tk.Helpers[i].PrintAfter = strings.Repeat("0", 40)
			tk.Helpers[i].PrintBefore = tk.Helpers[i].PrintAfter
		}
	}
	must(t, SaveTask(tk))
	h.editFile("src/b.go", "package src\n\nfunc B() int { return 10 }\n")
	h.greenTests()
	checked := "# Self-review\n## Checked\n- src/a.go: the checklist\n- src/b.go: the checklist\n## Fixed\n- none\n"
	h.writeSelfReview(checked + "## Reach\n- src/b.go: callers\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "src/a.go")
	h.writeSelfReview(checked + "## Reach\n- src/a.go: callers\n- src/b.go: callers\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	tk = h.task()
	tk.StartCommit = strings.Repeat("1", 40)
	must(t, SaveTask(tk))
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "couldn't list")
}

func reasonOf(o Output) string {
	_, r := decision(o)
	return r
}

func TestRedFullSuiteIsNotOutweighedByAGreenRelatedRun(t *testing.T) {
	h := relatedTask(t)
	// The full suite fails; a run given files passes.
	h.editFile("test.sh", "#!/bin/sh\nif [ $# -eq 0 ]; then echo 'FAILED tests/t.py::test_new'; echo '== 1 failed, 2 passed in 0.1s =='; exit 1; fi\necho '== 3 passed in 0.1s =='\n")
	must(t, os.Chmod(filepath.Join(h.repo, "test.sh"), 0o755))
	r, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	if r.Failed == 0 {
		t.Fatal("fixture: the full suite should fail")
	}
	h.relatedRun("./test.sh src/a.go src/b.go test.sh", []string{"src/a.go", "src/b.go", "test.sh"})
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- src/b.go: the checklist\n- test.sh: the runner\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the full suite")
}

func TestReachLooksPastAnUncountedLatestRun(t *testing.T) {
	h := selfReviewTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	id, _ := h.startHelper(HelperCodeReviewer)
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 2\n## Findings\n- none\n"))
	h.stopHelper(id, HelperCodeReviewer)
	tk := h.task()
	tk.runByAgent(id).Problems = []string{"a finding without its quote"} // so it doesn't count
	must(t, SaveTask(tk))
	if r := h.task().lastFinished(HelperCodeReviewer); r == nil || h.task().counts(*r) {
		t.Fatalf("fixture: the latest run must have finished without counting: %+v", r)
	}
	h.editFile("src/a.go", "package src\n\nfunc A() int { return 11 }\n")
	h.greenTests()
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "## Reach")
}
