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
	caseTests := t.CaseTests
	if !t.CasesStored { // approved before 0.3.1
		if text, _, err := ReadDoc(t, DocPlan); err == nil {
			caseTests = CheckPlan(text).CaseTests
		}
	}
	for _, tf := range caseTests {
		if _, err := os.Stat(filepath.Join(t.Root, filepath.FromSlash(tf))); err != nil {
			miss = append(miss, fmt.Sprintf("case test %s doesn't exist yet", tf))
		}
	}
	for _, k := range t.ProofKinds {
		if waived(t, "proof:"+k, fp) {
			continue
		}
		r := LatestFor(runs, t.ID, KindProof, k, "", fp)
		if v := Judge(r, nil); !v.OK {
			how := map[string]string{
				"ui":     "click every changed button, link or tab in the real app (Playwright)",
				"api":    "call the changed API and show the response code",
				"schema": "run a real query against the changed schema",
			}[k]
			miss = append(miss, fmt.Sprintf("real-world proof (%s): %s; %s, with: plumbline run proof %s -- <command>", k, v.Reason, how, k))
		}
	}
	report, fi, err := ReadDoc(t, DocReport)
	var reportTime time.Time
	if err == nil {
		reportTime = fi.ModTime()
	}
	miss = append(miss, reviewMissing(t, s, fp, report, reportTime)...)
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
			if !gone(t.Root, p) && !strings.Contains(report, p) { // skip files created and removed again
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
	}
	return miss
}

// reportShapeMissing: the report's "For you" section and every waiver the
// task was given, so nothing skipped or left for the user is hidden.
func reportShapeMissing(t *Task, report string) []string {
	var miss []string
	if !reForYouHeading.MatchString(report) || len(Section(report, "for you")) == 0 {
		miss = append(miss, `report: add a "## For you" section: anything that needs the user (refused files, packages, decisions, the commit question), or "- nothing"`)
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
// short report, so it needs a "For you" line the user can act on.
func finalMessageMissing(m string) string {
	if reForYou.MatchString(m) {
		return ""
	}
	return `final message: end the task with the short report in chat: bad news first, what changed, the proof, the review, and a "For you" line (or "For you: nothing"), then the commit question`
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
		if m := finalMessageMissing(in.LastMessage); m != "" {
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
