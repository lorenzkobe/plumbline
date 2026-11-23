package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	l82KindGuard   = "guard-reviewer"
	l82KindArbiter = "arbiter"

	l82PlanBase = "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n"
	l82NoneSec  = "## Guards loosened\n- none\n"
	l82GuardSec = "## Guards loosened\n- the dirty-tree ask (src/a.go:3 `func A() int`): reached by plain switch, switch -f\n"
	l82Guard2   = "## Guards loosened\n- the dirty-tree ask (src/a.go:3 `func A() int`): reached by plain switch, switch -f, switch -q\n"
	l82Note     = "## Challenge note\n- nothing changed\n"

	l82NoneFindings  = "## Findings\n- none\n## Set aside\n- none\n"
	l82OpenMustFix   = "## Findings\n- [90] must-fix: A still returns the wrong value (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n## Set aside\n- none\n"
	l82ClearedReport = "# Report\n" + reportEnd + "## Review\n- code reviewer: out of rounds, cleared by the arbiter (arbiter-1.md)\n## For you\n- nothing\n"
)

func l82NonePlan() string  { return l82PlanBase + l82NoneSec + l82Note }
func l82GuardPlan() string { return l82PlanBase + l82GuardSec + l82Note }

func l82Agent(kind, prompt string) map[string]any {
	return map[string]any{"subagent_type": "plumbline:" + kind, "prompt": prompt}
}

// l82Finish writes a helper's findings file and stops it.
func (h *harness) l82Finish(id, kind, text string) Output {
	h.t.Helper()
	p := h.findingsPath(id)
	h.expect(PreToolUse(h.helperIn(id, kind, "Write", map[string]any{"file_path": p})), "allow", "")
	write(h.t, p, text)
	return h.stopHelper(id, kind)
}

// l82Run starts a helper through the gate, writes its findings and stops it.
func (h *harness) l82Run(kind, text string) string {
	h.t.Helper()
	id, _ := h.startHelper(kind)
	if o := h.l82Finish(id, kind, text); o != nil {
		h.t.Fatalf("%s stop blocked: %v", kind, o)
	}
	return id
}

func (h *harness) l82ForcedStop(id, kind string) Output {
	return SubagentStop(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + kind, StopHookActive: true})
}

func (h *harness) l82Checkpoint() (string, string) {
	return decision(h.pre("AskUserQuestion", checkpointQ()))
}

// l82Capped: a small task whose code reviewer used its 3 runs, then the code
// changed (no assertion on the finish wording).
func l82Capped(t *testing.T) *harness {
	t.Helper()
	h := provenTask(t)
	for i := 1; i <= MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	h.editA("7")
	return h
}

func (h *harness) l82Arbiter(text string) string {
	h.t.Helper()
	h.selfReview()
	return h.l82Run(l82KindArbiter, text)
}

// l82CappedArbiterOff: l82Capped in a project whose setup switched the
// arbiter off, committed before the task starts (a setup file changed during
// the task would call for more reviewers).
func l82CappedArbiterOff(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.setup()
	s, _, _ := LoadSetup(h.repo)
	s.Parts = map[string]bool{"arbiter": false}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: arbiter off")
	h.startTask("small")
	h.ground()
	h.docs(DocPlan, l82NonePlan())
	h.approve()
	h.redRun()
	h.editA("2")
	for i := 1; i <= MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	h.editA("7")
	return h
}

// C1
func TestL82PlanWithoutGuardsLoosenedIsRefused(t *testing.T) {
	for _, size := range []string{"small", "normal"} {
		t.Run(size, func(t *testing.T) {
			h := groundedTask(t, size)
			h.docs(DocPlan, l82PlanBase+l82Note)
			if size != "small" {
				h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
			}
			if d, r := h.l82Checkpoint(); d != "deny" || !strings.Contains(r, "Guards loosened") {
				t.Fatalf("a %s plan with no \"## Guards loosened\": %q (%s), want a deny naming the section", size, d, r)
			}
		})
	}
}

// C2
func TestL82GuardsLoosenedNonePasses(t *testing.T) {
	for _, size := range []string{"small", "normal"} {
		t.Run(size, func(t *testing.T) {
			h := groundedTask(t, size)
			h.docs(DocPlan, l82NonePlan())
			if size != "small" {
				h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
			}
			if d, r := h.l82Checkpoint(); d == "deny" {
				t.Fatalf("a %s plan with \"- none\" under Guards loosened: denied (%s)", size, r)
			}
			h.approve()
		})
	}
}

// C3
func TestL82GuardBulletNeedsFileLineAndNoneAlone(t *testing.T) {
	cases := map[string]string{
		"no file:line":       "## Guards loosened\n- the dirty-tree ask: reached by plain switch, switch -f\n",
		"none next to guard": "## Guards loosened\n- none\n- the dirty-tree ask (src/a.go:3 `func A() int`): reached by plain switch\n",
	}
	for name, sec := range cases {
		t.Run(name, func(t *testing.T) {
			h := groundedTask(t, "small")
			h.docs(DocPlan, l82PlanBase+sec+l82Note)
			h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
			if d, r := h.l82Checkpoint(); d != "deny" || !strings.Contains(r, "Guards loosened") {
				t.Fatalf("%s: %q (%s), want a deny naming Guards loosened", name, d, r)
			}
		})
	}
}

// C4
func TestL82GuardPlanNeedsGuardReviewerBeforeCheckpoint(t *testing.T) {
	for _, size := range []string{"small", "normal"} {
		t.Run(size, func(t *testing.T) {
			h := groundedTask(t, size)
			h.docs(DocPlan, l82GuardPlan())
			if size != "small" {
				h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
			}
			if d, r := h.l82Checkpoint(); d != "deny" || !strings.Contains(r, "plumbline:guard-reviewer") {
				t.Fatalf("a %s plan that loosens a guard, no guard review: %q (%s), want a deny naming plumbline:guard-reviewer", size, d, r)
			}
			id := h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
			if got := filepath.Base(h.findingsPath(id)); got != "guard-review-1.md" {
				t.Fatalf("guard reviewer findings file = %q, want guard-review-1.md", got)
			}
			if d, r := h.l82Checkpoint(); d == "deny" {
				t.Fatalf("after the guard review: denied (%s)", r)
			}
			h.approve()
		})
	}
}

// C5
func TestL82GuardReviewerStartRefusals(t *testing.T) {
	t.Run("plan loosens nothing", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82NonePlan())
		h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "")
	})
	t.Run("no plan yet", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "")
	})
	t.Run("after approval and after 2 rounds", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82GuardPlan())
		h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
		h.docs(DocPlan, l82PlanBase+l82Guard2+l82Note)
		h.l82Run(l82KindGuard, "# Guard review 2\n"+l82NoneFindings)
		h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "")
		h.approve()
		h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "")
	})
	t.Run("after approval, first round", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82GuardPlan())
		h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
		h.approve()
		h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "")
	})
}

// C6
func TestL82GuardReviewStaleWhenGuardListChanges(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, l82GuardPlan())
	id, _ := h.startHelper(l82KindGuard)
	time.Sleep(10 * time.Millisecond)
	h.docs(DocPlan, l82PlanBase+l82Guard2+l82Note)
	if o := h.l82Finish(id, l82KindGuard, "# Guard review 1\n"+l82NoneFindings); o != nil {
		t.Fatalf("stop blocked: %v", o)
	}
	if d, r := h.l82Checkpoint(); d != "deny" || !strings.Contains(r, "plumbline:guard-reviewer") {
		t.Fatalf("the guard list changed during the review: %q (%s), want a deny naming plumbline:guard-reviewer", d, r)
	}
	h.l82Run(l82KindGuard, "# Guard review 2\n"+l82NoneFindings)
	if d, r := h.l82Checkpoint(); d == "deny" {
		t.Fatalf("a guard review on the current guard list: denied (%s)", r)
	}
	// Only the goal changes: the guard list is the same, so the review holds.
	h.docs(DocPlan, strings.Replace(l82PlanBase, "A works as planned", "A returns the planned value", 1)+l82Guard2+l82Note)
	if d, r := h.l82Checkpoint(); d == "deny" {
		t.Fatalf("a plan edit outside the guard list: denied (%s)", r)
	}
}

// C7
func TestL82GuardReviewerReadsOnlyPlanUnderstandingAndOwn(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, l82GuardPlan())
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs("notes-extra.md", "# Extra\nscratch\n")
	id, _ := h.startHelper(l82KindGuard)
	docs := TaskDocsDir(h.repo, h.task().ID)
	read := func(name string) (string, string) {
		return decision(PreToolUse(h.helperIn(id, l82KindGuard, "Read", map[string]any{"file_path": filepath.Join(docs, name)})))
	}
	for _, ok := range []string{DocPlan, DocUnderstanding, "guard-review-1.md"} {
		if d, r := read(ok); d == "deny" {
			t.Errorf("guard reviewer reading %s: denied (%s)", ok, r)
		}
	}
	for _, bad := range []string{"challenge-1.md", "notes-extra.md", "Challenge-1.md"} {
		if d, _ := read(bad); d != "deny" {
			t.Errorf("guard reviewer reading %s: %q, want deny", bad, d)
		}
	}
	if d, _ := decision(PreToolUse(h.helperIn(id, l82KindGuard, "Grep", map[string]any{"pattern": "x", "path": docs}))); d != "deny" {
		t.Errorf("guard reviewer searching the notes folder: %q, want deny", d)
	}
	h.expect(PreToolUse(h.helperIn(id, l82KindGuard, "Write", map[string]any{"file_path": filepath.Join(docs, DocPlan)})), "deny", "")
	h.expect(PreToolUse(h.helperIn(id, l82KindGuard, "Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")})), "deny", "")
	h.expect(PreToolUse(h.helperIn(id, l82KindGuard, "Bash", map[string]any{"command": "rm src/a.go"})), "deny", "")
}

// C8
func TestL82GuardReviewerFindingsChecks(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, l82GuardPlan())
	id, _ := h.startHelper(l82KindGuard)
	p := h.findingsPath(id)
	bare := "# Guard review 1\n## Findings\n- [90] must-fix: switch -m also reaches the ask (src/a.go:3 `func A() int`). Why: missing shape.\n## Set aside\n- none\n"
	write(t, p, bare)
	o := h.stopHelper(id, l82KindGuard)
	if o["decision"] != "block" || !strings.Contains(fmtOut(o), "Fails when") {
		t.Fatalf("a guard review must-fix with no Fails when should be sent back: %v", o)
	}
	badQuote := "# Guard review 1\n## Findings\n- [90] must-fix: x (src/a.go:3 `nothing like this line`). Fails when: git switch -m other -> the edit is lost.\n## Set aside\n- none\n"
	write(t, p, badQuote)
	if o := h.stopHelper(id, l82KindGuard); o["decision"] != "block" {
		t.Fatalf("a quote that isn't near the cited line should be sent back: %v", o)
	}
	good := strings.Replace(bare, "Why: missing shape.", "Fails when: git switch -m other -> the edit is lost.", 1)
	write(t, p, good)
	if o := h.stopHelper(id, l82KindGuard); o != nil {
		t.Fatalf("a well-formed guard review should stop: %v", o)
	}
}

// C9
func TestL82ChallengerStartNoteNamesGuardList(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, l82GuardPlan())
	_, o := h.startHelper(HelperChallenger)
	if s := contextOf(o); !strings.Contains(strings.ToLower(s), "loosen") {
		t.Fatalf("the challenger's start note should ask it to check the Guards loosened list: %s", s)
	}
}

// C10
func TestL82CappedAfterChangeAsksForArbiter(t *testing.T) {
	h := l82Capped(t)
	m := h.finishMissing()
	if !strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("a capped reviewer after a code change should send Claude to the arbiter: %s", m)
	}
	if strings.Contains(m, `header "Review cap"`) {
		t.Fatalf("the Review cap question comes after the arbiter, not first: %s", m)
	}
}

// C11
func TestL82ArbiterClearsCappedReviews(t *testing.T) {
	h := l82Capped(t)
	h.expect(h.pre("AskUserQuestion", commitQ()), "deny", "")
	id := h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	if got := filepath.Base(h.findingsPath(id)); got != "arbiter-1.md" {
		t.Fatalf("arbiter findings file = %q, want arbiter-1.md", got)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code reviewer: three rounds\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "cleared by the arbiter") {
		t.Fatalf("the report must say the reviews were cleared by the arbiter: %s", m)
	}
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("an arbiter clear plus the report saying so should finish: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", commitQ()), "", "")
}

// C12
func TestL82ArbiterOpenMustFixAsksReviewCap(t *testing.T) {
	h := l82Capped(t)
	h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix)
	m := h.finishMissing()
	if !strings.Contains(m, `"Review cap"`) || !strings.Contains(m, "arbiter-1.md") {
		t.Fatalf("an open must-fix from the arbiter should lead to the Review cap question, naming arbiter-1.md: %s", m)
	}
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m == "" {
		t.Fatal("an arbiter with an open must-fix doesn't clear the reviews")
	}
	h.answerReviewCap("Accept as is")
	h.docs(DocReport, acceptedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("after Accept as is the old flow finishes: %s", m)
	}
}

// C13
func TestL82CodeChangeAfterArbiterClearMakesItDue(t *testing.T) {
	h := l82Capped(t)
	h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	h.editA("8")
	h.docs(DocReport, l82ClearedReport)
	m := h.finishMissing()
	if !strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("a code change after the arbiter cleared makes it due again: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", commitQ()), "deny", "")
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "deny", "arbiter")
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "", "")
}

// C14
func TestL82ArbiterStartRefusals(t *testing.T) {
	t.Run("before building", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82NonePlan())
		h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
	})
	t.Run("no reviewer capped", func(t *testing.T) {
		h := provenTask(t)
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
		h.editA("7")
		h.selfReview()
		h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
	})
	t.Run("already ruled on this code", func(t *testing.T) {
		for _, text := range []string{l82NoneFindings, l82OpenMustFix} {
			h := l82Capped(t)
			h.l82Arbiter("# Arbiter 1\n" + text)
			h.selfReview()
			h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
		}
	})
	t.Run("past 3 runs", func(t *testing.T) {
		h := l82Capped(t)
		for i, n := range []string{"8", "9", "5"} {
			h.l82Arbiter(fmt.Sprintf("# Arbiter %d\n", i+1) + l82OpenMustFix)
			h.editA(n)
		}
		h.selfReview()
		h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
		// C15: with the arbiter's runs used, the Review cap question is allowed.
		h.expect(h.pre("AskUserQuestion", reviewCapQ()), "", "")
	})
}

// C15
func TestL82ReviewCapQuestionGatedByArbiter(t *testing.T) {
	t.Run("while due", func(t *testing.T) {
		h := l82Capped(t)
		h.expect(h.pre("AskUserQuestion", reviewCapQ()), "deny", "arbiter")
	})
	t.Run("after it cleared", func(t *testing.T) {
		h := l82Capped(t)
		h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
		h.expect(h.pre("AskUserQuestion", reviewCapQ()), "deny", "")
	})
	t.Run("after an open must-fix", func(t *testing.T) {
		h := l82Capped(t)
		h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix)
		h.expect(h.pre("AskUserQuestion", reviewCapQ()), "", "")
	})
}

// C16
func TestL82ArbiterReadsFindingsNotReport(t *testing.T) {
	h := l82Capped(t)
	report := "# Report\n## Review\n- the cache layer silently drops the second write when two browser tabs save the same draft together\n"
	h.docs(DocReport, report)
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "Check the open findings. Note: the cache layer silently drops the second write when two browser tabs save the same draft together.")), "deny", "report.md")
	id, _ := h.startHelper(l82KindArbiter)
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3 `func A() int`)\n")
	docs := TaskDocsDir(h.repo, h.task().ID)
	read := func(name string) (string, string) {
		return decision(PreToolUse(h.helperIn(id, l82KindArbiter, "Read", map[string]any{"file_path": filepath.Join(docs, name)})))
	}
	for _, ok := range []string{"code-review-3.md", DisputesName(HelperCodeReviewer), "arbiter-1.md"} {
		if d, r := read(ok); d == "deny" {
			t.Errorf("arbiter reading %s: denied (%s)", ok, r)
		}
	}
	for _, bad := range []string{DocReport, DocSelfReview, "Report.md", "SELF-REVIEW.md"} {
		if d, _ := read(bad); d != "deny" {
			t.Errorf("arbiter reading %s: %q, want deny", bad, d)
		}
	}
	h.expect(PreToolUse(h.helperIn(id, l82KindArbiter, "Write", map[string]any{"file_path": filepath.Join(docs, DocReport)})), "deny", "")
	h.expect(PreToolUse(h.helperIn(id, l82KindArbiter, "Bash", map[string]any{"command": "cat " + filepath.Join(docs, DocReport)})), "deny", "")
}

// C17
func TestL82ArbiterOffKeepsOldReviewCapFlow(t *testing.T) {
	h := l82CappedArbiterOff(t)
	m := h.finishMissing()
	if !strings.Contains(m, `"Review cap"`) || strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("with the arbiter off, the finish asks the Review cap question as before: %s", m)
	}
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
	h.answerReviewCap("Accept as is")
	h.docs(DocReport, acceptedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("Accept as is with the arbiter off: %s", m)
	}
}

// C18
func TestL82FailedArbiterFindingsDoNotClear(t *testing.T) {
	h := l82Capped(t)
	h.selfReview()
	id, _ := h.startHelper(l82KindArbiter)
	p := h.findingsPath(id)
	write(t, p, "# Arbiter 1\n## Findings\n- none\n## Checked\n- the fix (src/a.go:3 `nothing like this line`)\n## Set aside\n- none\n")
	if o := h.stopHelper(id, l82KindArbiter); o["decision"] != "block" {
		t.Fatalf("an arbiter file with a bad quote is sent back: %v", o)
	}
	h.l82ForcedStop(id, l82KindArbiter)
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m == "" || !strings.Contains(m, "arbiter") {
		t.Fatalf("an arbiter run whose findings failed the checks must not clear: %q", m)
	}
}

// l82RealPost: an AskUserQuestion PostToolUse shaped like Claude Code's real
// one (2026-11-22 capture): the answers in tool_input too, and tool_response
// {"questions":[...],"answers":{...},"annotations":{}}, decoded the way the
// hook decodes stdin.
func (h *harness) l82RealPost(questions []any, answers map[string]string) Output {
	h.t.Helper()
	qs, err := json.Marshal(questions)
	must(h.t, err)
	as, err := json.Marshal(answers)
	must(h.t, err)
	raw := fmt.Sprintf(`{"session_id":"other-session","transcript_path":"/home/u/.claude/projects/p/s.jsonl","cwd":"/somewhere/else","permission_mode":"default","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":%s,"answers":%s},"tool_response":{"questions":%s,"answers":%s,"annotations":{}},"tool_use_id":"toolu_01AbCdEfGhIjKlMnOpQrStUv"}`, qs, as, qs, as)
	in, err := ReadHookInput(strings.NewReader(raw))
	must(h.t, err)
	in.Cwd, in.SessionID = h.repo, h.sid
	return PostToolUse(in)
}

func l82Q(question, header string, labels ...string) map[string]any {
	var opts []any
	for _, l := range labels {
		opts = append(opts, map[string]any{"label": l})
	}
	return map[string]any{"question": question, "header": header, "options": opts, "multiSelect": false}
}

func (h *harness) l82AskPre(q map[string]any) {
	h.t.Helper()
	if d, r := decision(h.pre("AskUserQuestion", map[string]any{"questions": []any{q}})); d == "deny" {
		h.t.Fatalf("question %q refused before the answer: %s", q["header"], r)
	}
}

// C19
func TestL82RecordersFromRealPayload(t *testing.T) {
	t.Run("Checkpoint approve", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82NonePlan())
		q := map[string]any{"question": "Change A to return 2. Go ahead?", "header": "Checkpoint", "multiSelect": false, "options": []any{
			map[string]any{"label": "Approve", "description": "Build it as planned"},
			map[string]any{"label": "Change something", "description": "Say what to change"},
		}}
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Change A to return 2. Go ahead?": "Approve"})
		if p := h.task().Phase; p != PhaseBuilding {
			t.Fatalf("approval from the real payload not recorded: phase %s", p)
		}
	})
	t.Run("Drop", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82NonePlan())
		id := h.task().ID
		q := l82Q("Change A to return 2. Go ahead?", "Checkpoint", "Approve", "Change something", "Drop it")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Change A to return 2. Go ahead?": "Drop it"})
		if p := phaseOf(t, h, id); p != PhaseCancelled {
			t.Fatalf("Drop it from the real payload not recorded: phase %s", p)
		}
	})
	t.Run("Close task", func(t *testing.T) {
		h := groundedTask(t, "small")
		id := h.task().ID
		q := l82Q("Close this task now?", "Close task", "Close it", "Keep it open")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Close this task now?": "Close it"})
		if p := phaseOf(t, h, id); p != PhaseCancelled {
			t.Fatalf("Close it from the real payload not recorded: phase %s", p)
		}
	})
	t.Run("Review cap Accept as is", func(t *testing.T) {
		h := l82CappedArbiterOff(t)
		q := l82Q("The reviews ran out of rounds. Go on how?", "Review cap", "Accept as is", "One more round")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"The reviews ran out of rounds. Go on how?": "Accept as is"})
		h.docs(DocReport, acceptedReport)
		if m := h.finishMissing(); m != "" {
			t.Fatalf("Accept as is from the real payload not recorded: %s", m)
		}
	})
	t.Run("Review cap One more round", func(t *testing.T) {
		h := l82CappedArbiterOff(t)
		q := l82Q("The reviews ran out of rounds. Go on how?", "Review cap", "Accept as is", "One more round")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"The reviews ran out of rounds. Go on how?": "One more round"})
		h.runHelper(HelperCodeReviewer, "# Review 4\nnothing at 80 or more\n")
		h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- four rounds, nothing at 80 or more\n## For you\n- nothing\n")
		if m := h.finishMissing(); m != "" {
			t.Fatalf("One more round from the real payload not recorded: %s", m)
		}
	})
	t.Run("Review cap after the arbiter's open must-fix", func(t *testing.T) {
		h := l82Capped(t)
		h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix)
		q := l82Q("The reviews ran out of rounds. Go on how?", "Review cap", "Accept as is", "One more round")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"The reviews ran out of rounds. Go on how?": "Accept as is"})
		h.docs(DocReport, acceptedReport)
		if m := h.finishMissing(); m != "" {
			t.Fatalf("Accept as is after the arbiter from the real payload not recorded: %s", m)
		}
	})
	t.Run("Commit it", func(t *testing.T) {
		h := provenTask(t)
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
		h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code-review-1: nothing at 80 or more\n## For you\n- nothing\n")
		if m := h.finishMissing(); m != "" {
			t.Fatalf("setup: the task should be finished: %s", m)
		}
		q := l82Q("Commit the change now?", "Commit", "Commit it", "Leave uncommitted")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Commit the change now?": "Commit it"})
		if h.task().CommitOK == "" {
			t.Fatal("Commit it from the real payload not recorded")
		}
	})
	t.Run("Direction", func(t *testing.T) {
		h := designTaskH(t)
		h.docs(DocDesign, designDoc)
		q := l82Q("Which look fits?", "Direction", "Direction A: bold", "Direction B: calm", "Direction C: dense")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Which look fits?": "Direction B: calm"})
		if got := h.task().Direction; got != "Direction B: calm" {
			t.Fatalf("Direction pick from the real payload not recorded: %q", got)
		}
	})
}

// C20
func TestL82DebugCapturesEveryAskUserPost(t *testing.T) {
	h := groundedTask(t, "small")
	p := filepath.Join(DataDir(), "debug", "askuser-post.json")
	read := func() map[string]any {
		t.Helper()
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("no debug capture at %s: %v", p, err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("debug capture isn't JSON: %v\n%s", err, b)
		}
		return m
	}
	t.Setenv("PLUMBLINE_DEBUG", "")
	h.l82RealPost([]any{l82Q("Which colour first?", "Colour", "Red", "Blue")}, map[string]string{"Which colour first?": "Red"})
	if _, err := os.Stat(p); err == nil {
		t.Fatal("without PLUMBLINE_DEBUG nothing is captured")
	}
	t.Setenv("PLUMBLINE_DEBUG", "1")
	h.l82RealPost([]any{l82Q("Which colour first?", "Colour", "Red", "Blue")}, map[string]string{"Which colour first?": "Red"})
	m := read()
	for _, k := range []string{"session_id", "tool_name", "tool_input", "tool_response"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("debug capture lacks %q: %v", k, m)
		}
	}
	b, _ := json.Marshal(m)
	if !strings.Contains(string(b), "Which colour first?") || !strings.Contains(string(b), "annotations") {
		t.Fatalf("debug capture should hold the whole input: %s", b)
	}
	h.docs(DocPlan, l82NonePlan())
	cq := l82Q("Change A. Go ahead?", "Checkpoint", "Approve", "Change something")
	h.l82RealPost([]any{cq}, map[string]string{"Change A. Go ahead?": "Change something"})
	b, _ = json.Marshal(read())
	if !strings.Contains(string(b), "Change A. Go ahead?") || strings.Contains(string(b), "Which colour first?") {
		t.Fatalf("each question overwrites the capture: %s", b)
	}
	t.Run("no task", func(t *testing.T) {
		h2 := newHarness(t)
		t.Setenv("PLUMBLINE_DEBUG", "1")
		h2.l82RealPost([]any{l82Q("Which shape?", "Shape", "Round", "Square")}, map[string]string{"Which shape?": "Round"})
		b, err := os.ReadFile(filepath.Join(DataDir(), "debug", "askuser-post.json"))
		if err != nil || !strings.Contains(string(b), "Which shape?") {
			t.Fatalf("a question outside a task is captured too: %v %s", err, b)
		}
	})
}

// C21
func TestL82RulesTaskUnderSizeLimit(t *testing.T) {
	fi, err := os.Stat("rules-task.md")
	must(t, err)
	if fi.Size() >= 10*1024 {
		t.Fatalf("rules-task.md is %d bytes, over 10 KB", fi.Size())
	}
}

// C22, C25
func TestL82ResumedHelpersAreRegated(t *testing.T) {
	t.Run("guard reviewer past its rounds", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82GuardPlan())
		h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
		h.docs(DocPlan, l82PlanBase+l82Guard2+l82Note)
		id := h.l82Run(l82KindGuard, "# Guard review 2\n"+l82NoneFindings)
		h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "deny", "")
		h.approve()
		h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "deny", "")
	})
	t.Run("guard reviewer after approval", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82GuardPlan())
		id := h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
		h.approve()
		h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "deny", "")
	})
	t.Run("arbiter with nothing due", func(t *testing.T) {
		h := l82Capped(t)
		id := h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
		h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "deny", "")
	})
	t.Run("unchecked arbiter clears nothing", func(t *testing.T) {
		h := l82Capped(t)
		h.selfReview()
		id := "arbiter-direct"
		SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + l82KindArbiter})
		r := h.task().runByAgent(id)
		if r == nil {
			t.Fatal("an arbiter start that skipped the gate should still be recorded")
		}
		if !r.Unchecked {
			t.Fatalf("an arbiter start that skipped the gate must be unchecked: %+v", r)
		}
		write(t, filepath.Join(TaskDocsDir(h.repo, h.task().ID), r.Findings), "# Arbiter\n"+l82NoneFindings)
		h.stopHelper(id, l82KindArbiter)
		h.docs(DocReport, l82ClearedReport)
		if m := h.finishMissing(); !strings.Contains(m, "plumbline:arbiter") {
			t.Fatalf("an unchecked arbiter run clears nothing: %s", m)
		}
	})
	t.Run("unchecked guard reviewer counts for nothing", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82GuardPlan())
		id := "guard-direct"
		SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + l82KindGuard})
		r := h.task().runByAgent(id)
		if r == nil || !r.Unchecked {
			t.Fatalf("a guard reviewer start that skipped the gate must be recorded unchecked: %+v", r)
		}
		write(t, filepath.Join(TaskDocsDir(h.repo, h.task().ID), r.Findings), "# Guard review\n"+l82NoneFindings)
		h.stopHelper(id, l82KindGuard)
		if d, r := h.l82Checkpoint(); d != "deny" || !strings.Contains(r, "plumbline:guard-reviewer") {
			t.Fatalf("an unchecked guard review counts for nothing: %q (%s)", d, r)
		}
	})
}

// C23, C26
func TestL82ArbiterRefusedWhenGitCantDiff(t *testing.T) {
	h := l82Capped(t)
	tk := h.task()
	bogus := strings.Repeat("1", 40)
	for i := len(tk.Helpers) - 1; i >= 0; i-- {
		if tk.Helpers[i].Kind == HelperCodeReviewer {
			tk.Helpers[i].PrintBefore, tk.Helpers[i].PrintAfter = bogus, bogus
			break
		}
	}
	must(t, SaveTask(tk))
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "")
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m == "" {
		t.Fatal("nothing is cleared when the arbiter couldn't start")
	}
}

// C24, C27
func TestL82DisputesCappedNoChangeSkipsArbiter(t *testing.T) {
	h := reviewedTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 2\nnothing new\n")
	h.runHelper(HelperCodeReviewer, "# Code review 3\nnothing new\n")
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	if m := h.finishMissing(); strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("open disputes on unchanged code need no arbiter: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "", "")
}

// dirty-tree command shapes (0.8.1 review fixes)
func l82DirtyTask(t *testing.T) *harness {
	t.Helper()
	h := buildingTask(t)
	run(t, h.repo, "git", "branch", "other")
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 4 }\n")
	return h
}

func l82WantAsk(t *testing.T, h *harness, cmds ...string) {
	t.Helper()
	for _, cmd := range cmds {
		if d, r := decision(h.bash(cmd)); d != "ask" {
			t.Errorf("%q: %q (%s), want an ask", cmd, d, r)
		}
	}
}

func l82WantFree(t *testing.T, h *harness, cmds ...string) {
	t.Helper()
	for _, cmd := range cmds {
		if d, r := decision(h.bash(cmd)); d == "ask" || d == "deny" {
			t.Errorf("%q: %q (%s), want no Plumbline prompt", cmd, d, r)
		}
	}
}

// C28
func TestL82DirtyTreeForcedCreateSwitchesAsk(t *testing.T) {
	h := l82DirtyTask(t)
	l82WantAsk(t, h, "git checkout -b n -fq", "git switch -c n --discard o", "git switch -c n --forc o", "git switch --orphan o --disc")
	l82WantFree(t, h, "git checkout -b fresh")
}

// C29
func TestL82GitEnvBeforeCheckoutAsks(t *testing.T) {
	cmds := []string{
		"GIT_DIR=../x/.git git checkout src/a.go",
		"GIT_WORK_TREE=../wt git checkout src/a.go",
		"GIT_INDEX_FILE=/tmp/idx git checkout src/a.go",
		"GIT_COMMON_DIR=../x/.git git checkout src/a.go",
		"git -c core.worktree=../wt checkout src/a.go",
	}
	t.Run("dirty", func(t *testing.T) {
		l82WantAsk(t, l82DirtyTask(t), cmds...)
	})
	t.Run("clean main checkout", func(t *testing.T) {
		h := buildingTask(t)
		l82WantAsk(t, h, cmds...)
		l82WantFree(t, h, "git checkout src/a.go")
	})
}

// C30
func TestL82FindingLabelsReadInAnyShape(t *testing.T) {
	mustFix := map[string]string{
		"subheading": "# Code review 1\n## Findings\n### Correctness\n- [90] must-fix: A returns 2 (src/a.go:3 `func A() int`). Why: x.\n",
		"numbered":   "# Code review 1\n## Findings\n1. [90] must-fix: A returns 2 (src/a.go:3 `func A() int`). Why: x.\n",
		"confidence": "# Code review 1\n## Findings\n- [confidence 90] must-fix: A returns 2 (src/a.go:3 `func A() int`). Why: x.\n",
		"colon":      "# Code review 1\n## Findings\n- [90]: must-fix: A returns 2 (src/a.go:3 `func A() int`). Why: x.\n",
	}
	for name, text := range mustFix {
		t.Run("must-fix "+name, func(t *testing.T) {
			h := provenTask(t)
			id, _ := h.startHelper(HelperCodeReviewer)
			write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, text))
			o := h.stopHelper(id, HelperCodeReviewer)
			if o["decision"] != "block" || !strings.Contains(fmtOut(o), "Fails when") {
				t.Fatalf("a must-fix in shape %q without Fails when should be sent back: %v", name, o)
			}
		})
	}

	shouldFix := map[string]string{
		"subheading": "## Findings\n### Style\n- [60] should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n",
		"numbered":   "## Findings\n1. [60] should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n",
		"confidence": "## Findings\n- [confidence 50] should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n",
		"colon":      "## Findings\n- [50]: should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n",
	}
	for name, text := range shouldFix {
		t.Run("should-fix "+name, func(t *testing.T) {
			h := provenTask(t)
			h.runHelper(HelperCodeReviewer, "# Code review 1\n"+text)
			h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code-review-1: one should-fix\n## For you\n- nothing\n")
			if m := h.finishMissing(); !strings.Contains(m, "Should-fix not fixed:") {
				t.Fatalf("a should-fix in shape %q must be read: %s", name, m)
			}
		})
	}
}

// C31
func TestL82ShouldFixFromNewestCountingRun(t *testing.T) {
	report := "# Report\n" + reportEnd + "## Review\n- code reviewer: two runs\n## For you\n- nothing\n"
	failing := func(h *harness, extra string) {
		h.t.Helper()
		h.selfReview()
		id, _ := h.startHelper(HelperCodeReviewer)
		write(h.t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 2\n## Findings\n"+extra+"- [70] note on A (src/a.go:3 `nothing like this line`)\n"))
		h.stopHelper(id, HelperCodeReviewer)
		h.l82ForcedStop(id, HelperCodeReviewer)
		if r := h.task().runByAgent(id); r == nil || h.task().counts(*r) {
			h.t.Fatalf("setup: the second run should not count: %+v", r)
		}
	}
	t.Run("counted run has it, later failed run doesn't", func(t *testing.T) {
		h := provenTask(t)
		h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- [60] should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n")
		failing(h, "")
		h.docs(DocReport, report)
		if m := h.finishMissing(); !strings.Contains(m, "Should-fix not fixed:") {
			t.Fatalf("the newest counting run's should-fix still needs the line: %s", m)
		}
	})
	t.Run("later failed run has it, counted run doesn't", func(t *testing.T) {
		h := provenTask(t)
		h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n")
		failing(h, "- [60] should-fix: A uses a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n")
		h.docs(DocReport, report)
		if m := h.finishMissing(); strings.Contains(m, "Should-fix not fixed:") {
			t.Fatalf("a should-fix only in a run that didn't count needs no line: %s", m)
		}
	})
}

// C32
func TestL82RestoreStagedShapes(t *testing.T) {
	h := l82DirtyTask(t)
	l82WantFree(t, h, "git restore -S src/a.go", "git restore --staged -- src/a.go")
	l82WantAsk(t, h, "git restore -SW src/a.go", "git restore -WS src/a.go")
}

// C33
func TestL82QuietSwitchIsPlain(t *testing.T) {
	h := l82DirtyTask(t)
	l82WantFree(t, h, "git switch -q other", "git switch --quiet other")
	l82WantAsk(t, h, "git switch -qf other", "git switch -fq other", "git switch -q -f other")
}

// C34
func TestL82SwitchGlobIsNotPlain(t *testing.T) {
	h := l82DirtyTask(t)
	l82WantAsk(t, h, "git switch *", "git switch other*", "git switch oth?r")
}
