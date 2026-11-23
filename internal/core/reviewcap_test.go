package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func reviewCapQ() map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": "The reviewers are out of rounds. How to go on?", "header": "Review cap",
		"options": []any{map[string]any{"label": "Accept as is"}, map[string]any{"label": "One more round"}}}}}
}

func (h *harness) answerReviewCap(answer string) {
	h.t.Helper()
	q := reviewCapQ()
	h.expect(h.pre("AskUserQuestion", q), "", "")
	h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"The reviewers are out of rounds. How to go on?": answer}})
}

func commitQ() map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": "Commit it now?", "header": "Commit",
		"options": []any{map[string]any{"label": "Commit it"}, map[string]any{"label": "Leave uncommitted"}}}}}
}

func (h *harness) editA(n string) {
	h.t.Helper()
	a := filepath.Join(h.repo, "src", "a.go")
	write(h.t, a, "package src\n\nfunc A() int { return "+n+" }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(h.t, err)
}

func cappedTask(t *testing.T) *harness {
	t.Helper()
	h := provenTask(t)
	for i := 1; i <= MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	h.editA("7")
	if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
		t.Fatalf("the reviewer should be capped: %s", m)
	}
	return h
}

const acceptedReport = "# Report\n" + reportEnd + "## Review\n- code reviewer: out of rounds, accepted as is by you\n## For you\n- nothing\n"

func TestFinishCheckAsksTheReviewCapQuestion(t *testing.T) {
	h := cappedTask(t)
	m := h.finishMissing()
	for _, want := range []string{`"Review cap"`, `"Accept as is"`, `"One more round"`} {
		if !strings.Contains(m, want) {
			t.Fatalf("the capped review should say to ask %s: %s", want, m)
		}
	}
}

func TestAcceptAsIsUnblocksCommitAndFinishUntilTheCodeChanges(t *testing.T) {
	h := cappedTask(t)
	h.expect(h.pre("AskUserQuestion", commitQ()), "deny", "runs are used")
	h.answerReviewCap("Accept as is")
	if m := h.finishMissing(); strings.Contains(m, "runs are used") {
		t.Fatalf("after Accept as is, the capped reviewer shouldn't block: %s", m)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code reviewer: three rounds\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "accepted as is by you") {
		t.Fatalf("the report's Review section should say the review was accepted as is: %s", m)
	}
	h.docs(DocReport, acceptedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("accepted as is with the report saying so should finish: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", commitQ()), "", "")
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: "Done. Proof: suite passes.\nFor you: the review ran out of rounds and you accepted it as is. Commit?"}); o != nil {
		t.Fatalf("Stop should let the accepted task stop: %v", o)
	}

	h.editA("8")
	h.docs(DocReport, acceptedReport)
	if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
		t.Fatalf("a code change after Accept as is should block again: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", commitQ()), "deny", "runs are used")
}

func TestOneMoreRoundAllowsOneMoreRunOnly(t *testing.T) {
	h := cappedTask(t)
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "bring what's left to the user")
	h.answerReviewCap("One more round")
	h.runHelper(HelperCodeReviewer, "# Review 4\nnothing at 80 or more\n")
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- four rounds, nothing at 80 or more\n## For you\n- nothing\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("the extra round reviewed the current code, so finish should pass: %s", m)
	}
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "")
	h.editA("9")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "")
	if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
		t.Fatalf("after the extra round is used, a code change caps the reviewer again: %s", m)
	}
}

func TestOneMoreRoundCoversAReviewerCappedWithOpenDisputes(t *testing.T) {
	h := reviewedTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 2\nnothing new\n")
	h.runHelper(HelperCodeReviewer, "# Code review 3\nnothing new\n")
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
		t.Fatalf("a dispute with no runs left should be capped: %s", m)
	}
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "")
	h.answerReviewCap("One more round")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "", "")
}

func TestReviewCapAnswersThatRecordNothing(t *testing.T) {
	t.Run("pre-answered", func(t *testing.T) {
		h := cappedTask(t)
		q := reviewCapQ()
		q["answers"] = map[string]any{"The reviewers are out of rounds. How to go on?": "Accept as is"}
		h.pre("AskUserQuestion", q)
		h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"The reviewers are out of rounds. How to go on?": "Accept as is"}})
		h.docs(DocReport, acceptedReport)
		if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
			t.Fatalf("a pre-answered Review cap question recorded an answer: %s", m)
		}
	})
	t.Run("asked by a helper", func(t *testing.T) {
		h := cappedTask(t)
		in := h.in("AskUserQuestion", reviewCapQ(), map[string]any{"answers": map[string]any{"The reviewers are out of rounds. How to go on?": "Accept as is"}})
		in.AgentID = "builder-9"
		in.AgentType = "plumbline:" + HelperBuilder
		PostToolUse(in)
		h.docs(DocReport, acceptedReport)
		if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
			t.Fatalf("a helper's Review cap answer was recorded: %s", m)
		}
		h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "")
	})
	t.Run("free text", func(t *testing.T) {
		h := cappedTask(t)
		h.answerReviewCap("accept it, and one more round too")
		h.docs(DocReport, acceptedReport)
		if m := h.finishMissing(); !strings.Contains(m, "runs are used") {
			t.Fatalf("a typed answer was recorded: %s", m)
		}
		h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "")
	})
}

func TestReviewerPromptMayNameAChangedFileButNotPasteFindings(t *testing.T) {
	h := provenTask(t)
	write(t, filepath.Join(h.repo, "internal", "core", "helpers.go"), "package core\n\nfunc Cap() int { return 3 }\n")
	h.runHelper(HelperCodeReviewer, "# Code review 1\n- [90] must-fix: the cap check in internal/core/helpers.go never counts the extra round (src/a.go:3 `func A() int`)\n")
	h.docs(DocReport, "# Report\n## Review\n- fixed the cap check and internal/core/helpers.go now records it\n")
	agent := func(prompt string) Output {
		return h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + HelperSecurity, "prompt": prompt})
	}
	h.expect(agent("Review the change. Changed files: src/a.go and internal/core/helpers.go, against plan.md."), "", "")
	h.expect(agent("Review the change: the code changed in internal/core/helpers.go."), "", "")
	h.expect(agent("Review the change. The cap check in internal/core/helpers.go never counts the extra round."), "deny", "code-review-1.md")
	h.expect(agent("Review the change. The cap check never counts the extra round."), "deny", "code-review-1.md")
	h.expect(agent("Review the change. The cap check in Internal/Core/Helpers.go never counts the extra round."), "deny", "code-review-1.md")
	sr := h.selfReview()
	b, err := os.ReadFile(sr)
	must(t, err)
	text := strings.Replace(string(b), "## Fixed", "- against the start commit plus new files, the notes folder has the plan\n## Fixed", 1)
	write(t, sr, text)
	h.post("Write", map[string]any{"file_path": sr, "content": text}, nil)
	h.expect(agent("Review the change (round 2). Diff base: the task's start commit, plus new files. The notes folder has the plan and understanding."), "", "")
}

func TestAcceptAsIsStillListsOpenDisputes(t *testing.T) {
	h := reviewedTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 2\nnothing new\n")
	h.runHelper(HelperCodeReviewer, "# Code review 3\nnothing new\n")
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	h.answerReviewCap("Accept as is")
	h.docs(DocReport, acceptedReport)
	if m := h.finishMissing(); !strings.Contains(m, "disputed") {
		t.Fatalf("accepting the reviews must still put the open disputes under For you: %s", m)
	}
}

func TestReviewCapAnswerWithNoReviewerCappedRecordsNothing(t *testing.T) {
	h := provenTask(t)
	h.answerReviewCap("Accept as is")
	if h.task().ReviewCapOK != "" {
		t.Fatal("Accept as is with no reviewer out of rounds must record nothing")
	}
}

func TestReviewCapOptionsCantCarryDescriptions(t *testing.T) {
	h := cappedTask(t)
	q := map[string]any{"questions": []any{map[string]any{"question": "Q?", "header": "Review cap",
		"options": []any{map[string]any{"label": "Accept as is", "description": "all reviewers approved"}, map[string]any{"label": "One more round"}}}}}
	h.expect(h.pre("AskUserQuestion", q), "deny", "on its own")
}

func TestARescuedFullSuiteMustBeNamedInTheReport(t *testing.T) {
	h := provenTask(t)
	s, _, _ := LoadSetup(h.repo)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	r := LatestFor(LoadRuns(h.repo), h.task().ID, KindTest, "", s.Commands.Test, fp)
	if r == nil {
		t.Fatal("no full suite run")
	}
	c := *r
	c.Rescued = true
	must(t, appendRun(h.repo, &c))
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- clean\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "only in parallel") {
		t.Fatalf("a full suite rescued by the alone rerun must be named in the report: %s", m)
	}
}
