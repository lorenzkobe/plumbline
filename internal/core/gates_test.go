package core

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// harness: a temp git repo, a temp Plumbline home, and a session.
type harness struct {
	t    *testing.T
	repo string
	sid  string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	clearServiceEnv(t) // a DATABASE_URL in the shell running the tests doesn't reach them
	repo := filepath.Join(home, "repo")
	must(t, os.MkdirAll(filepath.Join(repo, "src"), 0o755))
	run(t, repo, "git", "init", "-q", "-b", "main")
	run(t, repo, "git", "config", "user.email", "t@example.com")
	run(t, repo, "git", "config", "user.name", "T")
	write(t, filepath.Join(repo, "src", "a.go"), "package src\n\nfunc A() int { return 1 }\n")
	write(t, filepath.Join(repo, "src", "b.go"), "package src\n\nfunc B() int { return 2 }\n")
	write(t, filepath.Join(repo, ".gitignore"), "coverage/\n")
	// the "test suite": prints pytest-style output, fails when a FAIL file
	// exists (tests/red is a test file, for a [bug] case's failing run)
	write(t, filepath.Join(repo, "test.sh"), "#!/bin/sh\nif [ -f FAIL ] || [ -f tests/red ]; then echo 'FAILED tests/t.py::test_new'; echo '== 1 failed, 2 passed in 0.1s =='; exit 1; fi\necho 'PASS test.sh'; echo '== 3 passed in 0.1s =='\n")
	must(t, os.Chmod(filepath.Join(repo, "test.sh"), 0o755))
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "-m", "init")
	repo, _ = filepath.EvalSymlinks(repo)
	// Each refusal lands in a later message than the one before, unless a
	// test sets its own clock (repeat.go's sameMessageWindow).
	at := time.Unix(1_700_000_000, 0)
	blockNow = func() time.Time { at = at.Add(3 * time.Second); return at }
	t.Cleanup(func() { blockNow = time.Now })
	return &harness{t: t, repo: repo, sid: "sess-1"}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(p), 0o755))
	must(t, os.WriteFile(p, []byte(s), 0o644))
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command(args[0], args[1:]...)
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", args, err, b)
	}
	return string(b)
}

func (h *harness) in(tool string, input any, resp any) *HookInput {
	ti, _ := json.Marshal(input)
	var tr json.RawMessage
	if resp != nil {
		tr, _ = json.Marshal(resp)
	}
	return &HookInput{SessionID: h.sid, Cwd: h.repo, ToolName: tool, ToolInput: ti, ToolResponse: tr}
}

func decision(o Output) (string, string) {
	if o == nil {
		return "", ""
	}
	hs, ok := o["hookSpecificOutput"].(map[string]any)
	if !ok {
		return "", ""
	}
	d, _ := hs["permissionDecision"].(string)
	r, _ := hs["permissionDecisionReason"].(string)
	return d, r
}

func (h *harness) expect(o Output, want string, contains string) {
	h.t.Helper()
	d, r := decision(o)
	if d != want {
		h.t.Fatalf("decision = %q (%s), want %q", d, r, want)
	}
	if contains != "" && !strings.Contains(r, contains) {
		h.t.Fatalf("reason %q doesn't mention %q", r, contains)
	}
}

func (h *harness) pre(tool string, input any) Output { return PreToolUse(h.in(tool, input, nil)) }
func (h *harness) post(tool string, input, resp any) Output {
	return PostToolUse(h.in(tool, input, resp))
}
func (h *harness) bash(cmd string) Output { return h.pre("Bash", map[string]any{"command": cmd}) }

func (h *harness) setup() {
	h.t.Helper()
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.IgnoreChanges = []string{"coverage/**"}
	_, err := SaveSetup(h.repo, s)
	must(h.t, err)
	run(h.t, h.repo, "git", "add", ".plumbline.json")
	run(h.t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
}

func (h *harness) startTask(size string) *Task {
	h.t.Helper()
	pre, _ := DirtyFiles(h.repo)
	tk := &Task{ID: NewID(), Title: "fix A", Size: size, Root: h.repo, Branch: CurrentBranch(h.repo), Phase: PhaseUnderstanding, Created: Now(), Preexisting: pre}
	must(h.t, os.MkdirAll(TaskDocsDir(h.repo, tk.ID), 0o700))
	must(h.t, SaveTask(tk))
	// the CLI prints the id; the post hook binds it to the session
	h.post("Bash", map[string]any{"command": "plumbline task start --title 'fix A'"}, map[string]any{"stdout": "plumbline-task-id: " + tk.ID + "\n"})
	got, err := ActiveTask(h.repo, h.sid, "")
	if err != nil || got == nil || got.ID != tk.ID {
		h.t.Fatalf("task not bound: %v %v", got, err)
	}
	return got
}

func (h *harness) task() *Task {
	t, err := ActiveTask(h.repo, h.sid, "")
	must(h.t, err)
	return t
}

func (h *harness) docs(name, text string) string {
	p := filepath.Join(TaskDocsDir(h.repo, h.task().ID), name)
	o := h.pre("Write", map[string]any{"file_path": p, "content": text})
	h.expect(o, "allow", "")
	write(h.t, p, text)
	return p
}

func (h *harness) cp(n int, answer string) Output {
	h.t.Helper()
	q := map[string]any{"questions": []any{map[string]any{
		"question": "Checkpoint question", "header": "Checkpoint " + string(rune('0'+n)),
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}},
	}}}
	if o := h.pre("AskUserQuestion", q); o != nil {
		return o
	}
	return h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Checkpoint question": answer}})
}

func TestFullTaskAndTryToBreak(t *testing.T) {
	h := newHarness(t)
	a := filepath.Join(h.repo, "src", "a.go")

	// Questions mode: reading is fine, editing is not.
	h.expect(h.bash("git status && cat src/a.go"), "allow", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "no task")
	// Outside a task a command that writes a project file is refused like
	// an edit (step 10b); one whose effect isn't known is Claude Code's
	// mode's call, and the post hook stops the work if it changed files.
	h.expect(h.bash("echo hi > src/a.go"), "deny", "no task")

	// Safety rules, always.
	h.expect(h.bash("git push origin main"), "deny", "pushing")
	h.expect(h.bash("claude plugin disable plumbline"), "deny", "claude plugin")
	h.expect(h.bash("plumbline pause"), "deny", "/plumbline:pause")
	h.expect(h.bash("echo '{}' | plumbline hook post"), "deny", "hooks")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, ".env")}), "deny", ".env")
	h.expect(h.pre("Read", map[string]any{"file_path": filepath.Join(h.repo, ".env")}), "ask", "")
	h.expect(h.pre("Read", map[string]any{"file_path": filepath.Join(h.repo, ".env.example")}), "", "")
	h.expect(h.bash("cat .env"), "ask", "")
	h.expect(h.bash("echo KEY=1 >> .env"), "deny", "")
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, ".claude", "settings.json")}), "deny", "protected")
	h.expect(h.bash("sed -i s/x/y/ ~/.claude/settings.json"), "deny", "")
	h.expect(h.bash("npm install left-pad"), "ask", "package")
	h.expect(h.bash("aws s3 ls"), "allow", "") // looking outside the laptop doesn't ask (2026-11-19)
	h.expect(h.bash("aws s3 rm s3://b/x"), "ask", "outside your laptop")
	h.expect(h.bash("psql -h prod.db.internal -c 'select 1'"), "allow", "")
	h.expect(h.bash("psql -h prod.db.internal -c 'delete from t'"), "ask", "")
	h.expect(h.pre("Agent", map[string]any{}), "", "")
	sub := h.in("Agent", map[string]any{}, nil)
	sub.AgentID = "helper-1"
	h.expect(PreToolUse(sub), "deny", "helpers")

	// Setup, then a task.
	h.setup()
	h.expect(h.bash("./test.sh"), "allow", "") // saved command runs before approval
	tk := h.startTask("normal")
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "approves the plan")
	h.expect(h.bash("node scripts/try.js"), "", "")
	h.expect(h.bash("node scripts/try.js > src/a.go"), "deny", "only look")
	stateFile := filepath.Join(TaskDir(h.repo, tk.ID), "state.json")
	h.expect(h.pre("Write", map[string]any{"file_path": stateFile}), "deny", "own files")
	h.expect(h.bash("rm "+stateFile), "deny", "own files")

	// Baseline run (before any edit).
	_, err := Execute(h.repo, tk, KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)

	// Checkpoint 1 without lookups or understanding: refused.
	h.expect(h.cp(1, "Approve"), "deny", "SocratiCode")
	// Pre-answered: refused.
	q := map[string]any{"questions": []any{map[string]any{"question": "Q", "header": "Checkpoint 1", "options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "No"}}}}, "answers": map[string]any{"Q": "Approve"}}
	h.expect(h.pre("AskUserQuestion", q), "deny", "pre-answered")

	// Do the lookups and read the file.
	h.post("mcp__socraticode__codebase_search", map[string]any{}, map[string]any{})
	h.post("mcp__plugin_x_mempalace__search", map[string]any{}, map[string]any{})
	h.post("mcp__serena__find_referencing_symbols", map[string]any{}, map[string]any{})
	// Quoting a file not opened: refused.
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3) and B (src/b.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.expect(h.cp(1, "Approve"), "deny", "src/b.go:3")
	h.post("Read", map[string]any{"file_path": a}, map[string]any{})
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}, map[string]any{})
	// A line past the end of the file: refused.
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:99)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.expect(h.cp(1, "Approve"), "deny", "only 3 lines")
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")

	// Free-text answer that starts with "approve" isn't approval.
	o := h.cp(1, "approve, but also change B")
	if h.task().Phase != PhaseUnderstanding {
		t.Fatalf("free text approved the checkpoint: %v", o)
	}
	h.cp(1, "Approve")
	if h.task().Phase != PhasePlanning {
		t.Fatal("checkpoint 1 approval not recorded")
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "")

	// Checkpoint 2 with an incomplete plan: refused.
	h.docs(DocPlan, "# Plan\nChange A.\n")
	h.expect(h.cp(2, "Approve"), "deny", "## Files")
	h.docs(DocPlan, "# Plan\n## Goal\nDone when: A works as planned.\n## Files\n- src/a.go\n## Cases\n- [bug] A returns 2 -> test: test.sh\n## Real-world proof\n- api: does not apply, because no API\n- ui: does not apply, because no UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n## Challenge note\n- nothing changed\n")
	h.expect(h.cp(2, "Approve"), "deny", "plumbline:challenger") // a normal task needs the challenger
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.cp(2, "Approve")
	if h.task().Phase != PhaseBuilding {
		t.Fatal("checkpoint 2 approval not recorded")
	}
	h.redRun()

	// Building runs without prompts: plan files and same-change files go
	// ahead; risky files outside the plan are refused and must be reported.
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "b.go")}), "", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "auth", "login.go")}), "deny", "For you")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "package.json")}), "deny", "risky")
	h.expect(h.bash("npm install left-pad"), "deny", "Needs your OK")
	h.expect(h.bash("git checkout -b fix/a"), "", "")
	// More than 3 files outside the plan is the user's decision.
	tk2 := h.task()
	for _, f := range []string{"src/c.go", "src/d.go", "src/e.go"} {
		write(t, filepath.Join(h.repo, f), "package src\n")
		tk2.NoteOutside(f, "edited")
	}
	must(t, SaveTask(tk2))
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "c.go")}), "", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "deny", "plan missed files")
	for _, f := range []string{"src/c.go", "src/d.go", "src/e.go"} {
		must(t, os.Remove(filepath.Join(h.repo, f))) // created and removed again: no longer counted
	}
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "f.go")}), "", "")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, map[string]any{})

	// A shell command that changes a file outside the plan stops the work.
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 3 }\n")
	o = h.post("Bash", map[string]any{"command": "gofmt -w src"}, map[string]any{})
	if o["continue"] == false || !strings.Contains(fmtOut(o), "src/b.go") {
		t.Fatalf("outside-plan change should be reported to Claude, not stop the work: %v", o)
	}
	run(t, h.repo, "git", "checkout", "src/b.go")
	h.post("Bash", map[string]any{"command": "git checkout src/b.go"}, map[string]any{})

	// Commit rules.
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "full test suite")
	h.expect(h.bash(`git commit --no-verify -am "fix: A returns 2"`), "deny", "no-verify")
	h.expect(h.bash(`git commit -am "fix: A returns 2

Co-Authored-By: Claude <noreply@anthropic.com>"`), "deny", "AI markers")
	h.expect(h.bash(`git commit -am "A returns 2"`), "deny", "feat:")
	h.expect(h.bash("git commit"), "deny", "-m")

	// Finish check: not proven, sent back once; again, it gives up and tells the user.
	stop := &HookInput{SessionID: h.sid, Cwd: h.repo}
	o = Stop(stop)
	if _, ok := o["hookSpecificOutput"]; !ok {
		t.Fatalf("stop should send back: %v", o)
	}
	stop.StopHookActive = true
	o = Stop(stop)
	if _, ok := o["systemMessage"]; !ok {
		t.Fatalf("second same send-back should stop and tell the user: %v", o)
	}
	// A stop to ask the user something goes through.
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: "Should I also fix B?"}); o != nil {
		t.Fatalf("question stop blocked: %v", o)
	}

	// Prove it: a new failure blocks, pre-existing don't.
	write(t, filepath.Join(h.repo, "FAIL"), "x")
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.post("Bash", map[string]any{"command": "touch FAIL"}, map[string]any{})
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "deny", "failing")
	must(t, os.Remove(filepath.Join(h.repo, "FAIL")))
	_, err = Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.expect(h.bash(`git commit -am "fix: A returns 2"`), "ask", "full suite passed")

	// The report must list what happened outside the plan.
	h.docs(DocReport, "# Report\n"+reportEnd+"Done.\n")
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo}); !strings.Contains(fmtOut(o), "src/auth/login.go") {
		t.Fatalf("report without outside-plan files should be sent back: %v", o)
	}
	h.runHelper(HelperCodeReviewer, "# Review 1\nnothing at 80 or more\n")
	h.runHelper(HelperPlanReviewer, "# Review 1\nmatches the plan\n") // a normal task gets both
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- nothing at 80 or more\n## Outside the plan\n- src/b.go (formatting)\n## For you\n- src/auth/login.go: not changed\n- package.json: left-pad not installed\n")
	// Proven, but the closing chat message must be the short report.
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: "All done. Want me to commit?"}); !strings.Contains(fmtOut(o), "final message") {
		t.Fatalf("a closing message without \"For you\" should be sent back: %v", o)
	}
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: "Fixed A. Proof: suite passes. For you: src/auth/login.go wasn't changed. Commit?"}); o != nil {
		t.Fatalf("proven task should stop cleanly: %v", o)
	}
	// Once reported, a later message (after the commit, say) isn't checked again.
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo, LastMessage: "Committed."}); o != nil {
		t.Fatalf("a reported task should stop cleanly: %v", o)
	}
	// Code changes after the report make it stale again.
	write(t, a, "package src\n\nfunc A() int { return 22 }\n")
	h.post("Edit", map[string]any{"file_path": a}, map[string]any{})
	if o := Stop(&HookInput{SessionID: h.sid, Cwd: h.repo}); o == nil {
		t.Fatal("stale proof should send back")
	}

	// Tampering with the state file is detected.
	sp := filepath.Join(TaskDir(h.repo, tk.ID), "state.json")
	b, _ := os.ReadFile(sp)
	must(t, os.WriteFile(sp, []byte(strings.Replace(string(b), `"building"`, `"building" `, 1)), 0o600))
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "outside Plumbline")
}

func fmtOut(o Output) string { b, _ := json.Marshal(o); return string(b) }

func TestChangedBeforeApprovalStops(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	write(t, filepath.Join(h.repo, "src", "a.go"), "changed\n")
	o := h.post("Bash", map[string]any{"command": "./script.sh"}, map[string]any{})
	if o["continue"] != false {
		t.Fatalf("change before approval should stop: %v", o)
	}
	// Ignored and git-ignored files don't count.
	write(t, filepath.Join(h.repo, "coverage", "x.json"), "{}")
	if o := h.post("Bash", map[string]any{"command": "./test.sh"}, map[string]any{}); o != nil {
		t.Fatalf("ignored change stopped the work: %v", o)
	}
}

func TestCombinedCheckpoint(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	h.post("mcp__socraticode__search", nil, nil)
	h.post("mcp__mempalace__search", nil, nil)
	tk := h.task()
	tk.ToolDown = map[string]string{"serena": "new file only"}
	must(t, SaveTask(tk))
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nsrc/a.go:1\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, "## Goal\nDone when: x works.\n## Files\n- src/a.go\n## Cases\n- [normal] x -> test: test.sh\n## Real-world proof\n- does not apply, because tiny\n## Needs your OK\nnone\n## Guards loosened\n- none\n")
	h.cp(2, "Approve")
	if h.task().Phase != PhaseBuilding {
		t.Fatal("combined checkpoint should approve a small task")
	}
	// The one combined checkpoint (header "Checkpoint") also works for a
	// normal task, and still needs the grounding and the understanding.
	h2 := newHarness(t)
	h2.setup()
	h2.startTask("normal")
	h2.docs(DocPlan, "## Files\n- src/a.go\n## Cases\n- [normal] x -> test: test.sh\n## Real-world proof\n- does not apply, because tiny\n## Needs your OK\n- `npm install zod`\n")
	q := map[string]any{"questions": []any{map[string]any{"question": "Go?", "header": "Checkpoint",
		"options": []any{map[string]any{"label": "Approve"}, map[string]any{"label": "Change something"}}}}}
	h2.expect(h2.pre("AskUserQuestion", q), "deny", "SocratiCode")
	h2.post("mcp__socraticode__search", nil, nil)
	h2.post("mcp__mempalace__search", nil, nil)
	h2.post("mcp__serena__find_symbol", nil, nil)
	h2.post("Read", map[string]any{"file_path": filepath.Join(h2.repo, "src", "a.go")}, nil)
	h2.docs(DocUnderstanding, "# U\nsrc/a.go:3\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h2.docs(DocPlan, "## Goal\nDone when: x works.\n## Files\n- src/a.go\n## Cases\n- [normal] x -> test: test.sh\n## Real-world proof\n- does not apply, because tiny\n## Needs your OK\n- `npm install zod`\n## Guards loosened\n- none\n## Challenge note\n- nothing changed\n")
	h2.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	// The plan installs a package (N21): the suite's before-state comes first.
	h2.expect(h2.pre("AskUserQuestion", q), "deny", "plumbline run test")
	_, err := Execute(h2.repo, h2.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h2.expect(h2.pre("AskUserQuestion", q), "", "")
	h2.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Approve"}})
	if h2.task().Phase != PhaseBuilding {
		t.Fatal("combined checkpoint should approve a normal task")
	}
	// A package named in the approved plan installs without a prompt.
	h2.expect(h2.bash("npm install zod"), "", "")
}

func TestPausedKeepsSafetyRules(t *testing.T) {
	h := newHarness(t)
	h.setup()
	must(t, SetPaused(h.repo, true))
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
	h.expect(h.bash("git push"), "deny", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, ".env")}), "deny", "")
	h.expect(h.bash(`git commit -am "fix: x

Generated with Claude Code"`), "deny", "AI markers")
	h.expect(h.bash(`git commit -am "fix: x"`), "ask", "paused")
}

func TestUserOnlyCommands(t *testing.T) {
	h := newHarness(t)
	h.setup()
	o := UserPromptExpansion(&HookInput{Cwd: h.repo, CommandName: "plumbline:pause"})
	if o == nil || !IsPaused(h.repo) {
		t.Fatal("pause didn't work")
	}
	UserPromptExpansion(&HookInput{Cwd: h.repo, CommandName: "plumbline:resume"})
	if IsPaused(h.repo) {
		t.Fatal("resume didn't work")
	}
	// Another plugin's "pause" is ignored.
	UserPromptExpansion(&HookInput{Cwd: h.repo, CommandName: "other:pause", CommandSource: "plugin", Prompt: "/other:pause"})
	if IsPaused(h.repo) {
		t.Fatal("other plugin paused Plumbline")
	}
	h.bash("git push") // a block to mark
	if _, err := MarkLastBlockWrong("it was fine"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Stats(1), "no-push 1") {
		t.Errorf("stats: %s", Stats(1))
	}
}

func TestPlumblineReadCommandsBeforeApproval(t *testing.T) {
	// The task skill needs these before the checkpoint (where to write the
	// notes, what's missing); they only read.
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	for _, c := range []string{"plumbline docs-dir", "plumbline status", "plumbline version", "plumbline task show", "plumbline task list", "plumbline stats", "plumbline setup show"} {
		h.expect(h.bash(c), "allow", "")
	}
	h.expect(h.bash("plumbline task replan"), "deny", "only look")
}
