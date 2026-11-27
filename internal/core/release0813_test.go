package core

// 0.8.13: cargo copies keep their own target dir, cargo failure names
// carry their crate, and a listing with yarn v1's lines around its JSON
// parses. Case numbers are the run's case table.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// C1: the real capture of yarn 1.22.22 running turbo 2.11.13's listing.
func TestRelease0813_C1_YarnV1TurboListingParses(t *testing.T) {
	mrFakeTools(t)
	t.Setenv("MR_LIST", mrFixture(t, "yarn1-turbo-ls.txt"))
	units, why := listUnits(t.TempDir(), MonorepoCmd{Tool: "turbo", Prefix: []string{"yarn"}})
	if why != "" || len(units) != 1 || units[0].name != "a" || units[0].dir != "packages/a" {
		t.Fatalf("yarn v1's 'Done in' line after the JSON should be ignored: %+v %q", units, why)
	}
}

// C2: hand-made, the nx graph capture wrapped in yarn v1's lines.
func TestRelease0813_C2_NxGraphWithTrailingLineMaps(t *testing.T) {
	e := mrProjectTest(t, "nx", "yarn nx run-many -t test")
	text := "yarn run v1.22.22\n$ /work/nx-ws/node_modules/.bin/nx graph --file=stdout\n" + mrText(t, "nx-graph.json") + "\nDone in 1.20s.\n"
	t.Setenv("MR_LIST", mrTemp(t, "graph.txt", text))
	if args, _, why := e.affected("packages/ui/index.js"); why != "" || len(args) == 0 {
		t.Fatalf("the nx graph followed by 'Done in' should map: %v %q", args, why)
	}
}

// C3
func TestRelease0813_C3_NotJSONStillFallsBack(t *testing.T) {
	for _, text := range []string{"yarn run v1.22.22\nerror Command \"nx\" not found.\n", "yarn run v1.22.22\n{\"graph\": \nDone in 0.1s.\n"} {
		e := mrProjectTest(t, "nx", "yarn nx run-many -t test")
		t.Setenv("MR_LIST", mrTemp(t, "bad.txt", text))
		if args, _, why := e.affected("packages/ui/index.js"); why == "" {
			t.Fatalf("output with no complete JSON value mapped: %v\n%s", args, text)
		}
	}
}

// C4: the real capture of two crates each failing tests::two.
func TestRelease0813_C4_TwoCratesFailuresKeepTheirCrate(t *testing.T) {
	c := ParseTestOutput(mrText(t, "cargo-two-crates-fail.txt"), "")
	if c.Ran != 2 || c.Failed != 2 || !mrSame(c.IDs, []string{"a/a: tests::two", "b/b: tests::two"}) || !c.CargoNames {
		t.Fatalf("each crate's tests::two should be its own failure: %+v", c)
	}
	if c := ParseTestOutput(mrText(t, "cargo-fail.txt"), ""); !mrSame(c.IDs, []string{"app/app: tests::two"}) {
		t.Fatalf("the 0.8.12 capture: %+v", c)
	}
}

// C5, C17: hand-made in the layout of cargo before build-dir (deps/), and
// failures with no Running line before them.
func TestRelease0813_C5_OldLayoutDocTestsAndNoRunningLine(t *testing.T) {
	old := "     Running unittests src/lib.rs (target/debug/deps/core_lib-3debfb12cdffe644)\n\nrunning 1 test\ntest tests::two ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n\n" +
		"     Running tests/it.rs (target/debug/deps/it-78ecf94a56eec945)\n\nrunning 1 test\ntest two ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n\n" +
		"   Doc-tests core_lib\n\nrunning 1 test\ntest src/lib.rs - f (line 3) ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.10s\n"
	c := ParseTestOutput(old, "")
	if c.Failed != 3 || !mrSame(c.IDs, []string{"core_lib: tests::two", "it: two"}) || !c.CargoNames {
		t.Fatalf("old layout qualifies by the binary's name: %+v", c)
	}
	bare := "running 1 test\ntest tests::two ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	if c := ParseTestOutput(bare, ""); !mrSame(c.IDs, []string{"tests::two"}) || c.CargoNames {
		t.Fatalf("with no Running line the name stays as cargo printed it: %+v", c)
	}
}

// C15, C16: hand-made Running lines from the same workspace run three ways.
func TestRelease0813_C15_QualifierIgnoresWhereTargetIs(t *testing.T) {
	body := "\n\nrunning 1 test\ntest tests::two ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n"
	for _, dir := range []string{
		"target/debug/build/app/b36a579cdbdf5796/out/app-b36a579cdbdf5796",
		"/Users/x/ws/target/plumbline-copy/debug/build/app/b36a579cdbdf5796/out/app-b36a579cdbdf5796",
		"/cache/cargo/debug/build/app/b36a579cdbdf5796/out/app-b36a579cdbdf5796",
		"target/aarch64-apple-darwin/debug/build/app/b36a579cdbdf5796/out/app-b36a579cdbdf5796",
	} {
		if c := ParseTestOutput("     Running unittests src/lib.rs ("+dir+")"+body, ""); !mrSame(c.IDs, []string{"app/app: tests::two"}) {
			t.Fatalf("%s: %+v", dir, c)
		}
	}
	both := "     Running unittests src/lib.rs (target/debug/build/my-crate/92dff2e28f0269a2/out/my_crate-92dff2e28f0269a2)" + body +
		"     Running tests/it.rs (target/debug/build/my-crate/9e80441840a0bf48/out/it-9e80441840a0bf48)" + body
	if c := ParseTestOutput(both, ""); c.Failed != 2 || !mrSame(c.IDs, []string{"my-crate/it: tests::two", "my-crate/my_crate: tests::two"}) {
		t.Fatalf("the lib and tests/it.rs of one package are told apart: %+v", c)
	}
}

func cargoRun(ids ...string) *Run {
	return &Run{Kind: KindTest, Ran: 5, Failed: len(ids), Exit: 101, FailedIDs: ids, CargoNames: true,
		Finished: Now(), PrintBefore: "x", PrintAfter: "x"}
}

// C6, C7, C18
func TestRelease0813_C6_BaselineNames(t *testing.T) {
	old := &Run{Kind: KindTest, Ran: 5, Failed: 1, Exit: 101, FailedIDs: []string{"tests::two"}, Finished: Now(), PrintBefore: "x", PrintAfter: "x"}
	if v := Judge(cargoRun("a/a: tests::two"), old); !v.OK {
		t.Fatalf("a 0.8.12 baseline's bare tests::two should excuse a/a: tests::two: %+v", v)
	}
	if v := Judge(cargoRun("a/a: tests::three"), old); v.OK || !strings.Contains(v.Reason, "a/a: tests::three") {
		t.Fatalf("a different path is new: %+v", v)
	}
	if v := Judge(cargoRun("b/b: tests::two"), cargoRun("a/a: tests::two")); v.OK {
		t.Fatalf("a qualified baseline's a/a: tests::two must not excuse b/b: tests::two: %+v", v)
	}
	mocha := &Run{Kind: KindTest, Ran: 5, Failed: 1, Exit: 1, FailedIDs: []string{"API: list"}, Finished: Now(), PrintBefore: "x", PrintAfter: "x"}
	base := &Run{Kind: KindTest, Ran: 5, Failed: 1, Exit: 1, FailedIDs: []string{"list"}, Finished: Now(), PrintBefore: "x", PrintAfter: "x"}
	if v := Judge(mocha, base); v.OK {
		t.Fatalf("a non-cargo name with ': ' is matched whole: %+v", v)
	}
}

// C8
func TestRelease0813_C8_BugTestNameMatchesQualifiedID(t *testing.T) {
	for _, name := range []string{"two", "tests::two"} {
		if !failedNamed(testNames(&Run{FailedIDs: []string{"my-crate/it: tests::two"}, CargoNames: true}), name) {
			t.Fatalf("%s should match my-crate/it: tests::two", name)
		}
	}
}

// cargoTargetRepo: a committed repo with Cargo.toml and, when ignored, a
// .gitignore holding target/.
func cargoTargetRepo(t *testing.T, ignored bool) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	must(t, err)
	run(t, root, "git", "init", "-q")
	write(t, filepath.Join(root, "Cargo.toml"), "[workspace]\n")
	if ignored {
		write(t, filepath.Join(root, ".gitignore"), "target/\n")
	}
	run(t, root, "git", "add", "-A")
	run(t, root, "git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "i")
	must(t, os.MkdirAll(filepath.Join(root, "target", "debug"), 0o755))
	return root
}

// copyTargetDirOnce: the dir a copy run gets, its lock let go at once.
func copyTargetDirOnce(t *testing.T, root, wt string) string {
	t.Helper()
	dir, release := copyTarget(root, wt)
	release()
	return dir
}

// C19
func TestRelease0813_C19_ConcurrentCopiesDontShare(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	root := cargoTargetRepo(t, true)
	shared := filepath.Join(root, "target", "plumbline-copy")
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	first, release := copyTarget(root, a)
	if first != shared {
		t.Fatalf("the first copy gets plumbline-copy: %q", first)
	}
	if second, r2 := copyTarget(root, b); second != filepath.Join(b, "target") {
		r2()
		t.Fatalf("while the first holds plumbline-copy, a second copy builds in its own target: %q", second)
	} else {
		r2()
	}
	release()
	if again := copyTargetDirOnce(t, root, b); again != shared {
		t.Fatalf("once let go, plumbline-copy is used again: %q", again)
	}
}

// C20
func TestRelease0813_C20_BugNameIsTheTestPathOnly(t *testing.T) {
	r := &Run{FailedIDs: []string{"parser/parser: tests::unrelated"}, CargoNames: true}
	if failedNamed(testNames(r), "parser") {
		t.Fatal("a case named parser must not match another test's failure in package parser")
	}
	if !failedNamed(testNames(r), "unrelated") || !failedNamed(testNames(r), "tests::unrelated") {
		t.Fatal("the test path still matches")
	}
	mocha := &Run{FailedIDs: []string{"API: list"}}
	if !failedNamed(testNames(mocha), "API: list") {
		t.Fatal("a non-cargo name with ': ' is matched whole")
	}
}

// C9-C13
func TestRelease0813_C9_CopyTargetDir(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	wt := filepath.Join(t.TempDir(), "copy")
	own := filepath.Join(wt, "target")

	root := cargoTargetRepo(t, true)
	if got := copyTargetDirOnce(t, root, wt); got != filepath.Join(root, "target", "plumbline-copy") {
		t.Fatalf("an ignored plain target/ should give its plumbline-copy: %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(root, "target", "plumbline-copy")); err != nil || !fi.IsDir() {
		t.Fatalf("plumbline-copy should be made: %v", err)
	}
	if got := copyTargetDirOnce(t, root, wt); got != filepath.Join(root, "target", "plumbline-copy") {
		t.Fatalf("an existing plumbline-copy is reused: %q", got)
	}

	refuse := func(name string, root string) {
		t.Helper()
		if got := copyTargetDirOnce(t, root, wt); got != own {
			t.Fatalf("%s: want the copy's own %s, got %q", name, own, got)
		}
	}
	refuse("not ignored", cargoTargetRepo(t, false))

	r := cargoTargetRepo(t, true)
	must(t, os.RemoveAll(filepath.Join(r, "target")))
	refuse("no target/", r)

	r = cargoTargetRepo(t, true)
	elsewhere := t.TempDir()
	must(t, os.RemoveAll(filepath.Join(r, "target")))
	must(t, os.Symlink(elsewhere, filepath.Join(r, "target")))
	refuse("target/ a symlink", r)

	r = cargoTargetRepo(t, true)
	must(t, os.Symlink(elsewhere, filepath.Join(r, "target", "plumbline-copy")))
	refuse("plumbline-copy a symlink", r)
	if _, err := os.Lstat(filepath.Join(elsewhere, "debug")); err == nil {
		t.Fatal("nothing may be written through the symlink")
	}

	r = cargoTargetRepo(t, true)
	write(t, filepath.Join(r, "target", "plumbline-copy"), "a file\n")
	refuse("plumbline-copy a file", r)

	r = cargoTargetRepo(t, true)
	must(t, os.Remove(filepath.Join(r, "Cargo.toml")))
	refuse("no Cargo.toml", r)

	r = cargoTargetRepo(t, true)
	must(t, os.RemoveAll(filepath.Join(r, ".git")))
	refuse("git check-ignore fails (not a repo)", r)

	shared := t.TempDir()
	t.Setenv("CARGO_TARGET_DIR", shared)
	if got := copyTargetDirOnce(t, cargoTargetRepo(t, true), wt); got != filepath.Join(shared, "plumbline-copy") {
		t.Fatalf("a CARGO_TARGET_DIR from the env gives its plumbline-copy, never itself: %q", got)
	}
	r = cargoTargetRepo(t, false)
	must(t, os.MkdirAll(filepath.Join(r, "build"), 0o755))
	t.Setenv("CARGO_TARGET_DIR", "build")
	refuse("a relative CARGO_TARGET_DIR in the repo that git doesn't ignore", r)
}

// C9 end to end: the before-fix copies get the persistent dir, the
// checkout's own run gets none.
func TestRelease0813_C9_BeforeFixAndCheckoutRunsTargetEnv(t *testing.T) {
	t.Setenv("CARGO_TARGET_DIR", "")
	h, _ := mrHarness(t, "cargo", func(h *harness) {
		write(t, filepath.Join(h.repo, ".gitignore"), "target/\n")
		run(t, h.repo, "git", "add", ".gitignore")
		run(t, h.repo, "git", "commit", "-q", "-m", "ignore target")
	})
	must(t, os.MkdirAll(filepath.Join(h.repo, "target"), 0o755))
	fake, err := exec.LookPath("cargo")
	must(t, err)
	bin := t.TempDir()
	envLog := filepath.Join(t.TempDir(), "env.log")
	write(t, filepath.Join(bin, "cargo"), "#!/bin/sh\nif [ \"$1\" = test ]; then printf '%s\\n' \"${CARGO_TARGET_DIR:-unset}\" >> \""+envLog+"\"; fi\nexec \""+fake+"\" \"$@\"\n")
	must(t, os.Chmod(filepath.Join(bin, "cargo"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	f := mrTaskFiles["cargo"]
	h.affEdit(f.test, mrToolFiles("cargo")[f.test]+"// new case\n")
	s, _, err := LoadSetup(h.repo)
	must(t, err)
	if _, err := RunBeforeFix(h.repo, h.task(), []string{f.test}, s, io.Discard); err != nil {
		t.Fatalf("before-fix refused: %v", err)
	}
	b, err := os.ReadFile(envLog)
	must(t, err)
	want, err := filepath.EvalSymlinks(filepath.Join(h.repo, "target"))
	must(t, err)
	lines := strings.Fields(string(b))
	if len(lines) != 2 {
		t.Fatalf("the control and the before-fix run should each run cargo test once: %q", lines)
	}
	for _, l := range lines {
		got, err := filepath.EvalSymlinks(filepath.Dir(l))
		if err != nil || got != want || filepath.Base(l) != "plumbline-copy" {
			t.Fatalf("a copy's CARGO_TARGET_DIR should be %s/plumbline-copy: %q", want, l)
		}
	}
	must(t, os.Remove(envLog))
	cliIn(t, h.repo, nil, "run", "related", "--files", f.test)
	b, err = os.ReadFile(envLog)
	must(t, err)
	if strings.TrimSpace(string(b)) != "unset" {
		t.Fatalf("the checkout's own run must not get a CARGO_TARGET_DIR: %q", b)
	}
}

func TestRelease0813_ReleaseText(t *testing.T) {
	var plugin struct{ Version string }
	must(t, json.Unmarshal([]byte(mrRead(t, filepath.Join("..", "..", ".claude-plugin", "plugin.json"))), &plugin))
	if plugin.Version != "0.8.13" {
		t.Errorf("plugin.json version %q, want 0.8.13", plugin.Version)
	}
	log := mrRead(t, filepath.Join("..", "..", "CHANGELOG.md"))
	if !strings.HasPrefix(log, "# Changelog\n\n## 0.8.13") {
		t.Error("CHANGELOG.md should open with the 0.8.13 entry")
	}
	for _, w := range []string{"plumbline-copy", "yarn", "Running"} {
		if !strings.Contains(log[:strings.Index(log, "## 0.8.12")], w) {
			t.Errorf("the 0.8.13 entry should name %s", w)
		}
	}
}
