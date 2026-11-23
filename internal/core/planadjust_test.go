package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func (h *harness) planEdit(text string) Output {
	h.t.Helper()
	p := h.docs(DocPlan, text)
	return h.post("Write", map[string]any{"file_path": p, "content": text}, map[string]any{})
}

func planWith(files ...string) string {
	return strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- "+strings.Join(files, "\n- ")+"\n", 1)
}

func (h *harness) changeOutside(files ...string) Output {
	h.t.Helper()
	for _, f := range files {
		write(h.t, filepath.Join(h.repo, f), "package src\n\nvar changed = 1\n")
	}
	return h.post("Bash", map[string]any{"command": "./gen.sh"}, nil)
}

func pastCap(t *testing.T) *harness {
	h := buildingTask(t)
	h.post("Bash", map[string]any{"command": "true"}, nil)
	h.changeOutside("src/b.go", "src/c.go", "src/d.go")
	return h
}

func TestCapAdjustsThePlan(t *testing.T) {
	h := buildingTask(t)
	h.post("Bash", map[string]any{"command": "true"}, nil)
	o := h.changeOutside("src/b.go", "src/c.go", "src/d.go")
	if stopped(o) || strings.Contains(fmtOut(o), "plan missed") || !strings.Contains(fmtOut(o), "src/d.go") {
		t.Fatalf("three files outside the plan get the usual note: %v", o)
	}
	o = h.changeOutside("src/e.go")
	out := fmtOut(o)
	if stopped(o) {
		t.Fatalf("a fourth file outside the plan must not stop the work: %v", o)
	}
	for _, want := range []string{"src/e.go", "plan missed them", "plan.md's ## Files", "keep going", "Don't stop or ask the user"} {
		if !strings.Contains(out, want) {
			t.Errorf("the note should say %q: %s", want, out)
		}
	}
	if strings.Contains(out, "user decides") {
		t.Errorf("the note must not hand the call to the user: %s", out)
	}
}

func TestPlanEditAddsFiles(t *testing.T) {
	h := pastCap(t)
	h.changeOutside("src/e.go")
	files := []string{"src/b.go", "src/c.go", "src/d.go", "src/e.go"}
	o := h.planEdit(planWith(files...))
	if !strings.Contains(fmtOut(o), "added to the plan's files") {
		t.Fatalf("the plan edit should say what joined the plan: %v", o)
	}
	h.planEdit(planWith(files...))
	tk := h.task()
	for _, f := range files {
		n := 0
		for _, p := range tk.PlanFiles {
			if p == f {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s is in the plan list %d times, want once: %v", f, n, tk.PlanFiles)
		}
		if _, ok := tk.OutsidePlan[f]; !ok {
			t.Errorf("%s must stay on the outside-the-plan record for the report: %v", f, tk.OutsidePlan)
		}
	}
	if n := tk.OutsideCount(); n != 0 {
		t.Fatalf("files added to the plan still count: %d", n)
	}
	h.planEdit(goodPlan)
	if !h.task().InPlan("src/b.go") {
		t.Error("an entry taken out of plan.md mid-build must stay in the plan list")
	}
	before := len(h.task().PlanFiles)
	for _, broken := range []string{"# Plan\n## Goal\nDone when: A works.\n", "# Plan\n## Files\nnot a list, just words\n\n## Cases\n"} {
		if o := h.planEdit(broken); o != nil && strings.Contains(fmtOut(o), "added") {
			t.Errorf("a plan with no usable ## Files adds nothing: %v", o)
		}
	}
	if n := len(h.task().PlanFiles); n != before {
		t.Errorf("a plan with no usable ## Files changed the plan list: %d -> %d", before, n)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "", "")
	h.docs(DocReport, "# Report\n")
	if miss := h.finishMissing(); !strings.Contains(miss, "under \"Outside the plan\" or \"For you\": src/b.go, src/c.go, src/d.go, src/e.go") {
		t.Errorf("the finish check must still want the added files in the report: %s", miss)
	}
}

func TestPlanEditKeepsRefusals(t *testing.T) {
	h := pastCap(t)
	tk := h.task()
	tk.Preexisting = map[string]string{"src/mine.go": "x"}
	must(t, SaveTask(tk))
	other := h.session("sess-2")
	other.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, filepath.Join(h.repo, "src", "theirs.go"), "package src\n")
	other.post("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "theirs.go")}, map[string]any{})
	entries := []string{"src/auth/login.go", ".github/workflows/ci.yml", "CLAUDE.md", "src/mine.go", "src/theirs.go", "src/", "src/*.go",
		`go.mo\d`, `src/au\th/login.go`, filepath.Join(h.repo, "src", "x.go"), "../other/x.go"}
	o := h.planEdit(planWith(entries...))
	out := fmtOut(o)
	for _, f := range []string{"src/auth/login.go (a risky", ".github/workflows/ci.yml (a risky", "CLAUDE.md (a risky", "src/mine.go (it held", "src/theirs.go (another Claude session", "src/ (a folder", "src/*.go (a folder",
		`go.mo\\d (a folder`, `src/au\\th/login.go (a folder`, "src/x.go (not a path inside the repo", "../other/x.go (not a path inside the repo"} {
		if !strings.Contains(out, f) {
			t.Errorf("the note should name %q as not added: %s", f, out)
		}
	}
	if strings.Contains(out, "added to the plan's files") {
		t.Errorf("nothing should be added: %s", out)
	}
	tk = h.task()
	for _, f := range []string{"src/auth/login.go", ".github/workflows/ci.yml", "CLAUDE.md", "src/mine.go", "src/theirs.go", "src/x.go", "go.mod"} {
		if tk.InPlan(f) {
			t.Errorf("%s must not join the plan mid-build: %v", f, tk.PlanFiles)
		}
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "auth", "login.go")}), "deny", "risky")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, ".github", "workflows", "ci.yml")}), "deny", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "CLAUDE.md")}), "deny", "decides which checks run")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "mine.go")}), "deny", "unsaved")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "go.mod")}), "deny", "risky")
}

func TestEditAfterPlanAdjust(t *testing.T) {
	h := pastCap(t)
	f := filepath.Join(h.repo, "src", "f.go")
	h.expect(h.pre("Edit", map[string]any{"file_path": f}), "deny", "add it to plan.md's ## Files")
	h.planEdit(planWith("src/f.go (new)"))
	h.expect(h.pre("Edit", map[string]any{"file_path": f}), "", "")
	write(t, f, "package src\n\nvar f = 1\n")
	h.post("Edit", map[string]any{"file_path": f}, map[string]any{})
	h.docs(DocReport, "# Report\n")
	if miss := h.finishMissing(); !strings.Contains(miss, "src/f.go") {
		t.Errorf("a file added to the plan before its edit must still be on the report's list: %s", miss)
	}
}

func TestPlanWrittenFromTerminal(t *testing.T) {
	h := pastCap(t)
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan)
	write(t, p, planWith("src/f.go (new)"))
	o := h.post("Bash", map[string]any{"command": "cat > " + p + " <<'EOF'\n" + planWith("src/f.go (new)") + "EOF"}, map[string]any{})
	if !strings.Contains(fmtOut(o), "added to the plan's files: src/f.go") {
		t.Fatalf("a plan.md written from the terminal should be read too: %v", o)
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "", "")
}

func TestReplanClearsTheCount(t *testing.T) {
	h := pastCap(t)
	h.changeOutside("src/e.go")
	tk := h.task()
	tk.Phase = PhasePlanning
	must(t, SaveTask(tk))
	h.docs(DocPlan, planWith("src/b.go", "src/c.go", "src/d.go", "src/e.go"))
	h.approveOnly()
	tk = h.task()
	if tk.Phase != PhaseBuilding {
		t.Fatalf("the new approval wasn't recorded: %s", tk.Phase)
	}
	if n := tk.OutsideCount(); n != 0 {
		t.Fatalf("files the new plan lists still count: %d", n)
	}
}

func TestPlanEditRefusesFoldersNotesAndOwnRiskyPaths(t *testing.T) {
	h := pastCap(t)
	s, _, err := LoadSetup(h.repo)
	must(t, err)
	s.RiskyPaths = []string{"src/money/**"}
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	write(t, filepath.Join(h.repo, "src", "money", "rate.go"), "package money\n")
	write(t, filepath.Join(h.repo, "lib", "x.go"), "package lib\n")
	o := h.planEdit(planWith("src/money/rate.go", "lib", "Why:", "src/g.go (new)"))
	out := fmtOut(o)
	for _, want := range []string{"src/money/rate.go (a risky", "lib (a folder", "Why (not in the repo", "added to the plan's files: src/g.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("the note should say %q: %s", want, out)
		}
	}
	tk := h.task()
	for _, f := range []string{"src/money/rate.go", "lib", "Why"} {
		if tk.InPlan(f) {
			t.Errorf("%s must not join the plan mid-build: %v", f, tk.PlanFiles)
		}
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "money", "rate.go")}), "deny", "risky")
}
