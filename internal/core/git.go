package core

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Fingerprint is the git tree hash of the working tree as it is now,
// including untracked files and excluding git-ignored ones. It is computed in
// a temporary index, so your real staging area is never touched. Proof is
// tied to it: any change to the code makes earlier results stale.
func Fingerprint(root string, ignore []string) (string, error) {
	tmp, err := os.CreateTemp("", "plumbline-index-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	var indexTime time.Time
	// Start from the real index so git can reuse its file stat cache.
	gitDir, err := gitOut(root, "rev-parse", "--git-dir")
	if err == nil {
		gd := strings.TrimSpace(gitDir)
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(root, gd)
		}
		if src, err := os.Open(filepath.Join(gd, "index")); err == nil {
			_, _ = io.Copy(tmp, src)
			if fi, err := src.Stat(); err == nil {
				indexTime = fi.ModTime()
			}
			src.Close()
		}
	}
	tmp.Close()
	// Keep the real index's time on the copy. Git re-reads files changed in
	// the same second the index was written ("racy clean"); a copy with a
	// newer time hides a quick same-size edit, and proof would be recorded
	// against the old code.
	if !indexTime.IsZero() {
		_ = os.Chtimes(tmpName, indexTime, indexTime)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+tmpName, "GIT_OPTIONAL_LOCKS=0")
	args := []string{"add", "-A", "--", "."}
	if worktreesUnignored(root) {
		// Builders' copies sit inside the main checkout and are never its
		// code. (Naming a path git already ignores is an error in git add.)
		args = append(args, ":(exclude)"+WorktreesDir)
	}
	// The setup file isn't code: runs count only for the saved command that
	// made them, so a setup change leaves the proof standing (CFG1).
	for _, g := range liveGlobs(root, append(append([]string{}, ignore...), RepoSetupFile)) {
		args = append(args, ":(exclude,glob)"+g)
	}
	add := exec.Command("git", args...)
	add.Dir = root
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	wt := exec.Command("git", "write-tree")
	wt.Dir = root
	wt.Env = env
	b, err := wt.Output()
	if err != nil {
		return "", fmt.Errorf("git write-tree: %v", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// ChangedSince lists the files that differ from commit: committed since,
// staged, unstaged or new (git-ignored ones excluded), relative to root.
// An empty commit means HEAD.
func ChangedSince(root, commit string) []string {
	if commit == "" {
		commit = "HEAD"
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		for _, p := range strings.Split(s, "\x00") {
			if p != "" && !seen[p] && !inWorktreesDir(p) {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	if s, err := gitOut(root, "diff", "--name-only", "-z", "--no-renames", commit, "--"); err == nil {
		add(s)
	}
	if s, err := gitOut(root, "ls-files", "--others", "--exclude-standard", "-z"); err == nil {
		add(s)
	}
	sort.Strings(out)
	return out
}

// DirtyFiles lists changed and untracked files (not ignored) with a cheap
// size+mtime stamp, so a later call can tell what changed in between.
func DirtyFiles(root string) (map[string]string, error) {
	out, err := gitOut(root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	res := map[string]string{}
	parts := strings.Split(out, "\x00")
	for i := 0; i < len(parts); i++ {
		e := parts[i]
		if len(e) < 4 {
			continue
		}
		code, path := e[:2], e[3:]
		if code[0] == 'R' || code[0] == 'C' {
			i++ // the next entry is the old name
		}
		if inWorktreesDir(path) {
			continue // a builder's copy, not a change to this checkout
		}
		res[path] = stamp(filepath.Join(root, path))
	}
	return res, nil
}

func stamp(p string) string {
	fi, err := os.Stat(p)
	if err != nil {
		return "gone"
	}
	return fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
}

// Changed returns paths whose stamp differs between two snapshots of root.
// A file that left git status with its stamp unchanged was committed (by
// the user's own `! git commit`, say): its content didn't change (CC1).
func Changed(root string, before, after map[string]string) []string {
	var out []string
	for p, s := range after {
		if before[p] != s {
			out = append(out, p)
		}
	}
	for p, s := range before {
		if _, ok := after[p]; !ok && stamp(filepath.Join(root, p)) != s {
			out = append(out, p) // reverted or deleted
		}
	}
	sort.Strings(out)
	return out
}

func MatchAny(globs []string, rel string) bool {
	for _, g := range globs {
		g = strings.TrimSuffix(g, "/**")
		if ok, _ := filepath.Match(g, rel); ok {
			return true
		}
		if rel == g || strings.HasPrefix(rel, strings.TrimSuffix(g, "/")+"/") {
			return true
		}
		if ok, _ := filepath.Match(g, filepath.Base(rel)); ok && !strings.Contains(g, "/") {
			return true
		}
	}
	return false
}

func StagedFiles(root string) []string {
	out, err := gitOut(root, "diff", "--cached", "--name-only", "-z")
	if err != nil {
		return nil
	}
	var res []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

func StagedDiff(root string) string {
	out, _ := gitOut(root, "diff", "--cached", "-U0", "--no-color")
	return out
}

// WorkingDiffPaths is the working-tree diff, limited to paths when given.
func WorkingDiffPaths(root, base string, paths []string) string {
	args := []string{"diff", "-U0", "--no-color"}
	if base != "" {
		args = append(args, base)
	}
	if len(paths) > 0 {
		args = append(append(args, "--"), paths...)
	}
	out, _ := gitOut(root, args...)
	return out
}

// HeadCommit is the commit HEAD points to, or "" in a repo with no commits.
func HeadCommit(root string) string {
	out, err := gitOut(root, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// worktreesUnignored: root holds the builders' copies folder and git doesn't
// ignore it (Plumbline's exclude line, or the project's own .gitignore).
func worktreesUnignored(root string) bool {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(WorktreesDir))); err != nil {
		return false
	}
	_, err := gitOut(root, "check-ignore", "-q", WorktreesDir+"/")
	return err != nil
}

// liveGlobs drops the ignore globs whose fixed leading part git already
// ignores (dist/** with dist/ in .gitignore): they can only name ignored
// files, which git add -A leaves out anyway, and git add refuses a pathspec
// whose fixed part is an ignored path, which made every fingerprint fail
// (SR8). A glob with no fixed folder (**/*.snap) is never refused.
func liveGlobs(root string, globs []string) []string {
	prefix := map[string]string{}
	var ask []string
	for _, g := range globs {
		if p := globPrefix(g); p != "" {
			if _, seen := prefix[g]; !seen {
				prefix[g] = p
				ask = append(ask, p)
			}
		}
	}
	if len(ask) == 0 {
		return globs
	}
	cmd := exec.Command("git", "check-ignore", "--stdin", "-z")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(strings.Join(ask, "\x00") + "\x00")
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			return globs // check-ignore failed: keep them all, as before
		}
	}
	ignored := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			ignored[p] = true
		}
	}
	var live []string
	for _, g := range globs {
		if p := prefix[g]; p == "" || !ignored[p] {
			live = append(live, g)
		}
	}
	return live
}

// DeadIgnoreGlobs: the ignore globs that only name files git already
// ignores, for setup to point out.
func DeadIgnoreGlobs(root string, globs []string) []string {
	live := map[string]bool{}
	for _, g := range liveGlobs(root, globs) {
		live[g] = true
	}
	var dead []string
	for _, g := range globs {
		if !live[g] {
			dead = append(dead, g)
		}
	}
	return dead
}

// globPrefix: the fixed path a glob starts with, up to the folder before its
// first wildcard (dist/*.js: dist), the whole glob when it has none, "" when
// it starts with one.
func globPrefix(g string) string {
	g = strings.TrimPrefix(g, "./")
	i := strings.IndexAny(g, "*?[")
	if i < 0 {
		return strings.TrimSuffix(g, "/")
	}
	j := strings.LastIndex(g[:i], "/")
	if j <= 0 {
		return ""
	}
	return g[:j]
}
