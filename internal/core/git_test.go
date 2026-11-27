package core

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A same-size edit made in the same second git last indexed the file must
// still change the fingerprint (git's "racy clean" case). Otherwise proof
// recorded on the old code would look current.
func TestFingerprintSeesQuickSameSizeEdit(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(map[bool]string{false: "one add", true: "retried add"}[retry], func(t *testing.T) {
			quickSameSizeEdit(t, retry)
		})
	}
}

func quickSameSizeEdit(t *testing.T, retry bool) {
	var globs []string
	for attempt := 0; attempt < 5; attempt++ {
		h := newHarness(t)
		if retry {
			ignoredSetupFile(t, h.repo)
		}
		for time.Now().Nanosecond() > 200e6 { // start early in a second
			time.Sleep(10 * time.Millisecond)
		}
		sec := time.Now().Unix()
		a := filepath.Join(h.repo, "src", "a.go")
		write(t, a, "package src\n\nfunc A() int { return 5 }\n")
		run(t, h.repo, "git", "commit", "-qam", "five")
		write(t, a, "package src\n\nfunc A() int { return 6 }\n") // same size
		if time.Now().Unix() != sec {
			continue // crossed a second boundary; not the racy case
		}
		for time.Now().Unix() == sec { // the fingerprint runs a second later
			time.Sleep(20 * time.Millisecond)
		}
		fp, err := Fingerprint(h.repo, globs)
		must(t, err)
		head := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", "HEAD^{tree}"))
		if fp == head {
			t.Fatal("fingerprint missed a same-size edit made in the same second as the commit")
		}
		if fi, _ := os.Stat(a); fi == nil {
			t.Fatal("missing file")
		}
		return
	}
	t.Skip("couldn't stay inside one second")
}

func oldFingerprint(t *testing.T, root string, ignore []string) string {
	t.Helper()
	tmp := filepath.Join(t.TempDir(), "index")
	must(t, os.WriteFile(tmp, nil, 0o600))
	gd := strings.TrimSpace(run(t, root, "git", "rev-parse", "--git-dir"))
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(root, gd)
	}
	if b, err := os.ReadFile(filepath.Join(gd, "index")); err == nil {
		must(t, os.WriteFile(tmp, b, 0o644))
		fi, err := os.Stat(filepath.Join(gd, "index"))
		must(t, err)
		must(t, os.Chtimes(tmp, fi.ModTime(), fi.ModTime()))
	}
	args := []string{"add", "-A", "--", "."}
	if worktreesUnignored(root) {
		args = append(args, ":(exclude)"+WorktreesDir)
	}
	for _, g := range liveGlobs(root, append(append([]string{}, ignore...), RepoSetupFile)) {
		args = append(args, ":(exclude,glob)"+g)
	}
	env := append(os.Environ(), "GIT_INDEX_FILE="+tmp, "GIT_OPTIONAL_LOCKS=0")
	add := exec.Command("git", args...)
	add.Dir, add.Env = root, env
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("oracle git add: %v: %s", err, out)
	}
	wt := exec.Command("git", "write-tree")
	wt.Dir, wt.Env = root, env
	b, err := wt.Output()
	must(t, err)
	return strings.TrimSpace(string(b))
}

func countGit(t *testing.T) func() []string {
	t.Helper()
	real, err := exec.LookPath("git")
	must(t, err)
	dir := t.TempDir()
	log := filepath.Join(dir, "starts")
	write(t, filepath.Join(dir, "git"), "#!/bin/sh\necho \"$1\" >> '"+log+"'\nexec '"+real+"' \"$@\"\n")
	must(t, os.Chmod(filepath.Join(dir, "git"), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		_ = os.Remove(log)
		return strings.Fields(string(b))
	}
}

func fingerprintMatches(t *testing.T, root string, ignore []string) string {
	t.Helper()
	fp, err := Fingerprint(root, ignore)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	if old := oldFingerprint(t, root, ignore); fp != old {
		t.Fatalf("fingerprint %s differs from the old algorithm's %s", fp, old)
	}
	return fp
}

func dirtyRepo(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	write(t, filepath.Join(h.repo, RepoSetupFile), "{}\n")
	run(t, h.repo, "git", "add", RepoSetupFile)
	run(t, h.repo, "git", "commit", "-qm", "setup")
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 7 }\n")
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 8 }\n")
	run(t, h.repo, "git", "add", "src/b.go")
	write(t, filepath.Join(h.repo, "src", "c.go"), "package src\n")
	must(t, os.Remove(filepath.Join(h.repo, "test.sh")))
	write(t, filepath.Join(h.repo, "coverage", "lcov.info"), "x")
	write(t, filepath.Join(h.repo, RepoSetupFile), "{\"changed\": true}\n")
	return h
}

func TestFingerprintMatchesTheOldAlgorithm(t *testing.T) {
	h := dirtyRepo(t)
	indexBefore, err := os.ReadFile(filepath.Join(h.repo, ".git", "index"))
	must(t, err)
	first := fingerprintMatches(t, h.repo, []string{"src/gen/**"})
	if again := fingerprintMatches(t, h.repo, []string{"src/gen/**"}); again != first {
		t.Fatalf("two fingerprints of one tree differ: %s %s", first, again)
	}
	indexAfter, err := os.ReadFile(filepath.Join(h.repo, ".git", "index"))
	must(t, err)
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Fatal("the fingerprint changed the real index")
	}
	head := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", "HEAD^{tree}"))
	if first == head {
		t.Fatal("premise: the edits should change the tree")
	}
}

func TestFingerprintStartsGitTwice(t *testing.T) {
	h := dirtyRepo(t)
	starts := countGit(t)
	if _, err := Fingerprint(h.repo, nil); err != nil {
		t.Fatal(err)
	}
	if got := starts(); strings.Join(got, " ") != "add write-tree" {
		t.Fatalf("git starts: %v, want add then write-tree", got)
	}
}

func TestFingerprintInALinkedWorktree(t *testing.T) {
	for _, relative := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute gitdir", true: "relative gitdir"}[relative], func(t *testing.T) {
			h := dirtyRepo(t)
			wt := filepath.Join(filepath.Dir(h.repo), "wt")
			run(t, h.repo, "git", "worktree", "add", "-q", wt)
			if relative {
				gd := strings.TrimPrefix(strings.TrimSpace(read(t, filepath.Join(wt, ".git"))), "gitdir: ")
				rel, err := filepath.Rel(wt, gd)
				must(t, err)
				write(t, filepath.Join(wt, ".git"), "gitdir: "+rel+"\n")
			}
			write(t, filepath.Join(wt, RepoSetupFile), "{\"worktree\": 1}\n")
			run(t, wt, "git", "add", RepoSetupFile)
			write(t, filepath.Join(wt, RepoSetupFile), "{\"worktree\": 2}\n")
			starts := countGit(t)
			fp := fingerprintMatches(t, wt, nil)
			if got := starts(); got[0] != "add" || got[1] != "write-tree" {
				t.Fatalf("git starts in the worktree: %v", got[:2])
			}
			main := fingerprintMatches(t, h.repo, nil)
			if fp == main {
				t.Fatal("premise: the worktree's index differs from the main checkout's")
			}
			staged := strings.TrimSpace(run(t, wt, "git", "rev-parse", ":"+RepoSetupFile))
			if got := strings.TrimSpace(run(t, wt, "git", "rev-parse", fp+":"+RepoSetupFile)); got != staged {
				t.Fatalf("the excluded setup file has blob %s, want the worktree's staged %s", got, staged)
			}
		})
	}
}

func TestFingerprintFallsBackToRevParse(t *testing.T) {
	t.Run(".git is a symlink", func(t *testing.T) {
		h := dirtyRepo(t)
		moved := filepath.Join(filepath.Dir(h.repo), "gitdir")
		must(t, os.Rename(filepath.Join(h.repo, ".git"), moved))
		must(t, os.Symlink(moved, filepath.Join(h.repo, ".git")))
		starts := countGit(t)
		fingerprintMatches(t, h.repo, nil)
		if got := starts(); len(got) == 0 || got[0] != "rev-parse" {
			t.Fatalf("a symlinked .git should be asked of git: %v", got)
		}
	})
	t.Run("GIT_DIR set", func(t *testing.T) {
		h := dirtyRepo(t)
		other := filepath.Join(filepath.Dir(h.repo), "elsewhere.git")
		run(t, h.repo, "cp", "-R", ".git", other)
		t.Setenv("GIT_DIR", other)
		t.Setenv("GIT_WORK_TREE", h.repo)
		write(t, filepath.Join(h.repo, RepoSetupFile), "{\"other\": 1}\n")
		run(t, h.repo, "git", "add", RepoSetupFile)
		write(t, filepath.Join(h.repo, RepoSetupFile), "{\"other\": 2}\n")
		starts := countGit(t)
		fp := fingerprintMatches(t, h.repo, nil)
		staged := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", ":"+RepoSetupFile))
		if got := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", fp+":"+RepoSetupFile)); got != staged {
			t.Fatalf("the excluded setup file has blob %s, want the one staged in GIT_DIR's index %s", got, staged)
		}
		if got := starts(); len(got) == 0 || got[0] != "rev-parse" {
			t.Fatalf("GIT_DIR should be asked of git: %v", got)
		}
	})
	t.Run("root is a subfolder", func(t *testing.T) {
		h := dirtyRepo(t)
		sub := filepath.Join(h.repo, "src")
		starts := countGit(t)
		fingerprintMatches(t, sub, nil)
		if got := starts(); len(got) == 0 || got[0] != "rev-parse" {
			t.Fatalf("a folder without .git should be asked of git: %v", got)
		}
	})
}

func TestFingerprintRetriesWithoutIgnoredExcludes(t *testing.T) {
	h := dirtyRepo(t)
	write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\ndist/\n")
	write(t, filepath.Join(h.repo, "dist", "x.js"), "built")
	globs := []string{"dist/**", "coverage/lcov.info", "src/gen/**"}
	starts := countGit(t)
	fingerprintMatches(t, h.repo, globs)
	if got := strings.Join(starts()[:3], " "); got != "check-ignore add write-tree" {
		t.Fatalf("a glob's folder on disk should be filtered before the one add: %s", got)
	}
	must(t, os.RemoveAll(filepath.Join(h.repo, "dist")))
	must(t, os.RemoveAll(filepath.Join(h.repo, "coverage")))
	globs = []string{"dist/**", "coverage/lcov.info"}
	fingerprintMatches(t, h.repo, globs)
	if got := strings.Join(starts()[:2], " "); got != "add write-tree" {
		t.Fatalf("with the ignored folders gone, one add should do: %s", got)
	}
	write(t, filepath.Join(h.repo, "coverage", "html", "index.html"), "x")
	for _, g := range []string{"coverage/lcov.info", "coverage/html/missing/**"} {
		fingerprintMatches(t, h.repo, []string{g})
		if got := strings.Join(starts()[:3], " "); got != "check-ignore add write-tree" {
			t.Fatalf("%s under an ignored folder on disk should be filtered before the one add: %s", g, got)
		}
	}
}

func ignoredSetupFile(t *testing.T, repo string) {
	t.Helper()
	write(t, filepath.Join(repo, ".gitignore"), "coverage/\n"+RepoSetupFile+"\n")
	write(t, filepath.Join(repo, RepoSetupFile), "{}\n")
}

func TestFingerprintRetriesWhenTheSetupFileIsIgnored(t *testing.T) {
	h := newHarness(t)
	ignoredSetupFile(t, h.repo)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 9 }\n")
	starts := countGit(t)
	fingerprintMatches(t, h.repo, nil)
	if got := strings.Join(starts()[:4], " "); got != "add check-ignore add write-tree" {
		t.Fatalf("git starts: %s", got)
	}
}

func TestFingerprintErrorsAreNeverAHash(t *testing.T) {
	t.Run("not a repo", func(t *testing.T) {
		if fp, err := Fingerprint(t.TempDir(), nil); err == nil {
			t.Fatalf("a folder outside git gave a hash: %s", fp)
		}
	})
	t.Run("add fails on both tries", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads unreadable files")
		}
		h := dirtyRepo(t)
		bad := filepath.Join(h.repo, "src", "secret.go")
		write(t, bad, "package src\n")
		must(t, os.Chmod(bad, 0))
		t.Cleanup(func() { _ = os.Chmod(bad, 0o644) })
		starts := countGit(t)
		if fp, err := Fingerprint(h.repo, nil); err == nil {
			t.Fatalf("an unreadable file gave a hash: %s", fp)
		}
		if got := strings.Join(starts(), " "); got != "add check-ignore add" {
			t.Fatalf("a failing add should be retried once, then refused: %s", got)
		}
		write(t, filepath.Join(h.repo, "dist", "x.js"), "built")
		write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\ndist/\n")
		if fp, err := Fingerprint(h.repo, []string{"dist/**"}); err == nil {
			t.Fatalf("an unreadable file gave a hash after filtering: %s", fp)
		}
		if got := strings.Join(starts(), " "); got != "check-ignore add" {
			t.Fatalf("an add that already had filtered globs should fail without a retry: %s", got)
		}
	})
}

func TestFingerprintWithBuilderCopiesFolder(t *testing.T) {
	t.Run("git-ignored, as MakeWorktree leaves it", func(t *testing.T) {
		h := dirtyRepo(t)
		write(t, filepath.Join(h.repo, filepath.FromSlash(WorktreesDir), "1", "a.go"), "package x\n")
		write(t, filepath.Join(h.repo, ".git", "info", "exclude"), WorktreesDir+"/\n")
		starts := countGit(t)
		fingerprintMatches(t, h.repo, nil)
		if got := strings.Join(starts()[:3], " "); got != "check-ignore add write-tree" {
			t.Fatalf("git starts: %s", got)
		}
	})
	t.Run("not ignored", func(t *testing.T) {
		h := dirtyRepo(t)
		inside := filepath.Join(h.repo, filepath.FromSlash(WorktreesDir), "1", "a.go")
		write(t, inside, "package x\n")
		before := fingerprintMatches(t, h.repo, nil)
		write(t, inside, "package y\n")
		if now := fingerprintMatches(t, h.repo, nil); now != before {
			t.Fatal("a file in a builder's copy changed the main checkout's fingerprint")
		}
	})
}

func TestFingerprintExcludeOnIgnoredFolderWithTrackedFile(t *testing.T) {
	h := dirtyRepo(t)
	write(t, filepath.Join(h.repo, "dist", "x.js"), "built")
	run(t, h.repo, "git", "add", "dist/x.js")
	run(t, h.repo, "git", "commit", "-qm", "dist")
	write(t, filepath.Join(h.repo, ".gitignore"), "coverage/\ndist/\n")
	write(t, filepath.Join(h.repo, "dist", "x.js"), "rebuilt")
	globs := []string{"dist/**"}
	if fp, err := Fingerprint(h.repo, globs); err == nil {
		t.Fatalf("git refuses this exclude, as it did for the old algorithm, yet a hash came back: %s", fp)
	}
	run(t, h.repo, "git", "rm", "-q", "--cached", "dist/x.js")
	run(t, h.repo, "git", "commit", "-qm", "untrack dist")
	fingerprintMatches(t, h.repo, globs)
}

func TestFingerprintRetryWithoutAnIndex(t *testing.T) {
	h := newHarness(t)
	must(t, os.Remove(filepath.Join(h.repo, ".git", "index")))
	ignoredSetupFile(t, h.repo)
	starts := countGit(t)
	if fp, err := Fingerprint(h.repo, nil); err == nil {
		t.Fatalf("no index gave a hash: %s", fp)
	}
	if got := strings.Join(starts(), " "); got != "add check-ignore add" {
		t.Fatalf("the retry path wasn't taken: %s", got)
	}
}
