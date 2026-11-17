package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
		if c.task == nil && c.taskErr == nil && in.AgentID != "" {
			// Inside a helper whose hook input doesn't carry the lead's
			// session: the task that recorded this helper's run, else the one
			// open task on this branch.
			if c.task = taskByAgent(c.root, in.AgentID); c.task == nil && in.SessionID != "" {
				c.task, c.taskErr = ActiveTask(c.root, "", "")
			}
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
		// A builder works in its own copy of the repo: that copy is the
		// working tree its edits, commands and checks are judged in.
		if c.task != nil && helperKind(in.AgentType, false) == HelperBuilder {
			if r := c.task.runByAgent(in.AgentID); r != nil && r.Worktree != "" && !r.Merged && !r.Dropped {
				if fi, err := os.Stat(r.Worktree); err == nil && fi.IsDir() {
					c.root = r.Worktree
				}
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

// othersCopy: abs is in a builder's copy of the repo that isn't this hook's
// own working tree. Only that builder changes it; the lead merges it.
func (c *hookCtx) othersCopy(abs string) bool {
	if c.root == "" || !insideWorktrees(c.root, abs) {
		return false
	}
	own := insideWorktrees(c.root, c.root) && IsInside(abs, c.root)
	return !own
}

// rel is a path relative to the working tree this hook runs in: the main
// checkout for the lead, its own copy for a builder.
func (c *hookCtx) rel(abs string) string {
	r, err := filepath.Rel(c.root, abs)
	if err != nil {
		return abs
	}
	return filepath.ToSlash(r)
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
	if noteBlock(c.in.SessionID, c.in.AgentID, rule, reason, blockTarget(c.in)) {
		c.log("repeat-block", rule, "")
		if c.in.AgentID != "" {
			reason += repeatNoteHelper
		} else {
			reason += repeatNote
		}
	}
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
	if (in.ToolName == "Grep" || in.ToolName == "Glob") && helperKind(in.AgentType, false) == "" {
		if in.ToolName == "Grep" {
			if p := grepSecret(in); p != "" {
				// Grep prints matching lines, so it gets Read's prompt.
				c := newCtx(in)
				defer c.close()
				return c.ask("secret-read", fmt.Sprintf("this search shows lines of %s in this chat, and the chat history is kept on your laptop. Allow only if Claude really needs to see the values (for example to debug production). Claude never repeats them.", p))
			}
		}
		return nil // only reviewers' searches are checked; the lead's stay fast
	}
	c := newCtx(in)
	defer c.close()
	if errors.Is(c.taskErr, ErrTampered) {
		switch {
		case in.ToolName == "Edit", in.ToolName == "Write", in.ToolName == "MultiEdit", in.ToolName == "NotebookEdit",
			in.ToolName == "Bash", in.ToolName == "Monitor", in.ToolName == "AskUserQuestion", strings.HasPrefix(in.ToolName, "mcp__") && MCPKind(in.ToolName) != "":
			return c.deny("tamper", "the task state was changed outside Plumbline, so its proof can't be trusted. Stop and tell the user; they can drop the task with /plumbline:cancel and start again.")
		}
	}
	if kind := helperKind(in.AgentType, false); kind != "" && in.AgentID != "" {
		c.touchRun(kind)
		if o := c.gateHelper(kind); o != nil {
			return o
		}
		if isWorker(kind) {
			return c.noAsk(kind, c.gateTool())
		}
	}
	return c.gateTool()
}

// noAsk: a worker runs without the user, so a question the gates would put
// to the user is refused instead, to go to the lead.
func (c *hookCtx) noAsk(kind string, o Output) Output {
	if d, reason := decisionOf(o); d == "ask" {
		return c.deny("helper-no-questions", fmt.Sprintf("the %s runs without the user, and this needs the user's OK: %s Put it in your notes for the lead.", kind, strings.TrimPrefix(reason, "Plumbline: ")))
	}
	return o
}

func decisionOf(o Output) (string, string) {
	h, _ := o["hookSpecificOutput"].(map[string]any)
	d, _ := h["permissionDecision"].(string)
	r, _ := h["permissionDecisionReason"].(string)
	return d, r
}

// gateTool is the gate for one tool call, after the helper rules.
func (c *hookCtx) gateTool() Output {
	in := c.in
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		if c.root == "" && p != "" {
			// The session started outside the repo (a parent folder): the
			// file's own repo decides.
			if r := RepoRoot(existingDir(Abs(p, in.Cwd))); r != "" {
				sub := *in
				sub.Cwd = r
				fc := newCtx(&sub)
				defer fc.close()
				return fc.gateEdit(Abs(p, in.Cwd))
			}
		}
		return c.gateEdit(p)
	case "Read":
		p := Abs(in.Str("file_path"), in.Cwd)
		if IsSecretFile(p) {
			return c.ask("secret-read", fmt.Sprintf("reading %s shows its values in this chat, and the chat history is kept on your laptop. Allow only if Claude really needs to see the values (for example to debug production). Claude never repeats them.", filepath.Base(p)))
		}
	case "Bash":
		return c.gateBash(in.Str("command"))
	case "Monitor":
		// Monitor runs its command in the same shell as Bash, so it gets the
		// same gate. Its WebSocket form runs no command.
		if cmd := in.Str("command"); cmd != "" {
			return c.gateBash(cmd)
		}
	case "AskUserQuestion":
		return c.gateQuestion()
	case "Agent", "Task":
		if in.AgentID != "" {
			return c.deny("no-nested-helpers", "helpers can't start other helpers. Report back to the lead instead.")
		}
		if kind := helperKind(in.Str("subagent_type"), true); kind != "" {
			return c.gateHelperStart(kind)
		}
	default:
		if strings.HasPrefix(in.ToolName, "mcp__") {
			return c.gateMCP()
		}
	}
	return nil
}

// grepSecret: the secret file a Grep names by its path or its glob, or "".
// A search of a whole folder isn't caught (the file has to be named).
func grepSecret(in *HookInput) string {
	if p := in.Str("path"); p != "" && IsSecretFile(Abs(p, in.Cwd)) {
		return filepath.Base(p)
	}
	g := in.Str("glob")
	for _, part := range strings.FieldsFunc(g, func(r rune) bool { return r == '{' || r == '}' || r == ',' || r == ' ' }) {
		base := strings.ToLower(filepath.Base(part))
		plain := strings.ReplaceAll(base, "*", "x")
		if isTemplateFile(strings.TrimRight(plain, "x")) {
			continue
		}
		if IsSecretFile(plain) || strings.Contains(base, ".env") {
			return part
		}
	}
	return ""
}

// MCP servers that only look things up or work outside the code (memory,
// code search, docs, the browser). Of the claude.ai connectors only Claude
// Docs is here: the others (Slack, Gmail, Atlassian, ...) send and change
// things in your name, so they are judged by tool name like any server.
var mcpLookupServers = []string{"mempalace", "socraticode", "context7", "claude-in-chrome", "easel", "claude_ai_claude_docs"}

var (
	reMCPShell = regexp.MustCompile(`(^|_)(shell|terminal|exec|execute|run|command|bash|script)(_|$)`)
	reMCPWrite = regexp.MustCompile(`(^|_)(write|edit|create|delete|remove|replace|insert|rename|move|patch|apply|save|modify|append|restore|reset|format|refactor|update|upload|put|set|add|commit|push|merge|checkout|mkdir|copy|clone|send|transition|assign|invite|publish|archive|reopen|approve|submit)(_|$)`)
	// Verbs that are also nouns (get_post, list_comment_threads): a write
	// unless the tool's name starts by reading.
	reMCPWriteSoft = regexp.MustCompile(`(^|_)(post|comment|reply|share|schedule|close)(_|$)`)
	reMCPReadFirst = regexp.MustCompile(`^(get|list|search|read|fetch|find|query|lookup|view|describe|show|count)(_|$)`)
	reCamel        = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// MCPKind: "shell" for a tool that runs commands, "write" for one that
// changes files or things, "" for lookups. Judged by the tool's name.
func MCPKind(tool string) string {
	if jira, read := JiraTool(tool); jira {
		if read {
			return ""
		}
		return "write"
	}
	server, name := mcpSplit(tool)
	low := strings.ToLower(server)
	for _, s := range mcpLookupServers {
		if strings.Contains(low, s) {
			return ""
		}
	}
	name = mcpWords(name)
	switch {
	case reMCPShell.MatchString(name):
		return "shell"
	case reMCPWrite.MatchString(name):
		return "write"
	case reMCPWriteSoft.MatchString(name) && !reMCPReadFirst.MatchString(name):
		return "write"
	}
	return ""
}

func mcpSplit(tool string) (server, name string) {
	rest := strings.TrimPrefix(tool, "mcp__")
	if i := strings.LastIndex(rest, "__"); i >= 0 {
		return rest[:i], rest[i+2:]
	}
	return rest, ""
}

// mcpWords: getJiraIssue, get-jira-issue -> get_jira_issue.
func mcpWords(name string) string {
	return strings.ReplaceAll(strings.ToLower(reCamel.ReplaceAllString(name, "${1}_${2}")), "-", "_")
}

// Jira (and the rest of Atlassian) is read-only for Claude: the user posts
// what Claude drafts, unless they ask for the post themselves.
var (
	reJira     = regexp.MustCompile(`(?i)atlassian|jira|confluence`)
	reJiraRead = regexp.MustCompile(`^(get|search|lookup|fetch|list|read|view|find|query)(_|$)|^atlassian_user_info$`)
)

// JiraTool: an MCP tool of a Jira or Atlassian server (or one named for
// Jira), and whether it only reads.
func JiraTool(tool string) (jira, read bool) {
	if !strings.HasPrefix(tool, "mcp__") || !reJira.MatchString(tool) {
		return false, false
	}
	_, name := mcpSplit(tool)
	n := mcpWords(name)
	for _, p := range []string{"jira_", "confluence_", "atlassian_"} {
		if m := strings.TrimPrefix(n, p); m != n && m != "user_info" {
			n = m
		}
	}
	return true, reJiraRead.MatchString(n)
}

// gateJiraWrite: in every state, paused included. A post goes through only
// when the user's latest message asked for it, and then only after their OK
// on the exact text.
func (c *hookCtx) gateJiraWrite() Output {
	text := strings.Join(mcpStrings(c.in.Input()), "\n")
	if m := AIMarkers(text); len(m) > 0 {
		return c.deny("jira-ai-markers", "this Jira text has AI markers ("+strings.Join(m, "; ")+"). Anything in the user's name carries none; remove them.")
	}
	if strings.Contains(text, "—") {
		return c.deny("jira-filler", "this Jira text has an em dash. Plain words for teammates: use a comma, a colon or a full stop.")
	}
	if !JiraAsked(c.in.SessionID) {
		where := "the task's notes folder"
		if c.task != nil {
			where = filepath.Join(TaskDocsDir(c.task.Root, c.task.ID), JiraCommentName(c.task.Ticket))
		}
		return c.deny("jira-draft-only", fmt.Sprintf("Jira is draft-only: posts, comments, status changes and edits are the user's, unless their own latest message asks you to post. Put the comment in %s (only the comment, ready to copy) and the suggested status in the report under \"For you\".", where))
	}
	return c.ask("jira-post", fmt.Sprintf("the user asked for this Jira change (%s). Check the exact text before allowing it.", c.in.ToolName))
}

// mcpStrings: every string value in a tool's input.
func mcpStrings(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case map[string]any:
		var out []string
		for _, e := range x {
			out = append(out, mcpStrings(e)...)
		}
		return out
	case []any:
		var out []string
		for _, e := range x {
			out = append(out, mcpStrings(e)...)
		}
		return out
	}
	return nil
}

var reMCPShellOnly = regexp.MustCompile(`(^|_)(shell|terminal|bash|command)(_|$)`)

// mcpCommand finds the shell command an MCP tool is given: a string, or a
// list of words.
func mcpCommand(input map[string]any) (string, bool) {
	for _, k := range []string{"command", "cmd", "script", "shell_command", "commandLine", "command_line", "args"} {
		switch v := input[k].(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v, true
			}
		case []any:
			var parts []string
			for _, x := range v {
				str, ok := x.(string)
				if !ok {
					return "", false
				}
				parts = append(parts, syntaxQuote(str, false))
			}
			if len(parts) > 0 {
				return strings.Join(parts, " "), true
			}
		}
	}
	return "", false
}

// mcpPath is the file an MCP tool names, if any.
func mcpPath(input map[string]any) string {
	for _, k := range []string{"relative_path", "file_path", "path", "filepath", "target_file", "filename", "file"} {
		if v, ok := input[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// gateMCP gives MCP tools the same rules as Bash and Edit: a tool that runs
// commands goes through the Bash gate; one that names a file goes through
// the edit gate; any other changing tool waits for the approved plan.
func (c *hookCtx) gateMCP() Output {
	if jira, read := JiraTool(c.in.ToolName); jira && !read {
		return c.gateJiraWrite()
	}
	kind := MCPKind(c.in.ToolName)
	if kind == "" {
		return nil
	}
	input := c.in.Input()
	if kind == "shell" {
		if cmd, ok := mcpCommand(input); ok {
			return c.gateBash(cmd)
		}
		if reMCPShellOnly.MatchString(strings.ToLower(reCamel.ReplaceAllString(c.in.ToolName, "${1}_${2}"))) {
			return c.deny("unclear-command", fmt.Sprintf("Plumbline can't find the command %s would run, so it can't be checked. Use the Bash tool.", c.in.ToolName))
		}
	}
	if p := mcpPath(input); p != "" {
		base := c.root
		if base == "" {
			base = c.in.Cwd
		}
		return c.gateEdit(Abs(p, base))
	}
	if c.paused {
		return nil
	}
	if c.task == nil {
		return c.ask("mcp-change", fmt.Sprintf("%s may change files or things outside the laptop, and no task is active. Allow it if that's fine now.", c.in.ToolName))
	}
	if c.task.Phase != PhaseBuilding {
		return c.deny("before-approval", fmt.Sprintf("%s may change things, and the plan isn't approved yet. Use read-only tools until the checkpoint.", c.in.ToolName))
	}
	return nil
}

// jiraTask: a company task on a Jira ticket, with the Jira part on.
func (c *hookCtx) jiraTask() bool {
	return c.task != nil && c.setup.JiraTicket(c.task.Ticket)
}

// existingDir is the deepest folder of p that exists.
func existingDir(p string) string {
	d := filepath.Dir(p)
	for {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return d
		}
		d = parent
	}
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
	abs = foldPath(abs)
	base := filepath.Base(abs)
	parent := filepath.Base(filepath.Dir(abs))
	if foldPath(parent) == foldPath(".claude") && (base == foldPath("settings.json") || base == foldPath("settings.local.json")) {
		return true
	}
	h, _ := os.UserHomeDir()
	if abs == foldPath(Abs(filepath.Join(h, ".claude.json"), "/")) {
		return true
	}
	ch := foldPath(Abs(claudeHome(), "/"))
	if base == foldPath("settings.json") && filepath.Dir(abs) == ch {
		return true
	}
	if IsInside(abs, filepath.Join(ch, foldPath("plugins"))) {
		return true
	}
	if strings.Contains(strings.ToLower(filepath.ToSlash(abs)), "managed-settings") {
		return true
	}
	return false
}

func (c *hookCtx) gateEdit(path string) Output {
	if path == "" {
		return nil
	}
	abs := realCase(Abs(path, c.in.Cwd))
	if isInsideFold(abs, Abs(DataDir(), "/")) {
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
	if c.root != "" && isInsideFold(abs, filepath.Join(c.root, ".git")) {
		return c.deny("git-internals", "git's own folder (.git, including its hooks) changes only through git commands. Tell the user if something there needs changing.")
	}
	if c.root != "" && abs == filepath.Join(c.root, RepoSetupFile) {
		return c.ask("setup-change", "this changes Plumbline's saved project setup, including the commands Claude may run before approval. Check the change before allowing it.")
	}
	if c.paused {
		return nil
	}
	if c.othersCopy(abs) {
		return c.deny("builder-copy", "that file is in a builder's copy of the repo. Builders change their own copies; bring the work back with plumbline builder merge <n>, or drop it with plumbline builder drop <n>.")
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
	if rule, reason := c.buildFile(c.rel(abs), 0); rule != "" {
		return c.deny(rule, reason)
	}
	return nil // in the plan, or part of the same change: allowed, recorded, and must be listed in the report
}

var (
	installRe = regexp.MustCompile(`^(npm (i|install|add)|yarn add|pnpm (add|i|install)|bun (add|install|i)|pip3? install|uv (add|pip install)|poetry add|pipenv install|go (get|install)|cargo (add|install)|composer require|gem install|brew install|apt(-get)? install|npx|pnpx|bunx)\b`)
	cloudCmds = map[string]bool{
		"aws": true, "sls": true, "serverless": true, "vercel": true, "kubectl": true, "helm": true,
		"terraform": true, "tofu": true, "pulumi": true, "gcloud": true, "az": true, "heroku": true,
		"fly": true, "flyctl": true, "doctl": true, "firebase": true, "netlify": true, "railway": true,
		"ssh": true, "scp": true, "cdk": true, "sam": true, "eb": true, "wrangler": true, "render": true,
		"gh": true, "hub": true, "glab": true,
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
		if seg != nil && !seg.unknownArgs() && len(seg.Env) == 0 && seg.Opaque == "" && redirectsHarmless(*seg) {
			for _, ss := range Segments(s) {
				if wordsEqual(ss.Words, seg.Words) {
					return true
				}
			}
		}
	}
	return false
}

func redirectsHarmless(s Segment) bool {
	for _, r := range s.Redirects {
		if !harmlessRedirect(r) {
			return false
		}
	}
	return true
}

// foldPath: macOS disks ignore case, so ~/.PLUMBLINE is ~/.plumbline.
func foldPath(p string) string {
	if runtime.GOOS == "darwin" {
		return strings.ToLower(p)
	}
	return p
}

func isInsideFold(path, dir string) bool { return IsInside(foldPath(path), foldPath(dir)) }

func mentionsDataDir(s Segment) bool {
	for _, w := range append(append([]string{}, s.Words...), s.Redirects...) {
		if wordMentionsDataDir(w) {
			return true
		}
	}
	return false
}

func wordMentionsDataDir(w string) bool {
	w = strings.ToLower(w)
	return strings.Contains(w, strings.ToLower(DataDir())) || strings.Contains(w, "/.plumbline/") || strings.HasSuffix(w, "/.plumbline") || strings.HasPrefix(w, "~/.plumbline") || strings.HasPrefix(w, "$home/.plumbline") || strings.HasPrefix(w, "${home}/.plumbline") || strings.HasPrefix(w, ".plumbline/")
}

func mentionsProtectedConfig(s Segment, cwd string) bool {
	if s.Dir != "" {
		cwd = s.Dir
	}
	for _, w := range append(append([]string{}, s.Words...), s.Redirects...) {
		low := strings.ToLower(w)
		if strings.Contains(low, ".claude/settings") || strings.HasSuffix(low, ".claude.json") || strings.Contains(low, ".claude/plugins") || strings.Contains(low, "managed-settings") {
			return true
		}
		if strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || strings.HasPrefix(w, ".") && s.Dir != "" {
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
	case "status", "docs-dir", "version", "--version", "-v", "stats", "help", "--help", "-h", "":
		return true
	case "task":
		return s.Arg(2) == "show" || s.Arg(2) == "list" || s.Arg(2) == ""
	case "setup":
		return s.Arg(2) == "show" || s.Arg(2) == ""
	case "builder":
		return s.Arg(2) == "list" || s.Arg(2) == ""
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

// targetAbs resolves a file a segment writes, or "" when its folder isn't
// known (a cd to somewhere unknown came first).
func targetAbs(s Segment, t string) string {
	if filepath.IsAbs(t) {
		return realCase(Abs(t, "/"))
	}
	if s.Dir == "" {
		return ""
	}
	return realCase(Abs(t, s.Dir))
}

// protectedRoots are folders no command may write files into wholesale.
func (c *hookCtx) protectedRoots() []string {
	h, _ := os.UserHomeDir()
	roots := []string{Abs(DataDir(), "/"), Abs(claudeHome(), "/"), Abs(filepath.Join(h, ".claude"), "/")}
	if c.root != "" {
		roots = append(roots, filepath.Join(c.root, ".git"))
	}
	return roots
}

// isGitConfig: git's global config files.
func isGitConfig(abs string) bool {
	h, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_CONFIG_HOME")
	if xdg == "" {
		xdg = filepath.Join(h, ".config")
	}
	f := foldPath(abs)
	return f == foldPath(filepath.Join(h, ".gitconfig")) || f == foldPath(filepath.Join(xdg, "git", "config"))
}

// protectedBase: names that are Claude Code's settings wherever they are, for
// writes whose folder isn't known.
func protectedBase(t string) bool {
	switch strings.ToLower(filepath.Base(t)) {
	case "settings.json", "settings.local.json", ".claude.json":
		return true
	}
	return false
}

// buildFile is the building-phase rule for one file outside the plan, shared
// by edits, terminal commands and MCP tools. pending counts other new
// outside-plan files in the same command. It returns the rule and the reason
// when the file is refused.
func (c *hookCtx) buildFile(rel string, pending int) (string, string) {
	if c.in.AgentID == "" && c.task.qaRunning() && !c.isTestFile(rel) {
		return "qa-running", fmt.Sprintf("blind QA is writing tests from the plan right now, and it must not see the new code, so code changes in this checkout (%s) wait until it finishes. Meanwhile, builders (plumbline:builder) can build in their own copies.", rel)
	}
	if c.task.InPlan(rel) {
		return "", ""
	}
	if _, ok := c.task.Preexisting[rel]; ok {
		c.hold(rel, "your own unsaved changes were in it before the task")
		return "user-work", fmt.Sprintf("%s had the user's own unsaved changes before this task started, so Claude doesn't touch it. Keep going with the rest, and list it under \"For you\" in the report.", rel)
	}
	if RiskyPath(rel, c.setup) {
		c.hold(rel, "risky file outside the plan")
		return "risky-outside-plan", fmt.Sprintf("%s isn't in the approved plan and is a risky kind of file (auth, payments, migrations, dependencies, config, deploy). Don't change it. Keep going with the rest, and list it under \"For you\" in the report with what you'd change and why.", rel)
	}
	if _, seen := c.task.OutsidePlan[rel]; !seen && c.task.OutsideCount()+pending >= OutsidePlanCap {
		return "outside-plan-cap", fmt.Sprintf("%s would be file %d outside the approved plan. That many means the plan was likely wrong, which is the user's decision. Stop and ask them (with your suggestion): go back to the plan (plumbline task replan, update ## Files, ask the checkpoint again), or something else. Finish anything that doesn't depend on it first.", rel, c.task.OutsideCount()+pending+1)
	}
	return "", ""
}

// Variables that switch commit hooks off.
func skipsHooks(e string) bool {
	name, val, _ := strings.Cut(e, "=")
	switch {
	case name == "HUSKY" && val != "1" && val != "true", name == "HUSKY_SKIP_HOOKS", name == "SKIP", name == "PRE_COMMIT_ALLOW_NO_CONFIG",
		strings.HasPrefix(name, "GIT_CONFIG"):
		return true
	}
	return false
}

func (c *hookCtx) gateBash(cmd string) Output {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}
	segs := ParseShell(cmd, c.in.Cwd)
	v := &verdicts{}
	allQuiet := true // every segment reads only, or is a saved command
	var commits []Segment
	addsAll := false
	var added []string
	building := c.task != nil && c.task.Phase == PhaseBuilding && !c.paused
	pending := map[string]bool{}
	hookEnv := false
	for i := range segs {
		for _, e := range segs[i].Env {
			if skipsHooks(e) {
				hookEnv = true
			}
		}
	}
	for i := range segs {
		s := segs[i]
		name := s.Name()
		if s.Opaque != "" {
			v.d("unclear-command", fmt.Sprintf("Plumbline can't tell what this runs: %s. Write the command out plainly (no eval, no command name in a variable, no shell reading commands from a pipe) so every part can be checked.", s.Opaque))
			allQuiet = false
			continue
		}
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
				quiet = true // a saved command, or proof whose command after -- is its own segment
			}
			if s.Arg(1) == "task" && (s.Arg(2) == "start" || s.Arg(2) == "tool-down" || s.Arg(2) == "size" || s.Arg(2) == "finish") {
				quiet = true
			}
		case name == "git":
			sub, args := GitSub(s)
			configs, _ := gitGlobals(s)
			for _, cf := range configs {
				if strings.HasPrefix(strings.ToLower(cf), "core.hookspath") {
					v.d("no-verify", "pointing core.hooksPath elsewhere skips the commit hooks, which is blocked for Claude.")
				}
			}
			switch sub {
			case "push":
				v.d("no-push", "pushing is always the user's job. Tell them the branch is ready to push.")
			case "commit":
				commits = append(commits, s)
			case "add":
				for _, a := range args {
					if a == "-A" || a == "--all" || a == "." || a == ":/" || strings.HasPrefix(a, "-") && strings.Contains(shortFlags(a), "A") {
						addsAll = true
					} else if !strings.HasPrefix(a, "-") {
						added = append(added, a)
					}
				}
			case "revert":
				if !contains(args, "-n") && !contains(args, "--no-commit") {
					v.d("commit-rule", "git revert makes a commit that skips the commit rule. Use git revert --no-commit, then commit with the usual message.")
				}
			case "commit-tree", "update-ref", "symbolic-ref", "replace", "notes":
				if !(sub == "symbolic-ref" && len(args) <= 1) && !(sub == "notes" && (len(args) == 0 || args[0] == "list" || args[0] == "show")) {
					v.d("commit-rule", "plumbing commands that write commits or move branches skip the commit rule. Use git commit.")
				}
			case "config":
				for k, a := range args {
					if !strings.EqualFold(a, "core.hooksPath") {
						continue
					}
					for _, rest := range args[k+1:] {
						if !strings.HasPrefix(rest, "-") {
							v.d("no-verify", "pointing core.hooksPath elsewhere skips the commit hooks, which is blocked for Claude.")
						}
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
			v.a("secret-read", fmt.Sprintf("this shows or sends the values in %s. Allow only if Claude really needs to see them. To just use them, load and use in one step (for example: set -a; . ./.env; set +a; psql ...).", filepath.Base(f)))
		}
		for _, t := range TreeTargets(s) {
			abs := targetAbs(s, t)
			if abs == "" {
				v.d("config-protect", "this writes files into a folder Plumbline can't work out. Give the folder as a full path.")
				continue
			}
			for _, p := range c.protectedRoots() {
				if isInsideFold(abs, p) || isInsideFold(p, abs) {
					v.d("config-protect", fmt.Sprintf("this writes files anywhere inside %s, which holds %s. Extract or copy into a folder that doesn't.", abs, p))
				}
			}
		}
		targets := WriteTargets(s)
		for _, t := range targets {
			if IsSecretFile(t) {
				v.d("secret-edit", "Claude doesn't write to .env or key files. Tell the user the variable name to add.")
			}
			if isPlumblineBin(name) {
				continue
			}
			abs := targetAbs(s, t)
			switch {
			case abs == "" && protectedBase(t):
				v.d("config-protect", fmt.Sprintf("%s is written in a folder Plumbline can't work out, and Claude Code's settings are protected. Write to it with a full path, or tell the user what to change.", t))
			case abs == "" && strings.Contains(strings.ToLower(t), "plumbline"):
				v.d("own-files", "Plumbline's own files can't be changed from the terminal, and this writes in a folder Plumbline can't work out.")
			case abs != "" && isInsideFold(abs, DataDir()):
				v.d("own-files", "Plumbline's own files can't be changed from the terminal. Use the plumbline command.")
			case abs != "" && isProtectedConfig(abs):
				v.d("config-protect", "Claude Code's settings and plugins are protected. Tell the user what to change.")
			case abs != "" && isGitConfig(abs):
				v.d("git-internals", "git's config files can change what every git command does. Use git commands, or tell the user what to change.")
			case abs != "" && c.root != "" && isInsideFold(abs, filepath.Join(c.root, ".git")):
				v.d("git-internals", "git's own folder (.git, including its hooks) changes only through git commands. Tell the user if something there needs changing.")
			case abs != "" && c.othersCopy(abs):
				v.d("builder-copy", "that writes in a builder's copy of the repo. Bring a builder's work back with plumbline builder merge <n>, or drop it with plumbline builder drop <n>.")
			case building && abs != "" && IsInside(abs, c.root):
				rel := c.rel(abs)
				if rule, reason := c.buildFile(rel, len(pending)); rule != "" {
					v.d(rule, reason)
				} else if _, seen := c.task.OutsidePlan[rel]; !seen && !c.task.InPlan(rel) {
					pending[rel] = true
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
					if w == "-X" || w == "--request" || strings.HasPrefix(w, "-d") || strings.HasPrefix(w, "--data") || strings.HasPrefix(w, "-F") || strings.HasPrefix(w, "--form") || strings.HasPrefix(w, "-T") || strings.HasPrefix(w, "--upload-file") || strings.HasPrefix(w, "--post-") || strings.HasPrefix(w, "--body-") || strings.HasPrefix(w, "--method") {
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
	for _, s := range commits {
		ci := ParseCommit(s)
		if ci.NoVerify || hookEnv {
			v.d("no-verify", "skipping the commit hooks (--no-verify, -n, HUSKY=0, SKIP=, GIT_CONFIG_*) is blocked for Claude.")
		}
	}
	if len(v.deny) == 0 {
		// every commit in the line, each in its own repo
		for _, s := range commits {
			switch d, rule, reason := c.gateCommit(s, addsAll, added); d {
			case "deny":
				v.d(rule, reason)
			case "ask":
				v.a(rule, reason)
			}
		}
	}
	hasCommit := len(commits) > 0
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

// gateCommit is the commit rule: no commit without proof on the current code.
// A commit in another repo (git -C, cd) is checked against that repo's task.
func (c *hookCtx) gateCommit(s Segment, addsAll bool, added []string) (decision, rule, reason string) {
	ctx := c
	if d := gitDir(s); d != "" {
		if r := RepoRoot(d); r != "" && r != c.root {
			in := *c.in
			in.Cwd = d
			sub := newCtx(&in)
			defer sub.close()
			ctx = sub
		}
	}
	if ctx.root == "" {
		return "", "", "" // not in a repo: git refuses it
	}
	ci := ParseCommit(s)
	if ci.MsgBad != "" {
		return "deny", "commit-message", ci.MsgBad + "."
	}
	if !ci.MsgFound {
		return "deny", "commit-message", "write the commit message with -m \"...\" so it can be checked."
	}
	m := ci.Message
	problems := CheckCommitMessage(m, ctx.setup)
	if secrets := ctx.scanForCommit(ci, addsAll); len(secrets) > 0 {
		return "deny", "secrets", "this commit looks like it contains secrets: " + strings.Join(secrets, "; ") + ". Move them to environment variables (the value never goes in the repo), then commit."
	}
	if len(problems) > 0 {
		return "deny", "commit-message", strings.Join(problems, "; ") + "."
	}
	subject := strings.SplitN(strings.TrimSpace(m), "\n", 2)[0]
	if ctx.paused {
		return "ask", "commit", fmt.Sprintf("commit \"%s\" (Plumbline is paused, so proof isn't checked).", subject)
	}
	if ctx.task == nil {
		return "deny", "commit-no-task", "commits happen inside a task, after the report and the user's OK. If this commit is outside any task, the user can pause Plumbline with /plumbline:pause."
	}
	if ctx.task.Phase != PhaseBuilding {
		return "deny", "commit-phase", fmt.Sprintf("task %s is at the %s step; nothing can be committed before the plan is approved and proven.", ctx.task.ID, ctx.task.Phase)
	}
	if !ctx.setupOK || ctx.setup == nil {
		return "deny", "setup", "the project setup isn't confirmed, so its test command can't be trusted. Run /plumbline:setup."
	}
	final := ctx.isFinalCommit(ci, addsAll, added)
	missing := ProofMissing(ctx.task, ctx.setup, final)
	if len(missing) > 0 {
		return "deny", "commit-proof", "no commit without proof on the current code: " + strings.Join(missing, "; ") + "."
	}
	kind := "slice commit, related tests passed"
	if final {
		kind = "last commit of the task, full suite passed"
	}
	return "ask", "commit", fmt.Sprintf("commit \"%s\" (%s).", subject, kind)
}

// scanForCommit scans what the commit will hold: the staged diff, plus the
// working-tree changes of -a, git add -A, or the pathspecs given.
func (c *hookCtx) scanForCommit(ci CommitInfo, addsAll bool) []string {
	diff := StagedDiff(c.root)
	var paths []string
	if !ci.All && !addsAll {
		if len(ci.Paths) == 0 {
			return uniq(ScanSecrets(diff))
		}
		paths = ci.Paths
	}
	diff += "\n" + WorkingDiffPaths(c.root, "HEAD", paths)
	if d, err := DirtyFiles(c.root); err == nil {
		for p := range d {
			if paths != nil && !pathspecMatch(paths, p) {
				continue
			}
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
	return uniq(ScanSecrets(diff))
}

// pathspecMatch: a repo-relative file is named by one of the pathspecs (the
// file, a folder holding it, or a pattern).
func pathspecMatch(specs []string, rel string) bool {
	for _, s := range specs {
		s = strings.TrimPrefix(filepath.ToSlash(s), "./")
		if s == "." || s == rel || strings.HasPrefix(rel, strings.TrimSuffix(s, "/")+"/") {
			return true
		}
		if ok, _ := filepath.Match(s, rel); ok {
			return true
		}
	}
	return false
}

// isFinalCommit: this commit leaves no task changes behind.
func (c *hookCtx) isFinalCommit(ci CommitInfo, addsAll bool, added []string) bool {
	dirty, err := DirtyFiles(c.root)
	if err != nil {
		return true
	}
	staged := map[string]bool{}
	for _, p := range StagedFiles(c.root) {
		staged[p] = true
	}
	all := ci.All || addsAll
	named := append(append([]string{}, ci.Paths...), added...)
	for p := range dirty {
		if _, mine := c.task.Preexisting[p]; mine {
			continue
		}
		if c.setup != nil && MatchAny(c.setup.IgnoreChanges, p) {
			continue
		}
		if !staged[p] && !all && !pathspecMatch(named, p) {
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
		full := Judge(LatestFor(runs, t.ID, KindTest, "", s.Commands.Test, fp), BaselineFor(runs, t.ID, KindTest))
		if final && !full.OK {
			miss = append(miss, "full test suite: "+full.Reason+" (run: plumbline run test)")
		}
		if !final && !full.OK {
			rel := Judge(LatestFor(runs, t.ID, KindRelated, "", s.Commands.TestRelated, fp), BaselineFor(runs, t.ID, KindTest))
			if !rel.OK {
				miss = append(miss, "related tests: "+rel.Reason+" (run: plumbline run related --files <changed files>)")
			}
		}
	}
	if s.Commands.Lint != "" {
		if v := Judge(LatestFor(runs, t.ID, KindLint, "", s.Commands.Lint, fp), BaselineFor(runs, t.ID, KindLint)); !v.OK {
			miss = append(miss, "lint: "+v.Reason+" (run: plumbline run lint)")
		}
	}
	if s.Commands.Build != "" {
		if v := Judge(LatestFor(runs, t.ID, KindBuild, "", s.Commands.Build, fp), BaselineFor(runs, t.ID, KindBuild)); !v.OK {
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
	cp, found := 0, 0
	var cq question
	for _, q := range qs {
		if n := checkpointOf(q); n > 0 {
			cp, cq = n, q
			found++
		}
	}
	if found > 1 {
		return c.deny("checkpoint-format", "ask one checkpoint question per prompt, on its own, so the answer the user gives is the one that's recorded.")
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
			p = append(p, c.teamProblems(text, pc.Files)...)
			p = append(p, c.sizeProblems(pc.Files)...)
		}
	}
	return p
}

// sizeProblems: "small" means low impact, not few lines. A plan that changes
// a high-impact kind of file (auth, permissions, payments, user data,
// secrets, migrations, queries, money math, dependencies, config, deploy)
// isn't small, so the challenger and the plan reviewer still run.
func (c *hookCtx) sizeProblems(files []string) []string {
	if c.task.Size != "small" {
		return nil
	}
	var vague, risky []string
	for _, f := range files {
		// A folder or a pattern would let a risky file in unseen (InPlan
		// matches both), so a small plan names each file.
		if strings.HasSuffix(f, "/") || strings.ContainsAny(f, "*?[") {
			vague = append(vague, f)
			continue
		}
		hit := RiskyPath(filepath.ToSlash(f), c.setup)
		for _, k := range ReviewersFor("small", c.task.Root, []string{f}) {
			hit = hit || k == HelperSecurity || k == HelperData
		}
		if hit {
			risky = append(risky, f)
		}
	}
	var p []string
	if len(vague) > 0 {
		p = append(p, fmt.Sprintf(`a small task's "## Files" names each file it changes, not folders or patterns (%s), so its impact can be checked`, strings.Join(vague, ", ")))
	}
	if len(risky) > 0 {
		p = append(p, fmt.Sprintf(`the task is sized small, but the plan changes high-impact files (%s), so it isn't small: run plumbline task size normal --why "..." (or big), then ask the checkpoint again`, strings.Join(risky, ", ")))
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
	if c.jiraTask() {
		need("jira", fmt.Sprintf("read the ticket %s and all its comments with the Jira tools first (comments often change the requirements; if Jira can't be reached: plumbline task tool-down jira --reason \"...\")", t.Ticket))
	}
	return p
}
