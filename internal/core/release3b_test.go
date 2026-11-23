package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- the diff parser shared by the scans ---

func TestParseDiffReadsHunksByCount(t *testing.T) {
	// A removed SQL comment starts with "-- ", an added line with "++":
	// neither is a file header, because the hunk counts say they're lines.
	diff := "diff --git a/q.sql b/q.sql\n--- a/q.sql\n+++ b/q.sql\n@@ -3,2 +3 @@\n--- old comment\n-select 1;\n+++counter;\n" +
		"diff --git a/old_test.go b/old_test.go\ndeleted file mode 100644\n--- a/old_test.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-package x\n" +
		addedFileDiff("new.txt", []byte("one\ntwo\n"))
	got := parseDiff(diff)
	if len(got) != 3 {
		t.Fatalf("want 3 files, got %+v", got)
	}
	if got[0].Path != "q.sql" || len(got[0].Lines) != 3 || got[0].Lines[0].Text != "-- old comment" || got[0].Lines[0].Add || !got[0].Lines[2].Add || got[0].Lines[2].Text != "++counter;" || got[0].Lines[2].N != 3 {
		t.Errorf("q.sql: %+v", got[0])
	}
	if got[1].Path != "old_test.go" || !got[1].Deleted {
		t.Errorf("deleted file: %+v", got[1])
	}
	if got[2].Path != "new.txt" || len(got[2].Lines) != 2 || got[2].Lines[1].N != 2 {
		t.Errorf("new file: %+v", got[2])
	}
	if addedFileDiff("empty", nil) != "--- /dev/null\n+++ b/empty\n@@ -0,0 +1,0 @@\n" {
		t.Errorf("empty file: %q", addedFileDiff("empty", nil))
	}
}

func TestSecretScanStillReadsNewFiles(t *testing.T) {
	// The commit scan builds new files with addedFileDiff: every line of a
	// new file is read, not only the first (the old header said +1).
	d := addedFileDiff("config.ts", []byte("const a = 1\nconst k = 'AKIAABCDEFGHIJKLMNOP'\n"))
	if got := ScanSecrets(d); len(got) != 1 || !strings.Contains(got[0], "config.ts:2") {
		t.Errorf("secret on line 2 of a new file: %v", got)
	}
}

// --- P3: weakened tests ---

func file(path string, deleted bool, lines ...string) fileDiff {
	f := fileDiff{Path: path, Deleted: deleted}
	for i, l := range lines {
		f.Lines = append(f.Lines, diffLine{N: i + 1, Add: strings.HasPrefix(l, "+"), Text: l[1:]})
	}
	return f
}

func TestWeakenedTestsFinds(t *testing.T) {
	cases := []struct {
		f    fileDiff
		want string // "" means nothing found
	}{
		{file("src/a.test.ts", false, "+  it.skip('refunds', () => {"), "skipped or focused"},
		{file("src/a.test.ts", false, "+  describe.only('refunds', () => {"), "skipped or focused"},
		{file("x_test.go", false, "+\tt.Skip(\"flaky\")"), "skipped or focused"},
		{file("tests/test_a.py", false, "+@pytest.mark.skip(reason='slow')"), "skipped or focused"},
		{file("spec/a_spec.rb", false, "+  xit 'refunds' do"), ""}, // xit without ( is rspec's: not matched by the call form
		{file("spec/a_spec.rb", false, "+    skip 'not yet'"), "skipped or focused"},
		{file("src/parser.ts", false, "+  return skip('(')"), ""}, // product code may call skip()
		{file("src/a.ts", false, "+// eslint-disable-next-line no-explicit-any"), "silenced"},
		{file("src/a.ts", false, "+// @ts-ignore"), "silenced"},
		{file("app/a.py", false, "+import os  # noqa: F401"), "silenced"},
		{file("x.go", false, "+\tx := 1 //nolint:errcheck"), "silenced"},
		{file(".github/workflows/ci.yml", false, "+    continue-on-error: true"), "allowed to pass"},
		{file(".github/workflows/ci.yml", false, "+      run: npm test || true"), "allowed to pass"},
		{file("scripts/clean.sh", false, "+rm -rf build || true"), ""}, // only in CI files
		{file(".gitlab-ci.yml", false, "+  allow_failure: true"), "allowed to pass"},
		{file("Makefile", false, "+\tgit commit --no-verify -m x"), "allowed to pass"},
		{file("src/pay.ts", false, "+if (process.env.CI) return true"), "behaves differently"},
		{file("src/pay.ts", false, "+if (process.env.NODE_ENV === 'test') return mockPay()"), "behaves differently"},
		{file("src/pay.ts", false, "+if (process.env.NODE_ENV === 'production') minify()"), ""},
		{file("vite.config.ts", false, "+  open: !process.env.CI,"), ""}, // config may read CI
		{file("internal/x_test.go", true), "deleted"},
		{file("internal/x.go", true, "-package x"), ""},
		{file("src/a.test.ts", false, "-  expect(total).toBe(10)", "-  expect(tax).toBe(1)", "+  expect(total).toBe(10)"), "fewer assertions (2 removed, 1 added)"},
		{file("src/a.test.ts", false, "-  expect(total).toBe(10)", "+  expect(total).toEqual(10)"), ""}, // reworded, not removed
		{file("jest.config.js", false, "-  coverageThreshold: 80,", "+  coverageThreshold: 70,"), "threshold is lowered (coverageThreshold 80 to 70)"},
		{file("jest.config.js", false, "-  coverageThreshold: 70,", "+  coverageThreshold: 80,"), ""},
		{file("src/rate.ts", false, "-const threshold = 5", "+const threshold = 3"), ""}, // product code, not a check
		{file("docs/testing.md", false, "+Never use it.skip( or eslint-disable."), ""},
	}
	for _, c := range cases {
		got := WeakenedTests([]fileDiff{c.f})
		var text []string
		for _, w := range got {
			text = append(text, w.String())
		}
		all := strings.Join(text, "; ")
		switch {
		case c.want == "" && len(got) > 0:
			t.Errorf("%s %v: want nothing, got %s", c.f.Path, c.f.Lines, all)
		case c.want != "" && !strings.Contains(all, c.want):
			t.Errorf("%s %v: want %q, got %q", c.f.Path, c.f.Lines, c.want, all)
		}
	}
}

func TestWeakenedGoldenNeedsItsTest(t *testing.T) {
	snap := file("src/__snapshots__/a.test.ts.snap", false, "+exports[`x`] = `2`;")
	golden := file("internal/testdata/out.golden", false, "+new")
	if w := WeakenedTests([]fileDiff{snap, golden}); len(w) != 2 {
		t.Errorf("snapshot and golden changed alone: %v", w)
	}
	withTests := []fileDiff{snap, golden, file("src/a.test.ts", false, "+  expect(a()).toMatchSnapshot()"), file("internal/out_test.go", false, "+\t// regenerated")}
	if w := WeakenedTests(withTests); len(w) != 0 {
		t.Errorf("with their tests changed: %v", w)
	}
}

func TestWeakenedLineListsEachFile(t *testing.T) {
	changes := []fileDiff{file("src/a.test.ts", false, "+  it.skip('x', () => {")}
	cases := []struct{ report, want string }{
		{"## Proof\n- Weakened tests: none\n", "weakened tests"},
		{"## Proof\n- Full suite: pass\n## Review\n- src/a.test.ts skipped\n", "weakened tests"}, // named, but not on the line
		{"## Proof\n- Weakened tests: src/a.test.ts: the refund test waits on the provider fix (ticket 12)\n", ""},
		{"## Proof\n- Weakened tests:\n  - src/a.test.ts: waits on the provider fix\n- Full suite: pass\n", ""},
	}
	for _, c := range cases {
		m := strings.Join(weakenedMissing(changes, c.report, nil), "; ")
		if (c.want == "") != (m == "") || !strings.Contains(m, c.want) {
			t.Errorf("report %q: want %q, got %q", c.report, c.want, m)
		}
	}
}

func TestDebugTagLeftIn(t *testing.T) {
	tag := "[PLB-" + "DEBUG]"
	changes := []fileDiff{file("src/a.ts", false, "+console.log('"+tag+" total', total)"), file("docs/debug.md", false, "+Tag logs with "+tag)}
	m := strings.Join(debugMissing(changes), "; ")
	if !strings.Contains(m, "src/a.ts:1") || strings.Contains(m, "docs/debug.md") {
		t.Errorf("debug tag: %s", m)
	}
	if m := debugMissing([]fileDiff{file("src/a.ts", false, "-console.log('"+tag+"')")}); len(m) != 0 {
		t.Errorf("a removed debug line is the fix, not a leftover: %v", m)
	}
}

// builtTask: a small task built and proven with the given files written
// (each Edit recorded), its suite run and its code reviewed.
func builtTask(t *testing.T, files map[string]string) *harness {
	h := groundedTask(t, "small")
	plan := "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n"
	for p := range files {
		plan += "- " + p + " (new)\n"
	}
	plan += "## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n"
	h.docs(DocPlan, plan)
	h.approve()
	h.redRun()
	for p, content := range files {
		abs := filepath.Join(h.repo, p)
		write(t, abs, content)
		h.post("Edit", map[string]any{"file_path": abs}, nil)
	}
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.runHelper(HelperCodeReviewer, "# Code review 1\nnothing at 80 or more\n")
	return h
}

func TestFinishFindsWeakenedTestsAndDebugLogs(t *testing.T) {
	tag := "[PLB-" + "DEBUG]"
	h := builtTask(t, map[string]string{
		"src/a_test.go": "package src\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Skip(\"later\") }\n",
		"src/b.go":      "package src\n\nfunc B() int { println(\"" + tag + "\"); return 2 }\n",
	})
	h.docs(DocReport, r2Report("Ready: proven."+"\nRollback: revert the commit.", "- Full suite: pass\n- Weakened tests: none"))
	m := h.finishMissing()
	if !strings.Contains(m, "src/a_test.go:5 a test is skipped") || !strings.Contains(m, "src/b.go:3") {
		t.Fatalf("finish should name the skip and the debug line: %s", m)
	}
	// Listing the skip doesn't excuse the debug log.
	h.docs(DocReport, r2Report("Ready: proven."+"\nRollback: revert the commit.", "- Full suite: pass\n- Weakened tests: src/a_test.go: TestA waits on ticket 12"))
	if m := h.finishMissing(); strings.Contains(m, "weakened") || !strings.Contains(m, "debug logs") {
		t.Fatalf("listed skip, debug log left: %s", m)
	}
}

func TestFinishIgnoresTheUsersOwnWork(t *testing.T) {
	h := newHarness(t)
	h.setup()
	// Unsaved work of the user's own from before the task, with a skip.
	write(t, filepath.Join(h.repo, "src", "mine_test.go"), "package src\n\nimport \"testing\"\n\nfunc TestM(t *testing.T) { t.Skip() }\n")
	h.startTask("small")
	tk := h.task()
	changes, err := taskChanges(tk, nil)
	must(t, err)
	for _, w := range WeakenedTests(changes) {
		t.Errorf("the user's own file counted: %v", w)
	}
}

// --- L4: failing test names for more runners, skipped counts, baselines ---

func TestParseTestOutputMoreRunners(t *testing.T) {
	cases := []struct {
		name, out   string
		ran, failed int
		skipped     int
		ids         string
	}{
		{"vitest", " FAIL  src/a.test.ts > refunds > twice\n FAIL  src/b.test.ts > tax\n Test Files  2 failed (2)\n      Tests  2 failed | 5 passed | 1 skipped (8)\n", 8, 2, 1, "src/a.test.ts > refunds > twice,src/b.test.ts > tax"},
		{"jest", "Tests:       1 failed, 2 skipped, 3 passed, 6 total\n", 6, 1, 2, ""},
		{"pytest", "== 1 failed, 4 passed, 2 skipped in 0.3s ==\n", 5, 1, 2, ""},
		{"mocha", "  3 passing (20ms)\n  1 pending\n  2 failing\n\n  1) Refunds\n       refuses twice:\n     AssertionError: x\n\n  2) Tax rounds:\n     Error: y\n", 5, 2, 1, "Refunds refuses twice,Tax rounds"},
		{"rspec", "Failures:\n\n4 examples, 1 failure, 2 pending\n\nFailed examples:\n\nrspec ./spec/order_spec.rb:12 # Order refunds twice\n", 4, 1, 2, "Order refunds twice"},
		{"cargo", "test tests::adds ... ok\ntest tests::refunds ... FAILED\n\ntest result: FAILED. 1 passed; 1 failed; 1 ignored; 0 measured; 0 filtered out\n", 2, 1, 1, "tests::refunds"},
		{"phpunit", "There was 1 failure:\n\n1) Tests\\Unit\\OrderTest::testRefund\nFailed asserting\n\nFAILURES!\nTests: 5, Assertions: 9, Failures: 1, Skipped: 2.\n", 5, 1, 2, "Tests\\Unit\\OrderTest::testRefund"},
		{"go -v", "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n--- SKIP: TestB (0.00s)\nok  \tx/y\t0.1s\n", 2, 0, 1, ""},
	}
	for _, c := range cases {
		got := ParseTestOutput(c.out, "")
		if got.Ran != c.ran || got.Failed != c.failed || got.Skipped != c.skipped || strings.Join(got.IDs, ",") != c.ids {
			t.Errorf("%s: got %+v, want ran %d failed %d skipped %d ids %q", c.name, got, c.ran, c.failed, c.skipped, c.ids)
		}
	}
	if got := ParseTestOutput("custom: 9 ok, 2 skipped", `(?P<ran>\d+) ok, (?P<skipped>\d+) skipped`); got.Ran != 9 || got.Skipped != 2 {
		t.Errorf("custom pattern with skipped: %+v", got)
	}
}

func TestNewFailureBehindFixedOneIsCaught(t *testing.T) {
	// With names (now read for vitest too) a new failure can't hide behind
	// an old one that was fixed: same count, different test.
	base := &Run{Kind: KindTest, Finished: Now(), PrintBefore: "a", PrintAfter: "a", Ran: 5, Failed: 1, FailedIDs: []string{"src/a.test.ts > old"}}
	now := &Run{Kind: KindTest, Finished: Now(), PrintBefore: "b", PrintAfter: "b", Ran: 5, Failed: 1, Exit: 1, FailedIDs: []string{"src/b.test.ts > new"}}
	if v := Judge(now, base); v.OK || !strings.Contains(v.Reason, "src/b.test.ts > new") {
		t.Errorf("new failure behind a fixed one: %+v", v)
	}
}

func TestBaselineNewestAndNotBroken(t *testing.T) {
	mk := func(id string, ran, failed int) *Run {
		return &Run{ID: id, TaskID: "t1", Kind: KindTest, Baseline: true, Finished: Now(), PrintBefore: "p", PrintAfter: "p", Ran: ran, Failed: failed}
	}
	// The first baseline ran with the database down: every test failed.
	broken, fixed := mk("1", 40, 40), mk("2", 40, 2)
	if b := BaselineFor([]*Run{broken}, "t1", KindTest); b != nil {
		t.Errorf("an all-failed baseline isn't one: %+v", b)
	}
	if b := BaselineFor([]*Run{broken, fixed}, "t1", KindTest); b == nil || b.ID != "2" {
		t.Errorf("the newest good baseline wins: %+v", b)
	}
	if b := BaselineFor([]*Run{mk("3", 1, 1)}, "t1", KindTest); b == nil {
		t.Error("a one-test suite whose test fails is still a baseline (a bug's repro)")
	}
	if !brokenBaseline([]*Run{broken}, "t1", KindTest) || brokenBaseline([]*Run{broken, fixed}, "t1", KindTest) || brokenBaseline(nil, "t1", KindTest) {
		t.Error("brokenBaseline: only when every baseline failed every test")
	}
	lint := &Run{ID: "4", TaskID: "t1", Kind: KindLint, Baseline: true, Finished: Now(), PrintBefore: "p", PrintAfter: "p", Ran: 1, Exit: 1, Problems: 3}
	if b := BaselineFor([]*Run{lint}, "t1", KindLint); b == nil {
		t.Error("lint baselines aren't judged by test counts")
	}
}

// --- N7: a case's test ran and passed ---

func TestCaseTestsSeenInOutput(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module example.com/shop\n\ngo 1.22\n")
	write(t, filepath.Join(root, "backend", "tests", "test_refund.py"), "")
	tests := []string{"src/orders/refund.test.ts", "backend/tests/test_refund.py", "internal/orders/refund_test.go", "src/tax.test.ts", "lib/x_test.go"}
	out := "PASS src/orders/refund.test.ts\ntests/test_refund.py ....\nok  \texample.com/shop/internal/orders\t0.2s\nPASS src/other_tax.test.ts\n"
	got := strings.Join(CaseTestsSeen(root, out, tests), ",")
	if got != "src/orders/refund.test.ts,backend/tests/test_refund.py,internal/orders/refund_test.go" {
		t.Errorf("seen: %s", got)
	}
}

func TestCaseTestMustHaveRun(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a.test.ts"), "")
	s := &Setup{}
	s.Commands.Test, s.Commands.TestRelated = "npm test", "npx jest {files}"
	tk := &Task{ID: "t1", Root: root, CaseTests: []string{"a.test.ts", "./missing.test.ts"}, CasesStored: true}
	run := func(kind, cmd string, failed, skipped int, files, seen []string) *Run {
		return &Run{TaskID: "t1", Kind: kind, Command: cmd, Finished: Now(), PrintBefore: "fp", PrintAfter: "fp", Ran: 5, Failed: failed, Skipped: skipped, Files: files, Seen: seen}
	}
	cases := []struct {
		name string
		runs []*Run
		weak string
		want string
	}{
		{"no run", nil, "", "a.test.ts: no passing run"},
		{"suite didn't name it", []*Run{run(KindTest, "npm test", 0, 0, nil, nil)}, "", "no passing run"},
		{"suite named it", []*Run{run(KindTest, "npm test", 0, 0, nil, []string{"a.test.ts"})}, "", ""},
		{"related was given it", []*Run{run(KindRelated, "npx jest 'a.test.ts'", 0, 0, []string{"a.test.ts"}, nil)}, "", ""},
		{"a failing run proves nothing", []*Run{run(KindTest, "npm test", 1, 0, nil, []string{"a.test.ts"})}, "", "no passing run"},
		{"another command proves nothing", []*Run{run(KindTest, "npm test -- --bail", 0, 0, nil, []string{"a.test.ts"})}, "", "no passing run"},
		{"skipped tests", []*Run{run(KindTest, "npm test", 0, 2, nil, []string{"a.test.ts"})}, "", "skipped 2 more test(s)"},
		{"skipped, listed", []*Run{run(KindTest, "npm test", 0, 2, nil, []string{"a.test.ts"})}, "## Proof\n- Weakened tests: a.test.ts has one test skipped for ticket 9\n", ""},
		{"skipped, then a clean related run", []*Run{run(KindTest, "npm test", 0, 2, nil, []string{"a.test.ts"}), run(KindRelated, "npx jest a.test.ts", 0, 0, []string{"a.test.ts"}, nil)}, "", ""},
	}
	for _, c := range cases {
		m := strings.Join(caseRunMissing(tk, s, "fp", c.runs, c.weak), "; ")
		if !strings.Contains(m, "missing.test.ts doesn't exist yet") {
			t.Errorf("%s: a missing case test file is still named: %s", c.name, m)
		}
		m = strings.ReplaceAll(m, "case test missing.test.ts doesn't exist yet", "")
		if (c.want == "") != (strings.Trim(m, "; ") == "") || !strings.Contains(m, c.want) {
			t.Errorf("%s: want %q, got %q", c.name, c.want, m)
		}
	}
}

func TestRelatedRunRecordsFiles(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	r, err := ExecuteRelated(h.repo, h.task(), "./test.sh", []string{"./src/a_test.go", " test.sh "}, nil, io.Discard)
	must(t, err)
	if strings.Join(r.Files, ",") != "src/a_test.go,test.sh" || strings.Join(r.Seen, ",") != "test.sh" || r.Kind != KindRelated {
		t.Errorf("related run: files %v seen %v kind %s", r.Files, r.Seen, r.Kind)
	}
}

// --- P4: e2e when the change reaches the frontend ---

func TestE2ERequiredWhenItReachesTheFrontend(t *testing.T) {
	h := newHarness(t)
	s := &Setup{}
	s.Commands.E2E = "npx playwright test"
	tk := &Task{ID: "t1", Root: h.repo}
	ok := &Run{TaskID: "t1", Kind: KindE2E, Command: "npx playwright test", Finished: Now(), PrintBefore: "fp", PrintAfter: "fp", Ran: 4}
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); m != nil {
		t.Errorf("a backend change with no API change: %v", m)
	}
	if m := e2eMissing(tk, s, "fp", nil, []string{"web/src/Refund.tsx"}); len(m) == 0 || !strings.Contains(m[0], "frontend files") {
		t.Errorf("a frontend change needs e2e: %v", m)
	}
	if m := e2eMissing(tk, s, "fp", nil, []string{"web/src/Refund.test.tsx"}); m != nil {
		t.Errorf("a frontend test alone isn't a frontend change: %v", m)
	}
	if m := e2eMissing(tk, s, "fp", []*Run{ok}, []string{"web/src/Refund.tsx"}); m != nil {
		t.Errorf("a passing e2e run on this code: %v", m)
	}
	// An API change: needs e2e only when the project has a frontend.
	tk.ProofKinds = []string{"api"}
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); m != nil {
		t.Errorf("an API change in a backend-only repo: %v", m)
	}
	s.FrontendRepo = "../shop-web"
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); len(m) == 0 || !strings.Contains(m[0], "API") {
		t.Errorf("an API change with a frontend repo: %v", m)
	}
	s.FrontendRepo = ""
	write(t, filepath.Join(h.repo, "web", "App.vue"), "<template/>")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "web")
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); len(m) == 0 {
		t.Error("an API change in a repo with frontend files needs e2e")
	}
	tk.Waivers = []Waiver{{Check: "e2e", Print: "fp"}}
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); m != nil {
		t.Errorf("waived: %v", m)
	}
	tk.Waivers = []Waiver{{Check: "e2e", Print: "older"}}
	if m := e2eMissing(tk, s, "fp", nil, []string{"src/a.go"}); len(m) == 0 {
		t.Error("a waiver for older code lapses")
	}
	s.Commands.E2E = ""
	if m := e2eMissing(tk, s, "fp", nil, []string{"web/src/Refund.tsx"}); m != nil {
		t.Errorf("no e2e command saved: %v", m)
	}
}

// --- R5: bad news first in the closing message ---

func TestClosingMessageOpensWithForYou(t *testing.T) {
	withItems := "## For you\n- src/auth/login.go: not changed, needs your OK\n- Commit? fix: A. You push.\n"
	nothing := "## For you\n- nothing\n- Commit? fix: A. You push.\n"
	cases := []struct {
		msg, report string
		pass        bool
	}{
		{"Fixed A, proof passes.\nFor you: login.go needs your OK. Commit?", withItems, false},
		{"**For you:** login.go needs your OK.\nFixed A, proof passes. Commit?", withItems, true},
		{"\n\n> For you: login.go needs your OK.\nFixed A. Commit?", withItems, true},
		{"Fixed A, proof passes.\nFor you: nothing. Commit?", nothing, true},
		{"Fixed A, proof passes. Commit?", nothing, false},
	}
	for _, c := range cases {
		if got := finalMessageMissing(c.msg, c.report) == ""; got != c.pass {
			t.Errorf("message %q with report %q: pass = %v, want %v", c.msg, c.report, got, c.pass)
		}
	}
}

// --- R4 and N8: the user's size and files that decide what runs ---

func TestGateFiles(t *testing.T) {
	gates := []string{".github/workflows/ci.yml", ".gitlab-ci.yml", ".husky/pre-commit", ".pre-commit-config.yaml", "jest.config.ts", "vitest.config.mts", "eslint.config.js", ".eslintrc.json", "tsconfig.json", "tsconfig.build.json", ".golangci.yml", "pytest.ini", "phpunit.xml.dist", "CLAUDE.md", "AGENTS.md", "web/CLAUDE.md", ".claude/settings.json", "skills/task/SKILL.md", "agents/code-reviewer.md", "hooks/hooks.json", ".plumbline.json"}
	plain := []string{"src/hooks/useCart.ts", "src/agents/list.ts", "README.md", "docs/skills/SKILL.md.bak", "src/config.ts", "jest.setup.ts", "src/claude.ts"}
	for _, p := range gates {
		if !GateFile(p) {
			t.Errorf("%s decides what runs", p)
		}
	}
	for _, p := range plain {
		if GateFile(p) {
			t.Errorf("%s doesn't decide what runs", p)
		}
	}
	if got := strings.Join(ReviewersFor("small", "", []string{"jest.config.ts"}), ","); got != "plan-reviewer,code-reviewer" {
		t.Errorf("a small change to test config gets the full review: %s", got)
	}
}

func expand(h *harness, name, args string) Output {
	return UserPromptExpansion(&HookInput{SessionID: h.sid, Cwd: h.repo, CommandName: "/plumbline:" + name, CommandArgs: args})
}

func TestUserKeepsARiskyChangeSmall(t *testing.T) {
	h := groundedTask(t, "small")
	// Specialists the files call for still review a small task.
	plan := strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- src/auth/login.tsx (new)\n", 1) + "## Team\n- lead, code-reviewer, security-reviewer, ux-reviewer\n"
	h.docs(DocPlan, plan)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "isn't small")
	// SV1: the refusal doesn't point Claude at the user's override.
	if _, r := decision(h.pre("AskUserQuestion", checkpointQ())); strings.Contains(r, "by typing /plumbline:size small") || !strings.Contains(r, "Don't suggest") {
		t.Fatalf("the refusal suggests the override: %s", r)
	}

	if o := expand(h, "size", "normal"); !strings.Contains(fmtOut(o), "only /plumbline:size small") {
		t.Errorf("only small is the user's: %v", o)
	}
	if o := expand(h, "size", "small just the button label"); !strings.Contains(fmtOut(o), "is small, set by the user") {
		t.Fatalf("size small: %v", o)
	}
	if tk := h.task(); tk.Size != "small" || !tk.SizeByUser || tk.SizeWhy != "just the button label" {
		t.Fatalf("task after /plumbline:size small: %+v", tk)
	}
	// Risky files no longer make it normal, but a gate file still does.
	h.docs(DocPlan, strings.Replace(plan, "- src/auth/login.tsx (new)\n", "- src/auth/login.tsx (new)\n- .github/workflows/ci.yml (new)\n", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "decide which checks run")
	h.docs(DocPlan, plan)
	h.approve()
	if o := expand(h, "size", "small"); !strings.Contains(fmtOut(o), "fixed once the plan is approved") {
		t.Errorf("after approval: %v", o)
	}
}

// --- N12: skills fit what Claude Code keeps after compaction ---

func TestSkillsFitAfterCompaction(t *testing.T) {
	// Claude Code re-attaches an invoked skill after compaction, capped at
	// 5,000 tokens, and says to keep SKILL.md under 500 lines
	// (code.claude.com/docs/en/skills.md and context-window.md, checked
	// 2026-11-18). At a cautious 3.5 characters a token, 5,000 tokens is
	// 17,500 characters.
	files, _ := filepath.Glob(filepath.Join("..", "..", "skills", "*", "SKILL.md"))
	if len(files) < 5 {
		t.Fatalf("skills not found: %v", files)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		must(t, err)
		if n := len([]rune(string(b))); n > 17500 {
			t.Errorf("%s is %d characters; after compaction Claude Code keeps about 17,500: move detail to a file the skill links", f, n)
		}
		if n := strings.Count(string(b), "\n"); n >= 500 {
			t.Errorf("%s is %d lines; keep SKILL.md under 500", f, n)
		}
	}
}

// --- N13: test runs point at throwaway targets ---

func clearServiceEnv(t *testing.T) {
	for _, e := range os.Environ() {
		k, _, _ := strings.Cut(e, "=")
		if reServiceVar.MatchString(k) {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
	}
}

func TestServiceHosts(t *testing.T) {
	cases := []struct{ v, hosts, db string }{
		{"postgres://u:p@db.example.com:5432/shop", "db.example.com", "shop:5432"},
		{"postgres://localhost/shop_dev", "localhost", "shop_dev:"},
		{"postgres:///shop?host=/var/run/postgresql", "/var/run/postgresql", "shop:"},
		{"postgres:///shop", "localhost", "shop:"},
		{"user:pw@tcp(10.0.0.5:3306)/shop?parseTime=true", "10.0.0.5", "shop"},
		{"user:pw@unix(/tmp/mysql.sock)/shop", "localhost", "shop"},
		{"user:pw@/shop", "localhost", "shop"},
		{"host=pg.internal dbname=shop user=x", "pg.internal", "shop"},
		{"file:./dev.db", "localhost", "file:./dev.db"},
		{"db", "db", ""},
		{"localhost:5432", "localhost", ""},
		{"mongodb://localhost:27017,localhost:27018/shop?replicaSet=rs", "localhost,localhost", "shop:27017"},
		{"mongodb://a.example.com:27017,localhost/shop", "a.example.com,localhost", "shop:27017"},
		{"postgres://${U}:${P}@prod.example.com/app", "prod.example.com", "app:"}, // variables in the password don't hide the host
		{"[::1]:5432", "::1", ""},
	}
	for _, c := range cases {
		h, db, ok := serviceHosts(c.v)
		if !ok || strings.Join(h, ",") != c.hosts || db != c.db {
			t.Errorf("%q: got %q %q %v", c.v, h, db, ok)
		}
	}
	if _, _, ok := serviceHosts("postgres://${DB_HOST}/x"); ok {
		t.Error("a host that is only a variable isn't known")
	}
	for h, local := range map[string]bool{"localhost": true, "127.0.0.2": true, "::1": true, "db": true, "postgres": true, "/tmp/sock": true, "db.example.com": false, "10.0.0.5": false, "192.168.1.9": false, "api.localhost": true} {
		if serviceLocal(h) != local {
			t.Errorf("serviceLocal(%q) = %v", h, !local)
		}
	}
}

func TestCheckTestTargets(t *testing.T) {
	clearServiceEnv(t)
	root := t.TempDir()
	env := func(name, body string) { write(t, filepath.Join(root, name), body) }
	check := func() TestTargets { return CheckTestTargets(root, nil, nil) }

	env(".env", "# dev\nexport DATABASE_URL=\"postgres://u:secret@db.prod.example.com/shop\"\nREDIS_URL=redis://localhost:6379\nAPI_KEY=abc\n")
	if tt := check(); strings.Join(tt.Remote, ",") != "DATABASE_URL (from .env)" {
		t.Errorf("remote database: %+v", tt)
	}
	for _, r := range check().Remote {
		if strings.Contains(r, "secret") || strings.Contains(r, "prod.example") {
			t.Errorf("a value leaked: %s", r)
		}
	}
	// The test overlay points at a local one: fine.
	env(".env.test", "DATABASE_URL=postgres://localhost/shop_test\n")
	if tt := check(); len(tt.Remote)+len(tt.DevDB) != 0 {
		t.Errorf("test overlay is local: %+v", tt)
	}
	// No overlay, and dev is local: the tests share the dev database.
	must(t, os.Remove(filepath.Join(root, ".env.test")))
	env(".env", "DATABASE_URL=postgres://localhost/shop_dev\n")
	if tt := check(); strings.Join(tt.DevDB, ",") != "DATABASE_URL (from .env)" || len(tt.Remote) != 0 {
		t.Errorf("shared dev database: %+v", tt)
	}
	// The command's own value wins, and it's the user's explicit choice.
	if tt := CheckTestTargets(root, []string{"DATABASE_URL=postgres://localhost/shop_dev"}, nil); len(tt.DevDB) != 0 {
		t.Errorf("set on the command: %+v", tt)
	}
	// Django makes its own test database.
	env("manage.py", "")
	if tt := check(); len(tt.DevDB) != 0 {
		t.Errorf("framework switches the database: %+v", tt)
	}
	must(t, os.Remove(filepath.Join(root, "manage.py")))
	// The session's environment comes before the files.
	t.Setenv("DATABASE_URL", "postgres://u@rds.amazonaws.com/shop")
	if tt := check(); strings.Join(tt.Remote, ",") != "DATABASE_URL (from the session's environment)" {
		t.Errorf("session env: %+v", tt)
	}
	// The user listed it: the tests never connect to it.
	s := &Setup{TestTargetsOK: []string{"DATABASE_URL"}}
	if tt := CheckTestTargets(root, nil, s); len(tt.Remote) != 0 {
		t.Errorf("listed in test_targets_ok: %+v", tt)
	}
	t.Setenv("DATABASE_URL", "")
	// An empty value is no value: the next source is read.
	if tt := check(); strings.Join(tt.DevDB, ",") != "DATABASE_URL (from .env)" {
		t.Errorf("empty session value: %+v", tt)
	}
}

func TestTestRunTargetsGate(t *testing.T) {
	clearServiceEnv(t)
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://u@db.example.com/shop\n")
	h.expect(h.bash("plumbline run test"), "deny", "isn't on this laptop (DATABASE_URL (from .env))")
	h.expect(h.bash("plumbline run lint"), "", "") // lint doesn't connect
	h.expect(h.bash("prisma migrate deploy"), "ask", "migration would run against a database")

	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://localhost/shop_dev\n")
	h.expect(h.bash("plumbline run test"), "ask", "same database as development")
	// Allowed once (the command ran): not asked again in this task.
	h.post("Bash", map[string]any{"command": "plumbline run test"}, map[string]any{"stdout": "ok"})
	if !h.task().DevDBOK {
		t.Fatal("the allowed run should be remembered")
	}
	h.expect(h.bash("plumbline run test"), "", "")
	// Still refused off the laptop.
	write(t, filepath.Join(h.repo, ".env.test"), "DATABASE_URL=postgres://u@db.example.com/shop_test\n")
	h.expect(h.bash("plumbline run related --files src/a.go"), "deny", "isn't on this laptop")
}

func TestUnconfirmedSetupKeepsOnlyConfirmedTestTargets(t *testing.T) {
	h := newHarness(t)
	h.setup()
	write(t, filepath.Join(h.repo, ".plumbline.json"), `{"version":1,"kind":"personal","commands":{"test":"./test.sh"},"test_targets_ok":["DATABASE_URL"]}`)
	s, ok, err := LoadSetup(h.repo)
	must(t, err)
	if ok || len(s.TestTargetsOK) != 0 {
		t.Errorf("an edit made outside the confirm prompt can't add test targets: ok=%v %v", ok, s.TestTargetsOK)
	}
}

// --- N21: package upgrades ---

func TestUpgradePlan(t *testing.T) {
	yes := []PlanCheck{{Files: []string{"package.json"}}, {Files: []string{"./api/go.mod"}}, {Files: []string{"requirements-dev.txt"}}, {Approved: []string{"npm install zod@3"}}, {Approved: []string{"go get example.com/x@v2"}}, {Approved: []string{"bundle update rails"}}}
	no := []PlanCheck{{Files: []string{"src/package.ts"}}, {Approved: []string{"npx prisma generate"}}, {Approved: []string{"brew install jq"}}}
	for _, pc := range yes {
		if !upgradePlan(pc, "") {
			t.Errorf("changes packages: %+v", pc)
		}
	}
	for _, pc := range no {
		if upgradePlan(pc, "") {
			t.Errorf("doesn't change packages: %+v", pc)
		}
	}
}

func TestUpgradeNamesEditedTests(t *testing.T) {
	changes := []fileDiff{file("package.json", false, "+  \"zod\": \"^3\""), file("src/a.test.ts", false, "+  expect(x).toBe(1)"), file("src/a.ts", false, "+x")}
	tk := &Task{Upgrade: true}
	if m := upgradeMissing(tk, nil, changes, "## What changed\n- package.json: zod 3"); len(m) == 0 || !strings.Contains(m[0], "src/a.test.ts") || strings.Contains(m[0], "src/a.ts;") {
		t.Errorf("edited test not named: %v", m)
	}
	if m := upgradeMissing(tk, nil, changes, "## What changed\n- src/a.test.ts: the error text changed in zod 3"); m != nil {
		t.Errorf("named: %v", m)
	}
	tk.Upgrade = false
	if m := upgradeMissing(tk, nil, changes, ""); m != nil {
		t.Errorf("not an upgrade: %v", m)
	}
}

func TestUpgradeApprovalIsRecorded(t *testing.T) {
	h := groundedTask(t, "normal")
	h.docs(DocPlan, strings.Replace(goodPlan, "- none\n", "- `npm install zod`\n", 1)+"## Challenge note\n- nothing changed\n")
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "plumbline run test")
	// Even a run where every test failed (the database was down) records
	// how the checks stood, so the checkpoint doesn't ask for it forever.
	write(t, filepath.Join(h.repo, "coverage", "allfail.sh"), "#!/bin/sh\necho '== 3 failed in 0.1s =='\nexit 1\n")
	must(t, os.Chmod(filepath.Join(h.repo, "coverage", "allfail.sh"), 0o755))
	_, err := Execute(h.repo, h.task(), KindTest, "", "./coverage/allfail.sh", nil, io.Discard)
	must(t, err)
	h.approve()
	if !h.task().Upgrade {
		t.Error("the approved plan changes packages")
	}
}

// --- P5: token counts from the transcripts ---

func TestTranscriptTokens(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "s.jsonl")
	since := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	lines := []string{
		`{"type":"user","timestamp":"2026-11-18T10:00:01Z","message":{"content":"hi"}}`,
		// Before the task: not counted.
		`{"type":"assistant","timestamp":"2026-11-18T09:59:00Z","message":{"id":"m0","usage":{"input_tokens":500,"output_tokens":500}}}`,
		// One message over two lines (two content blocks): counted once.
		`{"type":"assistant","timestamp":"2026-11-18T10:00:02Z","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","timestamp":"2026-11-18T10:00:02.5Z","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":7,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
		`{"type":"assistant","timestamp":"2026-11-18T10:01:00Z","requestId":"r2","message":{"usage":{"input_tokens":3,"output_tokens":4}}}`,
		`not json`,
	}
	write(t, p, strings.Join(lines, "\n")+"\n")
	got := TranscriptTokens(p, since)
	if got.Input != 13 || got.Output != 11 || got.CacheRead != 100 || got.CacheWrite != 20 || got.Messages != 2 || got.Unknown {
		t.Errorf("counted: %+v", got)
	}
	if got.Total() != 144 || !strings.Contains(got.String(), "144 tokens") {
		t.Errorf("total: %d %s", got.Total(), got)
	}
	if c := TranscriptTokens(filepath.Join(dir, "none.jsonl"), since); !c.Unknown {
		t.Error("a missing transcript is unknown")
	}
	// The format moved: the task's assistant lines carry no usage.
	write(t, p, `{"type":"assistant","timestamp":"2026-11-18T10:00:02Z","message":{"id":"m1","tokens":9}}`+"\n")
	if c := TranscriptTokens(p, since); !c.Unknown || c.Total() != 0 {
		t.Errorf("no usage: %+v", c)
	}
	write(t, p, `{"type":"assistant","timestamp":"yesterday","message":{"id":"m1","usage":{"input_tokens":1}}}`+"\n")
	if c := TranscriptTokens(p, since); !c.Unknown {
		t.Errorf("unreadable time: %+v", c)
	}
	if groupDigits(1234567) != "1,234,567" || groupDigits(12) != "12" || groupDigits(-1000) != "-1,000" {
		t.Error("groupDigits")
	}
}

func TestStopRecordsTokensOnce(t *testing.T) {
	h := groundedTask(t, "small")
	p := filepath.Join(t.TempDir(), "s.jsonl")
	at := Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	write(t, p, `{"type":"assistant","timestamp":"`+at+`","message":{"id":"m1","usage":{"input_tokens":10,"output_tokens":5}}}`+"\n")
	for i := 0; i < 2; i++ { // every stop counts the whole transcript again: no double count
		Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, TranscriptPath: p})
	}
	tc, ok := h.task().TokenTotal()
	if !ok || tc.Total() != 15 || tc.Messages != 1 {
		t.Fatalf("tokens after two stops: %+v %v", tc, ok)
	}
	LogTokens(h.task(), "proj")
	if st := Stats(1); !strings.Contains(st, "Tokens: 15 tokens") || !strings.Contains(st, "1 task(s)") {
		t.Errorf("stats: %s", st)
	}
}

// --- regressions from the fresh-eyes review ---

func TestCaseTestsWithoutAFilesCommand(t *testing.T) {
	// A runner that never prints file names and no related command that
	// takes {files}: the only way to run the case tests is the suite, so a
	// suite that passes against the baseline proves them (no deadlock).
	root := t.TempDir()
	write(t, filepath.Join(root, "spec", "a_spec.rb"), "")
	s := &Setup{}
	s.Commands.Test = "bundle exec rspec"
	tk := &Task{ID: "t1", Root: root, CaseTests: []string{"spec/a_spec.rb"}, CasesStored: true}
	base := &Run{TaskID: "t1", Kind: KindTest, Command: "bundle exec rspec", Baseline: true, Finished: Now(), PrintBefore: "old", PrintAfter: "old", Ran: 10, Failed: 2, FailedIDs: []string{"Old one", "Old two"}, Skipped: 3}
	suite := &Run{TaskID: "t1", Kind: KindTest, Command: "bundle exec rspec", Finished: Now(), PrintBefore: "fp", PrintAfter: "fp", Ran: 11, Failed: 2, Exit: 1, FailedIDs: []string{"Old one", "Old two"}, Skipped: 3}
	if m := caseRunMissing(tk, s, "fp", []*Run{base, suite}, ""); len(m) != 0 {
		t.Errorf("a suite that passes against the baseline, with only the old skips: %v", m)
	}
	suite.FailedIDs = []string{"Old one", "New one"}
	if m := caseRunMissing(tk, s, "fp", []*Run{base, suite}, ""); len(m) == 0 || !strings.Contains(m[0], "plumbline run test") {
		t.Errorf("a new failure: %v", m)
	}
	// With a {files} command, skips already in the baseline don't count
	// against the case test either.
	s.Commands.TestRelated = "bundle exec rspec {files}"
	clean := &Run{TaskID: "t1", Kind: KindTest, Command: "bundle exec rspec", Finished: Now(), PrintBefore: "fp", PrintAfter: "fp", Ran: 11, Skipped: 3, Seen: []string{"spec/a_spec.rb"}}
	if m := caseRunMissing(tk, s, "fp", []*Run{base, clean}, ""); len(m) != 0 {
		t.Errorf("skips that were there before: %v", m)
	}
	clean.Skipped = 4
	if m := caseRunMissing(tk, s, "fp", []*Run{base, clean}, ""); len(m) == 0 || !strings.Contains(m[0], "skipped 1 more") {
		t.Errorf("one new skip: %v", m)
	}
}

func TestReplanIntoAPackagePlan(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approveOnly() // no runs before the change
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	tk := h.task()
	tk.Phase = PhasePlanning // plumbline task replan
	must(t, SaveTask(tk))
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- package.json\n", 1)+"## Team\n- lead, code-reviewer\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "plumbline run test")
	r, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	if r.Baseline {
		t.Fatal("a run after edits isn't a baseline")
	}
	// The code changed already, so the newest record of the checks is enough.
	if d, reason := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" && strings.Contains(reason, "packages") {
		t.Errorf("after a replan, a finished run counts: %s", reason)
	}
}

func TestGoBaselineWithSubtests(t *testing.T) {
	// Without -v, three packages pass and one fails with four failing
	// subtests: one failing package of four, not a broken environment.
	out := "ok  \tx/a\t0.1s\nok  \tx/b\t0.1s\nok  \tx/c\t0.1s\n--- FAIL: TestX (0.00s)\n    --- FAIL: TestX/one (0.00s)\n    --- FAIL: TestX/two (0.00s)\n    --- FAIL: TestX/three (0.00s)\nFAIL\nFAIL\tx/d\t0.2s\n"
	c := ParseTestOutput(out, "")
	if c.Ran != 4 || c.Failed != 1 || len(c.IDs) != 4 {
		t.Fatalf("go without -v: %+v", c)
	}
	r := &Run{TaskID: "t1", Kind: KindTest, Baseline: true, Finished: Now(), PrintBefore: "p", PrintAfter: "p", Ran: c.Ran, Failed: c.Failed, FailedIDs: c.IDs}
	if BaselineFor([]*Run{r}, "t1", KindTest) == nil {
		t.Error("one failing package isn't a broken environment")
	}
}

func TestBaselineFewestFailuresWins(t *testing.T) {
	mk := func(id string, failed int) *Run {
		return &Run{ID: id, TaskID: "t1", Kind: KindTest, Baseline: true, Finished: Now(), PrintBefore: "p", PrintAfter: "p", Ran: 40, Failed: failed}
	}
	// A later run with half the tests failing (a wrong DATABASE_URL on the
	// command) doesn't excuse those failures later.
	if b := BaselineFor([]*Run{mk("good", 2), mk("worse", 20)}, "t1", KindTest); b.ID != "good" {
		t.Errorf("fewest failures: %s", b.ID)
	}
	if b := BaselineFor([]*Run{mk("first", 2), mk("second", 2)}, "t1", KindTest); b.ID != "second" {
		t.Errorf("newest on a tie: %s", b.ID)
	}
}

func TestUnnamedFailuresMayNotGrow(t *testing.T) {
	// The named old failure is still there, and a test file that didn't load
	// (no test name) is new: not "only failures that were there before".
	base := &Run{Kind: KindTest, Finished: Now(), PrintBefore: "a", PrintAfter: "a", Ran: 9, Failed: 1, FailedIDs: []string{"a.test.ts > old"}}
	now := &Run{Kind: KindTest, Finished: Now(), PrintBefore: "b", PrintAfter: "b", Ran: 9, Failed: 2, Exit: 1, FailedIDs: []string{"a.test.ts > old"}}
	if v := Judge(now, base); v.OK {
		t.Errorf("an unnamed new failure: %+v", v)
	}
	// Vitest's whole-file failure now has a name of its own.
	c := ParseTestOutput(" FAIL  src/b.test.ts [ src/b.test.ts ]\nError: Cannot find module\n Test Files  1 failed (1)\n      Tests  no tests\n      Tests  5 passed (5)\n", "")
	if !contains(c.IDs, "src/b.test.ts") {
		t.Errorf("file-level vitest failure: %+v", c)
	}
}

func TestCypressOutput(t *testing.T) {
	out := "  │ Tests:        4                                                                                │\n  │ Passing:      3                                                                                │\n  │ Failing:      1                                                                                │\n  │ Pending:      0                                                                                │\n  │ Skipped:      0                                                                                │\n" +
		"  │ Tests:        2                                                                                │\n  │ Passing:      1                                                                                │\n  │ Failing:      0                                                                                │\n  │ Pending:      1                                                                                │\n"
	if c := ParseTestOutput(out, ""); c.Ran != 6 || c.Failed != 1 || c.Skipped != 1 {
		t.Errorf("cypress: %+v", c)
	}
}

func TestWeakenedReviewFindings(t *testing.T) {
	cases := []struct {
		f    fileDiff
		want string
	}{
		{file("src/a.test.ts", false, "-  expect(res.status).toBe(200)", "+  // expect(res.status).toBe(200)"), "commented out"},
		{file("src/a.test.ts", false, "+  // the next line checks the total"), ""},
		{file("package.json", false, "+    \"test\": \"jest || true\","), "allowed to pass"},
		{file("Makefile", false, "+\tgo test ./... || true"), "allowed to pass"},
		{file("vitest.config.ts", false, "-        branches: 80,", "+        branches: 10,"), "threshold is lowered"},
	}
	for _, c := range cases {
		var text []string
		for _, w := range WeakenedTests([]fileDiff{c.f}) {
			text = append(text, w.String())
		}
		all := strings.Join(text, "; ")
		if (c.want == "") != (all == "") || !strings.Contains(all, c.want) {
			t.Errorf("%s %v: want %q, got %q", c.f.Path, c.f.Lines, c.want, all)
		}
	}
}

func TestWeakenedLineFormats(t *testing.T) {
	changes := []fileDiff{file("src/a.test.ts", false, "+  it.skip('x', () => {")}
	ok := []string{
		"## Proof\n- Weakened tests:\n- src/a.test.ts: waits on ticket 12\n- Full suite: pass\n",
		"## Proof\n**Weakened tests:**\n\n- src/a.test.ts: waits on ticket 12\n",
		"## Proof\n- Full suite: pass\n### Weakened tests\n- src/a.test.ts: waits on ticket 12\n## Review\n",
	}
	for _, r := range ok {
		if m := weakenedMissing(changes, r, nil); len(m) != 0 {
			t.Errorf("report %q lists the file: %v", r, m)
		}
	}
	if m := weakenedMissing(changes, "## Proof\n- Weakened tests: none\n- Full suite: pass\n## Review\n- src/a.test.ts looked fine\n", nil); len(m) == 0 {
		t.Error("named outside the weakened tests text")
	}
}

func TestMovedTestIsNotDeleted(t *testing.T) {
	h := newHarness(t)
	h.setup()
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(\"x\")\n\t}\n}\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "test")
	tk := h.startTask("small")
	tk.StartCommit = HeadCommit(h.repo)
	must(t, SaveTask(tk))
	run(t, h.repo, "git", "mv", "src/a_test.go", "src/moved_test.go")
	changes, err := taskChanges(h.task(), nil)
	must(t, err)
	if w := WeakenedTests(changes); len(w) != 0 {
		t.Errorf("a moved test file: %v", w)
	}
	// A start commit git can't find: the check says so instead of passing.
	tk = h.task()
	tk.StartCommit = "0123456789abcdef0123456789abcdef01234567"
	if _, err := taskChanges(tk, nil); err == nil {
		t.Error("an unreadable diff should be an error")
	}
}

func TestTestTargetsOnTheCommandLine(t *testing.T) {
	clearServiceEnv(t)
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://localhost/shop_dev\n")
	write(t, filepath.Join(h.repo, ".env.test"), "DATABASE_URL=postgres://localhost/shop_test\n")
	h.expect(h.bash("plumbline run test"), "", "")
	// Exported or loaded earlier on the line: still read.
	h.expect(h.bash("export DATABASE_URL=postgres://u@db.example.com/shop; plumbline run test"), "deny", "isn't on this laptop")
	write(t, filepath.Join(h.repo, ".env.prod"), "DATABASE_URL=postgres://u@db.example.com/shop\n")
	h.expect(h.bash("source .env.prod && plumbline run test"), "deny", "isn't on this laptop")
	h.expect(h.bash("source .venv/bin/activate && plumbline run test"), "", "") // not an env file: nothing to read
	// A test runner typed directly is checked too.
	h.expect(h.bash("DATABASE_URL=postgres://u@db.example.com/shop go test ./..."), "deny", "isn't on this laptop")
	// The folder the saved command runs in has its own env files.
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Test = "cd backend && ./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	must(t, ConfirmSetup(h.repo))
	write(t, filepath.Join(h.repo, "backend", ".env.test"), "REDIS_URL=redis://cache.example.com:6379\n")
	h.expect(h.bash("plumbline run test"), "deny", "REDIS_URL (from backend/.env.test)")
}

func TestMigrationReadsDevSettings(t *testing.T) {
	clearServiceEnv(t)
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	// Migrations read the development settings, not the test overlay.
	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://u@db.example.com/shop\n")
	write(t, filepath.Join(h.repo, ".env.test"), "DATABASE_URL=postgres://localhost/shop_test\n")
	h.expect(h.bash("prisma migrate dev"), "ask", "migration would run against a database")
	h.expect(h.bash("npm run db:migrate"), "ask", "migration would run against a database")
	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://localhost/shop_dev\n")
	write(t, filepath.Join(h.repo, ".env.test"), "DATABASE_URL=postgres://u@db.example.com/shop_test\n")
	if _, reason := decision(h.bash("prisma migrate dev")); strings.Contains(reason, "migration would run") {
		t.Errorf("dev is local: %s", reason)
	}
}

func TestMarkDevDBAllowedFromTheRun(t *testing.T) {
	clearServiceEnv(t)
	h := groundedTask(t, "small")
	write(t, filepath.Join(h.repo, ".env"), "DATABASE_URL=postgres://localhost/shop_dev\n")
	MarkDevDBAllowed(h.repo, h.task().ID, nil)
	if !h.task().DevDBOK {
		t.Error("a run on the dev database records the user's OK")
	}
}

func TestCancelLogsTokensOnce(t *testing.T) {
	h := groundedTask(t, "small")
	tk := h.task()
	tk.Tokens = map[string]TokenCount{"main:x": {Input: 7}}
	must(t, SaveTask(tk))
	id := tk.ID
	expand(h, "cancel", id)
	expand(h, "cancel", id)
	if st := Stats(1); !strings.Contains(st, "across 1 task(s)") {
		t.Errorf("cancelled twice, logged once: %s", st)
	}
}
