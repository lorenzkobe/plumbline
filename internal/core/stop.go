package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// FinishMissing lists what the task still needs before Claude may say it's
// done: proof on the current code, the case list's tests, real-world proof,
// and a report written after the last change.
func FinishMissing(t *Task, s *Setup) []string {
	if s == nil {
		return []string{"setup: the project setup is missing (run /plumbline:setup)"}
	}
	miss := ProofMissing(t, s, true)
	fp, _ := Fingerprint(t.Root, s.IgnoreChanges)
	runs := LoadRuns(t.Root)
	report, fi, err := ReadDoc(t, DocReport)
	changes, derr := taskChanges(t, s.IgnoreChanges)
	if derr != nil {
		miss = append(miss, "diff: "+derr.Error()+" (the weakened-tests, debug-log and comment checks need it)")
	}
	var changed []string
	for _, f := range changes {
		changed = append(changed, f.Path)
	}
	miss = append(miss, caseRunMissing(t, s, fp, runs, report)...)
	miss = append(miss, bugRedMissing(t, s, fp, runs)...)
	miss = append(miss, e2eMissing(t, s, fp, runs, changed)...)
	miss = append(miss, debugMissing(changes)...)
	miss = append(miss, commentsMissing(t, changes, report)...)
	for _, k := range t.ProofKinds {
		if waived(t, "proof:"+k, fp) {
			continue
		}
		r := LatestFor(runs, t.ID, KindProof, k, "", fp)
		if v := Judge(r, nil); !v.OK {
			how := map[string]string{
				"ui":     "open the changed screen of the app on this laptop in the browser and take a screenshot after the last edit (it counts by itself), or click every changed button, link or tab with Playwright",
				"api":    "call the changed API and show the response proves the intended change (the body or the changed record), not only its code",
				"schema": "run a real query against the changed schema",
			}[k]
			miss = append(miss, fmt.Sprintf("real-world proof (%s): %s; %s, with: plumbline run proof %s -- <command>", k, v.Reason, how, k))
		}
	}
	var reportTime time.Time
	if err == nil {
		reportTime = fi.ModTime()
	}
	miss = append(miss, reviewMissing(t, s, fp, report, reportTime)...)
	if r := LatestFor(runs, t.ID, KindTest, "", s.Commands.Test, fp); r != nil && r.Rescued && report != "" &&
		!strings.Contains(strings.ToLower(strings.Join(Section(report, "for you"), "\n")), "only in parallel") {
		miss = append(miss, "report: the full suite passed only after tests that failed in parallel passed when run alone (its output names them); a timing bug in the change could hide there, so say so under \"For you\" (\"only in parallel\", with the test names)")
	}
	miss = append(miss, builderMissing(t)...)
	miss = append(miss, qaMissing(t, s, report)...)
	if s.JiraTicket(t.Ticket) {
		name := JiraCommentName(t.Ticket)
		p := filepath.Join(TaskDocsDir(t.Root, t.ID), name)
		if text, _, err := ReadDoc(t, name); err != nil {
			miss = append(miss, fmt.Sprintf("jira: write the comment for %s to %s: only the comment, in a few plain lines a teammate reads in seconds, ready for the user to copy and post (the suggested status goes in the report under \"For you\")", t.Ticket, p))
		} else if prob := JiraCommentProblems(text); len(prob) > 0 {
			miss = append(miss, fmt.Sprintf("jira: %s: %s", name, strings.Join(prob, "; ")))
		}
	}
	switch {
	case err != nil:
		miss = append(miss, "report: write it to "+filepath.Join(TaskDocsDir(t.Root, t.ID), DocReport))
	case fi.ModTime().Before(t.LastCodeChange):
		miss = append(miss, "report: the code changed after report.md was written; update it")
	default:
		var notListed []string
		for p := range t.OutsidePlan {
			if !gone(t.Root, p) && !t.BackToStart(p) && !strings.Contains(report, p) { // skip files created and removed again
				notListed = append(notListed, p)
			}
		}
		for p := range t.Held { // refused files are always reported
			if !strings.Contains(report, p) {
				notListed = append(notListed, p)
			}
		}
		if len(notListed) > 0 {
			sort.Strings(notListed)
			miss = append(miss, "report: list these under \"Outside the plan\" or \"For you\": "+strings.Join(notListed, ", "))
		}
		miss = append(miss, reportShapeMissing(t, report)...)
		miss = append(miss, hotfixReportMissing(t, report)...)
		miss = append(miss, verdictMissing(t, s, fp, report)...)
		miss = append(miss, weakenedMissing(changes, report, s)...)
		miss = append(miss, upgradeMissing(t, s, changes, report)...)
		miss = append(miss, qaTestsMissing(t, report)...)
	}
	return miss
}

// Two of the three verdicts (the third is "Not ready").
const (
	VerdictLimited = "Ready with noted limitations"
	VerdictReady   = "Ready"
)

var (
	reVerdictHeading = regexp.MustCompile(`(?im)^#+\s*verdict\s*:?\s*$`)
	reCommonHeading  = regexp.MustCompile(`(?im)^#+\s*common[- ]sense calls\s*:?\s*$`)
	// Longest first, so "Ready with noted limitations" isn't read as "Ready".
	reVerdictWord    = regexp.MustCompile(`(?i)^(?:verdict\s*:\s*)?(not ready|ready with noted limitations|ready)(?:[^a-z]|$)`)
	reRollback       = regexp.MustCompile(`(?i)^rollback\b`)
	reCouldntRun     = regexp.MustCompile(`(?i)\bcould(?:n['’]t| not) run\b`)
	reSkipped        = regexp.MustCompile(`(?i)\bskip(?:ped|s)?\b`)
	reSkippedCount   = regexp.MustCompile(`(?i)\b\d+\s+(?:tests?\s+)?skipped\b`) // a suite's count, not a check
	reSetAside       = regexp.MustCompile(`(?i)^set aside\b`)
	reShouldNotFixed = regexp.MustCompile(`(?i)\bshould[- ]fix(?:es)?\b[^\n]*\bnot fixed\b|\bnot fixed\b[^\n]*\bshould[- ]fix`)
	reLeadingMarkup  = regexp.MustCompile(`^[\s>*_#+-]+`)
	reInlineEmphasis = regexp.MustCompile("[*_]")
)

// sectionLines is Section with each line's indentation kept, so a list
// nested under a line can be told apart from the next top-level line.
func sectionLines(text, name string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			if in {
				break
			}
			in = strings.HasPrefix(strings.ToLower(strings.TrimSpace(strings.TrimLeft(trim, "#"))), strings.ToLower(name))
			continue
		}
		if in && trim != "" {
			out = append(out, strings.TrimRight(line, " \t\r"))
		}
	}
	return out
}

// plainLine: a report line without its bullet and bold or italic marks.
func plainLine(l string) string {
	return strings.TrimSpace(reInlineEmphasis.ReplaceAllString(reLeadingMarkup.ReplaceAllString(l, ""), ""))
}

// verdictMissing: the report's "## Verdict" (Ready, Ready with noted
// limitations or Not ready) with a rollback line, "couldn't run" checks
// each with the command to run later, no "skipped" checks, the
// common-sense calls, and the should-fix findings left unfixed.
func verdictMissing(t *Task, s *Setup, fp, report string) []string {
	var miss []string
	kinds := RequiredReviewers(t, s)
	verdict := ""
	lines := Section(report, "verdict")
	if !reVerdictHeading.MatchString(report) || len(lines) == 0 {
		miss = append(miss, `report: add a "## Verdict" section: its first line is Ready, Ready with noted limitations or Not ready, with why, then a "Rollback:" line (how to undo the change, or why it can't be undone)`)
	} else {
		if m := reVerdictWord.FindStringSubmatch(plainLine(lines[0])); m != nil {
			verdict = strings.ToLower(m[1])
		} else {
			miss = append(miss, fmt.Sprintf(`report: the verdict's first line must start with Ready, Ready with noted limitations or Not ready (it says %q)`, short(lines[0])))
		}
		rollback := false
		for _, l := range lines {
			rollback = rollback || reRollback.MatchString(plainLine(l))
		}
		if !rollback {
			miss = append(miss, `report: add a "Rollback:" line under the verdict: how to undo the change (revert the commit, the down migration), or why it can't be undone (data deleted, sent outside, a public API removed)`)
		}
	}

	var couldnt, noCommand, skipped []string
	exemptBelow := -1 // indent of a weakened-tests or waiver line whose sub-lines are exempt
	for _, raw := range sectionLines(report, "proof") {
		l := strings.TrimSpace(raw)
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		if exemptBelow >= 0 && indent <= exemptBelow {
			exemptBelow = -1
		}
		lower := strings.ToLower(plainLine(l))
		if reCouldntRun.MatchString(l) {
			couldnt = append(couldnt, l)
			if !reBacktick.MatchString(l) {
				noCommand = append(noCommand, short(plainLine(l)))
			}
			continue
		}
		// The weakened-tests line names tests someone skipped, and a waiver
		// is a check the user let go: both may say "skipped", and so may the
		// lines listed under them.
		if exemptBelow >= 0 {
			continue
		}
		if strings.HasPrefix(lower, "weakened") || strings.Contains(lower, "waive") {
			exemptBelow = indent
			continue
		}
		if reSkipped.MatchString(reSkippedCount.ReplaceAllString(l, "")) {
			skipped = append(skipped, short(plainLine(l)))
		}
	}
	if len(noCommand) > 0 {
		miss = append(miss, "report: each Proof line that says couldn't run needs the exact command to run later, in backticks: "+strings.Join(noCommand, "; "))
	}
	if len(skipped) > 0 {
		miss = append(miss, "report: a check that didn't run is an error, never \"skipped\": say \"couldn't run\" with why and the exact command to run later in backticks, or list its waiver: "+strings.Join(skipped, "; "))
	}

	if verdict == strings.ToLower(VerdictReady) {
		var why []string
		if len(couldnt) > 0 {
			why = append(why, "a check couldn't run")
		}
		for _, w := range t.Waivers {
			// In effect: it was given for this code, or it's the qa waiver,
			// which doesn't lapse (qaMissing).
			if w.Print == fp || w.Check == "qa" {
				why = append(why, "a check is waived ("+w.Check+")")
				break
			}
		}
		for _, k := range kinds {
			if last := t.lastFinished(k); last != nil && t.upheld(*last) {
				why = append(why, "a reviewer upheld a finding you disputed")
				break
			}
		}
		if len(why) > 0 {
			miss = append(miss, fmt.Sprintf("report: the verdict says Ready, but %s; say %q and list each limitation", strings.Join(why, ", "), VerdictLimited))
		}
	}

	if !reCommonHeading.MatchString(report) || len(Section(report, "common")) == 0 {
		miss = append(miss, `report: add a "## Common-sense calls" section: each call you made without asking (a test helper, formatting a file you touched, a generated file) and what it costs if it's wrong, or "- none"`)
	}

	// N4: a changed file the code reviewer didn't cover means the change
	// isn't known to be good.
	if last := t.lastFinished(HelperCodeReviewer); last != nil && contains(kinds, HelperCodeReviewer) && len(last.NotCovered) > 0 && verdict != "" && verdict != "not ready" {
		miss = append(miss, fmt.Sprintf("report: the code reviewer didn't cover %s, so the verdict is Not ready (or run the code reviewer again to cover them)", listSome(last.NotCovered, 8)))
	}
	// N6: what reviewers set aside as outside the plan gets the lead's call.
	var aside []string
	for _, k := range kinds {
		if last := t.lastFinished(k); last != nil {
			aside = append(aside, last.SetAside...)
		}
	}
	if len(aside) > 0 {
		found := false
		for _, l := range Section(report, "review") {
			found = found || reSetAside.MatchString(plainLine(strings.TrimSpace(l)))
		}
		if !found {
			miss = append(miss, fmt.Sprintf(`report: reviewers set aside %d thing(s) as outside the plan; add a "Set aside:" line under ## Review with your call on each (a follow-up, under For you, or not needed and why): %s`, len(aside), strings.Join(aside, "; ")))
		}
	}

	if anyShouldFix(t) {
		found := false
		for _, l := range Section(report, "review") {
			found = found || reShouldNotFixed.MatchString(l)
		}
		if !found {
			miss = append(miss, `report: a reviewer marked findings should-fix; add a "Should-fix not fixed:" line under ## Review listing each one you didn't fix and why, or "none"`)
		}
	}
	return miss
}

// The challenger's suggestions aren't marked, so it doesn't count.
func anyShouldFix(t *Task) bool {
	seen := map[string]bool{}
	for _, r := range t.Helpers {
		if r.Kind == HelperChallenger || isWorker(r.Kind) || r.Kind == HelperGuard || r.Kind == HelperArbiter || seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		last := t.lastCounted(r.Kind)
		if last == nil {
			last = t.lastFinished(r.Kind)
		}
		if last != nil {
			for _, item := range labelItems(t.findingsText(*last)) {
				if reShouldFixLabel.MatchString(item) {
					return true
				}
			}
		}
	}
	return false
}

// reGoalLine: the report's answer to the plan's goal.
var reGoalLine = regexp.MustCompile(`(?im)^[\s*_-]*goal\**:\**\s*\**(met|not met|partly met)\b`)

// reportShapeMissing: the report's "For you" section and every waiver the
// task was given, so nothing skipped or left for the user is hidden.
func reportShapeMissing(t *Task, report string) []string {
	var miss []string
	if !reForYouHeading.MatchString(report) || len(Section(report, "for you")) == 0 {
		miss = append(miss, `report: add a "## For you" section: anything that needs the user (refused files, packages, decisions, the commit question), or "- nothing"`)
	}
	if t.Goal != "" && !reGoalLine.MatchString(report) {
		miss = append(miss, `report: say whether the plan's goal is met, on a line under the verdict: "Goal: met" or "Goal: not met: <what's missing>" (the goal: `+t.Goal+`)`)
	}
	// Only the report's waiver lines count, so "e2e" said elsewhere in the
	// report doesn't list the e2e waiver.
	var waiverLines []string
	for _, l := range strings.Split(report, "\n") {
		if strings.Contains(strings.ToLower(l), "waive") {
			waiverLines = append(waiverLines, l)
		}
	}
	listed := strings.Join(waiverLines, "\n")
	var unlisted []string
	seen := map[string]bool{}
	for _, w := range t.Waivers {
		if !seen[w.Check] && !strings.Contains(listed, w.Check) {
			unlisted = append(unlisted, w.Check)
		}
		seen[w.Check] = true
	}
	if len(unlisted) > 0 {
		sort.Strings(unlisted)
		miss = append(miss, "report: list every waiver under \"Waivers\" in ## Proof, with why the check couldn't run: "+strings.Join(unlisted, ", "))
	}
	return miss
}

var (
	// A "For you" line in the closing message: at the start of a line, so
	// "ready for your review" or "I fixed it for you" don't count.
	reForYou = regexp.MustCompile(`(?im)^[\s>*_#-]*for you\b`)
	// The report's heading exactly: Section matches by prefix, and
	// "## For your information" isn't it.
	reForYouHeading = regexp.MustCompile(`(?im)^#+\s*for you\s*:?\s*$`)
)

// finalMessageMissing: the chat message that closes a proven task is the
// short report, so it needs a "For you" line the user can act on. When the
// report's "For you" holds something, that line opens the message (R5):
// what needs the user is the bad news that comes first.
func finalMessageMissing(m, report string) string {
	if !reForYou.MatchString(m) {
		return `final message: end the task with the short report in chat: one plain sentence on what changed (a failure first), the checks, the review and the goal in a line each, and "For you" with only what the user must know or decide. Keep it short: the details stay in report.md`
	}
	if forYouHasItems(report) && !reForYou.MatchString(firstLine(m)) {
		return `final message: the report's "For you" needs the user, so open the message with the "For you" line (what they must do or decide), then what changed, the proof, the review and the commit question`
	}
	return ""
}

// forYouHasItems: the report's "## For you" lists something besides the
// commit question, not just "nothing".
func forYouHasItems(report string) bool {
	for _, l := range Section(report, "for you") {
		p := strings.ToLower(strings.TrimRight(plainLine(l), ".!"))
		p = strings.TrimSpace(strings.TrimPrefix(p, "for you:"))
		if p == "" || p == "nothing" || p == "none" || strings.HasPrefix(p, "commit?") || strings.HasPrefix(p, "commit ") {
			continue
		}
		return true
	}
	return false
}

// firstLine: the message's first line with words on it.
func firstLine(m string) string {
	for _, l := range strings.Split(m, "\n") {
		if strings.TrimSpace(reLeadingMarkup.ReplaceAllString(l, "")) != "" {
			return l
		}
	}
	return ""
}

// gone: the file doesn't exist now and isn't in the last commit.
func gone(root, rel string) bool {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		return false
	}
	_, err := gitOut(root, "cat-file", "-e", "HEAD:"+rel)
	return err != nil
}

func waived(t *Task, check, fp string) bool {
	for _, w := range t.Waivers {
		if w.Check == check && w.Print == fp {
			return true
		}
	}
	return false
}

func endsWithQuestion(m string) bool {
	m = strings.TrimSpace(m)
	m = strings.TrimRight(m, "*_`) \n")
	return strings.HasSuffix(m, "?")
}

// Stop is the finish check. During building, "done" needs proof. A stop to
// ask the user something goes through. Claude is never sent back twice in a
// row for the same reason, and after two send-backs it stops and shows the
// user what's missing.
func Stop(in *HookInput) Output {
	c := newCtx(in)
	defer c.close()
	note := longConversationNote(in)
	c.endTurnBusy()
	if c.task != nil && c.task.Open() && in.TranscriptPath != "" {
		c.task.recordTokens("main:"+safeName(in.SessionID), in.TranscriptPath)
		_ = SaveTask(c.task)
	}
	if c.root == "" || c.paused || c.task == nil || c.task.Phase != PhaseBuilding {
		return noteOnly(note)
	}
	if c.task.HelperRunning() {
		return noteOnly(note) // waiting on a background helper is not "done"
	}
	t := c.task
	question := endsWithQuestion(in.LastMessage)
	if question && t.Reported {
		return noteOnly(note)
	}
	var miss []string
	if !c.setupOK {
		miss = []string{"setup: the project setup changed since the user confirmed it, so its commands can't be trusted (ask the user to re-confirm with /plumbline:setup)"}
	} else {
		miss = FinishMissing(t, c.setup)
	}
	if len(miss) > 0 && question {
		return noteOnly(note) // a stop to ask the user something goes through
	}
	if len(miss) == 0 {
		if t.Reported {
			return noteOnly(note)
		}
		// Just proven: the message that closes the task (usually ending with
		// the commit question) must be the short report. Reported resets
		// when the code changes, so a later fix is reported again.
		report, _, _ := ReadDoc(t, DocReport)
		if m := finalMessageMissing(in.LastMessage, report); m != "" {
			if n := len(t.Sendbacks); n > 0 && t.Sendbacks[n-1].Reason == firstRule([]string{m}) {
				// Sent back for this once already: it's the format, not the
				// proof, so don't hold the user up over it (or ask again on
				// every later turn).
				t.Reported = true
				t.Sendbacks = nil
				_ = SaveTask(t)
				c.log("finish", "proof-complete", "closing message without For you")
				return noteOnly(note)
			}
			miss = []string{m}
		} else {
			t.Reported = true
			t.Sendbacks = nil
			_ = SaveTask(t)
			c.log("finish", "proof-complete", "")
			return noteOnly(note)
		}
	}
	reason := strings.Join(miss, "; ")
	key := keyOf(miss)
	last := ""
	if n := len(t.Sendbacks); n > 0 {
		last = t.Sendbacks[n-1].Reason
	}
	if (in.StopHookActive && last == key) || len(t.Sendbacks) >= 2 {
		t.Sendbacks = nil
		_ = SaveTask(t)
		c.log("finish", "gave-up", reason)
		return Output{"systemMessage": msg("the task isn't proven yet, and Claude has been sent back already. Still missing: " + reason + ". Tell Claude how to go on (or /plumbline:pause if this check is wrong, then /plumbline:wrong).")}
	}
	t.Sendbacks = append(t.Sendbacks, Sendback{Reason: key, At: Now()})
	_ = SaveTask(t)
	c.log("sendback", firstRule(miss), reason)
	o := Context("Stop", msg("the task isn't done until it's proven. Still missing: "+reason+". Finish these, or if one can't be done, stop and ask the user (end your message with a question)."))
	return o
}

func keyOf(miss []string) string {
	keys := []string{}
	for _, m := range miss {
		keys = append(keys, firstRule([]string{m}))
	}
	return strings.Join(keys, ",")
}

func firstRule(miss []string) string {
	if len(miss) == 0 {
		return ""
	}
	m := miss[0]
	if i := strings.IndexAny(m, ":("); i > 0 {
		return strings.TrimSpace(m[:i])
	}
	return m
}

func noteOnly(note string) Output {
	if note == "" {
		return nil
	}
	return Output{"systemMessage": note}
}

// longConversationNote suggests a fresh session after a long conversation.
func longConversationNote(in *HookInput) string {
	if in.TranscriptPath == "" {
		return ""
	}
	fi, err := os.Stat(in.TranscriptPath)
	if err != nil || fi.Size() < 6<<20 {
		return ""
	}
	marker := filepath.Join(DataDir(), "sessions", safeName(in.SessionID)+".longnote")
	if _, err := os.Stat(marker); err == nil {
		return ""
	}
	_ = writeFileAtomic(marker, []byte("1"))
	return msg("this conversation is getting long. Once this task is wrapped up, a fresh session saves tokens; the task state carries over.")
}

// UncommittedWork: the task's own changes that aren't committed yet (CM1).
// Once the task is done no commit can be made for it, so finish waits for
// the commit. The user's own work, another session's files, what the setup
// ignores and the setup file itself don't count.
func UncommittedWork(t *Task, s *Setup) []string {
	dirty, err := DirtyFiles(t.Root)
	if err != nil {
		return nil
	}
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	others := t.OthersWork()
	var notes map[string]bool
	if t.CommandsOnly {
		var names []string
		for p := range dirty {
			names = append(names, p)
		}
		notes = untrackedNotes(t.Root, names)
	}
	var out []string
	for p := range dirty {
		if others[p] || MatchAny(ignore, p) || p == RepoSetupFile || notes[p] {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
