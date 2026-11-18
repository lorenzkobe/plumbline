package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// G6: setup can add Claude Code deny rules for secret files to the user
// settings, so Claude Code skips them for Read, Grep and Glob (folder-wide
// searches included). Other settings and the user's own rules are kept;
// undo removes only the rules Plumbline added.
func TestSetupLaptopDenySecrets(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	sp := filepath.Join(cfg, "settings.json")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	before := `{"model":"opus","permissions":{"allow":["Bash(ls:*)"],"deny":["Read(./private/**)","Read(**/.env)"]}}`
	if err := os.WriteFile(sp, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	perms := func() (map[string]json.RawMessage, []string) {
		t.Helper()
		b, err := os.ReadFile(sp)
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]json.RawMessage
		if err := json.Unmarshal(b, &s); err != nil {
			t.Fatalf("settings not JSON: %v\n%s", err, b)
		}
		var p struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		}
		_ = json.Unmarshal(s["permissions"], &p)
		if string(s["model"]) != `"opus"` || len(p.Allow) != 1 {
			t.Fatalf("other settings changed: %s", b)
		}
		return s, p.Deny
	}
	count := func(list []string, r string) int {
		n := 0
		for _, x := range list {
			if x == r {
				n++
			}
		}
		return n
	}
	for i := 0; i < 2; i++ { // twice: no duplicates
		if out, code := plumbline(t, home, filepath.Join(home, "data"), "", "setup", "laptop", "--deny-secrets"); code != 0 {
			t.Fatalf("setup laptop --deny-secrets: %d\n%s", code, out)
		}
	}
	_, deny := perms()
	for _, r := range []string{"Read(**/.env)", "Read(**/.env.local)", "Read(**/*.pem)", "Read(**/id_ed25519)", "Read(./private/**)"} {
		if count(deny, r) != 1 {
			t.Errorf("deny has %q %d times, want once: %v", r, count(deny, r), deny)
		}
	}
	if count(deny, "Read(**/.env.example)") != 0 {
		t.Errorf("a template is denied: %v", deny)
	}
	// The user adds a rule of their own afterwards; undo keeps it, and the
	// rule they had before Plumbline (Read(**/.env)) too.
	s, deny := perms()
	var p map[string]json.RawMessage
	_ = json.Unmarshal(s["permissions"], &p)
	deny = append(deny, "Read(./mine/**)")
	p["deny"], _ = json.Marshal(deny)
	s["permissions"], _ = json.Marshal(p)
	b, _ := json.Marshal(s)
	if err := os.WriteFile(sp, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := plumbline(t, home, filepath.Join(home, "data"), "", "setup", "undo-laptop"); code != 0 {
		t.Fatalf("undo-laptop: %d\n%s", code, out)
	}
	_, deny = perms()
	if len(deny) != 3 || count(deny, "Read(./private/**)") != 1 || count(deny, "Read(**/.env)") != 1 || count(deny, "Read(./mine/**)") != 1 {
		t.Errorf("after undo deny = %v, want the user's three rules", deny)
	}
}

// plumbline setup sandbox from the terminal: it refuses an interpreter in
// --exclude, writes the checkout's local settings, status shows it, and
// undo-sandbox takes it back out.
func TestSetupSandboxCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	t.Setenv("XDG_CONFIG_HOME", home)
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	data := filepath.Join(home, "data")
	if out, code := plumbline(t, repo, data, "", "setup", "sandbox", "--exclude", "psql,python"); code == 0 || !strings.Contains(out, "python can run any code") {
		t.Fatalf("an interpreter in --exclude: %d\n%s", code, out)
	}
	if out, code := plumbline(t, repo, data, "", "setup", "sandbox", "--exclude"); code == 0 || !strings.Contains(out, "--exclude takes command names") {
		t.Fatalf("a bare --exclude: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatal("a refused setup sandbox wrote settings")
	}
	out, code := plumbline(t, repo, data, "", "setup", "sandbox", "--exclude", "psql")
	if code != 0 || !strings.Contains(out, "psql *") || !strings.Contains(out, "undo-sandbox") {
		t.Fatalf("setup sandbox: %d\n%s", code, out)
	}
	if out, _ := plumbline(t, repo, data, "", "status"); !strings.Contains(out, "Bash sandbox: on (from .claude/settings.local.json)") || strings.Contains(out, "can't save proof") {
		t.Errorf("status after setup sandbox:\n%s", out)
	}
	if out, code := plumbline(t, repo, data, "", "setup", "undo-sandbox"); code != 0 {
		t.Fatalf("undo-sandbox: %d\n%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Error("undo-sandbox left the file it made")
	}
}

// A command that can't write Plumbline's folder (as inside Claude Code's
// sandbox) fails with the reason and the fix, before saving anything.
func TestCommandsSayWhenTheFolderIsFenced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	home := t.TempDir()
	data := filepath.Join(home, "data")
	if err := os.MkdirAll(data, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(data, 0o700) })
	out, code := plumbline(t, home, data, "", "status")
	if code != 1 || !strings.Contains(out, "can't read or write its folder") || !strings.Contains(out, "plumbline setup sandbox") {
		t.Errorf("status with a fenced folder: %d\n%s", code, out)
	}
	if out, code := plumbline(t, home, data, "", "version"); code != 0 {
		t.Errorf("version needs no folder: %d\n%s", code, out)
	}
}
