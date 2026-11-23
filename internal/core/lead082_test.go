package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitTargetEnvInOtherShapesAsks(t *testing.T) {
	h := buildingTask(t)
	l82WantAsk(t, h,
		"export GIT_WORK_TREE=../wt; git checkout src/a.go",
		"env GIT_DIR=../x/.git git checkout src/a.go",
		"git_work_tree=../wt git checkout src/a.go",
		"git -c CORE.WORKTREE=../wt checkout src/a.go",
		"git -ccore.worktree=../wt checkout src/a.go",
	)
}

func TestNewBranchAllowListKeepsOtherFlagsChecked(t *testing.T) {
	h := l82DirtyTask(t)
	l82WantFree(t, h, "git checkout -b fresh", "git switch -c fresh", "git switch -c fresh -q", "git checkout -b fresh other", "git switch --create fresh --track other")
	l82WantAsk(t, h, "git checkout -b fresh -m other", "git switch -c fresh --merge other", "git switch -C fresh other", "git checkout -b fresh --force", "git switch -c fresh -fq", "git checkout -bf fresh", "git switch -c fresh x y z", "git switch -c 'fr'* other")
}

func TestGuardRoundsUsedWithANewListNeedsANote(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, l82GuardPlan())
	h.l82Run(l82KindGuard, "# Guard review 1\n"+l82NoneFindings)
	h.docs(DocPlan, l82PlanBase+l82Guard2+l82Note)
	h.l82Run(l82KindGuard, "# Guard review 2\n"+l82NoneFindings)
	three := "## Guards loosened\n- the dirty-tree ask (src/a.go:3 `func A() int`): reached by plain switch, switch -f, switch -q, switch -m\n"
	h.docs(DocPlan, l82PlanBase+three+l82Note)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "not reviewed")
	h.docs(DocPlan, l82PlanBase+three+"## Challenge note\n- switch -m: not reviewed, added after the guard reviewer's last round\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "not reviewed")
	h.docs(DocPlan, l82PlanBase+three+"## Challenge note\n- the dirty-tree ask (src/a.go:3) with switch -m: not reviewed, added after the guard reviewer's last round\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "", "")
}

func TestArbiterFindingsMustSayWhatIsOpen(t *testing.T) {
	for name, text := range map[string]string{
		"no findings section": "# Arbiter 1\nAll fixed, looks good.\n",
		"empty findings":      "# Arbiter 1\n## Findings\n## Checked and fine\n- x\n",
		"unlabelled open":     "# Arbiter 1\n## Findings\n- [90] A still returns the wrong value (src/a.go:3 `func A() int`)\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := l82Capped(t)
			h.selfReview()
			id, _ := h.startHelper(l82KindArbiter)
			write(t, h.findingsPath(id), text)
			if o := h.stopHelper(id, l82KindArbiter); o["decision"] != "block" {
				t.Fatalf("an arbiter file that doesn't say what is open should be sent back: %v", o)
			}
			h.l82ForcedStop(id, l82KindArbiter)
			h.docs(DocReport, l82ClearedReport)
			if m := h.finishMissing(); m == "" {
				t.Fatal("an arbiter file that doesn't say what is open must not clear")
			}
		})
	}
}

func TestArbiterBlockedByGitLeavesTheReviewCapQuestionOpen(t *testing.T) {
	h := l82Capped(t)
	tk := h.task()
	for i := len(tk.Helpers) - 1; i >= 0; i-- {
		if tk.Helpers[i].Kind == HelperCodeReviewer {
			tk.Helpers[i].PrintBefore, tk.Helpers[i].PrintAfter = strings.Repeat("2", 40), strings.Repeat("2", 40)
			break
		}
	}
	must(t, SaveTask(tk))
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "Review cap")
	if m := h.finishMissing(); !strings.Contains(m, `header "Review cap"`) || strings.Contains(m, "start plumbline:arbiter") {
		t.Fatalf("with the arbiter blocked by git, the finish check asks the user: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "", "")
}

func TestGuardListShapes(t *testing.T) {
	cases := []struct {
		name, sec string
		guards    int
		bad       bool
	}{
		{"numbered, second has no file:line", "## Guards loosened\n1. the ask (src/a.go:3 `func A() int`): reached by switch\n2. the other ask: reached by restore\n", 2, true},
		{"none-worded bullet that cites code is a guard", "## Guards loosened\n- None of the asks go, but git switch -q now runs unasked (src/a.go:3 `func A() int`)\n", 1, false},
		{"indented shapes under one guard", "## Guards loosened\n- the ask (src/a.go:3 `func A() int`):\n  - reached by git switch -f\n  - reached by git checkout -b n -f\n", 1, false},
		{"plain none", "## Guards loosened\n- none\n", 0, false},
		{"none with words after it is not none", "## Guards loosened\n- none (this plan drops the dirty-tree ask for git switch -q)\n", 1, true},
		{"a second section is read too", "## Guards loosened\n- none\n## Notes\n- x\n## Guards loosened\n- the ask (src/a.go:3 `func A() int`): reached by switch\n", 1, true},
		{"none with a full stop", "## Guards loosened\n- None.\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pc := CheckPlan(l82PlanBase + c.sec)
			bad := false
			for _, p := range pc.Problems {
				bad = bad || strings.Contains(p, "Guards loosened")
			}
			if len(pc.Guards) != c.guards || bad != c.bad {
				t.Fatalf("guards %q, problems %v: want %d guards, refused=%v", pc.Guards, pc.Problems, c.guards, c.bad)
			}
		})
	}
}

func TestGuardReviewNotNeededWithReviewsOff(t *testing.T) {
	for _, part := range []string{"reviewer", "security-reviewer", "guard-reviewer"} {
		t.Run(part, func(t *testing.T) { guardOffCase(t, part) })
	}
}

func guardOffCase(t *testing.T, part string) {
	h := newHarness(t)
	h.setup()
	s, _, _ := LoadSetup(h.repo)
	s.Parts = map[string]bool{part: false}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: part off")
	h.startTask("small")
	h.ground()
	h.docs(DocPlan, l82GuardPlan())
	if d, r := h.l82Checkpoint(); d == "deny" && strings.Contains(r, "guard-reviewer") {
		t.Fatalf("with reviews off, no guard review is required: %s", r)
	}
	h.expect(h.pre("Agent", l82Agent(l82KindGuard, "go")), "deny", "switched off")
}

func TestDebugCaptureKeepsTheRawInput(t *testing.T) {
	h := groundedTask(t, "small")
	t.Setenv("PLUMBLINE_DEBUG", "1")
	raw := `{"session_id":"` + h.sid + `","cwd":"` + h.repo + `","hook_event_name":"PostToolUse","tool_name":"AskUserQuestion","tool_input":{"questions":[]},"tool_response":{"answers":{}},"new_field_from_claude_code":{"x":1}}`
	in, err := ReadHookInput(strings.NewReader(raw))
	must(t, err)
	PostToolUse(in)
	b, err := os.ReadFile(filepath.Join(DataDir(), "debug", "askuser-post.json"))
	must(t, err)
	if string(b) != raw {
		t.Fatalf("the capture should be the bytes Claude Code sent:\n%s", b)
	}
}

func TestGitConfigEnvAsks(t *testing.T) {
	h := buildingTask(t)
	l82WantAsk(t, h,
		"GIT_CONFIG_PARAMETERS=\"'core.worktree'='../wt'\" git checkout src/a.go",
		"GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.worktree GIT_CONFIG_VALUE_0=../wt git checkout src/a.go",
	)
}

func (h *harness) leadFinishRun(id, kind, text string) Output {
	write(h.t, h.findingsPath(id), h.complete(kind, text))
	return h.stopHelper(id, kind)
}

func TestArbiterDoesNotClearAReviewerWithNoCountedReview(t *testing.T) {
	h := provenTask(t)
	for i := 1; i <= MaxReviews; i++ {
		id, _ := h.startHelper(HelperCodeReviewer)
		h.editA(fmt.Sprint(10 + i))
		if o := h.leadFinishRun(id, HelperCodeReviewer, "# Review\nnothing at 80 or more\n"); o != nil {
			h.l82ForcedStop(id, HelperCodeReviewer)
		}
	}
	h.editA("7")
	h.selfReview()
	h.expect(h.pre("Agent", l82Agent(l82KindArbiter, "go")), "deny", "no review that counted")
	if m := h.finishMissing(); !strings.Contains(m, `header "Review cap"`) {
		t.Fatalf("a reviewer with no counted review goes to the user: %s", m)
	}
}

func TestArbiterClearsOnlyTheReviewersItWasShown(t *testing.T) {
	h := provenTask(t)
	tk := h.task()
	tk.Reviewers = []string{HelperCodeReviewer, HelperSecurity}
	must(t, SaveTask(tk))
	for i := 1; i <= MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	for i := 1; i < MaxReviews; i++ {
		h.runHelper(HelperSecurity, "# Review\nnothing at 80 or more\n")
	}
	sid, _ := h.startHelper(HelperSecurity)
	h.editA("7")
	h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	if o := h.leadFinishRun(sid, HelperSecurity, "# Review\n## Findings\n- [90] must-fix: A is wrong (src/a.go:3 `func A() int`). Fails when: A() -> wrong.\n"); o != nil {
		h.l82ForcedStop(sid, HelperSecurity)
	}
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("a reviewer capped after the arbiter started isn't cleared by it: %q", m)
	}
}

func TestArbiterNoneWordedOpenFindingIsSentBack(t *testing.T) {
	h := l82Capped(t)
	h.selfReview()
	id, _ := h.startHelper(l82KindArbiter)
	write(t, h.findingsPath(id), "# Arbiter 1\n## Findings\n- None of the code reviewer's must-fix findings is fixed: A still returns the old number (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n")
	if o := h.stopHelper(id, l82KindArbiter); o["decision"] != "block" {
		t.Fatalf("a none-worded open finding should be sent back: %v", o)
	}
	h.l82ForcedStop(id, l82KindArbiter)
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m == "" {
		t.Fatal("a none-worded open finding must not clear")
	}
}

func TestArbiterFencedHeadingDoesNotHideAMustFix(t *testing.T) {
	h := l82Capped(t)
	h.l82Arbiter("# Arbiter 1\n## Findings\n- [90] must-fix: A still returns the old number (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n```\n# a comment in a fence\n```\n")
	if m := h.finishMissing(); !strings.Contains(m, "must-fix still open") {
		t.Fatalf("the must-fix is open: %s", m)
	}
	h.editA("8")
	h.selfReview()
	id, _ := h.startHelper(l82KindArbiter)
	write(t, h.findingsPath(id), "# Arbiter 2\n## Findings\n- none\n```\n# x\n```\n- [90] must-fix: A still returns the old number (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n")
	if o := h.stopHelper(id, l82KindArbiter); o["decision"] != "block" {
		t.Fatalf("a must-fix after a fenced heading is still read under Findings, so the file is sent back: %v", o)
	}
}

func TestArbiterNoteNamesANewerRunThatDidNotCount(t *testing.T) {
	h := provenTask(t)
	for i := 1; i < MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "# Review\nnothing at 80 or more\n")
	}
	id, _ := h.startHelper(HelperCodeReviewer)
	h.editA("7")
	h.leadFinishRun(id, HelperCodeReviewer, "# Review\n## Findings\n- [90] must-fix: A wrong (src/a.go:3 `func A() int`). Fails when: A() -> wrong.\n")
	h.selfReview()
	_, o := h.startHelper(l82KindArbiter)
	if s := contextOf(o); !strings.Contains(s, "code-review-3.md") {
		t.Fatalf("the arbiter's note should name the newer run's findings: %s", s)
	}
}

func TestLeadCantWriteAFindingsFileInOtherLetterCase(t *testing.T) {
	h := l82Capped(t)
	dir := TaskDocsDir(h.repo, h.task().ID)
	for _, name := range []string{"Arbiter-1.md", "ARBITER-1.md", "Guard-Review-1.md", "Code-Review-9.md"} {
		h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(dir, name), "content": "## Findings\n- none\n"}), "deny", "")
	}
}

func TestAnyGitEnvBeforeCheckoutAsks(t *testing.T) {
	h := buildingTask(t)
	l82WantAsk(t, h, "GIT_CONFIG_GLOBAL=/tmp/g git checkout src/a.go", "GIT_NAMESPACE=x git checkout src/a.go")
}

func TestArbiterRefusalNamesWhatItMayRead(t *testing.T) {
	h := l82Capped(t)
	h.docs(DocReport, "# Report\n")
	h.selfReview()
	id, _ := h.startHelper(l82KindArbiter)
	_, r := decision(PreToolUse(h.helperIn(id, l82KindArbiter, "Read", map[string]any{"file_path": filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocReport)})))
	if !strings.Contains(r, "reviewers' findings and disputes") {
		t.Fatalf("the arbiter's refusal should say it may read the reviewers' findings: %s", r)
	}
}

func TestArbiterOpenMustFixAnywhereOrWordedAsNoneDoesNotClear(t *testing.T) {
	for name, text := range map[string]string{
		"none with an exception":      "# Arbiter 1\n## Findings\n- None, except the must-fix on A (src/a.go:3 `func A() int`) is still open. Fails when: A() is called -> it returns the old number.\n",
		"none colon":                  "# Arbiter 1\n## Findings\n- none: the must-fix on A is still open\n",
		"must-fix in another section": "# Arbiter 1\n## Findings\n- none\n## Still open\n- [90] must-fix: A still returns the old number (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := l82Capped(t)
			h.selfReview()
			id, _ := h.startHelper(l82KindArbiter)
			write(t, h.findingsPath(id), text)
			if o := h.stopHelper(id, l82KindArbiter); o != nil {
				h.l82ForcedStop(id, l82KindArbiter)
			}
			h.docs(DocReport, l82ClearedReport)
			if m := h.finishMissing(); m == "" {
				t.Fatal("an arbiter that says a must-fix is open must not clear")
			}
		})
	}
}

func TestChallengerRestartedByMessageIsRegated(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, l82NonePlan())
	id := h.l82Run(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "", "")
	id2 := h.l82Run(HelperChallenger, "# Challenge 2\nnothing at 80 or more\n")
	h.expect(h.pre("SendMessage", map[string]any{"to": id2, "message": "Look again."}), "deny", "already ran")
	h.approve()
	h.expect(h.pre("SendMessage", map[string]any{"to": id, "message": "Look again."}), "deny", "already approved")
}

func TestRetargetedCheckoutSaysWhyItAsks(t *testing.T) {
	h := buildingTask(t)
	_, r := decision(h.bash("GIT_PAGER=cat git checkout src/a.go"))
	if !strings.Contains(r, "can't tell which checkout") || strings.Contains(r, "there are uncommitted changes") {
		t.Fatalf("a clean tree with a git variable set: the ask should say Plumbline couldn't check, not that the tree is dirty: %s", r)
	}
}

func TestArbiterMustFixAfterAnUnclosedFenceStillCounts(t *testing.T) {
	h := l82Capped(t)
	h.l82Arbiter("# Arbiter 1\n## Findings\n- none\n## Checked and fine\n  ```go\n  x := 1\n## Still open\n- [90] must-fix: A still returns the old number (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the old number.\n")
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m == "" {
		t.Fatal("a must-fix after an unclosed fence is still open and must not clear")
	}
}
