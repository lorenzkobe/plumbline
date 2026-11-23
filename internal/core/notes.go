package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

var reNoteDir = regexp.MustCompile(`(?i)(^|/)(skills|agents|commands|hooks|\.claude|\.claude-plugin|\.github|testdata|fixtures|__snapshots__)/`)

var reNotGlob = regexp.MustCompile(`[*?\[\\]`)

func noteCandidate(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "" || strings.HasSuffix(rel, "/") || reNotGlob.MatchString(rel) || climbsOut(rel) || filepath.IsAbs(rel) {
		return false
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
	default:
		return false
	}
	return !GateFile(rel) && !reNoteDir.MatchString(rel) && !IsSecretFile(rel)
}

func notePatterns(rels []string) map[string][]string {
	byPattern := map[string][]string{}
	for _, r := range rels {
		r = filepath.ToSlash(r)
		for _, p := range []string{r, filepath.Base(r)} {
			if !contains(byPattern[p], r) {
				byPattern[p] = append(byPattern[p], r)
			}
		}
	}
	return byPattern
}

func namedInCode(root string, rels []string) (map[string]bool, error) {
	named := map[string]bool{}
	byPattern := notePatterns(rels)
	if len(byPattern) == 0 {
		return named, nil
	}
	var pats []string
	for p := range byPattern {
		pats = append(pats, p)
	}
	sort.Strings(pats)
	args := []string{"-c", "core.quotePath=false", "grep", "-F", "-I", "-o", "-z", "--no-color"}
	for _, p := range pats {
		args = append(args, "-e", p)
	}
	args = append(args, "--")
	out, err := gitOut(root, args...)
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok || ee.ExitCode() != 1 || out != "" {
			return nil, err
		}
	}
	for _, line := range strings.Split(out, "\n") {
		path, match, ok := strings.Cut(line, "\x00")
		if !ok || noteCandidate(path) {
			continue
		}
		for _, r := range byPattern[match] {
			named[r] = true
		}
	}
	if err := namedInLocalInstructions(root, pats, byPattern, named); err != nil {
		return nil, err
	}
	return named, nil
}

func namedInLocalInstructions(root string, pats []string, byPattern map[string][]string, named map[string]bool) error {
	out, err := gitOut(root, "-c", "core.quotePath=false", "ls-files", "-z", "--others", "--exclude-standard", "--",
		":(glob,icase)**/claude*.md", ":(glob,icase)**/agents.md", ":(glob,icase)**/gemini.md", ":(glob,icase)**/.cursorrules")
	if err != nil {
		return err
	}
	files := strings.Split(out, "\x00")
	files = append(files, "CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", "GEMINI.md", ".cursorrules", ".claude/CLAUDE.md")
	for _, p := range files {
		if p == "" || inWorktreesDir(p) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, p))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		text := string(b)
		for _, pat := range pats {
			if strings.Contains(text, pat) {
				for _, r := range byPattern[pat] {
					named[r] = true
				}
			}
		}
	}
	return nil
}

func noteFiles(root string, rels []string) bool {
	if len(rels) == 0 {
		return false
	}
	for _, r := range rels {
		if !noteCandidate(r) {
			return false
		}
	}
	named, err := namedInCode(root, rels)
	return err == nil && len(named) == 0
}

func trackedAny(root string, rels []string) (map[string]bool, error) {
	args := append([]string{"--literal-pathspecs", "-c", "core.quotePath=false", "ls-files", "-z", "--"}, rels...)
	out, err := gitOut(root, args...)
	if err != nil {
		return nil, err
	}
	tracked := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			tracked[p] = true
		}
	}
	return tracked, nil
}

func untrackedNotes(root string, rels []string) map[string]bool {
	ok := map[string]bool{}
	if root == "" {
		return ok
	}
	var cands []string
	for _, r := range rels {
		r = filepath.ToSlash(r)
		if noteCandidate(r) && !contains(cands, r) {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		return ok
	}
	tracked, err := trackedAny(root, cands)
	if err != nil {
		return ok
	}
	var free []string
	for _, r := range cands {
		if !tracked[r] && plainFile(filepath.Join(root, r)) {
			free = append(free, r)
		}
	}
	if len(free) == 0 {
		return ok
	}
	named, err := namedInCode(root, free)
	if err != nil {
		return ok
	}
	for _, r := range free {
		if !named[r] {
			ok[r] = true
		}
	}
	return ok
}

func plainFile(abs string) bool {
	fi, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return true
	}
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}

func untrackedNote(root, rel string) bool {
	return untrackedNotes(root, []string{rel})[filepath.ToSlash(rel)]
}

func changedNames(t *Task, s *Setup) ([]string, error) {
	var ignore []string
	if s != nil {
		ignore = s.IgnoreChanges
	}
	base := t.StartCommit
	if base == "" {
		base = "HEAD"
	}
	seen := map[string]bool{}
	var out []string
	add := func(list string) {
		for _, p := range strings.Split(list, "\x00") {
			if p != "" && !seen[p] && !inWorktreesDir(p) && !MatchAny(ignore, p) {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	d, err := gitOut(t.Root, "-c", "core.quotePath=false", "diff", "--name-only", "-z", "--no-renames", base, "--")
	if err != nil {
		return nil, err
	}
	add(d)
	u, err := gitOut(t.Root, "-c", "core.quotePath=false", "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, err
	}
	add(u)
	for p := range t.OutsidePlan {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

func NotesOnlySoFar(t *Task, s *Setup) bool {
	if t == nil {
		return false
	}
	files, err := changedNames(t, s)
	if err != nil {
		return false
	}
	if len(files) == 0 {
		return true
	}
	return noteFiles(t.Root, files)
}

func notesSinceApproval(t *Task, s *Setup, fp string) bool {
	if t.ApprovalPrint == "" || fp == "" {
		return false
	}
	if fp == t.ApprovalPrint {
		return true
	}
	diff, err := treeChanges(t.Root, t.ApprovalPrint, fp)
	return err == nil && noteFiles(t.Root, diff)
}

func CommandsOnly(t *Task, s *Setup) bool {
	if t == nil || !t.CommandsOnly || t.Hotfix() || len(t.PlanFiles) > 0 {
		return false
	}
	files, err := changedNames(t, s)
	if err != nil {
		return false
	}
	notes := untrackedNotes(t.Root, files)
	for _, f := range files {
		if !notes[filepath.ToSlash(f)] {
			return false
		}
	}
	return true
}

func DocsOnly(t *Task, s *Setup) bool {
	if t == nil || t.Hotfix() || len(t.PlanFiles) == 0 {
		return false
	}
	for _, p := range t.PlanFiles {
		if !noteCandidate(p) {
			return false
		}
	}
	files, err := changedNames(t, s)
	if err != nil {
		return false
	}
	for _, p := range t.PlanFiles {
		if !contains(files, p) {
			files = append(files, p)
		}
	}
	return noteFiles(t.Root, files)
}
