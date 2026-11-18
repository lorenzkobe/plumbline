package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The sprint file (S4). Plumbline asks before a write it can see coming:
// the Edit tool, a terminal write naming the file, and a recursive delete
// or archive extraction over its folder. Code run by an interpreter or a
// script can still change it unseen, so the file is compared before and
// after every command that may change something, and a change the user
// wasn't asked about is put in front of Claude to tell the user.

func (c *hookCtx) askTracker(v *verdicts) {
	v.a("tracker", fmt.Sprintf("this updates your sprint file (%s). Check the change before allowing it.", filepath.Base(TrackerPath(c.root))))
	c.trackerAsked = true
}

// trackerUnder: abs is a folder that holds the sprint file.
func (c *hookCtx) trackerUnder(abs string) bool {
	if abs == "" || c.root == "" {
		return false
	}
	tp := TrackerPath(c.root)
	return tp != "" && foldPath(tp) != foldPath(abs) && isInsideFold(tp, abs)
}

// removesTree: rm with -r, -R or --recursive.
func removesTree(s Segment) bool {
	if s.Name() != "rm" {
		return false
	}
	for _, a := range s.Words[1:] {
		if a == "--" {
			break
		}
		if a == "--recursive" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR") {
			return true
		}
	}
	return false
}

// trackerCopyMax: a bigger sprint file is compared by hash only.
const trackerCopyMax = 256 << 10

type trackerBefore struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Hash    string `json:"hash,omitempty"`
	Content []byte `json:"content,omitempty"`
}

var reToolUseID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func trackerBeforeDir(root string) string { return filepath.Join(ProjectDir(root), "sprint-before") }

// trackerWatched: the tools whose post hooks compare the sprint file.
func (c *hookCtx) trackerWatched() bool {
	switch {
	case c.in.ToolName == "Bash", c.in.ToolName == "Monitor":
		return !readOnlyLine(ParseShell(c.in.Str("command"), c.in.Cwd))
	case strings.HasPrefix(c.in.ToolName, "mcp__"):
		return c.mcpKind() != ""
	}
	return false
}

// noteTrackerBefore keeps the sprint file as it is before a call that may
// change it, unless the call's prompt already names it.
func (c *hookCtx) noteTrackerBefore() {
	if c.root == "" || c.trackerAsked || !reToolUseID.MatchString(c.in.ToolUseID) || !c.trackerWatched() {
		return
	}
	tp := TrackerPath(c.root)
	if tp == "" {
		return
	}
	dir := trackerBeforeDir(c.root)
	pruneTrackerBefore(dir)
	b := trackerBefore{Path: tp}
	if fi, err := os.Stat(tp); err == nil && !fi.IsDir() {
		b.Exists, b.Hash = true, fileHash(tp)
		if fi.Size() <= trackerCopyMax {
			b.Content, _ = os.ReadFile(tp)
		}
	}
	data, err := json.Marshal(b)
	if err != nil {
		return
	}
	_ = writeFileAtomic(filepath.Join(dir, c.in.ToolUseID+".json"), data)
}

// pruneTrackerBefore drops copies whose post hook never ran (a call
// declined at a prompt, a session that ended).
func pruneTrackerBefore(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// trackerChanged: after the call, what happened to the sprint file it
// wasn't asked about, or "".
func (c *hookCtx) trackerChanged() string {
	if c.root == "" || !reToolUseID.MatchString(c.in.ToolUseID) {
		return ""
	}
	f := filepath.Join(trackerBeforeDir(c.root), c.in.ToolUseID+".json")
	data, err := os.ReadFile(f)
	if err != nil {
		return ""
	}
	_ = os.Remove(f)
	var b trackerBefore
	if json.Unmarshal(data, &b) != nil || b.Path == "" {
		return ""
	}
	name := filepath.Base(b.Path)
	fi, err := os.Stat(b.Path)
	exists := err == nil && !fi.IsDir()
	var what string
	switch {
	case !b.Exists && !exists:
		return ""
	case b.Exists && !exists:
		what = fmt.Sprintf("was deleted or moved (%s)", name)
	case !b.Exists:
		what = fmt.Sprintf("was created (%s)", name)
	case fileHash(b.Path) == b.Hash:
		return ""
	default:
		what = fmt.Sprintf("changed (%s)", name)
		if d := trackerDiff(b, f); d != "" {
			what += ". The change:\n" + d
		}
	}
	c.log("tracker-changed", "tracker", "")
	if c.in.AgentID != "" {
		return fmt.Sprintf("the user's sprint file %s while this ran, and nobody asked them. Don't change it again; put this first in what you hand back, with what changed, so the lead tells the user.", what)
	}
	return fmt.Sprintf("your sprint file %s while this ran, and the user wasn't asked about it. Tell the user first, before anything else, and show them what changed. If it wasn't meant to change, ask them before putting it back with the Edit tool.", what)
}

// trackerDiff: the changed lines, at most 40, from git diff --no-index.
func trackerDiff(b trackerBefore, copyName string) string {
	if b.Content == nil {
		return ""
	}
	old := strings.TrimSuffix(copyName, ".json") + ".before"
	if err := os.WriteFile(old, b.Content, 0o600); err != nil {
		return ""
	}
	defer os.Remove(old)
	out, err := gitOut(filepath.Dir(old), "diff", "--no-index", "--no-color", "--no-ext-diff", "-U1", "--", old, b.Path)
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return ""
	}
	var lines []string
	body := false
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.HasPrefix(l, "@@") {
			body = true
		}
		if !body {
			continue // the header names Plumbline's copy
		}
		if len(lines) == 40 {
			lines = append(lines, "(more lines changed)")
			break
		}
		lines = append(lines, limit(l, 200))
	}
	return strings.Join(lines, "\n")
}

// withTrackerCheck adds the sprint file's change, if any, to a post result.
func (c *hookCtx) withTrackerCheck(o Output) Output {
	if note := c.trackerChanged(); note != "" {
		return addContext(o, msg(note))
	}
	return o
}

// addContext appends text to a post result's additionalContext.
func addContext(o Output, text string) Output {
	if o == nil {
		return Context("PostToolUse", text)
	}
	h, ok := o["hookSpecificOutput"].(map[string]any)
	if !ok {
		return o.With("hookSpecificOutput", map[string]any{"hookEventName": "PostToolUse", "additionalContext": text})
	}
	if prev, _ := h["additionalContext"].(string); prev != "" {
		h["additionalContext"] = prev + " " + text
	} else {
		h["additionalContext"] = text
	}
	return o
}
