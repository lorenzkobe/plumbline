package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The helpers: the challenger attacks the understanding and the plan before
// the checkpoint; the reviewers check the change after building, each on its
// own (plan match, code quality, and the specialists that risk signals call
// for). Tracers trace code and the tester runs the checks; these are all
// read-only. Builders build parts of the plan, each in its own copy of the
// repo, and blind QA writes tests from the plan's cases before the code
// exists (workers.go). Plumbline records when each run starts and stops, with
// the code fingerprint at both ends, so a run only counts when it finished on
// code that didn't change under it.
const (
	HelperChallenger   = "challenger"
	HelperPlanReviewer = "plan-reviewer"
	HelperCodeReviewer = "code-reviewer"
	HelperSecurity     = "security-reviewer"
	HelperData         = "data-reviewer"
	HelperUX           = "ux-reviewer"
	HelperTracer       = "tracer"
	HelperBuilder      = "builder"
	HelperQA           = "qa"
	HelperTester       = "tester"
	// 0.3.x had one reviewer; its runs count as the code reviewer's.
	legacyReviewer = "reviewer"

	MaxChallengeRounds = 2
	MaxReviews         = 3 // per reviewer: the first review plus 2 fix rounds
	MaxQARuns          = 2

	// A run with no stop recorded after this long is treated as dead.
	helperAlive = 30 * time.Minute
)

type HelperRun struct {
	Kind        string    `json:"kind"`
	AgentID     string    `json:"agent_id"`
	Started     time.Time `json:"started"`
	Finished    time.Time `json:"finished,omitempty"`
	PrintBefore string    `json:"print_before"`
	PrintAfter  string    `json:"print_after,omitempty"`
	Findings    string    `json:"findings"`           // file name in the task's docs folder
	SawDocs     bool      `json:"saw_docs,omitempty"` // understanding.md and plan.md existed at the start

	// Builders: the copy of the repo the run works in, the commit it was made
	// from (the lead's code then), and what became of it.
	Worktree   string `json:"worktree,omitempty"`
	BaseCommit string `json:"base_commit,omitempty"`
	Merged     bool   `json:"merged,omitempty"`
	Dropped    bool   `json:"dropped,omitempty"`
	Error      string `json:"error,omitempty"`
	Setup      string `json:"setup,omitempty"` // what went wrong running builder_setup in the copy
	// Blind QA: the test files it wrote.
	Wrote []string `json:"wrote,omitempty"`
	// Blind QA: each test file's hash when it stopped; a copy of each is
	// kept in qa-N-tests/ in the notes folder (N22).
	WroteHash map[string]string `json:"wrote_hash,omitempty"`
	// Builders and QA: the last tool call, so a run that works long still
	// counts as running and one that died stops counting.
	LastSeen time.Time `json:"last_seen,omitempty"`

	// Reviewers: the files this run opened (absolute), so a withdrawn
	// finding is known to have looked at the guard the lead cited (N5).
	Opened []string `json:"opened,omitempty"`
	// What the findings file said when the run stopped: what's still wrong
	// with it (such a run doesn't count), what the reviewer set aside as
	// outside the plan (N6), and the changed files the code reviewer didn't
	// cover (N4).
	Problems   []string `json:"problems,omitempty"`
	SetAside   []string `json:"set_aside,omitempty"`
	NotCovered []string `json:"not_covered,omitempty"`
	// Builders: how the run ended, from its notes' "## Status" (N10).
	Status string `json:"status,omitempty"`
	// The findings file (its hash, or "missing") when a handback was last
	// refused, so a second try with nothing changed goes through (SA1).
	HandbackDenied string `json:"handback_denied,omitempty"`
}

// Counts: a run that finished on unchanged code with a findings file that
// passed its checks.
func (t *Task) counts(r HelperRun) bool {
	return r.Complete() && t.findingsExist(r) && len(r.Problems) == 0
}

// Complete: the run finished and the code didn't change while it ran.
func (r HelperRun) Complete() bool {
	return !r.Finished.IsZero() && r.PrintAfter != "" && r.PrintAfter == r.PrintBefore
}

// ReviewerKinds in the order they are listed.
var ReviewerKinds = []string{HelperPlanReviewer, HelperCodeReviewer, HelperSecurity, HelperData, HelperUX}

func isReviewer(kind string) bool { return contains(ReviewerKinds, kind) }

// WorkerKinds: the helpers that do part of the work (2b).
var WorkerKinds = []string{HelperTracer, HelperBuilder, HelperQA, HelperTester}

func isWorker(kind string) bool { return contains(WorkerKinds, kind) }

// independent: helpers whose work must not see the others' notes. Reviewers
// stay independent of each other; blind QA must not see the new code.
func independent(kind string) bool { return isReviewer(kind) || kind == HelperQA }

// helperKind maps an agent type to a Plumbline helper. Hook input carries the
// plugin-scoped name; the lead may pass the bare name to the Agent tool.
func helperKind(agentType string, allowBare bool) string {
	k, scoped := strings.CutPrefix(agentType, "plumbline:")
	if !scoped && !allowBare {
		return ""
	}
	if k == HelperChallenger || isReviewer(k) || isWorker(k) {
		return k
	}
	return ""
}

// Findings files: challenge-N.md, code-review-N.md, ... (review-N.md from
// 0.3.x), and the workers' trace-N.md, build-N.md, qa-N.md, test-N.md.
var reFindings = regexp.MustCompile(`^(challenge|((plan|code|security|data|ux)-)?review|trace|build|qa|test)-[0-9]+\.md$`)

func findingsName(kind string, n int) string {
	switch kind {
	case HelperChallenger:
		return fmt.Sprintf("challenge-%d.md", n)
	case HelperTracer:
		return fmt.Sprintf("trace-%d.md", n)
	case HelperBuilder:
		return fmt.Sprintf("build-%d.md", n)
	case HelperQA:
		return fmt.Sprintf("qa-%d.md", n)
	case HelperTester:
		return fmt.Sprintf("test-%d.md", n)
	}
	return fmt.Sprintf("%s-%d.md", strings.TrimSuffix(kind, "er"), n)
}

// Risk signals in a file's path call for a specialist reviewer. They are
// judged by path only; the lead can always name more in the plan's Team.
var (
	// A word counts between path, dot, dash or underscore boundaries; file
	// names are split at camelCase first (AuthService -> Auth_Service).
	reSecuritySignal = regexp.MustCompile(`(?i)(^|[/._-])(auth|authn|authz|oauth|sso|saml|jwt|login|logout|signup|sessions?|permissions?|acl|rbac|roles?|policy|policies|guards?|payments?|billing|stripe|invoices?|checkout|secrets?|token|crypto|passwords?|credentials?|users?|accounts?|profiles?|pii|env)([/._-]|$)`)
	reDataSignal     = regexp.MustCompile(`(?i)((^|[/._-])(migrations?|migrate|schemas?|models?|repositor(y|ies)|quer(y|ies)|sql|db|database|seeds?|prisma|drizzle|knex|sequelize|typeorm|money|currency|prices?|pricing|amounts?|ledger|payments?|billing|invoices?)([/._-]|$))`)
	reUXSignal       = regexp.MustCompile(`(?i)(\.(tsx|jsx|vue|svelte|astro|css|scss|sass|less|html?|hbs|erb|ejs|njk|liquid)$|\.blade\.php$|(^|/)(components?|pages|views|screens|layouts?|styles?|templates?|ui)/)`)
)

// Risk signals in what a change adds, in any file (R1). SQL keywords count
// in capitals only, so "select a file from the list" in a comment doesn't.
var (
	reSecurityContent = regexp.MustCompile(`(?i)\b(bcrypt|argon2|scrypt|pbkdf2|jwt|jsonwebtoken|oauth2?|openid|saml|csrf|xsrf|passwords?|passwd|set-cookie|httponly|samesite|authori[sz]ation|authenticat(e|ed|ion)|authori[sz]e[ds]?|permissions?|is_?admin|api[_-]?keys?|private[_-]?keys?|access[_-]?tokens?|refresh[_-]?tokens?|hmac|stripe|paypal|braintree|adyen|dangerouslySetInnerHTML|innerHTML|child_process|subprocess|os\.system|shell\s*=\s*True|eval\s*\()`)
	reDataContent     = regexp.MustCompile(`\b(SELECT\s+(DISTINCT\s+)?[\w*.,\s()"]+?\s+FROM|INSERT\s+INTO|UPDATE\s+["\w.]+\s+SET|DELETE\s+FROM|CREATE\s+(UNIQUE\s+)?(TABLE|INDEX)|ALTER\s+TABLE|DROP\s+(TABLE|INDEX|COLUMN)|TRUNCATE\s+TABLE)\b|(?i)\b(findMany|findUnique|findFirst|updateMany|deleteMany|upsert|bulkCreate|bulk_create|bulk_update|select_related|prefetch_related|objects\.(filter|update|create|delete|get)|\$queryRaw|\$executeRaw|queryRaw|executeRaw|knex\(|createQueryBuilder|beginTransaction|transaction\s*\()`)
	reMoneyContent    = regexp.MustCompile(`(?i)\b(prices?|pricing|currenc(y|ies)|cents|subtotal|refunds?|payouts?|invoices?|ledger|stripe|paypal|braintree|adyen|BigDecimal|toFixed\(2\))\b`)
)

// ContentReviewers: the specialists what a change adds calls for, read from
// its added lines in files other than tests, prose and lockfiles.
func ContentReviewers(changes []fileDiff, s *Setup) []string {
	var out []string
	add := func(k string) {
		if !contains(out, k) {
			out = append(out, k)
		}
	}
	for _, f := range changes {
		if TestFile(f.Path, s) || reProse.MatchString(f.Path) || reDepFile.MatchString(filepath.Base(f.Path)) {
			continue
		}
		for _, l := range f.Lines {
			if !l.Add {
				continue
			}
			if reSecurityContent.MatchString(l.Text) {
				add(HelperSecurity)
			}
			if reDataContent.MatchString(l.Text) || reMoneyContent.MatchString(l.Text) {
				add(HelperData)
			}
		}
	}
	return out
}

var signalWhy = map[string]string{
	HelperSecurity: "auth, permissions, payments, user data or secrets",
	HelperData:     "migrations, queries, models or money math",
	HelperUX:       "the frontend",
}

// ReviewersFor: the reviewers a change needs, by size and by the files it
// touches (relative to root, so a home folder like /Users/... isn't read as
// user data). Small tasks get one reviewer (the code reviewer), unless a
// file decides which checks run (GateFile): that gets the full review.
func ReviewersFor(size, root string, files []string) []string {
	need := map[string]bool{HelperCodeReviewer: true}
	if size != "small" {
		need[HelperPlanReviewer] = true
	}
	for _, f := range files {
		if filepath.IsAbs(f) && root != "" {
			if r, err := filepath.Rel(root, f); err == nil && !climbsOut(r) {
				f = r
			}
		}
		need[HelperPlanReviewer] = need[HelperPlanReviewer] || GateFile(f)
		f = reCamel.ReplaceAllString(filepath.ToSlash(f), "${1}_${2}")
		need[HelperSecurity] = need[HelperSecurity] || reSecuritySignal.MatchString(f)
		if size != "small" { // a small change gets one review, plus security where it reaches security code (UX1)
			need[HelperData] = need[HelperData] || reDataSignal.MatchString(f)
			need[HelperUX] = need[HelperUX] || reUXSignal.MatchString(f)
		}
	}
	var out []string
	for _, k := range ReviewerKinds {
		if need[k] {
			out = append(out, k)
		}
	}
	return out
}

// TeamReviewers: the reviewers a plan's "## Team" section names.
func TeamReviewers(plan string) []string {
	team := strings.ToLower(strings.Join(Section(plan, "team"), "\n"))
	var out []string
	for _, k := range ReviewerKinds {
		if strings.Contains(team, k) {
			out = append(out, k)
		}
	}
	return out
}

// RequiredReviewers: every reviewer whose review the finish check needs.
// The approved plan's Team adds to the ones size and risk call for, and
// files changed outside the plan count as well.
func RequiredReviewers(t *Task, s *Setup) []string {
	if !s.PartOn("reviewer") {
		return nil
	}
	files := append([]string{}, t.PlanFiles...)
	for p := range t.OutsidePlan {
		files = append(files, p)
	}
	// Every file the task really changed counts too, so a folder or a
	// pattern in the plan can't hide an auth or payments file from the
	// specialists.
	others := t.OthersWork()
	for _, p := range ChangedSince(t.Root, t.StartCommit) {
		if !others[p] {
			files = append(files, p)
		}
	}
	need := ReviewersFor(t.Size, t.Root, files)
	if t.Size == "small" && len(t.OutsidePlan) > 0 {
		// The change grew past its small plan: the files outside it get the
		// specialists a normal task would (UX1 keeps one review only for
		// what was planned).
		var outside []string
		for p := range t.OutsidePlan {
			outside = append(outside, p)
		}
		for _, k := range ReviewersFor("normal", t.Root, outside) {
			if k != HelperPlanReviewer {
				need = append(need, k)
			}
		}
	}
	// What the change adds counts too (R1): SQL, auth or money code in a
	// plainly named file still gets its specialist.
	if changes, err := taskChanges(t, s.IgnoreChanges); err == nil {
		for _, k := range ContentReviewers(changes, s) {
			if t.Size != "small" || k == HelperSecurity || k == HelperCodeReviewer {
				need = append(need, k)
			}
		}
	}
	var out []string
	for _, k := range ReviewerKinds {
		if (contains(need, k) || contains(t.Reviewers, k)) && s.PartOn(k) {
			out = append(out, k)
		}
	}
	return out
}

func (t *Task) helperRuns(kind string) []HelperRun {
	var out []HelperRun
	for _, r := range t.Helpers {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// alive: started recently and not stopped yet.
func (r HelperRun) alive() bool {
	last := r.Started
	if r.LastSeen.After(last) {
		last = r.LastSeen
	}
	return r.Finished.IsZero() && Now().Sub(last) < helperAlive
}

// HelperRunning: some helper of this task is running right now.
func (t *Task) HelperRunning() bool {
	for _, r := range t.Helpers {
		if r.alive() {
			return true
		}
	}
	return false
}

func (t *Task) runByAgent(agentID string) *HelperRun {
	for i := len(t.Helpers) - 1; i >= 0; i-- {
		if t.Helpers[i].AgentID == agentID && agentID != "" {
			return &t.Helpers[i]
		}
	}
	return nil
}

func (t *Task) findingsExist(r HelperRun) bool {
	fi, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), r.Findings))
	return err == nil && fi.Size() > 0
}

// DisputesName: where the lead writes its disputes of one reviewer's
// findings. One file per reviewer, so reviews stay independent.
func DisputesName(kind string) string { return kind + "-disputes.md" }

func (t *Task) findingsText(r HelperRun) string {
	b, _ := os.ReadFile(filepath.Join(TaskDocsDir(t.Root, t.ID), r.Findings))
	return string(b)
}

// disputeOpen: the lead disputed this reviewer's findings and the reviewer
// hasn't answered yet (the disputes were written after its last run began,
// or its findings have no "## Disputes" section).
func (t *Task) disputeOpen(kind string, last HelperRun) bool {
	fi, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), DisputesName(kind)))
	if err != nil || fi.Size() == 0 {
		return false
	}
	if fi.ModTime().After(last.Started) {
		return true
	}
	return len(Section(t.findingsText(last), "disputes")) == 0
}

// upheld: the reviewer kept a disputed finding, which makes it the user's
// call.
func (t *Task) upheld(last HelperRun) bool {
	for _, l := range Section(t.findingsText(last), "disputes") {
		if strings.Contains(strings.ToLower(l), "upheld") {
			return true
		}
	}
	return false
}

// lastFinished is the newest helper run of a kind that has stopped.
func (t *Task) lastFinished(kind string) *HelperRun {
	runs := t.helperRuns(kind)
	for i := len(runs) - 1; i >= 0; i-- {
		if !runs[i].Finished.IsZero() {
			r := runs[i]
			return &r
		}
	}
	return nil
}

func (c *hookCtx) ignore() []string {
	if c.setup == nil {
		return nil
	}
	return c.setup.IgnoreChanges
}

func docExists(t *Task, name string) bool {
	_, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), name))
	return err == nil
}

// gateHelperStart runs when the lead starts a helper (PreToolUse on Agent).
func (c *hookCtx) gateHelperStart(kind string) Output {
	if c.paused {
		return nil
	}
	t := c.task
	if t == nil {
		return c.deny("helper-no-task", fmt.Sprintf("the %s works on a task; start one first.", kind))
	}
	if isWorker(kind) && !c.setup.PartOn(kind) {
		return c.deny("helper-off", fmt.Sprintf("the %s is switched off in this project's setup (parts).", kind))
	}
	switch {
	case kind == HelperTracer:
		// any time during a task: it only reads and reports
	case kind == HelperBuilder || kind == HelperTester:
		if t.Phase != PhaseBuilding {
			return c.deny(kind+"-phase", fmt.Sprintf("the %s works on the approved plan, and task %s is at the %s step. Before the checkpoint, tracers (plumbline:tracer) can trace the code.", kind, t.ID, t.Phase))
		}
	case kind == HelperQA:
		if why := c.qaStartProblem(); why != "" {
			return c.deny("qa-start", why)
		}
	case kind == HelperChallenger:
		if t.Phase == PhaseBuilding {
			return c.deny("challenger-phase", "the challenger is for before the checkpoint, and the plan is already approved. After building, run the reviewers (plumbline:code-reviewer and the others the plan names).")
		}
		if !docExists(t, DocUnderstanding) || !docExists(t, DocPlan) {
			return c.deny("challenger-docs", "write understanding.md and plan.md first: the challenger attacks both.")
		}
		if n := len(t.helperRuns(HelperChallenger)); n >= MaxChallengeRounds {
			return c.deny("challenger-rounds", fmt.Sprintf("the challenger already ran %d times, the most a task gets. Write the \"## Challenge note\" in plan.md with what is still open, and go to the checkpoint.", n))
		}
	case isReviewer(kind):
		if t.Phase != PhaseBuilding {
			return c.deny("reviewer-phase", fmt.Sprintf("the %s checks the built change, and task %s is at the %s step. Before the checkpoint, the challenger (plumbline:challenger) is the one to run.", kind, t.ID, t.Phase))
		}
		if n := len(t.helperRuns(kind)); n >= MaxReviews {
			return c.deny("reviewer-rounds", fmt.Sprintf("the %s already ran %d times (the first review plus 2 fix rounds). Stop and bring what's left to the user, with your suggestion.", kind, n))
		}
		if why := c.disputeProblem(kind); why != "" {
			return c.deny("dispute-evidence", why)
		}
	}
	if prompt, _ := c.in.Input()["prompt"].(string); prompt != "" {
		if why := c.promptProblem(kind, prompt); why != "" {
			return c.deny("helper-prompt", why)
		}
	}
	return nil
}

// disputeProblem: the lead's disputes of a reviewer's findings, written
// since its last run began, each cite the evidence (the guard, the caller,
// the test) with a file:line the lead opened and that hasn't changed since
// (N5). A dispute without evidence is an opinion.
func (c *hookCtx) disputeProblem(kind string) string {
	t := c.task
	p := filepath.Join(TaskDocsDir(t.Root, t.ID), DisputesName(kind))
	fi, err := os.Stat(p)
	if err != nil || fi.Size() == 0 {
		return ""
	}
	if last := t.lastFinished(kind); last != nil && !fi.ModTime().After(last.Started) {
		return "" // answered already
	}
	b, _ := os.ReadFile(p)
	var bare, bad []string
	for _, item := range bullets(string(b)) {
		refs := Refs(item)
		if len(refs) == 0 {
			bare = append(bare, short(plainLine(item)))
			continue
		}
		for _, r := range refs {
			if !isNotesDoc(r.Path) { // the plan and the understanding are the lead's own
				bad = append(bad, CheckRefs(t, fmt.Sprintf(" %s:%d", r.Path, r.Line))...)
			}
		}
	}
	switch {
	case len(bare) > 0:
		return fmt.Sprintf("each dispute in %s cites its evidence: the file:line of the guard, caller or test that shows the finding is wrong (open it first). These cite none: %s", DisputesName(kind), strings.Join(bare, "; "))
	case len(bad) > 0:
		return fmt.Sprintf("the disputes in %s cite lines Plumbline can't check: %s", DisputesName(kind), strings.Join(bad, "; "))
	}
	return ""
}

// gateHelper keeps the helpers to their job. Most are read-only: they read
// the code, run commands that only read, and write their own findings file
// and nothing else. Blind QA also writes test files, and builders change code
// in their own copy (workers.go).
func (c *hookCtx) gateHelper(kind string) Output {
	in := c.in
	if kind == HelperBuilder {
		return c.gateBuilder()
	}
	switch in.ToolName {
	case "Edit", "MultiEdit", "NotebookEdit", "Write":
		p := in.Str("file_path")
		if p == "" {
			p = in.Str("notebook_path")
		}
		abs := Abs(p, in.Cwd)
		var run *HelperRun
		if c.task != nil {
			run = c.task.runByAgent(in.AgentID)
		}
		if run != nil && in.ToolName == "Write" && abs == Abs(filepath.Join(TaskDocsDir(c.task.Root, c.task.ID), run.Findings), "/") {
			return allow("helper findings")
		}
		if kind == HelperQA {
			return c.qaEdit(run, abs)
		}
		if run == nil {
			return c.deny("helper-read-only", fmt.Sprintf("the %s can only write its findings file, and Plumbline has no record of this run (no open task found). Report your findings in your final message instead.", kind))
		}
		if in.ToolName != "Write" {
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only. Put what should change in your findings file.", kind))
		}
		return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only: the only file it can write is its findings file, %s.", kind, filepath.Join(TaskDocsDir(c.task.Root, c.task.ID), run.Findings)))
	case "AskUserQuestion":
		return c.deny("helper-no-questions", fmt.Sprintf("the %s runs without the user. Put open questions in your findings file; the lead decides what to ask.", kind))
	case "Bash":
		return c.helperBash(kind, in.Str("command"))
	case "Read":
		return c.reviewerNotes(kind, in.Str("file_path"), false)
	case "Grep", "Glob":
		base := in.Str("path")
		if base == "" {
			base = in.Cwd // a search with no path searches the working folder
		}
		if p := in.Str("pattern"); in.ToolName == "Glob" && p != "" {
			// A glob reads only what its pattern reaches, which may climb out
			// of the search folder: judge the pattern from there.
			if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
				p = filepath.Join(Abs(base, in.Cwd), p)
			}
			return c.reviewerNotes(kind, p, true)
		}
		if o := c.reviewerNotes(kind, base, true); o != nil {
			return o
		}
	}
	if strings.HasPrefix(in.ToolName, "mcp__") {
		switch c.mcpKind() {
		case "shell":
			if cmd, ok := in.Input()["command"].(string); ok && cmd != "" {
				return c.helperBash(kind, cmd)
			}
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only, and %s runs code.", kind, in.ToolName))
		case "write":
			return c.deny("helper-read-only", fmt.Sprintf("the %s is read-only. Put what should change in your findings file.", kind))
		}
		return c.reviewerNotes(kind, mcpPath(in.Input()), true)
	}
	return nil
}

func (c *hookCtx) helperBash(kind, cmd string) Output {
	for _, s := range ParseShell(cmd, c.in.Cwd) {
		if ReadOnly(s) || plumblineReads(s) || helperMayRun(kind, s) {
			continue
		}
		what := s.Name()
		if s.Opaque != "" {
			what = s.Opaque
		}
		runs := "The lead runs tests and proof."
		switch kind {
		case HelperQA:
			runs = "To run your tests: plumbline run related --files <test files>, or plumbline run test."
		case HelperTester:
			runs = "Run the checks with plumbline run test|related|lint|build|e2e."
		}
		edit := "To change your findings file, use the Edit or Write tool."
		for _, r := range s.Redirects {
			if !harmlessRedirect(r) {
				return c.deny("helper-read-only", fmt.Sprintf("the %s doesn't write files with the shell (%s > ...). %s %s", kind, what, edit, runs))
			}
		}
		return c.deny("helper-read-only", fmt.Sprintf("the %s only runs commands that read (git diff, git log, grep, cat ...). %q changes something or isn't known to be read-only. %s %s", kind, what, edit, runs))
	}
	if !independent(kind) {
		return nil
	}
	isDocs, listsOnly := c.notesListing(cmd)
	for _, s := range ParseShell(cmd, c.in.Cwd) {
		dir := s.Dir
		if dir == "" {
			dir = c.in.Cwd
		}
		recursive := reSearchTool.MatchString(s.Name())
		c.searchSkipsCopies = kind == HelperQA && qaSearchSkipsCopies(s)
		if kind == HelperQA {
			if o := c.qaCommand(s); o != nil {
				return o
			}
		}
		words := append(append([]string{}, s.Inputs...), s.Redirects...)
		if len(s.Words) > 1 {
			words = append(words, s.Words[1:]...)
		}
		for i, w := range words {
			j := i - len(s.Inputs) - len(s.Redirects) + 1
			dyn := (j >= 1 && j < len(s.Dyn) && s.Dyn[j]) || ((j < 1) && strings.ContainsAny(w, "$`"))
			if dyn {
				// Its value isn't known until it runs: refuse any that
				// could build a path into Plumbline's data folder.
				if wordMentionsDataDir(w) || strings.Contains(strings.ToLower(w), "plumb") {
					return c.deny("reviewer-own-notes", fmt.Sprintf("the %s can't tell which notes file %q reads; name the file.", kind, w))
				}
				if kind == HelperQA {
					return c.deny("qa-blind", fmt.Sprintf("blind QA names paths plainly: Plumbline can't tell where %q points, and it mustn't reach the builders' copies.", w))
				}
				continue
			}
			if strings.HasPrefix(w, "-") {
				// A flag's value can name a file: --file=<path>.
				_, v, ok := strings.Cut(w, "=")
				if !ok {
					continue
				}
				w = v
			}
			if listsOnly && s.Name() == "ls" && isDocs(w, dir) {
				continue
			}
			if o := c.reviewerNotes(kind, Abs(w, dir), recursive); o != nil {
				return o
			}
		}
	}
	c.searchSkipsCopies = false
	// Text fed to xargs or a read loop (heredocs, here-strings) names files
	// too, and isn't kept on the commands inside a loop: check every word of
	// the command as written.
	for _, w := range strings.FieldsFunc(cmd, func(r rune) bool { return strings.ContainsRune(" \t\n;|&()<>'\"`=", r) }) {
		if kind == HelperQA && strings.Contains(strings.ToLower(w), WorktreesDir+"/"+copyPrefix) {
			return c.deny("qa-blind", "blind QA doesn't look at the builders' copies of the repo: write the tests from plan.md's cases and the code as it was approved.")
		}
		switch {
		case strings.Contains(w, "$"):
			if strings.Contains(strings.ToLower(w), "plumb") {
				return c.deny("reviewer-own-notes", fmt.Sprintf("the %s can't tell which notes file %q reads; name the file.", kind, w))
			}
		case listsOnly && isDocs(w, c.in.Cwd):
		case filepath.IsAbs(w) || strings.HasPrefix(w, "~/"):
			if o := c.reviewerNotes(kind, w, false); o != nil {
				return o
			}
		}
	}
	return nil
}

// notesListing: isDocs tells whether a word names the task's notes folder
// itself; listsOnly is true when only plain ls operands name it. ls shows
// the names there, not what the findings say, so the review stays
// independent (HR1). Any other command naming the folder is checked as before.
func (c *hookCtx) notesListing(cmd string) (isDocs func(w, dir string) bool, listsOnly bool) {
	docs := ""
	if c.task != nil {
		docs = foldPath(Abs(TaskDocsDir(c.task.Root, c.task.ID), "/"))
	}
	isDocs = func(w, dir string) bool { return docs != "" && w != "" && foldPath(Abs(w, dir)) == docs }
	if docs == "" {
		return isDocs, false
	}
	for _, s := range ParseShell(cmd, c.in.Cwd) {
		dir := s.Dir
		if dir == "" {
			dir = c.in.Cwd
		}
		for _, w := range append(append([]string{}, s.Inputs...), s.Redirects...) {
			if isDocs(w, dir) {
				return isDocs, false
			}
		}
		for i := 1; i < len(s.Words); i++ {
			if isDocs(s.Words[i], dir) && (s.Name() != "ls" || s.dyn(i) || s.glob(i)) {
				return isDocs, false
			}
		}
	}
	return isDocs, true
}

var reQATestsDir = regexp.MustCompile(`^qa-[0-9]+-tests$`)

// Commands that search a folder tree: given a folder above the notes, they
// read every reviewer's findings.
var reSearchTool = regexp.MustCompile(`^(rg|grep|egrep|fgrep|ugrep|ag|ack|find|fd|tree|du)$`)

// reviewerNotes keeps the reviews independent. In Plumbline's data folder a
// reviewer reads only understanding.md, plan.md and its own earlier
// findings: not another reviewer's findings, and not the report, which sums
// them up. With tree set (a search), a folder holding the data folder counts
// too.
func (c *hookCtx) reviewerNotes(kind, path string, tree bool) Output {
	if !independent(kind) || path == "" {
		return nil
	}
	data := Abs(DataDir(), "/")
	abs := Abs(path, c.in.Cwd) // resolves symlinks, so /tmp and /private/tmp match
	pattern := false
	if i := strings.IndexAny(abs, "*?["); i >= 0 {
		abs, pattern = filepath.Dir(abs[:i]+"x"), true
	}
	if kind == HelperQA {
		if o := c.qaBlind(abs, tree, pattern); o != nil {
			return o
		}
	}
	inside := isInsideFold(abs, data) || foldPath(abs) == foldPath(data)
	// A search or a pattern starting above the data folder can reach into it.
	above := (tree || pattern) && isInsideFold(data, abs)
	if !inside && !above {
		return nil
	}
	if inside && !pattern && c.task != nil {
		docs := Abs(TaskDocsDir(c.task.Root, c.task.ID), "/")
		base := filepath.Base(abs)
		own := false
		for _, r := range c.task.helperRuns(kind) { // includes 0.3.x review-N.md
			own = own || foldPath(r.Findings) == foldPath(base)
		}
		own = own || foldPath(base) == foldPath(DisputesName(kind))
		if foldPath(filepath.Dir(abs)) == foldPath(docs) && (base == DocUnderstanding || base == DocPlan || own) {
			return nil
		}
		// Blind QA's tests as it wrote them are tests, not findings (N22).
		if rel, err := filepath.Rel(docs, abs); err == nil && !climbsOut(rel) && reQATestsDir.MatchString(strings.Split(filepath.ToSlash(rel), "/")[0]) {
			return nil
		}
	}
	return c.deny("reviewer-own-notes", fmt.Sprintf("each review stays independent: in the notes folder the %s reads only %s, %s and its own earlier findings, not other reviewers' findings or the report. Read the code and the diff for the rest.", kind, DocUnderstanding, DocPlan))
}

// SubagentStart records a helper run and tells the helper where its notes
// are and which file it writes.
func SubagentStart(in *HookInput) Output {
	kind := helperKind(in.AgentType, false)
	if kind == "" {
		return nil
	}
	c := newCtx(in)
	defer c.close()
	if c.task == nil {
		return Context("SubagentStart", msg(fmt.Sprintf("no open task was found, so this %s run isn't recorded and can't write a findings file. Give your findings in your final message.", kind)))
	}
	var made builderCopy
	if kind == HelperBuilder {
		// Checking out a copy can take a while, so it's made outside the
		// task lock; the run is recorded under the lock afterwards.
		made = c.makeBuilderCopy()
		if c.task == nil {
			return Context("SubagentStart", msg("the task's state couldn't be read again after making the copy, so this builder run isn't recorded. Stop and tell the lead."))
		}
	}
	t := c.task
	n := len(t.helperRuns(kind)) + 1
	run := HelperRun{Kind: kind, AgentID: in.AgentID, Started: Now(), Findings: findingsName(kind, n),
		SawDocs: docExists(t, DocUnderstanding) && docExists(t, DocPlan)}
	run.PrintBefore, _ = Fingerprint(t.Root, c.ignore())
	if kind == HelperBuilder {
		run.Worktree, run.BaseCommit, run.Error, run.Setup = made.path, made.base, made.err, made.setup
		if made.path != "" {
			run.PrintBefore = made.tree
		}
	}
	t.Helpers = append(t.Helpers, run)
	_ = SaveTask(t)
	root := c.root
	if run.Worktree != "" {
		root = run.Worktree
	}
	RefreshSnapshot(c.snapID(), root) // its own snapshot, for its changed-files check
	c.log("helper", kind+"-start", run.Error)
	docs := TaskDocsDir(t.Root, t.ID)
	var b strings.Builder
	fmt.Fprintf(&b, "you are the %s for task %s \"%s\" (%s). Notes folder: %s.", kind, t.ID, t.Title, t.Size, docs)
	if isWorker(kind) {
		c.workerStartNote(&b, run, n)
	} else if kind == HelperChallenger {
		fmt.Fprintf(&b, " Attack %s and %s there. This is round %d of at most %d.", DocUnderstanding, DocPlan, n, MaxChallengeRounds)
	} else {
		base := t.StartCommit
		if base == "" {
			base = "HEAD"
		}
		fmt.Fprintf(&b, " Review the change against the approved %s: `git diff %s` plus new files from `git status --porcelain`. This is run %d of at most %d for the %s. Other reviewers check the change on their own: in the notes folder read only %s, %s and your own earlier findings.", DocPlan, base, n, MaxReviews, kind, DocUnderstanding, DocPlan)
		if kind == HelperCodeReviewer {
			b.WriteString(` List every changed file under "## Coverage" as "- <path>: covered" or "- <path>: not covered, <why>"; a file not covered makes the verdict Not ready.`)
		}
		if changed := qaTestsChanged(t); len(changed) > 0 {
			fmt.Fprintf(&b, " Blind QA's tests changed after it wrote them (%s); its versions are under %s/qa-<n>-tests/. Compare them (diff) and flag any change to what a test expects: only its setup may change.", listSome(changed, 8), docs)
		}
	}
	if n > 1 && !isWorker(kind) {
		// A fresh pass first: a reviewer who reads its earlier verdict first
		// anchors on it and misses what the fix broke.
		done := "fixed"
		if kind == HelperChallenger {
			done = "answered"
		}
		fmt.Fprintf(&b, " Do a fresh pass first, as if this were your first run, and write those findings. Only then open your earlier findings (%s) and check whether each was really %s.", t.helperRuns(kind)[n-2].Findings, done)
	}
	if plan, _, err := ReadDoc(t, DocPlan); err == nil {
		if shared := CheckPlan(plan).SharedTests; len(shared) > 0 {
			fmt.Fprintf(&b, " Shared tests: one test is the proof for several cases; check it really proves each of them: %s.", strings.Join(shared, ", "))
		}
	}
	if kind != HelperChallenger && docExists(t, DisputesName(kind)) {
		fmt.Fprintf(&b, " The lead disputes some of your findings in %s: answer each one under ## Disputes in your findings file (withdrawn, or upheld with your evidence).", filepath.Join(docs, DisputesName(kind)))
	}
	if !isWorker(kind) {
		fmt.Fprintf(&b, " The shared checklist, scoring and findings format: %s. Write your findings to %s as you go: it is the only file you can write. Terminal commands must only read. Plumbline checks the file when you stop: each cited file:line needs a backticked quote from within %d lines of it.", ChecklistPath(), filepath.Join(docs, run.Findings), quoteWindow)
	}
	return Context("SubagentStart", msg(b.String()))
}

// stopProblems: whether the helper's findings file is missing, and what's
// wrong with it. It records on run what the checks read from the file.
func (c *hookCtx) stopProblems(run *HelperRun, kind string) (missing bool, problems []string) {
	t := c.task
	if !t.findingsExist(*run) {
		return true, nil
	}
	switch {
	case kind == HelperChallenger || isReviewer(kind):
		problems = c.findingsProblems(run)
	case kind == HelperBuilder:
		if run.Status = builderStatus(t.findingsText(*run)); run.Status == "" {
			problems = []string{fmt.Sprintf(`end your notes with "## Status" and one of: %s (blocked or needs context when you couldn't finish: bad work is worse than none)`, strings.Join(BuilderStatuses, ", "))}
		}
	}
	return false, problems
}

// stopReason: what a helper must do before it stops or hands back, or "".
func stopReason(t *Task, run *HelperRun, missing bool, problems []string, verb string) string {
	out := filepath.Join(TaskDocsDir(t.Root, t.ID), run.Findings)
	switch {
	case missing:
		return fmt.Sprintf("write your findings to %s before you %s (if you found nothing, say so there, with what you checked).", out, verb)
	case len(problems) > 0:
		return fmt.Sprintf("fix your findings file (%s) before you %s: %s.", out, verb, strings.Join(problems, "; "))
	}
	return ""
}

// gateHandback: a helper handing its report back (SubagentHandback) gets the
// stop check first, since a background helper that has handed back isn't
// sent back by SubagentStop (SA1). A second try with the file unchanged
// goes through, as a second stop does, and SubagentStop records what's
// still wrong.
func (c *hookCtx) gateHandback(kind string) Output {
	t := c.task
	if t == nil {
		return nil
	}
	run := t.runByAgent(c.in.AgentID)
	if run == nil || !run.Finished.IsZero() {
		return nil
	}
	missing, problems := c.stopProblems(run, kind)
	reason := stopReason(t, run, missing, problems, "hand back")
	if reason == "" {
		return nil
	}
	seen := sha256Hex([]byte(t.findingsText(*run)))
	if missing {
		seen = "missing"
	}
	if run.HandbackDenied == seen {
		return nil
	}
	run.HandbackDenied = seen
	_ = SaveTask(t)
	return c.deny("helper-handback", reason)
}

// SubagentStop records the end of a helper run. A helper that wrote no
// findings is sent back once to write them.
func SubagentStop(in *HookInput) Output {
	kind := helperKind(in.AgentType, false)
	if kind == "" {
		return nil
	}
	c := newCtx(in)
	defer c.close()
	if c.task == nil {
		return nil
	}
	t := c.task
	run := t.runByAgent(in.AgentID)
	if run == nil || !run.Finished.IsZero() {
		return nil
	}
	missing, problems := c.stopProblems(run, kind)
	if reason := stopReason(c.task, run, missing, problems, "stop"); reason != "" && !in.StopHookActive {
		if len(problems) > 0 {
			_ = SaveTask(t) // what the checks read from the file
		}
		return Output{"decision": "block", "reason": msg(reason)}
	}
	run.Problems = problems
	run.Finished = Now()
	run.PrintAfter, _ = Fingerprint(t.Root, c.ignore())
	detail := ""
	switch kind {
	case HelperBuilder:
		detail = strings.TrimSpace(c.stopBuilder(run) + " " + run.Status)
	case HelperQA:
		c.keepQATests(run)
		detail = fmt.Sprintf("%d test files", len(run.Wrote))
	}
	if len(problems) > 0 {
		detail = strings.TrimSpace(fmt.Sprintf("%s %d findings problems", detail, len(problems)))
	}
	t.recordTokens("agent:"+in.AgentID, in.AgentTranscript)
	_ = SaveTask(t)
	if !t.findingsExist(*run) {
		detail = strings.TrimSpace(detail + " no findings file")
	}
	c.log("helper", kind+"-stop", detail)
	return nil
}

// challengeProblems: before the checkpoint, a finished challenger run on both
// documents and a challenge note in the plan. Small tasks skip it.
func (c *hookCtx) challengeProblems(plan string) []string {
	t := c.task
	if t.Size == "small" || !c.setup.PartOn(HelperChallenger) {
		return nil
	}
	var p []string
	ok := false
	runs := t.helperRuns(HelperChallenger)
	for _, r := range runs {
		// The last round counts even when its file failed the checks: no
		// round is left to fix it, so it goes in the challenge note.
		last := len(runs) >= MaxChallengeRounds && r.Complete() && t.findingsExist(r)
		if (t.counts(r) || last) && r.SawDocs {
			ok = true
		}
	}
	switch {
	case ok:
	case t.HelperRunning():
		p = append(p, "the challenger is still running; wait for its findings")
	case t.lastFinished(HelperChallenger) != nil && len(t.lastFinished(HelperChallenger).Problems) > 0:
		p = append(p, "the challenger's last findings file failed Plumbline's checks ("+strings.Join(t.lastFinished(HelperChallenger).Problems, "; ")+"); run the challenger again (plumbline:challenger)")
	default:
		p = append(p, "the challenger hasn't attacked the understanding and the plan yet: start the Agent tool with subagent_type plumbline:challenger (it needs both files written first, and must finish with a findings file)")
	}
	if len(Section(plan, "challenge note")) == 0 {
		p = append(p, `plan.md needs a "## Challenge note": what the challenger questioned, what you changed, and anything you disagreed with and why`)
	}
	return p
}

// reviewMissing: the finish check needs, from every required reviewer, a
// finished review of the current code, and a "## Review" section in a report
// written after the last of them.
func reviewMissing(t *Task, s *Setup, fp, report string, reportTime time.Time) []string {
	kinds := RequiredReviewers(t, s)
	if len(kinds) == 0 {
		return nil
	}
	var notRun, running, again, capped, answer, answerCapped, upheld []string
	var lastDone time.Time
	for _, k := range kinds {
		runs := t.helperRuns(k)
		last := t.lastFinished(k)
		if last != nil && last.Finished.After(lastDone) {
			lastDone = last.Finished
		}
		name := "plumbline:" + k
		switch {
		case len(runs) == 0:
			notRun = append(notRun, name)
		case runs[len(runs)-1].alive():
			running = append(running, name)
		case last == nil || !t.counts(*last) || last.PrintAfter != fp:
			if len(runs) >= MaxReviews {
				capped = append(capped, name)
			} else {
				again = append(again, name)
			}
		case t.disputeOpen(k, *last):
			if len(runs) >= MaxReviews {
				answerCapped = append(answerCapped, name)
			} else {
				answer = append(answer, name)
			}
		default:
			if t.upheld(*last) {
				upheld = append(upheld, name)
			}
		}
	}
	var miss []string
	if len(notRun) > 0 {
		miss = append(miss, "review: run these reviewers on the finished change, after the proof (Agent tool, each with its subagent_type; they can run at the same time): "+strings.Join(notRun, ", "))
	}
	if len(running) > 0 {
		miss = append(miss, "review: still running, wait for their findings: "+strings.Join(running, ", "))
	}
	if len(again) > 0 {
		miss = append(miss, "review: the code changed after the last review (or during it, or a run never finished, left no findings file or one that failed Plumbline's checks); run these again to check the fixes: "+strings.Join(again, ", "))
	}
	if len(capped) > 0 {
		miss = append(miss, fmt.Sprintf("review: the code changed after the last review and all %d runs are used (the first plus 2 fix rounds) for %s; stop and ask the user how to go on", MaxReviews, strings.Join(capped, ", ")))
	}
	if len(answer) > 0 {
		miss = append(miss, "review: you disputed findings (<reviewer>-disputes.md in the notes folder); run these again so each answers its disputes, withdrawn or upheld: "+strings.Join(answer, ", "))
	}
	forYou := strings.ToLower(strings.Join(Section(report, "for you"), "\n"))
	if len(answerCapped) > 0 && !strings.Contains(forYou, "disput") {
		miss = append(miss, fmt.Sprintf("review: you disputed findings, but all %d runs are used for %s; put the disputes under \"For you\" in the report (say \"disputed\", with both sides) and stop and ask the user", MaxReviews, strings.Join(answerCapped, ", ")))
	}
	if len(upheld) > 0 && report != "" && !strings.Contains(forYou, "upheld") {
		miss = append(miss, "report: reviewers upheld findings you disputed, so they're the user's call; list each under \"For you\" (say \"upheld\", with both sides): "+strings.Join(upheld, ", "))
	}
	if report != "" {
		if len(Section(report, "review")) == 0 {
			miss = append(miss, `report: add a "## Review" section: what each reviewer found, what you checked against the code, and what you fixed or pushed back on`)
		} else if reportTime.Before(lastDone) {
			miss = append(miss, "report: the last review finished after report.md was written; update it with the review")
		}
	}
	return miss
}

// teamProblems: before the checkpoint, the plan's Team names every
// specialist the planned files call for.
func (c *hookCtx) teamProblems(plan string, files []string) []string {
	if !c.setup.PartOn("reviewer") {
		return nil
	}
	team := TeamReviewers(plan)
	var p []string
	for _, k := range ReviewersFor(c.task.Size, c.task.Root, files) {
		if why, specialist := signalWhy[k]; specialist && c.setup.PartOn(k) && !contains(team, k) {
			p = append(p, fmt.Sprintf(`plan.md's "## Team" must name the %s: the planned files touch %s`, k, why))
		}
	}
	return p
}
