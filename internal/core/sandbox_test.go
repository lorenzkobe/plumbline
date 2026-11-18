package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandboxHarness: a repo whose git ignores nothing from this laptop's own
// git config (Claude Code adds settings.local.json to the global ignore
// file), and a managed settings file the test controls.
func sandboxHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	cfg := t.TempDir()
	must(t, os.WriteFile(filepath.Join(cfg, "gitconfig"), nil, 0o644))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(cfg, "gitconfig"))
	t.Setenv("XDG_CONFIG_HOME", cfg)
	managed := filepath.Join(cfg, "managed-settings.json")
	old := managedSettingsPath
	managedSettingsPath = func() string { return managed }
	t.Cleanup(func() { managedSettingsPath = old })
	return h, managed
}

func readJSONFile(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	must(t, err)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s isn't JSON: %v\n%s", p, err, b)
	}
	return m
}

// The settings files are read in Claude Code's order: managed wins, then
// the checkout's local and shared settings, then the user's.
func TestSandboxStateReadsSettingsFiles(t *testing.T) {
	h, managed := sandboxHarness(t)
	user := filepath.Join(claudeHome(), "settings.json")
	local := filepath.Join(h.repo, ".claude", "settings.local.json")
	project := filepath.Join(h.repo, ".claude", "settings.json")

	if s := SandboxState(h.repo); s.On {
		t.Fatalf("no settings: want off, got %+v", s)
	}
	if l := strings.Join(SandboxLines(h.repo), "\n"); !strings.Contains(l, "Bash sandbox: off") {
		t.Errorf("status when off: %s", l)
	}
	if SandboxSessionLine(h.repo) != "" {
		t.Error("session start mentions the sandbox while it's off")
	}

	write(t, user, `{"sandbox":{"enabled":true}}`)
	s := SandboxState(h.repo)
	if !s.On || s.Source != "user settings" || s.PlumblineOut || s.DataFenced || s.NoEscape {
		t.Fatalf("user turns it on, nothing else: %+v", s)
	}
	lines := strings.Join(SandboxLines(h.repo), "\n")
	for _, want := range []string{"on (from user settings)", "can't save proof", "seal key", "retry a refused command"} {
		if !strings.Contains(lines, want) {
			t.Errorf("status misses %q:\n%s", want, lines)
		}
	}
	if l := SandboxSessionLine(h.repo); !strings.Contains(l, "aren't excluded") || !strings.Contains(l, "! plumbline setup sandbox") {
		t.Errorf("session line when Plumbline isn't excluded: %s", l)
	}

	write(t, local, `{"sandbox":{"enabled":false}}`)
	if s := SandboxState(h.repo); s.On {
		t.Errorf("the checkout's local false outranks the user's true: %+v", s)
	}
	write(t, managed, `{"sandbox":{"enabled":true,"allowUnsandboxedCommands":true}}`)
	write(t, local, `{"sandbox":{"enabled":false,"allowUnsandboxedCommands":false}}`)
	if s := SandboxState(h.repo); !s.On || s.Source != "managed settings" || s.NoEscape {
		t.Errorf("managed settings win over local ones: %+v", s)
	}
	must(t, os.Remove(managed))
	write(t, local, `{"sandbox":{"allowUnsandboxedCommands":true}}`)
	write(t, user, `{"sandbox":{"enabled":true,"allowUnsandboxedCommands":false}}`)
	if s := SandboxState(h.repo); !s.NoEscape {
		t.Errorf("a user false holds against a checkout's true: %+v", s)
	}

	// excludedCommands entries, combined across files.
	for _, c := range []struct {
		rule string
		out  bool
	}{
		{"plumbline *", true}, {"plumbline:*", true}, {"plumb*", true},
		{"plumbline", false}, {"plumbline run *", false}, {"plumblinex *", false}, {"docker *", false},
	} {
		write(t, project, `{"sandbox":{"excludedCommands":[`+jsonString(c.rule)+`]}}`)
		if got := SandboxState(h.repo).PlumblineOut; got != c.out {
			t.Errorf("excludedCommands %q: Plumbline out = %v, want %v", c.rule, got, c.out)
		}
	}

	// denyRead paths: absolute, "//" absolute, relative to the checkout,
	// the key itself, and paths that don't cover the key.
	rel, err := filepath.Rel(h.repo, DataDir())
	must(t, err)
	for _, c := range []struct {
		path   string
		fenced bool
	}{
		{DataDir(), true}, {"/" + DataDir(), true}, {rel, true}, {filepath.Join(DataDir(), ".key"), true},
		{filepath.Dir(DataDir()), true}, {filepath.Join(DataDir(), "projects"), false}, {"./src", false},
	} {
		write(t, local, `{"sandbox":{"filesystem":{"denyRead":[`+jsonString(c.path)+`]}}}`)
		if got := SandboxState(h.repo).DataFenced; got != c.fenced {
			t.Errorf("denyRead %q: fenced = %v, want %v", c.path, got, c.fenced)
		}
	}

	// allowRead over the key, or the file fence switched off, opens it again.
	write(t, local, `{"sandbox":{"filesystem":{"denyRead":[`+jsonString(DataDir())+`]}}}`)
	write(t, project, `{"sandbox":{"filesystem":{"allowRead":[`+jsonString(DataDir())+`]}}}`)
	if SandboxState(h.repo).DataFenced {
		t.Error("allowRead re-opens the key")
	}
	write(t, project, `{}`)
	write(t, user, `{"sandbox":{"enabled":true,"filesystem":{"disabled":true}}}`)
	if s := SandboxState(h.repo); s.DataFenced || len(s.Notes) != 1 || !strings.Contains(s.Notes[0], "filesystem.disabled") {
		t.Errorf("filesystem.disabled: %+v", s)
	}
	write(t, user, `{"sandbox":{"enabled":true}}`)

	// A broken file is skipped with a note; the others still count.
	write(t, project, `{"sandbox":`)
	s = SandboxState(h.repo)
	if !s.On || len(s.Notes) != 1 || !strings.Contains(s.Notes[0], ".claude/settings.json isn't valid JSON") {
		t.Errorf("broken project settings: %+v", s)
	}
	write(t, project, `{"sandbox":"yes"}`)
	if s := SandboxState(h.repo); len(s.Notes) != 1 {
		t.Errorf("a sandbox key that isn't an object is noted: %+v", s)
	}
}

func jsonString(s string) string { b, _ := json.Marshal(s); return string(b) }

// plumbline setup sandbox writes the checkout's local settings, keeps every
// other setting, adds nothing twice, keeps the file out of git, and undo
// puts back exactly what was there.
func TestSetupSandboxWritesAndUndoes(t *testing.T) {
	h, _ := sandboxHarness(t)
	local := filepath.Join(h.repo, ".claude", "settings.local.json")
	before := `{"permissions":{"allow":["Bash(ls:*)"]},"sandbox":{"enabled":false,"excludedCommands":["psql *"],"network":{"allowedDomains":["registry.npmjs.org"]}}}`
	write(t, local, before)

	for i := 0; i < 2; i++ {
		if _, err := EnableSandbox(h.repo, []string{"psql", "mysql"}); err != nil {
			t.Fatalf("enable %d: %v", i, err)
		}
	}
	m := readJSONFile(t, local)
	sb, _ := m["sandbox"].(map[string]any)
	if sb["enabled"] != true || sb["allowUnsandboxedCommands"] != false {
		t.Errorf("sandbox keys: %v", sb)
	}
	if _, ok := m["permissions"]; !ok {
		t.Error("other settings were dropped")
	}
	if nw, _ := sb["network"].(map[string]any); nw == nil || len(nw["allowedDomains"].([]any)) != 1 {
		t.Errorf("the user's network settings were changed: %v", sb["network"])
	}
	count := func(list any, x string) int {
		n := 0
		l, _ := list.([]any)
		for _, v := range l {
			if v == x {
				n++
			}
		}
		return n
	}
	for _, e := range []string{"plumbline *", "docker *", "psql *", "mysql *"} {
		if count(sb["excludedCommands"], e) != 1 {
			t.Errorf("excludedCommands has %q %d times: %v", e, count(sb["excludedCommands"], e), sb["excludedCommands"])
		}
	}
	fs, _ := sb["filesystem"].(map[string]any)
	if count(fs["denyRead"], DataDir()) != 1 || count(fs["denyWrite"], DataDir()) != 1 {
		t.Errorf("Plumbline's folder isn't fenced once: %v", fs)
	}
	s := SandboxState(h.repo)
	if !s.On || s.Source != SandboxLocalFile || !s.PlumblineOut || !s.DataFenced || !s.NoEscape {
		t.Errorf("after setup sandbox: %+v", s)
	}
	if l := SandboxSessionLine(h.repo); !strings.Contains(l, "on its own line") {
		t.Errorf("session line: %s", l)
	}
	if st := run(t, h.repo, "git", "status", "--porcelain"); strings.Contains(st, "settings.local.json") {
		t.Errorf("the local settings show up in git: %s", st)
	}
	exclude := filepath.Join(h.repo, ".git", "info", "exclude")
	if b, _ := os.ReadFile(exclude); strings.Count(string(b), "/"+SandboxLocalFile) != 1 {
		t.Errorf("info/exclude: %s", b)
	}

	must(t, UndoSandbox(h.repo))
	var want, got map[string]any
	must(t, json.Unmarshal([]byte(before), &want))
	got = readJSONFile(t, local)
	if wb, _ := json.Marshal(want); string(wb) != mustJSON(t, got) {
		t.Errorf("undo didn't put the file back:\nwant %s\ngot  %s", wb, mustJSON(t, got))
	}
	if b, _ := os.ReadFile(exclude); strings.Contains(string(b), SandboxLocalFile) || strings.Contains(string(b), sandboxExcludeComment) {
		t.Errorf("undo left the exclude line: %s", b)
	}
	if err := UndoSandbox(h.repo); err == nil {
		t.Error("a second undo should say there's nothing to undo")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	return string(b)
}

// With no local settings file, undo removes the file it made; an entry the
// user added in between stays.
func TestSetupSandboxUndoKeepsLaterEntries(t *testing.T) {
	h, _ := sandboxHarness(t)
	local := filepath.Join(h.repo, ".claude", "settings.local.json")
	if _, err := EnableSandbox(h.repo, nil); err != nil {
		t.Fatal(err)
	}
	must(t, UndoSandbox(h.repo))
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Errorf("undo should remove the file it made: %v", err)
	}

	if _, err := EnableSandbox(h.repo, nil); err != nil {
		t.Fatal(err)
	}
	m := readJSONFile(t, local)
	m["model"] = "opus" // Claude Code or the user wrote this since
	b, _ := json.Marshal(m)
	must(t, os.WriteFile(local, b, 0o644))
	must(t, UndoSandbox(h.repo))
	got := readJSONFile(t, local)
	if got["model"] != "opus" || got["sandbox"] != nil {
		t.Errorf("after undo: %v", got)
	}

	// The user's own exclude line (no Plumbline comment above it) stays.
	exclude := filepath.Join(h.repo, ".git", "info", "exclude")
	ex, _ := os.ReadFile(exclude)
	must(t, os.WriteFile(exclude, append(ex, []byte("/"+SandboxLocalFile+"\n")...), 0o644))
	if _, err := EnableSandbox(h.repo, nil); err != nil {
		t.Fatal(err)
	}
	must(t, UndoSandbox(h.repo))
	if b, _ := os.ReadFile(exclude); strings.Count(string(b), "/"+SandboxLocalFile) != 1 || strings.Contains(string(b), sandboxExcludeComment) {
		t.Errorf("the user's own exclude line: %s", b)
	}
}

func TestSetupSandboxRefusals(t *testing.T) {
	h, _ := sandboxHarness(t)
	local := filepath.Join(h.repo, ".claude", "settings.local.json")

	write(t, local, `{"sandbox": [`)
	if _, err := EnableSandbox(h.repo, nil); err == nil || !strings.Contains(err.Error(), "isn't valid JSON") {
		t.Errorf("broken JSON: %v", err)
	}
	if b, _ := os.ReadFile(local); string(b) != `{"sandbox": [` {
		t.Errorf("broken file was changed: %s", b)
	}
	if _, err := os.Stat(sandboxUndoPath(h.repo)); !os.IsNotExist(err) {
		t.Error("an undo record was left for a change that didn't happen")
	}

	write(t, local, `{"sandbox": {"excludedCommands": "docker"}}`)
	if _, err := EnableSandbox(h.repo, nil); err == nil || !strings.Contains(err.Error(), "excludedCommands isn't a list") {
		t.Errorf("excludedCommands not a list: %v", err)
	}

	write(t, local, `{}`)
	run(t, h.repo, "git", "add", "-f", SandboxLocalFile)
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: shared local settings")
	if _, err := EnableSandbox(h.repo, nil); err == nil || !strings.Contains(err.Error(), "committed") {
		t.Errorf("a committed local settings file: %v", err)
	}
	if b, _ := os.ReadFile(local); string(b) != `{}` {
		t.Errorf("committed file was changed: %s", b)
	}

	for _, bad := range []string{"psql;rm -rf /", "-x", "../bin/x", "python", "bash", "NPX", "sudo", "env", "a b", "git", "find", "awk"} {
		if _, err := ParseExclusions(bad); err == nil {
			t.Errorf("--exclude %q should be refused", bad)
		}
	}
	got, err := ParseExclusions(" psql, mysql,psql,,")
	if err != nil || strings.Join(got, ",") != "psql,mysql" {
		t.Errorf("ParseExclusions: %v %v", got, err)
	}
}

// A key that's there but can't be read is never replaced, nothing sealed
// without it verifies, and commands say why.
func TestSealKeyUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	newHarness(t)
	if err := CheckDataDir(); err != nil {
		t.Fatalf("a fresh folder: %v", err)
	}
	key := filepath.Join(DataDir(), ".key")
	orig, err := os.ReadFile(key)
	must(t, err)
	good := seal([]byte("x"))
	must(t, os.Chmod(key, 0o000))
	t.Cleanup(func() { _ = os.Chmod(key, 0o600) })

	if a, b := seal([]byte("x")), seal([]byte("x")); a == good || a == b {
		t.Errorf("without the key, seals must never match anything: %q %q (good %q)", a, b, good)
	}
	err = CheckDataDir()
	if err == nil || !strings.Contains(err.Error(), "sandbox") || !strings.Contains(err.Error(), "plumbline setup sandbox") {
		t.Errorf("CheckDataDir: %v", err)
	}
	must(t, os.Chmod(key, 0o600))
	if now, _ := os.ReadFile(key); string(now) != string(orig) {
		t.Error("an unreadable key was replaced")
	}
	if seal([]byte("x")) != good {
		t.Error("the key's seal changed")
	}

	must(t, os.Chmod(DataDir(), 0o500))
	t.Cleanup(func() { _ = os.Chmod(DataDir(), 0o700) })
	if err := CheckDataDir(); err == nil || !strings.Contains(err.Error(), "can't read or write") {
		t.Errorf("a folder that can't be written: %v", err)
	}
}

// Two checkouts of one repo share the exclude line: undo in one keeps it
// while the other still has the settings.
func TestSetupSandboxWorktreesShareTheExcludeLine(t *testing.T) {
	h, _ := sandboxHarness(t)
	wt := filepath.Join(filepath.Dir(h.repo), "wt")
	run(t, h.repo, "git", "worktree", "add", "-q", "--detach", wt)
	wt, _ = filepath.EvalSymlinks(wt)
	for _, r := range []string{h.repo, wt} {
		if _, err := EnableSandbox(r, nil); err != nil {
			t.Fatal(err)
		}
	}
	exclude := filepath.Join(h.repo, ".git", "info", "exclude")
	must(t, UndoSandbox(h.repo))
	if b, _ := os.ReadFile(exclude); !strings.Contains(string(b), "/"+SandboxLocalFile) {
		t.Errorf("undo in one checkout took the line the other needs: %s", b)
	}
	if st := run(t, wt, "git", "status", "--porcelain"); strings.Contains(st, "settings.local.json") {
		t.Errorf("the worktree's settings show in git: %s", st)
	}
	must(t, UndoSandbox(wt))
	if b, _ := os.ReadFile(exclude); strings.Contains(string(b), SandboxLocalFile) {
		t.Errorf("the last undo should take the line out: %s", b)
	}
}

// Turning the sandbox on, or undoing it, asks the user.
func TestSetupSandboxAsks(t *testing.T) {
	h, _ := sandboxHarness(t)
	for cmd, want := range map[string]string{
		"plumbline setup sandbox --exclude psql": "Bash sandbox",
		"plumbline setup undo-sandbox":           "sandbox settings Plumbline added",
	} {
		d, r := decision(h.bash(cmd))
		if d != "ask" || !strings.Contains(r, want) {
			t.Errorf("%s: %s %s", cmd, d, r)
		}
	}
}
