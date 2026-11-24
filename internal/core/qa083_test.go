package core

// Blind QA for release 0.8.3, written from the plan's case table (C1..C36),
// not from the new code. Fields 0.8.3 adds (Task.BaseTree,
// Task.CommitWhenGreen, Task.Rules083) are reached by reflection, so this
// file compiles before and after the change, and a missing field fails the
// one test that needs it with a clear message.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- reflection helpers for the fields 0.8.3 adds ---

func q83Field(t *testing.T, v any, name string) reflect.Value {
	t.Helper()
	f := reflect.ValueOf(v).Elem().FieldByName(name)
	if !f.IsValid() {
		t.Fatalf("%T has no field %s (0.8.3 adds it)", v, name)
	}
	return f
}

func q83Str(t *testing.T, v any, name string) string {
	t.Helper()
	return q83Field(t, v, name).String()
}

func q83Bool(t *testing.T, v any, name string) bool {
	t.Helper()
	return q83Field(t, v, name).Bool()
}

func q83Set(t *testing.T, v any, name string, val any) {
	t.Helper()
	q83Field(t, v, name).Set(reflect.ValueOf(val))
}

// every helper kind Plumbline has (helpers.go: the Helper* constants)
// q83AllKinds: every helper the plugin ships, from agents/*.md, so a new
// agent missing from the matchers fails C1 (review H5).
func q83AllKinds(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "agents", "*.md"))
	must(t, err)
	var out []string
	for _, f := range files {
		out = append(out, strings.TrimSuffix(filepath.Base(f), ".md"))
	}
	if len(out) < 12 {
		t.Fatalf("expected the plugin's agents, found %v", out)
	}
	return out
}

// C1
func TestQA083_C1_HooksMatchEveryHelperKind(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "hooks", "hooks.json"))
	must(t, err)
	var doc struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("hooks.json isn't JSON: %v", err)
	}
	for _, event := range []string{"SubagentStart", "SubagentStop"} {
		entries := doc.Hooks[event]
		if len(entries) == 0 {
			t.Fatalf("hooks.json has no %s hook", event)
		}
		matches := func(name string) bool {
			for _, e := range entries {
				re, err := regexp.Compile(e.Matcher)
				if err != nil {
					t.Fatalf("%s matcher %q doesn't compile: %v", event, e.Matcher, err)
				}
				if re.MatchString(name) {
					return true
				}
			}
			return false
		}
		for _, k := range q83AllKinds(t) {
			if !matches("plumbline:" + k) {
				t.Errorf("%s matcher doesn't match plumbline:%s, so its runs are never recorded", event, k)
			}
		}
		for _, other := range []string{"Explore", "plumbline:explorer", "other:code-reviewer", "plumbline:arbiter-x"} {
			if matches(other) {
				t.Errorf("%s matcher matches %q, which isn't a Plumbline helper", event, other)
			}
		}
	}
}

// C2
func TestQA083_C2_GuardStartRefusedWhenGitCantFingerprint(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, l82GuardPlan())
	locked := filepath.Join(h.repo, "src", "locked.go")
	write(t, locked, "package src\n")
	must(t, os.Chmod(locked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(locked, 0o644) })
	if _, err := Fingerprint(h.repo, nil); err == nil {
		t.Fatal("fixture: an unreadable file should make git add -A (Fingerprint) fail; running as root?")
	}
	d, r := decision(h.pre("Agent", l82Agent(l82KindGuard, "go")))
	if d != "deny" || !strings.Contains(strings.ToLower(r), "git") {
		t.Fatalf("guard reviewer start without a fingerprint: %q (%s), want a deny naming git", d, r)
	}
	for _, s := range h.task().ReviewerStart {
		if s.Kind == HelperGuard {
			t.Fatalf("a refused guard start must record no start pass: %+v", s)
		}
	}
}

// q83Disputed: a code reviewer that used its 3 runs, the code unchanged
// since its last review, and the lead's dispute of its must-fix written
// after it (the same fixture as TestL82DisputesCappedNoChangeSkipsArbiter).
func q83Disputed(t *testing.T) *harness {
	t.Helper()
	h := reviewedTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 2\nnothing new\n")
	h.runHelper(HelperCodeReviewer, "# Code review 3\nnothing new\n")
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	return h
}

// C3
func TestQA083_C3_CappedDisputeAsksForArbiterNotReviewCap(t *testing.T) {
	h := q83Disputed(t)
	m := h.finishMissing()
	if !strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("a capped reviewer with an open dispute should send Claude to the arbiter: %s", m)
	}
	if strings.Contains(m, `"Review cap"`) {
		t.Fatalf("the Review cap question comes only after the arbiter: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "deny", "arbiter")
	h.selfReview()
	_, o := h.startHelper(HelperArbiter)
	disputes := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DisputesName(HelperCodeReviewer))
	if !strings.Contains(fmtOut(o), disputes) {
		t.Fatalf("the arbiter's start note should name the disputes file %s: %s", disputes, fmtOut(o))
	}
}

// C4
func TestQA083_C4_ArbiterWithdrawsDisputedFinding(t *testing.T) {
	h := q83Disputed(t)
	h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	if m := h.finishMissing(); strings.Contains(m, "runs are used") || strings.Contains(m, `"Review cap"`) {
		t.Fatalf("a dispute the arbiter withdrew no longer blocks: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "deny", "")
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code reviewer: three rounds\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "cleared by the arbiter") {
		t.Fatalf("the report must say the dispute was cleared by the arbiter: %s", m)
	}
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("arbiter cleared the dispute and the report says so: %s", m)
	}
}

// C5
func TestQA083_C5_ArbiterUpholdsDisputedFinding(t *testing.T) {
	h := q83Disputed(t)
	h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix)
	m := h.finishMissing()
	if !strings.Contains(m, `"Review cap"`) || !strings.Contains(m, "arbiter-1.md") {
		t.Fatalf("an upheld must-fix leads to the Review cap question naming arbiter-1.md: %s", m)
	}
	h.expect(h.pre("AskUserQuestion", reviewCapQ()), "", "")
}

// C6
func TestQA083_C6_ArbiterClearOnOldCodeDoesNotHold(t *testing.T) {
	h := q83Disputed(t)
	h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	h.editA("8")
	h.docs(DocReport, l82ClearedReport)
	m := h.finishMissing()
	if m == "" || !strings.Contains(m, "plumbline:arbiter") {
		t.Fatalf("an arbiter clear on older code doesn't hold after a change: %q", m)
	}
}

// C30
func TestQA083_C30_DisputeAfterRulingNotCovered(t *testing.T) {
	h := q83Disputed(t)
	h.l82Arbiter("# Arbiter 1\n" + l82NoneFindings)
	h.docs(DocReport, l82ClearedReport)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("fixture: the ruling should clear the first dispute: %s", m)
	}
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n- [85] B is wrong too (src/a.go:3)\n")
	m := h.finishMissing()
	if m == "" || !(strings.Contains(m, "plumbline:arbiter") || strings.Contains(m, `"Review cap"`)) {
		t.Fatalf("a dispute written after the arbiter's ruling isn't covered by it: %q", m)
	}
}

// --- BaseTree and the first-edit gate ---

// C11 (BaseTree set): the first edit needs no baseline runs.
func TestQA083_C11_FirstEditWithBaseTreeNotRefused(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	tk := h.task()
	if bt := q83Str(t, tk, "BaseTree"); bt == "" || bt != tk.ApprovalPrint {
		t.Fatalf("approval should set BaseTree to the approved code's tree %q, got %q", tk.ApprovalPrint, bt)
	}
	// The suite needs no run first; the other saved checks still do (review H4: the copy lacks generated files they read).
	edit := map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}
	if _, r := decision(h.pre("Edit", edit)); strings.Contains(r, "plumbline run test") {
		t.Fatalf("with a BaseTree the first edit needs no test run first: %s", r)
	}
	s, _, _ := LoadSetup(h.repo)
	for _, k := range CheckKinds {
		if savedCommand(s, k) != "" {
			h.runCheck(k)
		}
	}
	if d, r := decision(h.pre("Edit", edit)); d == "deny" {
		t.Fatalf("once the other checks ran, the first edit goes ahead: %s", r)
	}
}

// C11 (no BaseTree): a task approved before 0.8.3 keeps the old refusal.
func TestQA083_C11_FirstEditWithoutBaseTreeRefused(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	tk := h.task()
	q83Set(t, tk, "BaseTree", "")
	must(t, SaveTask(tk))
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "deny", "plumbline run checks")
}

// C11 (hotfix): a hotfix keeps the baseline-first refusal.
func TestQA083_C11_HotfixFirstEditRefused(t *testing.T) {
	h := hotfixTask(t)
	h.docs(DocPlan, hotfixPlan)
	h.approveOnly()
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "deny", "plumbline run checks")
}

// q83Branched: a task on main, approved, with a branch "other" (made before
// the task started) whose code differs.
func q83Branched(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.setup()
	run(t, h.repo, "git", "switch", "-q", "-c", "other")
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 20 }\n")
	run(t, h.repo, "git", "commit", "-q", "-am", "other B")
	run(t, h.repo, "git", "switch", "-q", "main")
	tk := h.startTask("small")
	tk.StartCommit, tk.StartTips = HeadCommit(h.repo), BranchTips(h.repo) // as plumbline task start records them
	must(t, SaveTask(tk))
	h.ground()
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	return h
}

func (h *harness) q83Switch(branch string) {
	h.t.Helper()
	cmd := "git switch " + branch
	if d, r := decision(h.bash(cmd)); d == "deny" {
		h.t.Fatalf("%s refused: %s", cmd, r)
	}
	run(h.t, h.repo, "git", "switch", "-q", branch)
	h.post("Bash", map[string]any{"command": cmd}, map[string]any{"stdout": ""})
}

// C12, C11 (followApproval)
func TestQA083_C12_FollowApprovalMovesOrClearsBaseTree(t *testing.T) {
	t.Run("nothing changed: moves", func(t *testing.T) {
		h := q83Branched(t)
		before := q83Str(t, h.task(), "BaseTree")
		h.q83Switch("other")
		now, err := Fingerprint(h.repo, h.setupNow().IgnoreChanges)
		must(t, err)
		if got := q83Str(t, h.task(), "BaseTree"); got != now || got == before {
			t.Fatalf("a switch with nothing changed moves BaseTree to the new tree %s (was %s), got %q", now, before, got)
		}
	})
	t.Run("task test edits: clears", func(t *testing.T) {
		h := q83Branched(t)
		p := filepath.Join(h.repo, "src", "a_test.go")
		h.mustAllow(h.pre("Write", map[string]any{"file_path": p}), "test file")
		write(t, p, "package src\n")
		h.post("Write", map[string]any{"file_path": p}, nil)
		h.q83Switch("other")
		if got := q83Str(t, h.task(), "BaseTree"); got != "" {
			t.Fatalf("a switch after the task's test edits clears BaseTree, got %q", got)
		}
		// Without a BaseTree no run can be a baseline any more (the task already
		// changed a file), so edits go on as before 0.8.3 and every failure counts.
		h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
	})
}

// --- Approve and commit when green ---

const q83Green = "Approve and commit when green"

func q83CheckpointQ(labels ...string) map[string]any {
	var opts []any
	for _, l := range labels {
		opts = append(opts, map[string]any{"label": l})
	}
	return map[string]any{"questions": []any{map[string]any{"question": "Go?", "header": "Checkpoint", "options": opts}}}
}

func (h *harness) q83Answer(answer string) {
	h.t.Helper()
	q := q83CheckpointQ("Approve", q83Green, "Change something")
	h.expect(h.pre("AskUserQuestion", q), "", "")
	h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": answer}})
}

const q83Report = "# Report\n" + reportEnd + "## Review\n- code-review-1: nothing at 80 or more\n## For you\n- nothing\n"

// q83GreenTask: a small task approved with the given answer, built, proven
// and reviewed, with the report written: the finish check is empty.
func q83GreenTask(t *testing.T, answer string) *harness {
	t.Helper()
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.q83Answer(answer)
	if h.task().Phase != PhaseBuilding {
		t.Fatalf("%q should approve", answer)
	}
	h.baselines()
	h.redRun()
	h.editA("2")
	h.runHelper(HelperCodeReviewer, "# Code review 1\nnothing at 80 or more\n")
	h.docs(DocReport, q83Report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("fixture: the finish check should be empty: %s", m)
	}
	return h
}

// C17
func TestQA083_C17_CommitWhenGreenSkipsCommitAsk(t *testing.T) {
	h := q83GreenTask(t, q83Green)
	if !q83Bool(t, h.task(), "CommitWhenGreen") {
		t.Fatal("the commit-when-green label should set CommitWhenGreen")
	}
	if d, r := decision(h.bash(`git commit -am "fix: A returns 2"`)); d == "ask" || d == "deny" {
		t.Fatalf("with full proof and an empty finish check, the final commit needs no Commit ask: %q (%s)", d, r)
	}
}

// C18
func TestQA083_C18_CommitWhenGreenStillGated(t *testing.T) {
	t.Run("proof missing", func(t *testing.T) {
		h := q83GreenTask(t, q83Green)
		a := filepath.Join(h.repo, "src", "a.go")
		write(t, a, "package src\n\nfunc A() int { return 22 }\n")
		h.post("Edit", map[string]any{"file_path": a}, nil)
		h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "proof")
	})
	t.Run("finish check not empty", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, goodPlan)
		h.q83Answer(q83Green)
		h.baselines()
		h.redRun()
		h.editA("2") // proven, but no review and no report
		if d, r := decision(h.bash(`git commit -am "fix: A returns 2"`)); d != "ask" && d != "deny" {
			t.Fatalf("with the finish check not empty, the commit is still asked or refused: %q (%s)", d, r)
		}
	})
	t.Run("slice commit", func(t *testing.T) {
		h := q83GreenTask(t, q83Green)
		b := filepath.Join(h.repo, "src", "b.go")
		h.mustAllow(h.pre("Write", map[string]any{"file_path": b}), "outside-plan file")
		write(t, b, "package src\n\nfunc B() int { return 3 }\n")
		h.post("Write", map[string]any{"file_path": b}, nil)
		h.related("src/a.go", "src/b.go")
		h.expect(h.bash(`git commit -m "fix: A returns 2" src/a.go`), "ask", "")
	})
}

// C19
func TestQA083_C19_PlainApproveKeepsCommitAsk(t *testing.T) {
	t.Run("plain Approve", func(t *testing.T) {
		h := q83GreenTask(t, "Approve")
		if q83Bool(t, h.task(), "CommitWhenGreen") {
			t.Fatal("plain Approve must not set CommitWhenGreen")
		}
		h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "")
	})
	t.Run("free text approves nothing", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, goodPlan)
		h.q83Answer("approve and commit")
		if tk := h.task(); tk.Phase == PhaseBuilding || q83Bool(t, tk, "CommitWhenGreen") {
			t.Fatalf("free text is no approval: phase %s", tk.Phase)
		}
	})
	t.Run("real payload", func(t *testing.T) {
		h := groundedTask(t, "small")
		h.docs(DocPlan, l82NonePlan())
		q := l82Q("Change A to return 2. Go ahead?", "Checkpoint", "Approve", q83Green, "Change something")
		h.l82AskPre(q)
		h.l82RealPost([]any{q}, map[string]string{"Change A to return 2. Go ahead?": q83Green})
		tk := h.task()
		if tk.Phase != PhaseBuilding || !q83Bool(t, tk, "CommitWhenGreen") {
			t.Fatalf("the label from the real AskUserQuestion payload approves and sets CommitWhenGreen: phase %s", tk.Phase)
		}
	})
}

// C31
func TestQA083_C31_CommitWhenGreenFollowsTheLastApproval(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.q83Answer(q83Green)
	if !q83Bool(t, h.task(), "CommitWhenGreen") {
		t.Fatal("first approval with the label sets it")
	}
	replan := func() {
		tk := h.task()
		tk.Phase = PhasePlanning // plumbline task replan
		must(t, SaveTask(tk))
		h.docs(DocPlan, goodPlan) // a replan rewrites plan.md before asking again
	}
	replan()
	h.q83Answer("Approve")
	if q83Bool(t, h.task(), "CommitWhenGreen") {
		t.Fatal("plain Approve on a replan clears CommitWhenGreen")
	}
	replan()
	h.q83Answer(q83Green)
	if !q83Bool(t, h.task(), "CommitWhenGreen") {
		t.Fatal("re-approval with the label sets it again")
	}

	// checkpoint 1 never sets it
	h1 := groundedTask(t, "normal")
	q := map[string]any{"questions": []any{map[string]any{"question": "Understood?", "header": "Checkpoint 1",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": q83Green}, map[string]any{"label": "Change something"}}}}}
	if d, r := decision(h1.pre("AskUserQuestion", q)); d == "deny" {
		t.Fatalf("checkpoint 1 refused: %s", r)
	}
	h1.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Understood?": q83Green}})
	if q83Bool(t, h1.task(), "CommitWhenGreen") {
		t.Fatal("checkpoint 1 never sets CommitWhenGreen")
	}
}

// --- outside-plan cap ---

func (h *harness) q83Outside(rel string) (string, string) {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	d, r := decision(h.pre("Write", map[string]any{"file_path": p}))
	if d != "deny" {
		write(h.t, p, "package src\n")
		h.post("Write", map[string]any{"file_path": p}, nil)
	}
	return d, r
}

// C20
func TestQA083_C20_TestFilesDontCountTowardOutsidePlanCap(t *testing.T) {
	h := buildingTask(t)
	for _, f := range []string{"src/x1_test.go", "src/x2_test.go", "src/x3_test.go", "src/x4_test.go"} {
		if d, r := h.q83Outside(f); d == "deny" {
			t.Fatalf("test file %s outside the plan hit a refusal: %s", f, r)
		}
	}
	for _, f := range []string{"src/n1.go", "src/n2.go", "src/n3.go"} {
		if d, r := h.q83Outside(f); d == "deny" {
			t.Fatalf("non-test file %s is within the cap of 3: %s", f, r)
		}
	}
	if d, r := h.q83Outside("src/n4.go"); d != "deny" || !strings.Contains(r, "outside the approved plan") {
		t.Fatalf("a 4th non-test file outside the plan still hits the cap: %q (%s)", d, r)
	}
	h2 := buildingTask(t)
	if d, _ := h2.q83Outside("src/auth/session.go"); d != "deny" {
		t.Fatal("a risky file outside the plan is still refused")
	}
	if d, _ := h2.q83Outside(".github/workflows/ci.yml"); d != "deny" {
		t.Fatal("a gate file outside the plan is still refused")
	}
}

// --- reviewers ---

const q83NormalPlan = "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n%FILES%## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n%TEAM%## Challenge note\n- nothing changed\n"

// q83Normal: an approved normal task whose plan adds files and a Team, with
// A changed and the suite run.
func q83Normal(t *testing.T, files []string, team string) *harness {
	t.Helper()
	h := groundedTask(t, "normal")
	extra := ""
	for _, f := range files {
		extra += "- " + f + " (new)\n"
	}
	plan := strings.Replace(q83NormalPlan, "%FILES%", extra, 1)
	if team != "" {
		plan = strings.Replace(plan, "%TEAM%", "## Team\n"+team+"\n", 1)
	} else {
		plan = strings.Replace(plan, "%TEAM%", "", 1)
	}
	h.docs(DocPlan, plan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, plan)
	h.approve()
	h.redRun()
	h.editA("2")
	return h
}

// C21
func TestQA083_C21_NormalTaskNeedsNoPlanReviewer(t *testing.T) {
	h := q83Normal(t, nil, "")
	if got := h.required(); got != HelperCodeReviewer {
		t.Fatalf("a normal 0.8.3 task needs the code reviewer only: %s", got)
	}
	h2 := q83Normal(t, nil, "- lead, challenger, plan-reviewer, code-reviewer")
	if got := h2.required(); !strings.Contains(got, HelperPlanReviewer) {
		t.Fatalf("a Team naming plan-reviewer still requires it: %s", got)
	}
}

// C22
func TestQA083_C22_TestPathWordsBringNoSpecialist(t *testing.T) {
	h := q83Normal(t, []string{"src/payment_auth_test.go"}, "")
	p := filepath.Join(h.repo, "src", "payment_auth_test.go")
	write(t, p, "package src\n")
	h.post("Write", map[string]any{"file_path": p}, nil)
	got := h.required()
	if strings.Contains(got, HelperSecurity) || strings.Contains(got, HelperData) {
		t.Fatalf("a test file's path words bring no specialist: %s", got)
	}
	hs := riskyTask(t, "- lead, challenger, code-reviewer, security-reviewer")
	if got := hs.required(); !strings.Contains(got, HelperSecurity) {
		t.Fatalf("the same word in a source file still brings the security reviewer: %s", got)
	}
}

// C29
func TestQA083_C29_TaskFromBefore083KeepsOldReviewers(t *testing.T) {
	h := q83Normal(t, []string{"src/payment_auth_test.go"}, "")
	tk := h.task()
	if !q83Bool(t, tk, "Rules083") {
		t.Fatal("a task approved on 0.8.3 should be marked Rules083")
	}
	q83Set(t, tk, "Rules083", false)
	must(t, SaveTask(tk))
	got := h.required()
	for _, k := range []string{HelperPlanReviewer, HelperSecurity, HelperData} {
		if !strings.Contains(got, k) {
			t.Errorf("a task from before 0.8.3 keeps %s: %s", k, got)
		}
	}
}

// C33
func TestQA083_C33_GateFileStillNeedsPlanReviewer(t *testing.T) {
	h := q83Normal(t, []string{".github/workflows/ci.yml"}, "")
	p := filepath.Join(h.repo, ".github", "workflows", "ci.yml")
	h.mustAllow(h.pre("Write", map[string]any{"file_path": p}), "planned gate file")
	write(t, p, "on: push\n")
	h.post("Write", map[string]any{"file_path": p}, nil)
	if got := h.required(); !strings.Contains(got, HelperPlanReviewer) {
		t.Fatalf("a gate file changed on a 0.8.3 normal task still requires plan-reviewer: %s", got)
	}
}

// C34
func TestQA083_C34_AuthTestFileCantBeSmall(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- src/auth_test.go\n", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "isn't small")
}

// --- specialist carry-over after a fix ---

// q83Carry: a normal task with an auth file in the plan, a clean security
// review and a code review whose must-fix cites src/a.go.
func q83Carry(t *testing.T) *harness {
	t.Helper()
	h := riskyTask(t, "- lead, challenger, code-reviewer, security-reviewer")
	h.runHelper(HelperSecurity, "# Security review 1\n## Findings\n- none\n")
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: A should return 3 (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns 2.\n")
	return h
}

func (h *harness) q83Edit(rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	h.mustAllow(h.pre("Write", map[string]any{"file_path": p}), rel)
	write(h.t, p, body)
	h.post("Write", map[string]any{"file_path": p}, nil)
}

// C23
func TestQA083_C23_CleanSpecialistCarriesOverCitedFix(t *testing.T) {
	h := q83Carry(t)
	h.editA("3")
	if m := h.finishMissing(); strings.Contains(m, "plumbline:security-reviewer") || !strings.Contains(m, "plumbline:code-reviewer") {
		t.Fatalf("a fix limited to the file the code reviewer cited reruns only the code reviewer: %s", m)
	}
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:security-reviewer", "prompt": "go"}), "deny", "")
}

// C23 (refused side)
func TestQA083_C23_CarryOverRefused(t *testing.T) {
	t.Run("own signal file", func(t *testing.T) {
		h := q83Carry(t)
		h.q83Edit("src/auth/session.go", "package auth\n")
		h.editA("3")
		if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") {
			t.Fatalf("a fix touching a file that matches its own signal reruns the specialist: %s", m)
		}
	})
	t.Run("uncited source file", func(t *testing.T) {
		h := q83Carry(t)
		h.q83Edit("src/b.go", "package src\n\nfunc B() int { return 3 }\n")
		h.editA("3")
		if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") {
			t.Fatalf("a fix touching an uncited source file reruns the specialist: %s", m)
		}
	})
	t.Run("git can't list the changes", func(t *testing.T) {
		h := q83Carry(t)
		h.editA("3")
		tk := h.task()
		bogus := strings.Repeat("1", 40)
		for i := len(tk.Helpers) - 1; i >= 0; i-- {
			if tk.Helpers[i].Kind == HelperSecurity {
				tk.Helpers[i].PrintBefore, tk.Helpers[i].PrintAfter = bogus, bogus
				break
			}
		}
		must(t, SaveTask(tk))
		if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") {
			t.Fatalf("when git can't list the changes since the specialist's tree, it reruns: %s", m)
		}
	})
}

// C35
func TestQA083_C35_CarryOverRefusedForSecurityContent(t *testing.T) {
	h := q83Carry(t)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\n// A checks the password hash with bcrypt\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	h.related("src/a.go")
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") {
		t.Fatalf("a fix that adds security content in a cited file reruns the security reviewer: %s", m)
	}
}

// C24
func TestQA083_C24_ResumedBuilderWithoutHaltMerges(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	if real, err := filepath.EvalSymlinks(wt); err == nil {
		wt = real
	}
	RefreshSnapshot(h.sid+"-"+id, wt)
	cmd := map[string]any{"command": "./gen.sh"}
	in := h.helperIn(id, HelperBuilder, "Bash", cmd)
	in.Cwd = wt
	h.expect(PreToolUse(in), "", "")
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 2 }\n")
	in = h.helperIn(id, HelperBuilder, "Bash", cmd)
	in.Cwd, in.ToolResponse = wt, []byte("{}")
	PostToolUse(in)

	// SendMessage resumes it by agent id: SubagentStart fires again, no halt before
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	if n := len(h.task().helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("a resumed builder keeps one run: %d runs", n)
	}
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: A returns 2\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("stop: %v", o)
	}
	tk := h.task()
	if n := len(tk.helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("after the stop there is still one run: %d", n)
	}
	if _, _, err := MergeBuilder(tk, 1, nil); err != nil {
		t.Fatalf("the resumed builder's run is mergeable after its stop: %v", err)
	}
}

// C24 (resumed after a normal stop): SendMessage resumes a builder that
// already stopped; its second stop leaves one run, and it merges.
func TestQA083_C24_BuilderResumedAfterStopMerges(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 2 }\n")
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: A returns 2\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("first stop: %v", o)
	}
	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperBuilder})
	if strings.Contains(fmtOut(o), "already exists") {
		t.Fatalf("the resumed builder is told its copy already exists: %s", fmtOut(o))
	}
	if n := len(h.task().helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("a resume adds no run: %d runs", n)
	}
	write(t, filepath.Join(wt, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	write(t, h.findingsPath(id), h.complete(HelperBuilder, "# Build 1\n- src/a.go: A returns 3\n"))
	if o := h.stopHelper(id, HelperBuilder); o != nil {
		t.Fatalf("second stop: %v", o)
	}
	tk := h.task()
	if n := len(tk.helperRuns(HelperBuilder)); n != 1 {
		t.Fatalf("one run after the second stop: %d", n)
	}
	if r := tk.runByAgent(id); r == nil || r.alive() || r.Finished.IsZero() {
		t.Fatalf("the run is closed by the second stop: %+v", r)
	}
	if _, _, err := MergeBuilder(tk, 1, nil); err != nil {
		t.Fatalf("the resumed builder merges: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(h.repo, "src", "a.go"))
	must(t, err)
	if !strings.Contains(string(b), "return 3") {
		t.Fatalf("the merge brings the resumed run's work: %s", b)
	}
}

// C25
func TestQA083_C25_LighterSkillKeepsEveryRule(t *testing.T) {
	fi, err := os.Stat("rules-task.md")
	must(t, err)
	if fi.Size() >= 10*1024 {
		t.Fatalf("rules-task.md is %d bytes, over 10 KB", fi.Size())
	}
	skill, err := os.ReadFile(filepath.Join("..", "..", "skills", "task", "SKILL.md"))
	must(t, err)
	cps, err := os.ReadFile(filepath.Join("..", "..", "skills", "task", "checkpoints.md"))
	must(t, err)
	// sizes when the task opened (git show HEAD: | wc -c): 17498 and 12539
	if len(skill)+len(cps) >= 17498+12539 {
		t.Fatalf("SKILL.md plus checkpoints.md are %d bytes, not shorter than before (%d)", len(skill)+len(cps), 17498+12539)
	}
	all := string(skill) + string(cps)
	for _, rule := range []string{"Approve", "Change something", "Drop it", "Review cap", "plumbline run checks", "--final",
		"plumbline:arbiter", "Guards loosened", "self-review.md", "## For you", "## Review", "plumbline:challenger",
		"plumbline:code-reviewer", "plumbline:qa", "Challenge note", "Done when", "plumbline:guard-reviewer", q83Green} {
		if !strings.Contains(all, rule) {
			t.Errorf("the task skill no longer names %q", rule)
		}
	}
	rules, err := os.ReadFile("rules-task.md")
	must(t, err)
	if !strings.Contains(string(rules), q83Green) {
		t.Errorf("rules-task.md doesn't describe the new %q option", q83Green)
	}
}

// C26
func TestQA083_C26_StaticcheckClean(t *testing.T) {
	bin, err := exec.LookPath("staticcheck")
	if err != nil {
		home, _ := os.UserHomeDir()
		if p := filepath.Join(home, "go", "bin", "staticcheck"); fileExists(p) {
			bin = p
		} else {
			t.Skip("staticcheck isn't installed: run staticcheck ./internal/core/ ./cmd/plumbline/ by hand")
		}
	}
	c := exec.Command(bin, "./", "../../cmd/plumbline/")
	out, err := c.CombinedOutput()
	if err != nil && strings.Contains(string(out), "internal error in importing") {
		t.Skipf("the installed staticcheck can't read this Go version's packages (rebuild it: go install honnef.co/go/tools/cmd/staticcheck@latest):\n%s", out)
	}
	if err != nil {
		t.Fatalf("staticcheck found problems:\n%s", out)
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// C32
func TestQA083_C32_BaseRunNeverReused(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	s := h.setupNow()
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	// hand-made row: a passing run made on the approved tree in a temp
	// worktree for another task, as the 0.8.3 base run records it
	// (OnBase, Baseline, PrintBefore = PrintAfter = BaseTree, the repo path).
	r := &Run{ID: newID() + "-test", TaskID: "1124-other", Kind: KindTest, Command: s.Commands.Test, Started: Now().Add(-time.Minute),
		Finished: Now().Add(-30 * time.Second), PrintBefore: fp, PrintAfter: fp, Ran: 3, Root: h.repo,
		IgnoreKey: ignoreKey(s.IgnoreChanges), EnvKey: envKey(h.repo), Baseline: true}
	q83Set(t, r, "OnBase", true)
	must(t, appendRun(h.repo, r))
	got, err := ReuseBaseline(h.repo, h.task(), KindTest, s.Commands.Test, s)
	must(t, err)
	if got != nil {
		t.Fatalf("a base run (OnBase) must never be reused for another task: %+v", got)
	}
}
