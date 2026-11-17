package core

import (
	"strings"
	"testing"
)

func TestParseTests(t *testing.T) {
	cases := []struct {
		name, out   string
		ran, failed int
		ids         int
	}{
		{"jest pass", "Tests:       12 passed, 12 total\nTime: 1s", 12, 0, 0},
		{"jest fail", "  ● Orders › refunds twice\n\nTests:       1 failed, 11 passed, 12 total", 12, 1, 1},
		{"vitest", " Test Files  2 passed (2)\n      Tests  1 failed | 9 passed (10)", 10, 1, 0},
		{"pytest", "FAILED tests/test_a.py::test_x - assert\n===== 1 failed, 5 passed in 0.31s =====", 6, 1, 1},
		{"pytest none", "===== no tests ran in 0.01s =====", 0, 0, 0},
		{"go", "=== RUN   TestA\n--- PASS: TestA (0.00s)\nok  \texample.com/a\t0.01s\nok  \texample.com/b\t0.02s", 2, 0, 0},
		{"go fail", "--- FAIL: TestB (0.00s)\nFAIL\nFAIL\texample.com/b\t0.02s", 1, 1, 1},
		{"go no tests", "?   \texample.com/c\t[no test files]", 0, 0, 0},
		{"mocha", "  3 passing (20ms)\n  1 failing", 4, 1, 0},
		{"rspec", "10 examples, 2 failures", 10, 2, 0},
		{"cargo", "test result: ok. 7 passed; 0 failed; 0 ignored", 7, 0, 0},
		{"phpunit", "OK (15 tests, 30 assertions)", 15, 0, 0},
		{"unknown", "all good!", -1, 0, 0},
	}
	for _, c := range cases {
		ran, failed, ids := ParseTests(c.out, "")
		if ran != c.ran || failed != c.failed || len(ids) != c.ids {
			t.Errorf("%s: got ran=%d failed=%d ids=%v, want %d %d %d", c.name, ran, failed, ids, c.ran, c.failed, c.ids)
		}
	}
	ran, _, _ := ParseTests("custom: 33 checks ok", `(?P<ran>\d+) checks`)
	if ran != 33 {
		t.Errorf("custom pattern: got %d", ran)
	}
}

func TestJudge(t *testing.T) {
	done := func(r *Run) *Run {
		r.Finished = Now()
		r.PrintBefore, r.PrintAfter = "x", "x"
		return r
	}
	if v := Judge(done(&Run{Kind: KindTest, Ran: 0}), nil); v.OK {
		t.Error("0 tests ran must fail")
	}
	if v := Judge(done(&Run{Kind: KindTest, Ran: -1}), nil); v.OK {
		t.Error("unknown output must fail")
	}
	if v := Judge(&Run{Kind: KindTest, Ran: 5, Started: Now(), PrintBefore: "x"}, nil); v.OK {
		t.Error("unfinished run must fail")
	}
	base := done(&Run{Kind: KindTest, Ran: 10, Failed: 1, FailedIDs: []string{"old"}, Exit: 1})
	if v := Judge(done(&Run{Kind: KindTest, Ran: 10, Failed: 1, FailedIDs: []string{"old"}, Exit: 1}), base); !v.OK {
		t.Errorf("baseline failure only should pass: %s", v.Reason)
	}
	if v := Judge(done(&Run{Kind: KindTest, Ran: 10, Failed: 1, FailedIDs: []string{"new"}, Exit: 1}), base); v.OK {
		t.Error("new failure must fail")
	}
	changed := done(&Run{Kind: KindTest, Ran: 3})
	changed.PrintAfter = "y"
	if v := Judge(changed, nil); v.OK {
		t.Error("code changed during the run must fail")
	}
	lb := done(&Run{Kind: KindLint, Exit: 1, Problems: 4})
	if v := Judge(done(&Run{Kind: KindLint, Exit: 1, Problems: 4}), lb); !v.OK {
		t.Error("lint with only old problems should pass")
	}
	if v := Judge(done(&Run{Kind: KindLint, Exit: 1, Problems: 5}), lb); v.OK {
		t.Error("lint with a new problem must fail")
	}
}

func TestSegmentsAndReadOnly(t *testing.T) {
	ro := []string{
		"ls -la", "cat README.md | head -5", "git status && git diff HEAD", "rg foo src/ 2>/dev/null",
		"find . -name '*.go'", "sed -n '1,20p' a.go", "git log --oneline -5", "plumbline status",
		"docker ps", "git branch", "echo hi", "jq .name package.json",
	}
	for _, c := range ro {
		for _, s := range Segments(c) {
			if !ReadOnly(s) {
				t.Errorf("%q segment %v should be read-only", c, s.Words)
			}
		}
	}
	rw := []string{
		"echo x > a.go", "sed -i s/a/b/ a.go", "find . -delete", "git commit -m x", "rm -rf build",
		"npm install lodash", "git branch new-feature", "python -c 'open(\"a\",\"w\")'", "cat a | tee b",
		"ls; touch x", "echo $(rm -rf x)",
	}
	for _, c := range rw {
		all := true
		for _, s := range Segments(c) {
			if !ReadOnly(s) {
				all = false
			}
		}
		if all {
			t.Errorf("%q should not be read-only", c)
		}
	}
	segs := Segments("FOO=1 sudo git -C x push origin main")
	if len(segs) != 1 || segs[0].Name() != "git" {
		t.Fatalf("segments: %+v", segs)
	}
	if sub, _ := GitSub(segs[0]); sub != "push" {
		t.Errorf("git sub: %s", sub)
	}
}

func TestSecretsAndMarkers(t *testing.T) {
	diff := "+++ b/config.ts\n@@ -1,0 +1,3 @@\n+const k = 'AKIAABCDEFGHIJKLMNOP'\n+const password = \"hunter2Qx9!zz\"\n+const name = 'ok'\n+++ b/.env.example\n@@ -0,0 +1 @@\n+API_KEY=sk-proj-aaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"
	got := ScanSecrets(diff)
	if len(got) != 2 || !strings.Contains(got[0], "config.ts:1") {
		t.Errorf("secrets: %v", got)
	}
	for _, g := range got {
		if strings.Contains(g, "AKIA") || strings.Contains(g, "hunter2") {
			t.Error("secret value leaked into the message")
		}
	}
	if m := AIMarkers("fix: x\n\nCo-Authored-By: Claude Opus <noreply@anthropic.com>"); len(m) == 0 {
		t.Error("marker not found")
	}
	s := &Setup{Kind: "company"}
	if p := CheckCommitMessage("Fix the thing", s); len(p) == 0 {
		t.Error("company message without ticket should fail")
	}
	if p := CheckCommitMessage("PROJ-12: Fix the thing", s); len(p) != 0 {
		t.Errorf("good company message: %v", p)
	}
	if p := CheckCommitMessage("fix: handle empty cart", &Setup{Kind: "personal"}); len(p) != 0 {
		t.Errorf("good personal message: %v", p)
	}
	msg, ok := ParseCommitMessage("git commit -m \"$(cat <<'EOF'\nfix: a\n\nbody\nEOF\n)\"", "/")
	if !ok || !strings.HasPrefix(msg, "fix: a") {
		t.Errorf("heredoc message: %q %v", msg, ok)
	}
	msg, ok = ParseCommitMessage(`git commit -am "feat: b"`, "/")
	if !ok || msg != "feat: b" {
		t.Errorf("-am message: %q", msg)
	}
	if _, ok := ParseCommitMessage("git commit", "/"); ok {
		t.Error("editor commit should not parse")
	}
}

func TestDocs(t *testing.T) {
	refs := Refs("see src/a.go:12 and (lib/b.ts:3-9), not https://x.com:443/y.go")
	if len(refs) != 2 || refs[0].Path != "src/a.go" || refs[1].Line != 3 {
		t.Errorf("refs: %+v", refs)
	}
	plan := "## Files\n- src/a.go\n- `src/a_test.go`\n## Cases\n- [bug] x -> test: src/a_test.go\n- [edge] y, does not apply, because z\n- no label -> test: q\n## Real-world proof\n- api: POST /x returns 201\n- ui: does not apply, because no UI\n"
	pc := CheckPlan(plan)
	if len(pc.Files) != 2 || pc.Files[1] != "src/a_test.go" {
		t.Errorf("files: %v", pc.Files)
	}
	if len(pc.ProofKinds) != 1 || pc.ProofKinds[0] != "api" {
		t.Errorf("proof kinds: %v", pc.ProofKinds)
	}
	if len(pc.Problems) != 2 { // the unlabelled case, and no "Needs your OK"
		t.Errorf("problems: %v", pc.Problems)
	}
}
