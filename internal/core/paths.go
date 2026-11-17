package core

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
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

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
