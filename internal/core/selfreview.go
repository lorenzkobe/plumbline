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
	if why := selfReviewContent(t, c.ignore()); why != "" {
		return Context("PostToolUse", msg(fmt.Sprintf("%s is recorded, but reviewers won't start yet: %s", DocSelfReview, why)))
	}
	return nil
}

func selfReviewTodo(t *Task) string {
	diff := "git diff HEAD"
	if t.StartCommit != "" {
		diff = "git diff " + t.StartCommit
	}
	return fmt.Sprintf("go through skills/task/review-checklist.md and the project's CLAUDE.md over your own diff (%s, plus new files), fix what you find, then write %s with the Write or Edit tool: `## Checked` (each changed file by its path, and what you checked in it) and `## Fixed` (what you fixed, or `- none`).", diff, selfReviewPath(t))
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
	if why := selfReviewContent(t, c.ignore()); why != "" {
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
	return ""
}

func selfReviewContent(t *Task, ignore []string) string {
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
