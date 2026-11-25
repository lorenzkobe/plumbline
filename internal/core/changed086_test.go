package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const askedKeepOrUndo = "changed files before any plan was approved"

// asksKeepOrUndo: before approval the lead isn't stopped; it's told to ask
// the user whether to keep or undo what changed.
func asksKeepOrUndo(o Output) bool {
	return o["continue"] != false && strings.Contains(fmtOut(o), askedKeepOrUndo)
}

// reported: the change was put on this call, as a stop or as an ask.
func reported(o Output) bool { return o["continue"] == false || asksKeepOrUndo(o) }

// checkHarness: no task, like the kwenta.cc session, with saved checks of
// each shape the start-time rule tells apart.
type checkHarness struct {
	*harness
	shift time.Duration
	calls int
}

func newCheckHarness(t *testing.T) *checkHarness {
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.Commands.Format = "npm run format:check"
	s.Commands.Lint = "npm run lint && npm run lint:css"
	s.Commands.App = "npm run dev"
	s.ExtraAllowed = []string{"npm run fix"}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	old := Now
	t.Cleanup(func() { Now = old })
	ch := &checkHarness{harness: h}
	Now = func() time.Time { return old().Add(ch.shift) }
	ch.line("git status --short") // the snapshot
	return ch
}

// later moves the hooks' clock past the user's change: a file's change
// time can't be set back, so the call is moved forward instead.
func (h *checkHarness) later() { h.shift += 3 * time.Second }

// line runs one Bash call, pre then post, as Claude Code does.
func (h *checkHarness) line(cmd string) Output {
	h.calls++
	id := fmt.Sprintf("c%d", h.calls)
	h.call(true, "Bash", id, map[string]any{"command": cmd}, nil)
	return h.call(false, "Bash", id, map[string]any{"command": cmd}, map[string]any{"stdout": ""})
}

// userEdit is the user's own change between Claude's calls (a ! command or
// an editor), then the clock moves on.
func (h *checkHarness) userEdit(rel, body string) {
	write(h.t, filepath.Join(h.repo, filepath.FromSlash(rel)), body)
	h.later()
}

// The kwenta.cc line: the user's `! echo '.serena/' >> .gitignore`, then
// Claude's saved format check with reads after it.
func TestUserChangeBeforeASavedCheckIsntBlamed(t *testing.T) {
	h := newCheckHarness(t)
	h.userEdit(".gitignore", "coverage/\n.serena/\n")
	if o := h.line("npm run format:check > /dev/null 2>&1; echo format=$?; git status --short; uptime"); reported(o) {
		t.Fatalf("the user's own change was blamed on the saved check: %s", fmtOut(o))
	}
	h.userEdit("src/a.go", "package src\n\nfunc A() int { return 2 }\n")
	if o := h.line("npm run format:check"); reported(o) {
		t.Fatalf("the saved check alone was blamed: %s", fmtOut(o))
	}
	// The multi-part saved check counts as the whole line.
	h.userEdit("src/a.go", "package src\n\nfunc A() int { return 3 }\n")
	if o := h.line("npm run lint && npm run lint:css"); asksKeepOrUndo(o) {
		t.Fatalf("the whole saved lint line was blamed: %s", fmtOut(o))
	}
}

// What a check line changes while it runs still counts.
func TestSavedCheckStillOwnsWhatItChanges(t *testing.T) {
	h := newCheckHarness(t)
	id := "w1"
	cmd := "npm run format:check"
	h.call(true, "Bash", id, map[string]any{"command": cmd}, nil)
	write(t, filepath.Join(h.repo, "src", "new.go"), "package src\n")
	if o := h.call(false, "Bash", id, map[string]any{"command": cmd}, map[string]any{}); !asksKeepOrUndo(o) || !strings.Contains(fmtOut(o), "src/new.go") {
		t.Fatalf("a file the check wrote wasn't reported: %s", fmtOut(o))
	}
}

// A move, cp -p, unzip or touch -r keeps an old modified time; the change
// time still moves, so the check owns those too.
func TestSavedCheckOwnsOldTimedFiles(t *testing.T) {
	h := newCheckHarness(t)
	outside := filepath.Join(filepath.Dir(h.repo), "old.go")
	write(t, outside, "package src\n")
	past := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(outside, past, past))
	cmd := "npm run format:check"
	h.call(true, "Bash", "m1", map[string]any{"command": cmd}, nil)
	must(t, os.Rename(outside, filepath.Join(h.repo, "src", "moved.go")))
	if o := h.call(false, "Bash", "m1", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "src/moved.go") {
		t.Fatalf("a file moved in with an old time was hidden: %s", fmtOut(o))
	}

	h.call(true, "Bash", "p1", map[string]any{"command": cmd}, nil)
	kept := filepath.Join(h.repo, "src", "kept.go")
	write(t, kept, "package src\n")
	must(t, os.Chtimes(kept, past, past))
	if o := h.call(false, "Bash", "p1", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "src/kept.go") {
		t.Fatalf("a copy that kept its old time (cp -p) was hidden: %s", fmtOut(o))
	}
}

// A link made by the check is the check's: the link itself is looked at,
// not what it points to.
func TestSavedCheckOwnsLinksItMakes(t *testing.T) {
	h := newCheckHarness(t)
	old := filepath.Join(filepath.Dir(h.repo), "target.go")
	write(t, old, "package src\n")
	past := time.Now().Add(-time.Hour)
	must(t, os.Chtimes(old, past, past))
	cmd := "npm run format:check"
	h.call(true, "Bash", "l1", map[string]any{"command": cmd}, nil)
	must(t, os.Symlink("/bin/sh", filepath.Join(h.repo, "src", "sym.go")))
	if o := h.call(false, "Bash", "l1", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "src/sym.go") {
		t.Fatalf("a new symlink to a long-unchanged file was hidden: %s", fmtOut(o))
	}
	h.call(true, "Bash", "l2", map[string]any{"command": cmd}, nil)
	must(t, os.Link(old, filepath.Join(h.repo, "src", "hard.go")))
	if o := h.call(false, "Bash", "l2", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "src/hard.go") {
		t.Fatalf("a hard link to an old file was hidden: %s", fmtOut(o))
	}
}

// Only the exact saved checks, run in this checkout, get the start-time
// rule; every other shape keeps the full check.
func TestOnlyExactSavedChecksSkipTheUsersChange(t *testing.T) {
	h := newCheckHarness(t)
	must(t, os.MkdirAll(filepath.Join(h.repo, ".claude", "worktrees", "x"), 0o755))
	for i, line := range []string{
		"npm run fix", // saved, but not a check
		"npm run dev", // the app, not a check
		"./script.sh", // not saved
		"npm run format:check; touch notes.txt",
		"npm run format:check && mv src/b.go src/c.go",
		"CI=1 npm run format:check",
		"npm run format:check -- --write",
		"cd .. && npm run format:check",
		"cd .claude/worktrees/x && npm run format:check",
		"npm run lint:css", // one part of a multi-part saved check
		"npm run format:check $EXTRA",
	} {
		h.userEdit("src/a.go", "package src\n\nfunc A() int { return "+string(rune('a'+i))+" }\n")
		if o := h.line(line); !asksKeepOrUndo(o) || !strings.Contains(fmtOut(o), "src/a.go") {
			t.Errorf("%q skipped the full check: %s", line, fmtOut(o))
		}
		_ = os.Remove(filepath.Join(h.repo, "notes.txt"))
		if _, err := os.Stat(filepath.Join(h.repo, "src", "c.go")); err == nil {
			run(t, h.repo, "git", "checkout", "--", "src/b.go")
			_ = os.Remove(filepath.Join(h.repo, "src", "c.go"))
		}
		h.line("git status --short")
	}
}

// A removed file has no time of its own; its folder's time says when.
func TestRemovedFileGoesByItsFolder(t *testing.T) {
	h := newCheckHarness(t)
	must(t, os.Remove(filepath.Join(h.repo, "src", "b.go")))
	h.later()
	if o := h.line("npm run format:check"); reported(o) {
		t.Fatalf("the user's own removal was blamed on the check: %s", fmtOut(o))
	}
	h.shift = 0 // from here on the changes come while the check runs
	cmd := "npm run format:check"
	h.call(true, "Bash", "r1", map[string]any{"command": cmd}, nil)
	must(t, os.Remove(filepath.Join(h.repo, "src", "a.go")))
	if o := h.call(false, "Bash", "r1", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "src/a.go") {
		t.Fatalf("a removal while the check ran was hidden: %s", fmtOut(o))
	}
	// A whole folder gone: the nearest folder left decides.
	must(t, os.MkdirAll(filepath.Join(h.repo, "flaky", "deep"), 0o755))
	write(t, filepath.Join(h.repo, "flaky", "deep", "run1.log"), "x\n")
	h.line("git status --short")
	h.call(true, "Bash", "r2", map[string]any{"command": cmd}, nil)
	must(t, os.RemoveAll(filepath.Join(h.repo, "flaky")))
	if o := h.call(false, "Bash", "r2", map[string]any{"command": cmd}, map[string]any{}); !strings.Contains(fmtOut(o), "flaky/deep/run1.log") {
		t.Fatalf("a folder removed while the check ran was hidden: %s", fmtOut(o))
	}
}

// Claude's own background command keeps writing after its hooks ran, and
// its end isn't seen: from its start on, a changed file can't be put on the
// user, so it's asked about, saying it may be the user's own.
func TestBackgroundWritesBeforeACheckStillCount(t *testing.T) {
	for _, tool := range []string{"Bash", "Monitor"} {
		t.Run(tool, func(t *testing.T) {
			h := newCheckHarness(t)
			in := map[string]any{"command": "./gen.sh", "run_in_background": true}
			h.call(true, tool, "bg", in, nil)
			h.call(false, tool, "bg", in, map[string]any{})
			write(t, filepath.Join(h.repo, "src", "gen.go"), "package src\n")
			h.later()
			if o := h.line("npm run format:check"); !strings.Contains(fmtOut(o), "src/gen.go") || !strings.Contains(fmtOut(o), "background") {
				t.Errorf("a background command's file was hidden by a check: %s", fmtOut(o))
			}
			write(t, filepath.Join(h.repo, "src", "gen2.go"), "package src\n")
			h.later()
			if o := h.line("git status --short"); !strings.Contains(fmtOut(o), "src/gen2.go") {
				t.Errorf("a background command's file was hidden by a read: %s", fmtOut(o))
			}
			// Hours later it may still be running: still counted.
			h.shift += 3 * time.Hour
			write(t, filepath.Join(h.repo, "src", "gen3.go"), "package src\n")
			h.later()
			if o := h.line("git status --short"); !strings.Contains(fmtOut(o), "src/gen3.go") {
				t.Errorf("a long-running background command's file was hidden: %s", fmtOut(o))
			}
		})
	}
	t.Run("read-only background command", func(t *testing.T) {
		h := newCheckHarness(t)
		ro := map[string]any{"command": "git log --oneline", "run_in_background": true}
		h.call(true, "Bash", "ro", ro, nil)
		h.call(false, "Bash", "ro", ro, map[string]any{})
		h.userEdit("src/a.go", "package src\n\nfunc A() int { return 4 }\n")
		if o := h.line("git status --short"); reported(o) {
			t.Errorf("a read-only background command made the user's change count: %s", fmtOut(o))
		}
	})
	// Claude Code moved a foreground call to the background (its timeout,
	// or ctrl+b): the result carries the task id (2026-11 transcripts).
	for name, resp := range map[string]map[string]any{
		"timed out":  {"stdout": "", "stderr": "", "interrupted": false, "backgroundTaskId": "bnx1fv381", "timedOutAfterMs": 120000},
		"ctrl+b":     {"stdout": "", "stderr": "", "interrupted": false, "backgroundTaskId": "bo9eibhi8", "backgroundedByUser": true},
		"shell & at": nil,
	} {
		t.Run(name, func(t *testing.T) {
			h := newCheckHarness(t)
			cmd := "./test.sh"
			if resp == nil {
				cmd, resp = "nohup ./gen.sh > /dev/null 2>&1 &", map[string]any{"stdout": ""}
			}
			h.call(true, "Bash", "mv", map[string]any{"command": cmd}, nil)
			h.call(false, "Bash", "mv", map[string]any{"command": cmd}, resp)
			write(t, filepath.Join(h.repo, "src", "late.go"), "package src\n")
			h.later()
			if o := h.line("npm run format:check"); !strings.Contains(fmtOut(o), "src/late.go") {
				t.Errorf("a command still running in the background had its file hidden: %s", fmtOut(o))
			}
		})
	}
	// What never ran in the background sets nothing: a WebSocket Monitor
	// runs no command, and a call the user declined has no post hook.
	t.Run("no background command ran", func(t *testing.T) {
		h := newCheckHarness(t)
		ws := map[string]any{"url": "ws://localhost:9/x"}
		h.call(true, "Monitor", "ws", ws, nil)
		h.call(false, "Monitor", "ws", ws, map[string]any{})
		failed := h.in("Monitor", ws, nil)
		failed.ToolUseID, failed.Error = "ws2", "connection refused"
		PostToolUseFailure(failed)
		h.call(true, "Bash", "declined", map[string]any{"command": "npm run dev", "run_in_background": true}, nil)
		h.userEdit("src/a.go", "package src\n\nfunc A() int { return 5 }\n")
		if o := h.line("git status --short"); reported(o) {
			t.Errorf("the user's change was blamed though nothing ran in the background: %s", fmtOut(o))
		}
	})
	// A check line's own write after a background start is its own.
	t.Run("check writes after a background start", func(t *testing.T) {
		h := newCheckHarness(t)
		bg := map[string]any{"command": "./gen.sh", "run_in_background": true}
		h.call(true, "Bash", "bg", bg, nil)
		h.call(false, "Bash", "bg", bg, map[string]any{"backgroundTaskId": "b1"})
		h.call(true, "Bash", "ck", map[string]any{"command": "npm run format:check"}, nil)
		write(t, filepath.Join(h.repo, "src", "ck.go"), "package src\n")
		o := h.call(false, "Bash", "ck", map[string]any{"command": "npm run format:check"}, map[string]any{})
		if !strings.Contains(fmtOut(o), "src/ck.go") || strings.Contains(fmtOut(o), "background") {
			t.Errorf("the check's own write should be put on the check: %s", fmtOut(o))
		}
	})
	// A foreground writer after a background start: the ask still has
	// Claude say its own command made the files.
	t.Run("foreground writer later", func(t *testing.T) {
		h := newCheckHarness(t)
		bg := map[string]any{"command": "./gen.sh", "run_in_background": true}
		h.call(true, "Bash", "bg", bg, nil)
		h.call(false, "Bash", "bg", bg, map[string]any{"backgroundTaskId": "b1"})
		h.call(true, "Bash", "fg", map[string]any{"command": "./script.sh"}, nil)
		write(t, filepath.Join(h.repo, "src", "fg.go"), "package src\n")
		o := h.call(false, "Bash", "fg", map[string]any{"command": "./script.sh"}, map[string]any{})
		if !asksKeepOrUndo(o) || strings.Contains(fmtOut(o), "background") || !strings.Contains(fmtOut(o), "your command made them") {
			t.Errorf("a foreground writer's ask should say its own command made them: %s", fmtOut(o))
		}
	})
	t.Run("helper's background command", func(t *testing.T) {
		h := newCheckHarness(t)
		in := h.in("Bash", map[string]any{"command": "plumbline run test", "run_in_background": true}, nil)
		in.ToolUseID, in.AgentID, in.AgentType = "hb", "t1", "plumbline:"+HelperTester
		if d, r := decision(PreToolUse(in)); d == "deny" {
			t.Fatalf("setup: %s", r)
		}
		in.ToolResponse = rawJSON(map[string]any{})
		PostToolUse(in)
		write(t, filepath.Join(h.repo, "flaky", "run1.log"), "x\n")
		h.later()
		if o := h.line("npm run format:check"); !strings.Contains(fmtOut(o), "flaky/run1.log") {
			t.Errorf("a helper's background file was hidden by the lead's check: %s", fmtOut(o))
		}
	})
}

// Undo: the user removes the new file and restores the other with !, and
// Claude's next read doesn't ask about them again.
func TestUndoWithABangCommandIsntAskedAgain(t *testing.T) {
	h := newCheckHarness(t)
	h.call(true, "Bash", "g1", map[string]any{"command": "./gen.sh"}, nil)
	write(t, filepath.Join(h.repo, "src", "new.go"), "package src\n")
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 8 }\n")
	if o := h.call(false, "Bash", "g1", map[string]any{"command": "./gen.sh"}, map[string]any{}); !asksKeepOrUndo(o) {
		t.Fatalf("setup: %s", fmtOut(o))
	}
	must(t, os.Remove(filepath.Join(h.repo, "src", "new.go")))
	run(t, h.repo, "git", "restore", "--", "src/a.go")
	h.later()
	if o := h.line("git status --short"); reported(o) {
		t.Fatalf("the user's undo was asked about again: %s", fmtOut(o))
	}
}

// A failing check goes through PostToolUseFailure: same rule, and the ask
// keeps its context there.
func TestFailingCheckAfterTheUsersChange(t *testing.T) {
	h := newCheckHarness(t)
	h.userEdit(".gitignore", "coverage/\n.serena/\n")
	cmd := "npm run format:check"
	h.call(true, "Bash", "f1", map[string]any{"command": cmd}, nil)
	in := h.in("Bash", map[string]any{"command": cmd}, nil)
	in.ToolUseID, in.Error = "f1", "exit 1"
	if o := PostToolUseFailure(in); reported(o) {
		t.Fatalf("a failing check was blamed for the user's change: %s", fmtOut(o))
	}

	h.call(true, "Bash", "f2", map[string]any{"command": "./gen.sh"}, nil)
	write(t, filepath.Join(h.repo, "src", "gen.go"), "package src\n")
	in = h.in("Bash", map[string]any{"command": "./gen.sh"}, nil)
	in.ToolUseID, in.Error = "f2", "exit 1"
	o := PostToolUseFailure(in)
	hs, _ := o["hookSpecificOutput"].(map[string]any)
	ctx, _ := hs["additionalContext"].(string)
	if o["continue"] == false || hs["hookEventName"] != "PostToolUseFailure" || !strings.Contains(ctx, "AskUserQuestion") {
		t.Fatalf("a failed command's change should reach Claude as an ask: %s", fmtOut(o))
	}
}

// Before approval the lead isn't stopped: it asks keep or undo, then carries on.
func TestChangeBeforeApprovalAsksInsteadOfStopping(t *testing.T) {
	check := func(name string, h *harness) {
		t.Helper()
		h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
		write(t, filepath.Join(h.repo, "src", "x.go"), "package src\n")
		o := h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{})
		if o["continue"] == false {
			t.Fatalf("%s: the lead was stopped instead of asked: %s", name, fmtOut(o))
		}
		hs, _ := o["hookSpecificOutput"].(map[string]any)
		ctx, _ := hs["additionalContext"].(string)
		for _, want := range []string{askedKeepOrUndo, "src/x.go", "AskUserQuestion", "keep", "undo", "!", "git restore", "carry on"} {
			if !strings.Contains(ctx, want) {
				t.Errorf("%s: the ask doesn't say %q: %s", name, want, ctx)
			}
		}
		// Kept: the next command isn't asked about the same files again.
		if o := h.post("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{}); asksKeepOrUndo(o) {
			t.Errorf("%s: asked about the same files twice: %s", name, fmtOut(o))
		}
	}
	nt := newHarness(t)
	nt.setup()
	check("no task", nt)
	check("task before approval", groundedTask(t, "normal"))
}

// A helper can't ask the user: before approval it's still stopped.
func TestHelperChangeBeforeApprovalStillStops(t *testing.T) {
	h := groundedTask(t, "normal")
	SubagentStart(&HookInput{SessionID: h.sid, Cwd: h.repo, AgentID: "t1", AgentType: "plumbline:" + HelperTracer})
	h.post("Bash", map[string]any{"command": "ls"}, map[string]any{})
	in := h.in("Bash", map[string]any{"command": "./gen.sh"}, map[string]any{})
	in.AgentID, in.AgentType = "t1", "plumbline:"+HelperTracer
	write(t, filepath.Join(h.repo, "src", "x.go"), "package src\n")
	if o := PostToolUse(in); o["continue"] != false {
		t.Fatalf("a helper's change before approval should stop it: %s", fmtOut(o))
	}
}

// A disk with whole-second times reads a write at :00.95 as :00. A check
// that started at :00.9 must still own it.
func TestWholeSecondTimesCountAsLater(t *testing.T) {
	start := time.Date(2026, 11, 25, 9, 0, 0, 900_000_000, time.UTC)
	stored := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	if got := latestChange(stored, stored); got.Before(start.Add(-500 * time.Millisecond)) {
		t.Fatalf("a whole-second time %v reads as before the check started at %v", got, start)
	}
	fine := time.Date(2026, 11, 25, 8, 0, 0, 1, time.UTC)
	later := fine.Add(time.Minute)
	if got := latestChange(fine, later); !got.Equal(later) {
		t.Fatalf("the later of the two times should count: %v", got)
	}
}
