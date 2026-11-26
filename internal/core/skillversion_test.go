package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOldSkillNoteOncePerSession(t *testing.T) {
	newHarness(t)
	old := Version
	defer func() { Version = old }()
	t.Setenv("CLAUDE_PLUGIN_ROOT", "/cache/plumbline/plumbline/0.8.8")
	path := "/cache/plumbline/plumbline/0.8.4/skills/task/checkpoints.md"

	Version = "0.8.8"
	note := oldSkillNote("s1", path)
	for _, want := range []string{"this is Plumbline 0.8.4's skill text, but the hooks are 0.8.8", filepath.Join("/cache/plumbline/plumbline/0.8.8", "skills", "task", "SKILL.md")} {
		if !strings.Contains(note, want) {
			t.Errorf("note %q lacks %q", note, want)
		}
	}
	if oldSkillNote("s1", path) != "" {
		t.Error("a second note in the same session")
	}
	if oldSkillNote("s2", "/cache/plumbline/plumbline/0.8.8/skills/task/SKILL.md") != "" {
		t.Error("the current version needs no note")
	}
	if oldSkillNote("s2", "/repo/skills/task/SKILL.md") != "" {
		t.Error("a path outside the plugin cache needs no note")
	}
	Version = "dev"
	if oldSkillNote("s3", path) != "" {
		t.Error("a dev build adds no note")
	}
	Version = ""
	if oldSkillNote("s3", path) != "" {
		t.Error("no version adds no note")
	}
}

func TestStatsByHelperKindFromEvents(t *testing.T) {
	newHarness(t)
	LogEvent(Event{Task: "t1", Type: "block", Rule: "helper-read-only", Helper: "code-reviewer"})
	LogEvent(Event{Task: "t1", Type: "repeat-block", Rule: "helper-read-only", Helper: "code-reviewer"})
	LogEvent(Event{Task: "t1", Type: "helper", Rule: "code-reviewer-start", Helper: "code-reviewer"})
	LogEvent(Event{Task: "t1", Type: "helper", Rule: "code-reviewer-resume", Helper: "code-reviewer"})
	LogEvent(Event{Task: "t2", Type: "helper", Rule: "code-reviewer-start", Helper: "code-reviewer"})
	LogEvent(Event{Task: "t2", Type: "helper", Rule: "plan-reviewer-start", Helper: "plan-reviewer"})
	LogEvent(Event{Task: "t2", Type: "helper", Rule: "builder-start", Helper: "builder"})
	tc := TokenCount{Input: 3000}
	LogEvent(Event{Task: "t2", Type: "tokens", Tokens: &tc, HelperTokens: map[string]TokenCount{"code-reviewer": {Input: 1200}, "other": {Input: 7}}})
	out := Stats(1)
	for _, want := range []string{
		"Blocks by helper kind: code-reviewer 1",
		"Repeat blocks by helper kind: code-reviewer 1",
		"code-reviewer: 2 runs in 2 of 2 tasks",
		"plan-reviewer: 1 run in 1 of 2 tasks",
		"Tokens by helper kind: code-reviewer 1,200, other 7",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stats lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "builder:") {
		t.Errorf("only reviewers get a runs line:\n%s", out)
	}
}

func TestLogTokensByHelperKind(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	tk := h.task()
	tk.Tokens = map[string]TokenCount{
		"agent:" + id:     {Input: 100},
		"agent:unknown-1": {Input: 7},
		"main:s":          {Input: 5},
	}
	must(t, SaveTask(tk))
	LogTokens(tk, "proj")
	out := Stats(1)
	if !strings.Contains(out, "Tokens by helper kind: code-reviewer 100, other 7") {
		t.Errorf("tokens by kind:\n%s", out)
	}
}
