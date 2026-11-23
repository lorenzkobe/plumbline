package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// Fixes from smoke step 12a (0.7.2).

// PKG1: an install aimed outside the project (the scratchpad, a global
// install) doesn't change the project's packages; one into the project does.
func TestUpgradePlanOutsideProject(t *testing.T) {
	h := newHarness(t)
	scratch := t.TempDir()
	yes := []string{
		"npm install zod@3",
		"npm install --prefix ./tools zod",
		"npm install --prefix=" + h.repo + "/tools zod",
		"cd src && npm i zod",
		"pip install --target ./vendor requests",
	}
	no := []string{
		"npm install --prefix " + scratch + "/e2e @playwright/test@1.49.1",
		"npm install --prefix=" + scratch + " x",
		"npm i -g typescript",
		"npm install --global typescript",
		"npm install --location=global typescript",
		"pip install --user black",
		"pip install --target " + scratch + " requests",
		"cd " + scratch + " && npm install x",
		"npx --prefix " + scratch + "/e2e playwright install chromium",
	}
	for _, c := range yes {
		if !upgradePlan(PlanCheck{Approved: []string{c}}, h.repo) {
			t.Errorf("changes the project's packages: %s", c)
		}
	}
	for _, c := range no {
		if upgradePlan(PlanCheck{Approved: []string{c}}, h.repo) {
			t.Errorf("installs outside the project: %s", c)
		}
	}
	// One command of a line installing into the project is enough.
	if !upgradePlan(PlanCheck{Approved: []string{"npm i -g x && npm install zod"}}, h.repo) {
		t.Error("the second install goes into the project")
	}
	// A dependency file in the plan still counts, whatever the commands do.
	if !upgradePlan(PlanCheck{Files: []string{"package.json"}, Approved: []string{"npm i -g x"}}, h.repo) {
		t.Error("package.json in the plan's files changes packages")
	}
}

// CASE1: saving plan.md names what the plan question would refuse in the
// plan itself (a case without its test), not only lookups and file:line.
func TestPlanSaveNamesPlanRules(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_referencing_symbols"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	plan := smallPlan("src/a.go") + "- [concurrent] Covered by the repeat-safe case above.\n"
	p := h.docs(DocPlan, plan)
	o := fmtOut(h.post("Edit", map[string]any{"file_path": p}, nil))
	if !strings.Contains(o, "before the plan question") || !strings.Contains(o, "[concurrent] Covered") || !strings.Contains(o, `needs \"test: `) {
		t.Fatalf("the case without its test isn't named after the save: %s", o)
	}
	// The checkpoint refuses the same thing.
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "[concurrent] Covered")
	// Fixed: nothing left to say, and the question goes through.
	write(t, p, smallPlan("src/a.go"))
	o = fmtOut(h.post("Write", map[string]any{"file_path": p}, nil))
	if strings.Contains(o, "before the plan question") {
		t.Fatalf("nothing left, but: %s", o)
	}
	// PLAN3, HID2: then how to show the plan and ask.
	if !strings.Contains(o, "Done When") || !strings.Contains(o, "two or three full sentences") || !strings.Contains(o, "the problem, the fix and the risk") {
		t.Fatalf("the ready note doesn't say how to show the plan: %s", o)
	}
	if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" {
		t.Fatalf("the fixed plan was refused: %s", r)
	}
}

// PKG1 at the checkpoint: a plan whose only install goes to the scratchpad
// needs no saved check runs first.
func TestScratchInstallNeedsNoBaseline(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_referencing_symbols"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	scratch := t.TempDir()
	h.docs(DocPlan, smallPlan("src/a.go")+"## Needs your OK\n- `npm install --prefix "+scratch+"/e2e @playwright/test@1.49.1`\n")
	if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" {
		t.Fatalf("a scratchpad install was read as a package change: %s", r)
	}
	b := newHarness(t)
	b.setup()
	b.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_referencing_symbols"} {
		b.post(tool, nil, nil)
	}
	b.post("Read", map[string]any{"file_path": filepath.Join(b.repo, "src", "a.go")}, nil)
	b.docs(DocPlan, smallPlan("src/a.go")+"## Needs your OK\n- `npm install zod@3`\n")
	b.expect(b.pre("AskUserQuestion", checkpointQ()), "deny", "changes packages")
}

// PLAN3: a normal task's ready note names the Title Case sections, and only
// once the challenger has run (before that, the challenger is next).
func TestReadyPlanNoteNormal(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_referencing_symbols"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# Understanding\nA returns 1 (src/a.go:3).\n## Reuse\nnothing\n")
	p := h.docs(DocPlan, smallPlan("src/a.go")+"## Real-world proof\ndoes not apply, because nothing runs outside\n## Needs your OK\nnone\n")
	if o := fmtOut(h.post("Write", map[string]any{"file_path": p}, nil)); strings.Contains(o, "The Problem") {
		t.Fatalf("the challenger hasn't run yet, so it's not ready: %s", o)
	}
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	p = h.docs(DocPlan, smallPlan("src/a.go")+"## Real-world proof\ndoes not apply, because nothing runs outside\n## Needs your OK\nnone\n## Challenge note\n- nothing changed\n")
	o := fmtOut(h.post("Edit", map[string]any{"file_path": p}, nil))
	for _, want := range []string{"Done When", "The Problem", "The Fix", "Title Case", "path on the last line", "the problem, the fix and the risk"} {
		if !strings.Contains(o, want) {
			t.Fatalf("the ready note lacks %q: %s", want, o)
		}
	}
	if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" {
		t.Fatalf("the note said ready, but the question was refused: %s", r)
	}
}
