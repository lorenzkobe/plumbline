package core

// Blind QA for release 0.8.8 (the courtly 0.8.7 review), written from the
// plan's case table (brief-qa-1.md), not from the new code. Test names carry
// the case id.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- (1) reviewer reads of blind QA's tests ---

// r88QATests: a big task with a code change and blind QA's tests kept in
// the notes folder at qa-1-tests/ and qa-2-tests/, with the shapes a Next.js
// route test has ([id] folders). The kept copies are what keepQATests writes
// (the test file at its repo path under qa-N-tests); these are hand-made.
func r88QATests(t *testing.T) (*harness, string, string) {
	t.Helper()
	h := bigTask(t)
	h.fix("return 2")
	docs := TaskDocsDir(h.repo, h.task().ID)
	write(t, filepath.Join(docs, QATestsDir(1), "tests", "api", "[id]", "submit", "route.test.ts"), "test('submit', () => {})\n")
	write(t, filepath.Join(docs, QATestsDir(2), "tests", "lib", "x.test.ts"), "test('x', () => {})\n")
	write(t, filepath.Join(h.repo, "tests", "api", "[id]", "submit", "route.test.ts"), "test('submit', () => {})\n")
	write(t, filepath.Join(docs, "challenge-1.md"), "# Challenge 1\nsecret finding\n")
	id, _ := h.startHelper(HelperCodeReviewer)
	return h, id, docs
}

func TestR88_C1_ReviewerReadsBracketPathInQATests(t *testing.T) {
	h, id, docs := r88QATests(t)
	kept := filepath.Join(docs, "qa-1-tests", "tests", "api", "[id]", "submit", "route.test.ts")
	h.expect(h.hi(id, HelperCodeReviewer, "Read", map[string]any{"file_path": kept}), "", "")
	for _, cmd := range []string{
		`diff "` + kept + `" "tests/api/[id]/submit/route.test.ts"`,
		`diff '` + kept + `' 'tests/api/[id]/submit/route.test.ts'`,
		`diff ` + strings.ReplaceAll(strings.ReplaceAll(kept, "[", `\[`), "]", `\]`) + ` tests/api/\[id\]/submit/route.test.ts`,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "", "")
	}
}

func TestR88_C2_ReviewerListsQATestsGlob(t *testing.T) {
	h, id, docs := r88QATests(t)
	glob := filepath.Join(docs, "qa-*-tests")
	for _, cmd := range []string{
		"ls -d " + glob,
		"find " + glob + " -name '*.ts'",
		"ls -R " + glob,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "", "")
	}
}

func TestR88_C3_DotDotOutOfQATestsIsRefused(t *testing.T) {
	h, id, docs := r88QATests(t)
	for _, p := range []string{
		docs + "/qa-1-tests/*/../../challenge-1.md",
		docs + "/qa-1-tests/../challenge-1.md",
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Read", map[string]any{"file_path": p}), "deny", "independent")
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat " + p}), "deny", "independent")
	}
}

func TestR88_C4_OtherGlobsAndTheFolderStayRefused(t *testing.T) {
	h, id, docs := r88QATests(t)
	// q*-tests isn't qa-...-tests literally.
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "ls -d " + docs + "/q*-tests"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Read", map[string]any{"file_path": docs + "/q*-tests/tests/lib/x.test.ts"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat " + docs + "/*"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "ls " + docs + "/*"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cd " + docs + " && cat challenge-1.md"}), "deny", "")
	// A non-qa entry the pattern matches makes it refuse.
	write(t, filepath.Join(docs, "qa-x-tests"), "# notes\n")
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "ls -d " + docs + "/qa-*-tests"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat " + docs + "/qa-*-tests"}), "deny", "")
}

func TestR88_C5_UnlistableNotesFolderRefusesTheGlob(t *testing.T) {
	h, id, docs := r88QATests(t)
	must(t, os.Chmod(docs, 0o311)) // can enter, can't list
	t.Cleanup(func() { _ = os.Chmod(docs, 0o700) })
	if _, err := os.ReadDir(docs); err == nil {
		t.Skip("the folder can still be listed (running as root?)")
	}
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "ls -d " + docs + "/qa-*-tests"}), "deny", "")
}

func TestR88_C6_RefusalNamesQATestsByFullPath(t *testing.T) {
	h, id, docs := r88QATests(t)
	o := h.hi(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(docs, "challenge-1.md")})
	h.expect(o, "deny", "independent")
	r := reasonOf(o)
	if !strings.Contains(r, "-tests/") || !strings.Contains(r, "qa-") || !strings.Contains(r, "full path") {
		t.Fatalf("the refusal should say blind QA's tests under qa-<n>-tests/ are readable by full path: %s", r)
	}
}

// --- (2) related-run coverage leaves prose files out ---

func r88RelatedWithDocs(t *testing.T) *harness {
	t.Helper()
	h := relatedTask(t)
	h.editFile("README.md", "# Readme\nA returns 2 now\n")
	h.editFile("docs/notes.txt", "notes\n")
	h.writeSelfReview("# Self-review\n## Checked\n- src/a.go: the checklist\n- src/b.go: the checklist\n- README.md: wording\n- docs/notes.txt: wording\n## Fixed\n- none\n")
	return h
}

func TestR88_C7_RelatedRunNeedNotCoverDocs(t *testing.T) {
	h := r88RelatedWithDocs(t)
	h.relatedRun("./test.sh src/a.go src/b.go", []string{"src/a.go", "src/b.go"})
	h.expect(h.startReviewer(HelperCodeReviewer), "", "")
}

func TestR88_C8_MissingCodeFileStillRefuses(t *testing.T) {
	h := r88RelatedWithDocs(t)
	h.relatedRun("./test.sh src/a.go README.md", []string{"src/a.go", "README.md"})
	o := h.startReviewer(HelperCodeReviewer)
	h.expect(o, "deny", "src/b.go")
	if r := reasonOf(o); strings.Contains(r, "README.md") || strings.Contains(r, "notes.txt") {
		t.Fatalf("only the code file is missing: %s", r)
	}
}

// --- (3) builder notes ---

func r88Builder(t *testing.T) (*harness, string, string, string) {
	t.Helper()
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	notes := h.findingsPath(id)
	write(t, notes, "# Build 1\n- started\n")
	return h, id, wt, notes
}

func (h *harness) builderBash(id, wt, cmd string) Output {
	in := h.helperIn(id, HelperBuilder, "Bash", map[string]any{"command": cmd})
	in.Cwd = wt
	return PreToolUse(in)
}

func TestR88_C9_BuilderEditsItsNotes(t *testing.T) {
	h, id, _, notes := r88Builder(t)
	h.mustAllow(h.hi(id, HelperBuilder, "Edit", map[string]any{"file_path": notes, "old_string": "started", "new_string": "done"}), "Edit of the builder's notes")
	h.mustAllow(h.hi(id, HelperBuilder, "MultiEdit", map[string]any{"file_path": notes, "edits": []any{map[string]any{"old_string": "started", "new_string": "done"}}}), "MultiEdit of the builder's notes")
	h.mustAllow(h.hi(id, HelperBuilder, "Write", map[string]any{"file_path": notes, "content": "# Build 1\n"}), "Write of the builder's notes")
}

func TestR88_C10_LinkedBuilderNotesRefusedForEdit(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		h, id, _, notes := r88Builder(t)
		other := filepath.Join(filepath.Dir(notes), "elsewhere.md")
		write(t, other, "# Build 1\n- started\n")
		must(t, os.Remove(notes))
		must(t, os.Symlink(other, notes))
		if allowedOut(h.hi(id, HelperBuilder, "Edit", map[string]any{"file_path": notes, "old_string": "started", "new_string": "done"})) {
			t.Fatal("an Edit through a symlinked notes path is allowed")
		}
	})
	t.Run("hard link", func(t *testing.T) {
		h, id, _, notes := r88Builder(t)
		must(t, os.Link(notes, filepath.Join(filepath.Dir(notes), "second-name.md")))
		if allowedOut(h.hi(id, HelperBuilder, "Edit", map[string]any{"file_path": notes, "old_string": "started", "new_string": "done"})) {
			t.Fatal("an Edit of a notes file with a second hard link is allowed")
		}
	})
}

func TestR88_C11_BuilderShellWriteToNotesNamesTheTools(t *testing.T) {
	h, id, wt, notes := r88Builder(t)
	for _, cmd := range []string{
		"cat > " + notes + " <<'EOF'\n# Build 1\nEOF",
		"printf '%s\\n' '- more' >> " + notes,
	} {
		h.expect(h.builderBash(id, wt, cmd), "deny", "write your notes with the Write or Edit tool, not the shell")
	}
}

// --- (4) a specialist with only should-fix items keeps its review ---

const r88ShouldFix = "# Security review 1\n## Findings\n- [40] should-fix: A's result isn't checked by its caller (src/a.go:3 `func A() int`). Why: no caller reads it. Settle it: grep for A(. Fix: check it.\n"

// r88Carry: q83Carry's task (a normal task with an auth file in the plan),
// with the security review's only item a should-fix, then a fix in
// src/a.go, the file the code reviewer's must-fix cites.
func r88Carry(t *testing.T, security string) *harness {
	t.Helper()
	h := riskyTask(t, "- lead, challenger, code-reviewer, security-reviewer")
	h.runHelper(HelperSecurity, security)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: A should return 3 (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns 2.\n")
	h.editA("3")
	return h
}

func (h *harness) r88SecurityReruns() bool {
	h.t.Helper()
	return strings.Contains(h.finishMissing(), "plumbline:security-reviewer")
}

func TestR88_C12_ShouldFixOnlySpecialistCarries(t *testing.T) {
	h := r88Carry(t, r88ShouldFix)
	h.selfReview() // current, its Reach names src/a.go only
	if m := h.finishMissing(); strings.Contains(m, "plumbline:security-reviewer") || !strings.Contains(m, "plumbline:code-reviewer") {
		t.Fatalf("only the code reviewer reruns: %s", m)
	}
	h.expect(h.startReviewer(HelperSecurity), "deny", "had no must-fix")
}

func TestR88_C13_ReachNamingItsKindOfFileReruns(t *testing.T) {
	h := r88Carry(t, r88ShouldFix)
	p := h.selfReview()
	text := read(t, p)
	if !strings.Contains(text, "## Reach") {
		t.Fatalf("fixture: a fix round's self-review has a Reach section: %s", text)
	}
	h.writeSelfReview(text + "- `src/auth/session.go`: the session code that calls A, checked\n")
	if !h.r88SecurityReruns() {
		t.Fatalf("a Reach path matching the security signal reruns the security reviewer: %s", h.finishMissing())
	}
	h.expect(h.startReviewer(HelperSecurity), "", "")
}

func TestR88_C14_MissingOrStaleSelfReviewReruns(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		p := h.selfReview()
		must(t, os.Remove(p))
		if !h.r88SecurityReruns() {
			t.Fatalf("no self-review: the specialist reruns: %s", h.finishMissing())
		}
	})
	t.Run("older than the code", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		h.selfReview()
		h.editA("4")
		if !h.r88SecurityReruns() {
			t.Fatalf("a self-review older than the code: the specialist reruns: %s", h.finishMissing())
		}
	})
}

func TestR88_C15_MustFixDisputeAndCodeReviewerRerun(t *testing.T) {
	t.Run("must-fix", func(t *testing.T) {
		h := r88Carry(t, "# Security review 1\n## Findings\n- [85] must-fix: A leaks the session (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns the token.\n")
		h.selfReview()
		if !h.r88SecurityReruns() {
			t.Fatalf("a specialist with a must-fix reruns: %s", h.finishMissing())
		}
	})
	t.Run("open dispute", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		h.docs(DisputesName(HelperSecurity), "- [40] A's result is read by its caller (src/a.go:3)\n")
		h.selfReview()
		if !h.r88SecurityReruns() {
			t.Fatalf("a specialist with an open dispute reruns: %s", h.finishMissing())
		}
	})
	t.Run("code reviewer", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		h.selfReview()
		if m := h.finishMissing(); !strings.Contains(m, "plumbline:code-reviewer") {
			t.Fatalf("the code reviewer reruns after a code change: %s", m)
		}
	})
}

func TestR88_C44_UnlabeledFindingReruns(t *testing.T) {
	h := r88Carry(t, "# Security review 1\n## Findings\n- A's result may not be checked by its caller (src/a.go:3 `func A() int`).\n")
	h.selfReview()
	if !h.r88SecurityReruns() {
		t.Fatalf("an unlabeled finding doesn't carry: %s", h.finishMissing())
	}
}

func TestR88_C45_LinkedOrChangedSelfReviewReruns(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		p := h.selfReview()
		other := p + ".real"
		must(t, os.Rename(p, other))
		must(t, os.Symlink(other, p))
		if !h.r88SecurityReruns() {
			t.Fatalf("a symlinked self-review: the specialist reruns: %s", h.finishMissing())
		}
	})
	t.Run("hard link", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		p := h.selfReview()
		must(t, os.Link(p, p+".second"))
		if !h.r88SecurityReruns() {
			t.Fatalf("a hard-linked self-review: the specialist reruns: %s", h.finishMissing())
		}
	})
	t.Run("changed after it was recorded", func(t *testing.T) {
		h := r88Carry(t, r88ShouldFix)
		p := h.selfReview()
		write(t, p, read(t, p)+"- one more line, written with the shell\n")
		if !h.r88SecurityReruns() {
			t.Fatalf("a self-review changed since it was recorded: the specialist reruns: %s", h.finishMissing())
		}
	})
}

func TestR88_C48_BuilderWriteThroughSymlinkedNotesRefused(t *testing.T) {
	h, id, _, notes := r88Builder(t)
	must(t, os.Remove(notes))
	must(t, os.Symlink(filepath.Join(h.repo, "src", "a.go"), notes))
	if allowedOut(h.hi(id, HelperBuilder, "Write", map[string]any{"file_path": notes, "content": "package src\n"})) {
		t.Fatal("a Write to a notes path that links into the main checkout is allowed")
	}
}

// --- (6) a stop while a check runs on the current code ---

func TestR88_C19_StopWhileSuiteRunsGoesThrough(t *testing.T) {
	h := f87NeverFinished(t, os.Getpid(), Now().Add(-2*time.Minute))
	o := h.stop("Started the full suite in the background.")
	out := fmtOut(o)
	if strings.Contains(out, "isn't done until it's proven") {
		t.Fatalf("a stop while the suite runs on the current code is sent back: %s", out)
	}
	if !strings.Contains(out, "a check is still running") || !strings.Contains(out, "you're told when it ends") {
		t.Fatalf("the stop carries the wait note: %s", out)
	}
	if n := len(h.task().Sendbacks); n != 0 {
		t.Fatalf("no send-back is recorded: %d", n)
	}
}

func TestR88_C20_StopWithoutALiveRunIsSentBack(t *testing.T) {
	t.Run("no run on the current code", func(t *testing.T) {
		h := buildingTask(t)
		h.fix("return 2")
		out := fmtOut(h.stop("Done."))
		if !strings.Contains(out, "isn't done until it's proven") {
			t.Fatalf("no run: sent back: %s", out)
		}
		if len(h.task().Sendbacks) != 1 {
			t.Fatalf("one send-back recorded: %+v", h.task().Sendbacks)
		}
	})
	t.Run("dead run", func(t *testing.T) {
		h := f87NeverFinished(t, f87DeadPID(t), Now().Add(-2*time.Minute))
		out := fmtOut(h.stop("Done."))
		if !strings.Contains(out, "isn't done until it's proven") || !strings.Contains(out, "run it again") {
			t.Fatalf("a dead run: sent back, told to run it again: %s", out)
		}
	})
	t.Run("overdue run", func(t *testing.T) {
		s := &Setup{}
		s.Commands.Test = "./test.sh"
		h := f87NeverFinished(t, os.Getpid(), Now().Add(-(RunLimit(KindTest, s) + 2*time.Minute)))
		out := fmtOut(h.stop("Done."))
		if !strings.Contains(out, "isn't done until it's proven") || !strings.Contains(out, "run it again") {
			t.Fatalf("an overdue run: sent back, told to run it again: %s", out)
		}
	})
}

// --- (7) parallel reviewer starts refused for the same reason ---

func TestR88_C23_SecondSelfReviewRefusalIsOneLine(t *testing.T) {
	h := provenTask(t)
	first := reasonOf(h.startReviewer(HelperCodeReviewer))
	if !strings.Contains(first, "review your own change") {
		t.Fatalf("fixture: the first start is refused for the missing self-review: %s", first)
	}
	o := h.startReviewer(HelperSecurity)
	h.expect(o, "deny", "refused for the same reason as the reviewer start just before: write self-review.md first")
	if r := reasonOf(o); strings.Contains(r, "review-checklist.md") || strings.Contains(r, "\n") {
		t.Fatalf("the repeat is one short line: %q", r)
	}
}

// --- (8) the third refusal under one rule in a helper run ---

const r88RuleNote = "rewording won't get through"

func TestR88_C24_ThirdRefusalUnderARuleCarriesTheNote(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	for i, cmd := range []string{"npm test", "npx jest src", "make build"} {
		r := reasonOf(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}))
		if r == "" {
			t.Fatalf("fixture: %q is refused for a reviewer", cmd)
		}
		has := strings.Contains(r, r88RuleNote)
		if i < 2 && has {
			t.Fatalf("refusal %d already carries the note: %s", i+1, r)
		}
		if i == 2 && (!has || !strings.Contains(r, "Plumbline has refused 3 of your commands under this rule") || !strings.Contains(r, "note it in your findings")) {
			t.Fatalf("the third refusal carries the note: %s", r)
		}
	}
}

func TestR88_C25_NoNoteForTheLeadOrAfterAReset(t *testing.T) {
	t.Run("lead", func(t *testing.T) {
		h := provenTask(t)
		for _, cmd := range []string{"git push origin main", "git push origin HEAD", "git push -f origin main"} {
			o := h.bash(cmd)
			h.expect(o, "deny", "")
			if strings.Contains(reasonOf(o), r88RuleNote) {
				t.Fatalf("the lead gets no rule note: %s", reasonOf(o))
			}
		}
	})
	t.Run("reset by a user message", func(t *testing.T) {
		h := provenTask(t)
		id, _ := h.startHelper(HelperCodeReviewer)
		h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "npm test"})
		h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "npx jest src"})
		ResetBlocks(h.sid)
		for _, cmd := range []string{"make build", "cargo build"} {
			if r := reasonOf(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd})); r == "" || strings.Contains(r, r88RuleNote) {
				t.Fatalf("after a reset the count starts over: %q", r)
			}
		}
	})
}

func TestR88_C47_ParallelRefusalsCountOnce(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	at := time.Unix(1_800_000_000, 0)
	blockNow = func() time.Time { return at } // one message: three parallel calls
	for _, cmd := range []string{"npm test", "npx jest src", "make build"} {
		if r := reasonOf(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd})); r == "" || strings.Contains(r, r88RuleNote) {
			t.Fatalf("parallel refusals count once: %q", r)
		}
	}
	at = at.Add(10 * time.Second)
	if r := reasonOf(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cargo build"})); strings.Contains(r, r88RuleNote) {
		t.Fatalf("the next message's refusal is the second: %s", r)
	}
	at = at.Add(10 * time.Second)
	if r := reasonOf(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "go build ./..."})); !strings.Contains(r, r88RuleNote) {
		t.Fatalf("the third counted refusal carries the note: %s", r)
	}
}

// --- (9) arithmetic sed ranges and local psql reads ---

func r88Reviewer(t *testing.T) (*harness, string) {
	t.Helper()
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	return h, id
}

func TestR88_C26_ArithmeticSedRangeIsARead(t *testing.T) {
	h, id := r88Reviewer(t)
	for _, cmd := range []string{
		`for l in 8851 9052; do sed -n "$((l-25)),$((l+30))p" src/a.go; done`,
		`sed -n "$((10-5)),$(( (2+3)*4 ))p" src/a.go`,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "", "")
	}
}

func TestR88_C27_UnknownArithmeticStaysUnknown(t *testing.T) {
	h, id := r88Reviewer(t)
	for _, cmd := range []string{
		`sed -n "$((x-25)),$((x+30))p" src/a.go`,
		`for l in 5; do sed -n "$((l=5)),10p" src/a.go; done`,
		`for l in 5; do sed -n "$((l++)),10p" src/a.go; done`,
		`for l in 5; do sed -n "$((l--)),10p" src/a.go; done`,
		`for a in 1; do for b in 2; do sed -n "$((a,b)),10p" src/a.go; done; done`,
		`sed -n "$((010)),20p" src/a.go`,
		`sed -n "$((0x10)),20p" src/a.go`,
		`sed -n "$((2#101)),20p" src/a.go`,
		`for l in 5; do sed -n "$((l/0)),10p" src/a.go; done`,
		`for l in 5; do sed -n "$((l%0)),10p" src/a.go; done`,
		`sed -n "$((1?2:3)),20p" src/a.go`,
		`sed -n "$((1<<2)),20p" src/a.go`,
		// the value decides more than a line number
		`for l in 5; do sed -n "$((l))w out.txt" src/a.go; done`,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "deny", "")
	}
}

// The user dropped helpers' psql reads in review (Plumbline can't see a
// PGHOSTADDR or service file in the shell profile that sends even
// psql -h localhost elsewhere): they stay refused, as in 0.8.7.
func TestR88_C28_LocalPsqlSelectIsARead(t *testing.T) {
	h, id := r88Reviewer(t)
	for _, cmd := range []string{
		`psql postgresql://u:p@127.0.0.1:55322/db -c "select id, status from bookings limit 5"`,
		`psql -h localhost -p 55322 -U postgres -d db -c 'SELECT count(*) FROM bookings'`,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "deny", "")
	}
}

func TestR88_C29_OtherPsqlStaysRefused(t *testing.T) {
	h, id := r88Reviewer(t)
	write(t, filepath.Join(h.repo, "q.sql"), "select 1;\n")
	for _, cmd := range []string{
		`psql postgresql://u:p@db.example.com:5432/db -c "select 1"`,
		`psql -h prod.db.internal -c 'select 1'`,
		`psql postgresql://u:p@127.0.0.1:55322/db -f q.sql`,
		`psql postgresql://u:p@127.0.0.1:55322/db -c "insert into t values (1)"`,
		`psql postgresql://u:p@127.0.0.1:55322/db -c "update t set x = 1"`,
		`psql postgresql://u:p@127.0.0.1:55322/db -c "select 1; delete from t"`,
		`$P -c 'select 1'`,
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": cmd}), "deny", "")
	}
}

// --- (10) grep -l over dir/*.ext fed to xargs ---

func r88Migrations(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	for _, n := range []string{"001_init.sql", "002_bookings.sql", "003_roles.sql"} {
		write(t, filepath.Join(h.repo, "supabase", "migrations", n), "create table x (id int); -- bookings\n")
	}
	return h
}

func TestR88_C30_GlobListingOverCleanFolderRuns(t *testing.T) {
	h := r88Migrations(t)
	for _, cmd := range []string{
		"grep -ln bookings supabase/migrations/*.sql | xargs grep -ln create",
		"grep -l bookings supabase/migrations/*.sql | xargs cat",
		"grep -ln bookings supabase/migrations/00?_init.sql | xargs grep -n create",
	} {
		h.expect(h.bash(cmd), "allow", "")
	}
}

func TestR88_C31_BareGlobNamesStillAsk(t *testing.T) {
	h := r88Migrations(t)
	for _, cmd := range []string{
		"cd supabase/migrations; grep -ln bookings *.sql | xargs grep -ln create",
		"cd supabase/migrations && grep -l bookings *.sql | xargs cat",
		"grep -l bookings supabase/migrations/*.sql | xargs grep -l create *.sql",
	} {
		h.expect(h.bash(cmd), "ask", "")
	}
}

func TestR88_C32_GlobListingOverDoubtfulFolderAsks(t *testing.T) {
	h := r88Migrations(t)
	m := func(p string) string { return filepath.Join(h.repo, "supabase", filepath.FromSlash(p)) }
	write(t, m("m1/001.sql"), "x\n")
	write(t, m("m1/.env"), "KEY=1\n")
	write(t, m("m2/001.sql"), "x\n")
	must(t, os.Symlink(m("m1"), m("m2/link")))
	write(t, m("m3/001.sql"), "x\n")
	must(t, os.Link(m("m3/001.sql"), m("m3/002.sql")))
	write(t, m("m4/001.sql"), "x\n")
	must(t, os.Symlink(filepath.Join(h.repo, ".env"), m("m4/002.sql")))
	for _, dir := range []string{"m1", "m2", "m3", "m4"} {
		h.expect(h.bash("grep -l KEY supabase/"+dir+"/*.sql | xargs cat"), "ask", "")
	}
	for i := 0; i < 20001; i++ {
		p := m(fmt.Sprintf("big/d%02d/f%05d.sql", i%50, i))
		if i < 50 {
			must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		}
		must(t, os.WriteFile(p, []byte("x\n"), 0o644))
	}
	h.expect(h.bash("grep -l KEY supabase/big/*.sql | xargs cat"), "ask", "")
}

func TestR88_C33_OtherGlobShapesStillAsk(t *testing.T) {
	h := r88Migrations(t)
	abs := filepath.Join(h.repo, "supabase", "migrations")
	for _, cmd := range []string{
		"grep -l bookings supabase/migrations/{001_init,002_bookings}.sql | xargs cat",
		"grep -l bookings supabase/migrations/00[12]_*.sql | xargs cat",
		"grep -l bookings supabase/**/*.sql | xargs cat",
		"grep -l bookings " + abs + "/*.sql | xargs cat",
		"grep -l bookings ../repo/supabase/migrations/*.sql | xargs cat",
		"grep -l bookings supabase/*/001_init.sql | xargs cat",
		"grep -l bookings supabase/migrations/~*.sql | xargs cat",
		`grep -l bookings supabase/migrations/\*.sql | xargs cat`,
		`cd "$SOMEWHERE"; grep -l bookings *.sql | xargs cat`,
		"grep -l -f supabase/migrations/*.sql src | xargs cat",
	} {
		if d, r := decision(h.bash(cmd)); d != "ask" {
			t.Errorf("%s: decision %q (%s), want ask", cmd, d, r)
		}
	}
}

// --- (11) citations by full path ---

func TestR88_C34_BareCitationAsksForTheFullPath(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: the route drops the id (route.ts:12 `export async function POST`). Fails when: POST /x -> 500.\n"))
	out := fmtOut(h.stopHelper(id, HelperCodeReviewer))
	if !strings.Contains(out, "cite it by its full path from the repo's top, e.g. src/app/api/x/route.ts:12, not route.ts:12") {
		t.Fatalf("a bare-name citation is sent back with the full-path example: %s", out)
	}
}

var reFullCitation = regexp.MustCompile("[A-Za-z0-9_.-]+(/[A-Za-z0-9_.\\[\\]-]+)+\\.[A-Za-z]+:[0-9]+")

func TestR88_C35_AgentDefinitionsShowAFullPathCitation(t *testing.T) {
	for _, name := range []string{"arbiter", "builder", "challenger", "code-reviewer", "data-reviewer", "guard-reviewer", "plan-reviewer", "security-reviewer", "tester", "tracer", "ux-reviewer"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "agents", name+".md"))
		must(t, err)
		if !reFullCitation.Match(b) {
			t.Errorf("agents/%s.md shows no citation by full repo path (dir/file.ext:line)", name)
		}
	}
}

// --- (13) real-data browser wording ---

func TestR88_C38_RealDataAsksSayTheSetupSaysSo(t *testing.T) {
	h := newHarness(t)
	h.setup()
	yes := true
	st, _, _ := LoadSetup(h.repo)
	st.LocalRealData = &yes
	_, err := SaveSetup(h.repo, st)
	must(t, err)
	must(t, ConfirmSetup(h.repo))
	click := map[string]any{"action": "left_click", "ref": "ref_9", "action_summary": "Saves the new bill"}
	// outside a task
	o := h.pre("mcp__claude-in-chrome__computer", click)
	h.expect(o, "ask", "which may use real data (your setup says so)")
	// inside a task
	h.startTask("small")
	o = h.pre("mcp__claude-in-chrome__computer", click)
	h.expect(o, "ask", "which may use real data (your setup says so)")
	js := map[string]any{"action": "javascript_exec", "text": "await fetch('/rest/v1/bills', {method: 'DELETE'})"}
	h.expect(h.pre("mcp__claude-in-chrome__javascript_tool", js), "ask", "which may use real data (your setup says so)")
}

// --- (14) findings still open from an earlier run ---

func TestR88_C39_StillOpenKeepsItsLabelAndCounts(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n## Findings\n- [90] must-fix: A should return 3 (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns 2.\n")
	h.fix("return 4")
	_, o := h.startHelper(HelperCodeReviewer)
	if out := fmtOut(o); !strings.Contains(out, "still open from run 1:") {
		t.Fatalf("the rerun note says how to carry an unfixed finding: %s", out)
	}
	item := "- [90] must-fix: still open from run 1: A should return 3 (src/a.go:3 `func A() int`). Fails when: A() is called -> it returns 2."
	if got := openMustFix("# Code review 2\n## Findings\n" + item + "\n"); len(got) != 1 {
		t.Fatalf("a carried must-fix is still open: %v", got)
	}
	if !reMustFixLabel.MatchString(item) {
		t.Fatal("a carried must-fix keeps its label first")
	}
	bare := "- [90] must-fix: still open from run 1: A should return 3 (src/a.go:3 `func A() int`)."
	if p := mustFixProblems("# Code review 2\n## Findings\n" + bare + "\n"); len(p) == 0 {
		t.Fatal("a carried must-fix is still checked like any other (no Fails when)")
	}
}

// --- (15) stats by helper kind ---

func TestR88_C41_StatsByHelperKind(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperCodeReviewer)
	for i := 0; i < 2; i++ { // the same refusal twice: a repeat block
		h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "npm test"})
	}
	h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "make build"})
	write(t, h.findingsPath(id), h.complete(HelperCodeReviewer, "# Code review 1\n## Findings\n- none\n"))
	h.stopHelper(id, HelperCodeReviewer)
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- none\n")
	tk := h.task()
	if tk.Tokens == nil {
		tk.Tokens = map[string]TokenCount{}
	}
	tk.Tokens["agent:"+id] = TokenCount{Input: 1000, Output: 234, Messages: 3}
	tk.Tokens["main:"+safeName(h.sid)] = TokenCount{Input: 5, Messages: 1}
	must(t, SaveTask(tk))
	LogTokens(tk, "proj")
	out := Stats(1)
	hasLine := func(words ...string) bool {
		for _, l := range strings.Split(out, "\n") {
			ok := true
			for _, w := range words {
				ok = ok && strings.Contains(strings.ToLower(l), strings.ToLower(w))
			}
			if ok {
				return true
			}
		}
		return false
	}
	if !hasLine("block", "code-reviewer") {
		t.Errorf("no blocks by helper kind:\n%s", out)
	}
	if !hasLine("repeat", "code-reviewer") {
		t.Errorf("no repeat blocks by helper kind:\n%s", out)
	}
	if !regexp.MustCompile(`code-reviewer: 2 runs? in 1 of 1 tasks?`).MatchString(out) {
		t.Errorf("no runs per reviewer kind (code-reviewer: 2 runs in 1 of 1 tasks):\n%s", out)
	}
	if !hasLine("token", "code-reviewer", "1,234") && !hasLine("token", "code-reviewer", "1234") {
		t.Errorf("no tokens per helper kind (code-reviewer 1,234):\n%s", out)
	}
}

func TestR88_C42_OldLogsStillSummarize(t *testing.T) {
	newHarness(t)
	// a log line as 0.8.7 wrote it: no helper field
	LogEvent(Event{Task: "t1", Type: "block", Rule: "helper-read-only", Detail: "npm test"})
	tc := TokenCount{Input: 10, Output: 5, Messages: 1}
	LogEvent(Event{Task: "t1", Type: "tokens", Size: "small", Tokens: &tc})
	out := Stats(1)
	if !strings.Contains(out, "helper-read-only 1") || !strings.Contains(out, "Tokens: 15 tokens") {
		t.Fatalf("old events still summarize:\n%s", out)
	}
}

// --- docs ---

func TestR88_C43_DocsDescribe088(t *testing.T) {
	top := filepath.Join("..", "..")
	readTop := func(p string) string {
		b, err := os.ReadFile(filepath.Join(top, filepath.FromSlash(p)))
		must(t, err)
		return string(b)
	}
	var pj struct {
		Version string `json:"version"`
	}
	must(t, json.Unmarshal([]byte(readTop(".claude-plugin/plugin.json")), &pj))
	cl := readTop("CHANGELOG.md")
	at := strings.Index(cl, "## 0.8.8")
	if at < 0 {
		t.Fatal("CHANGELOG has no 0.8.8 entry")
	}
	entry := cl[at:]
	if i := strings.Index(entry[3:], "\n## "); i >= 0 {
		entry = entry[:i+3]
	}
	for _, w := range []string{"qa-", "--final", "should-fix", "still open from run", "Skill tool"} {
		if !strings.Contains(entry, w) {
			t.Errorf("the 0.8.8 CHANGELOG entry doesn't mention %q", w)
		}
	}
	for _, f := range []string{"skills/task/SKILL.md", "internal/core/rules-task.md"} {
		text := readTop(f)
		for _, w := range []string{"--final", "should-fix"} {
			if !strings.Contains(text, w) {
				t.Errorf("%s doesn't mention %q (run checks in a fix round, clean carry)", f, w)
			}
		}
	}
}

// Self-review: a local -h with a remote URI names two hosts; which one psql
// uses isn't plain, so the helper read stays refused.
func TestR88_LocalPsqlWithTwoHostsRefused(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperData)
	h.expect(h.hi(id, HelperData, "Bash", map[string]any{"command": `psql -h localhost "postgresql://u:p@db.example.com:5432/app" -c "select 1"`}), "deny", "")
	h.expect(h.hi(id, HelperData, "Bash", map[string]any{"command": `psql "postgresql://u:p@127.0.0.1:55322/app" -c "select 1" > /tmp/out.txt`}), "deny", "")
}

// Review round 1 (H3.1, H3.2, H7.1): glob listings that reach a folder
// cleanTree never checked keep the ask: a dot glob can match . and ..
// under bash before 5.2, a glob that matches nothing leaves grep -r on the
// working folder under nullglob, and a .. after a link climbs from the
// link's target.
func TestR88_Round1GlobListingsThatEscapeAsk(t *testing.T) {
	h := r88Migrations(t)
	write(t, filepath.Join(h.repo, ".env"), "KEY=1\n")
	out := t.TempDir()
	write(t, filepath.Join(out, "cfg", "credentials.json"), `{"KEY":"1"}`)
	must(t, os.MkdirAll(filepath.Join(out, "sub"), 0o755))
	must(t, os.Symlink(filepath.Join(out, "sub"), filepath.Join(h.repo, "link")))
	write(t, filepath.Join(h.repo, "cfg", "a.sql"), "select 1;\n")
	write(t, filepath.Join(h.repo, "supabase", "seed", ".gitkeep"), "")
	for _, cmd := range []string{
		"grep -rl KEY supabase/.* | xargs cat",
		"grep -rl KEY supabase/.? | xargs cat",
		"grep -rl KEY supabase/..* | xargs cat",
		"grep -rl KEY supabase/migrations/*.nope | xargs cat",
		"grep -l KEY link/../cfg/* | xargs cat",
		"grep -l KEY link/../cfg/*.json | xargs cat",
		"grep -rl KEY link/../cfg | xargs cat",
		"grep -l KEY supabase/migrations/../../link/../cfg/* | xargs cat",
		"grep -rl KEY supabase/seed/* | xargs cat",
		"grep -rl KEY supabase/seed/?gitkeep | xargs cat",
	} {
		if d, r := decision(h.bash(cmd)); d != "ask" {
			t.Errorf("%s: decision %q (%s), want ask", cmd, d, r)
		}
	}
	if d, r := decision(h.bash("grep -l bookings supabase/migrations/*.sql | xargs cat")); d == "ask" {
		t.Errorf("the clean dir/*.sql listing still runs: %s", r)
	}
}

// Review round 1 (H5.2, H6.2): a dot glob after qa-N-tests/ can expand to
// .. under bash, so it is refused like any other path out of the folder.
func TestR88_Round1DotGlobOutOfQATestsRefused(t *testing.T) {
	h, id, docs := r88QATests(t)
	for _, p := range []string{
		docs + "/qa-1-tests/.*/challenge-1.md",
		docs + "/qa-1-tests/.?/challenge-1.md",
		docs + "/qa-1-tests/[.][.]/challenge-1.md",
	} {
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "cat " + p}), "deny", "")
		h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "bash -c 'cat " + p + "'"}), "deny", "")
	}
	h.expect(h.hi(id, HelperCodeReviewer, "Bash", map[string]any{"command": "grep -r X " + docs + "/qa-1-tests/.*"}), "deny", "")
	h.expect(h.hi(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(docs, "qa-1-tests", "tests", "api", "[id]", "submit", "route.test.ts")}), "", "")
}

// Review round 1 (H5.1, H6.1, H6.3): only a database client counts as a
// local read, and only one that can't connect anywhere else.
func TestR88_Round1LocalDBReadIsOnlyAClientOnThisLaptop(t *testing.T) {
	h := provenTask(t)
	id, _ := h.startHelper(HelperData)
	for _, cmd := range []string{
		`node -e 'require("fs").writeFileSync("a","b")' postgresql://127.0.0.1/db --command=select`,
		`python3 -c 1 http://localhost --execute=select`,
		`python3 -c 'open("src/a.go","w").write("x")' host=localhost --command=select`,
		`perl -e 'unlink "src/a.go"' host=127.0.0.1 --command='select 1'`,
		`psql "postgresql:///app?host=db.prod.example.com" -c "select 1"`,
		`psql -h localhost -d "hostaddr=203.0.113.5 dbname=app" -c "select 1"`,
		`psql "postgresql://127.0.0.1:55322/app?hostaddr=203.0.113.5" -c "select 1"`,
		`psql "service=prod" -h localhost -c "select 1"`,
	} {
		h.expect(h.hi(id, HelperData, "Bash", map[string]any{"command": cmd}), "deny", "")
	}
	h.expect(h.hi(id, HelperData, "Bash", map[string]any{"command": `psql "postgresql://postgres:postgres@127.0.0.1:55322/postgres" -c "select indexdef from pg_indexes"`}), "deny", "")
}

// Review round 1 (H5.3): a setup from before the real-data question doesn't
// say so; the ask says Plumbline assumes it.
func TestR88_Round1RealDataUnsetSaysAssumed(t *testing.T) {
	h := newHarness(t)
	h.setup()
	click := map[string]any{"action": "left_click", "ref": "ref_9", "action_summary": "Saves the new bill"}
	o := h.pre("mcp__claude-in-chrome__computer", click)
	h.expect(o, "ask", "your setup doesn't say, so Plumbline assumes it")
	if strings.Contains(reasonOf(o), "your setup says so") {
		t.Fatalf("an unset setup said nothing: %s", reasonOf(o))
	}
}

// Review round 1 (H4.1): a Reach that named a specialist's kind of file in
// an earlier fix round still brings it back after a later self-review that
// names only other files.
func TestR88_Round1EarlierReachStillCounts(t *testing.T) {
	sr := &SelfReview{Reach: []ReachEntry{
		{At: time.Unix(100, 0), Paths: []string{"src/auth/session.go"}},
		{At: time.Unix(200, 0), Paths: []string{"src/a.go"}},
	}}
	if !reachSince(sr, time.Unix(50, 0), reSecuritySignal) {
		t.Fatal("the earlier Reach named an auth file after the review")
	}
	if reachSince(sr, time.Unix(150, 0), reSecuritySignal) {
		t.Fatal("a Reach recorded before the review doesn't count")
	}
}

// Review round 1 (H5.4): the window runs from the last counted refusal, so
// refusals 1.5 s apart still count every other one.
func TestR88_Round1RuleWindowFromLastCounted(t *testing.T) {
	newHarness(t)
	at := time.Unix(1_800_000_000, 0)
	old := blockNow
	defer func() { blockNow = old }()
	blockNow = func() time.Time { return at }
	var got string
	for i := 0; i < 5; i++ { // 0, 1.5, 3.0, 4.5, 6.0 s: counted at 0, 3.0 and 6.0
		got = noteRule("s1", "a1", "helper-read-only")
		if i < 4 && got != "" {
			t.Fatalf("refusal %d carries the note too early: %q", i+1, got)
		}
		at = at.Add(1500 * time.Millisecond)
	}
	if !strings.Contains(got, "refused 3 of your commands") {
		t.Fatalf("the third counted refusal carries the note: %q", got)
	}
}

// Review round 2 (H13.1): the Reach of an earlier fix round is kept through
// the hooks, so a later self-review naming only other files doesn't let the
// specialist carry past it.
func TestR88_Round2ReachKeptAcrossSelfReviews(t *testing.T) {
	h := r88Carry(t, r88ShouldFix)
	p := h.selfReview()
	h.writeSelfReview(read(t, p) + "- `src/auth/session.go`: the session code that calls A, checked\n")
	if !h.r88SecurityReruns() {
		t.Fatalf("fixture: the auth Reach brings the security reviewer back: %s", h.finishMissing())
	}
	h.runHelper(HelperCodeReviewer, "# Code review 2\n## Findings\n- [40] should-fix: still open from run 1: A has no doc comment (src/a.go:3 `func A() int`).\n")
	h.editA("4")
	h.selfReview() // the new Reach names src/a.go only
	if !h.r88SecurityReruns() {
		t.Fatalf("the earlier auth Reach still counts: %s", h.finishMissing())
	}
}

// Review round 2 (H9.1): a self-review recorded before 0.8.8 has no Reach
// log; the current file's ## Reach is read instead.
func TestR88_Round2OldSelfReviewRecordReadsTheFile(t *testing.T) {
	h := r88Carry(t, r88ShouldFix)
	p := h.selfReview()
	h.writeSelfReview(read(t, p) + "- `src/auth/session.go`: the session code that calls A, checked\n")
	tk := h.task()
	tk.SelfReview.Reach = nil
	must(t, SaveTask(tk))
	if !h.r88SecurityReruns() {
		t.Fatalf("an old record's Reach is read from the file: %s", h.finishMissing())
	}
}
