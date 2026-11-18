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
	DenyAdded    []string                   `json:"deny_added,omitempty"` // permissions.deny rules Plumbline added
}

// laptopState: what setup laptop sets, as Claude Code's user settings have
// it now, so setup learns it without reading the settings file (SR3). Only
// these values are shown, nothing else in the file.
func laptopState() string {
	sp := claudeSettingsPath()
	settings := map[string]json.RawMessage{}
	if b, err := os.ReadFile(sp); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return "Laptop: Claude Code's user settings aren't valid JSON (" + sp + ")\n"
		}
	}
	memory := "on"
	if v, ok := settings["autoMemoryEnabled"]; ok && strings.TrimSpace(string(v)) == "false" {
		memory = "off"
	}
	attribution := "Claude Code's default (on)"
	var a struct {
		Commit *string `json:"commit"`
		PR     *string `json:"pr"`
	}
	if v, ok := settings["attribution"]; ok && json.Unmarshal(v, &a) == nil && a.Commit != nil && *a.Commit == "" {
		attribution = "off"
	}
	rule := "no"
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(sp), "CLAUDE.md")); err == nil && strings.Contains(string(b), mdStart) {
		rule = "yes"
	}
	_, deny, _ := denyList(settings)
	n, all := 0, core.SecretDenyRules()
	for _, r := range all {
		if contains(deny, r) {
			n++
		}
	}
	return fmt.Sprintf("Laptop: built-in memory %s; AI attribution %s; ask-before-task rule in the global CLAUDE.md: %s; .env and key file deny rules: %d of %d\n", memory, attribution, rule, n, len(all))
}

func undoPath() string { return filepath.Join(core.DataDir(), "undo", "laptop.json") }

// setupLaptop changes your Claude Code user settings. Claude can only reach it
// through a confirm prompt, and every change is recorded so it can be undone.
func setupLaptop(f map[string]string) error {
	if f["memory-off"] == "" && f["attribution-off"] == "" && f["claude-md"] == "" && f["deny-secrets"] == "" {
		return errors.New("choose what to change: --memory-off, --attribution-off, --claude-md, --deny-secrets")
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
	if f["deny-secrets"] != "" {
		added, err := addDenyRules(settings, core.SecretDenyRules())
		if err != nil {
			return fmt.Errorf("%s: %v", sp, err)
		}
		for _, r := range added {
			if !contains(undo.DenyAdded, r) {
				undo.DenyAdded = append(undo.DenyAdded, r)
			}
		}
		changed = append(changed, fmt.Sprintf("Claude Code skips .env and key files for Read, Grep and Glob (%d deny rules added to permissions.deny)", len(added)))
	}
	if len(settings) > 0 && (f["memory-off"] != "" || f["attribution-off"] != "" || f["deny-secrets"] != "") {
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
	if len(undo.DenyAdded) > 0 {
		if err := removeDenyRules(settings, undo.DenyAdded); err != nil {
			return fmt.Errorf("%s: %v", undo.SettingsPath, err)
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

// addDenyRules adds the rules permissions.deny doesn't have yet, keeping
// every other permission setting, and returns the ones it added.
func addDenyRules(settings map[string]json.RawMessage, rules []string) ([]string, error) {
	perms, deny, err := denyList(settings)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, r := range rules {
		if !contains(deny, r) {
			deny = append(deny, r)
			added = append(added, r)
		}
	}
	return added, setDenyList(settings, perms, deny)
}

// removeDenyRules takes out the rules Plumbline added. A rule the user had
// before isn't in that list, so it stays.
func removeDenyRules(settings map[string]json.RawMessage, rules []string) error {
	perms, deny, err := denyList(settings)
	if err != nil {
		return err
	}
	var keep []string
	for _, r := range deny {
		if !contains(rules, r) {
			keep = append(keep, r)
		}
	}
	return setDenyList(settings, perms, keep)
}

func denyList(settings map[string]json.RawMessage) (map[string]json.RawMessage, []string, error) {
	perms := map[string]json.RawMessage{}
	if raw, ok := settings["permissions"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &perms); err != nil {
			return nil, nil, fmt.Errorf("permissions isn't an object: %v", err)
		}
	}
	var deny []string
	if raw, ok := perms["deny"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &deny); err != nil {
			return nil, nil, fmt.Errorf("permissions.deny isn't a list of rules: %v", err)
		}
	}
	return perms, deny, nil
}

func setDenyList(settings, perms map[string]json.RawMessage, deny []string) error {
	if len(deny) == 0 {
		delete(perms, "deny")
	} else {
		b, err := json.Marshal(deny)
		if err != nil {
			return err
		}
		perms["deny"] = b
	}
	if len(perms) == 0 {
		delete(settings, "permissions")
		return nil
	}
	b, err := json.Marshal(perms)
	if err != nil {
		return err
	}
	settings["permissions"] = b
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
