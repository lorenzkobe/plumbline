package core

import (
	"errors"
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

// RunBeforeFix runs the related tests for files on the approved code with
// the task's current tests laid over it, in a temporary worktree: a [bug]
// case's test is seen failing before the fix without setting the fix aside
// in the checkout (git stash can't lose work it never touched).
func RunBeforeFix(root string, t *Task, files []string, s *Setup, out io.Writer) (*Run, error) {
	if t == nil || t.Phase != PhaseBuilding || t.ApprovalPrint == "" {
		return nil, errors.New("--before-fix runs only in a task that is building, on the code as approved")
	}
	if s == nil {
		return nil, errors.New("no project setup; run /plumbline:setup first")
	}
	if foldPath(filepath.Clean(root)) != foldPath(filepath.Clean(t.Root)) || insideWorktrees(t.Root, root) {
		return nil, errors.New("--before-fix runs in the task's main checkout, which has the dependencies to link in; run it there")
	}
	template := RelatedTemplate(s)
	if !strings.Contains(template, "{files}") {
		return nil, errors.New("--before-fix needs a related-tests command that takes files ({files}); without one, set the fix aside, run plumbline run test and see it fail, then put the fix back")
	}
	cases := t.caseTestFiles()
	var rels []string
	for _, f := range files {
		rel := cleanRel(f)
		if rel == "" {
			continue
		}
		if filepath.IsAbs(rel) || climbsOut(filepath.Clean(rel)) {
			return nil, fmt.Errorf("%s is outside the project", f)
		}
		if !TestFile(rel, s) && !contains(cases, rel) {
			return nil, fmt.Errorf("%s isn't a test file: --before-fix runs the [bug] cases' tests on the approved code, so --files takes only tests", f)
		}
		if !contains(rels, rel) {
			rels = append(rels, rel)
		}
	}
	if len(rels) == 0 {
		return nil, errors.New("--before-fix needs --files <the [bug] case tests>")
	}
	fp, err := Fingerprint(root, s.IgnoreChanges)
	if err != nil {
		return nil, err
	}
	tree, err := beforeFixTree(root, t, s, fp)
	if err != nil {
		return nil, fmt.Errorf("git couldn't make the approved code with the current tests: %v", err)
	}
	args := rels
	switch {
	case IsDerivedGo(s):
		pkgs, why := GoRelatedPkgs(root, rels)
		if why != "" {
			return nil, errors.New("no Go packages for these tests (" + why + ")")
		}
		args = pkgs
	case IsDerivedPytest(s):
		tests, why := PytestRelated(root, rels)
		if why != "" {
			return nil, errors.New("no pytest files for these tests (" + why + ")")
		}
		args = tests
	}
	var q []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			a = "./" + a
		}
		q = append(q, shellQuoteWord(a))
	}
	command := strings.ReplaceAll(template, "{files}", strings.Join(q, " "))
	// The copy lacks git-ignored files other than dependencies and env
	// files, and LFS files are pointers there, so a test can fail in it for
	// what it lacks. The same tests run first on the current code in a copy
	// made the same way: only failing without the fix while passing with it
	// shows the bug (0.8.7).
	ctl, err := runInCopy(root, t, s, fp, command, rels, true, io.Discard)
	if err != nil {
		return nil, err
	}
	if v := Judge(ctl, BaselineFor(LoadRuns(root), t.ID, KindTest)); !v.OK || ctl.Ran <= 0 {
		return nil, fmt.Errorf("the tests don't pass in a temporary copy of the current code (%s), so a failure there shows nothing about the fix: the copy lacks git-ignored files other than node_modules, virtualenvs and .env files, and LFS files are pointers in it. Check how they stand in the checkout with plumbline run related --files; if they pass there, show them failing in the checkout with the fix set aside", ctlSummary(ctl))
	}
	return runInCopy(root, t, s, tree, command, rels, false, out)
}

func ctlSummary(r *Run) string {
	switch {
	case r.TimedOut:
		return "stopped at its time limit"
	case r.Ran < 0:
		return fmt.Sprintf("exit %d, test count unreadable", r.Exit)
	}
	return fmt.Sprintf("%d ran, %d failed", r.Ran, r.Failed)
}

// runInCopy runs command on tree in a temporary worktree and records it as
// a before-fix run (control: the same tests on the current code, which
// proves nothing itself).
func runInCopy(root string, t *Task, s *Setup, tree, command string, rels []string, control bool, out io.Writer) (*Run, error) {
	sweepBase(root)
	commit, err := commitTree(root, tree, "", "plumbline: approved code with the current tests")
	if err != nil {
		return nil, fmt.Errorf("git couldn't make a commit of the code to run: %v", err)
	}
	dir, err := os.MkdirTemp("", fmt.Sprintf("%s%d-", basePrefix, os.Getpid()))
	if err != nil {
		return nil, fmt.Errorf("couldn't make a temporary folder: %v", err)
	}
	wt := filepath.Join(dir, filepath.Base(root))
	defer removeBase(root, dir, wt)
	if err := addDetached(root, wt, commit, []string{"GIT_LFS_SKIP_SMUDGE=1"}); err != nil {
		return nil, fmt.Errorf("git couldn't check out the code to run: %v", err)
	}
	if err := linkBaseInputs(root, wt); err != nil {
		return nil, err
	}
	r := &Run{ID: newID() + "-" + KindRelated, TaskID: t.ID, Kind: KindRelated, Command: command, Started: Now(), PrintBefore: tree, Ran: -1,
		Root: wt, IgnoreKey: ignoreKey(s.IgnoreChanges), EnvKey: envKey(root), BeforeFix: true, Control: control, Files: rels}
	if err := appendRun(root, r); err != nil {
		return nil, err
	}
	text := runIn(root, wt, r, KindRelated, command, s, out)
	r.PrintAfter = tree
	readRun(root, t, r, KindRelated, toRepoPaths(text, wt, root), s)
	if err := appendRun(root, r); err != nil {
		return r, err
	}
	return r, nil
}

// BeforeFixMissing: the [bug] case tests in files that the task's runs
// still don't show failing before the fix. An error when none of files is
// a [bug] case's test, or the bug-test check is waived: then the run counts
// for nothing.
func BeforeFixMissing(t *Task, s *Setup, files []string) ([]string, error) {
	any := false
	for _, f := range t.bugCaseFiles() {
		any = any || contains(files, f)
	}
	if !any {
		return nil, errors.New("none of these files is a [bug] case's test in the approved plan, so the run counts for nothing")
	}
	if fp, err := Fingerprint(t.Root, s.IgnoreChanges); err == nil && waived(t, "bug-test", fp) {
		return nil, errors.New("the user waived the bug-test check on this code, so the run counts for nothing")
	}
	fp, err := Fingerprint(t.Root, s.IgnoreChanges)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range bugRedMissing(t, s, fp, LoadRuns(t.Root)) {
		ref := strings.TrimPrefix(m, "[bug] case test ")
		ref, _, _ = strings.Cut(ref, ": ")
		file, _, _ := strings.Cut(ref, "::")
		if contains(files, file) {
			out = append(out, ref)
		}
	}
	return out, nil
}

// beforeFixTree: the approved tree with every test (and case test) that
// differs in fp taken from fp, removed when fp has none.
func beforeFixTree(root string, t *Task, s *Setup, fp string) (string, error) {
	changed, err := treeChanges(root, t.ApprovalPrint, fp)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "plumbline-index-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpName)
	env := []string{"GIT_INDEX_FILE=" + tmpName}
	if _, err := gitIn(root, env, "read-tree", t.ApprovalPrint); err != nil {
		return "", err
	}
	cases := t.caseTestFiles()
	for _, p := range changed {
		if !TestFile(p, s) && !contains(cases, p) {
			continue
		}
		ls, err := gitIn(root, nil, "--literal-pathspecs", "ls-tree", "-z", fp, "--", p)
		if err != nil {
			return "", err
		}
		entry := strings.TrimSuffix(ls, "\x00")
		if entry == "" {
			if _, err := gitIn(root, env, "update-index", "--force-remove", "--", p); err != nil {
				return "", err
			}
			continue
		}
		meta, _, _ := strings.Cut(entry, "\t")
		f := strings.Fields(meta)
		if len(f) != 3 || f[1] != "blob" {
			return "", fmt.Errorf("%s isn't a file in the current code", p)
		}
		if _, err := gitIn(root, env, "update-index", "--add", "--cacheinfo", f[0]+","+f[2]+","+p); err != nil {
			return "", err
		}
	}
	out, err := gitIn(root, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// BeforeFixShows: a before-fix run shows the tests failing without the fix
// only when tests ran, the output said how many, it finished in time and
// some failed. why says what it showed.
func BeforeFixShows(r *Run) (bool, string) {
	switch {
	case r.TimedOut:
		return false, "stopped at its time limit, so it shows nothing; something likely hangs"
	case r.Ran < 0:
		return false, fmt.Sprintf("exit %d, but the output doesn't say how many tests ran (setup can save a tests_ran_pattern), so it shows nothing", r.Exit)
	case r.Ran == 0:
		return false, fmt.Sprintf("exit %d with 0 tests run, so it shows nothing; a test that needs a file the fix adds can't load on the approved code", r.Exit)
	}
	detail := fmt.Sprintf("%d ran, %d failed", r.Ran, r.Failed)
	if names := FailedNames(r.FailedIDs); names != "" {
		detail += ". Failed: " + names
	}
	if r.Failed > 0 {
		return true, detail
	}
	return false, "PASS (" + detail + "). The tests don't fail without the fix, so they don't show the bug yet"
}
