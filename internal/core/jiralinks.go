package core

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Jira links: a ticket's prototypes, specs and linked pages are part of
// the task. Before the checkpoint each link in the task's ticket is opened,
// or listed under ## Links with why it couldn't be, and named in a question
// to the user. Only the lead's successful calls count.

type JiraLink struct {
	URL    string `json:"url"`  // as the ticket wrote it
	Norm   string `json:"norm"` // normLink(URL)
	Opened bool   `json:"opened,omitempty"`
	Asked  bool   `json:"asked,omitempty"`
}

// linkEvent: one lead call that can add, open or ask about a link. The same
// events are applied live and, for calls made before the task started,
// replayed from the session file at bind.
type linkEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // read, open, ask
	Tool   string    `json:"tool,omitempty"`
	Values []string  `json:"values,omitempty"`
	Links  []string  `json:"links,omitempty"`
	Sites  []string  `json:"sites,omitempty"` // a read's Jira site hosts
}

var reLinkURL = regexp.MustCompile("(?i)https?://[^\\s\"'<>`\\\\\\[\\]{}|^]+")

// findLinks: the URLs in text, without trailing punctuation or a closing
// parenthesis the link didn't open (Foo_(bar) keeps its own).
func findLinks(text string) []string {
	var out []string
	for _, m := range reLinkURL.FindAllString(text, -1) {
		for {
			m = strings.TrimRight(m, ".,;:!?*_~'\"")
			if !strings.HasSuffix(m, ")") || strings.Count(m, "(") >= strings.Count(m, ")") {
				break
			}
			m = m[:len(m)-1]
		}
		out = append(out, m)
	}
	return out
}

// normLink: http and https, host case and a leading www. don't matter, nor
// a fragment or trailing slash; path and query do.
func normLink(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.ToLower(raw)
	}
	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")
	n := host + strings.TrimRight(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		n += "?" + u.RawQuery
	}
	return n
}

// Keys whose values are Jira's own machinery, never a link someone wrote.
var linkSkipKeys = set("self iconurl avatarurls avatarurl thumbnail")

// jiraOwnURL: API, avatar and icon URLs, and the ticket's own page.
// Attachment content stays: an attachment is part of the ticket.
func jiraOwnURL(raw, ticket string, sites []string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	host, path := hostOnly(u.Host), strings.ToLower(u.Path)
	switch {
	case ticket != "" && strings.HasSuffix(strings.TrimRight(path, "/"), "/browse/"+strings.ToLower(ticket)):
		return true // the ticket's own page, on whatever host
	case strings.HasSuffix(host, "atl-paas.net"), strings.Contains(host, "gravatar.com"):
		return true
	case !onJiraSite(host, sites):
		return false // an API reference or guide elsewhere is a link someone wrote
	case strings.Contains(path, "/rest/") && !strings.Contains(path, "/attachment/content/"):
		return true
	case strings.Contains(path, "/images/icons/"), strings.Contains(path, "/secure/") && !strings.Contains(path, "/secure/attachment/"):
		return true // avatars, thumbnails, profiles: the web UI's own pages
	case strings.Contains(path, "/jira/people/"), strings.Contains(path, "/wiki/people/"):
		return true
	}
	return false
}

// hostOnly: a host without its port or a leading www., lowercased.
func hostOnly(h string) string {
	h = strings.ToLower(h)
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		h = h[:i]
	}
	return strings.TrimPrefix(h, "www.")
}

// onJiraSite: the ticket's Jira site itself: Atlassian Cloud, or a host its
// self links name.
func onJiraSite(host string, sites []string) bool {
	host = hostOnly(host)
	return host == "atlassian.net" || strings.HasSuffix(host, ".atlassian.net") || contains(sites, host)
}

// jiraSibling: the site or a host beside it under the same company domain,
// such as a self-hosted Confluence next to a self-hosted Jira.
func jiraSibling(host string, sites []string) bool {
	if onJiraSite(host, sites) {
		return true
	}
	p := siteParent(hostOnly(host))
	if p == "" {
		return false
	}
	for _, s := range sites {
		if siteParent(s) == p {
			return true
		}
	}
	return false
}

// siteParent: the company domain after the first label (jira.acme.com:
// acme.com), or "" when that would be a public suffix (acme.co.uk: co.uk)
// or a bare top-level domain.
func siteParent(h string) string {
	parts := strings.Split(h, ".")
	if len(parts) < 3 {
		return ""
	}
	parts = parts[1:]
	if len(parts) == 2 && len(parts[1]) == 2 && len(parts[0]) <= 3 {
		return "" // co.uk, com.au, co.jp
	}
	return strings.Join(parts, ".")
}

// responseLinks: every URL in the response's strings, JSON inside a text
// field included, apart from the values of Jira's machinery keys.
func responseLinks(raw json.RawMessage) []string {
	links, _ := readResponse(raw)
	return links
}

// readResponse: the links, and the Jira site hosts that the response's self
// values name.
func readResponse(raw json.RawMessage) (out, sites []string) {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		v = string(raw)
	}
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 64 {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for k, e := range x {
				if self, ok := e.(string); ok && strings.EqualFold(k, "self") {
					if u, err := url.Parse(self); err == nil && u.Host != "" {
						// api.atlassian.com fronts every Cloud site, already known by .atlassian.net
						if h := hostOnly(u.Host); h != "" && !strings.HasSuffix(h, "atlassian.com") && !contains(sites, h) {
							sites = append(sites, h)
						}
					}
				}
				if !linkSkipKeys[strings.ToLower(k)] {
					walk(e, depth+1)
				}
			}
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		case string:
			t := strings.TrimSpace(x)
			if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
				var inner any
				if json.Unmarshal([]byte(t), &inner) == nil {
					walk(inner, depth+1)
					return
				}
			}
			out = append(out, findLinks(html.UnescapeString(x))...) // decoded first, so &gt; ends a link as > does
		}
	}
	walk(v, 0)
	return out, sites
}

// inputStrings: every string in a tool's input.
func inputStrings(raw json.RawMessage) []string {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 64 {
			return
		}
		switch x := v.(type) {
		case map[string]any:
			for _, e := range x {
				walk(e, depth+1)
			}
		case []any:
			for _, e := range x {
				walk(e, depth+1)
			}
		case string:
			out = append(out, x)
		}
	}
	walk(v, 0)
	return out
}

// Hosts whose pages need a sign-in WebFetch doesn't have: a fetch gets the
// login page. Their own MCP tools or Claude in Chrome (the user's sign-ins)
// open them. On the ticket's own Jira site (sites: hosts of its API URLs,
// self-hosted included) the Jira and Confluence paths need one too.
var loginWalled = []string{"figma.com", "atlassian.net", "docs.google.com", "drive.google.com", "notion.so", "miro.com", "invisionapp.com", "zeplin.io", "sharepoint.com"}

func loginWalledHost(n string, sites []string) bool {
	host, path, _ := strings.Cut(n, "/")
	host = hostOnly(host)
	for _, p := range []string{"browse/", "wiki/", "display/", "confluence/", "secure/", "jira/", "plugins/servlet/"} {
		if strings.HasPrefix(strings.ToLower(path), p) && jiraSibling(host, sites) {
			return true
		}
	}
	for _, h := range loginWalled {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// linkOpener: the tools that open a page. Lookup servers (memory, code
// search, docs) only search, whatever their query holds.
func linkOpener(tool string) string {
	switch {
	case tool == "WebFetch":
		return "fetch"
	case !strings.HasPrefix(tool, "mcp__"):
		return ""
	}
	if jira, read := JiraTool(tool); jira {
		if read {
			return "jira"
		}
		return ""
	}
	server, _ := mcpSplit(tool)
	low := strings.ToLower(server)
	switch {
	case strings.Contains(low, "figma"):
		return "figma"
	case strings.Contains(low, "chrome"), strings.Contains(low, "browser"), strings.Contains(low, "playwright"):
		return "browser"
	}
	return ""
}

// figmaFileKey: the segment after /file/, /design/, /proto/, /board/ or
// /slides/ of a figma.com link.
func figmaFileKey(n string) string {
	host, path, _ := strings.Cut(n, "/")
	if host != "figma.com" && !strings.HasSuffix(host, ".figma.com") {
		return ""
	}
	parts := strings.Split(strings.SplitN(path, "?", 2)[0], "/")
	if len(parts) >= 2 && set("file design proto board slides")[parts[0]] {
		return parts[1]
	}
	return ""
}

// jiraLinkIDs: the ticket key of a /browse/ link and the page id of a
// Confluence /wiki/.../pages/<id> link; a /wiki/x/ short link has neither.
func jiraLinkIDs(n string) (key, page string) {
	_, path, _ := strings.Cut(strings.SplitN(n, "?", 2)[0], "/")
	parts := strings.Split(path, "/")
	for i := 0; i+1 < len(parts); i++ {
		switch {
		case parts[i] == "browse" && reTicketKey.MatchString(strings.ToUpper(parts[i+1])):
			key = parts[i+1]
		case parts[i] == "pages" && len(parts) > 0 && parts[0] == "wiki" && isDigits(parts[i+1]):
			page = parts[i+1]
		}
	}
	return key, page
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func opensLink(kind string, values []string, l JiraLink, sites []string) bool {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch kind {
		case "fetch":
			if normLink(v) == l.Norm && !loginWalledHost(l.Norm, sites) {
				return true
			}
		case "browser":
			if strings.Contains(strings.ToLower(v), "://") && normLink(v) == l.Norm {
				return true
			}
		case "figma":
			if strings.Contains(v, "://") && normLink(v) == l.Norm || v == figmaFileKey(l.Norm) && v != "" {
				return true
			}
		case "jira":
			key, page := jiraLinkIDs(l.Norm)
			if key != "" && strings.EqualFold(v, key) || page != "" && v == page {
				return true
			}
		}
	}
	return false
}

// linkEventOf: the event a lead call makes, or nil.
func linkEventOf(in *HookInput) *linkEvent {
	if in.ToolName == "AskUserQuestion" {
		vals := inputStrings(in.ToolInput)
		if !anyHasURL(vals) {
			return nil
		}
		return &linkEvent{At: Now(), Kind: "ask", Values: vals}
	}
	kind := linkOpener(in.ToolName)
	if kind == "" {
		return nil
	}
	vals := inputStrings(in.ToolInput)
	if kind == "fetch" && fetchFailed(in.ToolResponse) {
		return nil
	}
	if kind == "jira" {
		links, sites := readResponse(in.ToolResponse)
		return &linkEvent{At: Now(), Kind: "read", Tool: kind, Values: vals, Links: links, Sites: sites}
	}
	if kind != "figma" && !anyHasURL(vals) {
		return nil
	}
	return &linkEvent{At: Now(), Kind: "open", Tool: kind, Values: vals}
}

// fetchFailed: a WebFetch that answered with a status outside 2xx, or only
// reported a redirect to another host, which it doesn't follow.
func fetchFailed(raw json.RawMessage) bool {
	var m map[string]any
	if json.Unmarshal(raw, &m) == nil {
		if code, ok := m["code"].(float64); ok && (code < 200 || code >= 300) {
			return true
		}
	}
	low := strings.ToLower(responseText(raw))
	return strings.Contains(low, "redirect detected") || strings.Contains(low, "redirects to a different host")
}

func anyHasURL(vals []string) bool {
	for _, v := range vals {
		if reLinkURL.MatchString(v) {
			return true
		}
	}
	return false
}

// maxLinkLog: the opens and questions kept to replay on links a later read
// adds (a linked page opened before the ticket was read).
const maxLinkLog = 300

// applyLinkEvent: a read naming the ticket's key (the whole value, any
// case) adds its links, and the earlier opens and questions are replayed on
// them; reads and opens mark what they open; a question naming a link asks
// about it.
func (t *Task) applyLinkEvent(e linkEvent) {
	ticket := strings.TrimSpace(t.Ticket)
	if ticket == "" {
		return
	}
	if e.Kind == "read" {
		ours := false
		for _, v := range e.Values {
			ours = ours || strings.EqualFold(strings.TrimSpace(v), ticket)
		}
		if ours {
			t.JiraRead = true
			for _, h := range e.Sites {
				if !contains(t.JiraSites, h) {
					t.JiraSites = append(t.JiraSites, h)
				}
			}
			for _, raw := range e.Links {
				if jiraOwnURL(raw, ticket, t.JiraSites) {
					continue
				}
				n := normLink(raw)
				dup := false
				for _, l := range t.JiraLinks {
					dup = dup || l.Norm == n
				}
				if dup {
					continue
				}
				l := JiraLink{URL: raw, Norm: n}
				for _, old := range t.JiraLinkLog {
					l = markLink(l, old, t.JiraSites)
				}
				t.JiraLinks = append(t.JiraLinks, l)
			}
		}
	}
	for i := range t.JiraLinks {
		t.JiraLinks[i] = markLink(t.JiraLinks[i], e, t.JiraSites)
	}
	e.Links, e.Sites = nil, nil
	t.JiraLinkLog = append(t.JiraLinkLog, e)
	if n := len(t.JiraLinkLog); n > maxLinkLog {
		t.JiraLinkLog = t.JiraLinkLog[n-maxLinkLog:]
	}
}

func markLink(l JiraLink, e linkEvent, sites []string) JiraLink {
	switch e.Kind {
	case "read", "open":
		l.Opened = l.Opened || opensLink(e.Tool, e.Values, l, sites)
	case "ask":
		l.Asked = l.Asked || textNamesLink(strings.Join(e.Values, "\n"), l)
	}
	return l
}

func textNamesLink(text string, l JiraLink) bool {
	for _, m := range findLinks(text) {
		if normLink(m) == l.Norm {
			return true
		}
	}
	return false
}

func sessionLinksPath(sessionID string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+".links.json")
}

// noteSessionLinkEvent: a call made while no task was open, kept for the
// task this session starts or adopts next. Parallel hooks share the file,
// so it's locked.
func noteSessionLinkEvent(sessionID string, e linkEvent) {
	if sessionID == "" {
		return
	}
	p := sessionLinksPath(sessionID)
	unlock := lockFile(p + ".lock")
	defer unlock()
	var old, evs []linkEvent
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &old)
	}
	for _, o := range old {
		if Now().Sub(o.At) <= sessionLookupWindow {
			evs = append(evs, o)
		}
	}
	evs = append(evs, e)
	b, _ := json.Marshal(evs)
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = writeFileAtomic(p, b)
}

// takeSessionLinkEvents: the events of the last sessionLookupWindow, in
// order, cleared so each counts for one task at most.
func takeSessionLinkEvents(sessionID string) []linkEvent {
	p := sessionLinksPath(sessionID)
	unlock := lockFile(p + ".lock")
	defer unlock()
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	_ = os.Remove(p)
	var evs, out []linkEvent
	if json.Unmarshal(b, &evs) != nil {
		return nil
	}
	for _, e := range evs {
		if Now().Sub(e.At) <= sessionLookupWindow {
			out = append(out, e)
		}
	}
	return out
}

// noteLinkEvent: PostToolUse, the lead's calls only.
func (c *hookCtx) noteLinkEvent() {
	if c.in.AgentID != "" {
		return
	}
	e := linkEventOf(c.in)
	if e == nil {
		return
	}
	if c.task == nil {
		noteSessionLinkEvent(c.in.SessionID, *e)
		return
	}
	if !c.task.Open() || !c.jiraTask() {
		return
	}
	c.task.applyLinkEvent(*e)
	_ = SaveTask(c.task)
}

// linkProblems: what the checkpoint refuses about the ticket's links.
// unasked: links listed with a reason that no question named yet; the
// checkpoint question itself can name them, so the plan hint reports them
// apart. question is the checkpoint question being asked, or "".
func (c *hookCtx) linkProblems(question string) (problems, unasked []string) {
	t := c.task
	if t == nil || !c.jiraTask() {
		return nil, nil
	}
	if !t.JiraRead {
		if l := t.Lookups["jira"]; l.Count > 0 && t.ToolDown["jira"] == "" {
			// Read by id, URL or a search: the links can't be tied to the ticket.
			return []string{fmt.Sprintf(`read the ticket with its key as the issue key (for example "issueIdOrKey": "%s"), so its links can be checked: a read by id, URL or search doesn't name it`, t.Ticket)}, nil
		}
		return nil, nil
	}
	var lines []string
	for _, d := range []string{DocUnderstanding, DocPlan} {
		if text, _, err := ReadDoc(t, d); err == nil {
			lines = append(lines, Section(text, "links")...)
		}
	}
	var missing, noReason []string
	for _, l := range t.JiraLinks {
		if l.Opened {
			continue
		}
		listed, reason := false, false
		for _, line := range lines {
			if !textNamesLink(line, l) {
				continue
			}
			listed = true
			rest := strings.Trim(reLinkURL.ReplaceAllString(line, " "), " -*+:;,.()")
			words := 0
			for _, f := range strings.Fields(rest) {
				if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
					words++
				}
			}
			reason = reason || words >= 3
		}
		switch {
		case !listed:
			missing = append(missing, l.URL)
		case !reason:
			noReason = append(noReason, l.URL)
		case !l.Asked && !textNamesLink(question, l):
			unasked = append(unasked, l.URL)
		}
	}
	if len(missing) > 0 {
		problems = append(problems, fmt.Sprintf(`the ticket %s links to pages you haven't opened: %s. Open each one (WebFetch for a public page; for a page behind a login, such as Figma, Confluence, Google Docs or an attachment, the Figma or Atlassian tools, or Claude in Chrome, which has the user's sign-ins), or list it under "## Links" in understanding.md with why it couldn't be opened, then name it in a question to the user asking for screenshots or access (the checkpoint question counts)`, t.Ticket, someLinks(missing)))
	}
	if len(noReason) > 0 {
		problems = append(problems, fmt.Sprintf(`"## Links" lists %s without a reason: say on its line why it couldn't be opened`, someLinks(noReason)))
	}
	return problems, unasked
}

func someLinks(ls []string) string {
	if len(ls) <= 5 {
		return strings.Join(ls, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(ls[:5], ", "), len(ls)-5)
}

func unaskedProblem(unasked []string) string {
	return fmt.Sprintf("name the links you couldn't open in a question to the user, asking for screenshots or access (the checkpoint question counts): %s", someLinks(unasked))
}

// linkAskNote: the plan hint's line for links the checkpoint question must name.
func linkAskNote(unasked []string) string {
	if len(unasked) == 0 {
		return ""
	}
	return " The question must also name the links you couldn't open, asking for screenshots or access: " + someLinks(unasked) + "."
}

// questionText: every string of the AskUserQuestion being asked.
func (c *hookCtx) questionText() string {
	if c.in.ToolName != "AskUserQuestion" {
		return ""
	}
	return strings.Join(inputStrings(c.in.ToolInput), "\n")
}
