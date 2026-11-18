package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// 0.7.0: the small path (UX1, decided with the user 2026-11-19).

// smallTask: a small task with the lookups done and src/<file> opened, but
// no understanding.md.
func smallTask(t *testing.T, riskyPaths []string, file string) *harness {
	t.Helper()
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main", RiskyPaths: riskyPaths}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	write(t, filepath.Join(h.repo, file), "package x\n\nfunc F() int { return 1 }\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "add "+file)
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, file)}, nil)
	return h
}

// A small plan: what changes, the files, the cases. No understanding.md,
// no proof or "Needs your OK" sections (none means none).
func smallPlan(file string) string {
	return "# Plan\nRename the local in F (" + file + ":3).\n## Files\n- " + file + "\n## Cases\n- [normal] F still returns 1 -> test: test.sh\n"
}

func TestSmallPlanNeedsNoUnderstanding(t *testing.T) {
	h := smallTask(t, nil, "src/lib/names.go")
	h.docs(DocPlan, smallPlan("src/lib/names.go"))
	if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" {
		t.Fatalf("a small plan was refused: %s", r)
	}
	// Without understanding.md, the plan holds the file:line references.
	b := smallTask(t, nil, "src/lib/names.go")
	b.docs(DocPlan, strings.Replace(smallPlan("src/lib/names.go"), " (src/lib/names.go:3)", "", 1))
	b.expect(b.pre("AskUserQuestion", checkpointQ()), "deny", "file:line")
	// A normal task still needs understanding.md.
	g := smallTask(t, nil, "src/lib/names.go")
	gt := g.task()
	gt.Size = "normal"
	must(t, SaveTask(gt))
	g.docs(DocPlan, smallPlan("src/lib/names.go"))
	g.expect(g.pre("AskUserQuestion", checkpointQ()), "deny", "understanding")
}

// The setup's own risky paths: Claude may keep a change there small (and
// says why). Built-in high-impact kinds stay never-small: the floor.
func TestSmallFloor(t *testing.T) {
	h := smallTask(t, []string{"src/lib/people.go"}, "src/lib/people.go")
	h.docs(DocPlan, smallPlan("src/lib/people.go"))
	if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" {
		t.Fatalf("a small change in a setup risky path was refused: %s", r)
	}
	for _, f := range []string{"src/auth/login.go", "src/payments/charge.go", "db/migrations/001_add.go", "src/permissions/check.go"} {
		g := smallTask(t, nil, f)
		g.docs(DocPlan, smallPlan(f))
		g.expect(g.pre("AskUserQuestion", checkpointQ()), "deny", "isn't small")
	}
}

// A small task gets one reviewer, the code reviewer; not the data or UX
// specialist its files would call for at normal size.
func TestSmallTaskOneReviewer(t *testing.T) {
	got := ReviewersFor("small", "", []string{"src/db/queries/orders.go", "src/components/Button.tsx"})
	if len(got) != 1 || got[0] != HelperCodeReviewer {
		t.Fatalf("small reviewers: %v", got)
	}
	normal := ReviewersFor("normal", "", []string{"src/db/queries/orders.go"})
	if !contains(normal, HelperData) {
		t.Fatalf("a normal task lost its data reviewer: %v", normal)
	}
	// Security still reviews security code, whatever the size.
	f := "src/auth/session.go"
	if contains(ReviewersFor("normal", "", []string{f}), HelperSecurity) && !contains(ReviewersFor("small", "", []string{f}), HelperSecurity) {
		t.Fatal("a small task dropped the security reviewer")
	}
}

func TestSmallPlanSectionsOptional(t *testing.T) {
	for _, p := range CheckPlanSized(smallPlan("src/a.go"), true).Problems {
		if strings.Contains(p, "Needs your OK") || strings.Contains(p, "Real-world proof") {
			t.Fatalf("small plan problem: %s", p)
		}
	}
	found := false
	for _, p := range CheckPlanSized(smallPlan("src/a.go"), false).Problems {
		found = found || strings.Contains(p, "Needs your OK")
	}
	if !found {
		t.Fatal("a normal plan no longer needs its Needs your OK section")
	}
}

// One stop: the plan question can drop the task. Only the question's own
// "Drop" option does it; typed text never does.
func TestCheckpointDropClosesTask(t *testing.T) {
	q := map[string]any{"questions": []any{map[string]any{"question": "Go?", "header": "Checkpoint",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}, map[string]any{"label": "Drop it"}}}}}
	h := smallTask(t, nil, "src/lib/names.go")
	h.docs(DocPlan, smallPlan("src/lib/names.go"))
	id := h.task().ID
	o := h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "drop it please"}})
	if tk, _ := LoadTask(h.repo, id); tk.Phase == PhaseCancelled {
		t.Fatalf("typed text dropped the task: %v", o)
	}
	o = h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Drop it"}})
	if tk, _ := LoadTask(h.repo, id); tk.Phase != PhaseCancelled {
		t.Fatalf("Drop it didn't close the task: %v", o)
	}
	if !strings.Contains(contextOf(o), "dropped") {
		t.Fatalf("Claude isn't told: %v", o)
	}
	// The next change can start a task in this checkout.
	if got, _ := ActiveTask(h.repo, h.sid, ""); got != nil {
		t.Fatalf("the dropped task is still active: %s", got.ID)
	}
}

// UX2: refusals name the way through, so Claude gets it right next time.
func TestRefusalsNameTheWay(t *testing.T) {
	h := smallTask(t, nil, "src/lib/names.go")
	h.docs(DocPlan, smallPlan("src/lib/names.go"))
	h.approveOnly()
	_, r := decision(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src/lib/names.go")}))
	if !strings.Contains(r, "plumbline run checks") {
		t.Fatalf("baseline refusal doesn't name run checks: %s", r)
	}
	docs := TaskDocsDir(h.repo, h.task().ID)
	_, r = decision(h.pre("Bash", map[string]any{"command": "cat > " + docs + "/report.md <<'EOF'\nx\nEOF"}))
	if !strings.Contains(r, "Write or Edit tool") {
		t.Fatalf("notes-from-the-shell refusal doesn't name the Write tool: %s", r)
	}
}

// One stop: the user asks for something else instead of answering the plan
// question. The next task this session starts closes the earlier one when
// it never got past planning (nothing changed yet); one that's building
// stays open.
func TestNewTaskReplacesUnapprovedOne(t *testing.T) {
	h := smallTask(t, nil, "src/lib/names.go")
	first := h.task().ID
	second := h.startTask("small")
	if tk, _ := LoadTask(h.repo, first); tk.Phase != PhaseCancelled {
		t.Fatalf("the unapproved first task is still %s", tk.Phase)
	}
	if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != second.ID {
		t.Fatal("the new task isn't the session's")
	}
	// A task that's building is real work: it stays.
	b := smallTask(t, nil, "src/lib/names.go")
	b.docs(DocPlan, smallPlan("src/lib/names.go"))
	b.approveOnly()
	building := b.task().ID
	b.startTask("small")
	if tk, _ := LoadTask(b.repo, building); !tk.Open() {
		t.Fatalf("a building task was closed by a new start: %s", tk.Phase)
	}
}
