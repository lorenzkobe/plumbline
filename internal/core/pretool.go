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
	// searchSkipsCopies: the search being checked skips what git ignores,
	// so it doesn't reach the builders' copies (B7).
	searchSkipsCopies bool
	// trackerAsked: this command's prompt names the sprint file, so a change
	// to it is one the user was asked about (S4).
	trackerAsked bool
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

// unboundHint: the lead has no task, and this branch has one open task in
// this checkout that no session holds (its start didn't bind, TB1): how to
// pick it up. "" otherwise.
func (c *hookCtx) unboundHint() string {
	if c.root == "" || c.task != nil || c.in.AgentID != "" {
		return ""
	}
	branch, here := CurrentBranch(c.root), treeKey(c.root)
	var found *Task
	for _, t := range ListTasks(c.root) {
		if t.Open() && t.SessionID == "" && t.Branch == branch && treeKey(t.Root) == here {
			if found != nil {
				return ""
			}
			found = t
		}
	}
	if found == nil {
		return ""
	}
	return fmt.Sprintf(" Task %s (\"%s\") is open on this branch but no session holds it. If it's the one just started, tie it to this session by running plumbline task adopt %s on its own line (it asks the user once).", found.ID, found.Title, found.ID)
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

// handMerge: a command that brings changes in by hand rather than through
// plumbline builder merge: git apply (not --check or --stat), patch (not
// --dry-run), or a copy out of a builder's copy (BLD2).
func (c *hookCtx) handMerge(s Segment) bool {
	name := s.Name()
	if sub, args := GitSub(s); sub == "apply" {
		for _, a := range args {
			if a == "--check" || a == "--stat" || a == "--numstat" || a == "--summary" {
				return false
			}
		}
		return true
	}
	switch name {
	case "patch":
		for _, a := range s.Words[1:] {
			if a == "--dry-run" || a == "-C" || a == "--check" {
				return false
			}
		}
		return true
	case "cp", "rsync", "ditto", "install", "mv":
		for i := 1; i < len(s.Words); i++ {
			if s.dyn(i) || strings.HasPrefix(s.Words[i], "-") {
				continue
			}
			if abs := Abs(s.Words[i], s.Dir); abs != "" && c.othersCopy(abs) {
				return true
			}
		}
	}
	return false
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
	e := Event{Type: typ, Rule: rule, Detail: limit(scrubDetail(detail, c.root), 160), Project: c.projectKey()}
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
			in.ToolName == "Bash", in.ToolName == "Monitor", in.ToolName == "AskUserQuestion", strings.HasPrefix(in.ToolName, "mcp__") && c.mcpKind() != "":
			return c.deny("tamper", "the task state was changed outside Plumbline, so its proof can't be trusted. Stop and tell the user; they can drop the task with /plumbline:cancel and start again.")
		}
	}
	if kind := helperKind(in.AgentType, false); kind != "" && in.AgentID != "" {
		c.touchRun(kind)
		if in.ToolName == "SubagentHandback" {
			return c.gateHandback(kind)
		}
		if o := c.gateHelper(kind); o != nil {
			return o
		}
		o := c.noAsk(kind, c.gateTool())
		if d, _ := decisionOf(o); d != "deny" {
			c.noteBusy()
			c.noteTrackerBefore()
		}
		return o
	}
	o := c.gateTool()
	if d, _ := decisionOf(o); d != "deny" {
		c.noteLeadBusy()
		c.noteBusy()
		c.noteHeadBefore()
		c.noteTrackerBefore()
	}
	return o
}

// noAsk: a helper runs without the user, so a question the gates would put
// to the user is refused instead, to go to the lead.
func (c *hookCtx) noAsk(kind string, o Output) Output {
	if d, reason := decisionOf(o); d == "ask" {
		where := "your findings"
		if isWorker(kind) {
			where = "your notes"
		}
		return c.deny("helper-no-questions", fmt.Sprintf("the %s runs without the user, and this needs the user's OK: %s Put it in %s for the lead.", kind, strings.TrimPrefix(reason, "Plumbline: "), where))
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
	// A name that says it reads, anywhere in it (pull_request_read).
	reMCPRead = regexp.MustCompile(`(^|_)(get|list|search|read|fetch|find|query|lookup|view|describe|show|count|browse|inspect|retrieve|status|diff|info|explain)(_|$)`)
	reCamel   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// MCPKind: "shell" for a tool that runs commands, "write" for one that
// changes files or things, "" for lookups. Judged by the tool's name: one
// that says neither counts as a write (see MCPUnclear).
func MCPKind(tool string) string {
	k, _ := mcpNameKind(tool)
	return k
}

// MCPKindIn is MCPKind with the setup's list of read tools: a tool whose
// name says neither read nor write is a lookup when the setup lists it.
func MCPKindIn(tool string, s *Setup) string {
	k, unclear := mcpNameKind(tool)
	if unclear {
		if server, name := mcpSplit(tool); s.MCPReadTool(server, name) || knownReadTool(server, name) {
			return ""
		}
	}
	return k
}

// Read tools of servers setup installs, whose names don't say so. Serena's
// onboarding writes memories and restart_language_server restarts a
// process, so they aren't here.
var mcpKnownReads = map[string][]string{
	"serena": {"activate_project", "check_onboarding_performed", "think_about_collected_information", "think_about_task_adherence",
		"think_about_whether_you_are_done", "get_current_config", "initial_instructions", "prepare_for_new_conversation", "summarize_changes"},
}

func knownReadTool(server, name string) bool {
	low := strings.ToLower(server)
	for srv, tools := range mcpKnownReads {
		if strings.Contains(low, srv) && contains(tools, name) {
			return true
		}
	}
	return false
}

// MCPUnclear: the tool's name says neither read nor write.
func MCPUnclear(tool string) bool {
	_, unclear := mcpNameKind(tool)
	return unclear
}

func mcpNameKind(tool string) (kind string, unclear bool) {
	if jira, read := JiraTool(tool); jira {
		if read {
			return "", false
		}
		return "write", false
	}
	server, name := mcpSplit(tool)
	low := strings.ToLower(server)
	for _, s := range mcpLookupServers {
		if strings.Contains(low, s) {
			return "", false
		}
	}
	name = mcpWords(name)
	switch {
	case reMCPShell.MatchString(name):
		return "shell", false
	case reMCPWrite.MatchString(name):
		return "write", false
	case reMCPWriteSoft.MatchString(name) && !reMCPReadFirst.MatchString(name):
		return "write", false
	case reMCPRead.MatchString(name):
		return "", false
	}
	return "write", true
}

// serenaREPL: Serena 2's default single tool, which runs Python (SR1).
func serenaREPL(tool string) bool {
	server, name := mcpSplit(tool)
	return strings.Contains(strings.ToLower(server), "serena") && contains(strings.Split(mcpWords(name), "_"), "repl") // not replace_symbol_body
}

// mcpKind is MCPKindIn with this project's setup.
func (c *hookCtx) mcpKind() string { return MCPKindIn(c.in.ToolName, c.setup) }

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
	if serenaREPL(c.in.ToolName) && !c.paused {
		return c.deny("serena-repl", "Serena's REPL runs Python, which can change any file, and Plumbline can't check what it does. Use Serena's own tools: the user re-adds Serena with --agent-interface tools (claude mcp add ... serena start-mcp-server ... --agent-interface tools; /plumbline:setup does it). Meanwhile use Read, Grep and the other tools.")
	}
	if jira, read := JiraTool(c.in.ToolName); jira && !read {
		return c.gateJiraWrite()
	}
	if o := c.gateBrowser(); o != nil {
		return o
	}
	kind := c.mcpKind()
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
		return c.ask("mcp-change", fmt.Sprintf("%s may change files or things outside the laptop, and no task is active. Allow it if that's fine now.%s", c.in.ToolName, c.unboundHint()))
	}
	if c.task.Phase != PhaseBuilding {
		why := fmt.Sprintf("%s may change things, and the plan isn't approved yet. Use read-only tools until the checkpoint.", c.in.ToolName)
		if MCPUnclear(c.in.ToolName) {
			server, name := mcpSplit(c.in.ToolName)
			why = fmt.Sprintf("%s's name doesn't say whether it only reads, so it counts as a change, and the plan isn't approved yet. Use read-only tools until the checkpoint. If it only reads, the user can list it in the project setup (\"mcp_read_tools\": {\"%s\": [\"%s\"]}, saved with plumbline setup save).", c.in.ToolName, server, name)
		}
		return c.deny("before-approval", why)
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
	if strings.HasSuffix(base, ".jsonl") && IsInside(abs, filepath.Join(ch, foldPath("projects"))) {
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
		return c.deny("own-files", "Plumbline's own files (task state, proof, logs) can't be edited. Task notes go in the task's docs folder (plumbline docs-dir)."+c.unboundHint())
	}
	if IsSecretFile(abs) {
		return c.deny("secret-edit", "Claude doesn't edit .env or key files. Tell the user which variable to add or change (name only) and they make the change. Templates like .env.example are fine.")
	}
	if isProtectedConfig(abs) {
		return c.deny("config-protect", "Claude Code's settings, installed plugins and session transcripts are protected so Plumbline can't be switched off or misled by Claude. Tell the user what to change and why.")
	}
	if c.root != "" && isInsideFold(abs, filepath.Join(c.root, ".git")) {
		return c.deny("git-internals", "git's own folder (.git, including its hooks) changes only through git commands. Tell the user if something there needs changing.")
	}
	if c.root != "" && abs == filepath.Join(c.root, RepoSetupFile) {
		return c.ask("setup-change", "this changes Plumbline's saved project setup, including the commands Claude may run before approval. Check the change before allowing it.")
	}
	if tp := TrackerPath(c.root); tp != "" && abs == realCase(tp) {
		return c.ask("tracker", fmt.Sprintf("this updates your sprint file (%s). Check the change before allowing it.", filepath.Base(tp)))
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
		return c.deny("no-task", "no task is active, so code can't change yet. If the user wants a change, start the task (plumbline task start --title \"...\" on its own line, no question needed), look at the code, write the plan, then ask the plan question. To try something out, use a throwaway copy in the scratchpad instead."+c.unboundHint())
	}
	if c.task.Phase != PhaseBuilding {
		return c.deny("before-approval", fmt.Sprintf("task %s is at the %s step. Code changes wait until the user approves the plan at the checkpoint.", c.task.ID, c.task.Phase))
	}
	if rule, reason := c.buildFile(c.rel(abs), 0); rule != "" {
		return c.deny(rule, reason)
	}
	if o := c.gateComments(abs); o != nil {
		return o
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
		"gh": true, "hub": true, "glab": true, "oc": true, "gsutil": true, "bq": true, "eksctl": true, "argocd": true,
	}
	dbClients = map[string]bool{"psql": true, "mysql": true, "mongosh": true, "mongo": true, "redis-cli": true, "pg_dump": true, "pg_restore": true, "mysqldump": true}
)

// Commands that reach outside the laptop or run downloaded code, wherever
// they run from: a throwaway folder doesn't make them harmless.
var reachesOut = map[string]bool{
	"git": true, "curl": true, "wget": true, "http": true, "https": true, "nc": true, "ncat": true,
	"rsync": true, "sftp": true, "ftp": true, "telnet": true, "docker": true, "podman": true,
	"supabase": true, "sudo": true, "su": true, "open": true, "osascript": true,
}

// throwawayRun (SCR1): before approval, a command that runs in the
// scratchpad or /tmp and names nothing in the project only touches throwaway
// files, so it isn't refused as a change. It isn't allowed outright either:
// Claude Code's own permission mode still judges it. Commands that reach
// outside the laptop, installs and downloads (npx), paths that leave the
// throwaway folders and words whose value isn't known don't count; their own
// asks still apply. If one changes project files anyway, the post hook
// stops the work.
func (c *hookCtx) throwawayRun(s Segment, installs bool) bool {
	name := s.Name()
	if c.task == nil || c.root == "" || s.Dir == "" || installs || name == "" || isPlumblineBin(name) ||
		reachesOut[name] || cloudCmds[name] || dbClients[name] || s.unknownArgs() || !c.throwawayPath(s.Dir) {
		return false
	}
	for _, w := range s.Words[1:] {
		if strings.HasPrefix(w, "-") {
			_, v, ok := strings.Cut(w, "=")
			if !ok {
				continue
			}
			w = v // --out=<path>
		}
		abs := Abs(w, s.Dir)
		if abs != "" && c.inProject(abs) {
			return false
		}
		// A path that leaves the folder it runs in (absolute, home, ..)
		// must stay in a throwaway folder too.
		if (strings.HasPrefix(w, "/") || strings.HasPrefix(w, "~") || w == ".." || strings.HasPrefix(w, "../") || strings.Contains(w, "/../")) && !c.throwawayPath(abs) {
			return false
		}
	}
	for _, r := range s.Redirects {
		if abs := Abs(r, s.Dir); abs == "" || !c.throwawayPath(abs) && r != "/dev/null" {
			return false
		}
	}
	return true
}

// throwawayPath: a path in the scratchpad or a temp folder, outside the
// project.
func (c *hookCtx) throwawayPath(abs string) bool {
	if abs == "" || c.inProject(abs) {
		return false
	}
	for _, d := range []string{os.TempDir(), "/tmp", c.in.ScratchpadDir} {
		// Inside, not a temp folder itself: that holds other sessions'
		// scratchpads too. The scratchpad itself is this session's own.
		if root := Abs(d, "/"); d != "" && (abs != root || d == c.in.ScratchpadDir) && IsInside(abs, root) {
			return true
		}
	}
	return false
}

// inProject: the path is in this checkout (case folded, like the edit rules).
func (c *hookCtx) inProject(abs string) bool {
	return c.root != "" && (IsInside(abs, c.root) || isInsideFold(abs, c.root))
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

// runsProjectTool: npx, bunx or pnpx running the project's own copy of a
// tool (node_modules/.bin here or in a folder above, inside the repo), or told
// never to install, so nothing is downloaded. -p/--package always installs.
func (c *hookCtx) runsProjectTool(s Segment) bool {
	switch s.Name() {
	case "npx", "bunx", "pnpx":
	default:
		return false
	}
	t, ok := runnerTarget(s)
	if !ok || len(t.Words) == 0 || s.Dir == "" || c.root == "" {
		return false
	}
	at := len(s.Words) - len(t.Words) // where the tool's name is
	for _, w := range s.Words[1:at] {
		switch {
		case w == "-p", w == "--package", strings.HasPrefix(w, "--package="):
			return false
		case w == "--no-install", w == "--no":
			return true
		}
	}
	tool := t.Words[0]
	if s.dyn(at) || tool == "" || strings.ContainsAny(tool, `/\@`) {
		return false
	}
	for d := s.Dir; IsInside(d, c.root); d = filepath.Dir(d) {
		if fi, err := os.Stat(filepath.Join(d, "node_modules", ".bin", tool)); err == nil && !fi.IsDir() {
			return true
		}
		if d == c.root || d == filepath.Dir(d) {
			break
		}
	}
	return false
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

// commitMessageless: the words of a segment without a git commit's message
// (-m, --message), which only describe the change: a message naming
// .claude/settings.local.json isn't the file (SB1, step 16).
func commitMessageless(s Segment) []string {
	if sub, _ := GitSub(s); sub != "commit" && sub != "tag" {
		return s.Words
	}
	var out []string
	for i := 0; i < len(s.Words); i++ {
		w := s.Words[i]
		switch {
		case w == "-m" || w == "--message":
			i++ // its value
			continue
		case strings.HasPrefix(w, "--message="), strings.HasPrefix(w, "-m") && len(w) > 2 && !strings.HasPrefix(w, "--"):
			continue
		}
		out = append(out, w)
	}
	return out
}

// gitUntracksOnly (SB1): git rm --cached with nothing but paths, which
// stops git tracking files and leaves them on disk unchanged.
func gitUntracksOnly(s Segment) bool {
	sub, args := GitSub(s)
	if sub != "rm" || s.dynArgs() {
		return false
	}
	cached, paths := false, 0
	for _, a := range args {
		switch {
		case a == "--cached":
			cached = true
		case a == "-q" || a == "--quiet" || a == "--":
		case strings.HasPrefix(a, "-"):
			return false // -r, -f, --pathspec-from-file and the rest
		default:
			paths++
		}
	}
	configs, _ := gitGlobals(s)
	return cached && paths > 0 && len(configs) == 0
}

func mentionsProtectedConfig(s Segment, cwd string) bool {
	if s.Dir != "" {
		cwd = s.Dir
	}
	for _, w := range append(commitMessageless(s), s.Redirects...) {
		low := strings.ToLower(w)
		if strings.Contains(low, ".claude/settings") || strings.HasSuffix(low, ".claude.json") || strings.Contains(low, ".claude/plugins") || strings.Contains(low, "managed-settings") ||
			strings.Contains(low, ".claude/projects/") && strings.HasSuffix(low, ".jsonl") {
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
	for _, w := range s.Words[1:] {
		if (w == "--help" || w == "-h" || w == "-help") && s.Arg(1) != "hook" {
			return true // a subcommand given --help prints help and runs nothing (RP1); hook does run
		}
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
	case "standup":
		return !standupSetsTracker(s)
	}
	return false
}

// finishLeavesWork: plumbline task finish --uncommitted (CM1).
func finishLeavesWork(s Segment) bool {
	for _, w := range s.Words[3:] {
		if w == "--uncommitted" || strings.HasPrefix(w, "--uncommitted=") {
			return true
		}
	}
	return false
}

// standupSetsTracker: plumbline standup tracker --set <path>.
func standupSetsTracker(s Segment) bool {
	if s.Arg(1) != "standup" {
		return false
	}
	for _, w := range s.Words[2:] {
		if w == "--set" || strings.HasPrefix(w, "--set=") {
			return true
		}
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

// commitNotReady: what the finish check still needs before the commit
// question, so the user's "Commit it" is the last step (CF1). A hotfix's
// first commit comes before its full suite and reviews, so it isn't held.
func (c *hookCtx) commitNotReady() []string {
	t := c.task
	if t == nil || t.Phase != PhaseBuilding || c.paused || !c.setupOK || c.setup == nil {
		return nil
	}
	if t.Hotfix() && t.Commits == 0 {
		return nil
	}
	return FinishMissing(t, c.setup)
}

// approvedLook: a command that only looks at a system outside the laptop
// (a cloud CLI read, a supabase read, a read-only query against a database
// that isn't local). It runs before approval like a local read, with no
// prompt. Helpers keep their own read-only list.
func approvedLook(s Segment) bool {
	if len(WriteTargets(s)) > 0 {
		return false // its output goes to a file
	}
	name := s.Name()
	switch {
	case cloudCmds[name]:
		return cloudRead(s)
	case name == "supabase":
		return supabaseRead(s)
	case dbClients[name]:
		return dbReadOnly(s)
	}
	return false
}

// leadNotesOnly: the lead writing its own task notes from the terminal
// (cat >> plan.md <<EOF, tee, echo, printf). The Write tool may write the
// same files, so the terminal may too. Helpers' findings files and anything
// else in Plumbline's folder stay refused, and helpers write with Write.
func (c *hookCtx) leadNotesOnly(s Segment) bool {
	if c.task == nil || c.in.AgentID != "" || s.Opaque != "" {
		return false
	}
	switch s.Name() {
	case "cat", "tee", "echo", "printf":
	default:
		return false
	}
	targets := WriteTargets(s)
	if len(targets) == 0 {
		return false
	}
	docs := Abs(TaskDocsDir(c.root, c.task.ID), "/")
	for _, t := range targets {
		abs := targetAbs(s, t)
		if abs == "" || !IsInside(abs, docs) || filepath.Dir(abs) != docs || !strings.HasSuffix(abs, ".md") || reFindings.MatchString(filepath.Base(abs)) {
			return false
		}
	}
	return true
}

// noTaskShell: a terminal command that writes a project file while no task
// is open, refused like the Edit tool (step 10b: sed -i ran outside a task
// and the post hook could only stop the work after the file changed).
const noTaskShell = "no task is active, so files in the project can't change yet, from the terminal either. If the user wants a change, start the task (plumbline task start --title \"...\" on its own line, no question needed), look at the code, write the plan, then ask the plan question. For logs or experiments, write in the scratchpad or /tmp."

// writeIgnored: a project file whose changes don't count (git ignores it,
// or the setup's ignore_changes lists it), so writing it needs no task.
func (c *hookCtx) writeIgnored(rel string) bool {
	if c.setup != nil && MatchAny(c.setup.IgnoreChanges, rel) {
		return true
	}
	_, err := gitOut(c.root, "check-ignore", "-q", "--", rel)
	return err == nil
}

// ownFilesFromShell: a terminal write into Plumbline's data folder. Task
// notes have their own way in (UX2: the refusal names it).
const ownFilesFromShell = "Plumbline's own files can't be read or changed from the terminal: plumbline status shows the task, and a failed plumbline run names its failing tests in its last line. Write the task's notes (understanding.md, plan.md, report.md, findings) with the Write or Edit tool (the lead may also use cat, tee, echo or printf into its own notes); anything else goes through the plumbline command."

// buildFile is the building-phase rule for one file outside the plan, shared
// by edits, terminal commands and MCP tools. pending counts other new
// outside-plan files in the same command. It returns the rule and the reason
// when the file is refused.
func (c *hookCtx) buildFile(rel string, pending int) (string, string) {
	if need := c.baselineMissing(); len(need) > 0 {
		if c.in.AgentID != "" {
			return "baseline-first", fmt.Sprintf("the lead hasn't recorded how the saved checks stand before any change (%s), so %s waits. Stop and tell the lead in your notes.", strings.Join(need, ", "), rel)
		}
		return "baseline-first", fmt.Sprintf("before the first change, record how the saved checks stand on the approved code: run plumbline run checks now (it runs and records every saved check; still missing: %s). A problem they show was there before the task, so it won't block the finish; without them it would. Then make the change.", strings.Join(need, ", "))
	}
	if c.in.AgentID == "" && !c.task.qaRunning() && c.task.qaFirst(c.setup) && !c.isTestFile(rel) && !docFile(rel) {
		return "qa-first", fmt.Sprintf("this is a big task: blind QA writes the tests from the plan first, before any code here changes (%s), or its tests wouldn't be blind. Start it now, or again if an earlier run stopped without writing its tests (Agent tool, subagent_type plumbline:qa), even if the user asked you to hurry; tell them in one line that the tests come first. Builders (plumbline:builder) can build in their own copies meanwhile. Only the user can skip blind QA (plumbline waive qa asks them).", rel)
	}
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
	if c.task.otherSessionFiles()[rel] {
		c.hold(rel, "another Claude session changed it while this task was open")
		return "other-session", fmt.Sprintf("%s was changed by another Claude session in this checkout while this task was open, so it isn't this task's and Claude doesn't touch it. Keep going with the rest, and list it under \"For you\" in the report.", rel)
	}
	if RiskyPath(rel, c.setup) {
		c.hold(rel, "risky file outside the plan")
		return "risky-outside-plan", fmt.Sprintf("%s isn't in the approved plan and is a risky kind of file (auth, payments, migrations, dependencies, config, deploy). Don't change it. Keep going with the rest, and list it under \"For you\" in the report with what you'd change and why.", rel)
	}
	if gateOutsidePlan(rel) {
		c.hold(rel, "a file that decides which checks run, outside the plan")
		return "gate-outside-plan", fmt.Sprintf("%s isn't in the approved plan and decides which checks run (CI, git hooks, test or lint config, AI instructions, plugin parts): one line there can switch a check off. Don't change it. Keep going with the rest, and list it under \"For you\" in the report with what you'd change and why; to change it, the plan must list it (plumbline task replan).", rel)
	}
	if _, seen := c.task.OutsidePlan[rel]; !seen && c.task.OutsideCount()+pending >= OutsidePlanCap {
		return "outside-plan-cap", fmt.Sprintf("%s would be file %d outside the approved plan. That many means the plan was likely wrong, which is the user's decision. Stop and ask them (with your suggestion): go back to the plan (plumbline task replan, update ## Files, ask the checkpoint again), or something else. Finish anything that doesn't depend on it first.", rel, c.task.OutsideCount()+pending+1)
	}
	return "", ""
}

// baselineMissing (SEC1): until the task's first change, the saved checks
// that have no run on the untouched code yet. Those runs tell a problem
// that was already there from a new one; without one, a check that already
// failed blocks the finish. A waived check needs none.
func (c *hookCtx) baselineMissing() []string {
	t := c.task
	// A task approved before 0.5.0 has no approval fingerprint, so no run
	// after approval can be one.
	if t == nil || !t.LastCodeChange.IsZero() || c.setup == nil || !c.setupOK || t.Phase == PhaseBuilding && t.ApprovalPrint == "" {
		return nil
	}
	var runs []*Run
	var need []string
	for _, k := range append([]string{KindTest}, CheckKinds...) {
		if savedCommand(c.setup, k) == "" || t.waived(k) {
			continue
		}
		if runs == nil {
			runs = LoadRuns(t.Root)
		}
		if len(baselineRuns(runs, t.ID, k)) == 0 {
			need = append(need, "plumbline run "+k)
		}
	}
	if len(need) > 0 && t.Phase == PhaseBuilding {
		// The code moved since approval without Claude changing it (the
		// user edited a file): no run can be a starting point now.
		if fp, err := Fingerprint(t.Root, c.ignore()); err != nil || fp != t.ApprovalPrint && fp != t.BaselinePrint {
			return nil
		}
	}
	return need
}

// waived: the task has a waiver for this check.
func (t *Task) waived(kind string) bool {
	for _, w := range t.Waivers {
		if w.Check == kind {
			return true
		}
	}
	return false
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
	allQuiet := true   // every segment reads only, or is a saved command
	throwaway := false // a segment runs only on throwaway files (SCR1)
	online := false    // a look that reaches the internet: Claude Code decides (READ2)
	hint := ""         // how to make the line read only, when there's a simple way
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
		if name == "cd" && c.in.AgentID == "" && c.root != "" {
			if to := s.Arg(1); to != "" && !s.dyn(1) {
				if abs := Abs(to, s.Dir); abs != "" && c.othersCopy(abs) {
					v.d("builder-copy", "don't cd into a builder's copy: the shell stays there for every later command, and merging, checks and proof must run in the main checkout. Look at its work from here: git -C "+abs+" diff (or plumbline builder list).")
				}
			}
		}
		if c.in.AgentID == "" && c.task != nil {
			if ns := c.task.unmergedCopies(); len(ns) > 0 && c.handMerge(s) {
				v.d("builder-hand-merge", "bring a builder's work in with plumbline builder merge "+ns[0]+" (copies waiting: "+strings.Join(ns, ", ")+"), not by hand: the merge checks the builders didn't change the same lines, and removes the copy. Its copy already holds your uncommitted work (QA's tests too), so nothing needs copying. Bring parts over by hand only after the merge refuses them.")
			}
		}
		quiet := ReadOnly(s)
		if quiet && LooksOnline(s) {
			online = true
		}
		if !approvedLook(s) {
			// keep quiet as ReadOnly says
		} else if c.in.AgentID == "" {
			quiet = true // looking at a system outside the laptop changes nothing there
		}
		notes := c.leadNotesOnly(s)
		if notes {
			quiet = true // the lead's own notes, as the Write tool may write them
		}
		c.testTargetVerdict(segs, i, v)
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
		case isPlumblineBin(name) && s.Arg(1) != "hook" && plumblineReads(s):
			quiet = true // help, status and listings only show things
		case isPlumblineBin(name):
			switch s.Arg(1) {
			case "hook":
				v.d("own-files", "Plumbline's hooks are run by Claude Code only.")
			case "pause", "resume", "cancel", "wrong":
				v.d("user-only", fmt.Sprintf("only the user can %s, by typing /plumbline:%s.", s.Arg(1), s.Arg(1)))
			case "setup":
				switch s.Arg(2) {
				case "show", "":
				case "sandbox":
					v.a("setup-change", "this turns on Claude Code's Bash sandbox in this checkout ("+SandboxLocalFile+"): Plumbline's folder is fenced off from commands, Plumbline's own commands, Docker and any --exclude names run outside it, and Claude can't retry a command outside it. Builds that need the network, caches outside the project or a login may then fail; undo with plumbline setup undo-sandbox.")
				case "undo-sandbox":
					v.a("setup-change", "this takes out the sandbox settings Plumbline added in this checkout ("+SandboxLocalFile+").")
				case "laptop":
					v.a("setup-change", "this changes Claude Code's own user settings (built-in memory, AI attribution, deny rules for .env and key files, as its flags say) and may add a rule to your global CLAUDE.md. The old values are kept; plumbline setup undo-laptop puts them back.")
				case "undo-laptop":
					v.a("setup-change", "this puts back the Claude Code user settings and global CLAUDE.md that plumbline setup laptop changed.")
				default:
					v.a("setup-change", "this saves Plumbline setup. Check the values before allowing.")
				}
			case "waive":
				// WV1: a waive the command would refuse is stopped before the
				// user is asked, so their yes isn't spent on an error.
				if check, reason, ok := waiveArgs(s); !ok {
					v.d("waiver", `a waiver needs the check and why it can't run, in one command: plumbline waive bug-test --reason "..." (or proof:ui, proof:api, proof:schema, e2e, qa). Ask the user first in plain words; the confirm prompt is theirs.`)
				} else {
					v.a("waiver", fmt.Sprintf("waive %s: %s. A waiver skips one check for this task. Only allow it if that check truly can't run.", check, reason))
				}
			case "standup":
				if standupSetsTracker(s) {
					v.a("tracker-set", "this remembers your sprint file for this project. Claude may then suggest edits to it, and each edit asks you first.")
				}
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
			if s.Arg(1) == "task" && s.Arg(2) == "finish" && finishLeavesWork(s) {
				v.a("finish-uncommitted", "this closes the task with its changes uncommitted, and Claude can't commit them after that. Allow it only if you said you'll commit them yourself.")
			}
			if s.Arg(1) == "task" && s.Arg(2) == "start" {
				if o := c.otherSessionTask(); o != nil && !SessionEnded(o.SessionID) {
					v.d("task-other-session", liveOtherNote(fmt.Sprintf("\"%s\"", o.Title)))
				} else if o != nil {
					v.d("task-other-session", fmt.Sprintf("task %s (\"%s\", %s step) is open in this checkout from an earlier session that has ended, and two tasks in one checkout would test each other's half-done code. Ask the user which way: continue that task in this session (plumbline task adopt %s), close it (%s, theirs to type), or start this task in its own worktree (a new session with claude -w).", o.ID, o.Title, o.Phase, o.ID, c.cancelHint(o)))
				}
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
		} else if PrintsEnvSecrets(s) {
			v.a("secret-read", "this prints environment variables, where keys, tokens and passwords live, so it may show a secret. Allow only if Claude really needs to see them. To check one is set without showing it: test -n \"$NAME\" && echo set.")
		} else if composeShowsEnv(s) {
			v.a("secret-read", "this prints the compose setup with the .env values filled in, so it may show a secret. Allow only if Claude really needs to see them; --services or --no-interpolate don't show values.")
		} else if ReadsListedFiles(s) {
			v.a("secret-read", fmt.Sprintf("%s reads files that a listing adds (xargs, find -exec or fd -x), and that listing could include .env or key files, which would show their values here. Narrow the listing to the file types needed (rg -l x -g '*.go' | xargs cat, find . -name '*.ts' -exec cat {} +, fd -e go -x cat), or open the files with the Read tool.", s.Name()))
		}
		for _, t := range TreeTargets(s) {
			abs := targetAbs(s, t)
			if c.trackerUnder(abs) {
				c.askTracker(v)
			}
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
			if removesTree(s) && c.trackerUnder(abs) {
				c.askTracker(v) // rm -r of the folder that holds it
			}
			switch {
			case abs == "" && protectedBase(t):
				v.d("config-protect", fmt.Sprintf("%s is written in a folder Plumbline can't work out, and Claude Code's settings are protected. Write to it with a full path, or tell the user what to change.", t))
			case abs == "" && strings.Contains(strings.ToLower(t), "plumbline"):
				v.d("own-files", "Plumbline's own files can't be changed from the terminal, and this writes in a folder Plumbline can't work out.")
			case abs != "" && isInsideFold(abs, DataDir()):
				if !notes {
					v.d("own-files", ownFilesFromShell)
				}
			case abs != "" && c.root != "" && foldPath(abs) == foldPath(filepath.Join(ProjectRoot(c.root), RepoSetupFile)):
				v.a("setup-change", "this changes Plumbline's saved project setup from the terminal. Until the user confirms it again with /plumbline:setup, its saved commands don't run and nothing in it switches a rule off.")
			case abs != "" && c.root != "" && TrackerPath(c.root) != "" && foldPath(abs) == foldPath(TrackerPath(c.root)):
				c.askTracker(v)
			case abs != "" && isProtectedConfig(abs):
				v.d("config-protect", "Claude Code's settings, plugins and session transcripts are protected. Tell the user what to change.")
			case abs != "" && isGitConfig(abs):
				v.d("git-internals", "git's config files can change what every git command does. Use git commands, or tell the user what to change.")
			case abs != "" && c.root != "" && isInsideFold(abs, filepath.Join(c.root, ".git")):
				v.d("git-internals", "git's own folder (.git, including its hooks) changes only through git commands. Tell the user if something there needs changing.")
			case abs != "" && c.othersCopy(abs):
				v.d("builder-copy", "that writes in a builder's copy of the repo. Bring a builder's work back with plumbline builder merge <n>, or drop it with plumbline builder drop <n>.")
			case abs != "" && c.task == nil && !c.paused && c.root != "" && IsInside(abs, c.root) && !c.writeIgnored(c.rel(abs)):
				v.d("no-task", noTaskShell+c.unboundHint())
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
			v.d("own-files", ownFilesFromShell)
		}
		if !quiet && mentionsProtectedConfig(s, c.in.Cwd) {
			if gitUntracksOnly(s) {
				// SB1: untracking leaves the file and its settings as they are.
				v.a("untrack-settings", "this stops git from tracking a Claude Code settings file. The file and its settings stay as they are on this laptop; the next commit removes it from the repo.")
			} else {
				v.d("config-protect", "Claude Code's settings, plugins and session transcripts are protected. Tell the user what to change.")
			}
		}
		joined := strings.Join(s.Words, " ")
		approved := c.task != nil && c.task.Phase == PhaseBuilding && c.task.Approved(s.Words)
		c.devopsVerdict(s, approved, v)
		if installRe.MatchString(joined) && !(len(s.Words) == 2 && (s.Words[1] == "install" || s.Words[1] == "i")) && !installOnlyAsks(s) && !approved && !c.runsProjectTool(s) {
			if c.task != nil {
				v.d("new-package", "this installs a package that isn't in the approved plan's \"Needs your OK\" list. Don't install it. Keep going with what you can, and put it under \"For you\" in the report: why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.")
			} else {
				v.a("new-package", "this installs a package. Say why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.")
			}
		}
		if approved {
			// in the plan you approved: runs as listed
		} else if cloudCmds[name] && !cloudRead(s) {
			v.a("outside-laptop", cloudAskReason(s, name))
		}
		if name == "supabase" && !supabaseRead(s) {
			v.a("outside-laptop", "supabase commands can reach and change the hosted project.")
		}
		if dbClients[name] && !dbLocal(s) && !dbReadOnly(s) {
			v.a("outside-laptop", fmt.Sprintf("%s without a local host may connect to a shared or production database.", name))
		}
		if name == "docker" && (dockerPushes(s) || s.Arg(1) == "login") {
			v.a("outside-laptop", "this reaches a registry outside your laptop.")
		}
		if (name == "curl" || name == "wget" || name == "http" || name == "https") && httpSends(s) {
			v.a("outside-laptop", "this sends data to a server outside your laptop.")
		}
		if !quiet && !c.isSaved(cmd, &s) && c.throwawayRun(s, installRe.MatchString(joined)) {
			throwaway = true // not refused before approval, but not allowed outright either
		} else if !quiet && !c.isSaved(cmd, &s) {
			allQuiet = false
			if needsDoubleDash(s) {
				hint = doubleDashHint
			}
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
			switch d, rule, reason := c.gateCommit(s, addsAll, added, len(commits) == 1); d {
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
	if !c.paused && c.task != nil && c.task.Phase != PhaseBuilding && !allQuiet && !hasCommit {
		// The user is often away while Claude researches, so refuse
		// instead of waiting on a prompt.
		return c.deny("before-approval", "the plan isn't approved yet, so only reading and the project's saved commands can run. Reproduce with the saved commands or in a throwaway copy (scratchpad or /tmp), or put this command in the plan's \"Needs your OK\" list."+hint)
	}
	// Outside a task an ordinary command is left to Claude Code's own
	// permission mode: the rules above still ask for their fixed list, and
	// the post hook stops the work when a command changes the repo's files.
	if len(v.ask) > 0 {
		return c.ask(v.askRule, strings.Join(uniq(v.ask), " "))
	}
	if allQuiet && !throwaway && !online && !hasCommit && !c.paused && (c.task == nil || c.task.Phase != PhaseBuilding) {
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
func (c *hookCtx) gateCommit(s Segment, addsAll bool, added []string, single bool) (decision, rule, reason string) {
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
		return "deny", "commit-no-task", "commits happen inside a task, after the report and the user's OK. If this commit is outside any task, the user can pause Plumbline with /plumbline:pause." + ctx.unboundHint()
	}
	if ctx.task.Phase != PhaseBuilding {
		return "deny", "commit-phase", fmt.Sprintf("task %s is at the %s step; nothing can be committed before the plan is approved and proven.", ctx.task.ID, ctx.task.Phase)
	}
	if !ctx.setupOK || ctx.setup == nil {
		return "deny", "setup", "the project setup isn't confirmed, so its test command can't be trusted. Run /plumbline:setup."
	}
	final := ctx.isFinalCommit(ci, addsAll, added)
	// A hotfix's first commit goes out on the related tests and the saved
	// checks (lint, build, and format, typecheck, security when saved);
	// the full suite comes right after it (W1).
	hotfix := ctx.task.Hotfix() && ctx.task.Commits == 0
	missing := ProofMissing(ctx.task, ctx.setup, final && !hotfix)
	if len(missing) > 0 {
		return "deny", "commit-proof", "no commit without proof on the current code: " + strings.Join(missing, "; ") + "."
	}
	kind := "slice commit, related tests passed"
	switch {
	case hotfix:
		kind = "hotfix commit, related tests and the saved checks passed; the full suite and every reviewer run right after it"
	case final:
		kind = "last commit of the task, full suite passed"
	}
	if single && ctx.task.CommitOK != "" && ctx.task.CommitOK == commitPrint(ctx.root, ctx.ignore()) {
		// The user just answered "Commit it" on this code: one stop, not two.
		return "", "", ""
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
			if _, err := gitOut(c.root, "ls-files", "--error-unmatch", p); err != nil {
				if b, ok := readableNewFile(c.root, p); ok {
					diff += "\n" + addedFileDiff(p, b)
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
	others := c.task.OthersWork()
	for p := range dirty {
		if others[p] {
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
		if !full.OK && brokenBaseline(runs, t.ID, KindTest) {
			full.Reason += "; the run before the first edit failed every test (a broken environment, like a database that was down), so it isn't the starting point and every failure counts: tell the user which failures were there before"
		}
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
	// Every saved check: lint and build, and format, typecheck and security
	// when the setup has them (WF3).
	for _, k := range CheckKinds {
		cmd := savedCommand(s, k)
		if cmd == "" {
			continue
		}
		if v := Judge(LatestFor(runs, t.ID, k, "", cmd, fp), BaselineFor(runs, t.ID, k)); !v.OK {
			why := v.Reason
			if k == KindFormat && formatRewrote(runs, t.ID, cmd, fp) {
				why += "; the saved format command rewrote files, and it must only check (prettier --check, ruff format --check): ask the user to change it with /plumbline:setup"
			}
			miss = append(miss, k+": "+why+" (run: plumbline run "+k+")")
		}
	}
	return miss
}

// formatRewrote: the task's newest run of the saved format command left the
// code as it is now, changed from where it started: the command rewrote
// files instead of checking them.
func formatRewrote(runs []*Run, taskID, cmd, fp string) bool {
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.TaskID == taskID && r.Kind == KindFormat && r.Command == cmd && !r.Finished.IsZero() {
			return r.PrintAfter == fp && r.PrintAfter != r.PrintBefore
		}
	}
	return false
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
	cp, found, dirs := 0, 0, 0
	var cq, dq question
	for _, q := range qs {
		if n := checkpointOf(q); n > 0 {
			cp, cq = n, q
			found++
		} else if c.designTask() && isPick(q) {
			dq = q
			dirs++
		}
	}
	if found > 1 || (dirs > 0 && len(qs) > 1) {
		return c.deny("checkpoint-format", "ask one checkpoint or direction question per prompt, on its own, so the answer the user gives is the one that's recorded.")
	}
	if len(answers) > 0 && cp > 0 {
		return c.deny("self-approval", "checkpoint questions can't come pre-answered. Only the user answers them.")
	}
	if cp == 0 {
		if dirs > 0 {
			return c.gateDirection(dq, answers)
		}
		for _, q := range qs {
			if !isCommitQ(q) {
				continue
			}
			if len(answers) > 0 {
				return c.deny("self-approval", "the commit question can't come pre-answered. Only the user answers it.")
			}
			if miss := c.commitNotReady(); len(miss) > 0 {
				return c.deny("commit-early", "not ready to ask about the commit yet, so the user's yes would come before the work is done. The finish check still needs: "+strings.Join(miss, "; ")+". Do these first, then ask.")
			}
		}
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
		return c.deny("no-task", "start the task first: plumbline task start --title \"...\"."+c.unboundHint())
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
	case cp == 1 && t.Hotfix():
		return []string{`a hotfix has one checkpoint: write understanding.md and plan.md, then ask the combined one (header "Checkpoint")`}
	case cp == 1 && t.Phase != PhaseUnderstanding:
		return []string{fmt.Sprintf("the task is at the %s step, past checkpoint 1", t.Phase)}
	case cp == 2 && t.Phase == PhaseBuilding:
		return []string{"the plan is already approved. If the plan has to change, run plumbline task replan, update plan.md, and ask the checkpoint again"}
	}
	if cp == 1 || combined {
		p = append(p, c.groundingProblems()...)
		text, _, err := ReadDoc(t, DocUnderstanding)
		switch {
		case err != nil && combined && t.Size == "small" && !t.Hotfix():
			// A small task's plan says what's happening; no separate
			// understanding.md (UX1). Its file:line refs are checked below.
		case err != nil:
			p = append(p, "write the understanding to "+filepath.Join(TaskDocsDir(t.Root, t.ID), DocUnderstanding))
		default:
			if len(Refs(text)) == 0 && !strings.Contains(strings.ToLower(text), "no existing code is involved") {
				p = append(p, "the understanding needs file:line references to the real code")
			}
			p = append(p, CheckRefs(t, text)...)
			if len(Section(text, "reuse")) == 0 {
				p = append(p, `understanding.md needs a "## Reuse" section: where you searched for code that already does this job (the queries and places) and what you found, with file:line, or "nothing"`)
			}
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
			p = append(p, c.planRuleProblems(text)...)
			p = append(p, CheckRefs(t, text)...)
			p = append(p, c.challengeProblems(text)...)
		}
	}
	return p
}

// planRuleProblems: what the plan question would refuse in plan.md itself,
// apart from the file:line refs and the challenge. The checkpoint uses it,
// and so does the note after each save of plan.md (CASE1), so Claude fixes
// a slip before it asks.
func (c *hookCtx) planRuleProblems(text string) []string {
	t := c.task
	pc := CheckPlanSized(text, t.Size == "small")
	p := append([]string(nil), pc.Problems...)
	if t.Size == "small" && !docExists(t, DocUnderstanding) && len(Refs(text)) == 0 && !strings.Contains(strings.ToLower(text), "no existing code is involved") {
		p = append(p, "the plan needs file:line references to the real code it changes (a small task has no understanding.md to hold them)")
	}
	p = append(p, c.teamProblems(text, pc.Files)...)
	p = append(p, c.sizeProblems(pc.Files)...)
	p = append(p, c.upgradeProblems(pc)...)
	p = append(p, c.designProblems(text, pc)...)
	p = append(p, hotfixProblems(t, text)...)
	return p
}

// sizeProblems: "small" means low impact, not few lines. A plan that changes
// a high-impact kind of file (auth, permissions, payments, user data,
// secrets, migrations, queries, money math, dependencies, config, deploy)
// isn't small, so the challenger and the plan reviewer still run; the user's
// /plumbline:size small keeps it small anyway. A file that decides which
// checks run (GateFile) is never small, whoever sized it.
func (c *hookCtx) sizeProblems(files []string) []string {
	if c.task.Size != "small" {
		return nil
	}
	var vague, risky, gates []string
	for _, f := range files {
		// A folder or a pattern would let a risky file in unseen (InPlan
		// matches both), so a small plan names each file.
		if strings.HasSuffix(f, "/") || strings.ContainsAny(f, "*?[") {
			vague = append(vague, f)
			continue
		}
		if GateFile(f) {
			gates = append(gates, f)
			continue
		}
		if c.task.SizeByUser {
			continue
		}
		// The floor: the built-in high-impact kinds (auth, permissions,
		// payments, migrations, deploy, dependencies) and security code
		// are never small. The setup's own risky_paths are Claude's call,
		// with the reason in --why (UX1).
		hit := RiskyPath(filepath.ToSlash(f), nil)
		for _, k := range ReviewersFor("small", c.task.Root, []string{f}) {
			hit = hit || k == HelperSecurity
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
		p = append(p, fmt.Sprintf(`the task is sized small, but the plan changes high-impact files (%s), so it isn't small: run plumbline task size normal --why "..." (or big), then ask the checkpoint again. Don't suggest to the user that they keep it small: that override is theirs to think of (SV1)`, strings.Join(risky, ", ")))
	}
	if len(gates) > 0 {
		p = append(p, fmt.Sprintf(`the plan changes files that decide which checks run (%s), so it isn't small: one line there can switch a check off, so they always get the full review: run plumbline task size normal --why "..." (or big), then ask the checkpoint again`, strings.Join(gates, ", ")))
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
	need("serena", "look up exact callers with Serena first (or, if Serena is down, the task adds only new code, or no code symbol changes (a config value, text, a version number): plumbline task tool-down serena --reason \"...\")")
	if c.jiraTask() {
		need("jira", fmt.Sprintf("read the ticket %s and all its comments with the Jira tools first (comments often change the requirements; if Jira can't be reached: plumbline task tool-down jira --reason \"...\")", t.Ticket))
	}
	return p
}

// installOnlyAsks (READ2): an install tool asked only for help, its version
// or what's installed (npx --version, pip install --help, cargo install
// --list). Its subcommand comes first; every word after it is one of those
// flags, so nothing is named to install (npm install add installs "add").
func installOnlyAsks(s Segment) bool {
	w := s.Words[1:]
	k := 0
	for k < len(w) && k < 2 && (w[k] == "install" || w[k] == "i" || w[k] == "add" || w[k] == "pip") {
		k++
	}
	if k >= len(w) {
		return false
	}
	for i, a := range w[k:] {
		if s.dyn(k + 1 + i) {
			return false
		}
		switch a {
		case "--help", "-h", "--version", "-v", "-V", "--list":
		default:
			return false
		}
	}
	return true
}

// composeShowsEnv (READ2): docker compose config prints the setup with the
// .env values filled in, unless it lists names only or skips them.
func composeShowsEnv(s Segment) bool {
	if s.Name() != "docker" && s.Name() != "docker-compose" {
		return false
	}
	words := s.Words[1:]
	ci := -1
	for i, w := range words {
		if w == "config" && (s.Name() == "docker-compose" || contains(words[:i], "compose")) {
			ci = i
			break
		}
	}
	if ci < 0 {
		return false
	}
	for _, w := range words[ci+1:] {
		switch w {
		case "--services", "--volumes", "--images", "--profiles", "--networks", "--no-interpolate", "--hash", "-q", "--quiet":
			return false
		}
	}
	return true
}

// waiveArgs: the check and reason of a plumbline waive line, and whether the
// command would take them (a known check and a reason).
func waiveArgs(s Segment) (check, reason string, ok bool) {
	for i := 2; i < len(s.Words); i++ {
		w := s.Words[i]
		switch {
		case w == "--":
			i = len(s.Words)
		case strings.HasPrefix(w, "--reason="):
			reason = strings.TrimPrefix(w, "--reason=")
		case w == "--reason" && i+1 < len(s.Words) && !strings.HasPrefix(s.Words[i+1], "--"):
			reason = s.Words[i+1]
			i++
		case strings.HasPrefix(w, "--"):
			if !strings.Contains(w, "=") && i+1 < len(s.Words) && !strings.HasPrefix(s.Words[i+1], "--") {
				i++
			}
		case check == "":
			check = w
		}
	}
	switch check {
	case "proof:ui", "proof:api", "proof:schema", "e2e", "qa", "bug-test":
	default:
		return check, reason, false
	}
	reason = strings.Join(strings.Fields(reason), " ")
	if reason == "" {
		return check, reason, false
	}
	if r := []rune(reason); len(r) > 200 {
		reason = string(r[:200]) + "..."
	}
	return check, reason, true
}

// cancelHint: what the user types to close task o. Plain /plumbline:cancel
// closes the one left-over task (ID1), so the id shows only when it must.
func (c *hookCtx) cancelHint(o *Task) string {
	if t, _ := cancelTarget(c.root); t != nil && t.ID == o.ID {
		return "/plumbline:cancel"
	}
	return "/plumbline:cancel " + o.ID
}
