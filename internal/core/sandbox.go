package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// Claude Code's Bash sandbox (G1, P1, B1) is the user's choice. Plumbline
// reads the settings files to say whether it's on, and `plumbline setup
// sandbox` writes this checkout's .claude/settings.local.json (the file
// Claude Code's own /sandbox panel writes) so that:
//   - Plumbline's folder (the seal key, task state, proof, helpers' notes)
//     can't be read or written by sandboxed commands, and
//   - Plumbline's own commands run outside the sandbox, so they can still
//     save proof. Code those commands run (saved test commands, proof
//     commands) is outside the fence too, as it is without the sandbox.
// Hooks run outside the sandbox, so the gates are unchanged.

// SandboxLocalFile is where `plumbline setup sandbox` writes, relative to
// the checkout.
const SandboxLocalFile = ".claude/settings.local.json"

// plumblineExclusion is the excludedCommands entry for Plumbline's own
// commands (Bash permission-rule syntax: the command with any arguments).
const plumblineExclusion = "plumbline *"

const sandboxExcludeComment = "Claude Code's local settings (plumbline setup sandbox)"

// managedSettingsPath is the organisation-wide settings file; a var so
// tests can point it elsewhere.
var managedSettingsPath = func() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode/managed-settings.json"
	case "linux":
		return "/etc/claude-code/managed-settings.json"
	}
	return ""
}

// SandboxInfo is what the settings files say about the sandbox. It's what
// Claude Code was told, not proof that it's running: a --settings flag
// isn't seen, and a platform without support runs commands unsandboxed.
type SandboxInfo struct {
	On           bool
	Source       string   // the file that turned it on
	PlumblineOut bool     // Plumbline's commands are excluded, so they can save proof
	DataFenced   bool     // sandboxed commands can't read Plumbline's seal key
	NoEscape     bool     // allowUnsandboxedCommands is false
	Notes        []string // files that couldn't be read, platform gaps
}

type settingsFile struct {
	path, label string
	base        string // what a relative sandbox path is relative to
}

func sandboxFiles(root string) []settingsFile {
	var out []settingsFile
	if p := managedSettingsPath(); p != "" {
		out = append(out, settingsFile{p, "managed settings", ""})
	}
	if root != "" {
		out = append(out,
			settingsFile{filepath.Join(root, ".claude", "settings.local.json"), SandboxLocalFile, root},
			settingsFile{filepath.Join(root, ".claude", "settings.json"), ".claude/settings.json", root})
	}
	return append(out, settingsFile{filepath.Join(claudeHome(), "settings.json"), "user settings", claudeHome()})
}

// sandboxKeys is the part of one settings file Plumbline reads.
type sandboxKeys struct {
	Enabled          *bool    `json:"enabled"`
	AllowUnsandboxed *bool    `json:"allowUnsandboxedCommands"`
	Excluded         []string `json:"excludedCommands"`
	Filesystem       struct {
		DenyRead  []string `json:"denyRead"`
		AllowRead []string `json:"allowRead"`
		Disabled  *bool    `json:"disabled"`
	} `json:"filesystem"`
}

// SandboxState reads the settings files in Claude Code's order: managed
// first (it wins), then this checkout's local and shared settings, then the
// user's. Lists are combined across files, as Claude Code combines them.
func SandboxState(root string) SandboxInfo {
	var info SandboxInfo
	enabledSet, escapeSet := false, false
	anyFalse, reopened, fsOff := false, false, false
	key := filepath.Join(DataDir(), ".key")
	for _, f := range sandboxFiles(root) {
		b, err := os.ReadFile(f.path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				info.Notes = append(info.Notes, fmt.Sprintf("%s couldn't be read (%v)", f.label, err))
			}
			continue
		}
		var top struct {
			Sandbox sandboxKeys `json:"sandbox"`
		}
		if err := json.Unmarshal(b, &top); err != nil {
			info.Notes = append(info.Notes, fmt.Sprintf("%s isn't valid JSON or has an unexpected sandbox shape, so it was skipped (%v)", f.label, err))
			continue
		}
		s := top.Sandbox
		if s.Enabled != nil && !enabledSet {
			enabledSet, info.On = true, *s.Enabled
			if info.On {
				info.Source = f.label
			}
		}
		if s.AllowUnsandboxed != nil {
			if f.label == "managed settings" {
				escapeSet, info.NoEscape = true, !*s.AllowUnsandboxed
			}
			anyFalse = anyFalse || !*s.AllowUnsandboxed
		}
		for _, e := range s.Excluded {
			if ruleCovers(e, "plumbline status") && ruleCovers(e, "plumbline run test") {
				info.PlumblineOut = true
			}
		}
		for _, p := range s.Filesystem.DenyRead {
			if abs := sandboxPath(p, f.base); abs != "" && IsInside(key, abs) {
				info.DataFenced = true
			}
		}
		for _, p := range s.Filesystem.AllowRead {
			if abs := sandboxPath(p, f.base); abs != "" && IsInside(key, abs) {
				reopened = true
			}
		}
		if s.Filesystem.Disabled != nil && *s.Filesystem.Disabled {
			fsOff = true
		}
	}
	// allowRead re-opens part of a denied region, and filesystem.disabled
	// turns the file fence off (network isolation stays).
	if reopened || fsOff {
		info.DataFenced = false
	}
	if info.On && fsOff {
		info.Notes = append(info.Notes, "sandbox.filesystem.disabled is true, so the sandbox doesn't fence files at all")
	}
	// A false in any file holds against a true below it (the docs say so for
	// user and managed settings; local settings outrank the user's). Only a
	// managed value overrides it.
	if !escapeSet {
		info.NoEscape = anyFalse
	}
	if info.On {
		switch runtime.GOOS {
		case "windows":
			info.Notes = append(info.Notes, "Claude Code doesn't sandbox commands on native Windows")
		case "linux":
			for _, tool := range []string{"bwrap", "socat"} {
				if _, err := exec.LookPath(tool); err != nil {
					info.Notes = append(info.Notes, fmt.Sprintf("%s isn't installed, and without bubblewrap and socat Claude Code runs commands unsandboxed", tool))
					break
				}
			}
		}
	}
	return info
}

// ruleCovers: whether an excludedCommands entry (Bash permission-rule
// syntax) matches the command text cmd. "x" is exact, "x *" and the older
// "x:*" are x with or without arguments, and a trailing "*" is a prefix.
func ruleCovers(rule, cmd string) bool {
	rule = strings.TrimSpace(rule)
	switch {
	case strings.HasSuffix(rule, ":*"):
		p := strings.TrimSuffix(rule, ":*")
		return cmd == p || strings.HasPrefix(cmd, p+" ")
	case strings.HasSuffix(rule, " *"):
		p := strings.TrimSuffix(rule, " *")
		return cmd == p || strings.HasPrefix(cmd, p+" ")
	case strings.HasSuffix(rule, "*"):
		return strings.HasPrefix(cmd, strings.TrimSuffix(rule, "*"))
	}
	return rule == cmd
}

// sandboxPath turns a sandbox filesystem path into an absolute one: "~/" is
// the home folder, "/" and "//" are absolute, anything else is relative to
// the settings file's base.
func sandboxPath(p, base string) string {
	p = strings.TrimSpace(p)
	switch {
	case p == "":
		return ""
	case strings.HasPrefix(p, "~/"):
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Clean(filepath.Join(h, p[2:]))
	case strings.HasPrefix(p, "/"):
		return filepath.Clean("/" + strings.TrimLeft(p, "/"))
	case base == "":
		return ""
	}
	return filepath.Clean(filepath.Join(base, p))
}

// SandboxLines is the sandbox part of `plumbline status`.
func SandboxLines(root string) []string {
	info := SandboxState(root)
	var out []string
	if !info.On {
		out = append(out, "Bash sandbox: off (optional; /plumbline:setup explains it).")
	} else {
		out = append(out, "Bash sandbox: on (from "+info.Source+").")
		if !info.PlumblineOut {
			out = append(out, "  - Plumbline's commands aren't excluded from it, so they can't save proof. Fix: plumbline setup sandbox, or add \"plumbline *\" to sandbox.excludedCommands.")
		}
		if !info.DataFenced {
			out = append(out, "  - Sandboxed commands can still read Plumbline's seal key. Fix: plumbline setup sandbox, or add "+DataDir()+" to sandbox.filesystem.denyRead.")
		}
		if !info.NoEscape {
			out = append(out, "  - Claude can retry a refused command outside the sandbox (allowUnsandboxedCommands isn't false).")
		}
	}
	for _, n := range info.Notes {
		out = append(out, "  - "+n+".")
	}
	return out
}

// SandboxSessionLine is the session start's note while the sandbox is on.
func SandboxSessionLine(root string) string {
	info := SandboxState(root)
	if !info.On {
		return ""
	}
	if !info.PlumblineOut {
		return "- Bash sandbox: on, but Plumbline's commands aren't excluded from it, so they can't save proof or task state. Tell the user first: they can run `! plumbline setup sandbox` (or add \"plumbline *\" to sandbox.excludedCommands)."
	}
	l := "- Bash sandbox: on. Run each plumbline command on its own line: a pipe, redirect, heredoc, `cd` or `&&` can keep the whole line in the sandbox, where Plumbline can't save proof."
	if info.DataFenced {
		l += " Read task notes with the Read tool; sandboxed commands can't read Plumbline's folder."
	}
	return l
}

// sandboxUndo records what `plumbline setup sandbox` changed in one
// checkout, so undo puts back exactly that.
type sandboxUndo struct {
	SettingsPath string                     `json:"settings_path"`
	CreatedFile  bool                       `json:"created_file,omitempty"`
	Keys         map[string]json.RawMessage `json:"keys"` // sandbox keys' old values; "null": absent
	Added        map[string][]string        `json:"added"`
}

func sandboxUndoPath(root string) string {
	return filepath.Join(DataDir(), "undo", "sandbox-"+treeKey(root)+".json")
}

var reCommandName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,63}$`)

// unfencing names commands that would take arbitrary code out of the
// sandbox if excluded: shells, interpreters and command runners.
var unfencing = set("sh bash zsh fish dash ksh csh tcsh env sudo doas xargs eval exec nohup time timeout nice watch script expect " +
	"python python3 python2 node nodejs deno bun ruby perl php lua tclsh rscript julia swift dotnet osascript " +
	"npx bunx pnpx uvx pipx make just npm pnpm yarn uv go cargo java gradle mvn git awk gawk find")

// ParseExclusions checks --exclude: a comma-separated list of plain command
// names, none of them a shell or interpreter.
func ParseExclusions(list string) ([]string, error) {
	var out []string
	for _, n := range strings.Split(list, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !reCommandName.MatchString(n) {
			return nil, fmt.Errorf("--exclude takes command names like psql or mysql, not %q", n)
		}
		if unfencing[strings.ToLower(n)] {
			return nil, fmt.Errorf("%s can run any code, so excluding it would take that code out of the sandbox; add it by hand if you really want that", n)
		}
		if !contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// EnableSandbox writes the sandbox settings to this checkout's
// .claude/settings.local.json, keeping every other setting, and records
// what it changed. extra are command names (already checked by
// ParseExclusions) that also run outside the sandbox, such as the
// database client.
func EnableSandbox(root string, extra []string) ([]string, error) {
	sp := filepath.Join(root, filepath.FromSlash(SandboxLocalFile))
	if tracked(root, SandboxLocalFile) {
		return nil, fmt.Errorf("%s is committed in this repo, so the sandbox settings would go into git. Hand the user this, with no task: run `! git rm --cached %s` (the file stays as it is) and add the line %s to .gitignore, then ask for the sandbox again", SandboxLocalFile, SandboxLocalFile, SandboxLocalFile)
	}
	settings := map[string]json.RawMessage{}
	existed := false
	if b, err := os.ReadFile(sp); err == nil {
		existed = true
		if err := json.Unmarshal(b, &settings); err != nil {
			return nil, fmt.Errorf("%s isn't valid JSON; fix it first: %v", SandboxLocalFile, err)
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	undo := sandboxUndo{SettingsPath: sp, Keys: map[string]json.RawMessage{}, Added: map[string][]string{}}
	if b, err := os.ReadFile(sandboxUndoPath(root)); err == nil {
		if err := json.Unmarshal(b, &undo); err != nil {
			return nil, fmt.Errorf("the record of the last sandbox change can't be read (%v); run plumbline setup undo-sandbox or remove %s", err, sandboxUndoPath(root))
		}
		if undo.Keys == nil {
			undo.Keys = map[string]json.RawMessage{}
		}
		if undo.Added == nil {
			undo.Added = map[string][]string{}
		}
	} else {
		undo.CreatedFile = !existed
	}
	sb, err := subObject(settings, "sandbox")
	if err != nil {
		return nil, err
	}
	fs, err := subObject(sb, "filesystem")
	if err != nil {
		return nil, fmt.Errorf("sandbox.%v", err)
	}
	var changed []string
	setBool := func(obj map[string]json.RawMessage, key, path string, v bool) {
		if _, done := undo.Keys[path]; !done {
			if old, ok := obj[key]; ok {
				undo.Keys[path] = old
			} else {
				undo.Keys[path] = json.RawMessage("null")
			}
		}
		obj[key] = json.RawMessage(fmt.Sprint(v))
	}
	setBool(sb, "enabled", "enabled", true)
	changed = append(changed, "sandbox on (sandbox.enabled: true)")
	setBool(sb, "allowUnsandboxedCommands", "allowUnsandboxedCommands", false)
	changed = append(changed, "no retrying a refused command outside the sandbox (sandbox.allowUnsandboxedCommands: false)")

	excl := []string{plumblineExclusion, "docker *"}
	for _, n := range extra {
		excl = append(excl, n+" *")
	}
	added, err := addToList(sb, "excludedCommands", excl)
	if err != nil {
		return nil, fmt.Errorf("sandbox.%v", err)
	}
	undo.Added["excludedCommands"] = union(undo.Added["excludedCommands"], added)
	changed = append(changed, "outside the sandbox: "+strings.Join(excl, ", ")+" (sandbox.excludedCommands)")

	data := DataDir()
	for _, k := range []string{"denyRead", "denyWrite"} {
		added, err := addToList(fs, k, []string{data})
		if err != nil {
			return nil, fmt.Errorf("sandbox.filesystem.%v", err)
		}
		undo.Added[k] = union(undo.Added[k], added)
	}
	changed = append(changed, "sandboxed commands can't read or write "+data+" (sandbox.filesystem.denyRead, denyWrite)")

	if err := putObject(sb, "filesystem", fs); err != nil {
		return nil, err
	}
	if err := putObject(settings, "sandbox", sb); err != nil {
		return nil, err
	}
	// The undo record goes first: if the settings write then fails, undo
	// still knows what to put back.
	if !ignored(root, SandboxLocalFile) && addGitExclude(root, sandboxExcludeComment, "/"+SandboxLocalFile) {
		changed = append(changed, SandboxLocalFile+" kept out of git (.git/info/exclude)")
	}
	if err := writeJSON(sandboxUndoPath(root), undo, 0o600); err != nil {
		return nil, err
	}
	if err := writeJSON(sp, settings, 0o644); err != nil {
		return nil, err
	}
	return changed, nil
}

// UndoSandbox puts back what EnableSandbox changed in this checkout.
// Entries the user had before, or added since, stay.
func UndoSandbox(root string) error {
	b, err := os.ReadFile(sandboxUndoPath(root))
	if err != nil {
		return errors.New("no sandbox settings in this checkout were added by Plumbline, so there is nothing to undo")
	}
	var undo sandboxUndo
	if err := json.Unmarshal(b, &undo); err != nil {
		return fmt.Errorf("the record of the sandbox change can't be read: %v", err)
	}
	settings := map[string]json.RawMessage{}
	if sb, err := os.ReadFile(undo.SettingsPath); err == nil {
		if err := json.Unmarshal(sb, &settings); err != nil {
			return fmt.Errorf("%s isn't valid JSON; fix it first: %v", undo.SettingsPath, err)
		}
		if settings == nil {
			settings = map[string]json.RawMessage{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	sb, err := subObject(settings, "sandbox")
	if err != nil {
		return err
	}
	fs, err := subObject(sb, "filesystem")
	if err != nil {
		return fmt.Errorf("sandbox.%v", err)
	}
	for k, v := range undo.Keys {
		if string(v) == "null" {
			delete(sb, k)
		} else {
			sb[k] = v
		}
	}
	if err := removeFromList(sb, "excludedCommands", undo.Added["excludedCommands"]); err != nil {
		return fmt.Errorf("sandbox.%v", err)
	}
	for _, k := range []string{"denyRead", "denyWrite"} {
		if err := removeFromList(fs, k, undo.Added[k]); err != nil {
			return fmt.Errorf("sandbox.filesystem.%v", err)
		}
	}
	if err := putObject(sb, "filesystem", fs); err != nil {
		return err
	}
	if err := putObject(settings, "sandbox", sb); err != nil {
		return err
	}
	if undo.CreatedFile && len(settings) == 0 {
		if err := os.Remove(undo.SettingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := writeJSON(undo.SettingsPath, settings, 0o644); err != nil {
		return err
	}
	// The exclude line is shared by the repo's worktrees: it stays while
	// another checkout still has Plumbline's sandbox settings, and goes with
	// the last one (only the line under Plumbline's comment).
	if !otherSandboxCheckouts(root) {
		removeGitExclude(root, sandboxExcludeComment, "/"+SandboxLocalFile)
	}
	return os.Remove(sandboxUndoPath(root))
}

// otherSandboxCheckouts: another checkout of root's repo has an undo record
// from plumbline setup sandbox.
func otherSandboxCheckouts(root string) bool {
	mine := sandboxUndoPath(root)
	recs, _ := filepath.Glob(filepath.Join(DataDir(), "undo", "sandbox-*.json"))
	for _, r := range recs {
		if r == mine {
			continue
		}
		var u sandboxUndo
		if b, err := os.ReadFile(r); err == nil && json.Unmarshal(b, &u) == nil && u.SettingsPath != "" {
			checkout := filepath.Dir(filepath.Dir(u.SettingsPath))
			if ProjectRoot(checkout) == ProjectRoot(root) {
				return true
			}
		}
	}
	return false
}

// subObject returns obj[key] as an object (empty when absent).
func subObject(obj map[string]json.RawMessage, key string) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if raw, ok := obj[key]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("%s isn't an object: %v", key, err)
		}
		if out == nil {
			out = map[string]json.RawMessage{}
		}
	}
	return out, nil
}

// putObject stores sub under key, or removes key when sub is empty.
func putObject(obj map[string]json.RawMessage, key string, sub map[string]json.RawMessage) error {
	if len(sub) == 0 {
		delete(obj, key)
		return nil
	}
	b, err := json.Marshal(sub)
	if err != nil {
		return err
	}
	obj[key] = b
	return nil
}

func stringList(obj map[string]json.RawMessage, key string) ([]string, error) {
	var list []string
	if raw, ok := obj[key]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("%s isn't a list of strings: %v", key, err)
		}
	}
	return list, nil
}

func putList(obj map[string]json.RawMessage, key string, list []string) {
	if len(list) == 0 {
		delete(obj, key)
		return
	}
	b, _ := json.Marshal(list)
	obj[key] = b
}

// addToList adds the entries obj[key] doesn't have yet and returns them.
func addToList(obj map[string]json.RawMessage, key string, entries []string) ([]string, error) {
	list, err := stringList(obj, key)
	if err != nil {
		return nil, err
	}
	var added []string
	for _, e := range entries {
		if !contains(list, e) {
			list = append(list, e)
			added = append(added, e)
		}
	}
	putList(obj, key, list)
	return added, nil
}

func removeFromList(obj map[string]json.RawMessage, key string, entries []string) error {
	list, err := stringList(obj, key)
	if err != nil {
		return err
	}
	var keep []string
	for _, e := range list {
		if !contains(entries, e) {
			keep = append(keep, e)
		}
	}
	putList(obj, key, keep)
	return nil
}

func union(a, b []string) []string {
	for _, x := range b {
		if !contains(a, x) {
			a = append(a, x)
		}
	}
	sort.Strings(a)
	return a
}

func writeJSON(path string, v any, mode os.FileMode) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), mode)
}

// tracked: rel is a file git tracks in the checkout.
func tracked(root, rel string) bool {
	_, err := gitOut(root, "ls-files", "--error-unmatch", "--", rel)
	return err == nil
}

// ignored: git already ignores rel (a .gitignore, the global excludes file,
// or info/exclude).
func ignored(root, rel string) bool {
	_, err := gitOut(root, "check-ignore", "-q", "--no-index", "--", rel)
	return err == nil
}
