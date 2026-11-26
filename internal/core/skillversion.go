package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Version is the running binary's version, set by main from its build flags.
var Version string

var (
	rePlainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	reSkillPath    = regexp.MustCompile(`/plumbline/([0-9]+\.[0-9]+\.[0-9]+)/skills/`)
)

// oldSkillNote: a session started on an older plugin version keeps reading
// that version's skill text from the plugin cache while the hooks run the
// newer binary. One note a session names where the current text is. A dev
// build (no plain x.y.z) can't tell, so it says nothing.
func oldSkillNote(sessionID, path string) string {
	if !rePlainVersion.MatchString(Version) || sessionID == "" {
		return ""
	}
	m := reSkillPath.FindStringSubmatch(filepath.ToSlash(path))
	if m == nil || m[1] == Version {
		return ""
	}
	dir := filepath.Join(DataDir(), "sessions")
	if os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	mark, err := os.OpenFile(filepath.Join(dir, safeName(sessionID)+".skillwarn"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ""
	}
	mark.Close()
	return fmt.Sprintf("Plumbline: this is Plumbline %s's skill text, but the hooks are %s: read %s instead.", m[1], Version, filepath.Join(pluginRoot(), "skills", "task", "SKILL.md"))
}

func pluginRoot() string {
	if r := strings.TrimSpace(os.Getenv("CLAUDE_PLUGIN_ROOT")); r != "" {
		return r
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return filepath.Dir(filepath.Dir(exe))
}
