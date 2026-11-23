package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cmdOnlyPlan = "# Plan\n## Goal\nDone when: the staging cache is cleared.\n## Files\n- none (commands only)\n" +
	"## Cases\n- [normal] the cache is empty after the run -> does not apply, because no file changes; the real-world proof shows it\n" +
	"## Real-world proof\n- api: the cache endpoint returns an empty list\n## Needs your OK\n- none\n## Challenge note\n- nothing changed\n"

func cmdOnlyTaskIn(h *harness, size, plan string) *harness {
	h.t.Helper()
	h.startTask(size)
	h.ground()
	h.docs(DocPlan, plan)
	if size != "small" {
		h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	}
	h.approveOnly()
	if tk := h.task(); !tk.CommandsOnly && CheckPlan(plan).CommandsOnly {
		tk.CommandsOnly = true
		must(h.t, SaveTask(tk))
	}
	return h
}

func cmdOnlyTask(t *testing.T, size string) *harness {
	t.Helper()
	return cmdOnlyTaskIn(docsHarness(t, nil), size, cmdOnlyPlan)
}

func (h *harness) cmdOnlyNow() bool {
	s, _, _ := LoadSetup(h.repo)
	return CommandsOnly(h.task(), s)
}

func TestCmdOnlyTaskFinish(t *testing.T) {
	for _, size := range []string{"small", "normal", "big"} {
		t.Run(size, func(t *testing.T) {
			h := cmdOnlyTask(t, size)
			if !h.task().CommandsOnly || !h.cmdOnlyNow() {
				t.Fatalf("a \"none (commands only)\" plan makes the task commands-only: %+v", h.task().PlanFiles)
			}
			if m := proofMissingNow(h, true); m != "" {
				t.Fatalf("a commands-only task needs no saved-check runs: %s", m)
			}
			if got := h.required(); got != "" {
				t.Fatalf("a commands-only task needs no reviewers: %s", got)
			}
			fm := h.finishMissing()
			for _, bad := range []string{"full test suite", "lint:", "build:", "e2e:", "qa:", "plumbline:" + HelperCodeReviewer, "plumbline:" + HelperPlanReviewer} {
				if strings.Contains(fm, bad) {
					t.Errorf("the finish of a commands-only task shouldn't need %q: %s", bad, fm)
				}
			}
			for _, want := range []string{"real-world proof (api)", "report:"} {
				if !strings.Contains(fm, want) {
					t.Errorf("the finish still needs %q: %s", want, fm)
				}
			}
			s, _, _ := LoadSetup(h.repo)
			_, err := Execute(h.repo, h.task(), KindProof, "api", "echo '[]'", s, io.Discard)
			must(t, err)
			h.docs(DocReport, "# Report\n"+reportEnd+"## For you\n- nothing\n")
			if fm := h.finishMissing(); strings.Contains(fm, "real-world proof") {
				t.Fatalf("the proof run counts: %s", fm)
			}
		})
	}
}

func TestCmdOnlyEditRefused(t *testing.T) {
	h := cmdOnlyTask(t, "big")
	for _, o := range []Output{
		h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}),
		h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "a_test.go")}),
		h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "docs", "guide.md")}),
		h.bash("echo 'package src' > src/b.go"),
	} {
		d, r := decision(o)
		if d != "deny" || !strings.Contains(r, "replan to change files") || strings.Contains(r, "plumbline run checks") || strings.Contains(r, "writes the tests from the plan first") {
			t.Errorf("an edit in a commands-only task is refused with the replan message: %s %s", d, r)
		}
	}
}

func TestCmdOnlyIgnoredAndNotes(t *testing.T) {
	h := cmdOnlyTask(t, "normal")
	for _, o := range []Output{
		h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "out", "gen.go")}),
		h.bash("echo 'package out' > out/gen.go"),
		h.bash("echo '{}' > coverage/x.json"),
		h.writeTool("docs/scratch-notes.md"),
	} {
		d, r := decision(o)
		if d == "deny" || strings.Contains(r, "replan to change files") {
			t.Errorf("an ignored file or an untracked note isn't refused as commands-only: %s %s", d, r)
		}
	}
	write(t, filepath.Join(h.repo, "docs", "scratch-notes.md"), "# notes\n")
	if !h.cmdOnlyNow() {
		t.Fatal("an untracked note it let through ends commands-only")
	}
	if w := UncommittedWork(h.task(), h.setupNow()); len(w) > 0 {
		t.Fatalf("an untracked note is no uncommitted work for a commands-only task: %v", w)
	}
}

func TestCmdOnlyRecomputed(t *testing.T) {
	h := cmdOnlyTask(t, "normal")
	a := filepath.Join(h.repo, "src", "a.go")
	orig, err := os.ReadFile(a)
	must(t, err)
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	if h.cmdOnlyNow() {
		t.Fatal("a repo file changed by a command ends commands-only")
	}
	fm := h.finishMissing()
	if !strings.Contains(fm, "full test suite") || !strings.Contains(fm, "plumbline:"+HelperCodeReviewer) {
		t.Fatalf("while a repo file is changed, the finish needs the suite and the code reviewer: %s", fm)
	}
	write(t, a, string(orig))
	if !h.cmdOnlyNow() {
		t.Fatal("with the change undone, the task is commands-only again")
	}

	write(t, filepath.Join(h.repo, "coverage", "x.json"), "{}\n")
	write(t, filepath.Join(h.repo, "out", "gen.go"), "package out\n")
	if !h.cmdOnlyNow() {
		t.Fatal("a change only to ignored files keeps the task commands-only")
	}
	if fm := h.finishMissing(); strings.Contains(fm, "full test suite") || strings.Contains(fm, "plumbline:"+HelperCodeReviewer) {
		t.Fatalf("an ignored file's change needs no suite or reviewer: %s", fm)
	}
}

func TestCmdOnlyGitError(t *testing.T) {
	h := cmdOnlyTask(t, "small")
	must(t, os.RemoveAll(filepath.Join(h.repo, ".git")))
	if h.cmdOnlyNow() {
		t.Fatal("when git fails, the task isn't commands-only")
	}
	if got := h.required(); got == "" {
		t.Fatal("when git fails, the full rules apply and a reviewer is needed")
	}
}

func TestCmdOnlyPlanCheck(t *testing.T) {
	pc := CheckPlan(cmdOnlyPlan)
	if len(pc.Problems) > 0 || !pc.CommandsOnly || len(pc.Files) != 0 {
		t.Fatalf("a commands-only plan passes the plan check: %+v", pc)
	}
	for _, v := range []string{"- **None** (commands only)", "- `none (commands only)`", "- None (Commands Only)"} {
		pc := CheckPlan(strings.Replace(cmdOnlyPlan, "- none (commands only)", v, 1))
		if len(pc.Problems) > 0 || !pc.CommandsOnly {
			t.Errorf("%q is the commands-only line: %v", v, pc.Problems)
		}
	}
	mixed := CheckPlan(strings.Replace(cmdOnlyPlan, "- none (commands only)\n", "- none (commands only)\n- src/a.go\n", 1))
	if mixed.CommandsOnly || !strings.Contains(strings.Join(mixed.Problems, "; "), "only line") {
		t.Fatalf("\"none\" with a real file is refused: %+v", mixed)
	}
	bare := CheckPlan(strings.Replace(cmdOnlyPlan, "- none (commands only)\n", "- none\n", 1))
	if bare.CommandsOnly || !strings.Contains(strings.Join(bare.Problems, "; "), "none (commands only)") {
		t.Fatalf("a bare \"none\" names the commands-only line: %+v", bare)
	}
	withTest := CheckPlan(strings.Replace(cmdOnlyPlan, "-> does not apply, because no file changes; the real-world proof shows it", "-> test: internal/core/cache_test.go", 1))
	if !strings.Contains(strings.Join(withTest.Problems, "; "), "commands only") {
		t.Fatalf("a commands-only case with a test is refused: %+v", withTest.Problems)
	}
	if CheckPlan(goodPlan).CommandsOnly {
		t.Fatal("a plan with files isn't commands-only")
	}
}

func TestCmdOnlyNeverHotfixOrUnplanned(t *testing.T) {
	h := docsHarness(t, nil)
	h.startTask("small")
	s, _, _ := LoadSetup(h.repo)
	if CommandsOnly(h.task(), s) {
		t.Fatal("a task with no plan yet is never commands-only")
	}
	tk := h.task()
	tk.Phase = PhaseBuilding
	must(t, SaveTask(tk))
	if CommandsOnly(h.task(), s) {
		t.Fatal("an empty files list alone never means commands-only")
	}

	hf := docsHarness(t, nil)
	if o := expand(hf, "hotfix", "PROJ-12: clear the cache"); !strings.Contains(fmtOut(o), "hotfix task") {
		t.Fatalf("the hotfix didn't start: %s", fmtOut(o))
	}
	tk = hf.task()
	tk.Phase = PhaseBuilding
	tk.CommandsOnly = true
	must(t, SaveTask(tk))
	if CommandsOnly(hf.task(), s) {
		t.Fatal("a hotfix is never commands-only")
	}
}

func TestCmdOnlyOlderTaskFile(t *testing.T) {
	var tk Task
	must(t, json.Unmarshal([]byte(`{"id":"x","root":"/r","phase":"building"}`), &tk))
	if tk.CommandsOnly {
		t.Fatal("an older task file reads as not commands-only")
	}
	b, err := json.Marshal(&Task{ID: "x"})
	must(t, err)
	if strings.Contains(string(b), "commands_only") {
		t.Fatalf("the field is left out when false: %s", b)
	}
}

func TestCmdOnlyApprovalStores(t *testing.T) {
	h := docsHarness(t, nil)
	h.startTask("small")
	h.ground()
	h.docs(DocPlan, cmdOnlyPlan)
	h.approveOnly()
	tk := h.task()
	if !tk.CommandsOnly {
		t.Fatal("the approval stores commands-only from the plan's \"none (commands only)\" line")
	}
	tk.Phase = PhasePlanning
	must(t, SaveTask(tk))
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	if h.task().CommandsOnly {
		t.Fatal("a replan with files clears commands-only")
	}
}
