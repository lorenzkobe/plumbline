package core

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// A check that fails after the task's first change, with no run from before
// that change, runs once more on the approved code (Task.BaseTree) in a
// temporary worktree, so a failure that was already there isn't the task's.
// The suite no longer runs before the first edit (0.8.3).

const basePrefix = "plumbline-base-"

// relatedBaseMax: the longest saved suite time (seconds) whose base run a
// related run makes in its own call.
const relatedBaseMax = 90

// git-ignored inputs a suite reads that a checkout of the approved tree lacks
var reBaseInput = regexp.MustCompile(`(?:^|/)(?:node_modules|\.venv|venv|\.env(?:\.[^/]+)?)$`)

// baseKind: the kind and command a failed run is compared through. A related
// run is judged against the full suite's baseline, so its base run is the
// full suite.
func baseKind(r *Run, s *Setup) (string, string) {
	if r.Kind == KindRelated {
		return KindTest, savedCommand(s, KindTest)
	}
	return r.Kind, r.Command
}

// baseRunWanted: r is a finished run in the task's main checkout after the
// task's first change that failed (a passing full suite with skipped tests
// too: the baseline's skips aren't the task's), the task has an approved
// tree to compare with, and no baseline or base run of that kind exists
// yet. A builder's copy lacks the checkout's ignored dependencies, so its
// runs never make the task's one base run.
func baseRunWanted(root string, t *Task, r *Run, s *Setup) bool {
	if foldPath(filepath.Clean(root)) != foldPath(filepath.Clean(t.Root)) || insideWorktrees(t.Root, root) {
		return false
	}
	if s == nil || t.Phase != PhaseBuilding || t.BaseTree == "" || t.Hotfix() || t.LastCodeChange.IsZero() || r.Baseline || r.OnBase || !r.Complete() || r.TimedOut {
		return false
	}
	// Tests only: lint, build, format, typecheck and security can fail in the
	// copy for what it lacks (generated and ignored files), which would excuse
	// the task's own problems, so those run before the first edit instead.
	switch r.Kind {
	case KindTest:
		if r.Exit == 0 && r.Failed == 0 && r.Skipped == 0 {
			return false
		}
	case KindRelated:
		if r.Exit == 0 && r.Failed == 0 {
			return false
		}
	default:
		return false
	}
	kind, cmd := baseKind(r, s)
	if cmd == "" {
		return false
	}
	runs := LoadRuns(root)
	if len(baselineRuns(runs, t.ID, kind)) > 0 {
		return false
	}
	for _, b := range runs {
		if b.OnBase && b.TaskID == t.ID && b.Kind == kind && b.Command == cmd && b.PrintBefore == t.BaseTree && (b.Complete() || b.TimedOut) {
			return false // one that was stopped or killed may be made again
		}
	}
	return true
}

// baseRunAfter makes the base run r calls for, if any, and says what it
// found or why there is none.
func baseRunAfter(root string, t *Task, r *Run, s *Setup, out io.Writer) {
	if !baseRunWanted(root, t, r, s) {
		return
	}
	kind, cmd := baseKind(r, s)
	if r.Kind == KindRelated && (s.SuiteSeconds <= 0 || s.SuiteSeconds > relatedBaseMax) {
		// The base run is the full suite; inside a related run's call it
		// would likely be cut off by the call's time limit, every round.
		fmt.Fprintf(out, "\nplumbline: related tests failed and no run from before the first edit says how they stood. To tell old failures from new, run plumbline run test in the background: when it fails, it runs once more on the approved code.\n")
		return
	}
	if r.Exit == 0 && r.Failed == 0 {
		fmt.Fprintf(out, "\nplumbline: the suite passed with %d skipped, and no run from before the first edit says how many were skipped then, so it runs once more on the approved code (a temporary copy) to tell old skips from new.\n", r.Skipped)
	} else {
		fmt.Fprintf(out, "\nplumbline: %s failed and no run from before the first edit says how it stood, so %s runs once more on the approved code (a temporary copy) to tell old failures from new.\n", r.Kind, kind)
	}
	b, why := runOnBase(root, t, kind, cmd, s)
	switch {
	case why != "":
		fmt.Fprintf(out, "plumbline: no base run (%s), so every failure counts as the task's.\n", why)
	case b.TimedOut:
		fmt.Fprintf(out, "plumbline: the base run stopped at its time limit, so it isn't a baseline and every failure counts.\n")
	case testKind(kind):
		fmt.Fprintf(out, "plumbline: on the approved code, %d ran and %d failed.\n", b.Ran, b.Failed)
	default:
		fmt.Fprintf(out, "plumbline: on the approved code it exited %d with %d problems.\n", b.Exit, b.Problems)
	}
}

// runOnBase runs command on the task's approved tree in a temporary
// worktree and records it as a baseline run. why says why it couldn't.
func runOnBase(root string, t *Task, kind, command string, s *Setup) (*Run, string) {
	sweepBase(root)
	commit, err := commitTree(root, t.BaseTree, "", "plumbline: approved code")
	if err != nil {
		return nil, "git couldn't make a commit of the approved code: " + err.Error()
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s%d-", basePrefix, os.Getpid()))
	if err != nil {
		return nil, "couldn't make a temporary folder: " + err.Error()
	}
	wt := filepath.Join(dir, filepath.Base(root))
	defer removeBase(root, dir, wt)
	if err := addDetached(root, wt, commit, []string{"GIT_LFS_SKIP_SMUDGE=1"}); err != nil {
		return nil, "git couldn't check out the approved code: " + err.Error()
	}
	if err := linkBaseInputs(root, wt); err != nil {
		return nil, err.Error()
	}
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	r := &Run{ID: newID() + "-" + kind, TaskID: t.ID, Kind: kind, Command: command, Started: Now(), PrintBefore: t.BaseTree, Ran: -1,
		Root: wt, IgnoreKey: ignoreKey(ignore), EnvKey: envKey(root), Baseline: true, OnBase: true,
		Rebased: t.BaselinePrint != "" && t.BaselinePrint == t.BaseTree}
	if err := appendRun(root, r); err != nil {
		return nil, "couldn't record the run: " + err.Error()
	}
	text := runIn(root, wt, r, kind, command, s, io.Discard)
	r.PrintAfter = t.BaseTree
	readRun(root, nil, r, kind, toRepoPaths(text, wt, root), s)
	if err := appendRun(root, r); err != nil {
		return nil, "couldn't record the run: " + err.Error()
	}
	return r, ""
}

// toRepoPaths: the base run's output names the worktree; failing tests are
// matched by name with the repo's own runs.
func toRepoPaths(text, wt, root string) string {
	if real, err := filepath.EvalSymlinks(wt); err == nil && real != wt {
		text = strings.ReplaceAll(text, real, root)
	}
	return strings.ReplaceAll(text, wt, root)
}

// linkBaseInputs links the checkout's git-ignored dependency folders and
// env files into the worktree, at any depth. A path the worktree already
// has is never replaced, and none is linked through a symlinked folder. A
// dependency folder that points back into the checkout (a workspace
// package, an editable install) would load the task's own code there, so
// it stops the base run.
func linkBaseInputs(root, wt string) error {
	out, err := gitOut(root, "ls-files", "--others", "--ignored", "--exclude-standard", "--directory", "-z")
	if err != nil {
		return fmt.Errorf("git couldn't list the ignored files to link in: %v", err)
	}
	for _, p := range strings.Split(out, "\x00") {
		rel := strings.TrimSuffix(p, "/")
		if rel == "" || !reBaseInput.MatchString(rel) || inWorktreesDir(rel) || strings.HasPrefix(rel, ".claude/") {
			continue
		}
		at := filepath.Join(wt, filepath.FromSlash(rel))
		if _, err := os.Lstat(at); err == nil || !plainDirs(wt, filepath.Dir(at)) {
			continue
		}
		src := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		if why := pointsIntoRepo(root, src); why != "" {
			return fmt.Errorf("%s %s, so the approved code can't be run apart from the task's", rel, why)
		}
		if err := os.Symlink(filepath.Join(root, filepath.FromSlash(rel)), at); err != nil {
			return fmt.Errorf("couldn't link %s into the base copy: %v", rel, err)
		}
	}
	return nil
}

// plainDirs: every folder from wt down to dir exists and is a real folder,
// not a symlink.
func plainDirs(wt, dir string) bool {
	rel, err := filepath.Rel(wt, dir)
	if err != nil || climbsOut(rel) {
		return false
	}
	at := wt
	if rel == "." {
		return true
	}
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		at = filepath.Join(at, part)
		fi, err := os.Lstat(at)
		if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return false
		}
	}
	return true
}

// pointsIntoRepo: why a dependency folder reaches the checkout's own code:
// a node_modules entry (or @scope entry) that is a link into the repo, or a
// virtualenv whose .pth, egg-link or editable finder names the repo.
func pointsIntoRepo(root, dep string) string {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "couldn't be checked"
	}
	inRepo := func(p string) bool {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || climbsOut(rel) {
			return false
		}
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			if part == "node_modules" {
				return false // a package store (pnpm's node_modules/.pnpm), not the repo's code
			}
		}
		return true
	}
	switch filepath.Base(dep) {
	case "node_modules":
		entries, err := os.ReadDir(dep)
		if err != nil {
			return "couldn't be read"
		}
		for _, e := range entries {
			p := filepath.Join(dep, e.Name())
			if strings.HasPrefix(e.Name(), "@") && e.IsDir() {
				scoped, err := os.ReadDir(p)
				if err != nil {
					return "couldn't be read"
				}
				for _, se := range scoped {
					if se.Type()&os.ModeSymlink != 0 && inRepo(filepath.Join(p, se.Name())) {
						return "links " + e.Name() + "/" + se.Name() + " to the repo's own code"
					}
				}
				continue
			}
			if e.Type()&os.ModeSymlink != 0 && inRepo(p) {
				return "links " + e.Name() + " to the repo's own code"
			}
		}
	case ".venv", "venv":
		hits, _ := filepath.Glob(filepath.Join(dep, "lib", "*", "site-packages", "*"))
		for _, h := range hits {
			b := filepath.Base(h)
			if !strings.HasSuffix(b, ".pth") && !strings.HasSuffix(b, ".egg-link") && !strings.HasPrefix(b, "__editable__") {
				continue
			}
			data, err := os.ReadFile(h)
			if err != nil {
				return "couldn't be read"
			}
			if namesRepoCode(string(data), dep, root, realRoot) {
				return "has an editable install of the repo (" + b + ")"
			}
		}
	}
	return ""
}

// namesRepoCode: text names a path in the repo outside the virtualenv dep
// (paths inside dep are the virtualenv's own packages).
var rePathEnd = regexp.MustCompile(`["'\s,)\]}]`)

func namesRepoCode(text, dep string, roots ...string) bool {
	realDep, _ := filepath.EvalSymlinks(dep)
	for _, r := range roots {
		for rest := text; ; {
			i := strings.Index(rest, r)
			if i < 0 {
				break
			}
			p := rest[i:]
			if j := rePathEnd.FindStringIndex(p); j != nil {
				p = p[:j[0]]
			}
			rest = rest[i+len(r):]
			if p != r && !strings.HasPrefix(p, r+string(filepath.Separator)) {
				continue
			}
			if !strings.HasPrefix(p, dep) && (realDep == "" || !strings.HasPrefix(p, realDep)) {
				return true
			}
		}
	}
	return false
}

func removeBase(root, dir, wt string) {
	_, _ = gitIn(root, nil, "worktree", "remove", "--force", wt)
	_ = os.RemoveAll(dir)
	_, _ = gitIn(root, nil, "worktree", "prune")
}

// sweepBase removes base worktrees a killed run left behind: their folder
// names the process that made them.
var reBaseDir = regexp.MustCompile(`/` + basePrefix + `(\d+)-[^/]+/[^/]+$`)

func sweepBase(root string) {
	out, err := gitOut(root, "worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	for _, l := range strings.Split(out, "\n") {
		wt, ok := strings.CutPrefix(l, "worktree ")
		if !ok {
			continue
		}
		m := reBaseDir.FindStringSubmatch(filepath.ToSlash(wt))
		if m == nil {
			continue
		}
		if pid, err := strconv.Atoi(m[1]); err == nil && pid != os.Getpid() && syscall.Kill(pid, 0) == syscall.ESRCH {
			removeBase(root, filepath.Dir(wt), wt)
		}
	}
}
