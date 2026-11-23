package core

import "strings"

func (t *Task) reviewLimit(kind string) int {
	return MaxReviews + t.ReviewExtra[kind]
}

func isReviewCapQ(q question) bool {
	return strings.EqualFold(strings.TrimSpace(q.Header), "review cap")
}

func reviewCapPlain(q question) bool {
	if len(q.Options) != 2 {
		return false
	}
	want := []string{"accept as is", "one more round"}
	for i, o := range q.Options {
		if !strings.EqualFold(strings.TrimSpace(o.Label), want[i]) || strings.TrimSpace(o.Description) != "" {
			return false
		}
	}
	return true
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
	if pre, ok := c.in.Input()["answers"].(map[string]any); ok && len(pre) > 0 {
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
		return Context("PostToolUse", msg("the user wants one more review round: each of these can run once more: "+strings.Join(more, ", ")+". Write your self-review for the current code first."))
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
