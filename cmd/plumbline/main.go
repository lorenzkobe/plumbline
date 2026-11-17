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
	"sort"
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
	var err error
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

func usage() {
	fmt.Fprint(os.Stderr, `plumbline: a careful task workflow for Claude Code

  plumbline status                      where the task is, and what's missing
  plumbline task start --title "..." [--ticket X] [--size small|normal|big --why "..."]
  plumbline task show|list|replan|finish
  plumbline task tool-down serena|context7|jira --reason "..."
  plumbline task adopt <id>             continue an earlier task in this session
  plumbline run test|related|lint|build|e2e [--files a,b] [--task id]
  plumbline run proof ui|api|schema -- <command>
  plumbline docs-dir                    the task's notes folder
  plumbline setup show|save|laptop|undo-laptop
  plumbline waive proof:ui|proof:api|proof:schema|e2e --reason "..."
  plumbline stats [--months N]
  plumbline pause|resume                (your terminal only)
`)
}

// runHook never lets a crash pass silently: a gate that fails denies.
func runHook(args []string) (code int) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "plumbline hook: which event?")
		return 2
	}
	event := args[0]
	gate := event == "pre"
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "Plumbline hook crashed (%v).", r)
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
		fmt.Fprintln(os.Stderr, "Plumbline couldn't read the hook input:", err)
		if gate {
			return 2
		}
		return 0
	}
	var out core.Output
	switch event {
	case "pre":
		out = core.PreToolUse(in)
	case "post":
		out = core.PostToolUse(in)
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
		if !dockerOK() {
			return errors.New("the Docker app isn't running. It's required (SocratiCode and the test database). Start it, then try again")
		}
		size := f["size"]
		if size == "" {
			size = "normal"
		}
		if size != "small" && size != "normal" && size != "big" {
			return errors.New("--size is small, normal or big")
		}
		pre, _ := core.DirtyFiles(root)
		t := &core.Task{
			ID: newTaskID(), Title: title, Ticket: f["ticket"], Size: size, SizeWhy: f["why"],
			Root: root, Branch: core.CurrentBranch(root), Phase: core.PhaseUnderstanding,
			Created: core.Now(), Preexisting: pre, StartCommit: core.HeadCommit(root),
		}
		if err := os.MkdirAll(core.TaskDocsDir(root, t.ID), 0o700); err != nil {
			return err
		}
		if err := core.SaveTask(t); err != nil {
			return err
		}
		core.LogEvent(core.Event{Type: "task", Task: t.ID, Size: size, Project: core.ProjectKey(root), Rule: "start"})
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
		fmt.Println("Next: read the task, check the tools, trace the code (SocratiCode, Serena, MemPalace), write understanding.md and plan.md, then the checkpoint.")
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
		t.Phase = core.PhaseDone
		core.LogEvent(core.Event{Type: "phase", Task: t.ID, Rule: core.PhaseDone, Size: t.Size, Project: core.ProjectKey(root), Detail: fmt.Sprintf("commits=%d", t.Commits)})
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
		return 0, errors.New("run what? test, related, lint, build, e2e, or proof")
	}
	kind := args[0]
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
		files := []string{}
		if f["files"] != "" {
			files = strings.Split(f["files"], ",")
		} else {
			d, _ := core.DirtyFiles(root)
			for p := range d {
				if t != nil {
					if _, mine := t.Preexisting[p]; mine {
						continue
					}
				}
				files = append(files, p)
			}
			sort.Strings(files)
		}
		q := []string{}
		for _, x := range files {
			q = append(q, shellQuote(strings.TrimSpace(x)))
		}
		command = strings.ReplaceAll(s.Commands.TestRelated, "{files}", strings.Join(q, " "))
		kind = core.KindRelated
	case "lint":
		command, kind = s.Commands.Lint, core.KindLint
	case "build":
		command, kind = s.Commands.Build, core.KindBuild
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
	default:
		return 0, fmt.Errorf("unknown run kind %q", kind)
	}
	if strings.TrimSpace(command) == "" {
		return 0, fmt.Errorf("no %s command is saved for this project (run /plumbline:setup)", args[0])
	}
	fmt.Printf("plumbline: running %s: %s\n", args[0], command)
	r, err := core.Execute(root, t, kind, sub, command, s, os.Stdout)
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
	t, terr := core.ActiveTask(root, "", f["task"])
	if terr != nil {
		return terr
	}
	if t == nil {
		fmt.Println("No open task on this branch.")
		return nil
	}
	fmt.Printf("Task %s \"%s\": %s step (%s).\nNotes: %s\n", t.ID, t.Title, t.Phase, t.Size, core.TaskDocsDir(root, t.ID))
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
		return errors.New("which check? proof:ui, proof:api, proof:schema or e2e")
	}
	check := pos[0]
	switch check {
	case "proof:ui", "proof:api", "proof:schema", "e2e":
	default:
		return errors.New("only real-world proof (proof:ui, proof:api, proof:schema) or e2e can be waived. Never tests on changed code, reviews on risky changes, or safety rules")
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

func cmdSetup(args []string) error {
	if len(args) == 0 {
		args = []string{"show"}
	}
	f, _, _ := flags(args[1:])
	switch args[0] {
	case "show":
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
		return nil
	case "laptop":
		return setupLaptop(f)
	case "undo-laptop":
		return undoLaptop()
	}
	return fmt.Errorf("unknown setup command %q", args[0])
}

func dockerOK() bool {
	if os.Getenv("PLUMBLINE_SKIP_DOCKER") == "1" { // tests only
		return true
	}
	return core.DockerUp()
}

func newTaskID() string { return core.NewID() }

func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude", "settings.json")
}
