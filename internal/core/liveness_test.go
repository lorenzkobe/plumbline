package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	tlPrompt      = `{"type":"user","message":{"role":"user","content":"Write the tests from the plan."}}`
	tlAttachment  = `{"type":"attachment","attachment":{"type":"hook_success","hookName":"PostToolUse:Bash"}}`
	tlRequest     = `{"type":"api-request","id":"r1"}`
	tlThinking    = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"","signature":"x"}]}}`
	tlToolUse     = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`
	tlToolResult  = `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":"a.go"}]}}`
	tlAnswer      = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done: tests in src/a_test.go."}]}}`
	tlInterrupt   = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	tlInterruptTU = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
)

func writeTranscript(t *testing.T, path string, wrote time.Time, lines ...string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Dir(path), 0o755))
	must(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	must(t, os.Chtimes(path, wrote, wrote))
}

func TestTranscriptState(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct {
		name  string
		lines []string
		want  transcriptState
	}{
		{"request in flight", []string{tlPrompt, tlRequest}, transcriptWorking},
		{"thinking", []string{tlPrompt, tlRequest, tlThinking}, transcriptWorking},
		{"tool call waiting", []string{tlPrompt, tlRequest, tlToolUse}, transcriptWorking},
		{"result then hook note", []string{tlToolUse, tlToolResult, tlAttachment}, transcriptWorking},
		{"final answer", []string{tlToolResult, tlRequest, tlThinking, tlAnswer, tlAttachment}, transcriptAnswered},
		{"interrupted", []string{tlToolResult, tlAttachment, tlRequest, tlInterrupt}, transcriptInterrupted},
		{"interrupted on a tool", []string{tlToolUse, tlToolResult, tlInterruptTU}, transcriptInterrupted},
		{"interrupt then work again", []string{tlInterruptTU, tlRequest}, transcriptWorking},
		{"only attachments", []string{tlAttachment, tlAttachment}, transcriptUnknown},
		{"garbage", []string{"not json", `{"type":"user","message":{"content":42}}`}, transcriptUnknown},
	} {
		p := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "-")+".jsonl")
		writeTranscript(t, p, time.Now(), c.lines...)
		if _, got := readTranscriptState(p); got != c.want {
			t.Errorf("%s: state %d, want %d", c.name, got, c.want)
		}
	}
	if w, s := readTranscriptState(filepath.Join(dir, "missing.jsonl")); !w.IsZero() || s != transcriptUnknown {
		t.Error("a missing transcript is unknown")
	}
	if _, s := readTranscriptState(""); s != transcriptUnknown {
		t.Error("no path is unknown")
	}
	p := filepath.Join(dir, "big.jsonl")
	big := `{"type":"user","message":{"content":"` + strings.Repeat("x", transcriptTailMax) + `"}}`
	writeTranscript(t, p, time.Now(), big, tlRequest, tlAnswer)
	if _, s := readTranscriptState(p); s != transcriptAnswered {
		t.Errorf("tail of a big transcript: %d", s)
	}
}

func TestHelperTranscriptPath(t *testing.T) {
	lead := "/h/.claude/projects/p/abc.jsonl"
	if got := helperTranscriptPath(lead, "a1"); got != "/h/.claude/projects/p/abc/subagents/agent-a1.jsonl" {
		t.Errorf("from the lead's: %s", got)
	}
	own := "/h/.claude/projects/p/abc/subagents/agent-a1.jsonl"
	if got := helperTranscriptPath(own, "a1"); got != own {
		t.Errorf("the helper's own: %s", got)
	}
	for _, c := range [][2]string{{"", "a1"}, {lead, ""}, {lead, "../x"}} {
		if helperTranscriptPath(c[0], c[1]) != "" {
			t.Errorf("%q %q has no transcript", c[0], c[1])
		}
	}
}

func TestHelperAliveFromTranscript(t *testing.T) {
	t.Setenv("PLUMBLINE_HOME", filepath.Join(t.TempDir(), "data"))
	real := Now
	defer func() { Now = real }()
	base := real()
	Now = func() time.Time { return base }
	tr := filepath.Join(t.TempDir(), "agent-q.jsonl")
	run := HelperRun{Kind: HelperQA, AgentID: "q", Session: "s1", Transcript: tr, Started: base.Add(-5 * time.Hour)}
	check := func(name string, quiet time.Duration, want bool, lines ...string) {
		t.Helper()
		writeTranscript(t, tr, base.Add(-quiet), lines...)
		if got := run.alive(); got != want {
			t.Errorf("%s (quiet %s): alive %v, want %v", name, quiet, got, want)
		}
	}
	check("thinking through one long reply", 40*time.Minute, true, tlPrompt, tlRequest)
	check("waiting on a permission prompt", 90*time.Minute, true, tlRequest, tlToolUse)
	check("about to send the next request", 40*time.Minute, true, tlToolUse, tlToolResult, tlAttachment)
	check("mid-step past the cap: the process died", helperStuck+time.Minute, false, tlRequest)
	check("answered, then quiet", 40*time.Minute, false, tlRequest, tlAnswer)
	check("answered moments ago: the stop may still come", time.Minute, true, tlRequest, tlAnswer)
	check("cancelled", 2*time.Minute, false, tlRequest, tlInterrupt)
	check("cancelled seconds ago: it may go on", 10*time.Second, true, tlRequest, tlInterruptTU)
	check("writing", time.Minute, true, tlToolUse)

	check("old start, fresh write", 2*time.Minute, true, tlToolResult)

	write(t, sessionEndedPath("s1"), base.Add(-time.Minute).Format(time.RFC3339)+"\n")
	check("session ended", time.Minute, false, tlRequest)
	write(t, sessionEndedPath("s1"), base.Add(-6*time.Hour).Format(time.RFC3339)+"\n")
	check("ended before it started", time.Minute, true, tlRequest)

	sessionStarted("s1", "compact")
	check("compacted session", time.Minute, true, tlRequest)
	if SessionEnded("s1") {
		t.Error("a session that starts again isn't ended")
	}
	sessionStarted("s1", "resume")
	check("restarted after it started", time.Minute, false, tlRequest)
	run.Started = base.Add(time.Second)
	check("started after the restart", time.Minute, true, tlRequest)

	run.Finished = base
	check("stopped", time.Minute, false, tlRequest)

	run = HelperRun{Kind: HelperQA, Started: base.Add(-time.Hour), LastSeen: base.Add(-10 * time.Minute)}
	if !run.alive() {
		t.Error("no transcript, a tool call 10 minutes ago: alive")
	}
	run.LastSeen = base.Add(-helperAlive - time.Minute)
	if run.alive() {
		t.Error("no transcript, quiet past helperAlive: dead")
	}
}

func TestQAThinkingLongStillHolds(t *testing.T) {
	h := bigTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	lead := filepath.Join(t.TempDir(), "lead.jsonl")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:qa", "prompt": "go"}), "", "")
	id := "qa-long"
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: id, AgentType: "plumbline:qa", TranscriptPath: lead})
	r := h.run(HelperQA, id)
	tr := helperTranscriptPath(lead, id)
	if r.Session != h.sid || r.Transcript != tr {
		t.Fatalf("run records session %q and transcript %q", r.Session, r.Transcript)
	}
	tk := h.task()
	tk.runByAgent(id).Started = Now().Add(-time.Hour)
	tk.runByAgent(id).LastSeen = Now().Add(-40 * time.Minute)
	must(t, SaveTask(tk))

	writeTranscript(t, tr, time.Now().Add(-40*time.Minute), tlPrompt, tlRequest)
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "blind QA is writing tests")

	writeTranscript(t, tr, time.Now().Add(-2*time.Minute), tlPrompt, tlRequest, tlInterrupt)
	h.expect(h.pre("Edit", map[string]any{"file_path": a}), "deny", "or again if an earlier run stopped")
}

func TestTranscriptsAreProtected(t *testing.T) {
	h := newHarness(t)
	h.setup()
	tr := filepath.Join(claudeHome(), "projects", "p", "s1", "subagents", "agent-q.jsonl")
	h.expect(h.pre("Write", map[string]any{"file_path": tr, "content": tlInterrupt}), "deny", "session transcripts")
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(claudeHome(), "projects", "p", "s1.jsonl")}), "deny", "protected")
	h.expect(h.bash("echo x >> ~/.claude/projects/p/s1/subagents/agent-q.jsonl"), "deny", "")
	h.expect(h.bash("printf x | tee -a "+tr), "deny", "")
	h.expect(h.pre("Read", map[string]any{"file_path": tr}), "", "")
	if o := h.pre("Write", map[string]any{"file_path": filepath.Join(claudeHome(), "projects", "p", "memory", "note.md"), "content": "x"}); strings.Contains(fmtOut(o), "config-protect") || strings.Contains(fmtOut(o), "session transcripts") {
		t.Fatalf("memory notes aren't transcripts: %v", o)
	}
}
