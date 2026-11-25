package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPlanFilesParsedFromBackticks(t *testing.T) {
	plan := "## Files\n" +
		"- `tests/pkg/conftest.py`: drop the stub loop (lines 17-27)\n" +
		"- `tests/test_a.py`: drop the setdefault loop (lines 17-24)\n" +
		"- `tests/test_new.py` (new): guard test\n" +
		"- Unused `MagicMock` imports in those files are removed if nothing else uses them.\n" +
		"- src/b.go, the loop\n" +
		"- src/c.go: new file for the helper\n" +
		"- ./src/d.go\n" +
		"- src/**/*.go\n" +
		"- `src/e/`\n"
	pc := CheckPlan(plan)
	want := []string{"tests/pkg/conftest.py", "tests/test_a.py", "tests/test_new.py", "Unused", "src/b.go", "src/c.go", "src/d.go", "src/**/*.go", "src/e/"}
	if !reflect.DeepEqual(pc.Files, want) {
		t.Errorf("files:\n got %q\nwant %q", pc.Files, want)
	}
	if wantNew := []string{"tests/test_new.py", "src/c.go"}; !reflect.DeepEqual(pc.NewFiles, wantNew) {
		t.Errorf("new files: got %q, want %q", pc.NewFiles, wantNew)
	}
	if pc := CheckPlan("## Files\n- `src/a.go\n"); len(pc.Files) != 1 || pc.Files[0] != "src/a.go" {
		t.Errorf("an unclosed backtick: %q", pc.Files)
	}
}

func TestCheckpointRefusesFilesThatArentThere(t *testing.T) {
	withFiles := func(files string) string {
		return strings.Replace(goodPlan, "## Files\n- src/a.go\n", "## Files\n"+files, 1)
	}
	for files, missing := range map[string]string{
		"- `src/a.go`: change A\n- Unused `MagicMock` imports are removed.\n": "Unused",
		"- `src/a.go`: change A\n- src/c.go\n":                                "src/c.go",
		"- `src/a.go`: change A\n- `src/c.go`: the new helper\n":              "src/c.go",
	} {
		h := groundedTask(t, "small")
		h.docs(DocPlan, withFiles(files))
		h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "names "+missing+", which isn't in the repo")
	}
	for _, files := range []string{
		"- `src/a.go`: change A\n- `src/c.go` (new): the new helper\n",
		"- `src/a.go`: change A\n- src/c.go: new file for the helper\n",
		"- `src/a.go`: change A\n- `src/b.go`, B too\n",
		"- `src/a.go`: change A\n- src/\n",
	} {
		h := groundedTask(t, "normal")
		h.docs(DocPlan, withFiles(files))
		h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
		h.docs(DocPlan, withFiles(files)+"## Challenge note\n- nothing changed\n")
		if _, r := decision(h.pre("AskUserQuestion", checkpointQ())); strings.Contains(r, "isn't in the repo") {
			t.Errorf("%q refused: %s", files, r)
		}
	}
	h := groundedTask(t, "small")
	must(t, os.Remove(filepath.Join(h.repo, "src", "b.go")))
	h.docs(DocPlan, withFiles("- src/a.go\n- src/b.go: removed\n"))
	if _, r := decision(h.pre("AskUserQuestion", checkpointQ())); strings.Contains(r, "isn't in the repo") {
		t.Errorf("a file the plan deletes, still in the last commit, refused: %s", r)
	}
}

func TestUndoIsNotOutsideThePlan(t *testing.T) {
	h := newHarness(t)
	outside := []string{"src/b.go", "src/c.go", "src/d.go", "src/e.go"}
	for _, f := range outside[1:] {
		write(t, filepath.Join(h.repo, f), "package src\n")
	}
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "more files")
	h.setup()
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, goodPlan)
	h.approve()
	h.post("Bash", map[string]any{"command": "true"}, nil)

	for _, f := range outside {
		write(t, filepath.Join(h.repo, f), "package src\n\nvar changed = 1\n")
	}
	if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, nil); reported(o) || !strings.Contains(fmtOut(o), "plan missed them") {
		t.Fatalf("four files outside the plan get the fix-the-plan note, not a stop: %v", o)
	}

	undo := "git checkout -- " + strings.Join(outside, " ")
	h.expect(h.bash(undo), "ask", "throws away uncommitted changes")
	run(t, h.repo, "git", "checkout", "--", outside[0], outside[1], outside[2], outside[3])
	o := h.post("Bash", map[string]any{"command": undo}, nil)
	if reported(o) || strings.Contains(fmtOut(o), "outside the approved plan") {
		t.Fatalf("undoing the files stopped the work again: %v", o)
	}
	if tk := h.task(); len(tk.OutsidePlan) != 0 || tk.OutsideCount() != 0 {
		t.Errorf("restored files still count as outside the plan: %v", tk.OutsidePlan)
	}

	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n\nvar again = 1\n")
	if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, nil); reported(o) {
		t.Errorf("one file outside the plan after the undo stopped the work: %v", o)
	}
	if n := h.task().OutsideCount(); n != 1 {
		t.Errorf("outside count after one new change = %d, want 1", n)
	}
}

func TestPartialUndoStillCounts(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approve()
	h.post("Bash", map[string]any{"command": "true"}, nil)
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nvar changed = 1\n")
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	h.post("Bash", map[string]any{"command": "./gen.sh"}, nil)

	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 3 }\n")
	h.post("Bash", map[string]any{"command": "./gen.sh"}, nil)
	tk := h.task()
	if _, ok := tk.OutsidePlan["src/b.go"]; !ok || tk.OutsideCount() != 2 {
		t.Errorf("a file changed again, not restored, must still count: %v (count %d)", tk.OutsidePlan, tk.OutsideCount())
	}
	if tk.BackToStart("src/b.go") || tk.BackToStart("src/c.go") || !tk.BackToStart("src/a.go") || !tk.BackToStart("src/never.go") {
		t.Error("BackToStart: b.go and c.go differ from the start, a.go is unchanged, never.go never existed")
	}
}
