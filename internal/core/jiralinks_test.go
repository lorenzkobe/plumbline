package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Jira links: every link in the task's ticket is opened, or listed under
// ## Links with a reason and named in a question to the user, before the
// checkpoint.

const (
	jlFigma  = "https://www.figma.com/proto/AbC123xYz9/Checkout-flow?node-id=1-2"
	jlSpec   = "https://example.com/spec"
	jlDoc    = "https://docs.google.com/document/d/1xYz/edit"
	jlAttach = "https://acme.atlassian.net/rest/api/3/attachment/content/10042"
	jlThumb  = "https://acme.atlassian.net/rest/api/3/attachment/thumbnail/10042"
	jlWiki   = "https://acme.atlassian.net/wiki/spaces/ENG/pages/123456/Checkout+Spec"
	jlShort  = "https://acme.atlassian.net/wiki/x/AbCd"
	jlLinked = "https://acme.atlassian.net/browse/ABC-200"
)

const jlJiraRead = "mcp__claude_ai_Atlassian__getJiraIssue"

// jlIssue: a ticket as the Atlassian tools return it, with Jira's own URLs
// around the links people wrote in the description and a comment.
func jlIssue(key string, links ...string) map[string]any {
	desc, comment := "", ""
	for i, l := range links {
		if i%2 == 0 {
			desc += "See " + l + ". "
		} else {
			comment += "Also " + l + "\n"
		}
	}
	return map[string]any{
		"key":  key,
		"self": "https://acme.atlassian.net/rest/api/3/issue/10001",
		"fields": map[string]any{
			"description": desc,
			"issuetype":   map[string]any{"iconUrl": "https://acme.atlassian.net/images/icons/issuetypes/story.svg"},
			"assignee": map[string]any{"avatarUrls": map[string]any{
				"48x48": "https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/5b/48",
			}},
			"comment": map[string]any{"comments": []any{map[string]any{
				"self": "https://acme.atlassian.net/rest/api/3/issue/10001/comment/1",
				"body": comment,
			}}},
		},
	}
}

func (h *harness) jlRead(key string, resp any) {
	h.t.Helper()
	h.post(jlJiraRead, map[string]any{"cloudId": "acme", "issueIdOrKey": key}, resp)
}

func (h *harness) jlCheckpoint(want, contains string) {
	h.t.Helper()
	h.expect(h.pre("AskUserQuestion", checkpointQ()), want, contains)
}

func (h *harness) jlLinks() []JiraLink {
	h.t.Helper()
	return h.task().JiraLinks
}

func jlAsk(text string) map[string]any {
	return map[string]any{"questions": []any{map[string]any{"question": text, "header": "Links",
		"options": []any{map[string]any{"label": "I'll share screenshots"}, map[string]any{"label": "Go without it"}}}}}
}

func jlCheckpointNaming(link string) map[string]any {
	q := checkpointQ()
	q["questions"].([]any)[0].(map[string]any)["question"] = "Go? I couldn't open " + link + ": please share screenshots or give access."
	return q
}

func (h *harness) jlUnderstanding(links string) {
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n## Links\n"+links)
}

// C1, C2, C3
func TestJiraLinksRefusedUntilOpened(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma, jlSpec))
	h.jlCheckpoint("deny", "figma.com/proto/AbC123xYz9")
	h.jlCheckpoint("deny", "example.com/spec")

	h.post("WebFetch", map[string]any{"url": jlSpec, "prompt": "what does the spec say"}, map[string]any{"result": "spec"})
	_, r := decision(h.pre("AskUserQuestion", checkpointQ()))
	if strings.Contains(r, "example.com/spec") || !strings.Contains(r, "figma.com/proto") {
		t.Fatalf("the fetched spec counts as opened, the prototype not yet: %s", r)
	}
	h.post("mcp__figma__get_design_context", map[string]any{"fileKey": "AbC123xYz9", "nodeId": "1:2"}, map[string]any{"content": "frame"})
	h.approve()
}

// C6
func TestJiraLinksLoginWalledFetch(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma, jlDoc, jlWiki))
	for _, u := range []string{jlFigma, jlDoc, jlWiki} {
		h.post("WebFetch", map[string]any{"url": u, "prompt": "read"}, map[string]any{"result": "Sign in to continue"})
	}
	for _, l := range h.jlLinks() {
		if l.Opened {
			t.Errorf("a fetch of a page behind a login doesn't open it: %s", l.URL)
		}
	}
	h.jlCheckpoint("deny", "docs.google.com")
}

// C4, C5, C14
func TestJiraLinksListedAndAsked(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))

	h.jlUnderstanding("- " + jlFigma + "\n")
	h.jlCheckpoint("deny", "reason")

	h.jlUnderstanding("- " + jlFigma + ": private Figma file, the Figma tools aren't connected\n")
	h.jlCheckpoint("deny", "question")

	// The checkpoint question naming the link is the ask.
	h.expect(h.pre("AskUserQuestion", jlCheckpointNaming(jlFigma)), "", "")

	// An earlier question naming it counts for a plain checkpoint question.
	h.post("AskUserQuestion", jlAsk("I couldn't open "+jlFigma+". Can you share screenshots?"), map[string]any{"answers": map[string]any{}})
	h.approve()
}

// C15
func TestJiraLinksSmallTaskPlan(t *testing.T) {
	h := companyTask(t)
	must(t, os.Remove(filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocUnderstanding)))
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	h.jlCheckpoint("deny", "figma.com/proto")
	h.docs(DocPlan, "A returns 1 (src/a.go:3)\n## Links\n- "+jlFigma+": private Figma file, the user is sending screenshots\n"+goodPlan)
	h.expect(h.pre("AskUserQuestion", jlCheckpointNaming(jlFigma)), "", "")
}

// C7, C21
func TestJiraLinksSkipJiraOwnURLs(t *testing.T) {
	h := companyTask(t)
	resp := jlIssue("ABC-123")
	resp["fields"].(map[string]any)["description"] = "Own page https://acme.atlassian.net/browse/ABC-123 and the screenshot " + jlAttach
	resp["fields"].(map[string]any)["attachment"] = []any{map[string]any{
		"self": "https://acme.atlassian.net/rest/api/3/attachment/10042", "content": jlAttach, "thumbnail": jlThumb,
	}}
	h.jlRead("ABC-123", resp)
	got := h.jlLinks()
	if len(got) != 1 || !strings.Contains(got[0].URL, "/attachment/content/10042") {
		t.Fatalf("only the attachment is a link: %+v", got)
	}
	h.jlCheckpoint("deny", "attachment/content/10042")
	h.post("mcp__claude-in-chrome__navigate", map[string]any{"url": jlAttach, "tabId": 1}, map[string]any{"content": "image"})
	h.approve()
}

// C8
func TestJiraLinksFoundAndMatchedLoosely(t *testing.T) {
	h := companyTask(t)
	inner, _ := json.Marshal(map[string]any{"key": "ABC-123", "fields": map[string]any{
		"description": "Spec: http://Example.com/spec/#top.",
		"comment":     map[string]any{"comments": []any{map[string]any{"body": "Proto (" + jlFigma + ")"}}},
	}})
	h.jlRead("ABC-123", []any{map[string]any{"type": "text", "text": string(inner)}})
	if n := len(h.jlLinks()); n != 2 {
		t.Fatalf("both links, from JSON inside the text: %+v", h.jlLinks())
	}
	h.post("WebFetch", map[string]any{"url": "https://example.com/spec", "prompt": "read"}, map[string]any{"result": "spec"})
	h.post("mcp__claude-in-chrome__navigate", map[string]any{"url": jlFigma + "#frame", "tabId": 1}, map[string]any{"content": "page"})
	for _, l := range h.jlLinks() {
		if !l.Opened {
			t.Errorf("not matched: %s", l.URL)
		}
	}
	h.approve()
}

func jlCompanySetup(h *harness) {
	h.t.Helper()
	s := &Setup{Kind: "company", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(h.t, err)
}

func (h *harness) jlStartTicket(ticket string) *Task {
	h.t.Helper()
	pre, _ := DirtyFiles(h.repo)
	tk := &Task{ID: NewID(), Title: "fix A", Size: "small", Ticket: ticket, Root: h.repo, Branch: CurrentBranch(h.repo), Phase: PhaseUnderstanding, Created: Now(), Preexisting: pre}
	must(h.t, os.MkdirAll(TaskDocsDir(h.repo, tk.ID), 0o700))
	must(h.t, SaveTask(tk))
	h.post("Bash", map[string]any{"command": "plumbline task start --title 'fix A'"}, map[string]any{"stdout": "plumbline-task-id: " + tk.ID + "\n"})
	return h.task()
}

// C9, C20
func TestJiraLinksBeforeTaskStart(t *testing.T) {
	h := newHarness(t)
	jlCompanySetup(h)
	old := Now
	t.Cleanup(func() { Now = old })
	Now = func() time.Time { return old().Add(-2 * time.Hour) }
	h.jlRead("ABC-123", jlIssue("ABC-123", "https://example.com/stale"))
	Now = old
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec, jlFigma))
	h.jlRead("ABC-999", jlIssue("ABC-999", "https://example.com/other"))
	h.post("WebFetch", map[string]any{"url": jlSpec, "prompt": "read"}, map[string]any{"result": "spec"})
	h.post("AskUserQuestion", jlAsk("Can you share "+jlFigma+"?"), map[string]any{"answers": map[string]any{}})

	tk := h.jlStartTicket("ABC-123")
	byURL := map[string]JiraLink{}
	for _, l := range tk.JiraLinks {
		byURL[l.URL] = l
	}
	if len(byURL) != 2 {
		t.Fatalf("the ticket's two links, not the stale read's or ABC-999's: %+v", tk.JiraLinks)
	}
	for u, l := range byURL {
		switch {
		case strings.Contains(u, "example.com/spec") && !l.Opened:
			t.Error("the fetch before the start opens the spec")
		case strings.Contains(u, "figma.com") && (!l.Asked || l.Opened):
			t.Error("the question before the start asks about the prototype")
		}
	}
}

// C20: adopt merges them too.
func TestJiraLinksBeforeAdopt(t *testing.T) {
	h := newHarness(t)
	jlCompanySetup(h)
	tk := &Task{ID: NewID(), Title: "fix A", Size: "small", Ticket: "ABC-123", Root: h.repo, Branch: CurrentBranch(h.repo), Phase: PhaseUnderstanding, Created: Now(), SessionID: "sess-old"}
	must(t, os.MkdirAll(TaskDocsDir(h.repo, tk.ID), 0o700))
	must(t, SaveTask(tk))
	h.jlRead("abc-123", jlIssue("ABC-123", jlFigma))
	h.post("Bash", map[string]any{"command": "plumbline task adopt " + tk.ID}, map[string]any{"stdout": "plumbline-adopt-id: " + tk.ID + "\n"})
	if got := h.task(); got.ID != tk.ID || len(got.JiraLinks) != 1 {
		t.Fatalf("adopt carries the links read before it: %+v", got.JiraLinks)
	}
}

// C10
func TestJiraLinksHelpersDontCount(t *testing.T) {
	h := companyTask(t)
	in := h.helperIn("agent-1", HelperTracer, jlJiraRead, map[string]any{"issueIdOrKey": "ABC-123"})
	in.ToolResponse, _ = json.Marshal(jlIssue("ABC-123", "https://example.com/helper"))
	PostToolUse(in)
	if n := len(h.jlLinks()); n != 0 {
		t.Fatalf("a helper's read adds no links: %d", n)
	}
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec))
	in = h.helperIn("agent-1", HelperTracer, "WebFetch", map[string]any{"url": jlSpec})
	in.ToolResponse, _ = json.Marshal(map[string]any{"result": "spec"})
	PostToolUse(in)
	if h.jlLinks()[0].Opened {
		t.Fatal("a helper's fetch doesn't open a link for the lead")
	}
	h.jlCheckpoint("deny", "example.com/spec")
}

// C11, C18
func TestJiraLinksReadsOpenOnlyByKey(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlLinked, jlWiki, jlShort, jlFigma))
	h.post("mcp__figma__get_metadata", map[string]any{"fileKey": "proto"}, map[string]any{"content": "x"})
	h.post("mcp__figma__get_metadata", map[string]any{"fileKey": "Checkout-flow"}, map[string]any{"content": "x"})
	h.post("mcp__claude_ai_Atlassian__getConfluencePage", map[string]any{"spaceKey": "ENG"}, map[string]any{"content": "x"})
	h.post("mcp__claude_ai_Atlassian__getConfluencePage", map[string]any{"pageId": "AbCd"}, map[string]any{"content": "x"})
	for _, l := range h.jlLinks() {
		if l.Opened {
			t.Errorf("a word from the path isn't the key: %s", l.URL)
		}
	}
	h.jlRead("abc-200", jlIssue("ABC-200", "https://example.com/linked-ticket-link"))
	h.post("mcp__claude_ai_Atlassian__getConfluencePage", map[string]any{"cloudId": "acme", "pageId": "123456"}, map[string]any{"content": "x"})
	h.post("mcp__figma__get_metadata", map[string]any{"fileKey": "AbC123xYz9"}, map[string]any{"content": "x"})
	for _, l := range h.jlLinks() {
		if want := l.Norm != normLink(jlShort); l.Opened != want {
			t.Errorf("%s opened = %v, want %v", l.URL, l.Opened, want)
		}
	}
	if n := len(h.jlLinks()); n != 4 {
		t.Fatalf("the linked ticket's own links aren't this ticket's: %+v", h.jlLinks())
	}
	h.jlCheckpoint("deny", "wiki/x/AbCd")
}

// C12
func TestJiraLinksFailedFetch(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec))
	in := h.in("WebFetch", map[string]any{"url": jlSpec, "prompt": "read"}, nil)
	PostToolUseFailure(in)
	if h.jlLinks()[0].Opened {
		t.Fatal("a failed fetch opens nothing")
	}
	h.jlCheckpoint("deny", "example.com/spec")
}

// C13
func TestJiraLinksOnlyOnJiraTickets(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.jlStartTicket("ABC-123")
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, goodPlan)
	h.approve() // a personal project: no Jira rules

	h2 := companyTask(t)
	tk := h2.task()
	tk.ToolDown = map[string]string{"jira": "the Jira server is down"}
	must(t, SaveTask(tk))
	h2.approve() // Jira down and nothing read: nothing to open

	h3 := companyTask(t)
	h3.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	tk = h3.task()
	tk.ToolDown = map[string]string{"jira": "went down after the read"}
	must(t, SaveTask(tk))
	h3.jlCheckpoint("deny", "figma.com/proto") // links already read still count
}

// C14, C22
func TestJiraLinksPlanHint(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	plan := filepath.Join(TaskDocsDir(h.repo, h.task().ID), DocPlan)
	ctx := contextOf(h.post("Write", map[string]any{"file_path": plan}, nil))
	if !strings.Contains(ctx, "still needed") || !strings.Contains(ctx, "figma.com/proto") {
		t.Fatalf("an unopened, unlisted link is still needed: %s", ctx)
	}
	h.jlUnderstanding("- " + jlFigma + ": private Figma file, the Figma tools aren't connected\n")
	ctx = contextOf(h.post("Write", map[string]any{"file_path": plan}, nil))
	if !strings.Contains(ctx, "has what the plan question needs") || !strings.Contains(ctx, "figma.com/proto") {
		t.Fatalf("listed, unasked: ready, and the question must name it: %s", ctx)
	}
}

// C16
func TestJiraLinksReadTwice(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec, jlFigma))
	h.post("WebFetch", map[string]any{"url": jlSpec, "prompt": "read"}, map[string]any{"result": "spec"})
	h.post("AskUserQuestion", jlAsk("Share "+jlFigma+"?"), map[string]any{"answers": map[string]any{}})
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec, jlFigma))
	got := h.jlLinks()
	if len(got) != 2 {
		t.Fatalf("no duplicates: %+v", got)
	}
	for _, l := range got {
		if !l.Opened && !l.Asked {
			t.Errorf("a second read keeps what was done: %+v", l)
		}
	}
}

// C17
func TestJiraLinksLookupsDontOpen(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlSpec))
	h.post("mcp__mempalace__mempalace_search", map[string]any{"query": jlSpec}, map[string]any{"content": "x"})
	h.post("mcp__socraticode__codebase_search", map[string]any{"query": jlSpec}, map[string]any{"content": "x"})
	h.post("mcp__context7__query-docs", map[string]any{"query": jlSpec}, map[string]any{"content": "x"})
	if h.jlLinks()[0].Opened {
		t.Fatal("a search naming the link doesn't open it")
	}
}

// C19
func TestJiraLinksTicketKeyExact(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-1234", jlIssue("ABC-1234", "https://example.com/a"))
	h.post("mcp__claude_ai_Atlassian__searchJiraIssuesUsingJql", map[string]any{"jql": "key in (ABC-123, ABC-124)"}, jlIssue("ABC-124", "https://example.com/b"))
	if n := len(h.jlLinks()); n != 0 {
		t.Fatalf("other tickets' links: %+v", h.jlLinks())
	}
	h.jlRead(" abc-123 ", jlIssue("ABC-123", jlSpec))
	if n := len(h.jlLinks()); n != 1 {
		t.Fatalf("the ticket's key in any case: %+v", h.jlLinks())
	}
}

func TestNormLink(t *testing.T) {
	for in, want := range map[string]string{
		"HTTP://Example.COM/Spec/#top": "example.com/Spec",
		"https://example.com/spec":     "example.com/spec",
		"https://example.com/a?b=1#c":  "example.com/a?b=1",
		"https://example.com/":         "example.com",
	} {
		if got := normLink(in); got != want {
			t.Errorf("normLink(%q) = %q, want %q", in, got, want)
		}
	}
}

// Review H2.1: HTML text keeps entities out of the link, and the web UI's
// own pages (thumbnails, profiles) aren't links.
func TestJiraLinksFromHTML(t *testing.T) {
	h := companyTask(t)
	resp := jlIssue("ABC-123")
	resp["renderedFields"] = map[string]any{"description": `<p><a href="https://example.com/a?x=1&amp;y=2">spec</a> ` +
		`<img src="https://acme.atlassian.net/secure/thumbnail/10042/shot.png"> ` +
		`<a href="https://acme.atlassian.net/secure/ViewProfile.jspa?accountId=5b1">@Ann</a> ` +
		`<a href="https://acme.atlassian.net/jira/people/5b1">Ann</a></p>`}
	h.jlRead("ABC-123", resp)
	got := h.jlLinks()
	if len(got) != 1 || got[0].Norm != "example.com/a?x=1&y=2" {
		t.Fatalf("one link, unescaped: %+v", got)
	}
	h.post("WebFetch", map[string]any{"url": "https://example.com/a?x=1&y=2", "prompt": "read"}, map[string]any{"code": 200, "result": "spec"})
	h.approve()
}

// Review H2.3, H3.2: a fetch that only reports a redirect, or answers with
// an error status, opens nothing; nor does a fetch of a self-hosted Jira page.
func TestJiraLinksFetchRedirectOrError(t *testing.T) {
	h := companyTask(t)
	resp := jlIssue("ABC-123", "https://bit.ly/3abc", jlSpec, "https://jira.acme.internal/browse/ABC-7")
	resp["self"] = "https://jira.acme.internal/rest/api/2/issue/10001" // a self-hosted Jira
	h.jlRead("ABC-123", resp)
	h.post("WebFetch", map[string]any{"url": "https://bit.ly/3abc", "prompt": "read"},
		map[string]any{"code": 301, "result": "REDIRECT DETECTED: The URL redirects to a different host.\n\nRedirect URL: https://www.loom.com/share/x"})
	h.post("WebFetch", map[string]any{"url": jlSpec, "prompt": "read"}, map[string]any{"code": 404, "result": "Not Found"})
	h.post("WebFetch", map[string]any{"url": "https://jira.acme.internal/browse/ABC-7", "prompt": "read"}, map[string]any{"code": 200, "result": "Log in"})
	for _, l := range h.jlLinks() {
		if l.Opened {
			t.Errorf("not opened: %s", l.URL)
		}
	}
	h.jlCheckpoint("deny", "bit.ly/3abc")
}

// Review H2.4: a linked page opened before the ticket read counts.
func TestJiraLinksOpenedBeforeTicketRead(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-200", jlIssue("ABC-200"))
	h.post("WebFetch", map[string]any{"url": jlSpec, "prompt": "read"}, map[string]any{"code": 200, "result": "spec"})
	h.post("AskUserQuestion", jlAsk("Can you share "+jlFigma+"?"), map[string]any{"answers": map[string]any{}})
	h.jlRead("ABC-123", jlIssue("ABC-123", jlLinked, jlSpec, jlFigma))
	for _, l := range h.jlLinks() {
		if !l.Opened && !l.Asked {
			t.Errorf("done before the read, still counts: %+v", l)
		}
	}
}

// Review H3.1: a read by numeric id meets "read the ticket" but can't tie
// its links to it, so the checkpoint asks for a read by key.
func TestJiraLinksReadByIDRefused(t *testing.T) {
	h := companyTask(t)
	h.post(jlJiraRead, map[string]any{"issueIdOrKey": "10001"}, jlIssue("ABC-123", jlFigma))
	h.jlCheckpoint("deny", `"issueIdOrKey": "ABC-123"`)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	h.jlCheckpoint("deny", "figma.com/proto")
}

// Review H3.3: the refusal names the browser that can run before approval.
func TestJiraLinksRefusalNamesChrome(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	h.jlCheckpoint("deny", "Claude in Chrome")
}

// Review H4.1: escaped brackets around or after a link stay out of it.
func TestJiraLinksEscapedBrackets(t *testing.T) {
	for _, text := range []string{
		"see &lt;https://example.com/spec&gt; now",
		"<p>https://example.com/spec&lt;/p&gt;</p>",
		"(see https://example.com/spec&#41;",
	} {
		got := responseLinks(json.RawMessage(`{"description":` + strconvQuote(text) + `}`))
		if len(got) != 1 || normLink(got[0]) != "example.com/spec" {
			t.Errorf("%q: %q", text, got)
		}
	}
}

func strconvQuote(s string) string { b, _ := json.Marshal(s); return string(b) }

// Review H5.1: the Jira and Confluence path rule is for the ticket's own
// site (self-hosted included), not every public /wiki/ or /browse/ page.
func TestJiraLinksPublicWikiPagesFetch(t *testing.T) {
	h := companyTask(t)
	public := []string{"https://en.wikipedia.org/wiki/Luhn_algorithm", "https://cwiki.apache.org/confluence/display/KAFKA/KIP-500", "https://issues.apache.org/jira/browse/KAFKA-123"}
	own := "https://jira.acme.internal/browse/ABC-7"
	resp := jlIssue("ABC-123", append(append([]string{}, public...), own)...)
	resp["self"] = "https://jira.acme.internal/rest/api/2/issue/10001"
	h.jlRead("ABC-123", resp)
	for _, u := range append(append([]string{}, public...), own) {
		h.post("WebFetch", map[string]any{"url": u, "prompt": "read"}, map[string]any{"code": 200, "result": "page"})
	}
	for _, l := range h.jlLinks() {
		if want := l.Norm != normLink(own); l.Opened != want {
			t.Errorf("%s opened = %v, want %v", l.URL, l.Opened, want)
		}
	}
}

// Review H5.2: a link's own balanced parentheses stay in it.
func TestJiraLinksParentheses(t *testing.T) {
	for text, want := range map[string]string{
		"see https://en.wikipedia.org/wiki/Foo_(bar) now":       "en.wikipedia.org/wiki/Foo_(bar)",
		"(docs: https://learn.microsoft.com/ms123456(v=vs.85))": "learn.microsoft.com/ms123456(v=vs.85)",
		"[spec](https://example.com/spec)":                      "example.com/spec",
		"(see https://example.com/spec).":                       "example.com/spec",
	} {
		got := responseLinks(json.RawMessage(`{"description":` + strconvQuote(text) + `}`))
		if len(got) != 1 || normLink(got[0]) != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

// Review H6.1: a self-hosted Confluence beside the Jira site, and a response
// that escapes its slashes, still count as the site's own.
func TestJiraLinksSiblingConfluenceHost(t *testing.T) {
	h := companyTask(t)
	conf := "https://confluence.acme.internal/display/ENG/Spec"
	h.post(jlJiraRead, map[string]any{"issueIdOrKey": "ABC-123"},
		[]any{map[string]any{"type": "text", "text": `{"self":"https:\/\/jira.acme.internal\/rest\/api\/2\/issue\/1","fields":{"description":"See ` + conf + `"}}`}})
	h.post("WebFetch", map[string]any{"url": conf, "prompt": "read"}, map[string]any{"code": 200, "result": "Log in"})
	if l := h.jlLinks(); len(l) != 1 || l[0].Opened {
		t.Fatalf("a Confluence page next to the Jira site needs a sign-in: %+v", l)
	}
}

// Review H6.2: a reason is three words with letters or digits, not table
// bars or dashes.
func TestJiraLinksReasonWords(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", jlFigma))
	for _, line := range []string{"| " + jlFigma + " | x |", "- " + jlFigma + " — needs login"} {
		h.jlUnderstanding(line + "\n")
		h.expect(h.pre("AskUserQuestion", jlCheckpointNaming(jlFigma)), "deny", "reason")
	}
	h.jlUnderstanding("| " + jlFigma + " | private file, no Figma access |\n")
	h.expect(h.pre("AskUserQuestion", jlCheckpointNaming(jlFigma)), "", "")
}

// Review H7.2: Jira's machinery paths are Jira's only on its own site; an
// API reference on another host is a link someone wrote.
func TestJiraLinksAPIDocsOnOtherHosts(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", jlIssue("ABC-123", "https://docs.github.com/en/rest/pulls/pulls", "https://vendor.example/secure/guide"))
	if n := len(h.jlLinks()); n != 2 {
		t.Fatalf("both vendor links are required: %+v", h.jlLinks())
	}
}

// Review H9.1: only the exact Jira site's machinery is skipped; a sibling
// host's /rest/ or /secure/ page is a link someone wrote.
func TestJiraLinksSiblingHostAPIDocs(t *testing.T) {
	h := companyTask(t)
	resp := jlIssue("ABC-123", "https://developer.acme.com/rest/v2/orders", "https://docs.acme.com/secure/payments", "https://jira.acme.com/secure/useravatar?id=1")
	resp["self"] = "https://jira.acme.com/rest/api/2/issue/10001"
	h.jlRead("ABC-123", resp)
	if n := len(h.jlLinks()); n != 2 {
		t.Fatalf("the two sibling-host pages are links, the site's avatar isn't: %+v", h.jlLinks())
	}
}

// Review H10.1: ports don't hide a sibling host.
func TestJiraLinksPortedHosts(t *testing.T) {
	h := companyTask(t)
	conf := "https://confluence.acme.com:8090/display/ENG/Spec"
	resp := jlIssue("ABC-123", conf)
	resp["self"] = "https://jira.acme.com:8080/rest/api/2/issue/10001"
	h.jlRead("ABC-123", resp)
	h.post("WebFetch", map[string]any{"url": conf, "prompt": "read"}, map[string]any{"code": 200, "result": "Log in"})
	if l := h.jlLinks(); len(l) != 1 || l[0].Opened {
		t.Fatalf("a ported Confluence beside the Jira site needs a sign-in: %+v", l)
	}
}

// Review H10.2: with no self value, the ticket's own page still isn't a link.
func TestJiraLinksOwnPageWithoutSite(t *testing.T) {
	h := companyTask(t)
	h.jlRead("ABC-123", map[string]any{"key": "ABC-123", "fields": map[string]any{"description": "Own page https://jira.acme.com/browse/ABC-123, spec " + jlSpec}})
	if l := h.jlLinks(); len(l) != 1 || l[0].Norm != normLink(jlSpec) {
		t.Fatalf("only the spec: %+v", l)
	}
}

// Review H10.3: api.atlassian.com and a public suffix don't widen the site.
func TestJiraLinksSiteParents(t *testing.T) {
	if onJiraSite("developer.atlassian.com", []string{"api.atlassian.com"}) {
		t.Error("api.atlassian.com isn't a Jira site")
	}
	if onJiraSite("bbc.co.uk", []string{"acme.co.uk"}) || jiraSibling("www.bbc.co.uk", []string{"acme.co.uk"}) {
		t.Error("co.uk isn't one company's domain")
	}
	if !jiraSibling("confluence.acme.co.uk", []string{"jira.acme.co.uk"}) {
		t.Error("a sibling under acme.co.uk")
	}
}
