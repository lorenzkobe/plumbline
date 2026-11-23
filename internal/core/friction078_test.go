package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func noteHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	h := newHarness(t)
	for p, body := range files {
		write(t, filepath.Join(h.repo, p), body)
	}
	if len(files) > 0 {
		run(t, h.repo, "git", "add", "-A")
		run(t, h.repo, "git", "commit", "-q", "-m", "chore: files")
	}
	h.setup()
	return h
}

func allowedOut(o Output) bool {
	d, _ := decision(o)
	return d == "" || d == "allow"
}

func (h *harness) mustAllow(o Output, what string) {
	h.t.Helper()
	if !allowedOut(o) {
		d, r := decision(o)
		h.t.Fatalf("%s: decision %q (%s), want allowed", what, d, r)
	}
}

func (h *harness) writeTool(rel string) Output {
	return h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, rel), "content": "# notes\n"})
}

func docsHarness(t *testing.T, files map[string]string) *harness {
	t.Helper()
	h := hotfixHarness(t)
	s, _, _ := LoadSetup(h.repo)
	s.Commands.E2E = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\nout/\n")
	write(t, filepath.Join(h.repo, "docs", "guide.md"), "# Guide\n\nSet it up.\n")
	write(t, filepath.Join(h.repo, "docs", "components", "auth-guide.md"), "# Auth guide\n\nSign in.\n")
	write(t, filepath.Join(h.repo, "docs", "notes.txt"), "notes\n")
	for p, body := range files {
		write(t, filepath.Join(h.repo, p), body)
	}
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: docs")
	return h
}

func docsPlan(files ...string) string {
	return "# Plan\n## Goal\nDone when: the guide explains the setup.\n## Files\n- " + strings.Join(files, "\n- ") +
		"\n## Cases\n- [normal] the guide reads well -> test: does not apply, because it is prose\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Challenge note\n- nothing changed\n"
}

func (h *harness) ground() {
	h.t.Helper()
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
}

func docsTaskIn(h *harness, size string, files ...string) *harness {
	h.t.Helper()
	h.startTask(size)
	h.ground()
	h.docs(DocPlan, docsPlan(files...))
	if size != "small" {
		h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	}
	h.approveOnly()
	return h
}

func docsTask(t *testing.T, size string, files ...string) *harness {
	t.Helper()
	return docsTaskIn(docsHarness(t, nil), size, files...)
}

func (h *harness) change(rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	h.mustAllow(h.pre("Edit", map[string]any{"file_path": p}), "edit "+rel)
	write(h.t, p, body)
	h.post("Edit", map[string]any{"file_path": p}, nil)
}

func (h *harness) required() string {
	s, _, _ := LoadSetup(h.repo)
	return strings.Join(RequiredReviewers(h.task(), s), ",")
}

func closeQ() map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": "Close this task?", "header": "Close task",
		"options": []any{map[string]any{"label": "Close it"}, map[string]any{"label": "Keep it open"}}}}}
}

func (h *harness) closeAnswer(ans string) Output {
	h.t.Helper()
	q := closeQ()
	if d, r := decision(h.pre("AskUserQuestion", q)); d == "deny" {
		h.t.Fatalf("the close question was refused: %s", r)
	}
	return h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Close this task?": ans}})
}

func taskEvents(t *testing.T, id string) []Event {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(DataDir(), "logs", "*", safeName(id)+".jsonl"))
	var out []Event
	for _, p := range paths {
		out = append(out, readEvents(p)...)
	}
	return out
}

func phaseOf(t *testing.T, h *harness, id string) string {
	t.Helper()
	tk, err := LoadTask(h.repo, id)
	must(t, err)
	return tk.Phase
}

func TestUntrackedNoteNoTask(t *testing.T) {
	h := noteHarness(t, nil)
	h.mustAllow(h.writeTool("docs/sprint-23.md"), "Write tool, untracked note, no task")
	h.mustAllow(h.bash("echo hi > docs/sprint-23.md"), "terminal write, untracked note, no task")
	h.mustAllow(h.writeTool("notes.markdown"), "Write tool, .markdown note, no task")

	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, filepath.Join(h.repo, "docs", "sprint-23.md"), "hi\n")
	if o := h.post("Bash", map[string]any{"command": "echo hi > docs/sprint-23.md"}, map[string]any{}); o["continue"] == false {
		t.Fatalf("an untracked note written with no task stopped the work: %v", o)
	}
	write(t, filepath.Join(h.repo, "docs", "sprint-23.md"), "hi again\n")
	if o := h.post("Write", map[string]any{"file_path": filepath.Join(h.repo, "docs", "sprint-23.md")}, map[string]any{}); o["continue"] == false {
		t.Fatalf("the Write tool's post hook stopped the work for a note: %v", o)
	}
	if o := h.post("Bash", map[string]any{"command": "ls"}, map[string]any{}); o["continue"] == false {
		t.Fatalf("a later command stopped the work for the note: %v", o)
	}

	write(t, filepath.Join(h.repo, "src", "x.go"), "package src\n")
	if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{}); o["continue"] != false {
		t.Fatalf("a code file changed with no task should still stop the work: %v", o)
	}
}

func TestUntrackedNoteRefused(t *testing.T) {
	h := noteHarness(t, map[string]string{"README.md": "# Readme\n"})
	for _, rel := range []string{
		"README.md", "CLAUDE.md", ".claude/notes.md", "skills/x/ref.md", "commands/x.md",
		"testdata/golden.md", "requirements.txt", "page.mdx", "src/x.go",
	} {
		h.expect(h.writeTool(rel), "deny", "no task")
	}
	for _, rel := range []string{"README.md", "CLAUDE.md", "skills/x/ref.md", "testdata/golden.md", "requirements.txt", "page.mdx", "src/x.go"} {
		h.expect(h.bash("echo hi > "+rel), "deny", "no task")
	}
	write(t, filepath.Join(h.repo, "docs", "staged.md"), "# staged\n")
	run(t, h.repo, "git", "add", "docs/staged.md")
	h.expect(h.writeTool("docs/staged.md"), "deny", "no task")
	h.expect(h.bash("echo hi > docs/staged.md"), "deny", "no task")
}

func TestNoteNamedInCode(t *testing.T) {
	named := map[string]string{
		"src/embed.go": "package src\n\nimport _ \"embed\"\n\n//go:embed embed.md\nvar Embedded string\n",
		"src/load.go":  "package src\n\nimport \"os\"\n\nvar loaded, _ = os.ReadFile(\"docs/loaded.md\")\n",
		"CLAUDE.md":    "# Rules\n\n@docs/x.md\n",
	}
	h := noteHarness(t, named)
	h.mustAllow(h.writeTool("docs/free.md"), "an untracked note nothing names")
	h.expect(h.writeTool("src/embed.md"), "deny", "no task")
	h.expect(h.writeTool("docs/embed.md"), "deny", "no task")
	h.expect(h.writeTool("docs/loaded.md"), "deny", "no task")
	h.expect(h.writeTool("docs/x.md"), "deny", "no task")
	h.expect(h.bash("echo hi > docs/x.md"), "deny", "no task")

	free := docsTaskIn(docsHarness(t, named), "small", "docs/free.md (new)")
	free.lintBuild()
	free.change("docs/free.md", "# Free\n")

	for _, f := range []string{"docs/embed.md", "docs/x.md"} {
		g := docsTaskIn(docsHarness(t, named), "small", f+" (new)")
		g.lintBuild()
		g.expect(g.pre("Edit", map[string]any{"file_path": filepath.Join(g.repo, f)}), "deny", "plumbline run test")
	}
}

func TestNoteGitError(t *testing.T) {
	h := noteHarness(t, nil)
	h.mustAllow(h.writeTool("docs/ok.md"), "an untracked note while git works")
	must(t, os.WriteFile(filepath.Join(h.repo, ".git", "index"), []byte("not an index"), 0o644))
	h.expect(h.writeTool("docs/notes.md"), "deny", "no task")
	h.expect(h.bash("echo hi > docs/notes.md"), "deny", "no task")

	g := docsTask(t, "normal", "docs/guide.md")
	if got := g.required(); got != HelperCodeReviewer {
		t.Fatalf("a docs-only task needs the code reviewer only while git works: %s", got)
	}
	must(t, os.WriteFile(filepath.Join(g.repo, ".git", "index"), []byte("not an index"), 0o644))
	if got := g.required(); !strings.Contains(got, HelperPlanReviewer) {
		t.Fatalf("when git can't answer, the task isn't docs-only and keeps the plan reviewer: %s", got)
	}
}

func TestDocsOnlyTask(t *testing.T) {
	note := "docs/guide.md"
	h := docsTaskIn(docsHarness(t, map[string]string{"web/App.vue": "<template/>\n"}), "normal", note)
	tk := h.task()
	tk.ProofKinds = []string{"api"}
	must(t, SaveTask(tk))
	p := filepath.Join(h.repo, note)

	_, r := decision(h.pre("Edit", map[string]any{"file_path": p}))
	if !strings.Contains(r, "plumbline run lint") || !strings.Contains(r, "plumbline run build") || strings.Contains(r, "plumbline run test") {
		t.Fatalf("the first edit of a docs-only task needs lint and build, not the suite: %q", r)
	}
	h.lintBuild()
	h.change(note, "# Guide\n\nSet it up with the button.\n")

	m := proofMissingNow(h, true)
	if !strings.Contains(m, "lint:") || !strings.Contains(m, "build:") || strings.Contains(m, "full test suite") {
		t.Fatalf("after the edit, lint and build are needed on the final code but not the suite: %s", m)
	}
	h.lintBuild()
	if m := proofMissingNow(h, true); m != "" {
		t.Fatalf("a docs-only task needs no suite once lint and build pass: %s", m)
	}
	if m := proofMissingNow(h, false); m != "" {
		t.Fatalf("a docs-only task needs no related tests: %s", m)
	}
	if got := h.required(); got != HelperCodeReviewer {
		t.Fatalf("a docs-only task needs only the code reviewer, whatever its size: %s", got)
	}
	fm := h.finishMissing()
	for _, bad := range []string{"full test suite", "e2e:", "qa:", HelperPlanReviewer} {
		if strings.Contains(fm, bad) {
			t.Errorf("the finish of a docs-only task shouldn't need %q: %s", bad, fm)
		}
	}
	if !strings.Contains(fm, "plumbline:"+HelperCodeReviewer) {
		t.Errorf("the finish still needs the code reviewer: %s", fm)
	}
	h.expect(h.bash(`git commit -am "docs: explain the setup"`), "ask", "")

	b := docsTask(t, "big", "docs/guide.md")
	b.lintBuild()
	b.change("docs/guide.md", "# Guide\n\nSet it up twice.\n")
	b.lintBuild()
	bm := b.finishMissing()
	for _, bad := range []string{"qa:", "full test suite", HelperPlanReviewer} {
		if strings.Contains(bm, bad) {
			t.Errorf("a big docs-only task shouldn't need %q: %s", bad, bm)
		}
	}
}

func TestDocsOnlyReviewHolds(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nFirst fix.\n")
	h.lintBuild()
	h.runHelper(HelperCodeReviewer, "# Review 1\nnothing at 80 or more\n")
	report := "# Report\n" + reportEnd + "## Review\n- review-1: nothing at 80 or more\n## For you\n- nothing\n"
	h.docs(DocReport, report)
	if m := h.finishMissing(); strings.Contains(m, HelperCodeReviewer) {
		t.Fatalf("a fresh review counts: %s", m)
	}

	h.change("docs/guide.md", "# Guide\n\nSecond fix.\n")
	h.lintBuild()
	h.docs(DocReport, report)
	if m := h.finishMissing(); strings.Contains(m, HelperCodeReviewer) || strings.Contains(m, "run these again") {
		t.Fatalf("a second doc fix in a docs-only task keeps the code review: %s", m)
	}
	o := h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + HelperCodeReviewer, "prompt": "Review the change against plan.md."})
	h.expect(o, "deny", "still counts")
}

func TestDocsOnlyCodeEdit(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "deny", "docs-only")
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "new.go")}), "deny", "docs-only")
	h.expect(h.bash("echo 'package src' > src/b.go"), "deny", "docs-only")

	for _, o := range []Output{
		h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "out", "gen.go")}),
		h.bash("echo 'package out' > out/gen.go"),
		h.bash("echo '{}' > coverage/x.json"),
	} {
		d, r := decision(o)
		if d == "deny" || strings.Contains(r, "docs-only") {
			t.Errorf("an ignored file isn't refused as docs-only: %s %s", d, r)
		}
	}

	h.change("docs/guide.md", "# Guide\n\nFixed.\n")
	h.lintBuild()
	if m := h.finishMissing(); strings.Contains(m, "full test suite") {
		t.Fatalf("a docs-only task's finish needs no suite: %s", m)
	}
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	h.lintBuild()
	if m := h.finishMissing(); !strings.Contains(m, "full test suite") {
		t.Fatalf("a code change no gate saw ends docs-only, so the finish wants the suite: %s", m)
	}
}

func TestNotDocsOnly(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		edit  string
		extra map[string]string
	}{
		{"plan with a go file", []string{"docs/guide.md", "src/a.go"}, "docs/guide.md", nil},
		{"folder entry", []string{"docs/"}, "docs/guide.md", nil},
		{"gate file", []string{"CLAUDE.md"}, "CLAUDE.md", map[string]string{"CLAUDE.md": "# Rules\n"}},
		{"txt file", []string{"docs/notes.txt"}, "docs/notes.txt", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := docsTaskIn(docsHarness(t, c.extra), "normal", c.files...)
			h.lintBuild()
			h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, c.edit)}), "deny", "plumbline run test")
			if got := h.required(); !strings.Contains(got, HelperPlanReviewer) {
				t.Fatalf("a task that isn't docs-only keeps its reviewers: %s", got)
			}
		})
	}
	t.Run("hotfix", func(t *testing.T) {
		h := docsHarness(t, nil)
		if o := expand(h, "hotfix", "PROJ-12: the guide is wrong"); !strings.Contains(fmtOut(o), "hotfix task") {
			t.Fatalf("the hotfix didn't start: %s", fmtOut(o))
		}
		h.ground()
		h.docs(DocPlan, docsPlan("docs/guide.md")+"## Root cause\n- A returned 1 (src/a.go:3); this fix is the root-cause fix\n")
		h.approveOnly()
		h.lintBuild()
		h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "docs", "guide.md")}), "deny", "plumbline run test")
	})
}

func TestCloseTaskAnswer(t *testing.T) {
	u := groundedTask(t, "small")
	uid := u.task().ID
	o := u.closeAnswer("Close it")
	if phaseOf(t, u, uid) != PhaseCancelled {
		t.Fatalf("Close it at the understanding step didn't close the task: %v", o)
	}
	if got, _ := ActiveTask(u.repo, u.sid, ""); got != nil {
		t.Fatalf("the closed task is still active: %s", got.ID)
	}
	found := false
	for _, e := range taskEvents(t, uid) {
		found = found || strings.Contains(e.Detail, "closed at the user's answer")
	}
	if !found {
		t.Fatalf("the close isn't logged with its detail: %+v", taskEvents(t, uid))
	}

	p := groundedTask(t, "small")
	p.docs(DocPlan, goodPlan)
	tk := p.task()
	tk.Phase = PhasePlanning
	must(t, SaveTask(tk))
	if p.closeAnswer("Close it"); phaseOf(t, p, tk.ID) != PhaseCancelled {
		t.Fatal("Close it at the planning step didn't close the task")
	}

	b := buildingTask(t)
	bid := b.task().ID
	id, _ := b.startHelper(HelperBuilder)
	wt := b.run(HelperBuilder, id).Worktree
	o = b.closeAnswer("Close it")
	if phaseOf(t, b, bid) != PhaseCancelled {
		t.Fatalf("Close it while building didn't close the task: %v", o)
	}
	if !strings.Contains(fmtOut(o), wt) {
		t.Fatalf("the close should name the builder copy left on disk (%s): %s", wt, fmtOut(o))
	}
}

func TestCloseTaskRefused(t *testing.T) {
	h := buildingTask(t)
	id := h.task().ID
	pre := closeQ()
	pre["answers"] = map[string]any{"Close this task?": "Close it"}
	h.expect(h.pre("AskUserQuestion", pre), "deny", "pre-answered")

	hid, _ := h.startHelper(HelperCodeReviewer)
	h.expect(PreToolUse(h.helperIn(hid, HelperCodeReviewer, "AskUserQuestion", closeQ())), "deny", "")
	h.helperPost(hid, HelperCodeReviewer, "AskUserQuestion", closeQ(), map[string]any{"answers": map[string]any{"Close this task?": "Close it"}})
	if phaseOf(t, h, id) != PhaseBuilding {
		t.Fatal("a helper's close question closed the task")
	}

	for _, ans := range []string{"close it please", "Keep it open", ""} {
		h.closeAnswer(ans)
		if phaseOf(t, h, id) != PhaseBuilding {
			t.Fatalf("the answer %q closed the task", ans)
		}
	}

	other := h.session("sess-2")
	q := closeQ()
	other.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Close this task?": "Close it"}})
	if phaseOf(t, h, id) != PhaseBuilding {
		t.Fatal("another session's close answer closed this session's task")
	}

	n := noteHarness(t, nil)
	if o := n.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Close this task?": "Close it"}}); o["continue"] == false {
		t.Fatalf("a close question with no task stopped the work: %v", o)
	}
	if got, _ := ActiveTask(n.repo, n.sid, ""); got != nil {
		t.Fatalf("a close question with no task made one: %s", got.ID)
	}
}

func TestCloseSharedPaths(t *testing.T) {
	h := buildingTask(t)
	id := h.task().ID
	bid, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, bid).Worktree
	write(t, filepath.Join(wt, "built.txt"), "built\n")
	write(t, h.findingsPath(bid), h.complete(HelperBuilder, "# Build 1\n"))
	h.stopHelper(bid, HelperBuilder)
	tk := h.task()
	tk.Phase = PhasePlanning
	tk.Checkpoint2At = time.Time{}
	must(t, SaveTask(tk))
	q := map[string]any{"questions": []any{map[string]any{"question": "Go?", "header": "Checkpoint",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}, map[string]any{"label": "Drop it"}}}}}
	o := h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Drop it"}})
	if phaseOf(t, h, id) != PhaseCancelled {
		t.Fatalf("Drop it didn't close the task: %v", o)
	}
	if !strings.Contains(contextOf(o), "dropped") || !strings.Contains(contextOf(o), wt) {
		t.Fatalf("Drop it should say the task was dropped and name the copy left on disk (%s): %s", wt, contextOf(o))
	}
	cancelled := false
	for _, e := range taskEvents(t, id) {
		cancelled = cancelled || e.Type == "phase" && e.Rule == PhaseCancelled
	}
	if !cancelled {
		t.Fatal("Drop it isn't logged")
	}

	c := buildingTask(t)
	cid := c.task().ID
	b2, _ := c.startHelper(HelperBuilder)
	wt2 := c.run(HelperBuilder, b2).Worktree
	out := fmtOut(expand(c, "cancel", ""))
	if phaseOf(t, c, cid) != PhaseCancelled || !strings.Contains(out, "closed by the user") || !strings.Contains(out, wt2) {
		t.Fatalf("/plumbline:cancel should close the task and name the copy (%s): %s", wt2, out)
	}
}

func TestDocsOnlyReplanBaseline(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nA note first.\n")

	tk := h.task()
	tk.Phase = PhasePlanning
	tk.Checkpoint2At = time.Time{}
	must(t, SaveTask(tk))
	h.docs(DocPlan, docsPlan("docs/guide.md", "src/a.go"))
	h.approveOnly()

	a := filepath.Join(h.repo, "src", "a.go")
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "plumbline run test")
	r := h.runCheck(KindTest)
	if !r.Baseline {
		t.Fatalf("a suite run while only notes changed should count as the baseline: %+v", r)
	}
	h.mustAllow(h.pre("Edit", map[string]any{"file_path": a}), "the .go edit after the baseline")
}

func TestDocsOnlyUnseenBinary(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nMore.\n")
	if strings.Contains(proofMissingNow(h, true), "full test suite") {
		t.Fatalf("a docs-only task needs no suite: %s", proofMissingNow(h, true))
	}
	big := append([]byte{0}, make([]byte, 2<<20)...)
	must(t, os.MkdirAll(filepath.Join(h.repo, "vendor"), 0o755))
	must(t, os.WriteFile(filepath.Join(h.repo, "vendor", "lib.wasm"), big, 0o644))
	if m := proofMissingNow(h, true); !strings.Contains(m, "full test suite") {
		t.Fatalf("a large binary file no gate saw ends docs-only, so the suite is needed: %s", m)
	}
}

func TestNoteNamedByLocalInstructions(t *testing.T) {
	h := noteHarness(t, map[string]string{".gitignore": "CLAUDE.local.md\n"})
	write(t, filepath.Join(h.repo, "CLAUDE.local.md"), "Follow @docs/house-rules.md\n")
	h.expect(h.writeTool("docs/house-rules.md"), "deny", "no task")
	h.mustAllow(h.writeTool("docs/scratch.md"), "a note no instruction file names")
	must(t, os.MkdirAll(filepath.Join(h.repo, "pkg"), 0o755))
	write(t, filepath.Join(h.repo, "pkg", "AGENTS.md"), "See docs/pkg-notes.md\n")
	h.expect(h.writeTool("docs/pkg-notes.md"), "deny", "no task")
}

func TestCloseNoteNamesWhatsLeft(t *testing.T) {
	h := buildingTask(t)
	h.startHelper(HelperCodeReviewer)
	o := h.closeAnswer("Close it")
	ctx := contextOf(o)
	if !strings.Contains(ctx, "chose to close") || !strings.Contains(ctx, "still running") || !strings.Contains(ctx, HelperCodeReviewer) {
		t.Fatalf("the close note names the helper still running: %s", ctx)
	}
	if !strings.Contains(ctx, "still wanted") || !strings.Contains(ctx, "report: write it") {
		t.Fatalf("the close note names what the finish check still wanted: %s", ctx)
	}
}

func TestUntrackedNoteLinks(t *testing.T) {
	h := noteHarness(t, nil)
	must(t, os.MkdirAll(filepath.Join(h.repo, "docs"), 0o755))
	must(t, os.Symlink(filepath.Join("..", "src", "new.go"), filepath.Join(h.repo, "docs", "x.md")))
	h.expect(h.writeTool("docs/x.md"), "deny", "")
	must(t, os.Link(filepath.Join(h.repo, "src", "a.go"), filepath.Join(h.repo, "docs", "y.md")))
	h.expect(h.writeTool("docs/y.md"), "deny", "")
	write(t, filepath.Join(h.repo, "docs", "z.md"), "# z\n")
	h.mustAllow(h.writeTool("docs/z.md"), "a plain untracked note")
}

func TestCloseQuestionFormat(t *testing.T) {
	h := buildingTask(t)
	described := map[string]any{"questions": []any{map[string]any{"question": "Close this task?", "header": "Close task",
		"options": []any{map[string]any{"label": "Close it", "description": "Keeps all your work safe"}, map[string]any{"label": "Keep it open"}}}}}
	h.expect(h.pre("AskUserQuestion", described), "deny", "exactly the options")
	h.post("AskUserQuestion", described, map[string]any{"answers": map[string]any{"Close this task?": "Close it"}})
	if !h.task().Open() {
		t.Fatal("a close question with option descriptions closed the task")
	}
	two := map[string]any{"questions": []any{closeQ()["questions"].([]any)[0], map[string]any{"question": "Also?", "header": "Other",
		"options": []any{map[string]any{"label": "Yes"}, map[string]any{"label": "No"}}}}}
	h.expect(h.pre("AskUserQuestion", two), "deny", "on its own")
	o := h.closeAnswer("Close it")
	if h.task() != nil && h.task().Open() {
		t.Fatalf("the plain close question closes the task: %v", o)
	}
}

func TestDocsOnlyLateCheckNoBaseline(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nMore.\n")
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Typecheck = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	h.mustAllow(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "docs", "guide.md")}), "an edit after a check was saved mid-task")
}

func TestDocsOnlyCommitWording(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nMore.\n")
	h.lintBuild()
	_, r := decision(h.bash(`git commit -am "docs: explain the setup"`))
	if !strings.Contains(r, "docs-only") || strings.Contains(r, "full suite passed") {
		t.Fatalf("a docs-only commit prompt says no suite ran: %s", r)
	}
}

func TestDocsOnlyUserCodeInTree(t *testing.T) {
	h := docsHarness(t, nil)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 9 }\n")
	docsTaskIn(h, "small", "docs/guide.md")
	h.lintBuild()
	if m := proofMissingNow(h, true); !strings.Contains(m, "full test suite") {
		t.Fatalf("unsaved code in the tree keeps the suite required: %s", m)
	}
}

func TestUntrackedNoteCaseVariant(t *testing.T) {
	h := noteHarness(t, map[string]string{"docs/guide.md": "# Guide\n"})
	h.expect(h.writeTool("docs/Guide.md"), "deny", "no task")
	h.expect(h.writeTool("DOCS/guide.md"), "deny", "no task")
	h.expect(h.bash("echo x > docs/GUIDE.md"), "deny", "no task")
}

func TestDocsOnlyNoTestCommand(t *testing.T) {
	h := docsTask(t, "small", "docs/guide.md")
	h.lintBuild()
	h.change("docs/guide.md", "# Guide\n\nMore.\n")
	h.lintBuild()
	s, _, _ := LoadSetup(h.repo)
	s.Commands.Test = ""
	if m := strings.Join(ProofMissing(h.task(), s, true), "; "); strings.Contains(m, "no test command") {
		t.Fatalf("a docs-only task doesn't need a saved test command: %s", m)
	}
}
