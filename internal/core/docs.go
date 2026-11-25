package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Task documents Claude writes into the task's docs folder.
const (
	DocUnderstanding = "understanding.md"
	DocPlan          = "plan.md"
	// (0.7.2: the plan in plain words is shown in the chat, not written to a
	// file; DocForYou stays so older tasks' files are still task notes.)
	// DocForYou: the plan in plain words for the user, with the detail
	// behind the chat summary (0.7.1). Normal and big tasks.
	DocForYou = "plan-for-you.md"
	DocReport = "report.md"
)

// JiraCommentName: the draft comment for a Jira ticket, named for the
// ticket so it's easy to find and copy (TICKET-123-jira-comment.md).
func JiraCommentName(ticket string) string {
	t := strings.TrimSpace(ticket)
	if t == "" {
		return "jira-comment.md"
	}
	return safeName(t) + "-jira-comment.md"
}

// JiraCommentProblems: the draft is only the comment, in plain words, with
// nothing that marks it as written by AI.
func JiraCommentProblems(text string) []string {
	var p []string
	if strings.TrimSpace(text) == "" {
		p = append(p, "it is empty")
	}
	if m := AIMarkers(text); len(m) > 0 {
		p = append(p, "remove AI markers ("+strings.Join(m, "; ")+")")
	}
	if strings.Contains(text, "—") {
		p = append(p, "no em dashes (use a comma, a colon or a full stop)")
	}
	return p
}

var reRef = regexp.MustCompile("(?:^|[\\s(\\[`'\"])((?:[\\w.@+-]+/)*[\\w@+-][\\w.@+-]*\\.[A-Za-z0-9]+):(\\d+)(?:-(\\d+))?")

type Ref struct {
	Path string
	Line int
	End  int // the last line of a range (file:10-14), or 0
}

// Refs finds every file:line reference in a document.
func Refs(text string) []Ref {
	var out []Ref
	seen := map[string]bool{}
	for _, m := range reRef.FindAllStringSubmatch(text, -1) {
		p := m[1]
		if strings.Contains(p, "://") || !strings.ContainsAny(strings.ToLower(p), "abcdefghijklmnopqrstuvwxyz") {
			continue // a URL, or a number like "4.6:1" (a contrast ratio), not a file (FP3)
		}
		k := p + ":" + m[2]
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Ref{Path: p, Line: atoi(m[2]), End: atoi(m[3])})
	}
	return out
}

// seenProblem: a file:line in a file Claude didn't open. A line a search
// showed is enough for a file the plan only mentions (RD2); a file the plan
// changes must be opened. "" when the search showed it.
func seenProblem(t *Task, r Ref) string {
	notOpened := fmt.Sprintf("%s:%d: Claude didn't open this file during the task", r.Path, r.Line)
	abs := filepath.Join(t.Root, filepath.FromSlash(r.Path))
	s, ok := t.Seen[abs]
	if !ok {
		hits := seenBySuffix(t, r.Path)
		if len(hits) > 1 {
			return fmt.Sprintf("%s:%d: %s matches %d files a search showed: %s; write the path from the repo's top", r.Path, r.Line, r.Path, len(hits), listFiles(hits))
		}
		if len(hits) == 1 {
			abs = hits[0]
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(t.Root, filepath.FromSlash(abs))
			}
			s, ok = t.Seen[abs]
		}
	}
	if !ok {
		return notOpened
	}
	rel := filepath.ToSlash(strings.TrimPrefix(abs, t.Root+string(filepath.Separator)))
	if contains(planFiles(t), rel) {
		return fmt.Sprintf("%s:%d: the plan changes this file, so open it (a search shows a line, not the code around it)", r.Path, r.Line)
	}
	if fileHash(abs) != s.Hash {
		return fmt.Sprintf("%s:%d: the file changed after the search showed it; open it", r.Path, r.Line)
	}
	if !s.near(r.Line) {
		return fmt.Sprintf("%s:%d: the search showed other lines of this file, not this one; open it", r.Path, r.Line)
	}
	return ""
}

// CheckRefs makes sure every file and line points to a file read during this
// task (by the lead or a helper) that has not changed since it was read.
func CheckRefs(t *Task, text string) []string {
	var problems []string
	refs := Refs(text)
	for _, r := range refs {
		abs := filepath.Join(t.Root, filepath.FromSlash(r.Path))
		rd, ok := t.Reads[abs]
		if !ok {
			// allow a path written relative to a subfolder if it matches one read file
			hits := readsBySuffix(t, r.Path)
			if len(hits) > 1 {
				problems = append(problems, fmt.Sprintf("%s:%d: %s matches %d opened files: %s; write the path from the repo's top", r.Path, r.Line, r.Path, len(hits), listFiles(hits)))
				continue
			}
			if len(hits) == 1 {
				abs = hits[0]
				if !filepath.IsAbs(abs) {
					abs = filepath.Join(t.Root, filepath.FromSlash(abs))
				}
				rd, ok = t.Reads[abs]
			}
		}
		if !ok {
			if p := seenProblem(t, r); p != "" {
				problems = append(problems, p)
			}
			continue
		}
		if fileHash(abs) != rd.Hash {
			problems = append(problems, fmt.Sprintf("%s:%d: the file changed after it was read; open it again", r.Path, r.Line))
			continue
		}
		if n := lineCount(abs); r.Line > n {
			problems = append(problems, fmt.Sprintf("%s:%d: the file has only %d lines", r.Path, r.Line, n))
		}
	}
	return problems
}

// readsBySuffix: the opened files whose path ends in "/"+rel, from the
// repo's top when inside it, sorted.
func readsBySuffix(t *Task, rel string) []string {
	var hits []string
	for p := range t.Reads {
		if strings.HasSuffix(filepath.ToSlash(p), "/"+rel) {
			if r, err := filepath.Rel(t.Root, p); err == nil && !climbsOut(r) {
				p = filepath.ToSlash(r)
			}
			hits = append(hits, p)
		}
	}
	sort.Strings(hits)
	return hits
}

func lineCount(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		n++
	}
	return n
}

// Section returns the lines under a "## <name>" heading (case-insensitive,
// prefix match), up to the next heading of the same or higher level.
func Section(text, name string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			h := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trim, "#")))
			if in {
				break
			}
			if strings.HasPrefix(h, strings.ToLower(name)) {
				in = true
			}
			continue
		}
		if in && trim != "" {
			out = append(out, trim)
		}
	}
	return out
}

func bullet(line string) (string, bool) {
	for _, p := range []string{"- ", "* ", "+ "} {
		if strings.HasPrefix(line, p) {
			return strings.TrimSpace(line[len(p):]), true
		}
	}
	return "", false
}

// CaseLabels is the one list of case labels. rules.md and checkpoints.md
// repeat it, and a test keeps all three the same.
var CaseLabels = []string{"normal", "edge", "refused", "boundary", "repeat-safe", "bug", "performance", "failure", "concurrent"}

var reCaseLabel = regexp.MustCompile(`^\[(` + strings.Join(CaseLabels, "|") + `)\]`)

// caseLabelList: "[normal] [edge] ...", for messages.
func caseLabelList() string {
	return "[" + strings.Join(CaseLabels, "] [") + "]"
}

// Plan wording that leaves the decision for later. Each is a whole phrase,
// so "edge" or "validation" alone doesn't count.
var reVaguePlan = regexp.MustCompile(`(?i)\b(?:tbd|to be (?:decided|determined)|decide later|figure (?:it |this |that )?out later|fill (?:it |this |these )?in later|details later|handle (?:all |any |the )?(?:other )?edge cases|(?:appropriate|proper|necessary) (?:validation|error handling)|handle errors (?:appropriately|properly|as needed)|(?:write|add) tests (?:for the above|as needed))\b`)

// vaguePlanLines: plan lines that decide nothing ("TBD", "handle edge
// cases", "add appropriate validation"). Text in backticks or a code block
// is code or a name, and the challenge note may quote the challenger, so
// neither is the plan's own wording.
func vaguePlanLines(text string) []string {
	var out []string
	fence, note := false, false
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			fence = !fence
			continue
		}
		if fence || trim == "" {
			continue
		}
		if strings.HasPrefix(trim, "#") {
			note = strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(trim, "#"))), "challenge note")
			continue
		}
		if note {
			continue
		}
		plain := reBacktick.ReplaceAllString(trim, "")
		if m := reVaguePlan.FindString(plain); m != "" {
			out = append(out, fmt.Sprintf("%q in %q", m, short(trim)))
		}
	}
	return out
}

// A case's test: "test: path" or "test: path::name", in backticks when the
// name has spaces ("test: `a.test.ts::refunds twice`"). "test" must be a
// whole word, so "latest: x" is not a link.
// caseTestLink: the case's first "test:" link that names a file (a path,
// or a name with an extension), as "file" or "file::test name". bad is the
// first word after "test:" when none does.
func caseTestLink(b string) (ref, bad string) {
	for _, m := range reTestLink.FindAllStringSubmatch(b, -1) {
		r := strings.TrimSpace(m[1] + strings.TrimRight(m[2], ").,;"))
		f := strings.SplitN(r, "::", 2)[0]
		if strings.Contains(f, "/") || filepath.Ext(f) != "" {
			return r, ""
		}
		if bad == "" {
			bad = f
		}
	}
	return "", bad
}

var reTestLink = regexp.MustCompile("(?i)\\btest:\\s*(?:`([^`]+)`|([^\\s`]+))")

const notApply = "does not apply, because"

const commandsOnlyLine = "none (commands only)"

// "test: does not apply, because ..." names no test file (PL1).
var reTestNotApply = regexp.MustCompile(`(?i)\btest:\s*(does not apply)`)

// PlanCheck is what the checkpoint needs from plan.md.
type PlanCheck struct {
	Files        []string
	CommandsOnly bool
	NewFiles     []string
	Cases        int
	CaseTests    []string // test files the cases link to
	// BugTests: the [bug] cases' test links ("file" or "file::name"), whose
	// test must be seen failing before the fix (WF4).
	BugTests []string
	// SharedTests: named tests (file::name) that are the proof for more than
	// one case, for the challenger and reviewers to check.
	SharedTests []string
	// Goal: the plan's "## Goal" line, what done looks like in the user's
	// words; reviewers and the report check the work against it.
	Goal       string
	ProofKinds []string // ui, api, schema that must be proven
	Approved   []string // commands in "Needs your OK" (packages, outside-laptop commands)
	Guards     []string
	Problems   []string
}

func CheckPlan(text string) PlanCheck { return CheckPlanSized(text, false) }

var reNewFile = regexp.MustCompile(`(?i)\(\s*new\b|\bnew file\b`)

func planFileEntry(b string) (string, bool) {
	b = strings.TrimSpace(b)
	var f, rest string
	if strings.HasPrefix(b, "`") {
		end := strings.Index(b[1:], "`")
		if end < 0 {
			f, rest = b[1:], ""
		} else {
			f, rest = b[1:end+1], b[end+2:]
		}
	} else {
		fields := strings.Fields(b)
		if len(fields) == 0 {
			return "", false
		}
		f, rest = fields[0], strings.TrimPrefix(b, fields[0])
	}
	f = strings.TrimSpace(strings.Trim(strings.TrimSpace(f), "`"))
	f = strings.TrimRight(f, ":,;.")
	f = strings.TrimPrefix(f, "./")
	return filepath.ToSlash(f), reNewFile.MatchString(rest)
}

// CheckPlanSized: a small plan may leave out "## Real-world proof" and
// "## Needs your OK" (left out means none); everything it does write is
// read the same way (UX1).
func CheckPlanSized(text string, small bool) PlanCheck {
	var pc PlanCheck
	for _, l := range Section(text, "goal") {
		if g := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "-* ")); g != "" {
			pc.Goal = g
			break
		}
	}
	if pc.Goal == "" {
		pc.Problems = append(pc.Problems, `plan.md needs a "## Goal" line first: what done looks like, in the user's words ("Done when: ..."), so the work can be checked against it`)
	}
	none, exact := false, false
	for _, l := range Section(text, "files") {
		if b, ok := bullet(l); ok {
			if plain := strings.ToLower(strings.TrimSpace(reMarkup.ReplaceAllString(b, ""))); plain == "none" || strings.HasPrefix(plain, "none ") || strings.HasPrefix(plain, "none(") {
				none = true
				exact = exact || plain == commandsOnlyLine
				continue
			}
			f, isNew := planFileEntry(b)
			if f == "" {
				continue
			}
			pc.Files = append(pc.Files, f)
			if isNew {
				pc.NewFiles = append(pc.NewFiles, f)
			}
		}
	}
	switch {
	case none && len(pc.Files) > 0:
		pc.Problems = append(pc.Problems, `plan.md's ## Files has "none" next to real files: "- none (commands only)" must be the only line, for a task that changes no files; otherwise list the files and drop "none"`)
	case none && !exact:
		pc.Problems = append(pc.Problems, `plan.md's ## Files says "none": for a task that changes no files, write the single line "- none (commands only)"`)
	case none:
		pc.CommandsOnly = true
	case len(pc.Files) == 0:
		pc.Problems = append(pc.Problems, `plan.md needs a "## Files" list naming every file the plan changes, or the single line "- none (commands only)" for a task that only runs commands`)
	}
	refs := map[string]int{}
	for _, l := range Section(text, "cases") {
		b, ok := bullet(l)
		if !ok {
			continue
		}
		pc.Cases++
		if !reCaseLabel.MatchString(b) {
			pc.Problems = append(pc.Problems, fmt.Sprintf("case %q needs a label: %s", short(b), caseLabelList()))
		}
		b = reTestNotApply.ReplaceAllString(b, "$1")
		lower := strings.ToLower(b)
		if ref, bad := caseTestLink(b); ref != "" || bad != "" {
			if pc.CommandsOnly {
				pc.Problems = append(pc.Problems, fmt.Sprintf("case %q links a test, but the plan is commands only (## Files is \"none\"), so no test can be written: write \"does not apply, because ...\", or list the files the change needs", short(b)))
				continue
			}
			if ref == "" {
				// "same test: at 120 s" isn't a link to a file named "at" (PL2)
				pc.Problems = append(pc.Problems, fmt.Sprintf("case %q: %q isn't a test file; write \"test: <path to the test file>\" (a path or a file name like a_test.go), or \"does not apply, because ...\"", short(b), bad))
				continue
			}
			pc.CaseTests = append(pc.CaseTests, strings.SplitN(ref, "::", 2)[0])
			if strings.HasPrefix(b, "[bug]") {
				pc.BugTests = append(pc.BugTests, ref)
			}
			if strings.Contains(ref, "::") {
				refs[ref]++
			}
		} else if !strings.Contains(lower, notApply) {
			pc.Problems = append(pc.Problems, fmt.Sprintf("case %q needs \"test: <file>\" or \"does not apply, because ...\"", short(b)))
		}
	}
	if pc.Cases == 0 {
		pc.Problems = append(pc.Problems, `plan.md needs a "## Cases" list (each case labelled and linked to a test)`)
	}
	for ref, n := range refs {
		if n > 1 {
			pc.SharedTests = append(pc.SharedTests, fmt.Sprintf("%s (%d cases)", ref, n))
		}
	}
	sort.Strings(pc.SharedTests)
	proof := Section(text, "real-world proof")
	if len(proof) == 0 && !small {
		pc.Problems = append(pc.Problems, `plan.md needs a "## Real-world proof" section: ui / api / schema, or "does not apply, because ..."`)
	}
	for _, l := range proof {
		b, ok := bullet(l)
		if !ok {
			b = l
		}
		lower := strings.ToLower(reMarkup.ReplaceAllString(b, ""))
		if m := reProofKind.FindStringSubmatch(lower); m != nil && !strings.Contains(lower, notApply) && !contains(pc.ProofKinds, m[1]) {
			pc.ProofKinds = append(pc.ProofKinds, m[1])
		}
	}
	if !small && !strings.Contains(strings.ToLower(text), "## needs your ok") {
		pc.Problems = append(pc.Problems, `plan.md needs a "## Needs your OK" section: packages to install and commands that reach outside the laptop, each in backticks, or "none"`)
	}
	for _, l := range Section(text, "needs your ok") {
		if m := reBacktick.FindAllStringSubmatch(l, -1); m != nil {
			for _, x := range m {
				pc.Approved = append(pc.Approved, strings.TrimSpace(x[1]))
			}
		}
	}
	pc.Guards, pc.Problems = planGuards(text, pc.Problems)
	if vague := vaguePlanLines(text); len(vague) > 0 {
		pc.Problems = append(pc.Problems, "plan.md has lines that leave the decision for later ("+strings.Join(vague, "; ")+"): name the exact cases, checks or values instead")
	}
	return pc
}

const guardsHelp = `each check the plan removes or narrows (an ask, a refusal, a permission check, a validation) or new path that runs without asking, with its file:line and every shape that reaches it, or the single line "- none"`

func planGuards(text string, problems []string) ([]string, []string) {
	if !hasHeading(text, "guards loosened") {
		return nil, append(problems, `plan.md needs a "## Guards loosened" section: `+guardsHelp)
	}
	var guards, bare []string
	saidNone := false
	for _, b := range guardItems(text) {
		if strictNone(b) && len(Refs(" "+b)) == 0 {
			saidNone = true
			continue
		}
		guards = append(guards, b)
		if len(Refs(" "+b)) == 0 {
			bare = append(bare, short(b))
		}
	}
	switch {
	case len(guards) == 0 && !saidNone:
		problems = append(problems, `plan.md's "## Guards loosened" is empty: `+guardsHelp)
	case len(guards) > 0 && saidNone:
		problems = append(problems, `plan.md's "## Guards loosened" says "none" next to a guard: list the guards and drop "none", or keep only "- none"`)
	}
	if len(bare) > 0 {
		problems = append(problems, fmt.Sprintf(`each guard in "## Guards loosened" cites the code that checks it (file:line): %s`, strings.Join(bare, "; ")))
	}
	return guards, problems
}

func guardItems(text string) []string {
	var out []string
	in, fence := false, false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		if strings.HasPrefix(t, "#") {
			in = strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "#"))), "guards loosened")
			continue
		}
		if !in || t == "" {
			continue
		}
		top := len(line)-len(strings.TrimLeft(line, " \t")) < 2
		t = reNumbered.ReplaceAllString(t, "- ")
		b, isBullet := bullet(t)
		if top && isBullet || len(out) == 0 {
			if !isBullet {
				b = t
			}
			out = append(out, b)
			continue
		}
		out[len(out)-1] += " " + t
	}
	return out
}

func hasHeading(text, name string) bool {
	fence := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
		}
		if !fence && strings.HasPrefix(t, "#") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(t, "#"))), name) {
			return true
		}
	}
	return false
}

var reBacktick = regexp.MustCompile("`([^`]+)`")

// A proof line names its kind first, however it's formatted: "api: ...",
// "**API**: ...", "ui (Playwright): ...", "schema - ...".
var (
	reMarkup    = regexp.MustCompile("[*_`]")
	reProofKind = regexp.MustCompile(`^\s*(ui|api|schema)\b\s*(\([^)]*\))?\s*[:\-–—]`)
)

func short(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

func ReadDoc(t *Task, name string) (string, os.FileInfo, error) {
	p := filepath.Join(TaskDocsDir(t.Root, t.ID), name)
	b, err := os.ReadFile(p)
	if err != nil {
		return "", nil, err
	}
	fi, _ := os.Stat(p)
	return string(b), fi, nil
}
