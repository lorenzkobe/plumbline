package core

import (
	"net/url"
	"regexp"
	"strings"
)

// The browser tools Claude drives: Claude in Chrome (the user's own Chrome,
// signed in to their accounts) and the desktop app's built-in browser.
func browserTool(tool string) bool {
	if !strings.HasPrefix(tool, "mcp__") {
		return false
	}
	server, _ := mcpSplit(tool)
	low := strings.ToLower(server)
	return strings.Contains(low, "claude-in-chrome") || strings.Contains(low, "claude_browser")
}

// Computer actions that only look (or move the view); the rest click, type
// or press keys.
var browserLookActions = set("screenshot zoom scroll scroll_to hover mouse_move wait cursor_position")

// browserChanges: the call can change something in the page or behind it
// (a click, typing, a key, a form, a page script, a file upload, a batch of
// actions). Looking, reading, resizing, tabs and opening a URL don't.
func browserChanges(tool string, input map[string]any) bool {
	_, name := mcpSplit(tool)
	n := mcpWords(name)
	if n == "computer" {
		a, _ := input["action"].(string)
		return !browserLookActions[a]
	}
	if n == "javascript_tool" || strings.Contains(n, "evaluate") {
		script, _ := input["text"].(string)
		if script == "" {
			script, _ = input["script"].(string)
		}
		return !readOnlyScript(script)
	}
	for _, w := range []string{"click", "type", "fill", "form", "javascript", "evaluate", "upload", "press", "drag", "select", "batch", "key"} {
		if contains(strings.Split(n, "_"), w) {
			return true
		}
	}
	return false
}

// reScriptChanges: what a page script needs to change data rather than
// look: network calls, storage, cookies, clicks and events, forms and
// field values, navigation, the app's own data client. Styles and reading
// the page are fine: they change nothing that's saved.
var reScriptChanges = regexp.MustCompile(`(?i)\bfetch\s*\(|xmlhttprequest|sendbeacon|websocket|eventsource|localstorage|sessionstorage|indexeddb|document\.cookie|\.click\s*\(|dispatchevent|\.submit\s*\(|requestsubmit|\.value\s*=[^=]|\.checked\s*=[^=]|\bsupabase\b|\.(post|put|patch|delete|insert|update|upsert|rpc)\s*\(|location\s*(\.href)?\s*=[^=]|location\.(assign|replace|reload)|window\.open|history\.(push|replace)state|\bimport\s*\(|\beval\s*\(|new\s+function|\bFunction\s*\(|\[\s*['"][a-z]*['"]\s*\+|postmessage|serviceworker|caches\.`)

// readOnlyScript: a page script that only reads or measures (sizes, text,
// styles), so it needs no prompt even on real data. One that could change
// data, or is empty, isn't.
func readOnlyScript(script string) bool {
	return strings.TrimSpace(script) != "" && !reScriptChanges.MatchString(script)
}

// realData: the app run on this laptop uses real data. Unknown (a setup
// from before the question) counts as real, so it asks rather than risk it.
func realData(s *Setup) bool {
	return s != nil && (s.LocalRealData == nil || *s.LocalRealData)
}

// gateBrowser: in the browser, clicks, typing and page scripts ask first
// when the app uses real data (DATA1). Looking never asks.
func (c *hookCtx) gateBrowser() Output {
	if !browserTool(c.in.ToolName) || !c.setupOK || !realData(c.setup) || !browserChanges(c.in.ToolName, c.in.Input()) {
		return nil
	}
	return c.ask("real-data", "this clicks, types or runs a script in the browser, and the app on this laptop uses real data (your real account). Allow only if it can't change anything you care about. (/plumbline:setup can record that the local app uses a test database.)")
}

// localURL: the app on this laptop (localhost and the like).
func localURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" || strings.HasSuffix(h, ".localhost")
}

// afterBrowser: a page opened on the local app, then a screenshot of it,
// counts as the task's real-app (ui) proof on the code as it is now (PR2):
// Claude looked at the real screen, so it needn't feed the recorder a
// command.
func (c *hookCtx) afterBrowser() {
	t := c.task
	if t == nil || c.in.AgentID != "" || !browserTool(c.in.ToolName) {
		return
	}
	_, name := mcpSplit(c.in.ToolName)
	n := mcpWords(name)
	input := c.in.Input()
	if n == "navigate" {
		if u, _ := input["url"].(string); localURL(u) {
			t.BrowserURL = u
		} else {
			t.BrowserURL = ""
		}
		_ = SaveTask(t)
		return
	}
	shot := false
	if n == "computer" {
		a, _ := input["action"].(string)
		shot = a == "screenshot" || a == "zoom"
	}
	if !shot && !strings.Contains(n, "screenshot") {
		return
	}
	if t.Phase != PhaseBuilding || t.BrowserURL == "" || !contains(t.ProofKinds, "ui") {
		return
	}
	fp, err := Fingerprint(t.Root, c.ignore())
	if err != nil || fp == "" {
		return
	}
	now := Now()
	_ = appendRun(t.Root, &Run{ID: newID() + "-" + KindProof, TaskID: t.ID, Kind: KindProof, Sub: "ui",
		Command: "browser screenshot of " + t.BrowserURL, Started: now, Finished: now,
		PrintBefore: fp, PrintAfter: fp, Ran: 1})
}
