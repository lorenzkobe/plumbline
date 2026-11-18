package core

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// A design task (plumbline task start --design) agrees the look before any
// real screen is built: the brand kit, at least 3 mockup directions, the
// user's pick on the question prompt, then the plan for the real screens.
const (
	KindDesign     = "design"
	DocDesign      = "design.md"
	MinDirections  = 3
	directionWord  = "direction"
	designPartName = "design"
)

// designTask: the task is a design task and the design part is on.
func (c *hookCtx) designTask() bool {
	return c.task != nil && c.task.Kind == KindDesign && c.setup.PartOn(designPartName)
}

// isDirectionQ: a question whose header (or text) starts with "Direction"
// is the user's pick of a mockup direction.
func isDirectionQ(q question) bool {
	for _, s := range []string{q.Header, q.Question} {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), directionWord) {
			return true
		}
	}
	return false
}

// directionOptions are the options that name a direction ("Direction B:
// calm and editorial"); others ("Make more", "Change something") aren't a pick.
func directionOptions(q question) []string {
	var out []string
	for _, o := range q.Options {
		l := strings.TrimSpace(o.Label)
		if strings.HasPrefix(strings.ToLower(l), directionWord) {
			out = append(out, l)
		}
	}
	return out
}

// directionKey is the short name of a picked direction: "Direction B" from
// "Direction B: calm and editorial". plan.md's ## Design names it.
func directionKey(label string) string {
	k := strings.TrimSpace(label)
	for _, sep := range []string{":", " - ", " – ", " — ", "(", ","} {
		if i := strings.Index(k, sep); i > 0 {
			k = k[:i]
		}
	}
	return strings.TrimSpace(k)
}

// opensWith: the line, without list marks and emphasis, starts with the
// phrase as whole words.
func opensWith(line, phrase string) bool {
	l := strings.TrimSpace(reMarkup.ReplaceAllString(line, ""))
	l = strings.TrimSpace(strings.TrimLeft(l, "-+ "))
	if len(l) < len(phrase) || !strings.EqualFold(l[:len(phrase)], phrase) {
		return false
	}
	rest := []rune(l[len(phrase):])
	return len(rest) == 0 || !(unicode.IsLetter(rest[0]) || unicode.IsDigit(rest[0]))
}

// designDocProblems: design.md holds the brand kit (or the one agreed for a
// new project) and at least 3 directions, each one a bullet with its mockup.
func designDocProblems(t *Task) []string {
	text, _, err := ReadDoc(t, DocDesign)
	if err != nil {
		return []string{fmt.Sprintf("write %s first: \"## Brand kit\" (where it is, or the one agreed with the user) and \"## Directions\" (at least %d, each a bullet with its mockup link)", filepath.Join(TaskDocsDir(t.Root, t.ID), DocDesign), MinDirections)}
	}
	var p []string
	if !hasContent(Section(text, "brand kit")) {
		p = append(p, `design.md needs a "## Brand kit" section: where the kit is, or the one agreed with the user for a new project`)
	}
	n := 0
	for _, l := range Section(text, "directions") {
		if _, ok := bullet(l); ok {
			n++
		}
	}
	if n < MinDirections {
		p = append(p, fmt.Sprintf(`design.md's "## Directions" lists %d; make at least %d different directions as mockups, each a bullet with its link`, n, MinDirections))
	}
	return p
}

func hasContent(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return true
		}
	}
	return false
}

// gateDirection checks a "Direction" question before it's shown.
func (c *hookCtx) gateDirection(q question, answers map[string]any) Output {
	if !c.designTask() {
		return nil // an ordinary question that happens to start with "Direction"
	}
	if len(answers) > 0 {
		return c.deny("self-approval", "the direction question can't come pre-answered. Only the user picks a direction.")
	}
	if c.task.Phase == PhaseBuilding {
		return c.deny("design-direction", "the plan is already approved. To change the direction, run plumbline task replan, ask the direction again, then the checkpoint.")
	}
	opts := directionOptions(q)
	if len(opts) < 2 {
		return c.deny("design-direction", `the direction question needs an option per direction, each label starting with "Direction" (for example "Direction A: bold and dense"), so the pick is one of them.`)
	}
	if p := designDocProblems(c.task); len(p) > 0 {
		return c.deny("design-direction", "the directions aren't ready to pick from: "+strings.Join(p, "; ")+".")
	}
	text, _, _ := ReadDoc(c.task, DocDesign)
	listed := strings.Join(Section(text, "directions"), "\n")
	for _, o := range opts {
		if !mentionsWord(listed, directionKey(o)) {
			return c.deny("design-direction", fmt.Sprintf(`%q isn't one of the directions in design.md's "## Directions"; every option must be one of them, with its mockup.`, directionKey(o)))
		}
	}
	return nil
}

// isPick: in a design task, a "Direction" question with direction options
// is the user's pick; one without them is an ordinary question.
func isPick(q question) bool { return isDirectionQ(q) && len(directionOptions(q)) > 0 }

// recordDirection stores the user's pick from the question's answer. Only
// an answer that is exactly one of the direction options counts.
func (c *hookCtx) recordDirection(q question) Output {
	if !c.designTask() || c.task.Phase == PhaseBuilding {
		return nil
	}
	ans := strings.TrimSpace(findAnswer(c.in.ToolResponse, q.Question))
	for _, o := range directionOptions(q) {
		if ans == o {
			c.task.Direction = o
			c.task.DirectionAt = Now()
			_ = SaveTask(c.task)
			c.log("design", "direction-picked", "")
			return Context("PostToolUse", msg(fmt.Sprintf("the user picked %q. Refine it with them if they asked for changes, then write plan.md with a \"## Design\" section naming %q and the mockup, and ask the checkpoint.", o, directionKey(o))))
		}
	}
	if ans == "" {
		return Context("PostToolUse", msg("couldn't read a direction pick, so none is recorded. If the user did pick one, tell them Plumbline didn't see it (run with PLUMBLINE_DEBUG=1 to capture the answer shape)."))
	}
	return Context("PostToolUse", msg("no direction was picked. Work on what the user asked (more directions, changes), then ask again."))
}

// designProblems: what the checkpoint needs on top of the usual for a
// design task.
func (c *hookCtx) designProblems(planText string, pc PlanCheck) []string {
	if !c.designTask() {
		return nil
	}
	t := c.task
	p := designDocProblems(t)
	if t.Direction == "" {
		p = append(p, `the user hasn't picked a design direction: show the mockups, then ask with AskUserQuestion, header "Direction", one option per direction (labels starting "Direction")`)
		return p
	}
	key := directionKey(t.Direction)
	design := Section(planText, "design")
	switch {
	case !hasContent(design):
		p = append(p, fmt.Sprintf(`plan.md needs a "## Design" section naming the picked direction (%s) and its mockup`, key))
	case !opensWith(design[0], key):
		// The first line is the pick, so a mention ("not Direction B")
		// further on can't stand in for it.
		p = append(p, fmt.Sprintf(`plan.md's "## Design" must start with the direction the user picked (%s), as in "%s (mockup link): ..."`, key, key))
	}
	if !contains(pc.ProofKinds, "ui") {
		p = append(p, `a design task needs "- ui: ..." under "## Real-world proof": screenshots of the real screens at phone, tablet and desktop sizes compared to the mockup`)
	}
	return p
}

// designReminder: once per normal task, when the lead's plan.md first
// names screen files, a reminder that a design task mocks them up first
// (D1). A reminder, not a gate: a small visual fix needs no mockups.
func (c *hookCtx) designReminder(abs string) Output {
	t := c.task
	if t == nil || c.in.AgentID != "" || t.Kind == KindDesign || t.DesignNoted || t.Phase == PhaseBuilding || !c.setup.PartOn(designPartName) {
		return nil
	}
	if foldPath(abs) != foldPath(Abs(filepath.Join(TaskDocsDir(t.Root, t.ID), DocPlan), "/")) {
		return nil
	}
	plan, _, err := ReadDoc(t, DocPlan)
	if err != nil {
		return nil
	}
	var screens []string
	for _, f := range CheckPlan(plan).Files {
		if reUXSignal.MatchString(f) && !TestFile(f, c.setup) && !strings.HasSuffix(f, "/") {
			screens = append(screens, f)
		}
	}
	if len(screens) == 0 {
		return nil
	}
	t.DesignNoted = true
	_ = SaveTask(t)
	c.log("design", "design-reminder", "")
	return Context("PostToolUse", msg(fmt.Sprintf("the plan changes screens (%s). If what users see changes in a way they'd want to choose, say so at the checkpoint: a design task (/plumbline:design) mocks up %d directions first. For a small visual fix, say in the plan why no mockups are needed.", listSome(screens, 5), MinDirections)))
}
