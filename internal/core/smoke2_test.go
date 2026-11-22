package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixes from the second smoke run (0.6.13).

// TB2: piping plumbline's output into a filter that only drops lines (tail,
// head) still binds the task, for start and adopt alike.
func TestTaskStartPipedThroughFilterBinds(t *testing.T) {
	for _, tail := range []string{" 2>&1 | tail -30", " | tail -n 30", " | head -20", " 2>&1 | tail -30 && git status", " | tail -n +2", " | head -n5 | tail -1"} {
		h := newHarness(t)
		tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(tk))
		h.post("Bash", map[string]any{"command": "plumbline task start --title x" + tail}, map[string]any{"stdout": "plumbline-task-id: " + tk.ID + "\nTask started.\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != tk.ID {
			t.Fatalf("%q: the started task wasn't bound", tail)
		}
	}
}

func TestTaskAdoptPipedThroughFilterBinds(t *testing.T) {
	for _, tail := range []string{" 2>&1 | tail -20", " | head -5"} {
		h := newHarness(t)
		tk := &Task{ID: NewID(), Title: "x", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
		must(t, SaveTask(tk))
		h.post("Bash", map[string]any{"command": "plumbline task adopt " + tk.ID + tail}, map[string]any{"stdout": "plumbline-adopt-id: " + tk.ID + "\n"})
		if got, _ := ActiveTask(h.repo, h.sid, ""); got == nil || got.ID != tk.ID {
			t.Fatalf("%q: the adopted task wasn't bound", tail)
		}
	}
}

// TB2: a pipe stage that can print text of its own (a file it reads, a
// rewrite, a script) still binds nothing.
func TestPipedPrinterBindsNothing(t *testing.T) {
	stages := []string{" | tail -30 notes.txt", " | sed s/a/b/", " | awk '{print}'", " | tee out.txt", " | tail -30 > out.txt", " | cat - notes.txt", " | xargs echo", " | sort --files0-from=list", " | tail -n 5 notes.txt", " | head --lines=5 notes.txt"}
	for _, tail := range stages {
		for _, verb := range []string{"start --title x", "adopt "} {
			h := newHarness(t)
			other := &Task{ID: NewID(), Title: "other", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
			must(t, SaveTask(other))
			cmd, line := "plumbline task "+verb, "plumbline-task-id: "
			if verb == "adopt " {
				cmd += other.ID
				line = "plumbline-adopt-id: "
			}
			o := h.post("Bash", map[string]any{"command": cmd + tail}, map[string]any{"stdout": line + other.ID + "\n"})
			if got, _ := ActiveTask(h.repo, h.sid, ""); got != nil {
				t.Fatalf("%q%q bound the session to %s", cmd, tail, got.ID)
			}
			if !strings.Contains(contextOf(o), "on its own line") {
				t.Fatalf("%q%q: no word that the task isn't tied: %v", cmd, tail, o)
			}
		}
	}
}

// CC1: the user committed their changes themselves (`! git commit`). The
// files leave git status with their content untouched, so the next command
// changed nothing; a real revert still counts.
func TestCommittedFilesAreNotAChange(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 5 }\n")
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot: a.go dirty
	run(t, h.repo, "git", "commit", "-q", "-am", "mine")
	if o := h.post("Bash", map[string]any{"command": "git remote -v"}, map[string]any{}); o["continue"] == false {
		t.Fatalf("a commit by the user stopped the work as a change: %v", o)
	}
	// A revert rewrites the file (another size here): still a change.
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 777 }\n")
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot: b.go dirty
	run(t, h.repo, "git", "checkout", "--", "src/b.go")
	o := h.post("Bash", map[string]any{"command": "git checkout -- src/b.go"}, map[string]any{})
	if o["continue"] != false || !strings.Contains(fmtOut(o), "src/b.go") {
		t.Fatalf("a revert before approval should stop the work: %v", o)
	}
}

// HR1: a reviewer listing the notes folder sees only file names; reading
// another reviewer's findings or the report is still refused.
func TestReviewerListsNotesFolder(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	docs := TaskDocsDir(h.repo, h.task().ID)
	hb := func(cmd string) Output {
		return PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}))
	}
	for _, cmd := range []string{"ls " + docs, "ls -la " + docs + "/", "git diff; ls " + docs, "sed -n 1,20p src/a.go; ls " + docs} {
		if d, r := decision(hb(cmd)); d == "deny" {
			t.Fatalf("%q refused: %s", cmd, r)
		}
	}
	for _, cmd := range []string{"cat " + docs + "/plan-review-1.md", "grep -r x " + docs, "ls " + docs + " && cat " + docs + "/report.md", "ls " + docs + "/*", "head " + docs + " " + docs + "/report.md"} {
		h.expect(hb(cmd), "deny", "")
	}
}

// FR1: the lead's lookups just before the task starts, in the same
// session, count for the checkpoint; older ones and a helper's don't.
func TestLookupsBeforeStartCount(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.post("mcp__socraticode__codebase_search", nil, nil)
	h.post("mcp__mempalace__mempalace_search", nil, nil)
	tk := h.startTask("normal")
	tk, _ = LoadTask(h.repo, tk.ID)
	if tk.Lookups["socraticode"].Count != 1 || tk.Lookups["mempalace"].Count != 1 {
		t.Fatalf("lookups before the start didn't count: %v", tk.Lookups)
	}
	if _, ok := tk.Lookups["serena"]; ok {
		t.Fatalf("a lookup that never ran counted: %v", tk.Lookups)
	}
	// They're taken once: the next task in this session starts clean.
	h.post("Bash", map[string]any{"command": "plumbline task finish"}, nil)
	tk.Phase = PhaseDone
	must(t, SaveTask(tk))
	next := h.startTask("normal")
	if len(next.Lookups) != 0 {
		t.Fatalf("the first task's lookups carried over: %v", next.Lookups)
	}

	// Older than the window: not counted.
	h2 := newHarness(t)
	h2.setup()
	old := Now
	Now = func() time.Time { return old().Add(-2 * time.Hour) }
	h2.post("mcp__socraticode__codebase_search", nil, nil)
	Now = old
	if tk := h2.startTask("normal"); len(tk.Lookups) != 0 {
		t.Fatalf("a lookup from two hours ago counted: %v", tk.Lookups)
	}
}

// FR2: brew's read-only subcommands only read.
func TestBrewReadsOnly(t *testing.T) {
	h := groundedTask(t, "normal") // before approval: only reading runs
	for _, cmd := range []string{"brew --prefix postgresql@14", `ls "$(brew --prefix postgresql@14)/bin/postgres"`, "brew list --versions", "brew info node"} {
		if d, r := decision(h.pre("Bash", map[string]any{"command": cmd})); d == "deny" {
			t.Fatalf("%q refused: %s", cmd, r)
		}
	}
	for _, cmd := range []string{"brew install jq", "brew upgrade", "brew services start postgresql"} {
		h.expect(h.pre("Bash", map[string]any{"command": cmd}), "deny", "")
	}
}

// SR10: with no task open, every message's note says a change starts by
// asking, however small, before any edit. CM2: the rules say to ask before
// finishing with the change uncommitted.
func TestNoTaskNoteSaysAskFirst(t *testing.T) {
	h := newHarness(t)
	h.setup()
	note := contextOf(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: "rename x"}))
	// 0.7.0 (UX1): the task starts without a question; the plan question is the one stop.
	for _, want := range []string{"start it right away", "one-line", "Don't try the edit first", "one plan question"} {
		if !strings.Contains(note, want) {
			t.Fatalf("no-task note lacks %q: %s", want, note)
		}
	}
	for _, want := range []string{"Start the task right away", "however small", "Don't try an edit before they approve", "ask the user whether to commit first", "Never show the user task ids"} {
		if !strings.Contains(CoreRules(), want) {
			t.Fatalf("rules.md lacks %q", want)
		}
	}
}

// HR1: a helper writing its findings with the shell is told to use Edit or
// Write, not that "cat changes something".
func TestHelperShellWriteSaysUseEdit(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	docs := TaskDocsDir(h.repo, h.task().ID)
	o := PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat >> " + docs + "/code-review-1.md <<'EOF'\n- x\nEOF"}))
	_, r := decision(o)
	// HLP1: reviewers have no Edit tool, so the hint names Write only.
	if !strings.Contains(r, "rewrite it whole with the Write tool") || strings.Contains(r, `"cat" changes`) {
		t.Fatalf("shell write message: %s", r)
	}
	o = PreToolUse(h.helperIn(id, HelperCodeReviewer, "Bash", map[string]any{"command": "python3 fix.py 2>/dev/null"}))
	if _, r := decision(o); !strings.Contains(r, "rewrite it whole with the Write tool") || strings.Contains(r, "with the shell") {
		t.Fatalf("python3 message lacks the Edit/Write hint: %s", r)
	}
}

// PL1: "test: does not apply, because ..." is a case with no test, not a
// test file named "does".
func TestTestDoesNotApplyIsNotALink(t *testing.T) {
	pc := CheckPlan("## Files\n- src/a.go\n## Cases\n- [edge] a typo leaves an undefined name -> test: does not apply, because tsc refuses it\n- [normal] A works -> test: `src/a_test.go::TestA`\n")
	for _, p := range pc.Problems {
		if strings.Contains(p, "case") {
			t.Fatalf("case problem: %v", p)
		}
	}
	if len(pc.CaseTests) != 1 || pc.CaseTests[0] != "src/a_test.go" {
		t.Fatalf("case tests: %v", pc.CaseTests)
	}
}

// DP1 (smoke step 12b): in a pipeline project, a message about deploying
// carries a reminder of how the project ships, with its deploy_notes, so
// Claude doesn't offer a hand deploy. Off without the pipeline, with the
// devops part off, on unrelated messages and on Plumbline commands.
func TestPipelineNoteOnDeployMessages(t *testing.T) {
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main", DeployPipeline: true, DeployNotes: "Vercel deploys main\n through its git integration."}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	note := func(p string) string {
		return contextOf(UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: p}))
	}
	for _, p := range []string{"Run vercel --prod to deploy this.", "ship it", "push this to Production", "can we go live today?", "is it deployed?"} {
		got := note(p)
		for _, want := range []string{"pipeline deploys this project (Vercel deploys main through its git integration.)", "Don't deploy by hand (previews and test deploys too)", "don't offer to", "the user pushes"} {
			if !strings.Contains(got, want) {
				t.Errorf("%q: note lacks %q: %s", p, want, got)
			}
		}
	}
	for _, p := range []string{"rename the Other category", "fix the product page", "/plumbline:pause deploy later"} {
		if got := note(p); strings.Contains(got, "pipeline deploys") {
			t.Errorf("%q: unexpected pipeline note: %s", p, got)
		}
	}

	// Long notes are cut; no notes still names the pipeline.
	s.DeployNotes = strings.Repeat("é", maxDeployNotes+50)
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	if got := note("deploy"); !strings.Contains(got, strings.Repeat("é", maxDeployNotes)+"...)") || strings.Contains(got, strings.Repeat("é", maxDeployNotes+1)) {
		t.Errorf("long notes not cut at %d runes: %s", maxDeployNotes, got)
	}
	s.DeployNotes = ""
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	if got := note("deploy"); !strings.Contains(got, "pipeline deploys this project. Don't deploy by hand") {
		t.Errorf("no notes: %s", got)
	}

	// The refusal's own switches decide the note too.
	s.Parts = map[string]bool{devopsPartName: false}
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	if got := note("deploy"); strings.Contains(got, "pipeline deploys") {
		t.Errorf("devops part off: %s", got)
	}
	s.Parts, s.DeployPipeline = nil, false
	_, err = SaveSetup(h.repo, s)
	must(t, err)
	if got := note("deploy"); strings.Contains(got, "pipeline deploys") {
		t.Errorf("no pipeline: %s", got)
	}
}

// VER1 (smoke step 12c): asking a cloud or database tool for its version or
// help only prints locally, so it doesn't ask (`aws --version` asked "reaches
// systems outside your laptop"). The same tools with any command still go by
// the usual rules.
func TestCloudVersionAndHelpDontAsk(t *testing.T) {
	h := newHarness(t)
	for _, cmd := range []string{"aws --version", "rtk aws --version", "aws help", "gcloud --version", "kubectl version", "terraform -version", "vercel --version", "vercel -v", "ssh -V", "supabase --version", "psql --version", "psql -V", "mysql --version", "which aws; aws --version 2>&1"} {
		if d, r := decision(h.bash(cmd)); d == "ask" || d == "deny" {
			t.Errorf("%q: %s (%s), want no prompt", cmd, d, r)
		}
	}
	for _, cmd := range []string{"aws s3 rm s3://b/x", "aws s3 rm s3://b/x --version", "aws configure", "kubectl -v 9 delete pod x", "ssh -v host", "vercel", "psql -h db.example.com -c 'delete from t'"} {
		if d, _ := decision(h.bash(cmd)); d != "ask" && d != "deny" {
			t.Errorf("%q: %q, want it to ask or be refused", cmd, d)
		}
	}
}

// ASK1 (smoke step 12c): a cloud command that asks names its risk in plain
// words (deletes, changes, deploys), and the rules tell Claude its Bash
// description, the line on the prompt, says what it changes and why.
func TestCloudAskSaysTheRisk(t *testing.T) {
	h := newHarness(t)
	for cmd, want := range map[string]string{
		"aws s3 rm s3://b/x":                  "deletes something in aws, outside your laptop. Deleted data may not come back.",
		"aws s3 cp a.txt s3://b/a.txt":        "changes data or settings in aws, outside your laptop.",
		"aws configure set region x":          "outside your laptop",
		"gcloud compute instances delete vm1": "deletes something in gcloud",
	} {
		d, r := decision(h.bash(cmd))
		if d != "ask" || !strings.Contains(r, want) {
			t.Errorf("%q: %s (%s), want ask naming %q", cmd, d, r, want)
		}
	}
	for _, want := range []string{"Its Bash description is the line they see", "why the task needs it", "Commands that only look don't stop"} {
		if !strings.Contains(CoreRules(), want) {
			t.Errorf("rules lack %q", want)
		}
	}
}

// BLD3 (smoke step 13, from the 11c logs): builders for one-line label
// changes ran on the session's model. After approval, a normal or big task
// is told to start simple parts on Sonnet; a small task builds itself.
func TestApprovalTellsBuilderModel(t *testing.T) {
	for size, want := range map[string]bool{"small": false, "normal": true, "big": true} {
		got := approvalNext(&Task{Size: size, Phase: PhaseBuilding})
		if strings.Contains(got, `model: "sonnet"`) != want {
			t.Errorf("%s: builder model note present=%v, want %v: %s", size, !want, want, got)
		}
	}
	if got := approvalNext(&Task{Size: "normal", Phase: PhaseUnderstanding}); strings.Contains(got, "sonnet") {
		t.Errorf("before building: %s", got)
	}
}

// FL1 (smoke step 14a): a failed run names its failing tests in the one
// result line, so | tail -3 still shows them; Claude had guessed "flaky"
// and looked for them in Plumbline's own files.
func TestFailedNamesInResultLine(t *testing.T) {
	if got := FailedNames(nil); got != "" {
		t.Errorf("none: %q", got)
	}
	if got := FailedNames([]string{"a > x", "b > y"}); got != "a > x; b > y" {
		t.Errorf("two: %q", got)
	}
	if got := FailedNames([]string{"1", "2", "3", "4", "5"}); got != "1; 2; 3; and 2 more" {
		t.Errorf("five: %q", got)
	}
	if !strings.Contains(ownFilesFromShell, "names its failing tests") {
		t.Errorf("own-files refusal doesn't say where the failures are: %s", ownFilesFromShell)
	}
}

// LIVE1 (smoke step 14): a second window in the same folder is told up
// front that another window is working there, so it doesn't try a task
// (red box) or offer to close or take over the live task; once that
// window's session ends, its task is listed as left over.
func TestLiveOtherWindowToldUpFront(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	b := HookInput{SessionID: "sess-b", Cwd: h.repo}
	note := contextOf(UserPromptSubmit(&b))
	if !strings.Contains(note, "another Claude window is working on") || !strings.Contains(note, "claude -w") || strings.Contains(note, "start it right away") {
		t.Fatalf("B's message note should say another window works here: %s", note)
	}
	start := fmtOut(SessionStart(&HookInput{SessionID: "sess-b", Cwd: h.repo, Source: "startup"}))
	if !strings.Contains(start, "another Claude window is working on") || strings.Contains(start, "earlier sessions that have ended") {
		t.Fatalf("session start should list it as live: %s", start)
	}
	SessionEnd(&HookInput{SessionID: h.sid})
	if !SessionEnded(h.sid) {
		t.Fatal("the session end wasn't recorded")
	}
	note = contextOf(UserPromptSubmit(&b))
	if strings.Contains(note, "another Claude window is working on") {
		t.Fatalf("an ended session's task isn't live: %s", note)
	}
	start = fmtOut(SessionStart(&HookInput{SessionID: "sess-b", Cwd: h.repo, Source: "startup"}))
	if !strings.Contains(start, "earlier sessions that have ended") || !strings.Contains(start, "plumbline task adopt") {
		t.Fatalf("session start should list it as left over: %s", start)
	}
}

// SB1 (smoke step 16): untracking a Claude settings file asks instead of
// being refused (the file stays as it is); changing or deleting it is still
// refused. plumbline <subcommand> --help is a read, while planning too,
// except hook, which runs. The sandbox's refusal hands the user the command.
func TestUntrackSettingsAsksAndHelpReads(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.expect(h.bash("git rm --cached .claude/settings.local.json"), "ask", "stops git from tracking")
	h.expect(h.bash("git rm -q --cached -- .claude/settings.local.json"), "ask", "stops git from tracking")
	h.startTask("normal")
	// Before approval it's a change like any other: refused until the plan is approved.
	h.expect(h.bash("git rm --cached .claude/settings.local.json"), "deny", "isn't approved")
	for _, cmd := range []string{"git rm .claude/settings.local.json", "git rm -r --cached .claude", "rm .claude/settings.local.json", "echo x > .claude/settings.local.json", "git -c x=y rm --cached .claude/settings.local.json"} {
		if d, _ := decision(h.bash(cmd)); d != "deny" {
			t.Errorf("%q: %q, want refused", cmd, d)
		}
	}
	// A commit message that names the file isn't the file.
	if d, r := decision(h.bash("git commit -m \"chore: stop tracking .claude/settings.local.json\"")); strings.Contains(r, "protected") {
		t.Errorf("a commit message naming the settings file was refused as the file: %s %s", d, r)
	}
	if d, _ := decision(h.bash("git commit -F .claude/settings.local.json")); d != "deny" {
		t.Errorf("a commit reading the settings file isn't only a message: %q", d)
	}
	h.expect(h.bash("plumbline setup sandbox --help"), "allow", "")
	h.expect(h.bash("plumbline task finish -h"), "allow", "")
	if d, _ := decision(h.bash("plumbline hook post --help")); d == "allow" {
		t.Error("plumbline hook --help runs the hook, so it isn't a read")
	}
	run(t, h.repo, "git", "add", "-f", ".plumbline.json")
	must(t, os.MkdirAll(filepath.Join(h.repo, ".claude"), 0o755))
	write(t, filepath.Join(h.repo, ".claude", "settings.local.json"), "{}\n")
	run(t, h.repo, "git", "add", "-f", ".claude/settings.local.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "track settings")
	_, err := EnableSandbox(h.repo, nil)
	if err == nil || !strings.Contains(err.Error(), "! git rm --cached .claude/settings.local.json") || !strings.Contains(err.Error(), ".gitignore") {
		t.Fatalf("the refusal should hand the user the command: %v", err)
	}
}

// ST1 (smoke step 17): a task's report verdict line is its outcome in the
// standup, so "done" says what came of it; long lines are cut, a report
// without a verdict gives nothing.
func TestReportOutcome(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tk := h.startTask("small")
	if got := reportOutcome(tk); got != "" {
		t.Fatalf("no report: %q", got)
	}
	h.docs(DocReport, "# Report\n\n## Verdict\n\nReady with noted limitations: the AWS key is invalid, so S3 couldn't be checked.\nRollback: none.\n\n## What changed\nNothing.\n")
	if got := reportOutcome(tk); got != "Ready with noted limitations: the AWS key is invalid, so S3 couldn't be checked." {
		t.Fatalf("outcome: %q", got)
	}
	h.docs(DocReport, "# Report\n\n## Verdict\n"+strings.Repeat("x", maxOutcome+10)+"\n")
	if got := reportOutcome(tk); len([]rune(got)) != maxOutcome+3 {
		t.Fatalf("long verdict not cut: %d", len(got))
	}
	h.docs(DocReport, "# Report\n\n## Verdict\n\n## What changed\nx\n")
	if got := reportOutcome(tk); got != "" {
		t.Fatalf("empty verdict: %q", got)
	}
	rep := &StandupReport{Items: []*StandupItem{{Title: "x", Tasks: []StandupTask{{ID: tk.ID, Title: "x", Repo: "r", Phase: "done", Outcome: "Ready."}}}}}
	if !strings.Contains(rep.String(), "    outcome: Ready.") {
		t.Fatalf("the standup text lacks the outcome: %s", rep.String())
	}
}

// WV1 (smoke step 18d): a waive the command would refuse (no reason, or a
// check that can't be waived) is stopped before the user is asked, so their
// yes isn't spent on an error; the prompt names the check and the reason.
func TestWaiveNeedsReasonBeforeAsking(t *testing.T) {
	h := provenTask(t) // approved: before that, a waive is refused like any change
	h.expect(h.bash("plumbline waive bug-test"), "deny", `--reason "..."`)
	h.expect(h.bash(`plumbline waive bug-test --reason "   "`), "deny", "needs the check")
	h.expect(h.bash(`plumbline waive tests --reason "slow"`), "deny", "needs the check")
	h.expect(h.bash(`plumbline waive --reason "x"`), "deny", "needs the check")
	h.expect(h.bash(`plumbline waive bug-test --reason "it can't fail without the fix"`), "ask", "waive bug-test: it can't fail without the fix.")
	h.expect(h.bash(`plumbline waive --reason=no-browser proof:ui`), "ask", "waive proof:ui: no-browser.")
}

// ID1 (smoke step 18d): a plain /plumbline:cancel closes the one task left by
// a window that has ended, so the user isn't handed a task id; a task of a
// window that's still open needs its id.
func TestPlainCancelClosesTheLeftOverTask(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tk := h.startTask("normal")
	b := h.session("sess-2")
	if o := fmtOut(expand(b, "cancel", "")); !strings.Contains(o, "still open") {
		t.Fatalf("a live window's task isn't closed by a plain cancel: %s", o)
	}
	if x, _ := LoadTask(h.repo, tk.ID); !x.Open() {
		t.Fatal("the live window's task was closed")
	}
	SessionEnd(&HookInput{SessionID: h.sid})
	_, r := decision(b.bash("plumbline task start --title 'other work'"))
	if !strings.Contains(r, "(/plumbline:cancel, theirs to type)") {
		t.Fatalf("the refusal should hand the user a plain /plumbline:cancel: %s", r)
	}
	if o := fmtOut(expand(b, "cancel", "")); !strings.Contains(o, "closed by the user") {
		t.Fatalf("a plain cancel closes the left-over task: %s", o)
	}
	if x, _ := LoadTask(h.repo, tk.ID); x.Open() {
		t.Fatal("the left-over task is still open")
	}
	if o := fmtOut(expand(b, "cancel", "")); !strings.Contains(o, "no open task") {
		t.Fatalf("nothing left to close: %s", o)
	}
}

// CL1 (end-to-end run): on real data, the first click of a task asks once;
// once it ran (the user allowed it), the task's other clicks and typing go
// through. Page scripts that could change data still ask, and so does a
// new task's first click.
func TestBrowserClicksAskOncePerTask(t *testing.T) {
	h := provenTask(t)
	tool := "mcp__claude-in-chrome__computer"
	open := map[string]any{"action": "left_click", "ref": "ref_45", "action_summary": "Opens the sign-out confirmation dialog (does not sign out)"}
	cancel := map[string]any{"action": "left_click", "ref": "ref_58", "action_summary": "Closes the sign-out dialog with Cancel"}
	script := "mcp__claude-in-chrome__javascript_tool"
	del := map[string]any{"action": "javascript_exec", "text": "await fetch('/rest/v1/bills', {method: 'DELETE'})"}
	h.expect(h.pre(tool, open), "ask", "counts for the rest of this task")
	h.expect(h.pre(tool, cancel), "ask", "counts for the rest of this task") // not allowed yet
	h.post(tool, open, "ok")                                                 // the user allowed it, and it ran
	if !h.task().BrowserOK {
		t.Fatal("the allowed click wasn't recorded")
	}
	if d, r := decision(h.pre(tool, cancel)); d == "ask" || d == "deny" {
		t.Fatalf("a later click asked again: %s %s", d, r)
	}
	if d, _ := decision(h.pre("mcp__claude-in-chrome__form_input", map[string]any{"ref": "a", "value": true, "action_summary": "Turns on auto-pay"})); d == "ask" {
		t.Fatal("typing and picking a value are clicks too")
	}
	h.expect(h.pre(script, del), "ask", "real account")
	batch := map[string]any{"actions": []any{
		map[string]any{"name": "computer", "input": cancel},
		map[string]any{"name": "javascript_tool", "input": del},
	}}
	h.expect(h.pre("mcp__claude-in-chrome__browser_batch", batch), "ask", "real account")
	// A script that ran doesn't stand for the clicks' OK.
	h2 := provenTask(t)
	h2.post(script, del, "ok")
	if h2.task().BrowserOK {
		t.Fatal("an allowed page script was taken as the OK for clicks")
	}
}

// BL1 (end-to-end run): checks run in the same line as a new branch
// (git switch -c x && plumbline run checks) find the checkout's one open
// task, though its branch isn't the task's yet.
func TestRunAfterBranchSwitchFindsTheTask(t *testing.T) {
	h := provenTask(t)
	run(t, h.repo, "git", "switch", "-q", "-c", "fix/new-branch")
	got, err := ActiveTask(h.repo, "", "")
	if err != nil || got == nil || got.ID != h.task().ID {
		t.Fatalf("the checkout's one open task: %v %v", got, err)
	}
	// Two open tasks, none on this branch: no guess.
	other := h.task()
	other.ID, other.Branch, other.SessionID = "1122-zzzzzz", "elsewhere", "sess-9"
	must(t, SaveTask(other))
	if got, _ := ActiveTask(h.repo, "", ""); got != nil {
		t.Fatalf("with two open tasks it must not guess: %s", got.ID)
	}
}

// CM3 (end-to-end run): 72 characters is the advice; only a subject over
// 100 is refused, so a few extra characters aren't a red box.
func TestCommitSubjectUpTo100(t *testing.T) {
	s := &Setup{Kind: "personal"}
	ok := "fix: unstick Settings sign-out and reset dialogs when the unsent check fails" // 76
	if p := CheckCommitMessage(ok, s); len(p) > 0 {
		t.Fatalf("a 76-character subject was refused: %v", p)
	}
	long := "fix: " + strings.Repeat("x", 96)
	if p := CheckCommitMessage(long, s); len(p) == 0 || !strings.Contains(p[0], "101 characters") {
		t.Fatalf("a 101-character subject must be refused: %v", p)
	}
}
