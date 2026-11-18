package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Release 4 (0.6.10): WF3, optional saved format, typecheck and security
// checks; WF4, a [bug] case's test seen failing before the fix.

// checksHarness: a project whose setup saves format, typecheck and security
// commands as scripts that pass until a NO_<KIND> file exists, printing one
// "error" line per problem file.
func checksHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.Format = "./check.sh format"
	s.Commands.Typecheck = "./check.sh typecheck"
	s.Commands.Security = "./check.sh security"
	s.IgnoreChanges = []string{"coverage/**"}
	write(t, filepath.Join(h.repo, "check.sh"), "#!/bin/sh\nn=0\nfor f in NO_$1*; do [ -f \"$f\" ] && echo \"error: $f\" && n=1; done\nexit $n\n")
	must(t, os.Chmod(filepath.Join(h.repo, "check.sh"), 0o755))
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	return h
}

func (h *harness) runCheck(kind string) *Run {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	r, err := Execute(h.repo, h.task(), kind, "", savedCommand(s, kind), s, io.Discard)
	must(h.t, err)
	return r
}

func proofMissingNow(h *harness, final bool) string {
	s, _, _ := LoadSetup(h.repo)
	return strings.Join(ProofMissing(h.task(), s, final), "; ")
}

// --- WF3 ---

func TestSavedChecksAreNeededToCommit(t *testing.T) {
	h := checksHarness(t)
	h.startTask("small")
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/: nothing\n")
	h.docs(DocPlan, goodPlan)
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.approve()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)

	m := proofMissingNow(h, true)
	for _, k := range []string{KindFormat, KindTypecheck, KindSecurity} {
		if !strings.Contains(m, k+": no run on the current code (run: plumbline run "+k+")") {
			t.Errorf("%s should be needed: %s", k, m)
		}
	}
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "plumbline run security")
	for _, k := range []string{KindFormat, KindTypecheck, KindSecurity} {
		h.runCheck(k)
	}
	if m := proofMissingNow(h, true); m != "" {
		t.Fatalf("every saved check passed: %s", m)
	}
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "full suite passed")

	// A failing check blocks, and a run on older code doesn't count.
	write(t, filepath.Join(h.repo, "NO_typecheck"), "x")
	h.post("Bash", map[string]any{"command": "touch NO_typecheck"}, map[string]any{})
	h.runCheck(KindTypecheck)
	m = proofMissingNow(h, true)
	if !strings.Contains(m, "typecheck: typecheck failed with new problems") || !strings.Contains(m, "format: no run on the current code") {
		t.Fatalf("a failing typecheck, and the format run on older code: %s", m)
	}
}

func TestChecksThatWerentSavedArentNeeded(t *testing.T) {
	h := provenTask(t) // the setup saves only the test command
	if m := proofMissingNow(h, true); m != "" {
		t.Fatalf("no saved format, typecheck or security: %s", m)
	}
}

func TestSecurityProblemsFromBeforeTheTask(t *testing.T) {
	// A check that already failed before the first edit passes with no more
	// problems than then, like lint and build.
	base := &Run{TaskID: "t1", Kind: KindSecurity, Baseline: true, Finished: Now(), PrintBefore: "a", PrintAfter: "a", Exit: 1, Problems: 3, Ran: 1}
	same := &Run{TaskID: "t1", Kind: KindSecurity, Finished: Now(), PrintBefore: "b", PrintAfter: "b", Exit: 1, Problems: 3, Ran: 1}
	more := &Run{TaskID: "t1", Kind: KindSecurity, Finished: Now(), PrintBefore: "b", PrintAfter: "b", Exit: 1, Problems: 4, Ran: 1}
	runs := []*Run{base}
	if v := Judge(same, BaselineFor(runs, "t1", KindSecurity)); !v.OK {
		t.Errorf("the same problems as before the task: %v", v)
	}
	if v := Judge(more, BaselineFor(runs, "t1", KindSecurity)); v.OK {
		t.Errorf("a new problem: %v", v)
	}
	if v := Judge(same, nil); v.OK {
		t.Errorf("a failing check with no baseline: %v", v)
	}
}

func TestFormatThatRewritesIsCaught(t *testing.T) {
	for _, c := range []string{"prettier --write .", "npx prettier --write .", "pnpm exec prettier -w src", "gofmt -w .", "black .", "ruff format .", "eslint --fix .", "ruff check --fix"} {
		if !formatRewrites(c) {
			t.Errorf("%q rewrites files", c)
		}
	}
	for _, c := range []string{"", "prettier --check .", "npx prettier --check .", "ruff format --check", "ruff format --diff src", "black --check .", `test -z "$(gofmt -l .)"`, "npm run format:check"} {
		if formatRewrites(c) {
			t.Errorf("%q only checks", c)
		}
	}
	h := newHarness(t)
	s := &Setup{Kind: "personal"}
	s.Commands.Test = "./test.sh"
	s.Commands.Format = "npx prettier --write ."
	if _, err := SaveSetup(h.repo, s); err == nil || !strings.Contains(err.Error(), "only checks") {
		t.Fatalf("a format command that rewrites should be refused: %v", err)
	}
	s.Commands.Format = "npx prettier --check ."
	if _, err := SaveSetup(h.repo, s); err != nil {
		t.Fatalf("a check-only format command: %v", err)
	}
}

func TestFormatRunThatChangedFilesSaysWhy(t *testing.T) {
	// A saved format command that still rewrites (a script that does, which
	// the setup check can't see) changes the code under its own run.
	h := checksHarness(t)
	h.startTask("small")
	tk := h.task()
	tk.Phase = PhaseBuilding
	must(t, SaveTask(tk))
	write(t, filepath.Join(h.repo, "check.sh"), "#!/bin/sh\necho reformatted >> src/a.go\n")
	h.runCheck(KindFormat)
	if m := proofMissingNow(h, false); !strings.Contains(m, "the saved format command rewrote files, and it must only check") {
		t.Fatalf("the rewrite should be named: %s", m)
	}
}

func TestHelpersRunTheSavedChecks(t *testing.T) {
	for _, k := range []string{"format", "typecheck", "security", "lint", "build"} {
		s := Segments("plumbline run " + k)[0]
		if !helperMayRun(HelperTester, s) || !helperMayRun(HelperBuilder, s) {
			t.Errorf("the tester and builders may run %s", k)
		}
		if helperMayRun(HelperQA, s) {
			t.Errorf("blind QA runs only its tests, not %s", k)
		}
	}
	if helperMayRun(HelperBuilder, Segments("plumbline run e2e")[0]) {
		t.Error("builders don't run e2e (unchanged)")
	}
}

func TestPackagePlanNeedsTheSavedChecksBaselines(t *testing.T) {
	h := checksHarness(t)
	h.startTask("small")
	tk := h.task()
	s, _, _ := LoadSetup(h.repo)
	c := &hookCtx{task: tk, setup: s, root: h.repo}
	pc := PlanCheck{Files: []string{"package.json"}}
	if p := strings.Join(c.upgradeProblems(pc), ""); !strings.Contains(p, "plumbline run test, plumbline run format, plumbline run typecheck, plumbline run security") {
		t.Fatalf("every saved check needs its before-state: %s", p)
	}
	h.runCheck(KindFormat)
	if p := strings.Join(c.upgradeProblems(pc), ""); strings.Contains(p, "run format") || !strings.Contains(p, "run security") {
		t.Fatalf("format's before-state is saved: %s", p)
	}
}

func TestHotfixFirstCommitNeedsTheSavedChecks(t *testing.T) {
	h := hotfixHarness(t)
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Security = "./build.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "commit", "-q", "-am", "chore: security check")
	o := expand(h, "hotfix", "PROJ-12: checkout returns 500")
	if !strings.Contains(fmtOut(o), "the saved checks (lint, build, and format, typecheck, security when saved)") {
		t.Fatalf("the hotfix start says what the first commit needs: %s", fmtOut(o))
	}
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, hotfixPlan)
	h.approve()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err = ExecuteRelated(h.repo, h.task(), "./test.sh 'src/a.go'", []string{"src/a.go"}, s, io.Discard)
	must(t, err)
	h.lintBuild()
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "security: no run on the current code")
	h.runCheck(KindSecurity)
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "related tests and the saved checks passed")
}

// --- WF4 ---

func TestPlanKeepsTheBugCasesTests(t *testing.T) {
	plan := "## Files\n- src/a.go\n## Cases\n" +
		"- [bug] refund twice -> test: `src/refund.test.ts::refunds twice`\n" +
		"- [bug] old total -> test: src/a_test.go\n" +
		"- [bug] crash on empty, does not apply, because it can't be reached\n" +
		"- [normal] refund once -> test: src/refund.test.ts::refunds once\n" +
		"- [edge] bug-like but not a bug case -> test: src/b_test.go::TestBug\n" +
		"## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n"
	got := strings.Join(CheckPlan(plan).BugTests, ",")
	if got != "src/refund.test.ts::refunds twice,src/a_test.go" {
		t.Fatalf("bug tests: %s", got)
	}
}

func TestFailedNamed(t *testing.T) {
	cases := []struct {
		ids  []string
		name string
		want bool
	}{
		{[]string{"TestRefund"}, "TestRefund", true},
		{[]string{"TestRefund/twice"}, "TestRefund", true},
		{[]string{"TestRefund/twice"}, "TestRefund/twice", true},
		{[]string{"TestRefundTwice"}, "TestRefund", false},
		{[]string{"tests/test_refund.py::test_twice"}, "test_twice", true},
		{[]string{"tests/test_refund.py::TestRefund::test_twice[1]"}, "test_twice", true},
		{[]string{"tests/test_refund.py::test_twice_more"}, "test_twice", false},
		{[]string{"Refunds › refunds twice"}, "refunds twice", true},
		{[]string{"src/a.test.ts > refunds twice"}, "refunds twice", true},
		{[]string{"Refunds › refunds twice over"}, "refunds twice", true}, // the name is a whole-word part
		{[]string{"Refunds › refunds thrice"}, "refunds twice", false},
		{[]string{"Tests\\RefundTest::testTwice"}, "testTwice", true},
		{[]string{"orders::tests::refund_twice"}, "refund_twice", true},
		{nil, "TestRefund", false},
	}
	for _, c := range cases {
		if got := failedNamed(c.ids, c.name); got != c.want {
			t.Errorf("failedNamed(%v, %q) = %v, want %v", c.ids, c.name, got, c.want)
		}
	}
}

func TestGoFailingPackageNamesItsTests(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.22\n")
	out := "--- FAIL: TestRefund (0.00s)\nFAIL\nFAIL\texample.com/shop/internal/orders\t0.2s\nFAIL\n"
	got := strings.Join(CaseTestsSeen(root, out, []string{"internal/orders/refund_test.go", "internal/tax/tax_test.go"}), ",")
	if got != "internal/orders/refund_test.go" {
		t.Errorf("seen in a failing Go run: %q", got)
	}
	// A package that didn't build ran no test.
	if got := CaseTestsSeen(root, "FAIL\texample.com/shop/internal/orders [build failed]\n", []string{"internal/orders/refund_test.go"}); len(got) != 0 {
		t.Errorf("a build failure isn't a run of the test: %v", got)
	}
}

// bugTask: an approved small task whose [bug] case links to the given
// test, in a setup whose related command takes files. oldFailure: a test
// (tests/old) already fails before the task, in the baseline run.
func bugTask(t *testing.T, link string, oldFailure ...bool) *harness {
	t.Helper()
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.TestRelated = "./test.sh {files}"
	s.IgnoreChanges = []string{"coverage/**"}
	// The "suite" fails TestA while src/a.go still returns 1 and the test
	// exists, and TestOld while the test file tests/old exists.
	write(t, filepath.Join(h.repo, "test.sh"), `#!/bin/sh
fail=""
if [ -f src/a_test.go ] && grep -q 'return 1' src/a.go; then fail="$fail TestA"; fi
if [ -f tests/old ]; then fail="$fail TestOld"; fi
if [ -n "$fail" ]; then for f in $fail; do echo "--- FAIL: $f (0.00s)"; done; echo "FAIL"; echo "FAIL	example.com/r/src	0.1s"; exit 1; fi
echo "ok  	example.com/r/src	0.1s"
`)
	write(t, filepath.Join(h.repo, "go.mod"), "module example.com/r\n\ngo 1.22\n")
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/: nothing\n")
	if len(oldFailure) > 0 && oldFailure[0] {
		write(t, filepath.Join(h.repo, "tests", "old"), "x")
		if r, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", s, io.Discard); err != nil || !r.Baseline || r.Failed != 1 {
			t.Fatalf("the baseline should fail TestOld: %+v %v", r, err)
		}
	}
	h.docs(DocPlan, "# Plan\n## Files\n- src/a.go\n- src/a_test.go\n## Cases\n- [bug] A returns 2 -> test: "+link+"\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n")
	o := h.approveOut()
	if !strings.Contains(fmtOut(o), "see them fail with plumbline run related --files src/a_test.go") {
		t.Fatalf("the approval should say to see the [bug] test fail first: %s", fmtOut(o))
	}
	return h
}

// approveOut approves the checkpoint and returns what the post hook said.
func (h *harness) approveOut() Output {
	h.t.Helper()
	q := checkpointQ()
	h.expect(h.pre("AskUserQuestion", q), "", "")
	o := h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Approve"}})
	if h.task().Phase != PhaseBuilding {
		h.t.Fatal("checkpoint approval not recorded")
	}
	return o
}

func (h *harness) related(files ...string) *Run {
	h.t.Helper()
	s, _, _ := LoadSetup(h.repo)
	r, err := ExecuteRelated(h.repo, h.task(), "./test.sh "+strings.Join(files, " "), files, s, io.Discard)
	must(h.t, err)
	return r
}

func (h *harness) writeTest() {
	h.t.Helper()
	p := filepath.Join(h.repo, "src", "a_test.go")
	write(h.t, p, "package src\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
	h.post("Edit", map[string]any{"file_path": p}, nil)
}

func (h *harness) fix(body string) {
	h.t.Helper()
	p := filepath.Join(h.repo, "src", "a.go")
	write(h.t, p, "package src\n\nfunc A() int { "+body+" }\n")
	h.post("Edit", map[string]any{"file_path": p}, nil)
}

func bugMissing(h *harness) string {
	s, _, _ := LoadSetup(h.repo)
	fp, _ := Fingerprint(h.repo, s.IgnoreChanges)
	return strings.Join(bugRedMissing(h.task(), s, fp, LoadRuns(h.repo)), "; ")
}

func TestBugTestSeenFailingBeforeTheFix(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	if r := h.related("src/a_test.go"); r.Failed == 0 {
		t.Fatal("the test should fail before the fix")
	}
	h.fix("return 2")
	h.related("src/a_test.go")
	if m := bugMissing(h); m != "" {
		t.Fatalf("seen failing while only the test had changed: %s", m)
	}
}

func TestBugTestWrittenAfterTheFix(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.fix("return 2")
	h.writeTest()
	h.related("src/a_test.go")
	m := bugMissing(h)
	if !strings.Contains(m, "[bug] case test src/a_test.go::TestA: no recorded run shows it failing") || !strings.Contains(m, "plumbline waive bug-test") {
		t.Fatalf("no failing run: %s", m)
	}
	// A run that fails while the fix is half done isn't before the fix.
	h.fix("return 1 // half done")
	h.related("src/a_test.go")
	if m := bugMissing(h); m == "" {
		t.Fatal("a failing run after a code change counted")
	}
	// Setting the fix aside (what git stash does) shows the test failing
	// on the approved code, and putting it back keeps the passing runs.
	h.fix("return 1")
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	if r := h.related("src/a_test.go"); r.Failed == 0 {
		t.Fatal("the test should fail without the fix")
	}
	h.fix("return 2")
	if m := bugMissing(h); m != "" {
		t.Fatalf("the failing run with the fix set aside: %s", m)
	}
}

func TestBugTestFailureMustBeTheNamedTest(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	// Another test fails while only tests changed, and TestA isn't written
	// yet: the run was given the file, but TestA didn't fail.
	write(t, filepath.Join(h.repo, "tests", "old"), "x")
	if r := h.related("src/a_test.go"); strings.Join(r.FailedIDs, ",") != "TestOld" {
		t.Fatalf("only TestOld should fail: %v", r.FailedIDs)
	}
	if m := bugMissing(h); m == "" {
		t.Fatal("a run where TestA didn't fail counted")
	}
	h.writeTest()
	h.related("src/a_test.go")
	if m := bugMissing(h); m != "" {
		t.Fatalf("TestA failed beside TestOld: %s", m)
	}
}

func TestBugTestByFileNeedsANewFailure(t *testing.T) {
	// The case names only the file: a failure that was there before the
	// task isn't the case test's.
	h := bugTask(t, "src/a_test.go", true)
	if m := bugMissing(h); m == "" {
		t.Fatal("the baseline's old failure counted for the case test")
	}
	h.related("src/a_test.go") // fails TestOld only, as before the task
	if m := bugMissing(h); m == "" {
		t.Fatal("a failure that was there before the task counted for the case test")
	}
	h.writeTest()
	h.related("src/a_test.go")
	if m := bugMissing(h); m != "" {
		t.Fatalf("a new failure of the case's file: %s", m)
	}
}

func TestBugTestRunMustBeTheSavedCommandAndFail(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.writeTest()
	s, _, _ := LoadSetup(h.repo)
	_, err := ExecuteRelated(h.repo, h.task(), "sh ./test.sh src/a_test.go", []string{"src/a_test.go"}, s, io.Discard)
	must(t, err)
	if m := bugMissing(h); m == "" {
		t.Fatal("a run of another command counted")
	}
	// A passing run shows nothing failing.
	h.fix("return 2")
	h.related("src/a_test.go")
	if m := bugMissing(h); m == "" {
		t.Fatal("a passing run counted")
	}
}

func TestBugTestWaiver(t *testing.T) {
	h := bugTask(t, "src/a_test.go::TestA")
	h.fix("return 2")
	h.writeTest()
	tk := h.task()
	s, _, _ := LoadSetup(h.repo)
	fp, _ := Fingerprint(h.repo, s.IgnoreChanges)
	tk.Waivers = append(tk.Waivers, Waiver{Check: "bug-test", Reason: "the bug needs a real card terminal", Print: fp, At: Now()})
	must(t, SaveTask(tk))
	if m := bugMissing(h); m != "" {
		t.Fatalf("waived on the current code: %s", m)
	}
	h.fix("return 2 // changed")
	if m := bugMissing(h); m == "" {
		t.Fatal("the waiver lapses when the code changes")
	}
}

func TestBugTestsFromAnOlderApproval(t *testing.T) {
	// Approved before 0.6.10: the [bug] cases are read from plan.md.
	h := bugTask(t, "src/a_test.go::TestA")
	tk := h.task()
	tk.BugTests, tk.BugStored = nil, false
	must(t, SaveTask(tk))
	if got := strings.Join(tk.bugCaseTests(), ","); got != "src/a_test.go::TestA" {
		t.Fatalf("read from the plan: %s", got)
	}
	// Approved before 0.5.0: no approval fingerprint, so it can't be judged.
	tk.ApprovalPrint = ""
	must(t, SaveTask(tk))
	if m := bugMissing(h); m != "" {
		t.Fatalf("no approval fingerprint: %s", m)
	}
}

func TestFinishNeedsTheBugTestSeenFailing(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	h.fix("return 2")
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	if m := h.finishMissing(); !strings.Contains(m, "[bug] case test test.sh: no recorded run shows it failing") {
		t.Fatalf("finish should need the failing run: %s", m)
	}
}

func TestBugTestRunMustBeTheCaseFile(t *testing.T) {
	// The case names only a file: the failing run must have run it (given
	// it, or its output names it), or be the full suite where the related
	// command can't take files.
	s := &Setup{}
	s.Commands.Test, s.Commands.TestRelated = "npm test", "npx jest {files}"
	tk := &Task{ID: "t1", Root: t.TempDir(), ApprovalPrint: "fp", BugTests: []string{"x.test.ts"}, BugStored: true}
	red := func(kind, cmd string, files, seen []string) []*Run {
		return []*Run{{TaskID: "t1", Kind: kind, Command: cmd, Finished: Now(), PrintBefore: "fp", PrintAfter: "fp", Ran: 3, Failed: 1, Exit: 1, Files: files, Seen: seen}}
	}
	cases := []struct {
		name    string
		related string
		runs    []*Run
		counts  bool
	}{
		{"given another file", "npx jest {files}", red(KindRelated, "npx jest other.test.ts", []string{"other.test.ts"}, nil), false},
		{"given the file", "npx jest {files}", red(KindRelated, "npx jest x.test.ts", []string{"x.test.ts"}, nil), true},
		{"the suite names it", "npx jest {files}", red(KindTest, "npm test", nil, []string{"x.test.ts"}), true},
		{"the suite doesn't name it", "npx jest {files}", red(KindTest, "npm test", nil, nil), false},
		{"the suite, no files command", "npx jest", red(KindTest, "npm test", nil, nil), true},
	}
	for _, c := range cases {
		s.Commands.TestRelated = c.related
		m := bugRedMissing(tk, s, "fp", c.runs)
		if (len(m) == 0) != c.counts {
			t.Errorf("%s: counts %v, want %v (%v)", c.name, len(m) == 0, c.counts, m)
		}
	}
}

// A hotfix switches to the production branch right after its checkpoint:
// the approval fingerprint follows that move while nothing but tests had
// changed, so the [bug] test can still be seen failing before the fix.
func TestApprovalFollowsTheCheckoutAfterTheCheckpoint(t *testing.T) {
	for _, mode := range []string{"tests only", "code first", "edit in the same line"} {
		codeFirst := mode != "tests only"
		h := newHarness(t)
		h.setup()
		run(t, h.repo, "git", "checkout", "-q", "-b", "develop")
		write(t, filepath.Join(h.repo, "src", "dev.go"), "package src\n")
		run(t, h.repo, "git", "add", "-A")
		run(t, h.repo, "git", "commit", "-q", "-m", "feat: dev only")
		tk := h.startTask("small")
		tk.StartCommit, tk.StartTips = HeadCommit(h.repo), BranchTips(h.repo)
		must(t, SaveTask(tk))
		for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
			h.post(tool, nil, nil)
		}
		h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
		h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- nothing\n")
		h.docs(DocPlan, goodPlan)
		h.approve()
		approved := h.task().ApprovalPrint
		if mode == "code first" {
			h.fix("return 2")
		}
		cmd := "git checkout -q main"
		if mode == "edit in the same line" {
			cmd += " && echo '// x' >> src/a.go"
		}
		if d, r := decision(h.bash(cmd)); d == "deny" {
			t.Fatalf("%s refused: %s", cmd, r)
		}
		run(t, h.repo, "git", "checkout", "-q", "main")
		if mode == "edit in the same line" {
			f, err := os.OpenFile(filepath.Join(h.repo, "src", "a.go"), os.O_APPEND|os.O_WRONLY, 0o644)
			must(t, err)
			_, err = f.WriteString("// x\n")
			must(t, err)
			must(t, f.Close())
		}
		h.post("Bash", map[string]any{"command": cmd}, map[string]any{})
		now, _ := Fingerprint(h.repo, []string{"coverage/**"})
		got := h.task().ApprovalPrint
		switch {
		case !codeFirst && got != now:
			t.Fatalf("the approval should follow to main's code: %s, now %s", got, now)
		case codeFirst && got != approved:
			t.Fatalf("%s: the approval stays: %s", mode, got)
		}
		if codeFirst {
			continue
		}
		h.redRun()
		if m := bugMissing(h); m != "" {
			t.Fatalf("the failing run on main, only a test changed: %s", m)
		}
		if h.task().PrintBeforeMove != "" {
			t.Fatal("the fingerprint noted before the move is cleared after it")
		}
	}
}

func TestBugTestChangedAfterItsFailingRun(t *testing.T) {
	// A stand-in test seen failing, then rewritten with the fix: the test as
	// it is now was never seen failing.
	h := bugTask(t, "src/a_test.go::TestA")
	p := filepath.Join(h.repo, "src", "a_test.go")
	write(t, p, "package src\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal() }\n")
	h.post("Edit", map[string]any{"file_path": p}, nil)
	h.related("src/a_test.go")
	h.writeTest()
	h.fix("return 2")
	if m := bugMissing(h); !strings.Contains(m, "it changed after the run that saw it failing") {
		t.Fatalf("the test changed after its failing run: %s", m)
	}
	// Set the fix aside, see the real test fail, put the fix back.
	h.fix("return 1")
	h.related("src/a_test.go")
	h.fix("return 2")
	if m := bugMissing(h); m != "" {
		t.Fatalf("the test as it is now was seen failing: %s", m)
	}
}
