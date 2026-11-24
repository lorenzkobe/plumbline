package core

import (
	"fmt"
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
	h.runHelper(HelperArbiter, "# Arbiter 1\n## Findings\n- [90] must-fix: A still returns the wrong value (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n")
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
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "deny", "already ran 3 times")
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
	h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix) // it upholds the disputed finding, so the user decides
	h.answerReviewCap("One more round")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "", "")
}

func TestReviewCapAnswerRecordedWhenClaudeCodeFillsTheInput(t *testing.T) {
	h := cappedTask(t)
	q := reviewCapQ()
	h.expect(h.pre("AskUserQuestion", q), "", "")
	ans := map[string]any{"The reviewers are out of rounds. How to go on?": "One more round"}
	filled := reviewCapQ()
	filled["answers"] = ans
	h.post("AskUserQuestion", filled, map[string]any{"answers": ans})
	if d, r := decision(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"})); d == "deny" && strings.Contains(r, "already ran") {
		t.Fatalf("the user's One more round, with the answers Claude Code puts in the tool input, wasn't recorded: %s", r)
	}
}

func TestReviewCapAnswersThatRecordNothing(t *testing.T) {
	t.Run("pre-answered", func(t *testing.T) {
		h := cappedTask(t)
		q := reviewCapQ()
		q["answers"] = map[string]any{"The reviewers are out of rounds. How to go on?": "Accept as is"}
		h.expect(h.pre("AskUserQuestion", q), "deny", "pre-answered")
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
	h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix) // it upholds the disputed finding, so the user decides
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
	h.expect(h.pre("AskUserQuestion", q), "deny", `description empty`)
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

func reviewCapQOpts(question string, opts ...string) map[string]any {
	var o []any
	for i := 0; i+1 < len(opts); i += 2 {
		o = append(o, map[string]any{"label": opts[i], "description": opts[i+1]})
	}
	return map[string]any{"question": question, "header": "Review cap", "multiSelect": false, "options": o}
}

// C1, C3, C17
func TestReviewCapRefusalNamesWhatIsWrong(t *testing.T) {
	h := cappedTask(t)
	ask := func(qs ...any) Output { return h.pre("AskUserQuestion", map[string]any{"questions": qs}) }
	described := reviewCapQOpts("Accept the reviews as they are for this code?",
		"Accept as is", "The reviews stay as they are for this code.", "One more round", "Each reviewer runs once more.")
	h.expect(ask(described), "deny", `"description": ""`)
	h.expect(ask(reviewCapQOpts("Accept?", "Accept as is", "", "One more round", "Run each reviewer once more")), "deny", `"description": ""`)
	h.expect(ask(reviewCapQOpts("Accept?", "Accept", "", "One more round", "")), "deny", "exactly")
	h.expect(ask(reviewCapQOpts("Accept?", "One more round", "", "Accept as is", "")), "deny", "exactly")
	h.expect(ask(reviewCapQOpts("Accept?", "Accept as is", "", "One more round", ""), l82Q("Which colour?", "Colour", "Red", "Blue")), "deny", "the only question")
	if _, r := decision(ask(described)); strings.Contains(r, "the only question") || strings.Contains(r, "exactly") {
		t.Fatalf("a description-only problem should name only the descriptions: %s", r)
	}
}

// C2
func TestReviewCapEmptyDescriptionsRecordedFromRealPayload(t *testing.T) {
	h := cappedTask(t)
	q := reviewCapQOpts("Accept the reviews as they are for this code?", "Accept as is", "", "One more round", "")
	h.l82AskPre(q)
	h.l82RealPost([]any{q}, map[string]string{"Accept the reviews as they are for this code?": "Accept as is"})
	if h.task().ReviewCapOK == "" {
		t.Fatal("Accept as is, asked with empty descriptions, should be recorded")
	}
	if m := h.finishMissing(); strings.Contains(m, "runs are used") {
		t.Fatalf("the accepted reviews should stop blocking: %s", m)
	}
}

// C4: every place that tells Claude to ask says how, with no gloss that reads as a description.
func TestReviewCapAsksSayEmptyDescriptions(t *testing.T) {
	sayHow := func(where, text string) {
		t.Helper()
		if !strings.Contains(text, `"description": ""`) {
			t.Fatalf("%s should say the descriptions are empty: %s", where, text)
		}
		if strings.Contains(text, "(the reviews stay") || strings.Contains(text, "(each of these reviewers") {
			t.Fatalf("%s still glosses the options: %s", where, text)
		}
	}
	h := cappedTask(t)
	sayHow("finish check after the arbiter", h.finishMissing())
	h.selfReview()
	_, r := decision(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}))
	if !strings.Contains(r, "Review cap") {
		t.Fatalf("a fourth reviewer run should be refused toward the Review cap: %s", r)
	}
	sayHow("reviewer-rounds refusal", r)

	off := l82CappedArbiterOff(t)
	sayHow("finish check with the arbiter off", off.finishMissing())

	// The courtly case: every review overlapped an edit, so none counted and the arbiter can't start.
	o := provenTask(t)
	for i, n := range []string{"3", "4", "5"} {
		id, _ := o.startHelper(HelperCodeReviewer)
		o.editA(n)
		write(t, o.findingsPath(id), o.complete(HelperCodeReviewer, fmt.Sprintf("# Review %d\nnothing at 80 or more\n", i+1)))
		o.stopHelper(id, HelperCodeReviewer)
	}
	o.selfReview()
	_, r = decision(o.pre("Agent", l82Agent(l82KindArbiter, "go")))
	if !strings.Contains(r, "no review that counted") {
		t.Fatalf("with no counted review the arbiter should be refused: %s", r)
	}
	sayHow("arbiter-start refusal", r)
	q := reviewCapQOpts("Accept the reviews as they are for this code?", "Accept as is", "", "One more round", "")
	o.l82AskPre(q)
	o.l82RealPost([]any{q}, map[string]string{"Accept the reviews as they are for this code?": "Accept as is"})
	if o.task().ReviewCapOK == "" {
		t.Fatal("after the arbiter is refused, the user's Accept as is should be recorded")
	}
}

// holdRun records a helper run of kind that started earlier and was last
// active quiet ago, with a transcript holding lines when any are given.
func (h *harness) holdRun(kind string, quiet time.Duration, lines ...string) {
	h.t.Helper()
	tk := h.task()
	r := HelperRun{Kind: kind, AgentID: fmt.Sprintf("%s-hold-%d", kind, len(tk.Helpers)+1), Started: Now().Add(-quiet - time.Minute), LastSeen: Now().Add(-quiet)}
	if len(lines) > 0 {
		r.Transcript = filepath.Join(h.t.TempDir(), "agent.jsonl")
		writeTranscript(h.t, r.Transcript, time.Now().Add(-quiet), lines...)
	}
	tk.Helpers = append(tk.Helpers, r)
	must(h.t, SaveTask(tk))
}

const reviewHoldText = "wait for its findings"

func (h *harness) expectHeld(o Output) {
	h.t.Helper()
	h.expect(o, "deny", reviewHoldText)
}

func (h *harness) expectNotHeld(o Output) {
	h.t.Helper()
	if _, r := decision(o); strings.Contains(r, reviewHoldText) {
		h.t.Fatalf("not held by a review, but refused as one: %s", r)
	}
}

// C5, C6, C7, C12, C14
func TestEditsWaitWhileAReviewerRuns(t *testing.T) {
	h := provenTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	id, _ := h.startHelper(HelperCodeReviewer)
	o := h.pre("Edit", map[string]any{"file_path": a})
	h.expectHeld(o)
	_, r := decision(o)
	if !strings.Contains(r, "10 minutes (15 inside one step)") {
		t.Fatalf("the refusal should give both release times: %s", r)
	}
	if strings.Contains(r, "AskUserQuestion") || strings.Contains(r, "ask the user") {
		t.Fatalf("a running review is waited for, not asked about: %s", r)
	}
	h.expectHeld(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "a_test.go")}))
	h.expectHeld(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "notes.md")}))
	h.expectHeld(h.bash("echo '// x' >> src/a.go"))
	h.expectHeld(h.bash("sed -i '' 's/2/3/' src/a.go"))

	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Review 1\nnothing at 80 or more\n"))
	if o := h.stopHelper(id, HelperCodeReviewer); o != nil {
		t.Fatalf("stop: %v", o)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "", "")
}

// C8, C15
func TestReviewHoldLetsNotesAndIgnoredFilesThrough(t *testing.T) {
	h := provenTask(t)
	write(t, filepath.Join(h.repo, ".gitignore"), "scratch/\n")
	run(t, h.repo, "git", "add", ".gitignore")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: ignore scratch")
	h.holdRun(HelperCodeReviewer, 0)
	h.expectHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	docs := TaskDocsDir(h.repo, h.task().ID)
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(docs, DocReport)}), "allow", "")
	h.expectNotHeld(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "scratch", "x.txt")}))
	h.expectHeld(h.bash("gofmt -w ."))
	h.expectNotHeld(h.bash("go generate ./..."))
}

// C9, C13
func TestReviewHoldKindsAndSilence(t *testing.T) {
	for _, kind := range []string{HelperCodeReviewer, HelperSecurity, HelperPlanReviewer, HelperArbiter, HelperGuard} {
		h := provenTask(t)
		h.holdRun(kind, 2*time.Minute)
		h.expectHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	}
	for _, kind := range []string{HelperTracer, HelperTester} {
		h := provenTask(t)
		h.holdRun(kind, 0)
		h.expectNotHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	}

	h := provenTask(t)
	h.holdRun(HelperCodeReviewer, checkedStepMax+time.Minute, tlPrompt, tlRequest)
	if r := h.task().Helpers[len(h.task().Helpers)-1]; r.alive() {
		t.Fatal("a reviewer with no sign of life for 16 minutes is stuck, even mid-step")
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")

	h = provenTask(t)
	h.holdRun(HelperCodeReviewer, WatchQuietHelper-time.Minute, tlPrompt, tlRequest)
	h.expectHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))

	h = provenTask(t)
	h.holdRun(HelperCodeReviewer, 2*time.Minute, tlPrompt, tlRequest, tlInterrupt)
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
}

// C10, C11, C16
func TestBuilderMergeWaitsForReviewDropDoesNot(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	h.holdRun(HelperCodeReviewer, 0)
	h.expectNotHeld(PreToolUse(h.helperIn(id, HelperBuilder, "Edit", map[string]any{"file_path": filepath.Join(wt, "src", "a.go")})))
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 9 }\n")
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: changed\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("stop: %v", o)
	}
	tk := h.task()
	if _, _, err := MergeBuilder(tk, 1, nil); err == nil || !strings.Contains(err.Error(), "review") {
		t.Fatalf("a merge while a reviewer runs should wait for it: %v", err)
	}
	if err := DropBuilder(tk, 1); err != nil {
		t.Fatalf("a drop doesn't touch the checkout, so it goes ahead: %v", err)
	}
}

// C18
func TestWatchGivesReviewerProgressEveryFiveMinutes(t *testing.T) {
	h := provenTask(t)
	step := fixedNow(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	w := NewWatch(h.repo, h.task().ID)
	if lines, busy := w.Check(); !busy || len(lines) != 0 {
		t.Fatalf("nothing to say right after the start: %q busy %v", lines, busy)
	}
	touch := func() {
		t.Helper()
		tk := h.task()
		tk.runByAgent(id).LastSeen = Now()
		must(t, SaveTask(tk))
	}
	step(4 * time.Minute)
	touch()
	if lines, _ := w.Check(); len(lines) != 0 {
		t.Fatalf("no update before 5 minutes: %q", lines)
	}
	step(time.Minute + time.Second)
	touch()
	lines, busy := w.Check()
	if !busy || len(lines) != 1 || !strings.Contains(lines[0], "code-reviewer 1 is still working") || !strings.Contains(lines[0], "tell the user") {
		t.Fatalf("a progress line at 5 minutes: %q", lines)
	}
	step(2 * time.Minute)
	touch()
	if lines, _ := w.Check(); len(lines) != 0 {
		t.Fatalf("not again within 5 minutes: %q", lines)
	}
	step(3 * time.Minute)
	touch()
	if lines, _ := w.Check(); len(lines) != 1 || !strings.Contains(lines[0], "10 min so far") {
		t.Fatalf("the next update at 10 minutes: %q", lines)
	}
}

// C19
func TestStuckReviewerIsDroppedAndRunsAgain(t *testing.T) {
	h := provenTask(t)
	step := fixedNow(t)
	for i := 1; i < MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	h.editA("7")
	id, _ := h.startHelper(HelperCodeReviewer)
	w := NewWatch(h.repo, h.task().ID)
	w.Check()
	step(WatchQuietHelper + time.Second)
	lines, _ := w.Check()
	if !strings.Contains(joined(lines), "code-reviewer 3 has shown no sign of life") || !strings.Contains(joined(lines), "Start a fresh one") {
		t.Fatalf("the watcher should call the reviewer stuck: %q", lines)
	}
	tk := h.task()
	if n := len(tk.helperRuns(HelperCodeReviewer)); n != MaxReviews-1 {
		t.Fatalf("a stuck run doesn't use one of the runs: %d counted", n)
	}
	if r := tk.runByAgent(id); r == nil || !r.Finished.IsZero() {
		t.Fatal("the stuck run is left as it was: the watcher writes nothing")
	}
	if m := h.finishMissing(); strings.Contains(m, "still running") || !strings.Contains(m, "plumbline:code-reviewer") {
		t.Fatalf("the finish check should ask for a fresh review: %s", m)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
	h.selfReview()
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer", "prompt": "go"}), "", "")
}

// C20
func TestReviewerToolCallsAreSignsOfLife(t *testing.T) {
	h := provenTask(t)
	step := fixedNow(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	for i := 0; i < 4; i++ {
		step(4 * time.Minute)
		h.helperPost(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
		PreToolUse(h.helperIn(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	}
	if r := h.task().runByAgent(id); !r.alive() {
		t.Fatal("a reviewer reading files every 4 minutes for 16 minutes is alive")
	}
	h.expectHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
}

// C21
func TestBuilderKeepsItsTiming(t *testing.T) {
	h := buildingTask(t)
	h.holdRun(HelperBuilder, WatchQuietHelper+time.Minute)
	if r := h.task().Helpers[len(h.task().Helpers)-1]; !r.alive() {
		t.Fatal("a builder quiet 11 minutes is still running (30-minute window)")
	}
	if n := len(h.task().helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("a quiet builder still counts: %d", n)
	}
}

// H2.4: one long step (a foreground Bash call ends within 10 minutes) isn't stuck; past 15 minutes it is.
func TestReviewerInOneLongStepIsNotStuck(t *testing.T) {
	h := provenTask(t)
	h.holdRun(HelperCodeReviewer, WatchQuietHelper+2*time.Minute, tlPrompt, tlToolUse)
	h.expectHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	if n := len(h.task().helperRuns(HelperCodeReviewer)); n != 1 {
		t.Fatalf("a reviewer 12 minutes into one tool call still counts: %d", n)
	}

	h = provenTask(t)
	h.holdRun(HelperCodeReviewer, WatchQuietHelper+2*time.Minute, tlPrompt, tlToolUse, tlToolResult, tlAnswer)
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
}

// H2.3, H3.3, H3.4: a fresh run after a stuck one gets its own findings file, and the stuck one stays out if it comes back.
func TestFreshRunAfterStuckOneKeepsTheCount(t *testing.T) {
	h := provenTask(t)
	step := fixedNow(t)
	for i := 1; i < MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	h.editA("7")
	old, _ := h.startHelper(HelperCodeReviewer)
	step(WatchQuietHelper + time.Second)
	fresh, _ := h.startHelper(HelperCodeReviewer)
	tk := h.task()
	if a, b := tk.runByAgent(old).Findings, tk.runByAgent(fresh).Findings; a == b {
		t.Fatalf("the fresh run must not take the stuck run's findings file: both %s", a)
	}
	if !tk.runByAgent(old).Stuck {
		t.Fatal("starting a fresh run sets the stuck one aside for good")
	}
	PreToolUse(h.helperIn(old, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
	h.helperPost(old, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	tk = h.task()
	if n := len(tk.helperRuns(HelperCodeReviewer)); n != MaxReviews {
		t.Fatalf("a stuck run that comes back stays out of the count: %d counted", n)
	}
	if tk.runByAgent(old).alive() {
		t.Fatal("a stuck run that comes back isn't running for Plumbline")
	}
	write(t, h.findingsPath(fresh), h.complete(HelperCodeReviewer, "# Review 3\nnothing at 80 or more\n"))
	if o := h.stopHelper(fresh, HelperCodeReviewer); o != nil {
		t.Fatalf("stop: %v", o)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
}

// H2.5: a run that can never count holds nothing.
func TestReviewHoldSkipsUncheckedRuns(t *testing.T) {
	h := provenTask(t)
	h.holdRun(HelperCodeReviewer, 0)
	tk := h.task()
	tk.Helpers[len(tk.Helpers)-1].Unchecked = true
	must(t, SaveTask(tk))
	h.expectNotHeld(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}))
}

// H3.5: the hold isn't only for the lead: another subagent editing the checkout waits too.
func TestReviewHoldCoversOtherSubagents(t *testing.T) {
	h := provenTask(t)
	h.holdRun(HelperCodeReviewer, 0)
	for _, agentType := range []string{"general-purpose", "Explore"} {
		in := h.in("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
		in.AgentID, in.AgentType = "other-1", agentType
		o := PreToolUse(in)
		h.expect(o, "deny", "tell the lead")
		if _, r := decision(o); strings.Contains(r, "you are told") {
			t.Fatalf("a subagent isn't told when the reviewer hands back: %s", r)
		}
	}
}
