package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Checks on what the challenger and the reviewers write, run when they stop
// (N3 to N6), and on what the lead tells a reviewer when it starts one (R3).
// A helper whose findings don't pass is sent back once; a run that still
// doesn't pass is recorded with its problems and doesn't count as a review.

const (
	// A quote counts within this many lines of the cited line.
	quoteWindow = 3
	// Shorter backticked text ("err", "x") would match almost anywhere.
	minQuote = 6
)

// findingItems: the bullets of a section, each with the lines wrapped under
// it joined on.
func findingItems(text, section string) []string {
	return items(Section(text, section))
}

// bullets: every bullet of a whole file, headings aside.
func bullets(text string) []string {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(l), "#") {
			lines = append(lines, l)
		}
	}
	var out []string
	for _, it := range items(lines) {
		if strings.HasPrefix(it, "- ") || strings.HasPrefix(it, "* ") {
			out = append(out, it)
		}
	}
	return out
}

// items groups lines into bullets, each with the lines wrapped under it;
// itemBreak ends the one before (a heading in between).
func items(lines []string) []string {
	var out []string
	fresh := true
	for _, l := range lines {
		if l == itemBreak {
			fresh = true
			continue
		}
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || fresh {
			out = append(out, t)
			fresh = false
			continue
		}
		out[len(out)-1] += " " + t
	}
	return out
}

const itemBreak = "\x00"

func normSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// isNotesDoc: a citation of the task's plan.md or understanding.md, which
// live in the notes folder, not the repo.
func isNotesDoc(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	return clean == DocPlan || clean == DocUnderstanding
}

// none: an item that says the section is empty.
func none(item string) bool {
	t := strings.ToLower(strings.Trim(plainLine(item), " .:"))
	return t == "none" || t == "nothing" || strings.HasPrefix(t, "none,") || strings.HasPrefix(t, "none ") || strings.HasPrefix(t, "nothing ")
}

// refTexts: the cited file's text now and at the task's start (a finding may
// cite a line the change removed). The notes folder's plan.md and
// understanding.md can be cited by name.
func (t *Task) refTexts(path string) []string {
	var out []string
	clean := filepath.ToSlash(filepath.Clean(path))
	if isNotesDoc(clean) {
		if b, err := os.ReadFile(filepath.Join(TaskDocsDir(t.Root, t.ID), clean)); err == nil {
			out = append(out, string(b))
		}
		return out
	}
	if strings.HasPrefix(clean, "../") || filepath.IsAbs(path) {
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(t.Root, filepath.FromSlash(clean))); err == nil {
		out = append(out, string(b))
	}
	base := t.StartCommit
	if base == "" {
		base = "HEAD"
	}
	if s, err := gitOut(t.Root, "show", base+":"+clean); err == nil {
		out = append(out, s)
	}
	return out
}

// quoteProblem: why an item's file:line citations aren't backed by a quote
// of the code there, or "" when one is (N3). Go checks the quote, not the
// judgement: a backticked piece of the cited line, within a few lines of it.
func (t *Task) quoteProblem(item string) string {
	refs := Refs(item)
	if len(refs) == 0 {
		return ""
	}
	var quotes []string
	for _, m := range reBacktick.FindAllStringSubmatch(item, -1) {
		q := normSpace(m[1])
		if len(q) >= minQuote && len(Refs(" "+q)) == 0 {
			quotes = append(quotes, q)
		}
	}
	if len(quotes) == 0 {
		return fmt.Sprintf("%s:%d has no quote: copy a few words of the cited line in backticks", refs[0].Path, refs[0].Line)
	}
	found := false
	for _, r := range refs {
		texts := t.refTexts(r.Path)
		found = found || len(texts) > 0
		for _, text := range texts {
			lines := strings.Split(text, "\n")
			end := r.Line
			if r.End > end {
				end = r.End
			}
			lo, hi := max(r.Line-quoteWindow, 1), min(end+quoteWindow, len(lines))
			if lo > hi {
				continue
			}
			window := normSpace(strings.Join(lines[lo-1:hi], "\n"))
			for _, q := range quotes {
				if strings.Contains(window, q) {
					return ""
				}
			}
		}
	}
	if !found {
		return fmt.Sprintf("%s: no such file in the repo, now or at the task's start (cite it from the repo root)", refs[0].Path)
	}
	return fmt.Sprintf("%s:%d: the quote isn't within %d lines of the cited line; open the file and cite the line the quote is on", refs[0].Path, refs[0].Line, quoteWindow)
}

// Coverage lines: "- path: covered" or "- path: not covered, why".
var reCoverage = regexp.MustCompile("^[-*]\\s+`?([^`\\s]+?)`?\\s*:\\s*(not covered|covered)\\b")

// coverage reads the code reviewer's "## Coverage" section.
func coverage(text string) map[string]bool {
	out := map[string]bool{}
	for _, item := range findingItems(text, "coverage") {
		if m := reCoverage.FindStringSubmatch(strings.ToLower(item)); m != nil {
			// The path keeps its case: read it again from the item.
			path := strings.Trim(strings.Fields(strings.TrimLeft(item, "-* "))[0], "`:")
			out[filepath.ToSlash(path)] = m[2] == "covered"
		}
	}
	return out
}

// reviewedFiles: what the task changed, for the code reviewer's coverage:
// every changed or new file since the start, without the user's own work,
// other sessions' files and what the setup ignores.
func reviewedFiles(t *Task, ignore []string) []string {
	var out []string
	others := t.OthersWork()
	for _, p := range ChangedSince(t.Root, t.StartCommit) {
		if others[p] || MatchAny(ignore, p) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// findingsProblems checks a finished challenger or reviewer run's findings
// file and records what it read from it on the run.
func (c *hookCtx) findingsProblems(run *HelperRun) []string {
	t := c.task
	text := t.findingsText(*run)
	if strings.TrimSpace(text) == "" {
		return nil // the missing file has its own message
	}
	var p []string
	// N3: a cited line comes with a quote of it, wherever it is in the file
	// (a finding outside "## Findings" is still a finding). Coverage lines
	// name whole files.
	var cited []string
	inCoverage := false
	for _, l := range strings.Split(text, "\n") {
		if h := strings.TrimSpace(l); strings.HasPrefix(h, "#") {
			inCoverage = strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(h, "#"))), "coverage")
			cited = append(cited, itemBreak)
			continue
		}
		if !inCoverage {
			cited = append(cited, l)
		}
	}
	for _, item := range items(cited) {
		if why := t.quoteProblem(item); why != "" && !strings.HasPrefix(strings.ToLower(plainLine(item)), "withdrawn") {
			p = append(p, "finding "+why)
		}
	}
	if !isReviewer(run.Kind) {
		return p
	}
	// N5: a withdrawn finding names the code that refutes it, and the files
	// the lead's disputes cite were opened in this run.
	withdrew := false
	for _, item := range findingItems(text, "disputes") {
		if !strings.HasPrefix(strings.ToLower(plainLine(item)), "withdrawn") {
			continue
		}
		withdrew = true
		if len(Refs(item)) == 0 {
			p = append(p, fmt.Sprintf("withdrawn %q: cite the code that refutes it (file:line with a quote); \"the tests pass\" isn't evidence", short(plainLine(item))))
		} else if why := t.quoteProblem(item); why != "" {
			p = append(p, "withdrawal "+why)
		}
	}
	if withdrew {
		disputes, _ := os.ReadFile(filepath.Join(TaskDocsDir(t.Root, t.ID), DisputesName(run.Kind)))
		var unopened []string
		for _, r := range Refs(string(disputes)) {
			abs := Abs(filepath.Join(t.Root, filepath.FromSlash(r.Path)), "/")
			if isNotesDoc(r.Path) {
				abs = Abs(filepath.Join(TaskDocsDir(t.Root, t.ID), filepath.Base(r.Path)), "/")
			}
			if !contains(run.Opened, abs) && !contains(unopened, r.Path) {
				unopened = append(unopened, r.Path)
			}
		}
		if len(unopened) > 0 {
			p = append(p, "before withdrawing, open each file the lead's disputes cite and check the guard is really there: "+strings.Join(unopened, ", "))
		}
	}
	// N6: what was set aside as outside the plan, one line each.
	run.SetAside = nil
	if len(Section(text, "set aside")) == 0 {
		p = append(p, `add a "## Set aside" section: each thing you noticed but left alone because it's outside the plan, one line each, or "- none"`)
	}
	for _, item := range findingItems(text, "set aside") {
		if !none(item) {
			run.SetAside = append(run.SetAside, short(plainLine(item)))
		}
	}
	// N4: the code reviewer says, for every changed file, whether it was
	// reviewed.
	if run.Kind == HelperCodeReviewer {
		cov := coverage(text)
		var missing []string
		run.NotCovered = nil
		for _, f := range reviewedFiles(t, c.ignore()) {
			covered, listed := cov[f]
			switch {
			case !listed:
				missing = append(missing, f)
				run.NotCovered = append(run.NotCovered, f)
			case !covered:
				run.NotCovered = append(run.NotCovered, f)
			}
		}
		if len(missing) > 0 {
			p = append(p, fmt.Sprintf(`"## Coverage" must list every changed file as "- <path>: covered" or "- <path>: not covered, <why>"; missing: %s`, listSome(missing, 10)))
		}
	}
	return p
}

// --- R3: what the lead tells an independent helper ---

// Phrases that tell a reviewer what to find or skip. The lead says what to
// review; the verdict is the reviewer's.
var reSteering = regexp.MustCompile(`(?i)\b(do not|don't|dont|no need to) (flag|re-?check|(report|mention|raise|review) (it|this|that|these|those|the|any))\b|\balready (fixed|reviewed|checked)\b|\bis a false positive\b|\bnot a real (issue|problem|bug)\b`)

// shingle: runs of this many words, at least minShared characters long,
// shared with a file the helper may not read, mean the lead pasted from it.
const (
	shingleWords = 5
	minShared    = 24
)

func shingles(text string) map[string]bool {
	// Words without their punctuation: "x->a" and "x -> a" are the same
	// words, so a phrase the plan holds counts as the plan's (IND1), and
	// changed punctuation doesn't hide a pasted finding.
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := map[string]bool{}
	for i := 0; i+shingleWords <= len(words); i++ {
		s := strings.Join(words[i:i+shingleWords], " ")
		if len(s) >= minShared {
			out[s] = true
		}
	}
	return out
}

// promptProblem: why the lead's prompt for an independent helper (a
// reviewer, blind QA) can't go as written, or "". It mustn't carry text
// from the notes the helper may not read (other reviewers' findings, the
// report, builders' notes) or tell the helper what to find.
func (c *hookCtx) promptProblem(kind, prompt string) string {
	t := c.task
	if t == nil || !independent(kind) || strings.TrimSpace(prompt) == "" {
		return ""
	}
	if m := reSteering.FindString(prompt); m != "" {
		return fmt.Sprintf("the prompt tells the %s what to find or skip (%q). Say what to review (the task, the plan, the diff base); what it finds is its call, and disputes go in %s after it reports.", kind, m, DisputesName(kind))
	}
	docs := TaskDocsDir(t.Root, t.ID)
	// Text the helper may read anyway doesn't count: the plan, the
	// understanding, the task title, and its own earlier findings.
	allowed := map[string]bool{DocPlan: true, DocUnderstanding: true, DisputesName(kind): true}
	for _, r := range t.helperRuns(kind) {
		allowed[r.Findings] = true
	}
	free := shingles(t.Title)
	// Folder paths aren't findings: the repo's, the notes folder's and the
	// builder copies' paths show up in every helper's notes (REV2).
	paths := []string{t.Root, docs, filepath.Join(t.Root, ".claude", "worktrees")}
	for _, r := range t.Helpers {
		if r.Worktree != "" {
			paths = append(paths, r.Worktree)
		}
	}
	// So are the changed files' names: the reviewer sees them in the diff
	// anyway (RL2).
	paths = append(paths, ChangedSince(t.Root, t.StartCommit)...)
	for _, p := range paths {
		for s := range shingles(p) {
			free[s] = true
		}
	}
	for name := range allowed {
		if b, err := os.ReadFile(filepath.Join(docs, name)); err == nil {
			for s := range shingles(string(b)) {
				free[s] = true
			}
		}
	}
	mine := shingles(prompt)
	for s := range free {
		delete(mine, s)
	}
	if len(mine) == 0 {
		return ""
	}
	entries, _ := os.ReadDir(docs)
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") && !allowed[e.Name()] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(docs, name))
		if err != nil {
			continue
		}
		for s := range shingles(string(b)) {
			if mine[s] {
				return fmt.Sprintf("the prompt repeats text from %s (%q), which the %s must not see: each review stays independent. Give it the task and the plan, not other helpers' notes or the report. If the user added to the task since the plan, write that into plan.md first; the reviewer may read it there.", name, s, kind)
			}
		}
	}
	return ""
}
