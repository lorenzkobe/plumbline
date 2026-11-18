package core

import (
	"path/filepath"
	"strings"
	"testing"
)

// Fixes from the first smoke run (0.6.12).

// TB1: plumbline task start followed by commands that only read binds the
// task, as a start on its own line does.
func TestTaskStartWithReadsBinds(t *testing.T) {
	for _, tail := range []string{" && plumbline docs-dir && git status --short", "; git status", " && ls src && pwd"} {
		h := newHarness(t)
		tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(tk))
		h.post("Bash", map[string]any{"command": "plumbline task start --title x" + tail}, map[string]any{"stdout": "plumbline-task-id: " + tk.ID + "\nTask started.\n?? notes.md\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != tk.ID {
			t.Fatalf("%q: the started task wasn't bound", tail)
		}
	}
}

// TB1: what may follow the start is narrow: a command that can print any
// text (echo, cat, a file's lines) could print another task's id.
func TestTaskStartWithPrinterBindsNothing(t *testing.T) {
	for _, tail := range []string{" && echo hi", "; cat notes.txt", " && grep -r id .", " && touch x"} {
		h := newHarness(t)
		other := &Task{ID: NewID(), Title: "other", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(other))
		h.post("Bash", map[string]any{"command": "plumbline task start --title x" + tail}, map[string]any{"stdout": "plumbline-task-id: " + other.ID + "\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got != nil {
			t.Fatalf("%q bound the session to %s", tail, got.ID)
		}
	}
	// two different ids in the output: neither binds
	h := newHarness(t)
	a := &Task{ID: NewID(), Title: "a", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
	b := &Task{ID: NewID() + "b", Title: "b", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
	must(t, SaveTask(a))
	must(t, SaveTask(b))
	h.post("Bash", map[string]any{"command": "plumbline task start --title x && git log -1"}, map[string]any{"stdout": "plumbline-task-id: " + a.ID + "\ncommit 1\n    plumbline-task-id: " + b.ID + "\n"})
	if got, _ := ActiveTask(h.repo, h.sid, ""); got != nil {
		t.Fatalf("two ids in the output bound %s", got.ID)
	}
}

// TB1: when the session has no task but this branch has an open task no
// session holds, the refusals say how to pick it up.
func TestUnboundTaskIsNamed(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
	must(t, SaveTask(tk))
	want := "plumbline task adopt " + tk.ID
	h.expect(h.pre("Edit", map[string]any{"file_path": h.repo + "/src/a.go"}), "deny", want)
	h.expect(h.pre("Write", map[string]any{"file_path": TaskDocsDir(h.repo, tk.ID) + "/notes.md"}), "deny", want)
	// bound to another session: not offered
	tk.SessionID = "someone-else"
	must(t, SaveTask(tk))
	_, r := decision(h.pre("Edit", map[string]any{"file_path": h.repo + "/src/a.go"}))
	if strings.Contains(r, "task adopt") {
		t.Fatalf("offered another session's task: %s", r)
	}
}

// RTK1: rtk runs a command and trims its output; the command is what's
// judged.
func TestRtkUnwraps(t *testing.T) {
	cases := map[string]string{
		"rtk git diff --stat":              "git diff --stat",
		"rtk -v git status":                "git status",
		"rtk proxy git push":               "git push",
		"rtk --ultra-compact err npm test": "npm test",
		"rtk run -c 'git push origin'":     "git push origin",
		"rtk run git push":                 "git push",
		"rtk lint src":                     "eslint src",
		"rtk read -n -m 20 a.txt b.txt":    "cat a.txt b.txt",
		"rtk proxy rtk git log":            "git log",
		"rtk gain --history":               "rtk gain --history",
		"rtk init -g":                      "rtk init -g",
	}
	for in, want := range cases {
		segs := Segments(in)
		got := []string{}
		for _, s := range segs {
			if len(s.Words) > 0 {
				got = append(got, strings.Join(s.Words, " "))
			}
		}
		if strings.Join(got, " | ") != want {
			t.Errorf("%q: got %q, want %q", in, strings.Join(got, " | "), want)
		}
	}
	for cmd, ro := range map[string]bool{"rtk gain": true, "rtk discover": true, "rtk -v gain": true, "rtk --version": true, "rtk init -g": false, "rtk config --create": false, "rtk trust": false, "rtk format": false, "rtk": false} {
		segs := Segments(cmd)
		if got := readOnlyLine(segs); got != ro {
			t.Errorf("%q read-only = %v, want %v", cmd, got, ro)
		}
	}
	if s := Segments("rtk $X status"); len(s) != 1 || s[0].Opaque == "" {
		t.Errorf("a subcommand from a variable must be unclear: %+v", s)
	}
	if s := Segments("rtk proxy $X"); len(s) != 1 || s[0].Opaque == "" {
		t.Errorf("a command from a variable must be unclear: %+v", s)
	}
}

// RTK1: the lead's gate and the post hook see through rtk too (its hook
// rewrites git status into rtk git status before the command runs).
func TestRtkLeadGate(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.expect(h.bash("rtk git push"), "deny", "pushing is always the user's job")
	h.expect(h.bash("rtk proxy git push --force"), "deny", "pushing")
	h.expect(h.bash("rtk git status && rtk gain"), "allow", "reads only")
	h.expect(h.bash("rtk proxy ./test.sh"), "allow", "saved")
	tk := h.startTask("small")
	tk.Phase = PhaseBuilding
	must(t, SaveTask(tk))
	h.post("Bash", map[string]any{"command": "rtk git commit -m 'fix: a'"}, map[string]any{"stdout": "[main 1234567] fix: a\n"})
	if got := h.task().Commits; got != 1 {
		t.Fatalf("a commit through rtk wasn't counted: %d", got)
	}
}

// CM1: finishing first left the task's work with no task to commit it in, so
// finish waits for the commit; the user's own files and what the setup
// ignores don't count.
func TestFinishWaitsForTheCommit(t *testing.T) {
	h := provenTask(t)
	write(t, filepath.Join(h.repo, "coverage", "out.txt"), "x") // ignored by the setup
	if got := UncommittedWork(h.task(), h.setupNow()); strings.Join(got, ",") != "src/a.go" {
		t.Fatalf("uncommitted work = %v, want src/a.go", got)
	}
	run(t, h.repo, "git", "add", "src/a.go")
	if got := UncommittedWork(h.task(), h.setupNow()); strings.Join(got, ",") != "src/a.go" {
		t.Fatalf("staged but not committed still counts: %v", got)
	}
	run(t, h.repo, "git", "commit", "-q", "-m", "fix: a")
	if got := UncommittedWork(h.task(), h.setupNow()); len(got) != 0 {
		t.Fatalf("after the commit: %v", got)
	}
	// the user's own unsaved work from before the task isn't the task's
	tk := h.task()
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 9 }\n")
	tk.Preexisting = map[string]string{"src/b.go": "x"}
	must(t, SaveTask(tk))
	if got := UncommittedWork(h.task(), h.setupNow()); len(got) != 0 {
		t.Fatalf("the user's own work counted: %v", got)
	}
}

// CM1: finishing with the work uncommitted is the user's call.
func TestFinishUncommittedAsks(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("small")
	h.expect(h.bash("plumbline task finish --uncommitted"), "ask", "uncommitted")
	h.expect(h.bash("plumbline task finish --uncommitted=true"), "ask", "uncommitted")
}

func (h *harness) setupNow() *Setup {
	s, _, _ := LoadSetup(h.repo)
	return s
}

// SA1: a background helper that has handed back isn't sent back by
// SubagentStop, so its handback gets the stop check first.
func TestHandbackGetsTheStopCheck(t *testing.T) {
	h := provenTask(t) // src/a.go:3 is "func A() int { return 2 }"
	id, _ := h.startHelper(HelperCodeReviewer)
	p := h.findingsPath(id)
	hand := func() Output {
		return PreToolUse(h.helperIn(id, HelperCodeReviewer, "SubagentHandback", map[string]any{"report": "done"}))
	}
	h.expect(hand(), "deny", "write your findings to")
	write(t, p, h.complete(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: wrong value (src/a.go:3)\n"))
	h.expect(hand(), "deny", "has no quote")
	// the same file again goes through (as a second stop does); the stop
	// then records the problems and the run doesn't count
	h.expect(hand(), "", "")
	in := &HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:" + HelperCodeReviewer, StopHookActive: true}
	if o := SubagentStop(in); o != nil {
		t.Fatalf("the stop after the handback: %v", o)
	}
	if r := h.run(HelperCodeReviewer, id); len(r.Problems) == 0 {
		t.Fatalf("the problems are recorded: %+v", r)
	}

	// a fixed file hands back with no refusal
	id2, _ := h.startHelper(HelperCodeReviewer)
	write(t, h.findingsPath(id2), h.complete(HelperCodeReviewer, "# Code review 2\n## Findings\n- [90] must-fix: wrong value (src/a.go:3 `return 2`)\n"))
	h.expect(PreToolUse(h.helperIn(id2, HelperCodeReviewer, "SubagentHandback", map[string]any{"report": "done"})), "", "")
	// the lead's own handback (no helper) isn't checked
	h.expect(h.pre("SubagentHandback", map[string]any{"report": "x"}), "", "")
}

// SR8: an ignore_changes glob whose folder git already ignores made git add
// refuse, so every fingerprint (and every plumbline run) failed.
func TestFingerprintWithIgnoredGlob(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\ndist/\n")
	write(t, filepath.Join(h.repo, "dist", "x.js"), "built")
	write(t, filepath.Join(h.repo, "coverage", "lcov.info"), "x")
	globs := []string{"dist/**", "dist/*.js", "coverage/lcov.info", "**/*.snap", "src/gen/**"}
	before, err := Fingerprint(h.repo, globs)
	if err != nil {
		t.Fatalf("fingerprint with globs on ignored folders: %v", err)
	}
	// the other globs still work: a change they name doesn't count
	write(t, filepath.Join(h.repo, "src", "gen", "z.go"), "package gen\n")
	write(t, filepath.Join(h.repo, "a.snap"), "x")
	if now, _ := Fingerprint(h.repo, globs); now != before {
		t.Fatal("a file an ignore glob names changed the fingerprint")
	}
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 5 }\n")
	if now, _ := Fingerprint(h.repo, globs); now == before {
		t.Fatal("a real change didn't change the fingerprint")
	}
	if got := strings.Join(DeadIgnoreGlobs(h.repo, globs), ","); got != "dist/**,dist/*.js,coverage/lcov.info" {
		t.Fatalf("setup should point out the globs git already covers: %s", got)
	}
}

// SEC1: a saved check that already failed before the task needs a run from
// before the first change, or the finish can't tell its old problems from
// new ones. The first change waits for those runs.
func TestFirstChangeWaitsForTheChecksRuns(t *testing.T) {
	h := checksHarness(t)
	write(t, filepath.Join(h.repo, "NO_security1"), "x") // already failing at the start
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: old problem")
	h.startTask("small")
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/: nothing\n")
	h.docs(DocPlan, goodPlan)
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.approveOnly()
	a := filepath.Join(h.repo, "src", "a.go")
	edit := map[string]any{"file_path": a}
	_, r := decision(h.pre("Edit", edit))
	for _, k := range []string{"plumbline run test", "plumbline run format", "plumbline run typecheck", "plumbline run security"} {
		if !strings.Contains(r, k) {
			t.Fatalf("the refusal should name %q: %s", k, r)
		}
	}
	h.expect(h.bash("sed -i '' s/1/2/ src/a.go"), "deny", "before the first change")
	h.expect(h.pre("mcp__serena__replace_symbol_body", map[string]any{"relative_path": "src/a.go", "name_path": "A", "body": "x"}), "deny", "before the first change")

	// runs after approval on the code as approved are the starting point
	for _, k := range []string{KindTest, KindFormat, KindTypecheck} {
		if r := h.runCheck(k); !r.Baseline {
			t.Fatalf("%s after approval, before any change, is a starting point", k)
		}
	}
	h.expect(h.pre("Edit", edit), "deny", "plumbline run security")
	// a waived check needs none
	tk := h.task()
	tk.Waivers = append(tk.Waivers, Waiver{Check: KindSecurity, Reason: "x", At: Now()})
	must(t, SaveTask(tk))
	h.expect(h.pre("Edit", edit), "", "")
	tk.Waivers = nil
	must(t, SaveTask(tk))
	if r := h.runCheck(KindSecurity); !r.Baseline || r.Exit == 0 {
		t.Fatalf("the failing security run is the starting point: %+v", r)
	}
	h.expect(h.pre("Edit", edit), "", "")

	// the change: the old problem doesn't block, a new one does
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", edit, nil)
	after := h.runCheck(KindSecurity)
	if after.Baseline {
		t.Fatal("a run after the change isn't a starting point")
	}
	if v := Judge(after, BaselineFor(LoadRuns(h.repo), h.task().ID, KindSecurity)); !v.OK {
		t.Fatalf("the old problem blocked: %s", v.Reason)
	}
	write(t, filepath.Join(h.repo, "NO_security2"), "x")
	if v := Judge(h.runCheck(KindSecurity), BaselineFor(LoadRuns(h.repo), h.task().ID, KindSecurity)); v.OK {
		t.Fatal("a new problem passed")
	}
}

// SEC1: when the code changed outside Claude after approval (the user edited
// a file), no run can be a starting point, so the first change isn't held.
func TestChecksRunsNotNeededWhenCodeMovedOutside(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	h.approveOnly()
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\n// the user's edit\n")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
}

// CFG1: changing the setup file isn't a code change, so proof stays (runs
// already count only for the saved command that made them).
func TestSetupFileIsNotCode(t *testing.T) {
	for _, ignored := range []bool{false, true} {
		h := newHarness(t)
		h.setup()
		if ignored {
			write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\n.plumbline.json\n")
		}
		before, err := Fingerprint(h.repo, nil)
		must(t, err)
		s, _, _ := LoadSetup(h.repo)
		s.Commands.Lint = "./lint.sh"
		_, err = SaveSetup(h.repo, s)
		must(t, err)
		if now, err := Fingerprint(h.repo, nil); err != nil || now != before {
			t.Fatalf("ignored=%v: a setup change moved the fingerprint (%v)", ignored, err)
		}
	}
}

// IND1: a phrase the plan holds too isn't pasted from another helper's
// notes, however its punctuation is spaced; and a pasted finding is still
// caught with its punctuation changed.
func TestPromptCheckReadsWordsNotPunctuation(t *testing.T) {
	h := provenTask(t)
	h.docs(DocPlan, goodPlan+"\n- The proof: every use mapped x->aInstant and y->bInstant in sameInstant.\n")
	h.runHelper(HelperPlanReviewer, "# Plan review 1\n- [20] the mapping x -> aInstant and y -> bInstant holds on every use (src/a.go:3 `func A() int`)\n")
	agent := func(prompt string) Output {
		return h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + HelperCodeReviewer, "prompt": prompt})
	}
	h.expect(agent("Review the rename: x -> aInstant and y -> bInstant in sameInstant."), "", "")
	h.runHelper(HelperCodeReviewer, "# Code review 1\n- [90] must-fix: the refund path never checks the order owner before paying out (src/a.go:3 `func A() int`)\n")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:" + HelperSecurity, "prompt": "Note: the refund-path never checks the order-owner, before paying out!"}), "deny", "code-review-1.md")
}

// Outside a task Plumbline leaves an ordinary command to Claude Code's own
// mode, still asks for its fixed list, and stops the work when the command
// changed the repo's files.
func TestOutsideATaskCommandsAreClaudeCodes(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.expect(h.bash("touch foo.txt"), "", "")
	h.expect(h.bash("serena --help && claude mcp list"), "", "")
	h.expect(h.bash("npm install left-pad"), "ask", "installs a package")
	h.expect(h.bash("claude mcp add serena -- serena"), "ask", "MCP servers")
	h.expect(h.bash("plumbline setup save --file s.json"), "ask", "Plumbline setup")
	h.expect(h.bash("cat .env"), "ask", ".env")
	h.expect(h.bash("aws s3 ls"), "ask", "outside your laptop")
	h.expect(h.bash("git push"), "deny", "pushing")
	h.expect(h.bash("git status"), "allow", "reads only")
	// the command ran and made a file: the work stops, naming it
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{}) // the session's first look
	write(t, filepath.Join(h.repo, "foo.txt"), "")
	o := fmtOut(h.post("Bash", map[string]any{"command": "touch foo.txt"}, map[string]any{"stdout": ""}))
	if !strings.Contains(o, "foo.txt") {
		t.Fatalf("the stop should name the new file: %s", o)
	}
}

// SR1: Serena 2's REPL runs Python: refused at every step with the flag that
// gives Serena's own tools back (while building it would get past the edit
// gates).
func TestSerenaREPLIsRefused(t *testing.T) {
	h := newHarness(t)
	h.setup()
	in := map[string]any{"code": "open('src/a.go','w').write('x')"}
	h.expect(h.pre("mcp__serena__serena_repl", in), "deny", "--agent-interface tools")
	b := buildingTask(t)
	b.expect(b.pre("mcp__plugin_serena_serena__serena_repl", in), "deny", "--agent-interface tools")
	b.expect(b.pre("mcp__serena__find_symbol", map[string]any{"name_path": "A"}), "", "")
	if !serenaREPL("mcp__serena__repl") || serenaREPL("mcp__serena__replace_symbol_body") || serenaREPL("mcp__serena__replace_content") {
		t.Fatal("only the REPL tool is the REPL")
	}
}

// SR4: the laptop prompt says it changes Claude Code's own settings.
func TestLaptopPromptNamesClaudeCodeSettings(t *testing.T) {
	h := newHarness(t)
	h.expect(h.bash("plumbline setup laptop --attribution-off"), "ask", "Claude Code's own user settings")
	h.expect(h.bash("plumbline setup undo-laptop"), "ask", "puts back")
	h.expect(h.bash("plumbline setup show"), "allow", "")
}

// A > inside an awk string is text: the program only prints.
func TestAwkStringIsNotARedirect(t *testing.T) {
	for prog, ro := range map[string]bool{
		`length > 110 {c++} END {print c" lines >110 in src"}`: true,
		`length>110{print FILENAME": "FNR": "length}`:          true,
		`{print "a|b", $1}`:            true,
		`{print "say \"hi\" > there"}`: true,
		`{print > "out"}`:              false,
		`{print $1 > "/tmp/x"}`:        false,
		`{print | "sh"}`:               false,
		`{printf "%s" , $1 >> "log"}`:  false,
		`{print "x" | "sh"}`:           false,
		`BEGIN{system("touch x")}`:     false,
	} {
		if got := ReadOnly(Segments("awk '" + strings.ReplaceAll(prog, "'", "") + "' src/a.go")[0]); got != ro {
			t.Errorf("awk %s: read-only = %v, want %v", prog, got, ro)
		}
	}
}

// L6: Claude Code hands the hook the answers as a map (seen in the smoke
// run), so text that reads like "Q"="A" never approves: a free-text answer
// could carry it.
func TestAnswerTextNeverApproves(t *testing.T) {
	h := groundedTask(t, "small")
	h.docs(DocPlan, goodPlan)
	q := checkpointQ()
	h.expect(h.pre("AskUserQuestion", q), "", "")
	h.post("AskUserQuestion", q, `User has answered your questions: "Go?"="Approve". You can now continue.`)
	h.post("AskUserQuestion", q, map[string]any{"result": `"Go?"="Approve"`})
	if h.task().Phase == PhaseBuilding {
		t.Fatal("an answer read from text approved the plan")
	}
	h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Go?": "Approve"}})
	if h.task().Phase != PhaseBuilding {
		t.Fatal("the answers map should approve")
	}
}

// The prompt hook runs before a /plumbline: command does, so on that message
// it doesn't describe the state the command is about to change.
func TestPromptNoteOnAPlumblineCommand(t *testing.T) {
	h := newHarness(t)
	h.setup()
	must(t, SetPaused(h.repo, true))
	note := func(p string) string {
		return fmtOut(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: p}))
	}
	if n := note("/plumbline:resume"); strings.Contains(n, "is paused") || !strings.Contains(n, "/plumbline:resume") {
		t.Fatalf("resume note: %s", n)
	}
	if n := note("what does A do?"); !strings.Contains(n, "is paused") {
		t.Fatalf("an ordinary message still gets the state: %s", n)
	}
	if n := note("/plumbline:status"); !strings.Contains(n, "is paused") {
		t.Fatalf("a command that only shows keeps the state: %s", n)
	}
}
