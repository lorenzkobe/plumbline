package core

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for phase 2a: several reviewers, Jira reading and drafts, and the
// claude.ai connectors gated by tool name.

func TestReviewersFor(t *testing.T) {
	cases := []struct {
		size  string
		files []string
		want  string
	}{
		{"small", []string{"src/a.go"}, "code-reviewer"},
		{"normal", []string{"src/a.go", "src/a_test.go"}, "plan-reviewer,code-reviewer"},
		{"big", []string{"src/auth/login.go"}, "plan-reviewer,code-reviewer,security-reviewer"},
		{"small", []string{".env.example"}, "code-reviewer,security-reviewer"},
		{"normal", []string{"db/migrations/0042_add_refunds.sql"}, "plan-reviewer,code-reviewer,data-reviewer"},
		{"normal", []string{"src/billing/invoice.ts"}, "plan-reviewer,code-reviewer,security-reviewer,data-reviewer"},
		{"normal", []string{"web/src/components/Button.tsx"}, "plan-reviewer,code-reviewer,ux-reviewer"},
		{"normal", []string{"app/styles/main.scss"}, "plan-reviewer,code-reviewer,ux-reviewer"},
		{"normal", []string{"src/utils/format.go", "src/author.go", "src/dbg.go", "src/environment.ts"}, "plan-reviewer,code-reviewer"},
		// Found by the fresh-eyes review of 2a: camelCase names and absolute paths.
		{"small", []string{"src/services/AuthService.ts"}, "code-reviewer,security-reviewer"},
		{"small", []string{"app/Http/UserController.php"}, "code-reviewer,security-reviewer"},
		{"small", []string{"src/stripeWebhook.ts", "lib/passwordReset.rb"}, "code-reviewer,security-reviewer"},
		{"normal", []string{"src/OrderRepository.cs"}, "plan-reviewer,code-reviewer,data-reviewer"},
		{"normal", []string{"PaymentService.java"}, "plan-reviewer,code-reviewer,security-reviewer,data-reviewer"},
		// 0.7.0 (UX1): a small change gets one review, plus security for security code.
		{"small", []string{"src/OrderRepository.cs"}, "code-reviewer"},
		{"small", []string{"PaymentService.java"}, "code-reviewer,security-reviewer"},
		{"small", []string{"/Users/kobz/repo/src/a.go"}, "code-reviewer"},
	}
	for _, c := range cases {
		if got := strings.Join(ReviewersFor(c.size, "/Users/kobz/repo", c.files), ","); got != c.want {
			t.Errorf("%s %v: %s, want %s", c.size, c.files, got, c.want)
		}
	}
	if got := TeamReviewers("# Plan\n## Team\n- lead, plumbline:code-reviewer, Security-Reviewer\n## Branch\n- data-reviewer\n"); strings.Join(got, ",") != "code-reviewer,security-reviewer" {
		t.Errorf("team: %v", got)
	}
	for kind, want := range map[string]string{HelperChallenger: "challenge-2.md", HelperCodeReviewer: "code-review-2.md", HelperUX: "ux-review-2.md"} {
		if got := findingsName(kind, 2); got != want || !reFindings.MatchString(got) {
			t.Errorf("findings name for %s: %s", kind, got)
		}
	}
	if !reFindings.MatchString("review-1.md") || reFindings.MatchString("my-review-1.md") {
		t.Error("findings pattern")
	}
}

// riskyTask: an approved task whose plan touches an auth file. Sized small
// first, which the checkpoint refuses, then normal.
func riskyTask(t *testing.T, team string) *harness {
	h := groundedTask(t, "small")
	plan := strings.Replace(goodPlan, "- src/a.go\n", "- src/a.go\n- src/auth/session.go\n", 1)
	h.docs(DocPlan, plan)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "isn't small: run plumbline task size normal")
	tk := h.task()
	tk.Size = "normal" // what plumbline task size normal does
	must(t, SaveTask(tk))
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "must name the security-reviewer")
	h.runHelper(HelperChallenger, "# Challenge 1\nnothing at 80 or more\n")
	h.docs(DocPlan, plan+"## Team\n"+team+"\n## Challenge note\n- nothing changed\n")
	h.approve()
	h.redRun()
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	return h
}

func TestSpecialistsRequired(t *testing.T) {
	h := riskyTask(t, "- lead, challenger, plan-reviewer, code-reviewer, security-reviewer, data-reviewer")
	report := "# Report\n" + reportEnd + "## Review\n- all clean\n## For you\n- nothing\n"
	h.docs(DocReport, report)

	// The Team adds the data reviewer, the auth file the security reviewer.
	m := h.finishMissing()
	for _, k := range []string{"plumbline:plan-reviewer", "plumbline:code-reviewer", "plumbline:security-reviewer", "plumbline:data-reviewer"} {
		if !strings.Contains(m, k) {
			t.Fatalf("finish should need %s: %s", k, m)
		}
	}
	h.runHelper(HelperPlanReviewer, "# Plan review 1\nnothing\n")
	h.runHelper(HelperCodeReviewer, "# Code review 1\nnothing\n")
	h.runHelper(HelperData, "# Data review 1\nnothing\n")
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:security-reviewer") || strings.Contains(m, "code-reviewer") {
		t.Fatalf("only the security review is left: %s", m)
	}
	h.runHelper(HelperSecurity, "# Security review 1\n- [90] must-fix: token logged (src/a.go:3 `func A() int`)\n")
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("every reviewer done: %s", m)
	}

	// A fix makes every review stale, not only the one that found it.
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 3 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.docs(DocReport, report)
	if m := h.finishMissing(); !strings.Contains(m, "run these again to check the fixes: plumbline:plan-reviewer, plumbline:code-reviewer, plumbline:security-reviewer, plumbline:data-reviewer") {
		t.Fatalf("all reviewers re-check the final code: %s", m)
	}
	// The second run is told about its own earlier findings only.
	id, o := h.startHelper(HelperSecurity)
	if s := fmtOut(o); !strings.Contains(s, "security-review-1.md") || strings.Contains(s, "code-review-1.md") || !strings.Contains(s, "run 2 of at most 3 for the security-reviewer") {
		t.Fatalf("second security run context: %s", s)
	}
	write(t, h.findingsPath(id), h.complete(HelperSecurity, "fixed\n"))
	h.stopHelper(id, HelperSecurity)
	h.runHelper(HelperPlanReviewer, "fine\n")
	h.runHelper(HelperCodeReviewer, "fixed\n")
	h.runHelper(HelperData, "fine\n")
	h.docs(DocReport, report)
	if m := h.finishMissing(); m != "" {
		t.Fatalf("re-reviewed: %s", m)
	}

	// Switching a specialist off drops it.
	s, _, _ := LoadSetup(h.repo)
	s.Parts = map[string]bool{HelperData: false}
	if got := strings.Join(RequiredReviewers(h.task(), s), ","); got != "plan-reviewer,code-reviewer,security-reviewer" {
		t.Fatalf("data reviewer switched off: %s", got)
	}
}

func TestOutsidePlanFileAddsSpecialist(t *testing.T) {
	h := provenTask(t)
	card := filepath.Join(h.repo, "src", "components", "Card.tsx")
	h.expect(h.pre("Write", map[string]any{"file_path": card}), "", "")
	write(t, card, "export const Card = () => null\n")
	h.post("Write", map[string]any{"file_path": card}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	if m := h.finishMissing(); !strings.Contains(m, "plumbline:ux-reviewer") {
		t.Fatalf("a frontend file changed outside the plan needs the UX reviewer: %s", m)
	}
}

func TestReviewCapPerReviewer(t *testing.T) {
	h := provenTask(t)
	for i := 0; i < MaxReviews; i++ {
		h.runHelper(HelperCodeReviewer, "fine\n")
	}
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer"}), "deny", "code-reviewer already ran 3 times")
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "ux-reviewer"}), "", "")        // the lead may add one
	h.expect(h.pre("Agent", map[string]any{"subagent_type": "plumbline:reviewer"}), "", "") // not a Plumbline helper any more
}

func TestReviewersIndependent(t *testing.T) {
	h := provenTask(t)
	docs := TaskDocsDir(h.repo, h.task().ID)
	h.runHelper(HelperCodeReviewer, "# Code review 1\n- [85] must-fix: off by one\n")
	h.runHelper(HelperSecurity, "# Security review 1\nnothing\n")
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- code-review-1: off by one, fixed\n")
	id, _ := h.startHelper(HelperSecurity)
	hi := func(tool string, input any) Output { return PreToolUse(h.helperIn(id, HelperSecurity, tool, input)) }
	in := func(name string) string { return filepath.Join(docs, name) }

	for _, name := range []string{DocPlan, DocUnderstanding, "security-review-1.md", "security-review-2.md"} {
		h.expect(hi("Read", map[string]any{"file_path": in(name)}), "", "")
	}
	h.expect(hi("Read", map[string]any{"file_path": in("code-review-1.md")}), "deny", "independent")
	h.expect(hi("Read", map[string]any{"file_path": in(DocReport)}), "deny", "independent")
	h.expect(hi("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
	h.expect(hi("Grep", map[string]any{"pattern": "off by one", "path": docs}), "deny", "independent")
	h.expect(hi("Grep", map[string]any{"pattern": "off by one", "path": filepath.Dir(DataDir())}), "deny", "independent")
	h.expect(hi("Grep", map[string]any{"pattern": "A", "path": h.repo}), "", "")
	h.expect(hi("Grep", map[string]any{"pattern": "A"}), "", "")
	h.expect(hi("Glob", map[string]any{"pattern": docs + "/*.md"}), "deny", "independent")
	h.expect(hi("Glob", map[string]any{"pattern": "**/*.go"}), "", "")
	rel, err := filepath.Rel(h.repo, docs)
	must(t, err)
	h.expect(hi("Glob", map[string]any{"pattern": rel + "/*.md"}), "deny", "independent")
	h.expect(hi("Glob", map[string]any{"pattern": "*.md", "path": docs}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "grep --file=" + in("code-review-1.md") + " src/a.go"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat " + in("code-review-1.md")}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "head -5 < " + in(DocReport)}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat " + docs + "/*review*"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "rg -n off " + filepath.Dir(DataDir())}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat \"$(ls -d " + docs + ")/code-review-1.md\""}), "deny", "can't tell which notes file")
	h.expect(hi("Bash", map[string]any{"command": "d=" + docs + "; cat $d/code-review-1.md"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat " + in(DocPlan) + " && git diff HEAD && ls " + filepath.Dir(DataDir())}), "", "")
	h.expect(hi("Bash", map[string]any{"command": "rg -n A src"}), "", "")
	// Found by the fresh-eyes review of 2a.
	above := filepath.Dir(DataDir())
	h.expect(hi("Bash", map[string]any{"command": "cat " + above + "/d*/projects/*/tasks/*/docs/code-review-1.md"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat " + above + "/[d]ata/projects/x/code-review-1.md"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "xargs cat <<< " + in("code-review-1.md")}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "while read f; do cat \"$f\"; done <<EOF\n" + in("code-review-1.md") + "\nEOF"}), "deny", "independent")
	h.expect(hi("Bash", map[string]any{"command": "cat \"$(printf %s ~/.plumb)line/projects/x/code-review-1.md\""}), "deny", "can't tell")
	h.expect(hi("Bash", map[string]any{"command": "cat < \"$(printf %s ~/.plumb)line/x\""}), "deny", "can't tell")
	h.expect(hi("Bash", map[string]any{"command": "cat src/*.go && git diff $(git merge-base HEAD main)"}), "", "")
	h.expect(hi("mcp__filesystem__read_text_file", map[string]any{"path": in("code-review-1.md")}), "deny", "independent")
	h.expect(hi("mcp__filesystem__read_text_file", map[string]any{"path": filepath.Join(h.repo, "src", "a.go")}), "", "")

	// The challenger isn't a reviewer: it works before any review exists.
	if o := (&hookCtx{in: h.in("Read", nil, nil)}).reviewerNotes(HelperChallenger, in("code-review-1.md"), false); o != nil {
		t.Fatalf("challenger reads are not limited: %v", o)
	}
}

func TestLegacyReviewerRunCounts(t *testing.T) {
	h := provenTask(t)
	h.runHelper(HelperCodeReviewer, "fine\n")
	tk := h.task()
	tk.Helpers[0].Kind = legacyReviewer
	tk.Helpers[0].Findings = "review-1.md"
	must(t, SaveTask(tk))
	write(t, filepath.Join(TaskDocsDir(h.repo, tk.ID), "review-1.md"), "fine\n")
	if got := h.task().Helpers[0].Kind; got != HelperCodeReviewer {
		t.Fatalf("a 0.3.x reviewer run should load as the code reviewer's: %s", got)
	}
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For you\n- nothing\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("the 0.3.x review counts: %s", m)
	}
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(TaskDocsDir(h.repo, tk.ID), "review-1.md")}), "deny", "findings")

	// The next code review is told about review-1.md, and may read it.
	id, o := h.startHelper(HelperCodeReviewer)
	if !strings.Contains(fmtOut(o), "review-1.md") {
		t.Fatalf("second run should name the 0.3.x findings: %v", o)
	}
	h.expect(PreToolUse(h.helperIn(id, HelperCodeReviewer, "Read", map[string]any{"file_path": filepath.Join(TaskDocsDir(h.repo, tk.ID), "review-1.md")})), "", "")
}

func TestJiraAndConnectorKinds(t *testing.T) {
	jira := map[string]bool{ // tool -> reads only
		"mcp__atlassian__getJiraIssue":                              true,
		"mcp__atlassian__searchJiraIssuesUsingJql":                  true,
		"mcp__atlassian__getTransitionsForJiraIssue":                true,
		"mcp__atlassian__atlassianUserInfo":                         true,
		"mcp__atlassian__fetch":                                     true,
		"mcp__mcp-atlassian__jira_get_issue":                        true,
		"mcp__claude_ai_Atlassian__getJiraIssue":                    true,
		"mcp__atlassian__addCommentToJiraIssue":                     false,
		"mcp__atlassian__transitionJiraIssue":                       false,
		"mcp__atlassian__editJiraIssue":                             false,
		"mcp__atlassian__addWorklogToJiraIssue":                     false,
		"mcp__atlassian__createConfluencePage":                      false,
		"mcp__mcp-atlassian__jira_add_comment":                      false,
		"mcp__mcp-atlassian__jira_transition_issue":                 false,
		"mcp__claude_ai_Atlassian__addCommentToJiraIssue":           false,
		"mcp__claude_ai_Atlassian__transitionJiraIssue":             false,
		"mcp__plugin_atlassian_atlassian__getJiraIssue":             true,
		"mcp__plugin_atlassian_atlassian__jiraLinkIssues":           false,
		"mcp__claude_ai_Atlassian__getAccessibleAtlassianResources": true,
	}
	for tool, read := range jira {
		if j, r := JiraTool(tool); !j || r != read {
			t.Errorf("%s: jira=%v read=%v, want read=%v", tool, j, r, read)
		}
		want := "write"
		if read {
			want = ""
		}
		if got := MCPKind(tool); got != want {
			t.Errorf("%s kind: %q, want %q", tool, got, want)
		}
	}
	if j, _ := JiraTool("mcp__github__get_issue"); j {
		t.Error("github is not jira")
	}
	for tool, want := range map[string]string{
		"mcp__claude_ai_Slack__slack_send_message":     "write",
		"mcp__claude_ai_Slack__slack_schedule_message": "write",
		"mcp__claude_ai_Slack__slack_read_channel":     "",
		"mcp__claude_ai_Gmail__create_draft":           "write",
		"mcp__claude_ai_Gmail__search_threads":         "",
		"mcp__claude_ai_Claude_Docs__batch":            "",
		"mcp__claude_ai_Claude_Docs__update":           "",
		"mcp__github__add_issue_comment":               "write",
		"mcp__github__get_issue_comments":              "",
		"mcp__x__get_post":                             "",
		"mcp__x__reply_to_thread":                      "write",
		"mcp__x__post_message":                         "write",
		"mcp__claude-in-chrome__tabs_close_mcp":        "",
	} {
		if got := MCPKind(tool); got != want {
			t.Errorf("%s: %q, want %q", tool, got, want)
		}
	}
}

func (h *harness) say(prompt string) {
	UserPromptSubmit(&HookInput{SessionID: h.sid, Cwd: h.repo, Prompt: prompt})
}

func TestJiraDraftOnly(t *testing.T) {
	h := newHarness(t)
	h.setup()
	post := map[string]any{"cloudId": "x", "issueIdOrKey": "ABC-1", "commentBody": "Fixed the refund check. Tests added."}
	tool := "mcp__claude_ai_Atlassian__addCommentToJiraIssue"

	// No task, nobody asked: refused, not asked.
	h.say("fix the refund bug")
	h.expect(h.pre(tool, post), "deny", "draft-only")
	h.expect(h.pre("mcp__atlassian__transitionJiraIssue", map[string]any{"transition": "Done"}), "deny", "draft-only")
	h.expect(h.pre("mcp__atlassian__getJiraIssue", map[string]any{"issueIdOrKey": "ABC-1"}), "", "")

	// The user asks for it: their OK on the exact text.
	h.say("okay write this in the ticket")
	h.expect(h.pre(tool, post), "ask", "user asked")
	// Still no AI markers or em dashes in their name.
	h.expect(h.pre(tool, map[string]any{"commentBody": "Fixed.\n\nGenerated with Claude Code"}), "deny", "AI markers")
	h.expect(h.pre(tool, map[string]any{"body": map[string]any{"text": "Fixed it — tests added"}}), "deny", "em dash")
	// The next message decides again.
	h.say("thanks, now look at the logs")
	h.expect(h.pre(tool, post), "deny", "draft-only")

	// Paused: still the user's.
	must(t, SetPaused(h.repo, true))
	h.expect(h.pre(tool, post), "deny", "draft-only")
	must(t, SetPaused(h.repo, false))

	// A forged flag file counts for nothing.
	write(t, jiraAskedPath(h.sid), "2026-01-01T00:00:00Z abc\n")
	h.expect(h.pre(tool, post), "deny", "draft-only")
	// Claude can't write the flag: it's in the data folder.
	h.expect(h.bash("echo x > "+jiraAskedPath(h.sid)), "deny", "")

	// Inside a task, the refusal names the draft file.
	h.startTask("small")
	tk := h.task()
	tk.Ticket = "ABC-1"
	must(t, SaveTask(tk))
	h.expect(h.pre(tool, post), "deny", "ABC-1-jira-comment.md")

	// Helpers never post, even when the user asked.
	h.say("post it to jira")
	in := h.helperIn("r1", HelperCodeReviewer, tool, post)
	h.expect(PreToolUse(in), "deny", "read-only")
	if o := PreToolUse(h.helperIn("r1", HelperCodeReviewer, "mcp__atlassian__getJiraIssue", map[string]any{})); o != nil {
		t.Fatalf("helpers can read the ticket: %v", o)
	}
}

func TestSlackSendGated(t *testing.T) {
	h := newHarness(t)
	h.setup()
	send := map[string]any{"channel_id": "C1", "message": "hi"}
	h.expect(h.pre("mcp__claude_ai_Slack__slack_send_message", send), "ask", "")
	h.startTask("small")
	h.expect(h.pre("mcp__claude_ai_Slack__slack_send_message", send), "deny", "plan isn't approved")
	h.expect(h.pre("mcp__claude_ai_Slack__slack_read_channel", map[string]any{"channel_id": "C1"}), "", "")
	h.expect(h.pre("mcp__claude_ai_Claude_Docs__update", map[string]any{"payload": map[string]any{}}), "", "")
}

// companyTask: a company project's task on a Jira ticket, grounded and
// planned, ready for the checkpoint.
func companyTask(t *testing.T) *harness {
	h := newHarness(t)
	s := &Setup{Kind: "company", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	h.startTask("small")
	tk := h.task()
	tk.Ticket = "ABC-123"
	must(t, SaveTask(tk))
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, goodPlan)
	return h
}

func TestJiraTicketTask(t *testing.T) {
	h := companyTask(t)
	h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "read the ticket ABC-123")
	h.post("mcp__claude_ai_Atlassian__getJiraIssue", map[string]any{"issueIdOrKey": "ABC-123"}, map[string]any{})
	if h.task().Lookups["jira"].Count != 1 {
		t.Fatal("the Jira read wasn't recorded")
	}
	h.approve()
	h.redRun()

	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	_, err := Execute(h.repo, h.task(), KindTest, "", "./test.sh", nil, io.Discard)
	must(t, err)
	h.runHelper(HelperCodeReviewer, "fine\n")
	h.docs(DocReport, "# Report\n"+reportEnd+"## Review\n- fine\n## For you\n- Suggested status: In Review\n")
	if m := h.finishMissing(); !strings.Contains(m, "ABC-123-jira-comment.md") {
		t.Fatalf("a ticket task needs the drafted comment: %s", m)
	}
	h.docs("ABC-123-jira-comment.md", "Fixed the refund check — tests added.\n")
	if m := h.finishMissing(); !strings.Contains(m, "em dash") {
		t.Fatalf("em dash in the comment: %s", m)
	}
	h.docs("ABC-123-jira-comment.md", "Fixed the refund check. Tests added.\n\nCo-Authored-By: Claude <noreply@anthropic.com>\n")
	if m := h.finishMissing(); !strings.Contains(m, "AI markers") {
		t.Fatalf("AI marker in the comment: %s", m)
	}
	h.docs("ABC-123-jira-comment.md", "Fixed the refund check. A refunded order can't be refunded again. Tests added.\n")
	if m := h.finishMissing(); m != "" {
		t.Fatalf("drafted ticket task: %s", m)
	}
}

func TestJiraToolDown(t *testing.T) {
	h := companyTask(t)
	tk := h.task()
	tk.ToolDown = map[string]string{"jira": "the Jira server is down"}
	must(t, SaveTask(tk))
	h.approve()
}

func TestJiraTicketOnlyWhereItApplies(t *testing.T) {
	company := &Setup{Kind: "company"}
	if !company.JiraTicket("ABC-123") {
		t.Error("company ticket")
	}
	for _, c := range []struct {
		s      *Setup
		ticket string
	}{
		{&Setup{Kind: "personal"}, "ABC-123"},
		{company, ""},
		{company, "fix the bug"},
		{company, "ABC-123; rm -rf x"},
		{&Setup{Kind: "company", Parts: map[string]bool{"jira": false}}, "ABC-123"},
		{&Setup{Kind: "company", TicketRegex: `#\d+`}, "ABC-123"},
		{nil, "ABC-123"},
	} {
		if c.s.JiraTicket(c.ticket) {
			t.Errorf("%+v %q is not a Jira ticket task", c.s, c.ticket)
		}
	}
	if JiraCommentName("ABC-123") != "ABC-123-jira-comment.md" || JiraCommentName("a/b") != "a_b-jira-comment.md" {
		t.Error("comment file name")
	}
	for _, p := range []string{"okay write this in the ticket", "post it to Jira", "move ABC-1 to the done status", "add a comment"} {
		if !UserAsksJira(p) {
			t.Errorf("%q asks for Jira", p)
		}
	}
	for _, p := range []string{"fix the refund bug", "thanks", "stickets", "commentary"} {
		if UserAsksJira(p) {
			t.Errorf("%q doesn't ask for Jira", p)
		}
	}
}
