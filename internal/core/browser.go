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

// browserChanges: the call could save, send or delete something (BR1).
// Plumbline can't see what a button does, so a click is judged by Claude's
// own note for it (action_summary): a note naming a change asks, and so does
// a click with no note. Escape, Tab, arrows, scrolling, typing into a field
// and picking a value don't save anything by themselves; Enter (it submits a
// form), Delete and key combinations do ask. Page scripts ask unless they
// only read. A batch asks when any of its steps would.
func browserChanges(tool string, input map[string]any) bool {
	_, name := mcpSplit(tool)
	return browserStepChanges(mcpWords(name), input, 0)
}

func browserStepChanges(n string, input map[string]any, depth int) bool {
	note, _ := input["action_summary"].(string)
	switch {
	case n == "computer":
		a, _ := input["action"].(string)
		text, _ := input["text"].(string)
		switch {
		case browserLookActions[a]:
			return false
		case a == "key":
			return keysSubmit(text)
		case a == "type":
			return strings.ContainsAny(text, "\r\n") || reChangeNote.MatchString(note)
		}
		// clicks, drags, mouse down/up and anything new
		return strings.TrimSpace(note) == "" || reChangeNote.MatchString(note)
	case n == "form_input":
		v, _ := input["value"].(string)
		return strings.ContainsAny(v, "\r\n") || reChangeNote.MatchString(note)
	case n == "javascript_tool" || strings.Contains(n, "evaluate"):
		script, _ := input["text"].(string)
		if script == "" {
			script, _ = input["script"].(string)
		}
		return !readOnlyScript(script)
	case n == "browser_batch":
		steps, _ := input["actions"].([]any)
		if depth > 0 || len(steps) == 0 {
			return true
		}
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			sn, _ := step["name"].(string)
			sin, _ := step["input"].(map[string]any)
			if step == nil || sn == "" || sin == nil {
				return true
			}
			if _, short := mcpSplit(sn); strings.HasPrefix(sn, "mcp__") {
				sn = short
			}
			if browserStepChanges(mcpWords(sn), sin, depth+1) {
				return true
			}
		}
		return false
	}
	for _, w := range []string{"click", "type", "fill", "form", "javascript", "evaluate", "upload", "press", "drag", "select", "batch", "key"} {
		if contains(strings.Split(n, "_"), w) {
			return true
		}
	}
	return false
}

// keysSubmit: a key press that can submit or delete: Enter or Return,
// Delete, or a shortcut with cmd, ctrl or alt (cmd+s, ctrl+Enter). Shift
// with a moving key (shift+Tab) only moves.
func keysSubmit(keys string) bool {
	for _, k := range strings.Fields(strings.ToLower(keys)) {
		parts := strings.Split(k, "+")
		switch parts[len(parts)-1] {
		case "enter", "return", "kp_enter", "delete", "del":
			return true
		}
		for _, m := range parts[:len(parts)-1] {
			switch m {
			case "cmd", "command", "ctrl", "control", "meta", "super", "alt", "option", "opt":
				return true
			}
		}
	}
	return false
}

// reChangeNote: words in Claude's note for a click that mean it saves,
// sends or deletes something.
var reChangeNote = regexp.MustCompile(`(?i)\b(sav(e|es|ed|ing)|delet(e|es|ed|ing)|remov(e|es|ed|ing)|submit(s|ted|ting)?|send(s|ing)?|sent|pay(s|ing|ment)?|paid|confirm(s|ed|ing)?|publish(es|ed|ing)?|post(s|ed|ing)?|creat(e|es|ed|ing)|add(s|ed|ing)?|archiv(e|es|ed|ing)|approv(e|es|ed|ing)|reject(s|ed|ing)?|transfer(s|red|ring)?|invit(e|es|ed|ing)|shar(e|es|ed|ing)|upload(s|ed|ing)?|settl(e|es|ed|ing)|buy(s|ing)?|bought|purchas(e|es|ed|ing)|order(s|ed|ing)?|check ?out|reset(s|ting)?|clear(s|ed|ing)?|discard(s|ed|ing)?|accept(s|ed|ing)?|declin(e|es|ed|ing)|apply|appl(ies|ied|ying)|updat(e|es|ed|ing)|import(s|ed|ing)?|mark(s|ed|ing)?|toggl(e|es|ed|ing)|enabl(e|es|ed|ing)|disabl(e|es|ed|ing)|turn(s|ed|ing)? (on|off)|cancel(s|led|ling|ed|ing)?|leav(e|es|ing)|join(s|ed|ing)?|record(s|ed|ing)?|sign(s|ed|ing)? ?(out|up)|log(s|ged|ging)? ?out|logout|restor(e|es|ed|ing)|undo(es|ing)?|merg(e|es|ed|ing)|split(s|ting)?)\b`)

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

// realDataWhy: what the real-data asks say about where that comes from.
func realDataWhy(s *Setup) string {
	if s != nil && s.LocalRealData != nil {
		return "your setup says so"
	}
	return "your setup doesn't say, so Plumbline assumes it"
}

// gateBrowser: in the browser, what could save, send or delete asks first
// when the app uses real data (DATA1, BR1). Looking never asks.
func (c *hookCtx) gateBrowser() Output {
	if !browserTool(c.in.ToolName) || !c.setupOK || !realData(c.setup) || !browserChanges(c.in.ToolName, c.in.Input()) {
		return nil
	}
	if c.task != nil && c.task.BrowserOK && !browserScripts(c.in.ToolName, c.in.Input()) {
		return nil // the user allowed clicks for this task (CL1)
	}
	if c.task != nil && !browserScripts(c.in.ToolName, c.in.Input()) {
		return c.ask("real-data", "Claude wants to click and type in the app on this laptop, which may use real data ("+realDataWhy(c.setup)+"), such as your real account. Allowing it counts for the rest of this task's clicks and typing (page scripts that could change data still ask). Allow only if it won't change anything you care about. (/plumbline:setup can record that the local app uses a test database.)")
	}
	return c.ask("real-data", "this could save, send or delete something in the browser, in the app on this laptop, which may use real data ("+realDataWhy(c.setup)+"), such as your real account. Allow only if it can't change anything you care about. (/plumbline:setup can record that the local app uses a test database.)")
}

// noteBrowserAllowed: a click or typing on real data ran, so the user
// allowed it; the rest of the task's clicks don't ask (CL1).
func (c *hookCtx) noteBrowserAllowed() {
	in := c.in
	if c.task == nil || c.task.BrowserOK || in.AgentID != "" || !browserTool(in.ToolName) || !c.setupOK || !realData(c.setup) {
		return
	}
	if !browserChanges(in.ToolName, in.Input()) || browserScripts(in.ToolName, in.Input()) {
		return
	}
	c.task.BrowserOK = true
	_ = SaveTask(c.task)
}

// browserScripts: the call runs a page script, or a step Plumbline doesn't
// know, so it can reach the app's data directly; the per-task OK for
// clicks doesn't cover it.
func browserScripts(tool string, input map[string]any) bool {
	_, name := mcpSplit(tool)
	return browserStepScripts(mcpWords(name), input, 0)
}

func browserStepScripts(n string, input map[string]any, depth int) bool {
	switch {
	case n == "computer" || n == "form_input":
		return false
	case n == "browser_batch":
		steps, _ := input["actions"].([]any)
		if depth > 0 || len(steps) == 0 {
			return true
		}
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			sn, _ := step["name"].(string)
			sin, _ := step["input"].(map[string]any)
			if step == nil || sn == "" || sin == nil {
				return true
			}
			if _, short := mcpSplit(sn); strings.HasPrefix(sn, "mcp__") {
				sn = short
			}
			if browserStepChanges(mcpWords(sn), sin, depth+1) && browserStepScripts(mcpWords(sn), sin, depth+1) {
				return true
			}
		}
		return false
	}
	return true
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
	if n == "browser_batch" {
		// The steps in order: a local page opened, then a screenshot (PR3).
		before := t.BrowserURL
		steps, _ := input["actions"].([]any)
		for _, raw := range steps {
			step, _ := raw.(map[string]any)
			sn, _ := step["name"].(string)
			sin, _ := step["input"].(map[string]any)
			if _, short := mcpSplit(sn); strings.HasPrefix(sn, "mcp__") {
				sn = short
			}
			switch sn = mcpWords(sn); {
			case sn == "navigate":
				if u, _ := sin["url"].(string); localURL(u) {
					t.BrowserURL = u
				} else {
					t.BrowserURL = ""
				}
				shot = false
			case sn == "computer":
				if a, _ := sin["action"].(string); (a == "screenshot" || a == "zoom") && t.BrowserURL != "" {
					shot = true
				}
			case strings.Contains(sn, "screenshot") && t.BrowserURL != "":
				shot = true
			}
		}
		if t.BrowserURL != before {
			_ = SaveTask(t)
		}
		if !shot {
			return
		}
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
