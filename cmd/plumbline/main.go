// Command plumbline is Plumbline's hook runner and the CLI Claude uses to run
// recorded proof. See README.md.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lorenzkobe/plumbline/internal/core"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	args := os.Args[2:]
	if os.Args[1] != "hook" && asksHelp(args) {
		usage() // --help on a subcommand must not run it (RP1)
		return
	}
	var err error
	switch os.Args[1] {
	case "hook", "version", "--version", "-v", "help", "--help", "-h":
	default:
		// Hooks run outside Claude Code's sandbox; commands may not.
		if err := core.CheckDataDir(); err != nil {
			fmt.Fprintln(os.Stderr, "plumbline:", err)
			os.Exit(1)
		}
	}
	switch os.Args[1] {
	case "hook":
		os.Exit(runHook(args))
	case "task":
		err = cmdTask(args)
	case "run":
		var code int
		code, err = cmdRun(args)
		if err == nil {
			os.Exit(code)
		}
	case "status":
		err = cmdStatus(args)
	case "stats":
		fs := flag.NewFlagSet("stats", flag.ExitOnError)
		months := fs.Int("months", 3, "months to include")
		_ = fs.Parse(args)
		fmt.Print(core.Stats(*months))
	case "docs-dir":
		err = cmdDocsDir(args)
	case "setup":
		err = cmdSetup(args)
	case "waive":
		err = cmdWaive(args)
	case "builder":
		err = cmdBuilder(args)
	case "standup":
		err = cmdStandup(args)
	case "pause", "resume":
		root := mustRoot()
		err = core.SetPaused(root, os.Args[1] == "pause")
		if err == nil {
			fmt.Printf("Plumbline %sd in %s\n", os.Args[1], root)
		}
	case "version", "--version", "-v":
		fmt.Println("plumbline", version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "plumbline:", err)
		os.Exit(1)
	}
}

// asksHelp: -h or --help before any "--" (after it, the words belong to
// the command plumbline runs).
func asksHelp(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "-h" || a == "--help" || a == "-help" {
			return true
		}
	}
	return false
}

func usage() {
	fmt.Fprint(os.Stderr, `plumbline: a careful task workflow for Claude Code

  plumbline status                      where the task is, and what's missing
  plumbline task start --title "..." [--ticket X] [--size small|normal|big --why "..."]
  plumbline task show|list|replan|finish
  plumbline task size small|normal|big --why "..."   before the plan is approved
  plumbline task tool-down serena|context7|jira --reason "..."
  plumbline task adopt <id>             continue an earlier task in this session
  plumbline run test|related|lint|build|format|typecheck|security|e2e [--files a,b] [--task id]
  plumbline run checks                  every saved check above (not related or e2e), each recorded
  plumbline run proof ui|api|schema -- <command>
  plumbline docs-dir                    the task's notes folder
  plumbline setup show|save|laptop|undo-laptop
  plumbline setup sandbox [--exclude psql,mysql] | undo-sandbox   Claude Code's Bash sandbox, this checkout
  plumbline builder list|merge <n>|drop <n>   builders' copies of the repo
  plumbline waive proof:ui|proof:api|proof:schema|e2e|qa|bug-test --reason "..."
  plumbline stats [--months N]
  plumbline standup [--date YYYY-MM-DD]  your work since your last standup here (or on that day), from this repo
  plumbline standup tracker [--set <path>]   this project's sprint file
  plumbline pause|resume                (your terminal only)
`)
}

// runHook never lets a crash pass silently: it leaves a crash report, and a
// gate that fails denies. "pre-search" is the same check for searches
// (Grep, Glob, the search MCP tools): when it fails, the search goes
// through, so Claude can still look around and tell the user what broke.
func runHook(args []string) (code int) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "plumbline hook: which event?")
		return 2
	}
	event := args[0]
	gate := event == "pre"
	tool := ""
	defer func() {
		if r := recover(); r != nil {
			p := core.RecordFailure(version, event, tool, fmt.Sprint(r), debug.Stack())
			fmt.Fprintf(os.Stderr, "Plumbline hook crashed (%v). Report: %s.", r, p)
			if gate {
				fmt.Fprint(os.Stderr, " The action was blocked to be safe; tell the user.")
				code = 2
			} else {
				code = 0
			}
		}
	}()
	in, err := core.ReadHookInput(os.Stdin)
	if err != nil {
		p := core.RecordFailure(version, event, "", "couldn't read the hook input: "+err.Error(), nil)
		fmt.Fprintln(os.Stderr, "Plumbline couldn't read the hook input:", err, "Report:", p)
		if gate {
			return 2
		}
		return 0
	}
	tool = in.ToolName
	var out core.Output
	switch event {
	case "pre", "pre-search":
		out = core.PreToolUse(in)
	case "post":
		out = core.PostToolUse(in)
	case "post-failure":
		out = core.PostToolUseFailure(in)
	case "stop":
		out = core.Stop(in)
	case "subagent-start":
		out = core.SubagentStart(in)
	case "subagent-stop":
		out = core.SubagentStop(in)
	case "session":
		out = core.SessionStart(in)
	case "prompt":
		out = core.UserPromptSubmit(in)
	case "expansion":
		out = core.UserPromptExpansion(in)
	default:
		fmt.Fprintln(os.Stderr, "plumbline hook: unknown event", event)
		return 0
	}
	out.Write(os.Stdout)
	return 0
}

func mustRoot() string {
	root := core.RepoRoot("")
	if root == "" {
		fmt.Fprintln(os.Stderr, "plumbline: not inside a git repository")
		os.Exit(1)
	}
	return root
}

// flags parses --name value pairs plus positionals, stopping at "--".
func flags(args []string) (map[string]string, []string, []string) {
	f := map[string]string{}
	var pos, rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = args[i+1:]
			break
		}
		if strings.HasPrefix(a, "--") {
			k := strings.TrimPrefix(a, "--")
			if eq := strings.Index(k, "="); eq >= 0 {
				f[k[:eq]] = k[eq+1:]
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
				f[k] = args[i+1]
				i++
			} else {
				f[k] = "true"
			}
			continue
		}
		pos = append(pos, a)
	}
	return f, pos, rest
}

func activeTask(root string, f map[string]string) (*core.Task, error) {
	t, err := core.ActiveTask(root, "", f["task"])
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, errors.New("no open task on this branch (start one with: plumbline task start --title \"...\")")
	}
	return t, nil
}

// lockedTask is activeTask for commands that change the task: it holds the
// task lock (released when the command exits) and reads the state under it.
func lockedTask(root string, f map[string]string) (*core.Task, error) {
	t, err := activeTask(root, f)
	if err != nil {
		return nil, err
	}
	_ = core.LockTask(root, t.ID) // held until the process exits
	return core.LoadTask(root, t.ID)
}

func cmdTask(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	f, pos, _ := flags(args[1:])
	root := mustRoot()
	switch args[0] {
	case "start":
		title := strings.TrimSpace(f["title"])
		if title == "" && len(pos) > 0 {
			title = strings.Join(pos, " ")
		}
		if title == "" {
			return errors.New(`give the task a title: plumbline task start --title "..."`)
		}
		s, ok, err := core.LoadSetup(root)
		if err != nil {
			return errors.New("this project has no Plumbline setup yet; ask the user to run /plumbline:setup")
		}
		if !ok {
			return errors.New("the project setup changed since the user confirmed it; ask them to re-confirm with /plumbline:setup")
		}
		if !core.DockerReady() {
			return errors.New("the Docker app isn't running. It's required (SocratiCode and the test database). Start it, then try again")
		}
		size := f["size"]
		sizeGiven := size != ""
		if !sizeGiven {
			size = "normal"
		}
		if size != "small" && size != "normal" && size != "big" {
			return errors.New("--size is small, normal or big")
		}
		kind := ""
		if v, ok := f["design"]; ok {
			if v != "true" {
				return errors.New("--design takes no value")
			}
			if !s.PartOn("design") {
				return errors.New(`the design part is switched off for this project ("parts": {"design": false}); start a normal task instead`)
			}
			kind = core.KindDesign
		}
		t := &core.Task{Title: title, Ticket: f["ticket"], Size: size, SizeWhy: f["why"], Kind: kind}
		if err := core.NewTask(root, t); err != nil {
			return err
		}
		pre := t.Preexisting
		for _, o := range core.ListTasks(root) {
			if o.Open() && o.ID != t.ID && o.Branch == t.Branch {
				fmt.Printf("Note: task %s is also open on %s. Pass --task %s to plumbline commands.\n", o.ID, o.Branch, t.ID)
			}
		}
		fmt.Printf("plumbline-task-id: %s\n", t.ID)
		fmt.Printf("Task %s started (%s, %s project, branch %s).\n", t.ID, size, s.Kind, t.Branch)
		fmt.Printf("Notes folder: %s\n", core.TaskDocsDir(root, t.ID))
		if len(pre) > 0 {
			fmt.Printf("Your own unsaved changes before the task (not counted as the task's): %s\n", strings.Join(keys(pre), ", "))
		}
		if kind == core.KindDesign {
			fmt.Println("Next (design task): read the task, check the tools, trace the code, find the brand kit, make at least 3 mockup directions and list them in design.md, ask the user to pick one (header \"Direction\"), then write understanding.md and plan.md (with ## Design) and ask the checkpoint.")
			return nil
		}
		if !sizeGiven {
			fmt.Println(noSizeNote)
		}
		fmt.Println(startChecklist(size))
		return nil
	case "show", "list":
		for _, t := range core.ListTasks(root) {
			if args[0] == "show" && !t.Open() {
				continue
			}
			fmt.Printf("%s  %-13s %-6s %-20s %s\n", t.ID, t.Phase, t.Size, t.Branch, t.Title)
		}
		return nil
	case "tool-down":
		t, err := lockedTask(root, f)
		if err != nil {
			return err
		}
		if len(pos) == 0 {
			return errors.New("which tool? serena, context7 or jira")
		}
		tool := strings.ToLower(pos[0])
		if tool == "socraticode" || tool == "mempalace" {
			return errors.New(tool + " is required; the task can't go on without it. Fix it or ask the user")
		}
		if tool != "serena" && tool != "context7" && tool != "jira" {
			return errors.New("only serena, context7 or jira can be marked down")
		}
		if strings.TrimSpace(f["reason"]) == "" {
			return errors.New(`say why: --reason "..."`)
		}
		if t.ToolDown == nil {
			t.ToolDown = map[string]string{}
		}
		t.ToolDown[tool] = f["reason"]
		fmt.Printf("Recorded: %s not used (%s). Name this in the checkpoint and the report.\n", tool, f["reason"])
		return core.SaveTask(t)
	case "size":
		t, err := lockedTask(root, f)
		if err != nil {
			return err
		}
		if len(pos) == 0 || (pos[0] != "small" && pos[0] != "normal" && pos[0] != "big") {
			return errors.New(`which size? plumbline task size small|normal|big --why "..."`)
		}
		if strings.TrimSpace(f["why"]) == "" {
			return errors.New(`say why: --why "..." (size is impact, not lines)`)
		}
		if t.Phase == core.PhaseBuilding || !t.Open() {
			return fmt.Errorf("task is at the %s step; the size is fixed once the plan is approved (replan to change it)", t.Phase)
		}
		t.Size, t.SizeWhy, t.SizeByUser = pos[0], f["why"], false
		core.LogEvent(core.Event{Type: "task", Task: t.ID, Size: t.Size, Project: core.ProjectKey(root), Rule: "size"})
		fmt.Printf("Task %s is now %s (%s). Say why in the checkpoint.\n", t.ID, t.Size, t.SizeWhy)
		if t.Kind != core.KindDesign {
			fmt.Println(startChecklist(t.Size)) // the steps differ by size
		}
		return core.SaveTask(t)
	case "replan":
		t, err := lockedTask(root, f)
		if err != nil {
			return err
		}
		if t.Phase != core.PhaseBuilding {
			return fmt.Errorf("task is at the %s step; replan is for going back from building", t.Phase)
		}
		t.Phase = core.PhasePlanning
		t.Checkpoint2At = time.Time{}
		core.LogEvent(core.Event{Type: "phase", Task: t.ID, Rule: "replan", Size: t.Size, Project: core.ProjectKey(root)})
		fmt.Println("Back to planning. Update plan.md with what changes and why, then ask the checkpoint again. Edits are blocked until the user approves.")
		return core.SaveTask(t)
	case "finish":
		t, err := lockedTask(root, f)
		if err != nil {
			return err
		}
		if t.Phase != core.PhaseBuilding {
			return fmt.Errorf("task is at the %s step and can't be finished", t.Phase)
		}
		s, _, _ := core.LoadSetup(root)
		if miss := core.FinishMissing(t, s); len(miss) > 0 {
			return errors.New("not proven yet: " + strings.Join(miss, "; "))
		}
		if _, leave := f["uncommitted"]; !leave {
			if work := core.UncommittedWork(t, s); len(work) > 0 {
				return fmt.Errorf("the task's changes aren't committed yet (%s). Commit first (ask the user), then finish: once the task is done, Claude can't commit them. If the user said they'll commit these themselves, run plumbline task finish --uncommitted", strings.Join(work, ", "))
			}
		}
		t.Phase = core.PhaseDone
		core.LogEvent(core.Event{Type: "phase", Task: t.ID, Rule: core.PhaseDone, Size: t.Size, Project: core.ProjectKey(root), Detail: fmt.Sprintf("commits=%d", t.Commits)})
		core.LogTokens(t, core.ProjectKey(root))
		fmt.Printf("Task %s is done.\n", t.ID)
		return core.SaveTask(t)
	case "adopt":
		if len(pos) == 0 {
			return errors.New("which task id?")
		}
		t, err := core.LoadTask(root, pos[0])
		if err != nil {
			return err
		}
		if !t.Open() {
			return fmt.Errorf("task %s is %s", t.ID, t.Phase)
		}
		fmt.Printf("plumbline-adopt-id: %s\nContinuing task %s \"%s\" at the %s step.\n", t.ID, t.ID, t.Title, t.Phase)
		return nil
	}
	return fmt.Errorf("unknown task command %q", args[0])
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == ',' || r == '@' || r == '+' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func cmdRun(args []string) (int, error) {
	if len(args) == 0 {
		return 0, errors.New("run what? test, related, lint, build, format, typecheck, security, e2e, or proof")
	}
	kind := args[0]
	if kind == "checks" {
		return runChecks(args[1:])
	}
	f, pos, rest := flags(args[1:])
	root := mustRoot()
	s, ok, err := core.LoadSetup(root)
	if err != nil {
		return 0, errors.New("no project setup; run /plumbline:setup first")
	}
	if !ok {
		return 0, errors.New("the project setup changed since the user confirmed it, so its commands won't run; ask them to re-confirm with /plumbline:setup")
	}
	t, terr := core.ActiveTask(root, "", f["task"])
	if terr != nil {
		return 0, terr
	}
	var command, sub string
	var files []string // the related run's files
	switch kind {
	case "test":
		command = s.Commands.Test
		kind = core.KindTest
	case "related":
		if s.Commands.TestRelated == "" {
			fmt.Println("No related-tests command is saved; running the full suite instead.")
			command, kind = s.Commands.Test, core.KindTest
			break
		}
		if f["files"] != "" {
			files = strings.Split(f["files"], ",")
			for _, x := range files {
				if strings.HasPrefix(strings.TrimSpace(x), "-") {
					return 0, fmt.Errorf("--files takes file paths, not flags (%q): the runner would read it as an option", strings.TrimSpace(x))
				}
			}
		} else {
			d, _ := core.DirtyFiles(root)
			others := map[string]bool{}
			if t != nil {
				others = t.OthersWork()
			}
			for p := range d {
				if !others[p] {
					files = append(files, p)
				}
			}
			sort.Strings(files)
		}
		q := []string{}
		for _, x := range files {
			q = append(q, shellQuote(strings.TrimSpace(x)))
		}
		command = strings.ReplaceAll(s.Commands.TestRelated, "{files}", strings.Join(q, " "))
		if !strings.Contains(s.Commands.TestRelated, "{files}") {
			files = nil // the command doesn't take them, so they didn't run because of it
		}
		kind = core.KindRelated
	case "lint":
		command, kind = s.Commands.Lint, core.KindLint
	case "build":
		command, kind = s.Commands.Build, core.KindBuild
	case "format":
		command, kind = s.Commands.Format, core.KindFormat
	case "typecheck":
		command, kind = s.Commands.Typecheck, core.KindTypecheck
	case "security":
		command, kind = s.Commands.Security, core.KindSecurity
	case "e2e":
		command, kind = s.Commands.E2E, core.KindE2E
	case "proof":
		if len(pos) == 0 || (pos[0] != "ui" && pos[0] != "api" && pos[0] != "schema") {
			return 0, errors.New("proof kind is ui, api or schema: plumbline run proof api -- <command>")
		}
		if len(rest) == 0 {
			return 0, errors.New("give the command after --")
		}
		sub = pos[0]
		q := []string{}
		for _, x := range rest {
			q = append(q, shellQuote(x))
		}
		command, kind = strings.Join(q, " "), core.KindProof
		if core.TrivialProof(command) {
			return 0, errors.New("that command passes whatever the code does, so it proves nothing. Run something that uses the change: call the API (curl the endpoint), click it in the app (Playwright), or query the schema")
		}
		if sub == "ui" && core.FetchProof(command) {
			return 0, errors.New("fetching a page or a file doesn't show the screen works: nothing renders it or clicks it. Drive the running app (npx playwright test, or a Playwright script), or open the changed screen in the local app with Claude in Chrome and take a screenshot (Plumbline records that). If neither is possible, ask the user")
		}
		if sub == "ui" && core.UnitTestProof(command) {
			return 0, errors.New("unit and component tests aren't the real app, so they don't prove the screen works (they count as tests already). Drive the running app instead: npx playwright test, or a Playwright script that opens the changed screen and clicks the changed thing. If no browser tool is set up, ask the user: they look at it themselves (then waive proof:ui with their reason), or you use their Chrome")
		}
	default:
		return 0, fmt.Errorf("unknown run kind %q", kind)
	}
	if strings.TrimSpace(command) == "" {
		return 0, fmt.Errorf("no %s command is saved for this project (run /plumbline:setup)", args[0])
	}
	fmt.Printf("plumbline: running %s: %s\n", args[0], command)
	if t != nil && (kind == core.KindTest || kind == core.KindRelated || kind == core.KindE2E) {
		core.MarkDevDBAllowed(root, t.ID, s) // the hook asked before this ran
	}
	var r *core.Run
	if kind == core.KindRelated {
		r, err = core.ExecuteRelated(root, t, command, files, s, os.Stdout)
	} else {
		r, err = core.Execute(root, t, kind, sub, command, s, os.Stdout)
	}
	if err != nil {
		return 0, err
	}
	var base *core.Run
	if t != nil {
		bk := kind
		if kind == core.KindRelated {
			bk = core.KindTest
		}
		base = core.BaselineFor(core.LoadRuns(root), t.ID, bk)
	}
	v := core.Judge(r, base)
	status := "PASS"
	if !v.OK {
		status = "FAIL"
	}
	detail := ""
	if r.Ran >= 0 && (kind == core.KindTest || kind == core.KindRelated || kind == core.KindE2E) {
		detail = fmt.Sprintf(" %d ran, %d failed.", r.Ran, r.Failed)
	}
	if r.Baseline {
		detail += " Recorded as the task's starting point (baseline)."
	}
	if v.Reason != "" {
		detail += " " + v.Reason + "."
	}
	fmt.Printf("\nplumbline: %s recorded: %s.%s\n", args[0], status, detail)
	return r.Exit, nil
}

// noSizeNote: a task started without --size is normal, so Claude is told
// to judge it, or a one-word change takes the full path (SZ1).
const noSizeNote = `No size was given, so this task is normal. Size is impact, not lines: a low-impact change (a rename, a label or message, a typo, a local fix that callers can't notice) is small. If this one is, make it small now with plumbline task size small --why "...", which prints the small steps; otherwise keep going.`

// startChecklist: what the plan question will need, said up front so
// Claude meets it instead of being refused (UX2).
func startChecklist(size string) string {
	ask := `Then the plan question: AskUserQuestion with header "Checkpoint" and options "Approve", "Change something", "Drop it" (Drop it closes the task). First show the plan in the chat, as a message (not a file), the way checkpoints.md's "The plan in chat" shows: the goal line ("Done when: ..."), then short plain sections, and any suggestions from plan.md's ## Suggestions ("Related, want it in?" and "Not related, just noticed", apart). No task ids, notes paths or plumbline commands. Keep it short.`
	after := "After Approve: plumbline run checks before the first edit, build, run it again at the end, then the review and a short report."
	if size == "small" {
		return "Next (small): look at the code (SocratiCode and MemPalace; Serena for callers), and open every file you'll cite. Write plan.md in the notes folder with the Write tool: ## Goal first (one line, \"Done when: ...\", in the user's words); what changes, with file:line; ## Files (each file by name); ## Cases, each linked to a test (\"test: <file>\" or \"does not apply, because ...\"). No separate understanding note, no challenger, one code review. " + ask + " " + after
	}
	return "Next: look at the code (SocratiCode and MemPalace; Serena for callers), and open every file you'll cite. Write understanding.md (with ## Reuse) and plan.md (## Goal first: one line, \"Done when: ...\", in the user's words) in the notes folder with the Write tool, as checkpoints.md shows. Start the challenger, then add ## Challenge note to plan.md. " + ask + " " + after
}

// runChecks runs every saved check in turn (test, lint, build, format,
// typecheck, security; not related or e2e), each recorded as if run on its
// own, so none is forgotten (UX2). It goes on past a failing one, so each
// gets its run, and exits with the first failure's code.
func runChecks(args []string) (int, error) {
	root := mustRoot()
	s, ok, err := core.LoadSetup(root)
	if err != nil || !ok {
		return cmdRun(append([]string{"test"}, args...)) // the same setup errors as a single run
	}
	saved := []struct{ name, cmd string }{
		{"test", s.Commands.Test}, {"lint", s.Commands.Lint}, {"build", s.Commands.Build},
		{"format", s.Commands.Format}, {"typecheck", s.Commands.Typecheck}, {"security", s.Commands.Security},
	}
	exit := 0
	var summary []string
	for _, c := range saved {
		if strings.TrimSpace(c.cmd) == "" {
			continue
		}
		code, err := cmdRun(append([]string{c.name}, args...))
		if err != nil {
			return 1, err
		}
		status := "PASS"
		if code != 0 {
			status = "FAIL"
			if exit == 0 {
				exit = code
			}
		}
		summary = append(summary, c.name+": "+status)
	}
	if len(summary) == 0 {
		return 0, errors.New("no checks are saved for this project (run /plumbline:setup)")
	}
	fmt.Printf("\nplumbline: checks: %s\n", strings.Join(summary, ", "))
	return exit, nil
}

func cmdStatus(args []string) error {
	f, _, _ := flags(args)
	root := mustRoot()
	if core.IsPaused(root) {
		fmt.Println("Plumbline is PAUSED in this project (safety rules stay on).")
	}
	s, ok, err := core.LoadSetup(root)
	switch {
	case err != nil:
		fmt.Println("Setup: none yet (run /plumbline:setup).")
	case !ok:
		fmt.Println("Setup: changed since it was confirmed; saved commands won't run until the user re-confirms.")
	default:
		fmt.Printf("Setup: %s project, base branch %s.\n", s.Kind, s.BaseBranch)
	}
	for _, l := range core.SandboxLines(root) {
		fmt.Println(l)
	}
	t, terr := core.ActiveTask(root, "", f["task"])
	if terr != nil {
		return terr
	}
	if t == nil {
		fmt.Println("No open task on this branch.")
		return nil
	}
	fmt.Printf("Task %s \"%s\": %s step (%s).\nNotes: %s\n", t.ID, t.Title, t.Phase, t.Size, core.TaskDocsDir(root, t.ID))
	if tc, ok := t.TokenTotal(); ok {
		fmt.Printf("Tokens so far: %s\n", tc)
	}
	if t.Kind == core.KindDesign {
		fmt.Printf("Design task. Direction picked: %s\n", orNone(t.Direction))
	}
	for k, v := range t.ToolDown {
		fmt.Printf("Not used: %s (%s)\n", k, v)
	}
	for _, h := range t.Helpers {
		state := "running"
		switch {
		case !h.Finished.IsZero() && h.Complete():
			state = "finished"
		case !h.Finished.IsZero():
			state = "finished, but the code changed while it ran"
		}
		if h.Kind == core.HelperBuilder {
			state = h.BuilderState(ignoreOf(s))
		}
		fmt.Printf("Helper: %s %s, %s (%s)\n", h.Kind, h.Started.Local().Format("15:04"), state, h.Findings)
	}
	if t.Phase == core.PhaseBuilding && s != nil {
		if r := core.RequiredReviewers(t, s); len(r) > 0 {
			fmt.Println("Reviewers needed:", strings.Join(r, ", "))
		}
		miss := core.FinishMissing(t, s)
		if len(miss) == 0 {
			fmt.Println("Proof: complete on the current code.")
		} else {
			fmt.Println("Still missing:")
			for _, m := range miss {
				fmt.Println("  -", m)
			}
		}
		fmt.Printf("Plan files: %s\n", strings.Join(t.PlanFiles, ", "))
	}
	return nil
}

func cmdStandup(args []string) error {
	f, pos, _ := flags(args)
	root := mustRoot()
	if len(pos) > 0 && pos[0] == "tracker" {
		if p, ok := f["set"]; ok {
			abs, err := core.SetTracker(root, p)
			if err != nil {
				return err
			}
			fmt.Printf("Sprint file for this project: %s\nEdits to it always ask you first.\n", abs)
			return nil
		}
		if p := core.TrackerPath(root); p != "" {
			fmt.Println(p)
			return nil
		}
		fmt.Println("none")
		return nil
	}
	if len(pos) > 0 {
		return fmt.Errorf("unknown standup command %q", pos[0])
	}
	if d := f["date"]; d != "" {
		day, err := time.ParseInLocation("2006-01-02", d, time.Local)
		if err != nil {
			return errors.New("--date is YYYY-MM-DD, for example 2026-11-17")
		}
		r, err := core.Standup(root, day)
		if err != nil {
			return err
		}
		fmt.Print(r.String())
		return nil
	}
	// Since the last standup here, up to now; this one is remembered for
	// the next. A day named with --date isn't.
	now := time.Now()
	from, note := core.StandupSince(root, now)
	r, err := core.StandupBetween(root, from, now)
	if err != nil {
		return err
	}
	r.Note = note
	fmt.Print(r.String())
	if err := core.RecordStandup(root, from, now); err != nil {
		fmt.Printf("\nNote: this standup couldn't be remembered (%v), so the next one starts from the same place.\n", err)
	}
	return nil
}

func cmdDocsDir(args []string) error {
	f, _, _ := flags(args)
	root := mustRoot()
	t, err := activeTask(root, f)
	if err != nil {
		return err
	}
	fmt.Println(core.TaskDocsDir(root, t.ID))
	return nil
}

func cmdWaive(args []string) error {
	f, pos, _ := flags(args)
	root := mustRoot()
	if len(pos) == 0 {
		return errors.New("which check? proof:ui, proof:api, proof:schema, e2e, qa or bug-test")
	}
	check := pos[0]
	switch check {
	case "proof:ui", "proof:api", "proof:schema", "e2e", "qa", "bug-test":
	default:
		return errors.New("only real-world proof (proof:ui, proof:api, proof:schema), e2e, blind QA (qa) or a [bug] case's failing run before the fix (bug-test) can be waived. Never tests on changed code, reviews on risky changes, or safety rules")
	}
	if strings.TrimSpace(f["reason"]) == "" {
		return errors.New(`say why it can't run: --reason "..."`)
	}
	t, err := lockedTask(root, f)
	if err != nil {
		return err
	}
	s, _, _ := core.LoadSetup(root)
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	fp, err := core.Fingerprint(root, ignore)
	if err != nil {
		return err
	}
	t.Waivers = append(t.Waivers, core.Waiver{Check: check, Reason: f["reason"], Print: fp, At: core.Now()})
	core.LogEvent(core.Event{Type: "waiver", Task: t.ID, Rule: check, Size: t.Size, Project: core.ProjectKey(root)})
	fmt.Printf("Waived %s for task %s on the current code (lapses when the code changes). It will be listed in the report.\n", check, t.ID)
	return core.SaveTask(t)
}

func ignoreOf(s *core.Setup) []string {
	if s == nil {
		return nil
	}
	return s.IgnoreChanges
}

// cmdBuilder lists, merges and drops the builders' copies of the repo.
func cmdBuilder(args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	f, pos, _ := flags(args[1:])
	root := mustRoot()
	s, _, _ := core.LoadSetup(root)
	ignore := ignoreOf(s)
	if args[0] == "list" {
		t, err := activeTask(root, f)
		if err != nil {
			return err
		}
		n := 0
		for _, h := range t.Helpers {
			if h.Kind != core.HelperBuilder {
				continue
			}
			n++
			fmt.Printf("builder %d: %s. Copy: %s. Notes: %s\n", n, h.BuilderState(ignore), orNone(h.Worktree), h.Findings)
		}
		if n == 0 {
			fmt.Println("No builders have run in this task.")
		}
		return nil
	}
	if args[0] != "merge" && args[0] != "drop" {
		return fmt.Errorf("unknown builder command %q: list, merge <n> or drop <n>", args[0])
	}
	if len(pos) == 0 {
		return fmt.Errorf("which builder? plumbline builder %s <n> (plumbline builder list shows them)", args[0])
	}
	n, err := strconv.Atoi(pos[0])
	if err != nil || n < 1 {
		return fmt.Errorf("builder number %q isn't a number from plumbline builder list", pos[0])
	}
	t, err := lockedTask(root, f)
	if err != nil {
		return err
	}
	if args[0] == "drop" {
		if err := core.DropBuilder(t, n); err != nil {
			return err
		}
		core.LogEvent(core.Event{Type: "helper", Task: t.ID, Size: t.Size, Project: core.ProjectKey(root), Rule: "builder-drop"})
		fmt.Printf("Dropped builder %d's copy; none of its changes are in the task's code.\n", n)
		return core.SaveTask(t)
	}
	changed, conflicts, err := core.MergeBuilder(t, n, ignore)
	if errors.Is(err, core.ErrConflict) {
		core.LogEvent(core.Event{Type: "helper", Task: t.ID, Size: t.Size, Project: core.ProjectKey(root), Rule: "builder-conflict", Detail: fmt.Sprintf("%d files", len(conflicts))})
		r, _ := t.BuilderRun(n)
		return fmt.Errorf("builder %d's changes overlap changes already in the task's code, in: %s. Nothing was written. Bring those parts over by hand from %s (then plumbline builder drop %d), or give the part to a builder again", n, strings.Join(conflicts, ", "), r.Worktree, n)
	}
	if err != nil {
		if len(changed) > 0 {
			return fmt.Errorf("the merge of builder %d stopped part way: %v. Already written in the task's code: %s. Check those files, then finish the rest by hand from its copy", n, err, strings.Join(changed, ", "))
		}
		return err
	}
	core.LogEvent(core.Event{Type: "helper", Task: t.ID, Size: t.Size, Project: core.ProjectKey(root), Rule: "builder-merge", Detail: fmt.Sprintf("%d files", len(changed))})
	if err := core.SaveTask(t); err != nil {
		return err
	}
	if len(changed) == 0 {
		fmt.Printf("Builder %d's changes were already in the task's code; its copy is removed.\n", n)
		return nil
	}
	fmt.Printf("Merged builder %d into the task's code (%d files: %s). Its copy is removed. Run the related tests next.\n", n, len(changed), strings.Join(changed, ", "))
	if r, _ := t.BuilderRun(n); r != nil && r.Status == "done with concerns" {
		fmt.Printf("It finished with concerns: read them in its notes (%s) and settle each before the report.\n", r.Findings)
	}
	return nil
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func cmdSetup(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	f, _, _ := flags(args[1:])
	switch args[0] {
	case "show":
		fmt.Print(laptopState())
		root := mustRoot()
		p, found := core.SetupPath(root)
		if !found {
			fmt.Println("No setup yet.")
			return nil
		}
		_, ok, err := core.LoadSetup(root)
		if err != nil {
			return err
		}
		b, _ := os.ReadFile(p)
		fmt.Printf("Setup file: %s (confirmed: %v)\n%s", p, ok, b)
		return nil
	case "save":
		root := mustRoot()
		var r io.Reader = os.Stdin
		if f["file"] != "" {
			fh, err := os.Open(f["file"])
			if err != nil {
				return err
			}
			defer fh.Close()
			r = fh
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		var s core.Setup
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("setup JSON: %v", err)
		}
		p, err := core.SaveSetup(root, &s)
		if err != nil {
			return err
		}
		fmt.Printf("Saved and confirmed: %s\n", p)
		if dead := core.DeadIgnoreGlobs(root, s.IgnoreChanges); len(dead) > 0 {
			fmt.Printf("Note: ignore_changes %s only names files git already ignores, so it isn't needed (git never counts them).\n", strings.Join(dead, ", "))
		}
		return nil
	case "laptop":
		return setupLaptop(f)
	case "undo-laptop":
		return undoLaptop()
	case "sandbox":
		if f["exclude"] == "true" { // --exclude with nothing after it
			return errors.New("--exclude takes command names: --exclude psql,mysql")
		}
		extra, err := core.ParseExclusions(f["exclude"])
		if err != nil {
			return err
		}
		root := mustRoot()
		changed, err := core.EnableSandbox(root, extra)
		if err != nil {
			return err
		}
		fmt.Printf("Changed in %s:\n", filepath.Join(root, core.SandboxLocalFile))
		for _, c := range changed {
			fmt.Println("  -", c)
		}
		fmt.Println("Start a new Claude Code session to be sure it applies, then check with plumbline status.")
		fmt.Println("Undo any time with: plumbline setup undo-sandbox")
		return nil
	case "undo-sandbox":
		root := mustRoot()
		if err := core.UndoSandbox(root); err != nil {
			return err
		}
		fmt.Printf("Sandbox settings in %s put back to how they were before Plumbline.\n", filepath.Join(root, core.SandboxLocalFile))
		return nil
	}
	return fmt.Errorf("unknown setup command %q", args[0])
}

func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}
