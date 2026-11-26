package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The workers (2b): tracers trace code and report with file and line;
// builders build parts of the approved plan, each in its own copy of the
// repo; blind QA writes tests from the plan's cases before the code exists;
// the tester runs the checks and reports. None of them talks to the user, and
// only the lead merges a builder's copy back.

// Test files, by the usual naming of each ecosystem: a tests folder, or a
// test file name (Go, JS/TS, Python, Ruby, and JVM/PHP/.NET/Swift classes
// ending in Test or Tests, so Latest.swift or Contest.java aren't).
// Also: Rust test modules kept beside the code (src/foo/tests.rs,
// foo_tests.rs, included with #[cfg(test)] mod tests;), Elixir, Dart,
// Clojure, Lua and other *_test / *_spec files, and
// Scala, Kotlin, Groovy and Haskell *Spec classes. Anything else a project
// names its tests by goes in the setup's test_files (B8).
var reTestFile = regexp.MustCompile(`((^|/)(tests?|__tests__|testdata)/|(^|/)[^/]+\.Tests?/|_test\.(go|py|rs|exs|dart|clj|cljs|cljc|lua|cr|jl|nim|zig|ml|erl|c|cc|cpp|cxx|h|hpp)$|_tests\.rs$|(^|/)tests\.rs$|\.(test|spec|cy)\.[a-z0-9]+$|(^|/)test_[^/]*\.py$|(^|/)conftest\.py$|_spec\.(rb|lua|cr|exs)$|_unittest\.(c|cc|cpp|cxx|h|hpp)$|[A-Za-z0-9]Tests?\.(java|kt|kts|php|cs|swift|scala|groovy)$|[A-Za-z0-9]Spec\.(scala|kt|kts|groovy|hs|swift)$)`)

// TestFile: rel is a test file by its name, or by a test_files pattern in
// the setup. A plan's case can name any file as its test, so that alone
// doesn't make it one.
func TestFile(rel string, s *Setup) bool {
	rel = filepath.ToSlash(rel)
	return reTestFile.MatchString(rel) || (s != nil && MatchAny(s.TestFiles, rel))
}

func (c *hookCtx) isTestFile(rel string) bool { return TestFile(rel, c.setup) }

// qaRunning: blind QA is writing tests right now (it stopped, went quiet
// for helperAlive, or the user waived it: no longer).
// qaFirst: a big task whose blind QA hasn't written its tests yet (no
// finished run with findings, no waiver), so code in this checkout waits:
// once QA could see new code it isn't blind any more (QA1).
func (t *Task) qaFirst(s *Setup) bool {
	if t.Size != "big" || s == nil || !s.PartOn(HelperQA) || DocsOnly(t, s) || CommandsOnly(t, s) {
		return false
	}
	for _, w := range t.Waivers {
		if w.Check == "qa" {
			return false
		}
	}
	for _, r := range t.helperRuns(HelperQA) {
		if !r.Finished.IsZero() && t.findingsExist(r) {
			return false
		}
	}
	return true
}

func (t *Task) qaRunning() bool {
	for _, w := range t.Waivers {
		if w.Check == "qa" {
			return false
		}
	}
	for _, r := range t.helperRuns(HelperQA) {
		if r.alive() {
			return true
		}
	}
	return false
}

// touchRun records a builder's, QA's or checked helper's tool call, so a long
// run keeps counting as running and a dead one stops counting.
func (c *hookCtx) touchRun(kind string) {
	if c.task == nil || (kind != HelperBuilder && kind != HelperQA && !checked(kind)) {
		return
	}
	if r := c.task.runByAgent(c.in.AgentID); r != nil && r.Finished.IsZero() && Now().Sub(r.LastSeen) > time.Minute {
		r.LastSeen = Now()
		_ = SaveTask(c.task)
	}
}

// codeSinceApproval lists the files other than tests that changed since the
// plan was first approved. ok is false when that can't be known.
func (c *hookCtx) codeSinceApproval() (files []string, ok bool) {
	t := c.task
	now, err := Fingerprint(t.Root, c.ignore())
	if err != nil {
		return nil, false
	}
	if t.ApprovalPrint == "" {
		// Approved before 0.5.0: known only if no code changed since.
		if t.Checkpoint2At.IsZero() || !t.LastCodeChange.Before(t.Checkpoint2At) {
			return nil, false
		}
		t.ApprovalPrint = now
		_ = SaveTask(t)
	}
	changed, err := treeChanges(t.Root, t.ApprovalPrint, now)
	if err != nil {
		return nil, false
	}
	for _, f := range changed {
		if !c.isTestFile(f) {
			files = append(files, f)
		}
	}
	return files, true
}

// qaStartProblem: why blind QA can't start now, or "". It is blind because
// it starts before any code changed: builders work in their own copies, which
// QA can't read, and the lead's own code edits wait while it runs.
func (c *hookCtx) qaStartProblem() string {
	t := c.task
	switch {
	case !c.setup.PartOn(HelperQA):
		return "blind QA is switched off in this project's setup (parts)."
	case t.Phase != PhaseBuilding:
		return fmt.Sprintf("blind QA writes tests from the approved plan's cases, and task %s is at the %s step.", t.ID, t.Phase)
	case len(t.helperRuns(HelperQA)) >= MaxQARuns:
		return fmt.Sprintf("blind QA already ran %d times, the most a task gets.", MaxQARuns)
	case t.qaRunning():
		return "blind QA is already running; wait for it."
	}
	files, ok := c.codeSinceApproval()
	switch {
	case !ok:
		return "Plumbline can't tell whether the code changed since the plan was approved (a plan approved before Plumbline 0.5.0), so QA wouldn't be blind. Tell the user; they can waive it (plumbline waive qa)."
	case len(files) > 0:
		return fmt.Sprintf("the code already changed since the plan was approved (%s), so QA wouldn't be blind. Blind QA runs first, before any code: stop and ask the user whether to waive it (plumbline waive qa).", listFiles(files))
	}
	return ""
}

// qaEdit: blind QA writes test files in the main checkout, and nothing else.
func (c *hookCtx) qaEdit(run *HelperRun, abs string) Output {
	if run == nil || c.task == nil {
		return c.deny("helper-read-only", "blind QA can only write tests during a task, and Plumbline has no record of this run (no open task found). Report the tests you'd write in your final message instead.")
	}
	if insideWorktrees(c.root, abs) {
		return c.deny("qa-blind", "blind QA doesn't look at or write in the builders' copies of the repo. Write the tests in the main checkout.")
	}
	if c.root == "" || !IsInside(abs, c.root) || !c.isTestFile(c.rel(abs)) {
		return c.deny("qa-tests-only", fmt.Sprintf("blind QA writes only test files (named like *_test.go, *.test.ts, *.spec.js, test_*.py, *_spec.rb, FooTest.java, or in a tests/ or __tests__/ folder) and its findings file, %s.", filepath.Join(TaskDocsDir(c.task.Root, c.task.ID), run.Findings)))
	}
	return nil // then the usual building rules
}

// helperMayRun: the plumbline runs a helper may start. Saved commands only:
// QA runs its tests, the tester runs every check.
func helperMayRun(kind string, s Segment) bool {
	if !isPlumblineBin(s.Name()) || s.Arg(1) != "run" {
		return false
	}
	switch kind {
	case HelperQA:
		return s.Arg(2) == "related" || s.Arg(2) == "test"
	case HelperTester:
		return contains(append([]string{KindTest, KindRelated, KindE2E}, CheckKinds...), s.Arg(2))
	case HelperBuilder:
		return contains(append([]string{KindTest, KindRelated}, CheckKinds...), s.Arg(2))
	}
	return false
}

// taskByAgent finds the open task holding a run of this helper, for a
// helper's hooks that don't carry the lead's session.
func taskByAgent(root, agentID string) *Task {
	if root == "" || agentID == "" {
		return nil
	}
	for _, t := range ListTasks(root) {
		if t.Open() && t.runByAgent(agentID) != nil {
			return t
		}
	}
	return nil
}

type builderCopy struct{ path, base, tree, err, setup string }

// makeBuilderCopy makes the builder's copy of the repo from the lead's code.
// It lets go of the task lock while git checks the copy out, then takes it
// again and reads the task afresh (c.task is nil if that fails).
func (c *hookCtx) makeBuilderCopy() builderCopy {
	t := c.task
	if t.Phase != PhaseBuilding {
		return builderCopy{err: "the plan isn't approved"}
	}
	root, id, ignore := t.Root, t.ID, c.ignore()
	path := BuilderWorktree(root, id, c.in.AgentID)
	setup := ""
	if c.setupOK && c.setup != nil {
		setup = strings.TrimSpace(c.setup.Commands.BuilderSetup)
	}
	c.close()
	base, tree, err := MakeWorktree(root, ignore, path)
	note := ""
	if err == nil && setup != "" {
		note = RunBuilderSetup(path, setup, ignore, tree)
	}
	c.unlock = LockTask(root, id)
	c.task, c.taskErr = LoadTask(root, id)
	if c.taskErr != nil {
		c.task = nil
	}
	if err != nil {
		return builderCopy{err: "couldn't make the copy: " + err.Error()}
	}
	return builderCopy{path: path, base: base, tree: tree, setup: note}
}

// workerStartNote is what a tracer, builder, QA or tester is told at start.
func (c *hookCtx) workerStartNote(b *strings.Builder, run HelperRun, n int) {
	t := c.task
	docs := TaskDocsDir(t.Root, t.ID)
	out := filepath.Join(docs, run.Findings)
	switch run.Kind {
	case HelperTracer:
		fmt.Fprintf(b, " Trace what the lead asked in the code: read it, and report every claim with file:line. Write your report to %s as you go, with the Write tool: it is the only file you can write. Terminal commands must only read. The lead opens what it quotes, so name exact lines.", out)
	case HelperTester:
		fmt.Fprintf(b, " Run the checks the lead asked for with plumbline run test|related|lint|build|e2e (the saved commands; results are recorded as the task's proof). Don't fix anything. Write to %s with the Write tool (not the shell): each command, tests ran and failed, the failing tests' names and the first lines of each failure. It is the only file you can write.", out)
	case HelperQA:
		fmt.Fprintf(b, " You are blind QA: write tests for the cases in %s from the plan and the code as it was approved, without seeing the new code (it doesn't exist yet in this checkout, and the builders' copies under %s are closed to you). Write only test files, one or more per case, then run them with plumbline run related --files <your test files> (the only way to run them: npx, npm and the like are refused): tests for a fix or a new behaviour should fail now. Write to %s: each case, its test (file::name), and whether it fails now and why. Terminal commands otherwise must only read.", filepath.Join(docs, DocPlan), WorktreesDir, out)
	case HelperBuilder:
		if run.Worktree == "" {
			fmt.Fprintf(b, " Plumbline couldn't make your copy of the repo (%s), so you can't change code. Stop and tell the lead.", run.Error)
			return
		}
		switch {
		case run.Setup != "":
			fmt.Fprintf(b, " Setting up the copy's dependencies went wrong: %s. Tests may not run in the copy; say so in your notes.", run.Setup)
		case c.setupOK && c.setup != nil && c.setup.Commands.BuilderSetup != "":
			fmt.Fprintf(b, " The copy's dependencies are set up (%s), so its tests should run there.", c.setup.Commands.BuilderSetup)
		}
		fmt.Fprintf(b, " Your own copy of the repo: %s, made from the lead's code as it is now, with all its uncommitted changes (blind QA's tests too): nothing needs copying in from the main checkout, and you can't reach it. Build only the part of %s the lead gave you, and only in your copy: edit files under that folder, and start every terminal command with `cd %s && `. The plan's rules apply as for the lead (files outside the plan are listed, risky ones refused). Don't commit, switch branches or stash: the lead merges your copy. Run the related tests in your copy if they run there (plumbline run related --files <files>). Write to %s: what you changed and why, the tests you ran and their results, and anything left or uncertain, then end with \"## Status\" and one of: %s. Blocked or needs context is the honest end when you couldn't finish: bad work is worse than none.", run.Worktree, filepath.Join(docs, DocPlan), run.Worktree, out, strings.Join(BuilderStatuses, ", "))
	}
	if n > 1 {
		if prev := t.helperRuns(run.Kind); len(prev) >= n-1 {
			fmt.Fprintf(b, " An earlier %s run's notes: %s.", run.Kind, filepath.Join(docs, prev[n-2].Findings))
		}
	}
}

// stopBuilder: a builder that changed nothing leaves no copy behind.
func (c *hookCtx) stopBuilder(run *HelperRun) string {
	if run.Worktree == "" {
		return run.Error
	}
	print, err := Fingerprint(run.Worktree, c.ignore())
	if err != nil {
		return "couldn't read the copy: " + err.Error()
	}
	run.PrintAfter = print
	if print == run.PrintBefore {
		if RemoveWorktree(c.task.Root, run.Worktree) == nil {
			run.Dropped = true
		}
		return "no changes"
	}
	files := worktreeChanges(run.Worktree, c.ignore(), run.PrintBefore)
	return fmt.Sprintf("%d files changed", len(files))
}

// gateBuilder: a builder changes code only in its own copy, runs commands
// there, and leaves git to the lead. The usual building rules then apply, in
// its copy.
func (c *hookCtx) gateBuilder() Output {
	in := c.in
	if in.ToolName == "AskUserQuestion" {
		return c.deny("helper-no-questions", "builders run without the user. Put open questions in your build notes; the lead decides what to ask.")
	}
	var run *HelperRun
	if c.task != nil {
		run = c.task.runByAgent(in.AgentID)
	}
	edit := in.ToolName == "Edit" || in.ToolName == "Write" || in.ToolName == "MultiEdit" || in.ToolName == "NotebookEdit"
	if edit && c.ownNotesWrite(run, in.Str("file_path")) {
		return allow("builder notes")
	}
	changes := edit || in.ToolName == "Bash" || in.ToolName == "Monitor" || strings.HasPrefix(in.ToolName, "mcp__") && c.mcpKind() != ""
	if run == nil || run.Worktree == "" {
		if !changes {
			return nil
		}
		why := "no open task was found for this builder"
		if run != nil {
			why = run.Error
		}
		return c.deny("builder-no-copy", fmt.Sprintf("this builder has no copy of the repo to work in (%s), so it can't change anything. Stop and tell the lead.", why))
	}
	if run.Merged || run.Dropped {
		if changes {
			return c.deny("builder-done", "this builder's copy was already merged or dropped. Stop and tell the lead.")
		}
		return nil
	}
	wt := run.Worktree
	switch {
	case edit:
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		return c.builderPath(wt, Abs(p, in.Cwd))
	case in.ToolName == "Monitor":
		return c.deny("builder-background", "builders run commands in the foreground, so Plumbline can check what each one changed. Use the terminal and wait for it.")
	case in.ToolName == "Bash":
		if b, _ := in.Input()["run_in_background"].(bool); b {
			return c.deny("builder-background", "builders run commands in the foreground, so Plumbline can check what each one changed. Run it without run_in_background.")
		}
		if cmd := in.Str("command"); cmd != "" {
			if o := c.builderBash(wt, cmd); o != nil {
				return o
			}
			c.snapMain() // to see afterwards whether it reached the main checkout
		}
	case strings.HasPrefix(in.ToolName, "mcp__"):
		switch c.mcpKind() {
		case "shell":
			if cmd, ok := mcpCommand(in.Input()); ok {
				if o := c.builderBash(wt, cmd); o != nil {
					return o
				}
				c.snapMain()
				return nil
			}
			return c.deny("builder-own-copy", fmt.Sprintf("%s runs code somewhere Plumbline can't see. Use the terminal, inside your copy (%s).", in.ToolName, wt))
		case "write":
			if p := mcpPath(in.Input()); p != "" {
				return c.builderPath(wt, Abs(p, in.Cwd))
			}
			return c.deny("builder-own-copy", fmt.Sprintf("%s changes files Plumbline can't name. Edit files in your copy (%s) instead.", in.ToolName, wt))
		}
	}
	return nil
}

// builderPath: a file a builder changes must be in its own copy.
func (c *hookCtx) builderPath(wt, abs string) Output {
	if abs == "" {
		return nil
	}
	if !IsInside(abs, wt) {
		hint := ""
		if r, err := filepath.Rel(ProjectRoot(c.root), abs); err == nil && !climbsOut(r) && !insideWorktrees(c.root, abs) {
			hint = " Edit " + filepath.Join(wt, r) + " instead."
		}
		return c.deny("builder-own-copy", fmt.Sprintf("builders change files only in their own copy of the repo, %s.%s", wt, hint))
	}
	if abs == filepath.Join(wt, RepoSetupFile) {
		return c.deny("builder-own-copy", "builders don't change Plumbline's project setup. Put the change in your build notes for the lead.")
	}
	return nil // then the usual rules, in the copy
}

// builderBash: a builder's commands run in its copy, read-only git only, and
// no plumbline command beyond reads and checks.
func (c *hookCtx) builderBash(wt, cmd string) Output {
	if HasBackground(cmd) {
		return c.deny("builder-background", "builders run commands in the foreground (no trailing &), so Plumbline can check what each one changed.")
	}
	project := ProjectRoot(c.root)
	outside := func(abs string) bool { return !isInsideFold(abs, wt) }
	inProject := func(abs string) bool { return isInsideFold(abs, project) }
	for _, s := range ParseShell(cmd, c.in.Cwd) {
		if s.Opaque != "" {
			continue // the Bash gate refuses it
		}
		name := s.Name()
		if name == "git" {
			if sub, _ := GitSub(s); !ReadOnly(s) {
				return c.deny("builder-git", fmt.Sprintf("builders leave git to the lead (git %s): don't commit, switch, stash, reset or merge. The lead merges your copy.", sub))
			}
		}
		if isPlumblineBin(name) && !plumblineReads(s) && !helperMayRun(HelperBuilder, s) {
			return c.deny("builder-plumbline", "builders run only plumbline run test|related|lint|build and the read-only plumbline commands. The lead handles the task.")
		}
		if ReadOnly(s) || plumblineReads(s) || name == "cd" {
			continue // reading is fine anywhere
		}
		if s.Dir == "" || outside(realCase(Abs(s.Dir, "/"))) {
			return c.deny("builder-own-copy", fmt.Sprintf("run commands inside your copy of the repo: start with cd %s && ...", wt))
		}
		// Any path the command names in the project must be in the copy: an
		// ln source, make -C, go build -o or a climb out with ../.. could
		// otherwise reach the main checkout or another copy. A link must
		// point inside the copy wherever it points, and a command that
		// writes files by name must name them plainly.
		link := name == "ln" || (name == "cp" && (contains(s.Words, "-s") || contains(s.Words, "-l") || contains(s.Words, "--symbolic-link") || contains(s.Words, "--link")))
		for i, w := range s.Words {
			if i == 0 {
				continue
			}
			if i < len(s.Dyn) && s.Dyn[i] {
				if fileWriters[name] || inPlace(s) {
					return c.deny("builder-own-copy", fmt.Sprintf("%s names a file through %q, whose value Plumbline can't know. Builders write the paths out plainly, inside the copy.", name, w))
				}
				continue
			}
			if strings.HasPrefix(w, "-") {
				v := ""
				if _, after, ok := strings.Cut(w, "="); ok {
					v = after
				} else if !strings.HasPrefix(w, "--") && len(w) > 2 && strings.ContainsAny(w[2:], "/.~") {
					v = w[2:] // a value stuck to a short option: -C../.., -o../x
				}
				if v == "" {
					continue
				}
				w = v
			}
			abs := realCase(Abs(homeWord(w), s.Dir))
			if ((link && !systemToolPath(abs)) || inProject(abs)) && outside(abs) {
				return c.deny("builder-own-copy", fmt.Sprintf("%q is outside your copy of the repo (%s). Builders work only there: name paths inside the copy.", w, wt))
			}
		}
		for _, t := range append(WriteTargets(s), TreeTargets(s)...) {
			if strings.ContainsAny(t, "$`") {
				return c.deny("builder-own-copy", fmt.Sprintf("%q is written through a value Plumbline can't know. Builders write the paths out plainly, inside the copy.", t))
			}
			abs := targetAbs(s, t)
			if abs == "" || !outside(abs) {
				continue
			}
			// Throwaway files in the temp folder or scratchpad are fine, but
			// never the main checkout or another copy, wherever the repo is.
			scratch := isInsideFold(abs, Abs(os.TempDir(), "/")) || (c.in.ScratchpadDir != "" && isInsideFold(abs, Abs(c.in.ScratchpadDir, "/")))
			if scratch && !inProject(abs) {
				continue
			}
			if own := c.ownNotesPath(); own != "" && foldPath(abs) == foldPath(own) {
				return c.deny("builder-own-copy", c.ownFilesReason(abs))
			}
			return c.deny("builder-own-copy", fmt.Sprintf("%s is outside your copy of the repo (%s). Builders change files only there.", abs, wt))
		}
	}
	return nil // then the usual Bash gate, in the copy
}

// builderMissing: every builder's copy with changes is merged or dropped
// before the finish check passes.
func builderMissing(t *Task) []string {
	var open, running []string
	for i, r := range t.helperRuns(HelperBuilder) {
		if r.Worktree == "" || r.Merged || r.Dropped {
			continue
		}
		if r.alive() {
			running = append(running, fmt.Sprintf("builder %d", i+1))
			continue
		}
		if _, err := os.Stat(r.Worktree); err != nil {
			continue
		}
		open = append(open, fmt.Sprintf("builder %d's copy (%s): plumbline builder merge %d, or plumbline builder drop %d", i+1, r.Worktree, i+1, i+1))
	}
	var miss []string
	if len(running) > 0 {
		miss = append(miss, "builders: still running, wait for them and merge or drop their copies: "+strings.Join(running, ", "))
	}
	if len(open) > 0 {
		miss = append(miss, "builders: bring each copy into the task's code or drop it: "+strings.Join(open, "; "))
	}
	return miss
}

// qaMissing: big tasks need blind QA's tests, and the report says what they
// caught, so its value shows in the logs.
func qaMissing(t *Task, s *Setup, report string) []string {
	if t.Size != "big" || !s.PartOn(HelperQA) || DocsOnly(t, s) || CommandsOnly(t, s) {
		return nil
	}
	for _, w := range t.Waivers {
		if w.Check == "qa" {
			return nil // the user's call; it doesn't lapse with code changes
		}
	}
	var done *HelperRun
	for _, r := range t.helperRuns(HelperQA) {
		if !r.Finished.IsZero() && t.findingsExist(r) {
			r := r
			done = &r
		}
	}
	if done == nil {
		if t.qaRunning() {
			return []string{"qa: blind QA is still running; wait for its tests"}
		}
		return []string{"qa: big tasks need blind QA first: start plumbline:qa before any code changes (it writes tests from the plan's cases without seeing the code). If the code already changed, stop and ask the user whether to waive it (plumbline waive qa)"}
	}
	if report != "" && !strings.Contains(strings.ToLower(strings.Join(Section(report, "review"), "\n")), "qa") {
		return []string{fmt.Sprintf("report: say under ## Review what blind QA's tests caught (%s): which failed before the code and which found a problem", done.Findings)}
	}
	return nil
}

// qaBlind keeps blind QA out of the builders' copies: no path inside one,
// and while any copy exists, no search or pattern starting at or above the
// copies' folder (grep -r . or cat .claude/work*/... would reach into it).
// The Grep tool skips folders git ignores, so it may search the repo root
// once the copies' folder is ignored.
func (c *hookCtx) qaBlind(abs string, tree, pattern bool) Output {
	dir := copiesDir(c.root)
	if insideWorktrees(c.root, abs) || (foldPath(abs) == foldPath(dir) && copiesExist(c.root)) {
		return c.deny("qa-blind", "blind QA doesn't look at the builders' copies of the repo: write the tests from plan.md's cases and the code as it was approved.")
	}
	if !tree && !pattern || !copiesExist(c.root) {
		return nil
	}
	if tree && !pattern && (c.in.ToolName == "Grep" || c.searchSkipsCopies) && !worktreesUnignored(ProjectRoot(c.root)) && foldPath(abs) != foldPath(dir) {
		return nil // ripgrep and fd skip the ignored copies' folder when they search above it
	}
	if isInsideFold(dir, abs) || foldPath(abs) == foldPath(dir) {
		return c.deny("qa-blind", fmt.Sprintf("while builders work, a search or pattern from %s would reach into their copies of the repo (%s), and blind QA mustn't see the new code. Search a folder below it (src/, tests/ ...), or use the Grep tool.", abs, dir))
	}
	return nil
}

// builderReachedMain runs after a builder's command: files that changed in
// the main checkout while it ran are recorded for the user, since only the
// lead's merge should change them (a command can get there in ways no gate
// sees, such as interpreter code). The lead may have edited at the same
// moment, so the note says "while".
func (c *hookCtx) builderReachedMain() string {
	t := c.task
	if t == nil || c.root == t.Root {
		return ""
	}
	prev := loadSnap(c.snapID(), t.Root)
	now, err := mainState(t.Root)
	if err != nil || prev == nil {
		return ""
	}
	saveSnap(c.snapID(), t.Root, now)
	// The lead's own edits in flight aren't the builder's.
	leads, leadCommand := t.leadTouching()
	var changed []string
	for _, p := range Changed(t.Root, prev, now) {
		if (c.setup == nil || !MatchAny(c.setup.IgnoreChanges, p)) && !leads[p] {
			changed = append(changed, p)
		}
	}
	if len(changed) == 0 {
		return ""
	}
	why := "changed in the main checkout while a builder's command ran"
	if leadCommand {
		why += " and a command of the lead's ran too, so it may be the lead's"
	}
	if t.qaRunning() {
		why += ", during blind QA"
	}
	gitFiles := false
	for _, p := range changed {
		gitFiles = gitFiles || strings.HasPrefix(p, ".git/")
	}
	if gitFiles {
		why += "; git's own files changed: check .git/hooks and .git/config before the next commit"
	}
	if t.Held == nil {
		t.Held = map[string]string{}
	}
	for _, p := range changed {
		t.Held[p] = why
	}
	_ = SaveTask(t)
	c.log("block", "builder-outside-copy", fmt.Sprintf("%d files", len(changed)))
	return fmt.Sprintf("files in the main checkout changed while that command ran: %s. Builders change only their own copy (%s). If your command did that, don't undo it there: say in your notes exactly what ran, for the lead. They're listed for the user under \"For you\".", listFiles(changed), c.root)
}

// LeftCopies lists the builders' copies of a task still on disk, unmerged.
func LeftCopies(t *Task) []string {
	var out []string
	for _, r := range t.helperRuns(HelperBuilder) {
		if r.Worktree == "" || r.Merged || r.Dropped {
			continue
		}
		if _, err := os.Stat(r.Worktree); err == nil {
			out = append(out, r.Worktree)
		}
	}
	return out
}

// fileWriters: commands whose arguments name files they write or link.
// systemToolPath: a cleaned absolute path among the system's own tools
// (/usr/bin/node, /opt/homebrew/bin/python3), which a builder may link to.
// Never the root, the home folder or a project.
func systemToolPath(abs string) bool {
	if home, err := os.UserHomeDir(); err == nil && isInsideFold(abs, home) {
		return false
	}
	for _, d := range []string{"/usr", "/bin", "/sbin", "/opt/homebrew", "/opt/local", "/nix/store"} {
		if isInsideFold(abs, d) {
			return true
		}
	}
	return false
}

var fileWriters = map[string]bool{
	"cp": true, "mv": true, "ln": true, "rsync": true, "install": true, "tee": true, "dd": true, "rm": true,
	"mkdir": true, "rmdir": true, "touch": true, "chmod": true, "chown": true, "tar": true, "unzip": true,
	"patch": true, "make": true, "truncate": true, "shred": true,
}

// homeWord: ~ and ~/x name the home folder, as the shell expands them.
func homeWord(w string) string {
	if w == "~" {
		h, _ := os.UserHomeDir()
		return h
	}
	return w
}

// mainState is what a builder's command is checked against in the main
// checkout: its changed files plus git's own config, hooks and exclude
// files, which git status never shows.
func mainState(root string) (map[string]string, error) {
	m, err := DirtyFiles(root)
	if err != nil {
		return nil, err
	}
	gd := filepath.Join(ProjectRoot(root), ".git")
	for _, pat := range []string{"config", "hooks/*", "info/*"} {
		files, _ := filepath.Glob(filepath.Join(gd, pat))
		for _, f := range files {
			if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
				rel, _ := filepath.Rel(ProjectRoot(root), f)
				m[filepath.ToSlash(rel)] = stamp(f)
			}
		}
	}
	return m, nil
}

// snapMain takes the main checkout's state before a builder's command.
func (c *hookCtx) snapMain() {
	if m, err := mainState(c.task.Root); err == nil {
		saveSnap(c.snapID(), c.task.Root, m)
	}
}

// absorbIntoBuilders: after the lead's own change to the main checkout,
// the running builders' "before" snapshots take it in, so the lead's edits
// and merges aren't reported as a builder's. An edit takes in only its own
// file, so a builder's write landing at the same moment is still seen; a
// terminal command can change anything, so it takes in the whole state.
func (c *hookCtx) absorbIntoBuilders() {
	t := c.task
	if t == nil || c.in.AgentID != "" {
		return
	}
	if len(t.LeadBusy) > 0 {
		delete(t.LeadBusy, c.in.ToolUseID)
		for id, b := range t.LeadBusy {
			if Now().Sub(b.At) > leadBusyFor {
				delete(t.LeadBusy, id) // refused later, or its hook never ran
			}
		}
		_ = SaveTask(t)
	}
	var m map[string]string
	only := ""
	switch c.in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		p := c.in.Str("file_path")
		if p == "" {
			p = c.in.Str("notebook_path")
		}
		abs := Abs(p, c.in.Cwd)
		if !IsInside(abs, t.Root) {
			return // a notes file: nothing in the main checkout changed
		}
		only = c.rel(abs)
	}
	for _, r := range t.helperRuns(HelperBuilder) {
		if !r.alive() || r.Worktree == "" || r.Merged || r.Dropped {
			continue
		}
		if m == nil {
			var err error
			if m, err = mainState(t.Root); err != nil {
				return
			}
		}
		id := c.in.SessionID + "-" + r.AgentID
		if only == "" {
			saveSnap(id, t.Root, m)
			continue
		}
		snap := loadSnap(id, t.Root)
		if snap == nil {
			continue
		}
		if v, ok := m[only]; ok {
			snap[only] = v
		} else {
			delete(snap, only)
		}
		saveSnap(id, t.Root, snap)
	}
}

// LeadTouch: one of the lead's tool calls in flight: the files it names,
// or Any for a command that can change files it doesn't name.
type LeadTouch struct {
	Paths []string  `json:"paths,omitempty"`
	Any   bool      `json:"any,omitempty"`
	At    time.Time `json:"at"`
}

// A lead tool call older than this no longer counts as in flight.
const leadBusyFor = 10 * time.Minute

// buildersRunning: some builder of this task is working in its copy now.
func (t *Task) buildersRunning() bool {
	for _, r := range t.helperRuns(HelperBuilder) {
		if r.alive() && r.Worktree != "" && !r.Merged && !r.Dropped {
			return true
		}
	}
	return false
}

// noteLeadBusy records the lead's tool call before it runs, while builders
// work, so a builder's command that ends first isn't blamed for it (B5).
func (c *hookCtx) noteLeadBusy() {
	t := c.task
	if t == nil || c.in.AgentID != "" || c.in.ToolUseID == "" || !t.buildersRunning() {
		return
	}
	var touch LeadTouch
	switch c.in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		p := c.in.Str("file_path")
		if p == "" {
			p = c.in.Str("notebook_path")
		}
		abs := Abs(p, c.in.Cwd)
		if !IsInside(abs, t.Root) {
			return
		}
		touch.Paths = []string{c.rel(abs)}
	case "Bash", "Monitor":
		touch.Any = true
	default:
		if !strings.HasPrefix(c.in.ToolName, "mcp__") || c.mcpKind() == "" {
			return
		}
		touch.Any = true
	}
	touch.At = Now()
	if t.LeadBusy == nil {
		t.LeadBusy = map[string]LeadTouch{}
	}
	t.LeadBusy[c.in.ToolUseID] = touch
	_ = SaveTask(t)
}

// leadTouching: the files the lead's tool calls in flight name, and whether
// one of them could change files it doesn't name.
func (t *Task) leadTouching() (map[string]bool, bool) {
	paths, anyFile := map[string]bool{}, false
	for _, b := range t.LeadBusy {
		if Now().Sub(b.At) > leadBusyFor {
			continue
		}
		anyFile = anyFile || b.Any
		for _, p := range b.Paths {
			paths[p] = true
		}
	}
	return paths, anyFile
}

// qaCommand: blind QA's terminal commands run in a known folder, don't read
// git's whole object store (a builder's files land there when its copy is
// fingerprinted), and a search with no path named doesn't start at or above
// the copies' folder.
func (c *hookCtx) qaCommand(s Segment) Output {
	if s.Dir == "" {
		return c.deny("qa-blind", "blind QA runs commands in a folder Plumbline can see: no cd to a computed path.")
	}
	if s.Name() == "git" {
		sub, args := GitSub(s)
		if sub == "cat-file" || sub == "fsck" || sub == "unpack-objects" || sub == "verify-pack" {
			return c.deny("qa-blind", fmt.Sprintf("git %s reads git's object store, which holds the builders' work; blind QA mustn't see it.", sub))
		}
		for _, a := range args {
			if a == "--all" || a == "--reflog" || a == "--lost-found" || a == "--unreachable" || a == "--batch-all-objects" {
				return c.deny("qa-blind", fmt.Sprintf("git %s %s reaches past the approved code; blind QA reads the code as it is in this checkout.", sub, a))
			}
		}
	}
	if !reSearchTool.MatchString(s.Name()) || !copiesExist(c.root) || (qaSearchSkipsCopies(s) && !worktreesUnignored(ProjectRoot(c.root))) {
		return nil
	}
	dir := copiesDir(c.root)
	here := realCase(Abs(s.Dir, "/"))
	if !isInsideFold(dir, here) && foldPath(here) != foldPath(dir) {
		return nil // searching below the copies' folder, or beside it
	}
	for i, w := range s.Words {
		if i == 0 || strings.HasPrefix(w, "-") || (i < len(s.Dyn) && s.Dyn[i]) {
			continue
		}
		p := realCase(Abs(w, s.Dir))
		if _, err := os.Stat(p); err == nil && !isInsideFold(dir, p) && foldPath(p) != foldPath(dir) {
			return nil // it names what it searches; each path is checked on its own
		}
	}
	return c.deny("qa-blind", fmt.Sprintf("%s with no folder named searches %s, which holds the builders' copies (%s). Name the folders to search (src/, tests/ ...), or use the Grep tool.", s.Name(), here, dir))
}

// BuilderStatuses: how a builder's run ends, in its notes' "## Status"
// (N10). Builders can't ask the user, so blocked and needs context are
// their way out; the lead doesn't merge those.
var BuilderStatuses = []string{"done with concerns", "done", "blocked", "needs context"}

// builderStatus reads the status from a builder's notes, or "".
func builderStatus(notes string) string {
	for _, l := range Section(notes, "status") {
		t := strings.ToLower(strings.Trim(plainLine(strings.TrimSpace(l)), " .:"))
		if t == "" {
			continue
		}
		for _, s := range BuilderStatuses { // longest first: "done with concerns" isn't "done"
			if t == s || strings.HasPrefix(t, s+":") || strings.HasPrefix(t, s+",") || strings.HasPrefix(t, s+" ") {
				return s
			}
		}
		return ""
	}
	return ""
}

// QATestsDir: where blind QA's tests are kept as it wrote them, in the
// notes folder, so the reviewers can see what changed in them since.
func QATestsDir(n int) string { return fmt.Sprintf("qa-%d-tests", n) }

// keepQATests copies blind QA's test files into the notes folder and
// records their hashes, when its run stops (N22).
func (c *hookCtx) keepQATests(run *HelperRun) {
	t := c.task
	n := 0
	for i := range t.Helpers {
		if t.Helpers[i].Kind == HelperQA {
			n++
		}
		if &t.Helpers[i] == run {
			break
		}
	}
	dir := filepath.Join(TaskDocsDir(t.Root, t.ID), QATestsDir(n))
	run.WroteHash = map[string]string{}
	for _, rel := range run.Wrote {
		b, err := os.ReadFile(filepath.Join(t.Root, filepath.FromSlash(rel)))
		if err != nil {
			run.WroteHash[rel] = "" // written, then removed
			continue
		}
		run.WroteHash[rel] = sha256Hex(b)
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if os.MkdirAll(filepath.Dir(dst), 0o700) == nil {
			_ = writeFileAtomic(dst, b)
		}
	}
}

// qaTestsChanged: blind QA's test files that changed after it wrote them.
// The lead may fix a test's setup when the code's shape moved, never what
// it expects, and names each one on the report's Weakened tests line.
func qaTestsChanged(t *Task) []string {
	latest := map[string]string{}
	for _, r := range t.helperRuns(HelperQA) {
		for rel, h := range r.WroteHash {
			latest[rel] = h
		}
	}
	var out []string
	for rel, was := range latest {
		now := ""
		if b, err := os.ReadFile(filepath.Join(t.Root, filepath.FromSlash(rel))); err == nil {
			now = sha256Hex(b)
		}
		if now != was {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// qaTestsMissing: the report names every blind QA test changed since QA
// wrote it on the Weakened tests line.
func qaTestsMissing(t *Task, report string) []string {
	listed := weakenedLine(report)
	var unlisted []string
	for _, f := range qaTestsChanged(t) {
		if !strings.Contains(listed, f) {
			unlisted = append(unlisted, f)
		}
	}
	if len(unlisted) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(`qa: blind QA's tests changed after it wrote them: %s. Change only a test's setup when the code's shape moved, never what it expects, and name each file on the "Weakened tests:" line under ## Proof with what changed`, listSome(unlisted, 8))}
}

// qaSearchSkipsCopies: a search that leaves out what git ignores, so from
// the repo root it doesn't reach the builders' copies (their folder is in
// git's exclude file): rg and fd without the flags that search ignored or
// hidden files or add globs that override the ignores, and grep -r told to
// skip the copies' folder (B7).
func qaSearchSkipsCopies(s Segment) bool {
	var unsafeLong, unsafeShort []string
	switch s.Name() {
	case "rg":
		unsafeLong = []string{"--unrestricted", "--no-ignore", "--hidden", "--glob", "--iglob", "--files-from", "--follow"}
		unsafeShort = []string{"u", "g", ".", "L"}
	case "fd", "fdfind":
		unsafeLong = []string{"--unrestricted", "--no-ignore", "--hidden", "--follow"}
		unsafeShort = []string{"u", "I", "H", "L"}
	case "grep", "egrep", "fgrep":
		for _, w := range s.Words[1:] {
			if v, ok := strings.CutPrefix(w, "--exclude-dir="); ok {
				v = strings.Trim(strings.TrimSuffix(v, "/"), `"'`)
				if v == ".claude" || v == "worktrees" || v == ".claude/worktrees" {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
	for i, w := range s.Words {
		if i == 0 {
			continue
		}
		if i < len(s.Dyn) && s.Dyn[i] {
			return false // a flag Plumbline can't read
		}
		if w == "--" {
			break
		}
		if strings.HasPrefix(w, "--") {
			for _, u := range unsafeLong {
				if w == u || strings.HasPrefix(w, u+"=") || strings.HasPrefix(w, u+"-") {
					return false
				}
			}
			continue
		}
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			for _, u := range unsafeShort {
				if strings.Contains(w[1:], u) {
					return false
				}
			}
		}
	}
	return true
}
