package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hookCtx is what every hook works out first.
type hookCtx struct {
	in       *HookInput
	root     string
	paused   bool
	task     *Task
	taskErr  error
	setup    *Setup
	setupOK  bool
	setupErr error
	unlock   func()
}

func newCtx(in *HookInput) *hookCtx {
	c := &hookCtx{in: in}
	c.root = RepoRoot(in.Cwd)
	if c.root != "" {
		c.paused = IsPaused(c.root)
		c.task, c.taskErr = ActiveTask(c.root, in.SessionID, "")
		if c.task == nil && c.taskErr == nil && in.AgentID != "" && in.SessionID != "" {
			// Inside a helper whose hook input doesn't carry the lead's
			// session: fall back to the one open task on this branch.
			c.task, c.taskErr = ActiveTask(c.root, "", "")
		}
		if c.task != nil {
			// Hold the task for the whole hook, and read it again under the
			// lock so this hook sees every save made before it.
			c.unlock = LockTask(c.root, c.task.ID)
			if t, err := LoadTask(c.root, c.task.ID); err == nil {
				c.task = t
			} else {
				c.task, c.taskErr = nil, err
			}
		}
		c.setup, c.setupOK, c.setupErr = LoadSetup(c.root)
	}
	return c
}

// close releases the task lock; every hook defers it.
func (c *hookCtx) close() {
	if c.unlock != nil {
		c.unlock()
		c.unlock = nil
	}
}

func (c *hookCtx) projectKey() string {
	if c.root == "" {
		return ""
	}
	return ProjectKey(c.root)
}

func (c *hookCtx) log(typ, rule, detail string) {
	e := Event{Type: typ, Rule: rule, Detail: limit(detail, 160), Project: c.projectKey()}
	if c.task != nil {
		e.Task = c.task.ID
		e.Size = c.task.Size
	}
	LogEvent(e)
}

func (c *hookCtx) deny(rule, reason string) Output {
	c.log("block", rule, reason)
	return Decision("PreToolUse", "deny", msg(reason))
}

func (c *hookCtx) ask(rule, reason string) Output {
	c.log("ask", rule, reason)
	return Decision("PreToolUse", "ask", msg(reason))
}

func allow(reason string) Output {
	return Decision("PreToolUse", "allow", msg(reason))
}

// PreToolUse is the gate for edits, terminal commands, reads of secret files,
// checkpoint questions and helpers.
func PreToolUse(in *HookInput) Output {
	c := newCtx(in)
	defer c.close()
	if errors.Is(c.taskErr, ErrTampered) {
		switch in.ToolName {
		case "Edit", "Write", "MultiEdit", "NotebookEdit", "Bash", "AskUserQuestion":
			return c.deny("tamper", "the task state was changed outside Plumbline, so its proof can't be trusted. Stop and tell the user; they can drop the task with /plumbline:cancel and start again.")
		}
	}
	if kind := helperKind(in.AgentType, false); kind != "" && in.AgentID != "" {
		if o := c.gateHelper(kind); o != nil {
			return o
		}
	}
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		return c.gateEdit(p)
	case "Read":
		p := Abs(in.Str("file_path"), in.Cwd)
		if IsSecretFile(p) {
			return c.ask("secret-read", fmt.Sprintf("reading %s shows its values in this chat, and the chat history is kept on your laptop. Allow only if Claude really needs to see the values (for example to debug production). Claude never repeats them.", filepath.Base(p)))
		}
	case "Bash":
		return c.gateBash(in.Str("command"))
	case "AskUserQuestion":
		return c.gateQuestion()
	case "Agent", "Task":
		if in.AgentID != "" {
			return c.deny("no-nested-helpers", "helpers can't start other helpers. Report back to the lead instead.")
		}
		if kind := helperKind(in.Str("subagent_type"), true); kind != "" {
			return c.gateHelperStart(kind)
		}
	}
	return nil
}

// hold records a file Claude was refused, so the report must list it.
func (c *hookCtx) hold(rel, why string) {
	if c.task == nil {
		return
	}
	if c.task.Held == nil {
		c.task.Held = map[string]string{}
	}
	c.task.Held[rel] = why
	_ = SaveTask(c.task)
}

var reRisky = regexp.MustCompile(`(?i)(^|/)(auth|authn|authz|oauth|login|sessions?|permissions?|acl|rbac|roles?|payments?|billing|stripe|invoices?|checkout|migrations?|migrate|infra|infrastructure|terraform|deploy|deployment|k8s|helm|\.github)(/|\.|_|-|$)`)

var riskyNames = map[string]bool{
	"dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true, "compose.yml": true, "compose.yaml": true,
	"serverless.yml": true, "serverless.yaml": true, "serverless.ts": true, "vercel.json": true, "netlify.toml": true,
	"fly.toml": true, ".gitlab-ci.yml": true, "jenkinsfile": true, "package.json": true, "package-lock.json": true,
	"yarn.lock": true, "pnpm-lock.yaml": true, "bun.lockb": true, "go.mod": true, "go.sum": true, "pyproject.toml": true,
	"poetry.lock": true, "requirements.txt": true, "pipfile": true, "pipfile.lock": true, "gemfile": true,
	"gemfile.lock": true, "composer.json": true, "composer.lock": true, "cargo.toml": true, "cargo.lock": true,
	"supabase.toml": true, "config.toml": true,
}

// RiskyPath: kinds of files where a change outside the approved plan could
// spread far. Setup can add more with risky_paths.
func RiskyPath(rel string, s *Setup) bool {
	low := strings.ToLower(filepath.ToSlash(rel))
	if riskyNames[filepath.Base(low)] || strings.HasSuffix(low, ".tf") || strings.HasSuffix(low, ".sql") {
		return true
	}
	if reRisky.MatchString(low) {
		return true
	}
	return s != nil && MatchAny(s.RiskyPaths, rel)
}

func claudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".claude")
}

// isProtectedConfig: Claude Code's own settings, installed plugins, and the
// global config. Editing these could switch Plumbline off.
func isProtectedConfig(abs string) bool {
	base := filepath.Base(abs)
	parent := filepath.Base(filepath.Dir(abs))
	if parent == ".claude" && (base == "settings.json" || base == "settings.local.json") {
		return true
	}
	h, _ := os.UserHomeDir()
	if abs == filepath.Join(h, ".claude.json") {
		return true
	}
	ch := claudeHome()
	if base == "settings.json" && filepath.Dir(abs) == ch {
		return true
	}
	if IsInside(abs, filepath.Join(ch, "plugins")) {
		return true
	}
	if strings.Contains(filepath.ToSlash(abs), "managed-settings") {
		return true
	}
	return false
}

func (c *hookCtx) gateEdit(path string) Output {
	if path == "" {
		return nil
	}
	abs := Abs(path, c.in.Cwd)
	if IsInside(abs, Abs(DataDir(), "/")) {
		if c.task != nil && IsInside(abs, Abs(TaskDocsDir(c.root, c.task.ID), "/")) && strings.HasSuffix(abs, ".md") {
			if reFindings.MatchString(filepath.Base(abs)) {
				return c.deny("helper-findings", "challenge-N.md and review-N.md are the helpers' own findings; only the helper that wrote one can change it. Answer the findings in plan.md (## Challenge note) or report.md (## Review).")
			}
			return allow("task notes")
		}
		return c.deny("own-files", "Plumbline's own files (task state, proof, logs) can't be edited. Task notes go in the task's docs folder (plumbline docs-dir).")
	}
	if IsSecretFile(abs) {
		return c.deny("secret-edit", "Claude doesn't edit .env or key files. Tell the user which variable to add or change (name only) and they make the change. Templates like .env.example are fine.")
	}
	if isProtectedConfig(abs) {
		return c.deny("config-protect", "Claude Code's settings and installed plugins are protected so Plumbline can't be switched off by Claude. Tell the user what to change and why.")
	}
	if c.root != "" && abs == filepath.Join(c.root, RepoSetupFile) {
		return c.ask("setup-change", "this changes Plumbline's saved project setup, including the commands Claude may run before approval. Check the change before allowing it.")
	}
	if c.paused {
		return nil
	}
	inRepo := c.root != "" && IsInside(abs, c.root)
	if !inRepo && (IsInside(abs, Abs(os.TempDir(), "/")) || (c.in.ScratchpadDir != "" && IsInside(abs, Abs(c.in.ScratchpadDir, "/")))) {
		return nil // throwaway copies for exploring
	}
	if c.root == "" || !IsInside(abs, c.root) {
		return c.ask("outside-project", fmt.Sprintf("%s is outside this project.", abs))
	}
	if c.task == nil {
		return c.deny("no-task", "no task is active, so code can't change yet. If the user wants a change, ask in one line whether to start the task workflow, then run: plumbline task start --title \"...\". To try something out, use a throwaway copy in the scratchpad instead.")
	}
	if c.task.Phase != PhaseBuilding {
		return c.deny("before-approval", fmt.Sprintf("task %s is at the %s step. Code changes wait until the user approves the plan at the checkpoint.", c.task.ID, c.task.Phase))
	}
	rel := c.task.Rel(abs)
	if c.task.InPlan(rel) {
		return nil
	}
	if _, ok := c.task.Preexisting[rel]; ok {
		c.hold(rel, "your own unsaved changes were in it before the task")
		return c.deny("user-work", fmt.Sprintf("%s had the user's own unsaved changes before this task started, so Claude doesn't touch it. Keep going with the rest, and list it under \"For you\" in the report.", rel))
	}
	if RiskyPath(rel, c.setup) {
		c.hold(rel, "risky file outside the plan")
		return c.deny("risky-outside-plan", fmt.Sprintf("%s isn't in the approved plan and is a risky kind of file (auth, payments, migrations, dependencies, config, deploy). Don't change it. Keep going with the rest, and list it under \"For you\" in the report with what you'd change and why.", rel))
	}
	if _, seen := c.task.OutsidePlan[rel]; !seen && c.task.OutsideCount() >= OutsidePlanCap {
		c.log("block", "outside-plan-cap", rel)
		return c.deny("outside-plan-cap", fmt.Sprintf("%s would be file %d outside the approved plan. That many means the plan was likely wrong, which is the user's decision. Stop and ask them (with your suggestion): go back to the plan (plumbline task replan, update ## Files, ask the checkpoint again), or something else. Finish anything that doesn't depend on it first.", rel, OutsidePlanCap+1))
	}
	return nil // part of the same change: allowed, recorded, and must be listed in the report
}

var (
	installRe = regexp.MustCompile(`^(npm (i|install|add)|yarn add|pnpm (add|i|install)|bun (add|install|i)|pip3? install|uv (add|pip install)|poetry add|pipenv install|go (get|install)|cargo (add|install)|composer require|gem install|brew install|apt(-get)? install|npx|pnpx|bunx)\b`)
	cloudCmds = map[string]bool{
		"aws": true, "sls": true, "serverless": true, "vercel": true, "kubectl": true, "helm": true,
		"terraform": true, "tofu": true, "pulumi": true, "gcloud": true, "az": true, "heroku": true,
		"fly": true, "flyctl": true, "doctl": true, "firebase": true, "netlify": true, "railway": true,
		"ssh": true, "scp": true, "cdk": true, "sam": true, "eb": true, "wrangler": true, "render": true,
	}
	dbClients = map[string]bool{"psql": true, "mysql": true, "mongosh": true, "mongo": true, "redis-cli": true, "pg_dump": true, "pg_restore": true, "mysqldump": true}
)

func isLocalHost(s string) bool {
	return strings.Contains(s, "localhost") || strings.Contains(s, "127.0.0.1") || strings.Contains(s, "::1") || strings.Contains(s, "host.docker.internal")
}

func wordsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (c *hookCtx) isSaved(cmd string, seg *Segment) bool {
	if c.setup == nil || !c.setupOK {
		return false
	}
	for _, s := range c.setup.SavedCommands() {
		if strings.TrimSpace(cmd) == s {
			return true
		}
		if seg != nil {
			for _, ss := range Segments(s) {
				if wordsEqual(ss.Words, seg.Words) {
					return true
				}
			}
		}
	}
	return false
}

func mentionsDataDir(s Segment) bool {
	dd := DataDir()
	for _, w := range append(append([]string{}, s.Words...), s.Redirects...) {
		if strings.Contains(w, dd) || strings.Contains(w, "/.plumbline/") || strings.HasSuffix(w, "/.plumbline") || strings.HasPrefix(w, "~/.plumbline") || strings.HasPrefix(w, "$HOME/.plumbline") || strings.HasPrefix(w, ".plumbline/") {
			return true
		}
	}
	return false
}

func mentionsProtectedConfig(s Segment, cwd string) bool {
	for _, w := range append(append([]string{}, s.Words...), s.Redirects...) {
		if strings.Contains(w, ".claude/settings") || strings.HasSuffix(w, ".claude.json") || strings.Contains(w, ".claude/plugins") || strings.Contains(w, "managed-settings") {
			return true
		}
		if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || strings.HasPrefix(w, ".") {
			if isProtectedConfig(Abs(w, cwd)) {
				return true
			}
		}
	}
	return false
}

func isPlumblineBin(name string) bool {
	return name == "plumbline" || strings.HasPrefix(name, "plumbline-darwin") || strings.HasPrefix(name, "plumbline-linux")
}

// plumblineReads: plumbline subcommands that only show things.
func plumblineReads(s Segment) bool {
	if !isPlumblineBin(s.Name()) {
		return false
	}
	switch s.Arg(1) {
	case "status", "docs-dir", "version", "--version", "-v", "stats", "help", "--help", "-h":
		return true
	case "task":
		return s.Arg(2) == "show" || s.Arg(2) == "list"
	case "setup":
		return s.Arg(2) == "show" || s.Arg(2) == ""
	}
	return false
}

type verdicts struct {
	deny, ask []string
	denyRule  string
	askRule   string
}

func (v *verdicts) d(rule, r string) {
	if len(v.deny) == 0 {
		v.denyRule = rule
	}
	v.deny = append(v.deny, r)
}

func (v *verdicts) a(rule, r string) {
	if len(v.ask) == 0 {
		v.askRule = rule
	}
	v.ask = append(v.ask, r)
}

func (c *hookCtx) gateBash(cmd string) Output {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	segs := Segments(cmd)
	v := &verdicts{}
	allQuiet := true // every segment reads only, or is a saved command
	hasCommit := false
	for i := range segs {
		s := segs[i]
		name := s.Name()
		quiet := ReadOnly(s)
		switch {
		case name == "claude":
			switch s.Arg(1) {
			case "plugin", "plugins", "config":
				v.d("config-protect", "commands that change Claude Code itself (claude plugin, claude config) are blocked so Plumbline stays on. Tell the user what to run.")
			case "mcp":
				if s.Arg(2) != "list" && s.Arg(2) != "get" {
					v.a("mcp-change", "this changes Claude Code's MCP servers.")
				}
			}
		case isPlumblineBin(name):
			switch s.Arg(1) {
			case "hook":
				v.d("own-files", "Plumbline's hooks are run by Claude Code only.")
			case "pause", "resume", "cancel", "wrong":
				v.d("user-only", fmt.Sprintf("only the user can %s, by typing /plumbline:%s.", s.Arg(1), s.Arg(1)))
			case "setup":
				if s.Arg(2) != "show" && s.Arg(2) != "" {
					v.a("setup-change", "this saves Plumbline setup. Check the values before allowing.")
				}
			case "waive":
				v.a("waiver", "a waiver skips one check for this task. Only allow it if that check truly can't run.")
			case "task":
				if s.Arg(2) == "adopt" {
					v.a("adopt", "this continues an earlier task in this session.")
				}
			case "run":
				if s.Arg(2) == "proof" {
					inner := afterDashes(s.Words)
					if len(inner) > 0 {
						is := Segment{Words: inner}
						quiet = ReadOnly(is) || c.isSaved(strings.Join(inner, " "), &is)
					} else {
						quiet = true
					}
				} else {
					quiet = true // runs a saved command
				}
			}
			if s.Arg(1) == "task" && s.Arg(2) == "start" || s.Arg(1) == "task" && s.Arg(2) == "tool-down" || s.Arg(1) == "task" && s.Arg(2) == "finish" || plumblineReads(s) {
				quiet = true
			}
		case name == "git":
			sub, args := GitSub(s)
			switch sub {
			case "push":
				v.d("no-push", "pushing is always the user's job. Tell them the branch is ready to push.")
			case "commit":
				hasCommit = true
				for _, a := range args {
					if a == "--no-verify" || a == "-n" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "n") && !strings.ContainsAny(a, "m")) {
						v.d("no-verify", "--no-verify is blocked for Claude.")
					}
				}
			case "reset":
				for _, a := range args {
					if a == "--hard" || a == "--merge" || a == "--keep" {
						v.a("destructive-git", "git reset --hard throws away uncommitted work, including yours.")
					}
				}
			case "clean":
				v.a("destructive-git", "git clean deletes untracked files, including yours.")
			case "checkout", "switch", "restore":
				discard := false
				for _, a := range args {
					if a == "--" || a == "." || a == "-f" || a == "--force" || a == "--discard-changes" {
						discard = true
					}
				}
				if sub == "restore" && !contains(args, "--staged") {
					discard = true
				}
				if discard {
					v.a("destructive-git", "this throws away uncommitted changes in those files.")
				} else if contains(args, "-b") || contains(args, "-B") || contains(args, "-c") || contains(args, "--create") {
					// a new branch carries the changes along; nothing is lost
				} else if c.root != "" {
					if d, _ := DirtyFiles(c.root); len(d) > 0 {
						v.a("dirty-switch", "there are uncommitted changes. Switching branches carries them along or fails; check what should happen to them first.")
					}
				}
			case "stash":
				if len(args) > 0 && (args[0] == "drop" || args[0] == "clear") {
					v.a("destructive-git", "this deletes stashed work.")
				}
			case "rebase", "merge", "cherry-pick", "am":
				v.a("history-change", "this changes branch history.")
			case "branch":
				for _, a := range args {
					if a == "-D" || a == "--delete" || a == "-d" {
						v.a("destructive-git", "this deletes a branch.")
					}
				}
			}
		}
		if f := ReadsSecretFile(s); f != "" {
			v.a("secret-read", fmt.Sprintf("this shows the values in %s in the chat. Allow only if Claude really needs to see them. To just use them, load and use in one step (for example: set -a; . ./.env; set +a; psql ...).", filepath.Base(f)))
		}
		for _, r := range s.Redirects {
			if IsSecretFile(r) {
				v.d("secret-edit", "Claude doesn't write to .env or key files. Tell the user the variable name to add.")
			}
		}
		if !quiet && !isPlumblineBin(name) {
			for _, w := range s.Words[1:] {
				if IsSecretFile(w) && (name == "sed" || name == "cp" || name == "mv" || name == "tee" || name == "rm" || name == "truncate" || name == "perl" || name == "ln") {
					v.d("secret-edit", "Claude doesn't change .env or key files. Tell the user the variable name to add.")
				}
			}
		}
		if !quiet && !isPlumblineBin(name) && mentionsDataDir(s) {
			v.d("own-files", "Plumbline's own files can't be changed from the terminal. Use the plumbline command.")
		}
		if !quiet && mentionsProtectedConfig(s, c.in.Cwd) {
			v.d("config-protect", "Claude Code's settings and plugins are protected. Tell the user what to change.")
		}
		joined := strings.Join(s.Words, " ")
		approved := c.task != nil && c.task.Phase == PhaseBuilding && c.task.Approved(s.Words)
		if installRe.MatchString(joined) && !(len(s.Words) == 2 && (s.Words[1] == "install" || s.Words[1] == "i")) && !approved {
			if c.task != nil {
				v.d("new-package", "this installs a package that isn't in the approved plan's \"Needs your OK\" list. Don't install it. Keep going with what you can, and put it under \"For you\" in the report: why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.")
			} else {
				v.a("new-package", "this installs a package. Say why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.")
			}
		}
		if approved {
			// in the plan you approved: runs as listed
		} else if cloudCmds[name] {
			v.a("outside-laptop", fmt.Sprintf("%s reaches systems outside your laptop. Production is never changed without the user's explicit OK; reads are asked each time.", name))
		}
		if name == "supabase" && s.Arg(1) != "start" && s.Arg(1) != "stop" && s.Arg(1) != "status" {
			v.a("outside-laptop", "supabase commands can reach the hosted project.")
		}
		if dbClients[name] && !isLocalHost(joined) {
			v.a("outside-laptop", fmt.Sprintf("%s without a local host may connect to a shared or production database.", name))
		}
		if name == "docker" && (s.Arg(1) == "push" || s.Arg(1) == "login") {
			v.a("outside-laptop", "this reaches a registry outside your laptop.")
		}
		if name == "curl" || name == "wget" || name == "http" {
			if !isLocalHost(joined) {
				for _, w := range s.Words {
					if w == "-X" || w == "--request" || w == "-d" || strings.HasPrefix(w, "--data") || w == "-F" || w == "--form" || w == "-T" || w == "--upload-file" {
						v.a("outside-laptop", "this sends data to a server outside your laptop.")
						break
					}
				}
			}
		}
		if !quiet && !c.isSaved(cmd, &s) {
			allQuiet = false
		}
	}
	if hasCommit && len(v.deny) == 0 {
		if o := c.gateCommit(cmd); o != nil {
			return o
		}
	}
	if len(v.deny) > 0 {
		return c.deny(v.denyRule, strings.Join(uniq(v.deny), " "))
	}
	if !c.paused && (c.task == nil || c.task.Phase != PhaseBuilding) && !allQuiet && !hasCommit {
		if c.task != nil {
			// The user is often away while Claude researches, so refuse
			// instead of waiting on a prompt.
			return c.deny("before-approval", "the plan isn't approved yet, so only reading and the project's saved commands can run. Reproduce with the saved commands or in a throwaway copy (scratchpad or /tmp), or put this command in the plan's \"Needs your OK\" list.")
		}
		v.a("before-approval", "no task is active, so only reading and the project's saved commands run without asking. Allow this if it's fine to run now.")
	}
	if len(v.ask) > 0 {
		return c.ask(v.askRule, strings.Join(uniq(v.ask), " "))
	}
	if allQuiet && !hasCommit && !c.paused && (c.task == nil || c.task.Phase != PhaseBuilding) {
		return allow("reads only, or a saved project command")
	}
	return nil
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func afterDashes(w []string) []string {
	for i, x := range w {
		if x == "--" {
			return w[i+1:]
		}
	}
	return nil
}

// gateCommit is the commit rule: no commit without proof on the current code.
func (c *hookCtx) gateCommit(cmd string) Output {
	if c.root == "" {
		return nil
	}
	m, ok := ParseCommitMessage(cmd, c.in.Cwd)
	if !ok {
		return c.deny("commit-message", "write the commit message with -m \"...\" so it can be checked.")
	}
	var problems []string
	if c.setup != nil {
		problems = append(problems, CheckCommitMessage(m, c.setup)...)
	} else {
		problems = append(problems, CheckCommitMessage(m, nil)...)
	}
	if secrets := c.scanForCommit(cmd); len(secrets) > 0 {
		return c.deny("secrets", "this commit looks like it contains secrets: "+strings.Join(secrets, "; ")+". Move them to environment variables (the value never goes in the repo), then commit.")
	}
	if len(problems) > 0 {
		return c.deny("commit-message", strings.Join(problems, "; ")+".")
	}
	subject := strings.SplitN(strings.TrimSpace(m), "\n", 2)[0]
	if c.paused {
		return c.ask("commit", fmt.Sprintf("commit \"%s\" (Plumbline is paused, so proof isn't checked).", subject))
	}
	if c.task == nil {
		return c.deny("commit-no-task", "commits happen inside a task, after the report and the user's OK. If this commit is outside any task, the user can pause Plumbline with /plumbline:pause.")
	}
	if c.task.Phase != PhaseBuilding {
		return c.deny("commit-phase", fmt.Sprintf("task %s is at the %s step; nothing can be committed before the plan is approved and proven.", c.task.ID, c.task.Phase))
	}
	if !c.setupOK || c.setup == nil {
		return c.deny("setup", "the project setup isn't confirmed, so its test command can't be trusted. Run /plumbline:setup.")
	}
	final := c.isFinalCommit(cmd)
	missing := ProofMissing(c.task, c.setup, final)
	if len(missing) > 0 {
		return c.deny("commit-proof", "no commit without proof on the current code: "+strings.Join(missing, "; ")+".")
	}
	kind := "slice commit, related tests passed"
	if final {
		kind = "last commit of the task, full suite passed"
	}
	return c.ask("commit", fmt.Sprintf("commit \"%s\" (%s).", subject, kind))
}

func (c *hookCtx) scanForCommit(cmd string) []string {
	diff := StagedDiff(c.root)
	if strings.Contains(cmd, "-a") || strings.Contains(cmd, "git add") || strings.Contains(cmd, "--all") {
		diff += "\n" + WorkingDiff(c.root, "HEAD")
		if d, err := DirtyFiles(c.root); err == nil {
			for p := range d {
				full := filepath.Join(c.root, p)
				if _, err := gitOut(c.root, "ls-files", "--error-unmatch", p); err != nil {
					if fi, err := os.Stat(full); err == nil && fi.Size() < 1<<20 && !fi.IsDir() {
						b, _ := os.ReadFile(full)
						diff += "\n+++ b/" + p + "\n@@ -0,0 +1 @@\n"
						for _, l := range strings.Split(string(b), "\n") {
							diff += "+" + l + "\n"
						}
					}
				}
			}
		}
	}
	return uniq(ScanSecrets(diff))
}

// isFinalCommit: this commit leaves no task changes behind.
func (c *hookCtx) isFinalCommit(cmd string) bool {
	dirty, err := DirtyFiles(c.root)
	if err != nil {
		return true
	}
	staged := map[string]bool{}
	for _, p := range StagedFiles(c.root) {
		staged[p] = true
	}
	all := strings.Contains(cmd, " -a") || strings.Contains(cmd, "--all") || strings.Contains(cmd, "git add -A") || strings.Contains(cmd, "git add .") || strings.Contains(cmd, "git add --all")
	for p := range dirty {
		if _, mine := c.task.Preexisting[p]; mine {
			continue
		}
		if c.setup != nil && MatchAny(c.setup.IgnoreChanges, p) {
			continue
		}
		if !staged[p] && !all && !strings.Contains(cmd, p) {
			return false
		}
	}
	return true
}

// ProofMissing lists what the commit rule still needs on the current code.
func ProofMissing(t *Task, s *Setup, final bool) []string {
	var miss []string
	fp, err := Fingerprint(t.Root, s.IgnoreChanges)
	if err != nil {
		return []string{"couldn't read the code state: " + err.Error()}
	}
	runs := LoadRuns(t.Root)
	if s.Commands.Test == "" {
		miss = append(miss, "no test command is saved (run /plumbline:setup)")
	} else {
		full := Judge(LatestFor(runs, KindTest, "", fp), BaselineFor(runs, t.ID, KindTest))
		if final && !full.OK {
			miss = append(miss, "full test suite: "+full.Reason+" (run: plumbline run test)")
		}
		if !final && !full.OK {
			rel := Judge(LatestFor(runs, KindRelated, "", fp), BaselineFor(runs, t.ID, KindTest))
			if !rel.OK {
				miss = append(miss, "related tests: "+rel.Reason+" (run: plumbline run related --files <changed files>)")
			}
		}
	}
	if s.Commands.Lint != "" {
		if v := Judge(LatestFor(runs, KindLint, "", fp), BaselineFor(runs, t.ID, KindLint)); !v.OK {
			miss = append(miss, "lint: "+v.Reason+" (run: plumbline run lint)")
		}
	}
	if s.Commands.Build != "" {
		if v := Judge(LatestFor(runs, KindBuild, "", fp), BaselineFor(runs, t.ID, KindBuild)); !v.OK {
			miss = append(miss, "build: "+v.Reason+" (run: plumbline run build)")
		}
	}
	return miss
}

var reCheckpoint = regexp.MustCompile(`(?i)checkpoint\s*([12])?`)

type question struct {
	Question string `json:"question"`
	Header   string `json:"header"`
	Options  []struct {
		Label string `json:"label"`
	} `json:"options"`
}

func (c *hookCtx) questions() ([]question, map[string]any) {
	var q struct {
		Questions []question     `json:"questions"`
		Answers   map[string]any `json:"answers"`
	}
	_ = jsonUnmarshal(c.in.ToolInput, &q)
	return q.Questions, q.Answers
}

// checkpointOf: header "Checkpoint" (the usual one: understanding and plan
// together) counts as checkpoint 2. "Checkpoint 1" is the optional early
// check of the understanding alone.
func checkpointOf(q question) int {
	for _, s := range []string{q.Header, q.Question} {
		t := strings.TrimSpace(s)
		if !strings.HasPrefix(strings.ToLower(t), "checkpoint") {
			continue
		}
		if m := reCheckpoint.FindStringSubmatch(t); m != nil {
			if m[1] == "1" {
				return 1
			}
			return 2
		}
	}
	return 0
}

func (c *hookCtx) gateQuestion() Output {
	qs, answers := c.questions()
	cp := 0
	var cq question
	for _, q := range qs {
		if n := checkpointOf(q); n > 0 {
			cp, cq = n, q
		}
	}
	if len(answers) > 0 && cp > 0 {
		return c.deny("self-approval", "checkpoint questions can't come pre-answered. Only the user answers them.")
	}
	if cp == 0 {
		return nil
	}
	hasApprove := false
	for _, o := range cq.Options {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(o.Label)), "approve") {
			hasApprove = true
		}
	}
	if !hasApprove || len(cq.Options) < 2 {
		return c.deny("checkpoint-format", "a checkpoint question needs an option whose label starts with \"Approve\" and at least one other option (for example \"Change something\").")
	}
	if c.task == nil {
		return c.deny("no-task", "start the task first: plumbline task start --title \"...\".")
	}
	problems := c.checkpointProblems(cp)
	if len(problems) > 0 {
		name := "the checkpoint"
		if cp == 1 {
			name = "checkpoint 1"
		}
		return c.deny(fmt.Sprintf("checkpoint%d", cp), fmt.Sprintf("%s isn't ready: %s.", name, strings.Join(problems, "; ")))
	}
	return nil
}

func (c *hookCtx) checkpointProblems(cp int) []string {
	t := c.task
	var p []string
	combined := cp == 2 && t.Phase == PhaseUnderstanding
	switch {
	case cp == 1 && t.Phase != PhaseUnderstanding:
		return []string{fmt.Sprintf("the task is at the %s step, past checkpoint 1", t.Phase)}
	case cp == 2 && t.Phase == PhaseBuilding:
		return []string{"the plan is already approved. If the plan has to change, run plumbline task replan, update plan.md, and ask the checkpoint again"}
	}
	if cp == 1 || combined {
		p = append(p, c.groundingProblems()...)
		text, _, err := ReadDoc(t, DocUnderstanding)
		if err != nil {
			p = append(p, "write the understanding to "+filepath.Join(TaskDocsDir(t.Root, t.ID), DocUnderstanding))
		} else {
			if len(Refs(text)) == 0 && !strings.Contains(strings.ToLower(text), "no existing code is involved") {
				p = append(p, "the understanding needs file:line references to the real code")
			}
			p = append(p, CheckRefs(t, text)...)
		}
	}
	if cp == 2 {
		text, fi, err := ReadDoc(t, DocPlan)
		if err != nil {
			p = append(p, "write the plan to "+filepath.Join(TaskDocsDir(t.Root, t.ID), DocPlan))
		} else {
			if !combined && fi.ModTime().Before(t.Checkpoint1At) {
				p = append(p, "plan.md is older than checkpoint 1; update it")
			}
			pc := CheckPlan(text)
			p = append(p, pc.Problems...)
			p = append(p, CheckRefs(t, text)...)
			p = append(p, c.challengeProblems(text)...)
		}
	}
	return p
}

// groundingProblems: search first. SocratiCode and MemPalace always, Serena
// when existing code changes (unless it's down or not needed, which is then
// named in the checkpoint and report).
func (c *hookCtx) groundingProblems() []string {
	t := c.task
	var p []string
	need := func(tool, why string) {
		if l, ok := t.Lookups[tool]; ok && l.Count > 0 {
			return
		}
		if r, ok := t.ToolDown[tool]; ok && r != "" {
			return
		}
		p = append(p, why)
	}
	need("socraticode", "search the code with SocratiCode first")
	need("mempalace", "check MemPalace for earlier work on this first")
	need("serena", "look up exact callers with Serena first (or, if Serena is down or the task adds only new code: plumbline task tool-down serena --reason \"...\")")
	return p
}
