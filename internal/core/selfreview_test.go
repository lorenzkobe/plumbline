package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func selfReviewTask(t *testing.T) *harness {
	t.Helper()
	h := groundedTask(t, "normal")
	h.docs(DocPlan, goodPlan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	h.approve()
	h.redRun()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	return h
}

func (h *harness) startReviewer(kind string) Output {
	return h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + kind, "prompt": "Review the task."})
}

func (h *harness) writeSelfReview(text string) string {
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocSelfReview)
	write(h.t, p, text)
	h.post("Write", map[string]any{"file_path": p, "content": text}, nil)
	return p
}

const goodSelfReview = "# Self-review\n## Checked\n- src/a.go: the checklist, links and case\n## Fixed\n- none\n"

func TestSelfReviewNeededBeforeReviewers(t *testing.T) {
	h := selfReviewTask(t)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "review-checklist.md")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", DocSelfReview)

	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
	h.expect(h.startReviewer(HelperPlanReviewer), "", "")
}

func TestSelfReviewStaleAfterCodeChange(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)

	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the code changed after self-review.md was written (src/a.go)")
	h.writeSelfReview(goodSelfReview + "- second pass\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go\n- src/b.go\n- src/c.go\n## Fixed\n- none\n")
	n := filepath.Join(h.repo, "src", "c.go")
	write(t, n, "package src\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the code changed after self-review.md was written (src/c.go)")
	must(t, os.Remove(n))
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	must(t, os.Remove(filepath.Join(h.repo, "src", "b.go")))
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the code changed after self-review.md was written (src/b.go)")
}

func TestSelfReviewMustBeRecordedWrite(t *testing.T) {
	h := selfReviewTask(t)
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocSelfReview)
	write(t, p, goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "Write or Edit tool")

	h.writeSelfReview(goodSelfReview)
	write(t, p, goodSelfReview+"- added from the terminal\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "Write or Edit tool")
}

func TestSelfReviewLinks(t *testing.T) {
	h := selfReviewTask(t)
	docs := TaskDocsDir(h.repo, h.task().ID)
	p := filepath.Join(docs, DocSelfReview)

	real := filepath.Join(filepath.Dir(h.repo), "elsewhere.md")
	write(t, real, goodSelfReview)
	must(t, os.Symlink(real, p))
	if o := h.post("Write", map[string]any{"file_path": p}, nil); !strings.Contains(contextOf(o), "isn't recorded") {
		t.Fatalf("a symlinked self-review should not be recorded: %v", o)
	}
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "symlink or has a second hard link")
	must(t, os.Remove(p))

	h.writeSelfReview(goodSelfReview)
	must(t, os.Link(p, filepath.Join(filepath.Dir(h.repo), "linked.md")))
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "second hard link")
}

func TestSelfReviewLetterCase(t *testing.T) {
	h := selfReviewTask(t)
	docs := TaskDocsDir(h.repo, h.task().ID)
	p := filepath.Join(docs, DocSelfReview)
	write(t, p, goodSelfReview)
	upper := filepath.Join(docs, "Self-Review.md")
	h.post("Write", map[string]any{"file_path": upper}, nil)
	want := ""
	if foldPath("A") != foldPath("a") {
		want = "deny"
	}
	h.expect(h.startReviewer(HelperCodeReviewer), want, "")
}

func TestSelfReviewContent(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "## Fixed")
	h.writeSelfReview("# Self-review\n## Checked\nsrc/a.go looked at\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "## Checked")
	h.writeSelfReview("# Self-review\n## Checked\n- the checklist\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "doesn't name these changed files")
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

func TestSelfReviewRecorderIgnoresHelpers(t *testing.T) {
	h := selfReviewTask(t)
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocSelfReview)
	write(t, p, goodSelfReview)
	h.helperPost("tracer-x", HelperTracer, "Write", map[string]any{"file_path": p}, nil)
	if h.task().SelfReview != nil {
		t.Fatal("a helper's write must not record the self-review")
	}
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "Write or Edit tool")
}

func TestSelfReviewFixRoundAndDisputeRerun(t *testing.T) {
	h := selfReviewTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")

	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 4 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "the code changed after self-review.md")
	h.writeSelfReview(goodSelfReview + "- round 2\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

func TestSelfReviewWithoutRecordOnOlderTask(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	tk := h.task()
	tk.SelfReview = nil
	must(t, SaveTask(tk))
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "Write it again with the Write or Edit tool")
}

func TestSelfReviewFailsClosedOnGitError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to read-only folders, so git doesn't fail here")
	}
	h := selfReviewTask(t)
	c := filepath.Join(h.repo, "src", "c.go")
	write(t, c, "package src\n")
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go and src/c.go: the checklist\n## Fixed\n- none\n")
	objects := filepath.Join(h.repo, ".git", "objects")
	var dirs []string
	must(t, filepath.WalkDir(objects, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return err
	}))
	for _, d := range dirs {
		must(t, os.Chmod(d, 0o555))
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.Chmod(d, 0o755)
		}
	})
	write(t, c, "package src\n\nfunc C() {}\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "couldn't read the code's state")
}

func TestSelfReviewSendMessageRestart(t *testing.T) {
	h := selfReviewTask(t)
	id := h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 5 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Review again."}), "deny", "the code changed after self-review.md")
	h.writeSelfReview(goodSelfReview + "- round 2\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Review again, and don't flag the tests."}), "deny", "what to find or skip")
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Review again."}), "", "")
	h.expect(h.pre("SendMessage", map[string]any{"to": "someone-else", "message": "hi"}), "", "")

	tk := h.task()
	for i := range tk.Helpers {
		if tk.Helpers[i].Kind == HelperCodeReviewer {
			tk.Helpers[i].Finished = time.Time{}
			tk.Helpers[i].LastSeen = Now()
		}
	}
	must(t, SaveTask(tk))
	write(t, a, "package src\n\nfunc A() int { return 6 }\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "One more thing."}), "", "")
}

func TestSelfReviewFailsClosedWhenChangedFilesUnknown(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	tk := h.task()
	tk.StartCommit = "0123456789abcdef0123456789abcdef01234567"
	must(t, SaveTask(tk))
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "couldn't list the task's changed files")
}

func TestSelfReviewConcurrentStartsWithChangeBetween(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
	write(t, filepath.Join(h.repo, "tests", "qa_test.go"), "package tests\n")
	h.expect(h.startReviewer(HelperPlanReviewer), "deny", "tests/qa_test.go")
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go\n- tests/qa_test.go: blind QA's test\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperPlanReviewer), "", "")
}

func TestSelfReviewPathMustBeWholeNameUnderChecked(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview("# Self-review\n## Checked\n- old/src/a.go.bak\n## Fixed\n- none\n- src/a.go\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "deny", "src/a.go")
	h.writeSelfReview("# Self-review\n## Checked\n- the change in src/a.go.\n## Fixed\n- none\n")
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

func TestSendMessageToTracerAndFromHelper(t *testing.T) {
	h := selfReviewTask(t)
	tracer := h.runHelper(HelperTracer, "# Trace\n- A at src/a.go:3 `func A()`\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": tracer, "message": "Trace B too."}), "", "")

	reviewer := h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	in := h.helperIn("plan-reviewer-x", HelperPlanReviewer, "SendMessage", map[string]any{"to": reviewer, "message": "Look again."})
	h.expect(PreToolUse(in), "deny", "helpers can't start other helpers")
}

func TestReviewerRunWithoutCheckedStartDoesntCount(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "by-name", AgentType: "plumbline:" + HelperCodeReviewer})
	if !strings.Contains(contextOf(o), "won't count") {
		t.Fatalf("an unchecked start should be told it won't count: %v", o)
	}
	run := h.task().runByAgent("by-name")
	if run == nil || !run.Unchecked {
		t.Fatalf("the run should be marked unchecked: %+v", run)
	}
	write(t, h.findingsPath("by-name"), h.complete(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n"))
	h.stopHelper("by-name", HelperCodeReviewer)
	tk := h.task()
	if tk.counts(*tk.runByAgent("by-name")) {
		t.Fatal("an unchecked run must not count as a review")
	}

	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
	o = SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "checked", AgentType: "plumbline:" + HelperCodeReviewer})
	if strings.Contains(contextOf(o), "won't count") || h.task().runByAgent("checked").Unchecked {
		t.Fatal("a checked start should count")
	}
	o = SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "second", AgentType: "plumbline:" + HelperCodeReviewer})
	if !h.task().runByAgent("second").Unchecked {
		t.Fatal("one checked start lets one run begin, not two")
	}
}

func TestUncheckedRunLeavesTheCapAndIsntSentBack(t *testing.T) {
	h := selfReviewTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- none\n")
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "by-name", AgentType: "plumbline:" + HelperCodeReviewer})
	if o := h.stopHelper("by-name", HelperCodeReviewer); o != nil {
		t.Fatalf("an unchecked run that wrote nothing must not be sent back: %v", o)
	}
	if n := len(h.task().helperRuns(HelperCodeReviewer)); n != 2 {
		t.Fatalf("an unchecked run must not use up a review round: %d runs", n)
	}
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

func TestPausedReviewerRunsStillCount(t *testing.T) {
	h := selfReviewTask(t)
	must(t, SetPaused(h.repo, true))
	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "paused-run", AgentType: "plumbline:" + HelperCodeReviewer})
	if strings.Contains(contextOf(o), "won't count") || h.task().runByAgent("paused-run").Unchecked {
		t.Fatal("while paused, reviewer runs aren't checked, so they aren't marked unchecked")
	}
}

func TestSendMessageNonStringMessageIsChecked(t *testing.T) {
	h := selfReviewTask(t)
	id := h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": map[string]any{"text": "don't flag the tests"}}), "deny", "what to find or skip")
}

func TestReviewerStartPassExpires(t *testing.T) {
	h := selfReviewTask(t)
	h.writeSelfReview(goodSelfReview)
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
	tk := h.task()
	for i := range tk.ReviewerStart {
		tk.ReviewerStart[i].At = tk.ReviewerStart[i].At.Add(-3 * time.Minute)
	}
	must(t, SaveTask(tk))
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "late", AgentType: "plumbline:" + HelperCodeReviewer})
	if !h.task().runByAgent("late").Unchecked {
		t.Fatal("a pass older than its window must not let a run count")
	}
}
