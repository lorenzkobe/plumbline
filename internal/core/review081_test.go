package core

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const r081Cited = "# Code review 1\n## Findings\n- [70] should-fix: A returns a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n- [60] should-fix: the goal is vague (plan.md:3 `A works as planned`). Why: x. Fix: y.\n"

func (h *harness) r081Tests(name, body string) {
	h.t.Helper()
	p := filepath.Join(h.repo, filepath.FromSlash(name))
	write(h.t, p, body)
	h.post("Edit", map[string]any{"file_path": p}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(h.t, err)
}

func (h *harness) r081Run(n int) HelperRun {
	h.t.Helper()
	runs := h.task().helperRuns(HelperCodeReviewer)
	if len(runs) < n {
		h.t.Fatalf("no code reviewer run %d", n)
	}
	return runs[n-1]
}

func (h *harness) r081Findings(n int, text string) {
	h.t.Helper()
	write(h.t, filepath.Join(TaskDocsDir(h.repo, h.task().ID), h.r081Run(n).Findings), text)
}

func (h *harness) r081Edit(edit func(r *HelperRun)) {
	h.t.Helper()
	tk := h.task()
	for i := range tk.Helpers {
		if tk.Helpers[i].Kind == HelperCodeReviewer {
			edit(&tk.Helpers[i])
		}
	}
	must(h.t, SaveTask(tk))
}

func r081Focused(s string) bool { return strings.Contains(s, "This round is focused") }

func TestReview081FocusedRoundAfterAFixInCitedFiles(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	r1 := h.r081Run(1)
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n")
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	r2 := h.r081Run(2)
	if !r081Focused(s) || strings.Contains(s, "fresh pass first") {
		t.Fatalf("a fix in cited files and a test file gets the focused note: %s", s)
	}
	for _, want := range []string{"src/a.go", "src/a_test.go", "`git diff " + r1.PrintAfter + " " + r2.PrintBefore + "`", r1.Findings, "with its label", "Coverage"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the focused note should name %q: %s", want, s)
		}
	}
}

func TestReview081FullPassAfterAChangeToAnUncitedFile(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	h.r081Tests("src/b.go", "package src\n\nfunc B() int { return 5 }\n")
	_, o := h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	if r081Focused(s) || !strings.Contains(s, "fresh pass first") || !strings.Contains(s, "Files changed since that run: src/b.go.") {
		t.Fatalf("a change to an uncited file gets the full pass with the changed files: %s", s)
	}
	if strings.Index(s, "fresh pass first") > strings.Index(s, h.r081Run(1).Findings) {
		t.Fatalf("the fresh pass comes before the earlier findings: %s", s)
	}
}

func TestReview081CitedMeansAFindingsRefInTheRepo(t *testing.T) {
	for _, c := range []struct {
		name, findings string
		focused        bool
	}{
		{"checked and fine only", "# Code review 1\n## Findings\n- none\n## Checked and fine\n- src/a.go:3 `func A() int` is right\n", false},
		{"ref that doesn't resolve", "# Code review 1\n## Findings\n- [60] should-fix: x (src/a.go:3 `func A() int`) and (src/gone.go:1 `package src`)\n", false},
		{"ref that climbs out", "# Code review 1\n## Findings\n- [60] should-fix: x (src/a.go:3 `func A() int`) and (../src/a.go:3 `func A() int`)\n", false},
		{"plain ref", "# Code review 1\n## Findings\n- [60] should-fix: x (src/a.go:3 `func A() int`)\n", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := provenTask(t)
			h.runHelper(HelperCodeReviewer, r081Cited)
			h.r081Findings(1, c.findings)
			h.editA("3")
			_, o := h.startHelper(HelperCodeReviewer)
			if s := fmtOut(o); r081Focused(s) != c.focused {
				t.Fatalf("focused = %v, want %v: %s", r081Focused(s), c.focused, s)
			}
		})
	}
}

func TestReview081CitedPathFoldsCaseOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("letter case only folds on macOS")
	}
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	h.r081Findings(1, "# Code review 1\n## Findings\n- [60] should-fix: x (Src/A.go:3 `func A() int`)\n")
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	if s := fmtOut(o); !r081Focused(s) {
		t.Fatalf("Src/A.go is src/a.go on macOS: %s", s)
	}
}

func TestReview081UncountedRunGivesNoFocus(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	h.r081Edit(func(r *HelperRun) { r.Problems = []string{"sent back"} })
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	if r081Focused(s) || !strings.Contains(s, "fresh pass first") || strings.Contains(s, "Files changed since") || !strings.Contains(s, h.r081Run(1).Findings) {
		t.Fatalf("no counted run: the full pass with no list, naming the earlier findings: %s", s)
	}
}

func TestReview081UsesTheLastCountedRun(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	r1 := h.r081Run(1)
	h.r081Tests("src/a_test.go", "package src\n")
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- [60] should-fix: x (src/b.go:3 `func B() int`)\n")
	tk := h.task()
	for i := range tk.Helpers {
		if tk.Helpers[i].Kind == HelperCodeReviewer && tk.Helpers[i].Findings != r1.Findings {
			tk.Helpers[i].PrintAfter = "changed-while-running"
		}
	}
	must(t, SaveTask(tk))
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	r3 := h.r081Run(3)
	if !r081Focused(s) || !strings.Contains(s, "`git diff "+r1.PrintAfter+" "+r3.PrintBefore+"`") || !strings.Contains(s, r1.Findings) || strings.Contains(s, h.r081Run(2).Findings) {
		t.Fatalf("run 2 didn't count, so run 1's tree and findings are used: %s", s)
	}
}

func TestReview081ReplanGivesTheFullPass(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	tk := h.task()
	tk.Checkpoint2At = Now().Add(time.Second)
	must(t, SaveTask(tk))
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	if s := fmtOut(o); r081Focused(s) || !strings.Contains(s, "fresh pass first") || !strings.Contains(s, "src/a.go") {
		t.Fatalf("a plan approved again after the last run gets the full pass: %s", s)
	}
}

func TestReview081ChallengerNoteUnchanged(t *testing.T) {
	h := provenTask(t)
	tk := h.task()
	tk.Helpers = append(tk.Helpers, HelperRun{Kind: HelperChallenger, AgentID: "c1", Findings: "challenge-1.md", Started: Now(), Finished: Now()})
	must(t, SaveTask(tk))
	write(t, filepath.Join(TaskDocsDir(h.repo, tk.ID), "challenge-1.md"), "# Challenge\n## Findings\n- [60] x (src/a.go:3 `func A() int`)\n")
	o := SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "c2", AgentType: "plumbline:" + HelperChallenger})
	s := fmtOut(o)
	if !strings.Contains(s, "Do a fresh pass first, as if this were your first run, and write those findings. Only then open your earlier findings (challenge-1.md) and check whether each was really answered.") || r081Focused(s) || strings.Contains(s, "with its label") {
		t.Fatalf("the challenger's later-run note stays as it was: %s", s)
	}
}

func TestReview081DisputesOnlyRerun(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	_, o := h.startHelper(HelperCodeReviewer)
	s := fmtOut(o)
	if !strings.Contains(s, "The code hasn't changed since your last counted run") || strings.Contains(s, "fresh pass first") || r081Focused(s) {
		t.Fatalf("no code change since run 1 gets the disputes note: %s", s)
	}
}

func TestReview081FocusedCoverageCopiedPasses(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	h.r081Tests("src/a_test.go", "package src\n")
	id, o := h.startHelper(HelperCodeReviewer)
	if !r081Focused(fmtOut(o)) {
		t.Fatalf("a test-only fix is a focused round: %s", fmtOut(o))
	}
	text := "# Code review 2\n## Findings\n- [70] should-fix: A returns a magic number (src/a.go:3 `func A() int`). Why: unclear. Fix: name it.\n## Set aside\n- none\n## Coverage\n- src/a.go: covered\n- src/a_test.go: covered\n"
	p := h.findingsPath(id)
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Write", map[string]any{"file_path": p})), "allow", "")
	write(t, p, text)
	if o := h.stopHelper(id, HelperCodeReviewer); o != nil {
		t.Fatalf("copied Coverage lines pass: %v", o)
	}
	if r := h.r081Run(2); !h.task().counts(r) {
		t.Fatalf("the focused run counts: %+v", r)
	}
}

func TestReview081GateFileGivesTheFullPass(t *testing.T) {
	h := provenTask(t)
	h.r081Tests("CLAUDE.md", "# Rules\nkeep it simple\n")
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- [60] should-fix: x (CLAUDE.md:2 `keep it simple`) and (src/a.go:3 `func A() int`)\n")
	h.r081Tests("CLAUDE.md", "# Rules\nkeep it simpler\n")
	_, o := h.startHelper(HelperCodeReviewer)
	if s := fmtOut(o); r081Focused(s) || !strings.Contains(s, "fresh pass first") || !strings.Contains(s, "CLAUDE.md") {
		t.Fatalf("a change to a gate file gets the full pass, cited or not: %s", s)
	}
}

func TestReview081UnreadableTreeGivesTheFullPass(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, r081Cited)
	bogus := strings.Repeat("ab", 20)
	h.r081Edit(func(r *HelperRun) { r.PrintBefore, r.PrintAfter = bogus, bogus })
	h.editA("3")
	_, o := h.startHelper(HelperCodeReviewer)
	if s := fmtOut(o); r081Focused(s) || !strings.Contains(s, "fresh pass first") || strings.Contains(s, "Files changed since") || strings.Contains(s, "hasn't changed") {
		t.Fatalf("an unreadable tree gets the full pass and no list: %s", s)
	}
}

func TestReview081MustFixNeedsFailsWhen(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	p := h.findingsPath(id)
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Write", map[string]any{"file_path": p})), "allow", "")
	bare := "# Code review 1\n## Findings\n- [90] must-fix: A should return 3 (src/a.go:3 `func A() int`). Why: the ticket. Fix: return 3.\n"
	write(t, p, h.complete(HelperCodeReviewer, bare))
	s, _ := h.stopHelper(id, HelperCodeReviewer)["reason"].(string)
	if !strings.Contains(s, `has no "Fails when: <input or state> -> <wrong result>"`) || !strings.Contains(s, "mark the finding should-fix") {
		t.Fatalf("a must-fix with no Fails when is sent back: %s", s)
	}
	fixed := strings.Replace(bare, "Fix: return 3.", "Fails when: A() is called -> it returns 2, not 3. Fix: return 3.", 1)
	write(t, p, h.complete(HelperCodeReviewer, fixed))
	if o := h.stopHelper(id, HelperCodeReviewer); o != nil {
		t.Fatalf("the same finding with Fails when counts: %v", o)
	}
	if r := h.r081Run(1); !h.task().counts(r) || len(r.Problems) > 0 {
		t.Fatalf("the run counts: %+v", r)
	}
}

func TestReview081MustFixLabelAndFailsWhenShape(t *testing.T) {
	for _, c := range []struct {
		item string
		bad  bool
	}{
		{"- [90] must-fix: x. Fails when: a -> b.", false},
		{"- [90] must-fix: x. Fails when: a → b.", false},
		{"- [90] must-fix: x. **Fails when:** a -> b.", false},
		{"- [90] must-fix: x. Fails when:", true},
		{"- [90] must-fix: x. Fails when: -> b", true},
		{"- [90] must-fix: x. Fails when: a ->", true},
		{"- [90] must-fix: x. Fails when: a, b", true},
		{"- [90] must-fix: x (src/a.go:3 `f`) -> wrong", true},
		{"- [60] should-fix: x", false},
		{"- [60] should-fix: x is not a must-fix", false},
		{"- [90] Must-fix: x", true},
		{"- [90] **must-fix**: x", true},
		{"- **[90] must-fix**: x", true},
		{"- [90] must-fix (security): x", true},
		{"- must-fix: x", true},
		{"- none", false},
	} {
		got := len(mustFixProblems("# R\n## Findings\n"+c.item+"\n## Checked and fine\n- [90] must-fix: a note outside the findings\n")) > 0
		if got != c.bad {
			t.Errorf("%q: sent back = %v, want %v", c.item, got, c.bad)
		}
	}
}

func TestReview081ShouldFixFromTheLastRunOnly(t *testing.T) {
	h := provenTask(t)
	tk := h.task()
	docs := TaskDocsDir(h.repo, tk.ID)
	add := func(kind, name, text string) {
		tk.Helpers = append(tk.Helpers, HelperRun{Kind: kind, AgentID: name, Findings: name, Started: Now(), Finished: Now()})
		write(t, filepath.Join(docs, name), text)
	}
	add(HelperCodeReviewer, "code-review-1.md", "## Findings\n- [60] should-fix: x\n")
	add(HelperCodeReviewer, "code-review-2.md", "## Findings\n- none\n## Checked and fine\n- the earlier should-fix x: fixed\n")
	if anyShouldFix(tk) {
		t.Fatal("a should-fix that the last run doesn't repeat was fixed")
	}
	add(HelperCodeReviewer, "code-review-3.md", "## Findings\n- [60] should-fix: y\n")
	if !anyShouldFix(tk) {
		t.Fatal("a should-fix in the last run needs the line")
	}
	tk.Helpers = tk.Helpers[:2]
	add(HelperSecurity, "security-review-1.md", "## Findings\n- [65] should-fix: z\n")
	if !anyShouldFix(tk) {
		t.Fatal("either reviewer's last run having a should-fix is enough")
	}
	tk.Helpers = append(tk.Helpers, HelperRun{Kind: HelperSecurity, AgentID: "s2", Findings: "security-review-2.md", Started: Now()})
	if !anyShouldFix(tk) {
		t.Fatal("a run still going isn't the last finished one")
	}
	must(t, os.Remove(filepath.Join(docs, "security-review-1.md")))
	if anyShouldFix(tk) {
		t.Fatal("no should-fix in any last run")
	}
}
