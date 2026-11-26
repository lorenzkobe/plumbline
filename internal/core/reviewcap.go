package core

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func (t *Task) reviewLimit(kind string) int {
	return MaxReviews + t.ReviewExtra[kind]
}

func isReviewCapQ(q question) bool {
	return strings.EqualFold(strings.TrimSpace(q.Header), "review cap")
}

// reviewCapAsk says how to ask the Review cap question. AskUserQuestion wants
// a description on each option, and Claude fills it unless told to leave it
// empty; a filled one is refused (it could say more than the click records).
const reviewCapAsk = `AskUserQuestion on its own, header "Review cap", options "Accept as is" and "One more round", each with "description": ""`

func reviewCapLabels(q question) bool {
	if len(q.Options) != 2 {
		return false
	}
	want := []string{"accept as is", "one more round"}
	for i, o := range q.Options {
		if !strings.EqualFold(strings.TrimSpace(o.Label), want[i]) {
			return false
		}
	}
	return true
}

func reviewCapPlain(q question) bool {
	if !reviewCapLabels(q) {
		return false
	}
	for _, o := range q.Options {
		if strings.TrimSpace(o.Description) != "" {
			return false
		}
	}
	return true
}

// reviewCapProblem names what keeps a Review cap question from being plain.
func reviewCapProblem(qs []question, q question) string {
	switch {
	case len(qs) != 1:
		return "make the Review cap question the only question in its AskUserQuestion call, so the user's click means only that: " + reviewCapAsk + "."
	case !reviewCapLabels(q):
		return `the Review cap question's options must be exactly "Accept as is" and "One more round", in that order: ` + reviewCapAsk + "."
	case !reviewCapPlain(q):
		return `leave each Review cap option's description empty ("description": ""): the labels say it all, and the user's click means only that.`
	}
	return ""
}

func reviewCapAnswer(answer string, q question) string {
	a := strings.TrimSpace(answer)
	for _, o := range q.Options {
		if l := strings.TrimSpace(o.Label); a == l {
			return strings.ToLower(l)
		}
	}
	return ""
}

func (c *hookCtx) recordReviewCap(q question) Output {
	t := c.task
	if c.in.AgentID != "" || t == nil || t.Phase != PhaseBuilding || !reviewCapPlain(q) || c.setup == nil {
		return nil
	}
	switch reviewCapAnswer(findAnswer(c.in.ToolResponse, q.Question), q) {
	case "accept as is":
		if !c.anyReviewerCapped() {
			return Context("PostToolUse", msg("no reviewer is out of rounds, so \"Accept as is\" records nothing; run the reviewers the finish check names."))
		}
		fp, err := Fingerprint(c.root, c.ignore())
		if err != nil || fp == "" {
			return Context("PostToolUse", msg("the user's \"Accept as is\" couldn't be recorded (Plumbline couldn't read the code's state). Ask again."))
		}
		t.ReviewCapOK = fp
		if err := SaveTask(t); err != nil {
			return Context("PostToolUse", msg("the user's \"Accept as is\" couldn't be saved ("+err.Error()+"). Ask again."))
		}
		c.log("review-cap", "accepted", "")
		return Context("PostToolUse", msg("the user accepted the reviews as they are for this code. Say so under ## Review in the report (\"accepted as is by you\"), with what no reviewer saw. A code change after this asks again."))
	case "one more round":
		var more []string
		fp, _ := Fingerprint(c.root, c.ignore())
		for _, k := range RequiredReviewers(t, c.setup) {
			last := t.lastFinished(k)
			current := last != nil && t.counts(*last) && fp != "" && last.PrintAfter == fp && !t.disputeOpen(k, *last)
			if len(t.helperRuns(k)) >= t.reviewLimit(k) && !current {
				if t.ReviewExtra == nil {
					t.ReviewExtra = map[string]int{}
				}
				t.ReviewExtra[k]++
				more = append(more, "plumbline:"+k)
			}
		}
		if len(more) == 0 {
			return Context("PostToolUse", msg("no reviewer is out of rounds with an out-of-date review, so \"One more round\" records nothing; run the reviewers the finish check names."))
		}
		t.ReviewCapOK = ""
		if err := SaveTask(t); err != nil {
			return Context("PostToolUse", msg("the user's \"One more round\" couldn't be saved ("+err.Error()+"). Ask again."))
		}
		c.log("review-cap", "one-more-round", strings.Join(more, ","))
		return Context("PostToolUse", msg("the user wants one more review round: each of these can run once more: "+strings.Join(more, ", ")+". First run `plumbline run checks` until the tests pass, then write your self-review for the current code, with ## Reach for what changed since the last review."))
	}
	return nil
}

func (c *hookCtx) anyReviewerCapped() bool {
	for _, k := range RequiredReviewers(c.task, c.setup) {
		if len(c.task.helperRuns(k)) >= c.task.reviewLimit(k) {
			return true
		}
	}
	return false
}

const (
	arbiterOff     = "off"
	arbiterDue     = "due"
	arbiterRunning = "running"
	arbiterCleared = "cleared"
	arbiterOpen    = "open"
	arbiterUsed    = "used"
	arbiterBlocked = "blocked"
)

// FixRound: a reviewer has a review that counts, so the checks run now are
// for a fix (0.8.8: lint and build then wait for --final).
func (t *Task) FixRound() bool {
	for _, k := range ReviewerKinds {
		if t.lastCounted(k) != nil {
			return true
		}
	}
	return false
}

func (t *Task) lastCounted(kind string) *HelperRun {
	runs := t.helperRuns(kind)
	for i := len(runs) - 1; i >= 0; i-- {
		if t.counts(runs[i]) {
			r := runs[i]
			return &r
		}
	}
	return nil
}

func reviewOutOfDate(t *Task, s *Setup, kind, fp string, last *HelperRun) bool {
	if last == nil || !t.counts(*last) {
		return true
	}
	return last.PrintAfter != fp && !((kind != HelperCodeReviewer || DocsOnly(t, s)) && onlyTestsOrDocs(t.Root, s, last.PrintAfter, fp)) && !cleanCarries(t, s, kind, last, fp)
}

func cappedKinds(t *Task, s *Setup, fp string) []string {
	var out []string
	for _, k := range RequiredReviewers(t, s) {
		runs := t.helperRuns(k)
		if len(runs) == 0 || runs[len(runs)-1].alive() || len(runs) < t.reviewLimit(k) {
			continue
		}
		last := t.lastFinished(k)
		if reviewOutOfDate(t, s, k, fp, last) || t.disputeOpen(k, *last) {
			out = append(out, k) // the code changed after its last review, or the lead disputes it
		}
	}
	return out
}

// disputedSince: a capped reviewer's disputes file changed after at, so a
// ruling made then doesn't answer it.
func disputedSince(t *Task, kind string, at time.Time) bool {
	fi, err := os.Stat(filepath.Join(TaskDocsDir(t.Root, t.ID), DisputesName(kind)))
	return err == nil && fi.Size() > 0 && !fi.ModTime().Before(at)
}

func openMustFix(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if item := reNumbered.ReplaceAllString(strings.TrimSpace(l), "- "); reMustFixLabel.MatchString(item) {
			out = append(out, item)
		}
	}
	return out
}

func arbiterState(t *Task, s *Setup, fp string) (string, *HelperRun) {
	if !s.PartOn(HelperArbiter) {
		return arbiterOff, nil
	}
	runs := t.helperRuns(HelperArbiter)
	if len(runs) > 0 && runs[len(runs)-1].alive() {
		return arbiterRunning, nil
	}
	if last := t.lastFinished(HelperArbiter); last != nil && fp != "" && last.PrintAfter == fp && t.counts(*last) && covers(last.Shown, cappedKinds(t, s, fp)) && !anyDisputedSince(t, s, fp, last.Started) {
		if len(openMustFix(t.findingsText(*last))) == 0 {
			return arbiterCleared, last
		}
		return arbiterOpen, last
	}
	if len(runs) >= MaxArbiterRuns {
		return arbiterUsed, nil
	}
	if fixesUnreadable(t, s, fp) != "" {
		return arbiterBlocked, nil
	}
	return arbiterDue, nil
}

func anyDisputedSince(t *Task, s *Setup, fp string, at time.Time) bool {
	for _, k := range cappedKinds(t, s, fp) {
		if disputedSince(t, k, at) {
			return true
		}
	}
	return false
}

func covers(shown, capped []string) bool {
	for _, k := range capped {
		if !contains(shown, k) {
			return false
		}
	}
	return true
}

func fixesUnreadable(t *Task, s *Setup, fp string) string {
	for _, k := range cappedKinds(t, s, fp) {
		last := t.lastCounted(k)
		if last == nil {
			return fmt.Sprintf("the %s has no review that counted, so there are no findings for the arbiter to check", k)
		}
		if _, err := treeChanges(t.Root, last.PrintAfter, fp); err != nil {
			return fmt.Sprintf("git couldn't list what changed since the %s's last counted review (%v)", k, err)
		}
	}
	return ""
}

func (c *hookCtx) arbiterStartProblem(fp string, err error) string {
	t := c.task
	if t.Phase != PhaseBuilding {
		return fmt.Sprintf("the arbiter rules on reviews that ran out of rounds, and task %s is at the %s step.", t.ID, t.Phase)
	}
	if err != nil || fp == "" {
		return "Plumbline couldn't read the code's state, so the arbiter's ruling couldn't be tied to this code. Try again."
	}
	capped := cappedKinds(t, c.setup, fp)
	if len(capped) == 0 {
		return "no reviewer is out of rounds with a review older than the code or findings the lead disputes, so there is nothing for the arbiter to rule on. Run the reviewers the finish check names."
	}
	switch st, _ := arbiterState(t, c.setup, fp); st {
	case arbiterRunning:
		return "the arbiter is already running; wait for its findings."
	case arbiterCleared, arbiterOpen:
		return "the arbiter already ruled on the code as it is now; the finish check says what comes next."
	case arbiterUsed:
		return fmt.Sprintf("the arbiter already ran %d times, the most a task gets. Ask the user: %s.", MaxArbiterRuns, reviewCapAsk)
	case arbiterBlocked:
		return fixesUnreadable(t, c.setup, fp) + ", so the arbiter can't be shown the fixes. Ask the user: " + reviewCapAsk + "."
	}
	return ""
}

func (c *hookCtx) arbiterStartNote(fp string) string {
	t := c.task
	docs := TaskDocsDir(t.Root, t.ID)
	var b strings.Builder
	b.WriteString(" These reviewers used all their runs, and the code changed after their last review or the lead disputes their findings. Decide only whether a must-fix is still open on the code as it is now:")
	for _, k := range cappedKinds(t, c.setup, fp) {
		last := t.lastCounted(k)
		if last == nil {
			continue
		}
		changed, _ := treeChanges(t.Root, last.PrintAfter, fp)
		if len(changed) == 0 {
			fmt.Fprintf(&b, " the %s's last counted findings are %s; the code hasn't changed since.", k, filepath.Join(docs, last.Findings))
		} else {
			fmt.Fprintf(&b, " the %s's last counted findings are %s; the fixes since are `git diff %s %s` (%s).", k, filepath.Join(docs, last.Findings), last.PrintAfter, fp, listSome(changed, 20))
		}
		if newer := t.lastFinished(k); newer != nil && newer.Findings != last.Findings && t.findingsExist(*newer) {
			fmt.Fprintf(&b, " Its newer run didn't count, but read its must-fix findings too: %s.", filepath.Join(docs, newer.Findings))
		}
		if docExists(t, DisputesName(k)) {
			fmt.Fprintf(&b, " The lead's disputes of them: %s; rule on each disputed must-fix (upheld: it stays open under Findings; withdrawn: under Checked and fine).", filepath.Join(docs, DisputesName(k)))
		}
	}
	fmt.Fprintf(&b, " In the notes folder read only %s, those findings and disputes files, and your own findings, never report.md or self-review.md. Under \"## Findings\" list each must-fix still open, or \"- none\".", DocPlan)
	return b.String()
}

func (c *hookCtx) reviewCapEarly() string {
	t := c.task
	if t == nil || t.Phase != PhaseBuilding || c.setup == nil {
		return ""
	}
	fp, err := Fingerprint(t.Root, c.ignore())
	if err != nil || fp == "" || t.ReviewCapOK == fp || len(cappedKinds(t, c.setup, fp)) == 0 {
		return ""
	}
	switch st, run := arbiterState(t, c.setup, fp); st {
	case arbiterDue:
		return "start plumbline:arbiter first (Agent tool): it checks the capped reviewers' open must-fix findings and the fixes since. The user is asked only if it finds one still open."
	case arbiterRunning:
		return "the arbiter is still running; wait for its findings. The user is asked only if it finds a must-fix still open."
	case arbiterCleared:
		return fmt.Sprintf("the arbiter found no must-fix open on this code (%s), so the capped reviews count and there is nothing to ask. Say so under ## Review in the report (\"cleared by the arbiter\").", run.Findings)
	}
	return ""
}

var specialistSignal = map[string]*regexp.Regexp{
	HelperSecurity: reSecuritySignal,
	HelperData:     reDataSignal,
	HelperUX:       reUXSignal,
}

// reachSince: a path that a self-review recorded after the specialist's
// review named in ## Reach matches the specialist's path signal.
func reachSince(sr *SelfReview, since time.Time, signal *regexp.Regexp) bool {
	for _, e := range sr.Reach {
		if !e.At.After(since) {
			continue
		}
		for _, p := range e.Paths {
			if signal.MatchString(reCamel.ReplaceAllString(p, "${1}_${2}")) {
				return true
			}
		}
	}
	return false
}

// cleanCarries: a specialist whose last counted review found nothing keeps
// it after a fix that stayed in what the other reviewers cited (B3, 0.8.3).
// Every file changed since its tree is a test file or one another
// reviewer's last counted findings cite (any reviewer kind, required or not), none decides which checks run or
// matches this specialist's own path signal, and nothing the fix adds calls
// for it by content. A review with should-fix findings and no must-fix
// carries the same way when self-review.md is current and no ## Reach
// recorded since the review names a file of its kind (0.8.8). Any git
// error means it doesn't carry.
func cleanCarries(t *Task, s *Setup, kind string, last *HelperRun, fp string) bool {
	signal, specialist := specialistSignal[kind]
	if !t.newReviewRules() || !specialist || last == nil || !t.counts(*last) || t.disputeOpen(kind, *last) || last.PrintAfter == "" || fp == "" || last.PrintAfter == fp {
		return false
	}
	items := findingItems(t.findingsText(*last), "findings")
	if len(items) == 0 {
		return false
	}
	clean := true
	for _, it := range items {
		switch {
		case strictNone(it):
		case reShouldFixLabel.MatchString(it):
			clean = false
		default:
			return false // a must-fix, or an unlabeled finding ("None of the handlers check...")
		}
	}
	// With should-fix findings, the fix's reach decides (0.8.8): the current
	// self-review's ## Reach names what else the fix touches, and any of it
	// of this specialist's kind brings it back.
	if !clean && (!SelfReviewCurrent(t, s, fp) || reachSince(t.SelfReview, last.Finished, signal) ||
		reachSince(&SelfReview{Reach: []ReachEntry{{At: Now(), Paths: reachPaths(readText(selfReviewPath(t)))}}}, last.Finished, signal)) {
		return false
	}
	changed, err := treeChanges(t.Root, last.PrintAfter, fp)
	if err != nil || len(changed) == 0 {
		return false
	}
	cited := map[string]bool{}
	for _, k := range ReviewerKinds { // every reviewer's findings, without recomputing which are required (its git diffs)
		if k == kind {
			continue
		}
		if other := t.lastCounted(k); other != nil {
			for _, r := range Refs(strings.Join(Section(t.findingsText(*other), "findings"), "\n")) {
				if !isNotesDoc(r.Path) {
					cited[foldPath(path.Clean(filepath.ToSlash(r.Path)))] = true
				}
			}
		}
	}
	for _, p := range changed {
		if GateFile(p) {
			return false
		}
		if TestFile(p, s) {
			continue
		}
		if !cited[foldPath(p)] || signal.MatchString(reCamel.ReplaceAllString(filepath.ToSlash(p), "${1}_${2}")) {
			return false
		}
	}
	diffs, err := changesBetween(t.Root, last.PrintAfter, fp)
	if err != nil {
		return false
	}
	return !contains(ContentReviewers(diffs, s), kind)
}
