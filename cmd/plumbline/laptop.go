package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lorenzkobe/plumbline/internal/core"
)

const mdStart = "<!-- plumbline:start -->"
const mdEnd = "<!-- plumbline:end -->"

const globalRule = mdStart + `
## Plumbline
- If my message looks like a task (a ticket id, a bug, a feature, a design request), ask in one line before starting Plumbline's task workflow. Questions and chat get a normal answer, checked against the real code.
` + mdEnd + "\n"

type undoRecord struct {
	SettingsPath string                     `json:"settings_path"`
	Keys         map[string]json.RawMessage `json:"keys"` // previous values; "null" means the key was absent
	ClaudeMD     string                     `json:"claude_md,omitempty"`
}

func undoPath() string { return filepath.Join(core.DataDir(), "undo", "laptop.json") }

// setupLaptop changes your Claude Code user settings. Claude can only reach it
// through a confirm prompt, and every change is recorded so it can be undone.
func setupLaptop(f map[string]string) error {
	if f["memory-off"] == "" && f["attribution-off"] == "" && f["claude-md"] == "" {
		return errors.New("choose what to change: --memory-off, --attribution-off, --claude-md")
	}
	sp := claudeSettingsPath()
	settings := map[string]json.RawMessage{}
	if b, err := os.ReadFile(sp); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return fmt.Errorf("%s isn't valid JSON; fix it first: %v", sp, err)
		}
	}
	undo := undoRecord{SettingsPath: sp, Keys: map[string]json.RawMessage{}}
	if b, err := os.ReadFile(undoPath()); err == nil {
		_ = json.Unmarshal(b, &undo)
		if undo.Keys == nil {
			undo.Keys = map[string]json.RawMessage{}
		}
	}
	remember := func(k string) {
		if _, done := undo.Keys[k]; done {
			return // keep the oldest value, from before Plumbline
		}
		if v, ok := settings[k]; ok {
			undo.Keys[k] = v
		} else {
			undo.Keys[k] = json.RawMessage("null")
		}
	}
	var changed []string
	if f["memory-off"] != "" {
		remember("autoMemoryEnabled")
		settings["autoMemoryEnabled"] = json.RawMessage("false")
		changed = append(changed, "built-in memory off (autoMemoryEnabled: false)")
	}
	if f["attribution-off"] != "" {
		remember("attribution")
		settings["attribution"] = json.RawMessage(`{"commit":"","pr":""}`)
		changed = append(changed, "AI attribution on commits and PRs off (attribution: empty)")
	}
	if len(settings) > 0 && (f["memory-off"] != "" || f["attribution-off"] != "") {
		b, _ := json.MarshalIndent(settings, "", "  ")
		if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(sp, append(b, '\n'), 0o644); err != nil {
			return err
		}
	}
	if f["claude-md"] != "" {
		mp := filepath.Join(filepath.Dir(sp), "CLAUDE.md")
		b, _ := os.ReadFile(mp)
		if !strings.Contains(string(b), mdStart) {
			s := string(b)
			if s != "" && !strings.HasSuffix(s, "\n") {
				s += "\n"
			}
			if err := os.WriteFile(mp, []byte(s+"\n"+globalRule), 0o644); err != nil {
				return err
			}
			undo.ClaudeMD = mp
			changed = append(changed, "ask-before-task rule added to "+mp)
		}
	}
	b, _ := json.MarshalIndent(undo, "", "  ")
	if err := os.MkdirAll(filepath.Dir(undoPath()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(undoPath(), b, 0o600); err != nil {
		return err
	}
	fmt.Println("Changed:")
	for _, c := range changed {
		fmt.Println("  -", c)
	}
	fmt.Println("Undo any time with: plumbline setup undo-laptop")
	return nil
}

func undoLaptop() error {
	b, err := os.ReadFile(undoPath())
	if err != nil {
		return errors.New("nothing to undo")
	}
	var undo undoRecord
	if err := json.Unmarshal(b, &undo); err != nil {
		return err
	}
	settings := map[string]json.RawMessage{}
	if sb, err := os.ReadFile(undo.SettingsPath); err == nil {
		_ = json.Unmarshal(sb, &settings)
	}
	for k, v := range undo.Keys {
		if string(v) == "null" {
			delete(settings, k)
		} else {
			settings[k] = v
		}
	}
	out, _ := json.MarshalIndent(settings, "", "  ")
	if err := os.WriteFile(undo.SettingsPath, append(out, '\n'), 0o644); err != nil {
		return err
	}
	if undo.ClaudeMD != "" {
		if mb, err := os.ReadFile(undo.ClaudeMD); err == nil {
			s := string(mb)
			if i := strings.Index(s, mdStart); i >= 0 {
				if j := strings.Index(s, mdEnd); j > i {
					s = strings.TrimRight(s[:i], "\n") + "\n" + strings.TrimLeft(s[j+len(mdEnd):], "\n")
					_ = os.WriteFile(undo.ClaudeMD, []byte(s), 0o644)
				}
			}
		}
	}
	_ = os.Remove(undoPath())
	fmt.Println("Laptop settings restored to how they were before Plumbline.")
	return nil
}
