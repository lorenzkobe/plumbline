package core

import (
	"os"
	"strings"
	"testing"
)

// Claude Code keeps hook context over about 10,000 characters in a file and
// shows Claude a 2 KB preview (CTX1). The limit is per hook.
const hookContextLimit = 9500

// CTX1: each session-start part fits, even with a pile of other sessions'
// open tasks, and "Right now" comes before the rules.
func TestSessionPartsFit(t *testing.T) {
	h := newHarness(t)
	h.setup()
	long := strings.Repeat("a long task title ", 8)
	for i := 0; i < 9; i++ {
		tk := &Task{ID: NewID(), Title: long, Size: "normal", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now(), SessionID: "other-" + string(rune('a'+i))}
		must(t, os.MkdirAll(TaskDocsDir(h.repo, tk.ID), 0o700))
		must(t, SaveTask(tk))
	}
	in := &HookInput{SessionID: h.sid, Cwd: h.repo, Source: "compact"}
	first := contextOf(SessionStart(in))
	if n := len(first); n > hookContextLimit {
		t.Fatalf("session start is %d characters, over %d", n, hookContextLimit)
	}
	now, rules := strings.Index(first, "## Right now"), strings.Index(first, "# Plumbline is on")
	if now < 0 || rules < 0 || now > rules {
		t.Fatalf("Right now should come before the rules: now at %d, rules at %d", now, rules)
	}
	if !strings.Contains(first, "and 4 more (plumbline task list)") {
		t.Fatalf("nine open tasks should name five and count the rest: %s", first[:now+1500])
	}
	for _, part := range []string{"task", "finish"} {
		got := contextOf(SessionRules(in, part))
		if got == "" {
			t.Fatalf("part %s is empty", part)
		}
		if len(got) > hookContextLimit {
			t.Fatalf("part %s is %d characters, over %d", part, len(got), hookContextLimit)
		}
	}
	// Every rule still goes out: the three parts hold the whole text.
	for _, want := range []string{"## Core rules", "## Checkpoints", "## Helpers", "## Finishing", "## Jira"} {
		if !strings.Contains(CoreRules(), want) {
			t.Fatalf("the rules lack %q", want)
		}
	}
}

// Outside a git repo there are no tasks, so parts 2 and 3 stay out.
func TestSessionRulesNeedARepo(t *testing.T) {
	in := &HookInput{SessionID: "s", Cwd: t.TempDir(), Source: "startup"}
	if o := SessionRules(in, "task"); o != nil {
		t.Fatalf("no repo, no task rules: %v", o)
	}
	if o := SessionRules(in, "nope"); o != nil {
		t.Fatalf("unknown part: %v", o)
	}
}
