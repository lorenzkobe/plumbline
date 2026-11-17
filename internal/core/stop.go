package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	if text, _, err := ReadDoc(t, DocPlan); err == nil {
		pc := CheckPlan(text)
		for _, tf := range pc.CaseTests {
			if _, err := os.Stat(filepath.Join(t.Root, filepath.FromSlash(tf))); err != nil {
				miss = append(miss, fmt.Sprintf("case test %s doesn't exist yet", tf))
			}
		}
	}
	for _, k := range t.ProofKinds {
		if waived(t, "proof:"+k, fp) {
			continue
		}
		r := LatestFor(runs, KindProof, k, fp)
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
	}
	return miss
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
	note := longConversationNote(in)
	if c.root == "" || c.paused || c.task == nil || c.task.Phase != PhaseBuilding {
		return noteOnly(note)
	}
	if endsWithQuestion(in.LastMessage) {
		return noteOnly(note)
	}
	t := c.task
	var miss []string
	if !c.setupOK {
		miss = []string{"setup: the project setup changed since the user confirmed it, so its commands can't be trusted (ask the user to re-confirm with /plumbline:setup)"}
	} else {
		miss = FinishMissing(t, c.setup)
	}
	if len(miss) == 0 {
		if !t.Reported {
			t.Reported = true
			t.Sendbacks = nil
			_ = SaveTask(t)
			c.log("finish", "proof-complete", "")
		}
		return noteOnly(note)
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
