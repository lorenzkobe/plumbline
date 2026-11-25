package core

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests for the 2a hardening (0.4.1): report checks, repeated blocks, sealed
// proof runs, no "small" for risky plans, shared tests, reviewer disputes,
// and failure reports.

// reviewedTask: proven, reviewed by the code reviewer, ready to report.
func reviewedTask(t *testing.T) *harness {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n- [90] must-fix: A should return 3 (src/a.go:3 `func A() int`)\n")
	return h
}

func (h *harness) stop(last string) Output {
	return Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: last})
}

func TestReportNeedsForYouAndWaivers(t *testing.T) {
	h := reviewedTask(t)
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code-review-1: pushed back\n")
	if m := h.finishMissing(); !strings.Contains(m, `"## For you"`) {
		t.Fatalf("a report without For you should be refused: %s", m)
	}
	// A heading that only starts like it doesn't count.
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For your information\n- the suite is slow\n")
	if m := h.finishMissing(); !strings.Contains(m, `"## For you"`) {
		t.Fatalf("\"For your information\" isn't a For you section: %s", m)
	}
	// An empty For you heading doesn't count either.
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For you\n")
	if m := h.finishMissing(); !strings.Contains(m, `"## For you"`) {
		t.Fatalf("an empty For you section should be refused: %s", m)
	}
	report := "# Report\n" + reportEnd + "## Review\n- fine\n## For you\n- nothing\n"
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("complete report: %s", m)
	}

	// Every waiver the task was given must be in the report, lapsed or not.
	tk := h.task()
	fp, _ := Fingerprint(h.repo, nil)
	tk.Waivers = append(tk.Waivers, Waiver{Check: "proof:ui", Reason: "browser can't reach localhost", Print: fp, At: Now()},
		Waiver{Check: "e2e", Reason: "no local stack", Print: "old-code", At: Now()})
	must(t, SaveTask(tk))
	if m := h.finishMissing(); !strings.Contains(m, "every waiver") || !strings.Contains(m, "e2e, proof:ui") {
		t.Fatalf("unlisted waivers should be refused: %s", m)
	}
	// Saying "e2e" elsewhere in the report doesn't list the waiver.
	h.docs(DocReport, report+"## Proof\n- e2e: skipped\n- Waivers: proof:ui (browser can't reach localhost)\n")
	if m := h.finishMissing(); !strings.Contains(m, "every waiver") || !strings.Contains(m, ": e2e") {
		t.Fatalf("e2e outside the waiver line isn't a listed waiver: %s", m)
	}
	h.docs(DocReport, report+"## Proof\n- Waivers: proof:ui (browser can't reach localhost), e2e (no local stack)\n")
	if m := h.finishMissing(); !strings.Contains(m, "verdict says Ready") || !strings.Contains(m, "proof:ui") {
		t.Fatalf("a waiver in effect makes plain Ready wrong: %s", m)
	}
	limited := strings.Replace(report, "Ready: proven", "Ready with noted limitations: the UI wasn't clicked through", 1)
	h.docs(DocReport, limited+"## Proof\n- Waivers: proof:ui (browser can't reach localhost), e2e (no local stack)\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("waivers listed: %s", m)
	}
}

func TestClosingMessageIsTheShortReport(t *testing.T) {
	h := reviewedTask(t)
	// Not proven yet: a question goes through, as before.
	if o := h.stop("Should A return 3 or 4?"); o != nil {
		t.Fatalf("a question while unproven should pass: %v", o)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For you\n- nothing\n")
	// Proven: ending with the commit question no longer skips the check.
	if o := h.stop("Done, ready for your review. Commit?"); !strings.Contains(fmtOut(o), "final message") {
		t.Fatalf("the closing question without For you should be sent back: %v", o)
	}
	if h.task().Reported {
		t.Fatal("a refused closing message must not mark the task reported")
	}
	for m, ok := range map[string]bool{"I fixed it for you. Commit?": false, "Ready for your review.": false,
		"For you: nothing": true, "- **For you:** run the migration": true, "Done.\n\nFor you\n- nothing": true} {
		if got := finalMessageMissing(m, "") == ""; got != ok {
			t.Errorf("finalMessageMissing(%q) passes = %v, want %v", m, got, ok)
		}
	}
	if o := h.stop("Fixed A.\n**For you:** nothing.\nCommit as \"fix: A returns 2\"?"); o != nil {
		t.Fatalf("the short report should pass: %v", o)
	}
	if !h.task().Reported {
		t.Fatal("the short report should mark the task reported")
	}
	// A code change makes it unreported, so the next closing message is checked again.
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	if h.task().Reported {
		t.Fatal("a code change should reset reported")
	}
}

// The closing-message check sends back once, then lets go: it's the format,
// not the proof, and a real question after the proof must not loop.
func TestClosingMessageSentBackOnce(t *testing.T) {
	h := reviewedTask(t)
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For you\n- nothing\n")
	if o := h.stop("Should I also update the README?"); !strings.Contains(fmtOut(o), "final message") {
		t.Fatalf("first closing message without For you: %v", o)
	}
	if o := h.stop("Should I also update the README?"); o != nil {
		t.Fatalf("the second time it should go through: %v", o)
	}
	if !h.task().Reported {
		t.Fatal("letting it go marks the task reported, so later turns aren't sent back")
	}
	if o := h.stop("Okay, leaving the README."); o != nil {
		t.Fatalf("a later turn: %v", o)
	}
}

func TestSameBlockTwiceStops(t *testing.T) {
	h := groundedTask(t, "normal")
	a := map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}
	b := map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}
	_, r1 := decision(h.pre("Edit", a))
	if strings.Contains(r1, "second time") {
		t.Fatalf("the first block isn't a repeat: %s", r1)
	}
	h.expect(h.pre("Edit", a), "deny", "second time in a row")
	h.expect(h.pre("Edit", a), "deny", "end your message with a question") // still repeating

	// The same refusal for another file isn't a repeat, and starts over.
	if _, r := decision(h.pre("Edit", b)); strings.Contains(r, "second time") {
		t.Fatalf("another file isn't the same block: %s", r)
	}
	if _, r := decision(h.pre("Edit", a)); strings.Contains(r, "second time") {
		t.Fatalf("a different block in between should reset: %s", r)
	}
	// A new message from the user starts over too.
	h.say("keep going")
	if _, r := decision(h.pre("Edit", a)); strings.Contains(r, "second time") {
		t.Fatalf("a user message should reset: %s", r)
	}

	// Helpers count on their own, and are told to put it in their findings.
	h.docs(DocPlan, goodPlan)
	id, _ := h.startHelper(HelperChallenger)
	edit := func() Output { return PreToolUse(h.helperIn(id, HelperChallenger, "Edit", a)) }
	if _, r := decision(edit()); strings.Contains(r, "second time") {
		t.Fatalf("the helper's first block isn't the lead's repeat: %s", r)
	}
	h.expect(edit(), "deny", "put it in your findings")

	// No session id: no record, no crash.
	in := h.in("Edit", a, nil)
	in.SessionID = ""
	PreToolUse(in)
	PreToolUse(in)
}

func TestRunsSealed(t *testing.T) {
	h := provenTask(t)
	s, _, _ := LoadSetup(h.repo)
	if m := ProofMissing(h.task(), s, true); len(m) != 0 {
		t.Fatalf("the recorded run is proof: %v", m)
	}
	runs := LoadRuns(h.repo)
	real := *runs[len(runs)-1]

	// Make the code change, then forge a passing run for it.
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 5 }\n")
	fp, _ := Fingerprint(h.repo, s.IgnoreChanges)
	forged := real
	forged.ID, forged.PrintBefore, forged.PrintAfter = "forged-test", fp, fp
	b, _ := json.Marshal(forged)
	f, err := os.OpenFile(runsPath(h.repo), os.O_APPEND|os.O_WRONLY, 0o600)
	must(t, err)
	lines := []string{
		string(b), // unsealed, as before 0.4.1
		strings.Repeat("0", 64) + " " + string(b),    // wrong seal
		seal([]byte(`{"id":"x"}`)) + " " + string(b), // a seal for other data
	}
	_, err = f.WriteString(strings.Join(lines, "\n") + "\n")
	must(t, err)
	f.Close()
	for _, r := range LoadRuns(h.repo) {
		if r.ID == "forged-test" {
			t.Fatal("a forged run was loaded")
		}
	}
	if m := ProofMissing(h.task(), s, true); len(m) == 0 {
		t.Fatal("forged runs must not count as proof")
	}
}

func TestRunsBelongToTheTaskAndCommand(t *testing.T) {
	h := provenTask(t)
	s, _, _ := LoadSetup(h.repo)
	fp, _ := Fingerprint(h.repo, s.IgnoreChanges)
	runs := LoadRuns(h.repo)
	if LatestFor(runs, h.task().ID, KindTest, "", s.Commands.Test, fp) == nil {
		t.Fatal("the task's own run should count")
	}
	if LatestFor(runs, "other-task", KindTest, "", s.Commands.Test, fp) != nil {
		t.Fatal("another task's run must not count")
	}
	if LatestFor(runs, h.task().ID, KindTest, "", "go test ./...", fp) != nil {
		t.Fatal("a run of another command must not count")
	}

	// A run for another task on the same code doesn't prove this one.
	h2 := h.task()
	other := *h2
	other.ID = "0000-other"
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 6 }\n")
	_, err := Execute(h.repo, &other, KindTest, "", "./test.sh", s, io.Discard)
	must(t, err)
	if m := ProofMissing(h.task(), s, true); !strings.Contains(strings.Join(m, ";"), "full test suite") {
		t.Fatalf("another task's run on this code is no proof for this task: %v", m)
	}
	// And a run of a different command, even by this task.
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh --quick", s, io.Discard)
	must(t, err)
	if m := ProofMissing(h.task(), s, true); !strings.Contains(strings.Join(m, ";"), "full test suite") {
		t.Fatalf("a run of an unsaved command is no proof: %v", m)
	}
}

func TestRelatedCommandMatches(t *testing.T) {
	tpl := "npx vitest related {files} --run"
	for ran, want := range map[string]bool{
		"npx vitest related 'src/a.ts' 'src/b.ts' --run": true,
		"npx vitest related  --run":                      true,
		"npx vitest related 'src/a.ts' --run; true":      false,
		"echo npx vitest related --run":                  false,
		"npx vitest run":                                 false,
	} {
		if got := commandMatches(KindRelated, tpl, ran); got != want {
			t.Errorf("commandMatches(%q) = %v, want %v", ran, got, want)
		}
	}
	if !commandMatches(KindTest, "go test ./...", "go test ./...") || commandMatches(KindTest, "go test ./...", "go test ./a") {
		t.Error("other kinds need the exact saved command")
	}
}

func TestSmallRefusedForRiskyPlans(t *testing.T) {
	for _, f := range []string{"db/migrations/0042_add_refunds.sql", "src/billing/charge.go", "package.json", ".github/workflows/deploy.yml", "src/users/profile.go"} {
		h := groundedTask(t, "small")
		h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- "+f+"\n", 1))
		h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "isn't small")
	}
	// A folder or a pattern would let a risky file in unseen.
	for _, f := range []string{"src/", "src/**/*.go", "src/?.go"} {
		h := groundedTask(t, "small")
		h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- "+f+"\n", 1))
		h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "names each file")
	}
	// The command the refusal names runs before approval.
	h0 := groundedTask(t, "small")
	h0.expect(h0.bash(`plumbline task size normal --why "touches billing"`), "allow", "")
	// A plain file stays small, and a normal task with a risky file isn't asked to resize.
	h := groundedTask(t, "small")
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- docs/usage.md (new)\n", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "", "")
	h = groundedTask(t, "normal")
	h.docs(DocPlan, strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- package.json\n", 1))
	if _, r := decision(h.pre("AskUserQuestion", checkpointQ())); strings.Contains(r, "isn't small") {
		t.Fatalf("a normal task isn't resized: %s", r)
	}
}

func TestSharedTests(t *testing.T) {
	plan := "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n## Cases\n" +
		"- [normal] refund once -> test: src/a_test.go::TestRefund\n" +
		"- [repeat-safe] refund twice -> test: src/a_test.go::TestRefund\n" +
		"- [edge] zero amount -> test: `src/r.test.ts::refunds zero`\n" +
		"- [boundary] max amount -> test: `src/r.test.ts::refunds zero`\n" +
		"- [refused] no permission -> test: src/b_test.go\n" +
		"- [bug] bad state -> test: src/b_test.go\n" +
		"## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n"
	pc := CheckPlan(plan)
	want := "src/a_test.go::TestRefund (2 cases), src/r.test.ts::refunds zero (2 cases)"
	if got := strings.Join(pc.SharedTests, ", "); got != want {
		t.Fatalf("shared tests = %q, want %q (a file alone isn't a shared test)", got, want)
	}
	if got := strings.Join(pc.CaseTests, ","); got != "src/a_test.go,src/a_test.go,src/r.test.ts,src/r.test.ts,src/b_test.go,src/b_test.go" {
		t.Fatalf("case test files = %s", got)
	}

	// Helpers are told about them.
	h := groundedTask(t, "normal")
	h.docs(DocPlan, plan)
	_, o := h.startHelper(HelperChallenger)
	if !strings.Contains(fmtOut(o), "Shared tests") || !strings.Contains(fmtOut(o), "src/a_test.go::TestRefund (2 cases)") {
		t.Fatalf("the challenger should be told about shared tests: %s", fmtOut(o))
	}
}

func TestReviewerAnswersDisputes(t *testing.T) {
	h := reviewedTask(t)
	report := "# Report\n" + reportEnd + "## Review\n- code-review-1: pushed back, see disputes\n## For you\n- nothing\n"
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("no dispute yet: %s", m)
	}
	time.Sleep(10 * time.Millisecond) // the dispute is written after the review began
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	h.docs(DocReport, report)
	if m := h.finishMissing(); !strings.Contains(m, "answers its disputes") || !strings.Contains(m, "plumbline:code-reviewer") {
		t.Fatalf("a dispute needs the reviewer's answer: %s", m)
	}

	// The reviewer is pointed at its own disputes, and may read them, but not another reviewer's.
	id, o := h.startHelper(HelperCodeReviewer)
	if !strings.Contains(fmtOut(o), "code-reviewer-disputes.md") {
		t.Fatalf("the reviewer should be told about the disputes: %s", fmtOut(o))
	}
	docs := TaskDocsDir(h.repo, h.task().ID)
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(docs, DisputesName(HelperCodeReviewer))})), "", "")
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(docs, DisputesName(HelperSecurity))})), "deny", "independent")
	// Answering without a Disputes section isn't an answer.
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 2\nnothing new\n"))
	h.stopHelper(id, HelperCodeReviewer)
	h.docs(DocReport, report)
	if m := h.finishMissing(); !strings.Contains(m, "answers its disputes") {
		t.Fatalf("findings without ## Disputes don't answer: %s", m)
	}

	// Withdrawn: done.
	h.runHelperReading(HelperCodeReviewer, "# Code review 3\n## Disputes\n- withdrawn: the ticket does say 2 (src/a.go:3 `return 2`)\n", filepath.Join(h.repo, "src", "a.go"))
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("a withdrawn finding needs nothing more: %s", m)
	}
}

func TestUpheldDisputeGoesToTheUser(t *testing.T) {
	h := reviewedTask(t)
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2 (src/a.go:3)\n")
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Disputes\n- upheld: the comment on the ticket changed it to 3\n")
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- disputed\n## For you\n- nothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "upheld findings") {
		t.Fatalf("an upheld finding must reach the user: %s", m)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- disputed\n## For you\n- Upheld by the code reviewer: A should return 3 (ticket comment); I think 2 (ticket text). Your call.\n")
	if m := h.finishMissing(); !strings.Contains(m, "verdict says Ready") || !strings.Contains(m, "upheld a finding") {
		t.Fatalf("an upheld finding makes plain Ready wrong: %s", m)
	}
	limitedEnd := strings.Replace(reportEnd, "Ready: proven", "Ready with noted limitations: one finding is your call", 1)
	h.docs(DocReport, "# Report\n"+limitedEnd+"## Review\n- disputed\n## For you\n- Upheld by the code reviewer: A should return 3 (ticket comment); I think 2 (ticket text). Your call.\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("upheld and listed: %s", m)
	}

	// No runs left to answer a new dispute: the user decides.
	h.runHelper(HelperCodeReviewer, "# Code review 3\n## Disputes\n- upheld: same\n")
	time.Sleep(10 * time.Millisecond)
	h.docs(DisputesName(HelperCodeReviewer), "- [90] A should return 3: the ticket says 2, again (src/a.go:3)\n")
	if m := h.finishMissing(); !strings.Contains(m, "all 3 runs are used") {
		t.Fatalf("a dispute with no runs left goes to the user: %s", m)
	}
	// The arbiter rules on it first (0.8.3); it upholds the finding, so it's the user's call.
	h.l82Arbiter("# Arbiter 1\n" + l82OpenMustFix)
	h.answerReviewCap("Accept as is")
	h.docs(DocReport, "# Report\n"+limitedEnd+"## Review\n- disputed; accepted as is by you\n## For you\n- Upheld by the code reviewer: A should return 3.\n- Disputed again, no review runs left: the ticket says 2. Your call.\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("a capped dispute listed under For you is the user's now: %s", m)
	}
}

func TestFailureReports(t *testing.T) {
	h := newHarness(t)
	if n := FailureNote(); n != "" {
		t.Fatalf("no failures yet: %s", n)
	}
	p := RecordFailure("0.4.1", "pre", "Edit", "runtime error: index out of range", []byte("goroutine 1 [running]:\nmain.go:42"))
	b, err := os.ReadFile(p)
	must(t, err)
	if s := string(b); !strings.Contains(s, `"pre" hook`) || !strings.Contains(s, "Tool: Edit") || !strings.Contains(s, "main.go:42") {
		t.Fatalf("crash report: %s", s)
	}
	stamp := Now().Format(time.RFC3339) // whole seconds, like the launcher writes
	must(t, os.WriteFile(errorsLog(), []byte(stamp+" launcher: the Plumbline binary is missing\nnot a line\n"), 0o600))
	note := FailureNote()
	if !strings.Contains(note, "failed 2 time(s)") || !strings.Contains(note, p) || !strings.Contains(note, "couldn't start") {
		t.Fatalf("failure note: %s", note)
	}
	if again := FailureNote(); again != "" {
		t.Fatalf("failures are reported once: %s", again)
	}
	// A launcher line in the same second as the last note still counts, once.
	f, err := os.OpenFile(errorsLog(), os.O_APPEND|os.O_WRONLY, 0o600)
	must(t, err)
	_, err = f.WriteString(stamp + " launcher: missing again\n" + stamp + " launcher: half a line")
	must(t, err)
	f.Close()
	if n := FailureNote(); !strings.Contains(n, "failed 1 time(s)") {
		t.Fatalf("a new launcher line in the same second: %s", n)
	}
	if s := Stats(1); !strings.Contains(s, "Plumbline failures: 3") {
		t.Fatalf("stats should count failures: %s", s)
	}
	// The next session start tells the user.
	RecordFailure("0.4.1", "stop", "", "boom", nil)
	o := SessionStart(&HookInput{SessionID: h.sid, Cwd: h.repo, Source: "startup"})
	if sm, _ := o["systemMessage"].(string); !strings.Contains(sm, "failed 1 time(s)") {
		t.Fatalf("session start should show the failure: %v", o)
	}
}

func TestLauncherLogsAMissingBinary(t *testing.T) {
	src, err := filepath.Abs("../../bin/plumbline")
	must(t, err)
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	must(t, os.MkdirAll(filepath.Join(dir, "dist"), 0o755))
	b, err := os.ReadFile(src)
	must(t, err)
	launcher := filepath.Join(dir, "bin", "plumbline")
	must(t, os.WriteFile(launcher, b, 0o755))
	home := filepath.Join(dir, "home")
	cmd := exec.Command(launcher, "hook", "pre")
	cmd.Env = append(os.Environ(), "PLUMBLINE_HOME="+home)
	out, err := cmd.CombinedOutput()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 1 {
		t.Fatalf("a missing binary should exit 1 (not 2, which would block every prompt): %v %s", err, out)
	}
	if !strings.Contains(string(out), "claude plugin disable plumbline") {
		t.Fatalf("the message should say the way out: %s", out)
	}
	logged, err := os.ReadFile(filepath.Join(home, "errors.log"))
	must(t, err)
	if !strings.Contains(string(logged), "launcher: the Plumbline binary is missing") && !strings.Contains(string(logged), "no Plumbline binary") {
		t.Fatalf("errors.log: %s", logged)
	}
	if _, err := time.Parse(time.RFC3339, strings.Fields(string(logged))[0]); err != nil {
		t.Fatalf("errors.log lines start with the time FailureNote reads: %s", logged)
	}
	// With the binary there, it runs it.
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		arch := map[string]string{"arm64": "arm64", "amd64": "amd64"}[runtime.GOARCH]
		bin := filepath.Join(dir, "dist", "plumbline-"+runtime.GOOS+"-"+arch)
		must(t, os.WriteFile(bin, []byte("#!/bin/sh\necho ran \"$@\"\n"), 0o755))
		out, err := exec.Command(launcher, "version").CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != "ran version" {
			t.Fatalf("launcher should exec the binary: %v %s", err, out)
		}
	}
}

// The two PreToolUse groups in hooks.json: searches fail open, everything
// else fails closed, and no tool is in both (both run, and a failed blocking
// group would still block). Claude Code matchers are JavaScript regexes, so
// node checks them.
func TestHookMatchers(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; matchers are JavaScript regexes")
	}
	raw, err := os.ReadFile("../../hooks/hooks.json")
	must(t, err)
	var cfg struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command   string `json:"command"`
				OnFailure string `json:"onFailure"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	must(t, json.Unmarshal(raw, &cfg))
	var block, search string
	for _, g := range cfg.Hooks["PreToolUse"] {
		switch {
		case strings.HasSuffix(g.Hooks[0].Command, "hook pre") && g.Hooks[0].OnFailure == "block":
			block = g.Matcher
		case strings.HasSuffix(g.Hooks[0].Command, "hook pre-search") && g.Hooks[0].OnFailure == "":
			search = g.Matcher
		}
	}
	if block == "" || search == "" {
		t.Fatal("PreToolUse needs a blocking group (hook pre) and a fail-open search group (hook pre-search)")
	}
	cases := map[string]string{
		"Monitor": "block", "Edit": "block", "Write": "block", "MultiEdit": "block", "NotebookEdit": "block", "Bash": "block", "Read": "block",
		"AskUserQuestion": "block", "Agent": "block", "Task": "block",
		"mcp__serena__replace_symbol_body": "block", "mcp__serena__read_file": "block", "mcp__serena__execute_shell_command": "block",
		"mcp__claude_ai_Atlassian__addCommentToJiraIssue": "block", "mcp__claude_ai_Atlassian__getJiraIssue": "block",
		"mcp__filesystem__write_file": "block", "mcp__evil__search_context7": "block", "mcp__claude-in-chrome__navigate": "block",
		"Grep": "search", "Glob": "search", "mcp__mempalace__mempalace_search": "search", "mcp__socraticode__codebase_search": "search",
		"mcp__context7__resolve-library-id": "search", "mcp__plugin_context7_context7__get-library-docs": "search",
		"mcp__serena__find_symbol": "search", "mcp__serena__find_referencing_symbols": "search", "mcp__plugin_serena_serena__get_symbols_overview": "search",
	}
	names, _ := json.Marshal(cases)
	script := `const [b, s, n] = process.argv.slice(1); const B = new RegExp(b), S = new RegExp(s); const out = {};
for (const k of Object.keys(JSON.parse(n))) { out[k] = (B.test(k) ? "block" : "") + (S.test(k) ? "search" : ""); }
console.log(JSON.stringify(out));`
	out, err := exec.Command(node, "-e", script, block, search, string(names)).Output()
	must(t, err)
	var got map[string]string
	must(t, json.Unmarshal(out, &got))
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s: matched %q, want %q", k, got[k], want)
		}
	}
}

// Monitor runs a shell command, so it gets the Bash gate.
func TestMonitorGated(t *testing.T) {
	h := groundedTask(t, "normal")
	mon := func(cmd string) Output {
		return h.pre("Monitor", map[string]any{"command": cmd, "description": "x", "timeout_ms": 1000})
	}
	h.expect(mon(`git commit -am "fix: A"`), "deny", "")
	h.expect(mon("sed -i '' 's/1/2/' src/a.go"), "deny", "plan isn't approved")
	h.expect(mon("git push"), "deny", "")
	h.expect(mon("git status"), "allow", "")
	// The WebSocket form runs no command.
	h.expect(h.pre("Monitor", map[string]any{"ws": map[string]any{"url": "wss://example.com"}, "description": "x", "timeout_ms": 1000}), "", "")
}

// Grep prints matching lines, so naming a secret file asks like Read does.
func TestGrepOfSecretFilesAsks(t *testing.T) {
	h := newHarness(t)
	grep := func(in map[string]any) Output { return h.pre("Grep", in) }
	for _, in := range []map[string]any{
		{"pattern": "KEY", "path": ".env"}, {"pattern": "KEY", "path": filepath.Join(h.repo, "config", ".env.local")},
		{"pattern": "KEY", "glob": "**/.env*"}, {"pattern": "KEY", "glob": "*.env"}, {"pattern": "BEGIN", "glob": "*.pem"},
		{"pattern": "x", "glob": "{*.go,.env}"}, {"pattern": "x", "path": "certs/server.key"},
	} {
		h.expect(grep(in), "ask", "shows lines of")
	}
	for _, in := range []map[string]any{
		{"pattern": "KEY"}, {"pattern": "KEY", "path": "src"}, {"pattern": "KEY", "glob": "*.go"},
		{"pattern": "KEY", "path": ".env.example"}, {"pattern": "KEY", "glob": ".env.example"}, {"pattern": "KEY", "glob": "*.example"},
	} {
		h.expect(grep(in), "", "")
	}
}

// A folder in the plan doesn't cover an auth file (0.8.7: it needs its own
// line), and a listed auth file still calls for the security reviewer: the
// files the task really changed count.
func TestChangedFilesCallForSpecialists(t *testing.T) {
	h := groundedTask(t, "normal")
	tk := h.task()
	tk.StartCommit = HeadCommit(h.repo) // as plumbline task start records it
	must(t, SaveTask(tk))
	plan := strings.Replace(goodPlan, "- src/a.go\n", "- src/\n- src/auth/session.go (new)\n", 1) + "## Team\n- lead, code-reviewer, security-reviewer\n"
	h.docs(DocPlan, plan)
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing\n")
	h.docs(DocPlan, plan+"## Challenge note\n- nothing changed\n")
	h.approve()
	session := filepath.Join(h.repo, "src", "auth", "session.go")
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "auth", "token.go")}), "deny", "risky kind of file") // src/ doesn't cover it
	h.expect(h.pre("Write", map[string]any{"file_path": session}), "", "")                                                                // its own line
	write(t, session, "package auth\n")
	h.post("Write", map[string]any{"file_path": session}, nil)
	s, _, _ := LoadSetup(h.repo)
	if got := strings.Join(RequiredReviewers(h.task(), s), ","); !strings.Contains(got, HelperSecurity) {
		t.Fatalf("a changed auth file needs the security reviewer: %s", got)
	}
	// Committed slices count too.
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "feat: session")
	if got := strings.Join(RequiredReviewers(h.task(), s), ","); !strings.Contains(got, HelperSecurity) {
		t.Fatalf("a committed auth file still needs the security reviewer: %s", got)
	}
}

// reportEnd: the verdict and common-sense calls every finished report has
// (0.6.3).
const reportEnd = "## Verdict\nReady: proven and reviewed.\nGoal: met\nRollback: revert the commit.\n## Common-sense calls\n- none\n"
