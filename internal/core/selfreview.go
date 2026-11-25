package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const DocSelfReview = "self-review.md"

type SelfReview struct {
	Print string    `json:"print"`
	Hash  string    `json:"hash"`
	At    time.Time `json:"at"`
}

func selfReviewPath(t *Task) string {
	return filepath.Join(TaskDocsDir(t.Root, t.ID), DocSelfReview)
}

func (c *hookCtx) afterSelfReview(abs string) Output {
	t := c.task
	if t == nil || c.in.AgentID != "" || t.Phase != PhaseBuilding {
		return nil
	}
	p := selfReviewPath(t)
	if foldPath(abs) != foldPath(Abs(p, "/")) {
		return nil
	}
	if _, err := os.Lstat(p); err != nil || !plainFile(p) {
		return Context("PostToolUse", msg(fmt.Sprintf("%s isn't recorded: it must be a plain file (no symlink, no second hard link). Remove it and write it again with the Write tool.", DocSelfReview)))
	}
	h := fileHash(p)
	fp, err := Fingerprint(t.Root, c.ignore())
	if h == "" || err != nil || fp == "" {
		return Context("PostToolUse", msg(fmt.Sprintf("%s isn't recorded: Plumbline couldn't read it or the code's state (%v). Write it again.", DocSelfReview, err)))
	}
	t.SelfReview = &SelfReview{Print: fp, Hash: h, At: Now()}
	if err := SaveTask(t); err != nil {
		return Context("PostToolUse", msg(fmt.Sprintf("%s isn't recorded: the task couldn't be saved (%v). Write it again.", DocSelfReview, err)))
	}
	why := selfReviewContent(t, c.setup)
	if why == "" {
		why = reachProblem(t, c.setup, readText(p))
	}
	if why != "" {
		return Context("PostToolUse", msg(fmt.Sprintf("%s is recorded, but reviewers won't start yet: %s", DocSelfReview, why)))
	}
	return nil
}

func selfReviewTodo(t *Task) string {
	diff := "git diff HEAD"
	if t.StartCommit != "" {
		diff = "git diff " + t.StartCommit
	}
	return fmt.Sprintf("go through skills/task/review-checklist.md and the project's CLAUDE.md over your own diff (%s, plus new files), fix what you find, then write %s with the Write or Edit tool: `## Checked` (each changed file by its path, and what you checked in it), `## Fixed` (what you fixed, or `- none`), and on a fix round `## Reach` (each code file changed since the last review, with what else calls or shows the changed code, checked).", diff, selfReviewPath(t))
}

func (c *hookCtx) selfReviewProblem(fp string, fpErr error) string {
	t := c.task
	p := selfReviewPath(t)
	if _, err := os.Lstat(p); err != nil {
		if os.IsNotExist(err) {
			return "before any reviewer run, review your own change: " + selfReviewTodo(t)
		}
		return fmt.Sprintf("%s couldn't be read (%v); write it again with the Write tool.", DocSelfReview, err)
	}
	if !plainFile(p) {
		return fmt.Sprintf("%s is a symlink or has a second hard link, so it may not be the file you wrote. Remove it and write it again with the Write tool.", DocSelfReview)
	}
	sr := t.SelfReview
	if sr == nil || sr.Hash != fileHash(p) {
		return fmt.Sprintf("%s isn't recorded, or changed after it was: only the Write or Edit tool while building records it (a terminal write, or one before the plan was approved, isn't). Write it again with the Write or Edit tool.", DocSelfReview)
	}
	if why := selfReviewContent(t, c.setup); why != "" {
		return why
	}
	if fpErr != nil || fp == "" {
		return fmt.Sprintf("Plumbline couldn't read the code's state (%v), so it can't tell whether %s is current. Try again; the reviewers wait until it can.", fpErr, DocSelfReview)
	}
	if sr.Print != fp {
		changed, err := treeChanges(t.Root, sr.Print, fp)
		what := "files it couldn't list"
		if err == nil && len(changed) > 0 {
			what = listFiles(changed)
		}
		return fmt.Sprintf("the code changed after %s was written (%s). Go through the checklist over that change and update %s with the Write or Edit tool.", DocSelfReview, what, DocSelfReview)
	}
	return reachProblem(t, c.setup, readText(p))
}

// testsReadyProblem: why a reviewer can't start yet because the tests haven't
// passed on the code as it is now, or "". A reviewer reading red code spends
// its run on failures the lead's own run would show. The full suite counts,
// or a related run whose files cover every changed file. Without a test
// command, or on a docs- or commands-only task, the commit proof decides.
func testsReadyProblem(t *Task, s *Setup, fp string) string {
	if s == nil || s.Commands.Test == "" || CommandsOnly(t, s) || DocsOnly(t, s) {
		return ""
	}
	const run = "run `plumbline run checks` and fix what fails, then start the reviewers"
	runs := LoadRuns(t.Root)
	base := BaselineFor(runs, t.ID, KindTest)
	fullRun := LatestFor(runs, t.ID, KindTest, "", s.Commands.Test, fp)
	full := Judge(fullRun, base)
	if full.OK {
		return ""
	}
	if fullRun != nil {
		// A full suite that failed on this code isn't outweighed by a
		// narrower related run that passed.
		return fmt.Sprintf("reviewers start once the tests pass on the code as it is now (tests on the current code: the full suite %s): %s.", full.Reason, run)
	}
	template := savedCommand(s, KindRelated)
	var rel *Run
	if template != "" {
		rel = LatestFor(runs, t.ID, KindRelated, "", template, fp)
	}
	v := Judge(rel, base)
	if rel == nil || !v.OK {
		why := full.Reason
		if rel != nil {
			why = v.Reason
		}
		return fmt.Sprintf("reviewers start once the tests pass on the code as it is now (tests on the current code: %s): %s.", why, run)
	}
	if !strings.Contains(template, "{files}") {
		return ""
	}
	files, err := reviewedFilesErr(t, s.IgnoreChanges)
	if err != nil {
		return fmt.Sprintf("Plumbline couldn't list the task's changed files (%v), so it can't tell whether the related tests covered them. Try again; the reviewers wait until it can.", err)
	}
	ran := map[string]bool{}
	for _, f := range rel.Files {
		ran[foldPath(f)] = true
	}
	var missing []string
	for _, f := range files {
		if !ran[foldPath(f)] {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("the last related-tests run on this code wasn't given these changed files: %s. Reviewers start once the tests for every changed file pass: %s.", listFiles(missing), run)
	}
	return ""
}

// lastReviewPrint: the code the newest counted reviewer run read, or "" when
// no reviewer run counted yet.
func (t *Task) lastReviewPrint() string {
	var last *HelperRun
	for _, k := range ReviewerKinds {
		runs := t.helperRuns(k)
		for i := len(runs) - 1; i >= 0; i-- {
			if r := runs[i]; t.counts(r) {
				if last == nil || r.Finished.After(last.Finished) {
					last = &r
				}
				break
			}
		}
	}
	if last == nil {
		return ""
	}
	return last.PrintAfter
}

// reachFiles: on a fix round, the files changed since the last counted
// review that need a ## Reach line: code, not tests or prose (a gate file
// always needs one), as onlyTestsOrDocs splits them.
// When git can't read the last review's code (a pruned tree), every file the
// task changed needs one: stricter, and never a dead end.
func reachFiles(t *Task, s *Setup, fp string) ([]string, error) {
	last := t.lastReviewPrint()
	if last == "" || fp == "" || last == fp {
		return nil, nil
	}
	changed, err := treeChanges(t.Root, last, fp)
	if err != nil {
		if changed, err = changedSince(t.Root, t.StartCommit); err != nil {
			return nil, err
		}
	}
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	others := t.OthersWork()
	var out []string
	for _, p := range changed {
		if others[p] || MatchAny(ignore, p) {
			continue
		}
		if !GateFile(p) && (TestFile(p, s) || docFile(p)) {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func selfReviewContent(t *Task, s *Setup) string {
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	b, err := os.ReadFile(selfReviewPath(t))
	if err != nil {
		return fmt.Sprintf("%s couldn't be read (%v).", DocSelfReview, err)
	}
	text := string(b)
	var empty []string
	for _, s := range []string{"Checked", "Fixed"} {
		if !hasBullet(Section(text, s)) {
			empty = append(empty, "## "+s)
		}
	}
	if len(empty) > 0 {
		return fmt.Sprintf("%s needs %s, each with at least one bullet (`- none` is fine under ## Fixed).", DocSelfReview, strings.Join(empty, " and "))
	}
	files, err := reviewedFilesErr(t, ignore)
	if err != nil {
		return fmt.Sprintf("Plumbline couldn't list the task's changed files (%v), so it can't tell whether %s covers them. Try again; the reviewers wait until it can.", err, DocSelfReview)
	}
	checked := strings.Join(Section(text, "Checked"), "\n")
	var missing []string
	for _, f := range files {
		if !namesPath(checked, f) {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("%s doesn't name these changed files, so they weren't self-reviewed: %s. Go through the checklist over them and add each to ## Checked.", DocSelfReview, listFiles(missing))
	}
	return ""
}

// reachProblem: on a fix round, what self-review.md's ## Reach still lacks.
func reachProblem(t *Task, s *Setup, text string) string {
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	fp, err := Fingerprint(t.Root, ignore)
	if err != nil || fp == "" {
		return fmt.Sprintf("Plumbline couldn't read the code's state (%v), so it can't tell what changed since the last review. Try again; the reviewers wait until it can.", err)
	}
	reach, err := reachFiles(t, s, fp)
	if err != nil {
		return fmt.Sprintf("git couldn't list what changed since the last review (%v), so Plumbline can't tell what %s's ## Reach must cover. Try again; the reviewers wait until it can.", err, DocSelfReview)
	}
	if len(reach) == 0 {
		return ""
	}
	lines := Section(text, "Reach")
	body := strings.Join(lines, "\n")
	var unnamed []string
	for _, f := range reach {
		if !namesPath(body, f) {
			unnamed = append(unnamed, f)
		}
	}
	if !hasBullet(lines) || len(unnamed) > 0 {
		if len(unnamed) == 0 {
			unnamed = reach
		}
		return fmt.Sprintf("this is a fix round, and a fix most often breaks what else uses the code it changed: %s needs `## Reach` with a bullet for each file changed since the last review (missing: %s), saying what else calls or shows the changed code (other callers, the lead and subagents, other messages and refusals) and that you checked each.", DocSelfReview, listFiles(unnamed))
	}
	return ""
}

func namesPath(text, path string) bool {
	re := regexp.MustCompile("(^|[\\s(\\[`'\"])" + regexp.QuoteMeta(path) + "($|[\\s)\\]:,;`'\"]|\\.(\\s|$))")
	return re.MatchString(text)
}

func hasBullet(lines []string) bool {
	for _, l := range lines {
		if _, ok := bullet(l); ok {
			return true
		}
	}
	return false
}

const reviewerStartFor = 2 * time.Minute

type ReviewerStart struct {
	Kind  string    `json:"kind"`
	Print string    `json:"print"`
	At    time.Time `json:"at"`
}

func (t *Task) allowReviewerStart(kind, print string) {
	var keep []ReviewerStart
	for _, s := range t.ReviewerStart {
		if Now().Sub(s.At) <= reviewerStartFor {
			keep = append(keep, s)
		}
	}
	t.ReviewerStart = append(keep, ReviewerStart{Kind: kind, Print: print, At: Now()})
}

func (t *Task) takeReviewerStart(kind, print string) bool {
	for i, s := range t.ReviewerStart {
		if s.Kind == kind && print != "" && s.Print == print && Now().Sub(s.At) <= reviewerStartFor {
			t.ReviewerStart = append(t.ReviewerStart[:i], t.ReviewerStart[i+1:]...)
			return true
		}
	}
	return false
}

func readText(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}
