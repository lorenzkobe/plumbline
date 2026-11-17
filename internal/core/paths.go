package core

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// DataDir is Plumbline's own folder on the laptop, outside every repo.
// PLUMBLINE_HOME overrides it (used by tests). Pointing it elsewhere only
// hides proof from the hooks, so a fake folder fails closed.
func DataDir() string {
	if d := os.Getenv("PLUMBLINE_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".plumbline")
}

// Now is swappable for tests.
var Now = func() time.Time { return time.Now().UTC() }

// RepoRoot returns the git top level for dir, or "" when dir is not in a repo.
func RepoRoot(dir string) string {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	// Fast path: walk up to the folder holding .git (a dir, or a file for worktrees).
	if d, err := filepath.EvalSymlinks(dir); err == nil {
		for cur := d; ; {
			if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
				return cur
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	out, err := gitOut(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	p := strings.TrimSpace(out)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p
}

// ProjectKey is a short stable id for a repo path.
func ProjectKey(root string) string {
	s := sha256.Sum256([]byte(root))
	return filepath.Base(root) + "-" + hex.EncodeToString(s[:])[:10]
}

func ProjectDir(root string) string {
	return filepath.Join(DataDir(), "projects", ProjectKey(root))
}

func TaskDir(root, id string) string {
	return filepath.Join(ProjectDir(root), "tasks", id)
}

// PluginRoot is the installed plugin's folder: Claude Code sets it for hooks;
// otherwise it's found from the binary (dist/ under the plugin root).
func PluginRoot() string {
	if r := os.Getenv("CLAUDE_PLUGIN_ROOT"); r != "" {
		return r
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(filepath.Dir(exe))
}

// ChecklistPath is the checklist the challenger and every reviewer share.
func ChecklistPath() string {
	return filepath.Join(PluginRoot(), "skills", "task", "review-checklist.md")
}

// TaskDocsDir is the only part of the data folder Claude may write to
// (understanding.md, plan.md, report.md).
func TaskDocsDir(root, id string) string {
	return filepath.Join(TaskDir(root, id), "docs")
}

func gitOut(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	b, err := cmd.Output()
	return string(b), err
}

func CurrentBranch(root string) string {
	out, err := gitOut(root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// IsInside reports whether path is inside dir (both absolute).
func IsInside(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

func Abs(p, cwd string) string {
	if p == "" {
		return ""
	}
	if strings.HasPrefix(p, "~/") {
		h, _ := os.UserHomeDir()
		p = filepath.Join(h, p[2:])
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	p = filepath.Clean(p)
	// Resolve symlinks on the deepest existing parent so /tmp vs /private/tmp match.
	dir := p
	rest := ""
	for i := 0; i < 64; i++ {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			if rest == "" {
				return r
			}
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if rest == "" {
			rest = filepath.Base(dir)
		} else {
			rest = filepath.Join(filepath.Base(dir), rest)
		}
		dir = parent
	}
	return p
}

// writeFileAtomic writes through a temp file with a name of its own, so two
// writers never share one, then renames it into place.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// realCase returns an existing path with the letter case it has on disk.
// macOS disks ignore case, so SRC/B.go is src/b.go there; on a disk that
// keeps case, a different case is a different file and nothing changes.
func realCase(p string) string {
	if p == "" || runtime.GOOS != "darwin" || !filepath.IsAbs(p) {
		return p
	}
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(p), "/"), "/")
	cur := "/"
	for i, part := range parts {
		next := filepath.Join(cur, part)
		if _, err := os.Lstat(next); err != nil {
			return filepath.Join(append([]string{cur}, parts[i:]...)...)
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return filepath.Join(append([]string{cur}, parts[i:]...)...)
		}
		name := part
		exact := false
		for _, e := range entries {
			if e.Name() == part {
				exact = true
				break
			}
		}
		if !exact {
			for _, e := range entries {
				if strings.EqualFold(e.Name(), part) {
					name = e.Name()
					break
				}
			}
		}
		cur = filepath.Join(cur, name)
	}
	return cur
}
