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

// helperIn is a tool call made inside a helper.
func (h *harness) helperIn(id, kind, tool string, input any) *HookInput {
	in := h.in(tool, input, nil)
	in.AgentID = id
	in.AgentType = "plumbline:" + kind
	return in
}

// startHelper: the lead starts the helper, then Claude Code fires SubagentStart.
func (h *harness) startHelper(kind string) (string, Output) {
	h.t.Helper()
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + kind, "prompt": "go"}), "", "")
	id := fmt.Sprintf("%s-%d", kind, len(h.task().Helpers)+1)
	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + kind})
	return id, o
}

func (h *harness) findingsPath(id string) string {
	t := h.task()
	r := t.runByAgent(id)
	if r == nil {
		h.t.Fatalf("no run recorded for %s", id)
	}
	return filepath.Join(TaskDocsDir(h.repo, t.ID), r.Findings)
}

func (h *harness) stopHelper(id, kind string) Output {
	return SubagentStop(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + kind})
}

// runHelper is a whole helper run that writes its findings and stops.
func (h *harness) runHelper(kind, findings string) string {
	h.t.Helper()
	id, _ := h.startHelper(kind)
	p := h.findingsPath(id)
	h.expect(PreToolUse(h.helperIn(id, kind, "Write", map[string]any{"file_path": p})), "allow", "")
	write(h.t, p, findings)
	if o := h.stopHelper(id, kind); o != nil {
		h.t.Fatalf("helper stop blocked: %v", o)
	}
	return id
}

const goodPlan = "# Plan\n## Files\n- src/a.go\n## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n"

// groundedTask: a normal task with lookups done, the file read and the
// understanding written, ready for the plan.
func groundedTask(t *testing.T, size string) *harness {
	h := newHarness(t)
	h.setup()
	h.startTask(size)
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n")
	return h
}

func checkpointQ() map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": "Go?", "header": "Checkpoint",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}}}}}
}

func (h *harness) approve() {
	h.t.Helper()
	q := checkpointQ()
	h.expect(h.pre("AskUserQuestion", q), "", "")
	h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Approve"}})
	if h.task().Phase != PhaseBuilding {
		h.t.Fatal("checkpoint approval not recorded")
	}
}

func TestChallengerBeforeCheckpoint(t *testing.T) {
	h := groundedTask(t, "normal")
	a := filepath.Join(h.repo, "src", "a.go")

	// The challenger attacks both documents, so it needs both.
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:challenger"}), "deny", "plan.md")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "challenger"}), "deny", "plan.md") // bare name too
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer"}), "deny", "challenger")
	h.docs(DocPlan, goodPlan)

	// No challenger yet: the checkpoint is refused.
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "plumbline:challenger")

	id, o := h.startHelper(HelperChallenger)
	if !strings.Contains(fmtOut(o), "challenge-1.md") || !strings.Contains(fmtOut(o), "round 1 of at most 2") {
		t.Fatalf("start context should name the findings file and the round: %v", o)
	}
	// While it runs, the lead's stop isn't blocked and the checkpoint waits.
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "still running")

	// The helper is read-only.
	docs := TaskDocsDir(h.repo, h.task().ID)
	hi := func(tool string, input any) Output { return PreToolUse(h.helperIn(id, HelperChallenger, tool, input)) }
	h.expect(hi("Edit", map[string]any{"file_path": a}), "deny", "read-only")
	h.expect(hi("Write", map[string]any{"file_path": a}), "deny", "read-only")
	h.expect(hi("Write", map[string]any{"file_path": filepath.Join(docs, DocPlan)}), "deny", "read-only")
	h.expect(hi("Write", map[string]any{"file_path": filepath.Join(docs, "challenge-2.md")}), "deny", "challenge-1.md")
	h.expect(hi("Write", map[string]any{"file_path": filepath.Join(docs, "challenge-1.md")}), "allow", "")
	h.expect(hi("NotebookEdit", map[string]any{"notebook_path": a}), "deny", "read-only")
	h.expect(hi("Bash", map[string]any{"command": "rm src/a.go"}), "deny", "only runs commands that read")
	h.expect(hi("Bash", map[string]any{"command": "git stash"}), "deny", "")
	h.expect(hi("Bash", map[string]any{"command": "./test.sh"}), "deny", "") // saved commands are the lead's
	h.expect(hi("Bash", map[string]any{"command": "plumbline task replan"}), "deny", "")
	h.expect(hi("Bash", map[string]any{"command": "cat src/a.go | grep A && git log -3 && plumbline docs-dir"}), "allow", "")
	h.expect(hi("Bash", map[string]any{"command": "cat .env"}), "ask", "") // safety rules still apply
	h.expect(hi("AskUserQuestion", checkpointQ()), "deny", "without the user")
	h.expect(hi("Agent", map[string]any{"subagent_type": "Explore"}), "deny", "can't start other helpers")
	if o := hi("Read", map[string]any{"file_path": a}); o != nil {
		t.Fatalf("helper read blocked: %v", o)
	}
	// The lead can't write or forge the helper's findings.
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(docs, "challenge-1.md")}), "deny", "findings")

	// Stopping without findings: sent back once to write them.
	o = h.stopHelper(id, HelperChallenger)
	if o["decision"] != "block" || !strings.Contains(fmtOut(o), "challenge-1.md") {
		t.Fatalf("helper without findings should be sent back: %v", o)
	}
	write(t, h.findingsPath(id), "# Challenge 1\n- [85] the cause is in B, not A (src/b.go:3)\n")
	if o := h.stopHelper(id, HelperChallenger); o != nil {
		t.Fatalf("stop with findings should pass: %v", o)
	}
	if r := h.task().runByAgent(id); r == nil || !r.Complete() {
		t.Fatalf("finished run not recorded: %+v", r)
	}

	// Findings alone aren't enough: the plan answers them.
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "## Challenge note")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- Questioned: cause in B. Checked src/b.go:3, B is not called here; kept the plan.\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "src/b.go:3") // a quoted line still needs the file opened
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}, nil)

	// A second round is allowed, a third is not.
	h.runHelper(HelperChallenger, "# Challenge 2\nnothing at 80 or more\n")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:challenger"}), "deny", "ran 2 times")
	h.approve()
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:challenger"}), "deny", "already approved")
	// Other agents are not gated as helpers.
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "Explore"}), "", "")
}

func TestChallengerSkipped(t *testing.T) {
	// Small tasks skip the challenger.
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()

	// So does a project that switched it off.
	h2 := groundedTask(t, "big")
	s, _, _ := LoadSetup(h2.repo)
	s.Parts = map[string]bool{"challenger": false}
	_, err := SaveSetup(h2.repo, s)
	must(t, err)
	h2.docs(DocPlan, goodPlan)
	h2.approve()
}

func TestChallengerRunWhileCodeChanges(t *testing.T) {
	// A run where the code changed under it doesn't count.
	h := groundedTask(t, "normal")
	h.docs(DocPlan, goodPlan+"## Challenge note\n- nothing changed\n")
	id, _ := h.startHelper(HelperChallenger)
	write(t, h.findingsPath(id), "x\n")
	write(t, filepath.Join(h.repo, "src", "new.go"), "package src\n")
	h.stopHelper(id, HelperChallenger)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "plumbline:challenger")
}

// provenTask: an approved, built and proven task, waiting for its review.
func provenTask(t *testing.T) *harness {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	return h
}

func (h *harness) finishMissing() string {
	s, _, _ := LoadSetup(h.repo)
	return strings.Join(FinishMissing(h.task(), s), "; ")
}

func TestReviewerAfterBuilding(t *testing.T) {
	h := provenTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	report := "# Report\n## Review\n- review-1: nothing at 80 or more\n"

	h.docs(DocReport, "# Report\nDone.\n")
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:code-reviewer") || strings.Contains(m, "plan-reviewer") || !strings.Contains(m, `"## Review"`) {
		t.Fatalf("finish should need a review and a Review section: %s", m)
	}
	o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo})
	if !strings.Contains(fmtOut(o), "plumbline:code-reviewer") {
		t.Fatalf("stop should send back for the review: %v", o)
	}

	id, so := h.startHelper(HelperCodeReviewer)
	if !strings.Contains(fmtOut(so), "git diff HEAD") || !strings.Contains(fmtOut(so), "code-review-1.md") || !strings.Contains(fmtOut(so), "review-checklist.md") {
		t.Fatalf("reviewer context: %v", so)
	}
	// Waiting on the background reviewer is not "done".
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo}); o != nil {
		t.Fatalf("stop while the reviewer runs should pass: %v", o)
	}
	if m := h.finishMissing(); !strings.Contains(m, "still running") {
		t.Fatalf("finish while running: %s", m)
	}
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Edit", map[string]any{"file_path": a})), "deny", "read-only")
	write(t, h.findingsPath(id), "# Review 1\n- [90] A should return 3 (src/a.go:3)\n")
	h.stopHelper(id, HelperCodeReviewer)

	// The report is older than the review: update it.
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("reviewed task should be done: %s", m)
	}
	past := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocReport), past, past))
	if m := h.finishMissing(); !strings.Contains(m, "after report.md was written") {
		t.Fatalf("report older than the review: %s", m)
	}
	h.docs(DocReport, report)

	// A fix after the review makes it stale: review again.
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.docs(DocReport, report)
	if m := h.finishMissing(); !strings.Contains(m, "run these again to check the fixes: plumbline:code-reviewer") {
		t.Fatalf("stale review should need another: %s", m)
	}
	h.runHelper(HelperCodeReviewer, "# Review 2\nfixed\n")
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("re-reviewed task should be done: %s", m)
	}

	// Three reviews at most; after that, the user decides.
	h.runHelper(HelperCodeReviewer, "# Review 3\nfine\n")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer"}), "deny", "bring what's left to the user")
	write(t, a, "package src\n\nfunc A() int { return 4 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	if m := h.finishMissing(); !strings.Contains(m, "ask the user") {
		t.Fatalf("after 3 reviews a stale review goes to the user: %s", m)
	}
}

func TestReviewerSwitchedOff(t *testing.T) {
	h := provenTask(t)
	s, _, _ := LoadSetup(h.repo)
	s.Parts = map[string]bool{"reviewer": false}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard) // the setup file is code too
	must(t, err)
	h.docs(DocReport, "# Report\nDone.\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("with the reviewer off, no review is needed: %s", m)
	}
}

func TestDeadHelperDoesNotHoldTheStop(t *testing.T) {
	h := provenTask(t)
	h.startHelper(HelperCodeReviewer)
	real := Now
	defer func() { Now = real }()
	Now = func() time.Time { return real().Add(helperAlive + time.Minute) }
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo}); o == nil {
		t.Fatal("a helper with no stop for 30 minutes shouldn't keep letting the lead stop")
	}
	if m := h.finishMissing(); !strings.Contains(m, "never finished") {
		t.Fatalf("dead run: %s", m)
	}
}

func TestHelperFallsBackToBranchTask(t *testing.T) {
	// If a helper's hook input carries its own session id, the open task on
	// the branch is used, so its findings file can still be written.
	h := groundedTask(t, "normal")
	h.docs(DocPlan, goodPlan)
	id, _ := h.startHelper(HelperChallenger)
	in := h.helperIn(id, HelperChallenger, "Write", map[string]any{"file_path": h.findingsPath(id)})
	in.SessionID = "helper-own-session"
	h.expect(PreToolUse(in), "allow", "")
	// Other helpers' events are ignored.
	if o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "x", AgentType: "reviewer"}); o != nil {
		t.Fatalf("a non-Plumbline agent named reviewer was recorded: %v", o)
	}
}
