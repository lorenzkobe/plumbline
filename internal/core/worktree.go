package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Builders work in their own copy of the repo (a git worktree), so two
// builders can't clash and the lead's tree only changes when a copy is merged
// back. Plumbline makes the copies itself, from the lead's code as it is (the
// task branch plus uncommitted work), because Claude Code's own worktrees
// start from the default branch.

// WorktreesDir holds the copies, inside the main checkout, where Claude Code
// keeps its own worktrees. It's left out of the main checkout's changed-files
// check, fingerprint and diffs.
const WorktreesDir = ".claude/worktrees"

func inWorktreesDir(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == WorktreesDir || strings.HasPrefix(rel, WorktreesDir+"/")
}

// BuilderWorktree is where a builder gets its copy: named by its agent id,
// so builders started at the same moment never pick the same folder.
func BuilderWorktree(root, taskID, agentID string) string {
	return filepath.Join(copiesDir(root), copyPrefix+taskID+"-"+safeName(agentID))
}

// copyPrefix starts the folder name of every builder's copy, so Claude
// Code's own worktrees (claude --worktree) in the same folder aren't taken
// for one.
const copyPrefix = "plumbline-"

func copiesDir(root string) string {
	return filepath.Join(ProjectRoot(root), filepath.FromSlash(WorktreesDir))
}

// insideWorktrees: abs is inside one of the builders' copies.
func insideWorktrees(root, abs string) bool {
	if root == "" || abs == "" {
		return false
	}
	rel, err := filepath.Rel(foldPath(copiesDir(root)), foldPath(abs))
	if err != nil || rel == "." || climbsOut(rel) || filepath.IsAbs(rel) {
		return false
	}
	first := strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
	return strings.HasPrefix(first, foldPath(copyPrefix))
}

// copiesExist: some builder's copy of this project is on disk now.
func copiesExist(root string) bool {
	entries, err := os.ReadDir(copiesDir(root))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), copyPrefix) {
			return true
		}
	}
	return false
}

// gitIn runs git with an identity of its own, so commit-tree works on a
// laptop without user.name set and never borrows the user's name.
func gitIn(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GIT_OPTIONAL_LOCKS=0",
		"GIT_AUTHOR_NAME=Plumbline", "GIT_AUTHOR_EMAIL=plumbline@localhost",
		"GIT_COMMITTER_NAME=Plumbline", "GIT_COMMITTER_EMAIL=plumbline@localhost"), env...)
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(string(ee.Stderr)))
	}
	return string(out), err
}

// snapshotCommit records a working tree's code as a commit object (no branch
// moves, nothing staged): its fingerprint tree on top of parent.
func snapshotCommit(root string, ignore []string, parent string) (commit, tree string, err error) {
	tree, err = Fingerprint(root, ignore)
	if err != nil {
		return "", "", err
	}
	commit, err = commitTree(root, tree, parent, "plumbline: snapshot")
	return commit, tree, err
}

// commitTree makes a commit object of tree (no branch moves).
func commitTree(root, tree, parent, message string) (string, error) {
	args := []string{"commit-tree", tree, "-m", message}
	if parent != "" {
		args = append(args, "-p", parent)
	}
	out, err := gitIn(root, nil, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// addDetached checks commit out at path as a detached worktree. The repo's
// own checkout hooks don't run there: they're for the user's checkouts, and
// some install things or take long.
func addDetached(root, path, commit string, env []string) error {
	_, err := gitIn(root, env, "-c", "core.hooksPath="+os.DevNull, "worktree", "add", "--detach", "--quiet", path, commit)
	return err
}

// excludeWorktrees adds the copies' folder to the repo's local exclude file
// (never committed), so git status and searches in the main checkout skip
// them.
func excludeWorktrees(root string) {
	addGitExclude(root, "Plumbline builders' copies", "/"+WorktreesDir+"/")
}

// gitExcludeFile is the repo's local exclude file (shared by its worktrees,
// never committed), or "" for a bare repo or something unusual.
func gitExcludeFile(root string) string {
	common := filepath.Join(ProjectRoot(root), ".git")
	if fi, err := os.Stat(common); err != nil || !fi.IsDir() {
		return ""
	}
	return filepath.Join(common, "info", "exclude")
}

// addGitExclude adds line, under a comment, to the local exclude file and
// reports whether it added it (false when it was there or couldn't be
// written).
func addGitExclude(root, comment, line string) bool {
	p := gitExcludeFile(root)
	if p == "" {
		return false // the path filters still apply
	}
	b, _ := os.ReadFile(p)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == line {
			return false
		}
	}
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		b = append(b, '\n')
	}
	b = append(b, []byte("# "+comment+"\n"+line+"\n")...)
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil || os.WriteFile(p, b, 0o644) != nil {
		return false
	}
	return true
}

// removeGitExclude takes line out of the local exclude file, with the
// comment addGitExclude wrote right above it. A line without that comment
// above it isn't Plumbline's, so it stays.
func removeGitExclude(root, comment, line string) {
	p := gitExcludeFile(root)
	if p == "" {
		return
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	lines := strings.Split(string(b), "\n")
	var keep []string
	for _, l := range lines {
		if n := len(keep); strings.TrimSpace(l) == line && n > 0 && keep[n-1] == "# "+comment {
			keep = keep[:n-1]
			continue
		}
		keep = append(keep, l)
	}
	_ = os.WriteFile(p, []byte(strings.Join(keep, "\n")), 0o644)
}

// MakeWorktree makes a builder's copy at path from the lead's code in root.
// The copy is detached (no branch to clean up); its HEAD is the base commit,
// so git diff inside it shows only the builder's changes.
func MakeWorktree(root string, ignore []string, path string) (base, baseTree string, err error) {
	if _, err := os.Stat(path); err == nil {
		return "", "", fmt.Errorf("%s already exists", path)
	}
	base, baseTree, err = snapshotCommit(root, ignore, HeadCommit(root))
	if err != nil {
		return "", "", err
	}
	excludeWorktrees(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	if err := addDetached(root, path, base, nil); err != nil {
		return "", "", err
	}
	return base, baseTree, nil
}

// RemoveWorktree deletes a builder's copy and git's record of it.
func RemoveWorktree(root, path string) error {
	if path == "" {
		return nil
	}
	if !insideWorktrees(root, Abs(path, "/")) {
		return fmt.Errorf("%s isn't a builder's copy", path)
	}
	_, err := gitIn(ProjectRoot(root), nil, "worktree", "remove", "--force", path)
	if err != nil {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			_, _ = gitIn(ProjectRoot(root), nil, "worktree", "prune")
			return nil
		}
	}
	return err
}

// ErrConflict: a builder's changes overlap changes already in the lead's
// tree; nothing was written.
var ErrConflict = errors.New("overlapping changes")

// MergeWorktree brings a builder's changes into root with a three-way merge:
// base is the code the copy started from, ours the lead's code now, theirs
// the copy now. Nothing is written unless the whole merge is clean. It
// returns the files it changed in root.
func MergeWorktree(root, path, base string, ignore []string) (changed, conflicts []string, err error) {
	ours, oursTree, err := snapshotCommit(root, ignore, base)
	if err != nil {
		return nil, nil, err
	}
	theirs, _, err := snapshotCommit(path, ignore, base)
	if err != nil {
		return nil, nil, err
	}
	out, err := gitIn(root, nil, "merge-tree", "--write-tree", "--name-only", "--no-messages", ours, theirs)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 && len(lines) > 1 {
			return nil, uniq(lines[1:]), ErrConflict
		}
		if strings.Contains(err.Error(), "usage:") || strings.Contains(err.Error(), "unknown option") {
			return nil, nil, errors.New("merging a builder's copy needs git 2.38 or newer (git merge-tree --write-tree)")
		}
		return nil, nil, err
	}
	result := strings.TrimSpace(lines[0])
	diff, err := gitIn(root, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-status", oursTree, result)
	if err != nil {
		return nil, nil, err
	}
	type change struct{ status, path string }
	var todo []change
	parts := strings.Split(diff, "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		st, p := parts[i], parts[i+1]
		if st == "" || p == "" {
			continue
		}
		if strings.HasPrefix(p, "/") || strings.Contains("/"+p+"/", "/../") || strings.HasPrefix(p, ".git/") || inWorktreesDir(p) {
			return nil, nil, fmt.Errorf("refusing to write %s", p)
		}
		if IsSecretFile(filepath.Join(root, p)) || p == RepoSetupFile {
			return nil, nil, fmt.Errorf("the copy changed %s, which builders don't change; drop it or copy the rest by hand", p)
		}
		todo = append(todo, change{st[:1], p})
	}
	// Check every write first, and read every new file, so a problem leaves
	// the tree untouched: a folder that becomes a file (or the reverse) is
	// fine only when the merge removes everything in the way.
	dels := map[string]bool{}
	for _, c := range todo {
		if c.status == "D" {
			dels[c.path] = true
		}
	}
	clear := map[string]bool{} // folders to remove before writing a file there
	for _, c := range todo {
		if c.status == "D" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(c.path))
		if fi, err := os.Lstat(full); err == nil && fi.IsDir() {
			if err := onlyDeleted(root, full, dels); err != nil {
				return nil, nil, fmt.Errorf("can't put the file %s where a folder is: %v", c.path, err)
			}
			clear[full] = true
		}
		for q := filepath.Dir(c.path); q != "." && q != "/"; q = filepath.Dir(q) {
			if fi, err := os.Lstat(filepath.Join(root, q)); err == nil && !fi.IsDir() && !dels[filepath.ToSlash(q)] {
				return nil, nil, fmt.Errorf("can't put %s in a folder: %s is a file the merge doesn't remove", c.path, q)
			}
		}
	}
	type blob struct {
		mode, data string
	}
	blobs := map[string]blob{}
	for _, c := range todo {
		if c.status == "D" {
			continue
		}
		ls, err := gitIn(root, nil, "ls-tree", "-z", result, "--", c.path)
		if err != nil {
			return nil, nil, err
		}
		meta, _, _ := strings.Cut(strings.TrimRight(ls, "\x00"), "\t")
		f := strings.Fields(meta) // mode type oid
		if len(f) != 3 {
			return nil, nil, fmt.Errorf("can't read %s from the merge", c.path)
		}
		if f[1] != "blob" {
			return nil, nil, fmt.Errorf("%s is a %s, which Plumbline doesn't merge", c.path, f[1])
		}
		var data string
		if f[0] == "120000" {
			data, err = gitIn(root, nil, "cat-file", "blob", f[2])
		} else {
			// Through the repo's filters (line endings, LFS, ...), as a
			// checkout would write it.
			data, err = gitIn(root, nil, "cat-file", "--filters", "--path="+c.path, f[2])
		}
		if err != nil {
			return nil, nil, err
		}
		blobs[c.path] = blob{f[0], data}
	}
	// Deletions first, so a file can take a removed folder's place.
	for _, c := range todo {
		if c.status != "D" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(c.path))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return changed, nil, err
		}
		changed = append(changed, c.path)
		removeEmptyParents(root, filepath.Dir(full))
	}
	for dir := range clear {
		if err := os.RemoveAll(dir); err != nil {
			return changed, nil, err
		}
	}
	for _, c := range todo {
		if c.status == "D" {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(c.path))
		b := blobs[c.path]
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return changed, nil, err
		}
		if fi, err := os.Lstat(full); err == nil && (fi.Mode()&os.ModeSymlink != 0 || b.mode == "120000") {
			_ = os.Remove(full)
		}
		switch b.mode {
		case "120000":
			err = os.Symlink(b.data, full)
		case "100755":
			err = os.WriteFile(full, []byte(b.data), 0o755)
			if err == nil {
				err = os.Chmod(full, 0o755)
			}
		default:
			err = os.WriteFile(full, []byte(b.data), 0o644)
			if err == nil {
				err = os.Chmod(full, 0o644) // WriteFile keeps an existing file's mode
			}
		}
		if err != nil {
			return changed, nil, err
		}
		changed = append(changed, c.path)
	}
	return changed, nil, nil
}

// onlyDeleted: every file under dir is one the merge deletes.
func onlyDeleted(root, dir string, dels map[string]bool) error {
	return filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if !dels[filepath.ToSlash(rel)] {
			return fmt.Errorf("%s is still in it", filepath.ToSlash(rel))
		}
		return nil
	})
}

// removeEmptyParents removes folders a deletion left empty, up to root.
func removeEmptyParents(root, dir string) {
	for IsInside(dir, root) && dir != root {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// worktreeChanges lists the files a builder's copy changed since its base.
func worktreeChanges(path string, ignore []string, baseTree string) []string {
	tree, err := Fingerprint(path, ignore)
	if err != nil || tree == baseTree {
		return nil
	}
	out, err := gitIn(path, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-only", baseTree, tree)
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

// treeChanges lists the files that differ between two trees.
func treeChanges(root, a, b string) ([]string, error) {
	if a == b {
		return nil, nil
	}
	out, err := gitIn(root, nil, "diff-tree", "-r", "-z", "--no-renames", "--name-only", a, b)
	if err != nil {
		return nil, err
	}
	var res []string
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			res = append(res, p)
		}
	}
	return res, nil
}

// BuilderRun is builder run n (1-based) of a task.
func (t *Task) BuilderRun(n int) (*HelperRun, error) {
	k := 0
	for i := range t.Helpers {
		if t.Helpers[i].Kind != HelperBuilder {
			continue
		}
		k++
		if k == n {
			return &t.Helpers[i], nil
		}
	}
	return nil, fmt.Errorf("task %s has no builder %d (plumbline builder list)", t.ID, n)
}

// unmergedCopies: the numbers of builder copies still waiting for
// plumbline builder merge (not merged, dropped or refused by a merge).
func (t *Task) unmergedCopies() []string {
	var out []string
	k := 0
	for _, r := range t.Helpers {
		if r.Kind != HelperBuilder {
			continue
		}
		k++
		if r.Worktree != "" && !r.Merged && !r.Dropped && !r.MergeRefused {
			out = append(out, strconv.Itoa(k))
		}
	}
	return out
}

// MergeBuilder brings builder n's copy into the task's code and removes the
// copy. On overlapping changes nothing is written and the copy stays.
func MergeBuilder(t *Task, n int, ignore []string) (changed, conflicts []string, err error) {
	r, err := t.BuilderRun(n)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case t.Phase != PhaseBuilding:
		return nil, nil, fmt.Errorf("task %s is at the %s step", t.ID, t.Phase)
	case r.Merged:
		return nil, nil, fmt.Errorf("builder %d is already merged", n)
	case r.Dropped:
		return nil, nil, fmt.Errorf("builder %d's copy was dropped", n)
	case r.Worktree == "":
		return nil, nil, fmt.Errorf("builder %d has no copy (%s)", n, r.Error)
	case t.qaRunning():
		return nil, nil, errors.New("blind QA is running and must not see the new code; merge once it has stopped")
	case t.reviewRunning() != "":
		return nil, nil, fmt.Errorf("the %s is reviewing this checkout, and its review counts only if the code doesn't change under it; merge once it hands back", t.reviewRunning())
	case r.alive():
		return nil, nil, fmt.Errorf("builder %d is still running; wait for it to stop", n)
	case r.Status == "blocked" || r.Status == "needs context":
		return nil, nil, fmt.Errorf("builder %d stopped as %q, so its work isn't finished (its notes, %s, say why). Drop it (plumbline builder drop %d) and give the part to a new builder with what it needed, do it yourself, or ask the user", n, r.Status, r.Findings, n)
	}
	if _, err := os.Stat(r.Worktree); err != nil {
		return nil, nil, fmt.Errorf("builder %d's copy is gone (%s)", n, r.Worktree)
	}
	changed, conflicts, err = MergeWorktree(t.Root, r.Worktree, r.BaseCommit, ignore)
	if err != nil {
		return changed, conflicts, err
	}
	r.Merged = true
	_ = RemoveWorktree(t.Root, r.Worktree)
	return changed, nil, nil
}

// DropBuilder throws builder n's copy away, unmerged.
func DropBuilder(t *Task, n int) error {
	r, err := t.BuilderRun(n)
	if err != nil {
		return err
	}
	switch {
	case r.Merged:
		return fmt.Errorf("builder %d is already merged", n)
	case r.alive():
		return fmt.Errorf("builder %d is still running; wait for it to stop", n)
	}
	if err := RemoveWorktree(t.Root, r.Worktree); err != nil {
		return err
	}
	r.Dropped = true
	return nil
}

// BuilderState says where builder run r stands, for plumbline builder list.
func (r HelperRun) BuilderState(ignore []string) string {
	switch {
	case r.Merged:
		return "merged"
	case r.Dropped && r.PrintAfter != "" && r.PrintAfter == r.PrintBefore:
		return "no changes (copy removed)"
	case r.Dropped:
		return "dropped"
	case r.Worktree == "":
		return "no copy: " + r.Error
	case r.alive():
		return "running"
	case r.Finished.IsZero():
		return "stopped without a record (treat as finished)"
	}
	files := worktreeChanges(r.Worktree, ignore, r.PrintBefore)
	status := r.Status
	if status == "" {
		status = "no status in its notes"
	}
	return fmt.Sprintf("finished (%s), %d files changed, not merged yet", status, len(files))
}

// BuilderSetupTime: how long the setup's builder_setup command may run in a
// new copy. SubagentStart's hook timeout (hooks.json) leaves room for it and
// for making the copy.
const BuilderSetupTime = 150 * time.Second

// RunBuilderSetup runs the setup's builder_setup command in a new builder
// copy, so the copy has its own dependencies (B2). It returns what the
// builder should know: "" when it went well. The command may install into
// ignored folders only: tracked or new files it changes are put back, so
// they can't be merged as the builder's work.
func RunBuilderSetup(path, command string, ignore []string, tree string) string {
	ctx, cancel := context.WithTimeout(context.Background(), BuilderSetupTime)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = path
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	var notes []string
	switch {
	case ctx.Err() != nil:
		notes = append(notes, fmt.Sprintf("the setup's builder_setup (%s) didn't finish in %s, so the copy may lack its dependencies", command, BuilderSetupTime))
	case err != nil:
		notes = append(notes, fmt.Sprintf("the setup's builder_setup (%s) failed (%v): %s", command, err, lastLines(buf.String(), 3)))
	}
	if now, ferr := Fingerprint(path, ignore); ferr == nil && now != tree {
		changed, _ := treeChanges(path, tree, now)
		_, _ = gitIn(path, nil, "reset", "-q", "--hard")
		_, _ = gitIn(path, nil, "clean", "-q", "-fd")
		notes = append(notes, fmt.Sprintf("the setup's builder_setup (%s) changed files git tracks or would track (%s); Plumbline put them back, so fix the command (a frozen lockfile flag)", command, listSome(changed, 5)))
	}
	return strings.Join(notes, "; ")
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}
