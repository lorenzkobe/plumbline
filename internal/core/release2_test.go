package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Tests for release 2 (0.6.3): case labels kept in step with the docs,
// plans that decide nothing, the Reuse section, the report's verdict,
// common-sense calls and unfixed should-fix findings, and reviewers who
// start each later run with a fresh pass.

// The case labels live in docs.go, rules.md and checkpoints.md; all three
// must list the same ones in the same order.
func TestCaseLabelsMatchDocs(t *testing.T) {
	want := strings.Join(CaseLabels, ",")
	m := regexp.MustCompile("`- \\[([a-z|-]+)\\]").FindStringSubmatch(CoreRules())
	if m == nil {
		t.Fatal("rules.md: no `- [label|label|...]` case list found")
	}
	if got := strings.ReplaceAll(m[1], "|", ","); got != want {
		t.Errorf("rules.md labels = %s, want %s", got, want)
	}
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "task", "checkpoints.md"))
	must(t, err)
	m = regexp.MustCompile(`(?m)^Labels: ([a-z, -]+)\.`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("checkpoints.md: no \"Labels: a, b, ...\" line found")
	}
	if got := strings.ReplaceAll(m[1], ", ", ","); got != want {
		t.Errorf("checkpoints.md labels = %s, want %s", got, want)
	}
}

func TestFailureAndConcurrentLabels(t *testing.T) {
	plan := strings.Replace(goodPlan, "## Real-world proof",
		"- [failure] the store times out: nothing is half written -> test: test.sh\n"+
			"- [concurrent] two refunds at once: one wins -> test: test.sh\n## Real-world proof", 1)
	if pc := CheckPlan(plan); len(pc.Problems) > 0 || pc.Cases != 3 {
		t.Fatalf("[failure] and [concurrent] are labels: %d cases, %v", pc.Cases, pc.Problems)
	}
	pc := CheckPlan(strings.Replace(goodPlan, "[bug]", "[race]", 1))
	if p := strings.Join(pc.Problems, "; "); !strings.Contains(p, "needs a label") || !strings.Contains(p, "[failure] [concurrent]") {
		t.Fatalf("an unknown label is refused, and the message lists every label: %s", p)
	}
}

func TestPlanLinesThatDecideNothing(t *testing.T) {
	for _, line := range []string{
		"- Validation: TBD",
		"- Handle edge cases in the refund path.",
		"- Add appropriate validation to the body",
		"- handle errors appropriately",
		"- Write tests for the above",
		"- The retry count is to be decided",
		"- We'll figure it out later",
	} {
		plan := strings.Replace(goodPlan, "## Files", "## Approach\n"+line+"\n## Files", 1)
		if p := strings.Join(CheckPlan(plan).Problems, "; "); !strings.Contains(p, "leave the decision for later") {
			t.Errorf("%q should be refused: %s", line, p)
		}
	}
	for _, line := range []string{
		"- Rename the `TBD` column to `pending`", // code, in backticks
		"- Refuse a refund of 0.00 (an edge case the ticket names)",
		"- Validate the refund body at the API boundary: amount > 0, currency in the order's",
		"- The tbdx flag stays",
	} {
		plan := strings.Replace(goodPlan, "## Files", "## Approach\n"+line+"\n## Files", 1)
		if p := CheckPlan(plan).Problems; len(p) > 0 {
			t.Errorf("%q decides something, so it passes: %v", line, p)
		}
	}
	// A code block, and the challenge note quoting the challenger, aren't the
	// plan's own words.
	plan := goodPlan + "```\n// TBD: old comment in the code\n```\n## Challenge note\n- Questioned: \"handle edge cases\" was vague -> changed: listed them\n"
	if p := CheckPlan(plan).Problems; len(p) > 0 {
		t.Fatalf("code blocks and the challenge note aren't checked: %v", p)
	}
}

func TestUnderstandingNeedsReuse(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", `"## Reuse"`)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n## Outside the ticket\n- none\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", `"## Reuse"`)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\nSearched: \"returns 1\" in src/, SocratiCode \"constant getter\". Found: nothing.\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "", "")
}

// report builds a report around a verdict block and a proof section.
func r2Report(verdict, proof string) string {
	return "# Report\n## Verdict\n" + verdict + "\nGoal: met\n## Proof\n" + proof + "\n## Review\n- fine\n## Common-sense calls\n- none\n## For you\n- nothing\n"
}

func TestReportVerdict(t *testing.T) {
	h := reviewedTask(t)
	rb := "\nRollback: revert the commit."
	cases := []struct {
		verdict, proof, want string // want "" means the report passes
	}{
		{"", "- Full suite: pass", `"## Verdict"`},
		{"Done, all good." + rb, "- Full suite: pass", "first line must start with"},
		{"Ready: proven.", "- Full suite: pass", `"Rollback:"`},
		{"Ready: proven." + rb, "- Full suite: pass", ""},
		{"**Verdict:** Ready." + rb, "- Full suite: pass", ""},
		{"- **Ready with noted limitations**: none really." + rb, "- Full suite: pass", ""},
		{"Not ready: the suite fails." + rb, "- Full suite: 2 failed", ""},
		{"Readyish." + rb, "- Full suite: pass", "first line must start with"},
		// A count of skipped tests and the weakened-tests line may say skipped.
		{"Ready." + rb, "- Full suite: 120 passed, 3 skipped\n- Weakened tests: TestSlow skipped on purpose (see below)", ""},
		// Lines listed under the weakened-tests or waiver line may say it too,
		// but the next top-level line is checked again.
		{"Ready." + rb, "- Weakened tests:\n  - TestSlow skipped on purpose\n  - TestOld skipped, see below\n- Full suite: pass", ""},
		{"Ready." + rb, "- Weakened tests:\n  - TestSlow skipped on purpose\n- Lint: skipped", `never "skipped"`},
		// A check that didn't run is never "skipped".
		{"Ready." + rb, "- e2e: skipped", `never "skipped"`},
		{"Ready." + rb, "- Lint: skip, it's slow", `never "skipped"`},
		// Couldn't run needs the command, and makes plain Ready wrong.
		{"Ready with noted limitations: e2e not run." + rb, "- e2e: couldn't run (no Docker)", "exact command to run later"},
		{"Ready with noted limitations: e2e not run." + rb, "- e2e: couldn’t run (no Docker); run later: `plumbline run e2e`", ""},
		{"Ready." + rb, "- e2e: could not run (no Docker); run later: `plumbline run e2e`", "verdict says Ready, but a check couldn't run"},
		{"Not ready: e2e never ran." + rb, "- e2e: couldn't run (no Docker); run later: `plumbline run e2e`", ""},
	}
	for _, c := range cases {
		report := r2Report(c.verdict, c.proof)
		if c.verdict == "" {
			report = strings.Replace(report, "## Verdict\n\n", "", 1)
		}
		h.docs(DocReport, report)
		m := h.finishMissing()
		switch {
		case c.want == "" && m != "":
			t.Errorf("verdict %q, proof %q should pass: %s", c.verdict, c.proof, m)
		case c.want != "" && !strings.Contains(m, c.want):
			t.Errorf("verdict %q, proof %q: want %q in %s", c.verdict, c.proof, c.want, m)
		}
	}
	// "## Verdicts and notes" isn't the verdict heading.
	h.docs(DocReport, strings.Replace(r2Report("Ready."+rb, "- Full suite: pass"), "## Verdict\n", "## Verdicts and notes\n", 1))
	if m := h.finishMissing(); !strings.Contains(m, `"## Verdict"`) {
		t.Fatalf("only the exact heading counts: %s", m)
	}
}

func TestCommonSenseCallsRequired(t *testing.T) {
	h := reviewedTask(t)
	full := r2Report("Ready.\nRollback: revert the commit.", "- Full suite: pass")
	h.docs(DocReport, strings.Replace(full, "## Common-sense calls\n- none\n", "", 1))
	if m := h.finishMissing(); !strings.Contains(m, `"## Common-sense calls"`) {
		t.Fatalf("a report without common-sense calls is refused: %s", m)
	}
	h.docs(DocReport, strings.Replace(full, "## Common-sense calls\n- none\n", "## Common-sense calls\n", 1))
	if m := h.finishMissing(); !strings.Contains(m, `"## Common-sense calls"`) {
		t.Fatalf("an empty section is refused: %s", m)
	}
	h.docs(DocReport, strings.Replace(full, "- none\n## For you", "- Formatted src/a.go with gofmt; if wrong, the diff is noisier.\n## For you", 1))
	if m := h.finishMissing(); m != "" {
		t.Fatalf("calls listed: %s", m)
	}
}

func TestShouldFixNotFixedListed(t *testing.T) {
	h := reviewedTask(t)
	full := r2Report("Ready.\nRollback: revert the commit.", "- Full suite: pass")
	h.docs(DocReport, full)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("no should-fix findings, nothing more needed: %s", m)
	}
	// A should-fix in an earlier run still has to be accounted for.
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- [70] should-fix: B repeats the parse in a.go (src/a.go:3 `func A() int`)\n")
	h.docs(DocReport, full)
	if m := h.finishMissing(); !strings.Contains(m, `"Should-fix not fixed:"`) {
		t.Fatalf("an unaccounted should-fix is refused: %s", m)
	}
	// Mentioned elsewhere in the report doesn't count; under Review it does.
	h.docs(DocReport, full+"## Outside the ticket\n- should-fix not fixed: none\n")
	if m := h.finishMissing(); !strings.Contains(m, `"Should-fix not fixed:"`) {
		t.Fatalf("only the Review section counts: %s", m)
	}
	for _, line := range []string{"- Should-fix not fixed: none", "- **Should-fix, not fixed:** B's repeated parse (one caller; the third copy would justify a helper)"} {
		h.docs(DocReport, strings.Replace(full, "## Review\n- fine\n", "## Review\n- fine\n"+line+"\n", 1))
		if m := h.finishMissing(); m != "" {
			t.Fatalf("%q accounts for it: %s", line, m)
		}
	}
}

// The challenger's suggestions carry no should-fix mark, so a challenge
// file never asks for the line.
func TestShouldFixIgnoresChallenger(t *testing.T) {
	h := reviewedTask(t)
	tk := h.task()
	tk.Helpers = append(tk.Helpers, HelperRun{Kind: HelperChallenger, AgentID: "c1", Findings: "challenge-1.md", Started: Now(), Finished: Now()})
	must(t, SaveTask(tk))
	write(t, filepath.Join(TaskDocsDir(h.repo, tk.ID), "challenge-1.md"), "- [85] should-fix: the plan misses a case\n")
	h.docs(DocReport, r2Report("Ready.\nRollback: revert the commit.", "- Full suite: pass"))
	if m := h.finishMissing(); m != "" {
		t.Fatalf("challenge findings don't need the should-fix line: %s", m)
	}
}

// Reviewers aren't told the 80 cutoff (a judge told the pass mark drifts
// toward it), and a later run does a fresh pass before reading its earlier
// findings.
func TestReviewersFreshPassAndNoCutoff(t *testing.T) {
	h := provenTask(t)
	id, o := h.startHelper(HelperCodeReviewer)
	if s := fmtOut(o); reCutoff.MatchString(s) || strings.Contains(s, "fresh pass") {
		t.Fatalf("first run: no cutoff and no earlier findings: %s", s)
	}
	stop := SubagentStop(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperCodeReviewer})
	if s := fmtOut(stop); !strings.Contains(s, "write your findings") || reCutoff.MatchString(s) {
		t.Fatalf("the stop without findings names no cutoff: %s", s)
	}
	write(t, h.findingsPath(id), "# Code review 1\n- [60] should-fix: x\n")
	h.stopHelper(id, HelperCodeReviewer)
	_, o = h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	fresh, earlier := strings.Index(s, "fresh pass first"), strings.Index(s, "code-review-1.md")
	if fresh < 0 || earlier < fresh || !strings.Contains(s, "really fixed") || reCutoff.MatchString(s) {
		t.Fatalf("second run: a fresh pass, then the earlier findings: %s", s)
	}
}

// reCutoff: wording that tells a helper the score cutoff.
var reCutoff = regexp.MustCompile(`(?i)\b80\b\s*(?:or more|and up|or above|\+)|(?:under|below|scored|at least)\s*80\b`)

func TestChecklistHasNoCutoffForReviewers(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "task", "review-checklist.md"))
	must(t, err)
	text := string(b)
	if reCutoff.MatchString(text) {
		t.Fatal("review-checklist.md must not tell helpers the 80 cutoff")
	}
	for _, want := range []string{"## Reuse", "Backward compatibility", "Hidden failures", "Test quality", "tells an AI what to do"} {
		if !strings.Contains(text, want) {
			t.Errorf("review-checklist.md should cover %q", want)
		}
	}
}
