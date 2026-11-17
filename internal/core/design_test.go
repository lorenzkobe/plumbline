package core

import (
	"path/filepath"
	"strings"
	"testing"
)

const designDoc = "# Design\n## Brand kit\n- docs/brand.md\n## Directions\n- Direction A: bold, https://claude.ai/artifact/a\n- Direction B: calm, https://claude.ai/artifact/b\n- Direction C: dense, https://claude.ai/artifact/c\n"

const designPlan = "# Plan\n## Files\n- src/a.go\n## Cases\n- [normal] list shows -> test: test.sh\n## Real-world proof\n- ui: screenshots at 360, 768 and 1280 px against the mockup\n## Needs your OK\n- none\n## Design\nDirection B, mockup https://claude.ai/artifact/b\n"

// designTaskH: a small design task, grounded, ready for the directions.
func designTaskH(t *testing.T) *harness {
	h := groundedTask(t, "small")
	tk := h.task()
	tk.Kind = KindDesign
	must(t, SaveTask(tk))
	return h
}

func directionQ(labels ...string) map[string]any {
	var opts []any
	for _, l := range labels {
		opts = append(opts, map[string]any{"label": l})
	}
	return map[string]any{"questions": []any{map[string]any{"question": "Which look?", "header": "Direction", "options": opts}}}
}

func (h *harness) pick(q map[string]any, answer string) Output {
	h.t.Helper()
	return h.post("AskUserQuestion", q, map[string]any{"answers": map[string]any{"Which look?": answer}})
}

func TestDesignDirectionBeforeCheckpoint(t *testing.T) {
	h := designTaskH(t)
	q := directionQ("Direction A: bold", "Direction B: calm", "Direction C: dense", "Make more")

	// No mockups listed yet: nothing to pick from.
	h.expect(h.pre("AskUserQuestion", q), "deny", "design.md")
	h.docs(DocDesign, "# Design\n## Brand kit\n- docs/brand.md\n## Directions\n- Direction A\n- Direction B\n")
	h.expect(h.pre("AskUserQuestion", q), "deny", "at least 3")
	h.docs(DocDesign, "# Design\n## Directions\n- A\n- B\n- C\n")
	h.expect(h.pre("AskUserQuestion", q), "deny", "Brand kit")
	h.docs(DocDesign, designDoc)

	// The pick must be one of the directions in design.md, asked fresh and alone.
	h.expect(h.pre("AskUserQuestion", directionQ("Direction A: bold", "Make more")), "deny", `starting with "Direction"`)
	h.expect(h.pre("AskUserQuestion", directionQ("Direction A: bold", "Direction D: new")), "deny", `"Direction D" isn't one of the directions`)
	bundled := map[string]any{"questions": append(directionQ("Direction A", "Direction B")["questions"].([]any),
		map[string]any{"question": "Anything else?", "header": "Notes", "options": []any{map[string]any{"label": "No"}, map[string]any{"label": "Yes"}}})}
	h.expect(h.pre("AskUserQuestion", bundled), "deny", "on its own")
	// An ordinary question that starts with "Direction" is just a question.
	h.expect(h.pre("AskUserQuestion", directionQ("Left", "Right")), "", "")
	pre := directionQ("Direction A", "Direction B")
	pre["answers"] = map[string]any{"Which look?": "Direction B"}
	h.expect(h.pre("AskUserQuestion", pre), "deny", "pre-answered")
	both := map[string]any{"questions": append(directionQ("Direction A", "Direction B")["questions"].([]any), checkpointQ()["questions"].([]any)...)}
	h.expect(h.pre("AskUserQuestion", both), "deny", "one checkpoint or direction question")
	h.expect(h.pre("AskUserQuestion", q), "", "")

	// The plan is ready, but no direction is picked: the checkpoint waits.
	h.docs(DocPlan, designPlan)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "hasn't picked a design direction")

	// Free text and "Make more" aren't a pick.
	h.pick(q, "B, but darker")
	h.pick(q, "Make more")
	if h.task().Direction != "" {
		t.Fatalf("a non-option answer was recorded as a pick: %q", h.task().Direction)
	}
	h.pick(q, "Direction A: bold")
	if h.task().Direction != "Direction A: bold" {
		t.Fatalf("pick not recorded: %q", h.task().Direction)
	}
	// plan.md names B, the user picked A.
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "(Direction A)")
	// The user changes their mind: the latest pick counts.
	h.pick(q, "Direction B: calm")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "", "")
	h.approve()

	// After approval, the direction changes only through a replan.
	h.expect(h.pre("AskUserQuestion", q), "deny", "replan")
	h.pick(q, "Direction C: dense")
	if h.task().Direction != "Direction B: calm" {
		t.Fatal("a pick after approval must not change the approved direction")
	}
}

func TestDesignPlanNeedsDesignSectionAndUIProof(t *testing.T) {
	h := designTaskH(t)
	h.docs(DocDesign, designDoc)
	q := directionQ("Direction A: bold", "Direction B: calm", "Direction C: dense")
	h.pick(q, "Direction B: calm")

	h.docs(DocPlan, goodPlan)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", `"## Design" section`)
	h.docs(DocPlan, goodPlan+"## Design\nDirection B\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "ui: ...")
	// The pick is named as whole words: "Direction 1" isn't "Direction 12",
	// and "Direction B" isn't "direction but".
	h.docs(DocPlan, strings.Replace(designPlan, "Direction B, mockup", "Keep the direction but use Direction BX, mockup", 1))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "must name the direction the user picked")

	// design.md emptied after the pick: the checkpoint notices.
	h.docs(DocPlan, designPlan)
	h.docs(DocDesign, "# Design\n")
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "Brand kit")
	h.docs(DocDesign, designDoc)
	h.approve()
}

func TestDesignApprovalRechecksAtAnswer(t *testing.T) {
	h := designTaskH(t)
	h.docs(DocDesign, designDoc)
	q := directionQ("Direction A: bold", "Direction B: calm", "Direction C: dense")
	h.pick(q, "Direction B: calm")
	h.docs(DocPlan, designPlan)
	cq := checkpointQ()
	h.expect(h.pre("AskUserQuestion", cq), "", "")
	// Between the question and the answer, plan.md's Design section goes.
	write(t, filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan), goodPlan)
	h.post("AskUserQuestion", cq, map[string]any{"answers": map[string]any{"Go?": "Approve"}})
	if h.task().Phase == PhaseBuilding {
		t.Fatal("approval recorded although the design checks no longer pass")
	}
}

func TestDirectionQuestionOutsideDesign(t *testing.T) {
	// An ordinary task: a question that starts with "Direction" is just a question.
	h := groundedTask(t, "small")
	q := directionQ("Left", "Right")
	h.expect(h.pre("AskUserQuestion", q), "", "")
	h.pick(q, "Left")
	if h.task().Direction != "" {
		t.Fatal("a normal task recorded a direction")
	}
	h.docs(DocPlan, goodPlan)
	h.approve()

	// The design part switched off: a design task works like a normal one.
	g := designTaskH(t)
	s, _, err := LoadSetup(g.repo)
	must(t, err)
	s.Parts = map[string]bool{"design": false}
	_, err = SaveSetup(g.repo, s)
	must(t, err)
	g.docs(DocPlan, goodPlan)
	g.approve()
}

func TestDirectionKey(t *testing.T) {
	for in, want := range map[string]string{
		"Direction B: calm and editorial": "Direction B",
		"Direction A - bold":              "Direction A",
		"Direction C (dense)":             "Direction C",
		"Direction 2":                     "Direction 2",
		"  Direction X, airy ":            "Direction X",
	} {
		if got := directionKey(in); got != want {
			t.Errorf("directionKey(%q) = %q, want %q", in, got, want)
		}
	}
}
