package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func relatedProject(t *testing.T, test string, files map[string]string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PLUMBLINE_HOME", filepath.Join(home, "data"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "claude"))
	clearServiceEnv(t)
	repo := filepath.Join(home, "repo")
	must(t, os.MkdirAll(repo, 0o755))
	run(t, repo, "git", "init", "-q", "-b", "main")
	run(t, repo, "git", "config", "user.email", "t@example.com")
	run(t, repo, "git", "config", "user.name", "T")
	for p, text := range files {
		write(t, filepath.Join(repo, p), text)
	}
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-q", "--allow-empty", "-m", "init")
	repo, _ = filepath.EvalSymlinks(repo)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = test
	_, err := SaveSetup(repo, s)
	must(t, err)
	return repo
}

func lastRun(root string) *Run {
	runs := LoadRuns(root)
	if len(runs) == 0 {
		return nil
	}
	return runs[len(runs)-1]
}

var goRelatedFiles = map[string]string{
	"go.mod":                       "module example.com/rel\n\ngo 1.21\n",
	"internal/core/a.go":           "package core\n\nfunc A() int { return 1 }\n",
	"internal/core/a_test.go":      "package core\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(A())\n\t}\n}\n",
	"internal/core/data.json":      "{}\n",
	"cmd/x/b.go":                   "package main\n\nfunc main() {}\n",
	"cmd/x/b_test.go":              "package main\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {}\n",
	"internal/other/other.go":      "package other\n\nfunc O() int { return 3 }\n",
	"internal/other/other_test.go": "package other\n\nimport \"testing\"\n\nfunc TestO(t *testing.T) {}\n",
}

func TestGoRelatedRunsTheChangedPackages(t *testing.T) {
	repo := relatedProject(t, "go test ./...", goRelatedFiles)
	out, code := cliIn(t, repo, nil, "run", "related", "--files", "internal/core/a.go,cmd/x/b_test.go")
	r := lastRun(repo)
	if r == nil {
		t.Fatalf("no run recorded: %d %s", code, out)
	}
	if r.Kind != KindRelated || !strings.HasPrefix(r.Command, "go test ") || strings.Contains(r.Command, "./...") {
		t.Fatalf("the related run should be go test over the changed packages, got %s %q\n%s", r.Kind, r.Command, out)
	}
	for _, pkg := range []string{"./internal/core", "./cmd/x"} {
		if !strings.Contains(r.Command, pkg) {
			t.Fatalf("package %s missing from %q", pkg, r.Command)
		}
	}
	if strings.Contains(r.Command, "internal/other") {
		t.Fatalf("an unchanged package ran: %q", r.Command)
	}
	if strings.Join(r.Files, ",") != "internal/core/a.go,cmd/x/b_test.go" && strings.Join(r.Files, ",") != "cmd/x/b_test.go,internal/core/a.go" {
		t.Fatalf("the run should list the changed files, not the packages: %v", r.Files)
	}
	if code != 0 || r.Failed != 0 || r.Ran <= 0 {
		t.Fatalf("the related run should pass: %d %+v\n%s", code, r, out)
	}
}

func TestGoRelatedRunsTheFullSuiteWhenItCantMapTheChange(t *testing.T) {
	for _, c := range []struct {
		name, files, why string
	}{
		{"go.mod changed", "go.mod,internal/core/a.go", "go.mod"},
		{"go.sum changed", "go.sum", "go.sum"},
		{"a non-Go file under a package", "internal/core/data.json", ""},
		{"a deleted folder", "internal/gone/gone.go", ""},
		{"a file outside the project", "../elsewhere/x.go", "outside the project"},
		{"an absolute path", "/tmp/x.go", "outside the project"},
		{"a path that climbs out after cleaning", "x/../../elsewhere/x.go", "outside the project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			repo := relatedProject(t, "go test ./...", goRelatedFiles)
			out, _ := cliIn(t, repo, nil, "run", "related", "--files", c.files)
			r := lastRun(repo)
			if r == nil || r.Command != "go test ./..." {
				t.Fatalf("%s should run the full suite: %+v\n%s", c.name, r, out)
			}
			if c.why != "" && !strings.Contains(out, c.why) {
				t.Fatalf("the full-suite run should say why (%s): %s", c.why, out)
			}
		})
	}
}

func TestRelatedCommandDerivedFromTheTestCommand(t *testing.T) {
	for _, c := range []struct {
		test, want string
	}{
		{"npx jest", "npx jest --findRelatedTests {files}"},
		{"jest", "jest --findRelatedTests {files}"},
		{"npx vitest run", "npx vitest related --run {files}"},
		{"./test.sh", ""},
		{"make test", ""},
		{"go test ./... | tee out.log", ""},
	} {
		s := &Setup{}
		s.Commands.Test = c.test
		if got := savedCommand(s, KindRelated); got != c.want {
			t.Errorf("test %q: related command %q, want %q", c.test, got, c.want)
		}
	}
	s := &Setup{}
	s.Commands.Test = "go test -count=1 -race ./..."
	got := savedCommand(s, KindRelated)
	if !strings.HasPrefix(got, "go test ") || !strings.Contains(got, "-count=1") || !strings.Contains(got, "-race") || !strings.HasSuffix(got, "{files}") || strings.Contains(got, "./...") {
		t.Errorf("go test with safe flags: related command %q", got)
	}
	s.Commands.TestRelated = "echo {files}"
	if got := savedCommand(s, KindRelated); got != "echo {files}" {
		t.Errorf("a saved related command should win: %q", got)
	}
}

func fakeTool(t *testing.T, name, script string) string {
	t.Helper()
	bin := t.TempDir()
	p := filepath.Join(bin, name)
	write(t, p, script)
	must(t, os.Chmod(p, 0o755))
	return bin
}

func TestPytestRelatedRunsTheTestsForTheChangedFiles(t *testing.T) {
	bin := fakeTool(t, "pytest", "#!/bin/sh\necho '== 1 passed in 0.1s =='\n")
	files := map[string]string{
		"src/foo.py":          "x = 1\n",
		"src/lonely.py":       "y = 1\n",
		"tests/test_foo.py":   "def test_foo():\n    pass\n",
		"tests/test_bar.py":   "def test_bar():\n    pass\n",
		"pkg/baz.py":          "z = 1\n",
		"pkg/baz_test.py":     "def test_baz():\n    pass\n",
		"tests/test_other.py": "def test_other():\n    pass\n",
	}
	repo := relatedProject(t, "pytest", files)
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
	out, _ := cliIn(t, repo, env, "run", "related", "--files", "src/foo.py,tests/test_bar.py,pkg/baz.py")
	r := lastRun(repo)
	if r == nil || !strings.HasPrefix(r.Command, "pytest") {
		t.Fatalf("no pytest related run: %+v\n%s", r, out)
	}
	for _, f := range []string{"tests/test_foo.py", "tests/test_bar.py", "pkg/baz_test.py"} {
		if !strings.Contains(r.Command, f) {
			t.Fatalf("%s should run for the changed files: %q", f, r.Command)
		}
	}
	if strings.Contains(r.Command, "test_other.py") {
		t.Fatalf("an unrelated test file ran: %q", r.Command)
	}
	out, _ = cliIn(t, repo, env, "run", "related", "--files", "src/lonely.py")
	if r := lastRun(repo); r == nil || r.Command != "pytest" {
		t.Fatalf("a module with no test found by name should run the full suite: %+v\n%s", r, out)
	}
}

func TestRelatedFilesWithShellCharactersNeverRunAsFlagsOrCommands(t *testing.T) {
	bin := fakeTool(t, "npx", "#!/bin/sh\nfor a in \"$@\"; do echo \"$a\" >> \"$ARGS_FILE\"; done\necho 'Tests:       1 passed, 1 total'\n")
	argsFile := filepath.Join(t.TempDir(), "args")
	repo := relatedProject(t, "npx jest", map[string]string{"src/a.ts": "export const a = 1\n", "src/a.test.ts": "test('a', () => {})\n"})
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "ARGS_FILE=" + argsFile}
	pwned := filepath.Join(repo, "PWNED")
	cliIn(t, repo, env, "run", "related", "--files", "src/$(touch PWNED).ts,src/a.ts;touch PWNED")
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("a file name ran as a shell command")
	}

	write(t, filepath.Join(repo, "--watchAll.ts"), "x\n")
	write(t, filepath.Join(repo, "-u.ts"), "x\n")
	_ = os.Remove(argsFile)
	cliIn(t, repo, env, "run", "related")
	b, err := os.ReadFile(argsFile)
	if err != nil {
		return
	}
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	after := false
	for _, a := range args {
		if a == "--findRelatedTests" {
			after = true
			continue
		}
		if after && strings.HasPrefix(a, "-") {
			t.Fatalf("a changed file reached the runner as a flag: %q in %v", a, args)
		}
	}
	if _, err := os.Stat(pwned); err == nil {
		t.Fatal("a file name ran as a shell command")
	}
}

func goHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	write(t, filepath.Join(h.repo, "go.mod"), "module example.com/r\n\ngo 1.21\n")
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(B())\n\t}\n}\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "go module")
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "go test ./..."
	s.IgnoreChanges = []string{"coverage/**"}
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("small")
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, "# Plan\n## Goal\nDone when: A returns 2.\n## Files\n- src/a.go\n- src/a_test.go\n## Cases\n- [bug] A returns 2 -> test: src/a_test.go\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n")
	h.approve()
	return h
}

func TestDerivedRelatedRunCountsLikeASavedOne(t *testing.T) {
	h := goHarness(t)
	s, _, _ := LoadSetup(h.repo)
	if s.Commands.TestRelated != "" {
		t.Fatalf("the setup must have no saved related command: %q", s.Commands.TestRelated)
	}
	testFile := filepath.Join(h.repo, "src", "a_test.go")
	write(t, testFile, "package src\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(B())\n\t}\n}\n\nfunc TestA(t *testing.T) {\n\tif A() != 2 {\n\t\tt.Fatal(A())\n\t}\n}\n")
	h.post("Edit", map[string]any{"file_path": testFile}, nil)
	red, err := ExecuteRelated(h.repo, h.task(), "go test ./src/", []string{"src/a_test.go"}, s, io.Discard)
	must(t, err)
	if red.Failed == 0 {
		t.Fatalf("the [bug] test should fail before the fix: %+v", red)
	}

	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\nfunc A() int { return 2 }\n")
	h.post("Edit", map[string]any{"file_path": a}, nil)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(t, err)
	if m := bugRedMissing(h.task(), s, fp, LoadRuns(h.repo)); len(m) != 0 {
		t.Fatalf("a derived related run that failed before the fix should count for the [bug] test: %v", m)
	}

	_, err = ExecuteRelated(h.repo, h.task(), "./test.sh", nil, s, io.Discard)
	must(t, err)
	if m := strings.Join(ProofMissing(h.task(), s, false), "; "); !strings.Contains(m, "related tests") {
		t.Fatalf("a related run of another command shouldn't count once a related command is derived: %q", m)
	}

	green, err := ExecuteRelated(h.repo, h.task(), "go test ./src/", []string{"src/a_test.go"}, s, io.Discard)
	must(t, err)
	if green.Failed != 0 || green.Exit != 0 {
		t.Fatalf("the fixed code should pass: %+v", green)
	}
	runs := LoadRuns(h.repo)
	if m := strings.Join(ProofMissing(h.task(), s, false), "; "); strings.Contains(m, "related tests") {
		t.Fatalf("a passing derived related run should satisfy the related check: %q", m)
	}
	if m := caseRunMissing(h.task(), s, fp, runs, ""); len(m) != 0 {
		t.Fatalf("a passing derived related run given the case test should prove it: %v", m)
	}
}

func TestGoRelatedSkipsFilesOutsidePackages(t *testing.T) {
	files := map[string]string{}
	for k, v := range goRelatedFiles {
		files[k] = v
	}
	files["README.md"] = "# r\n"
	files["docs/notes.md"] = "notes\n"
	repo := relatedProject(t, "go test ./...", files)
	out, _ := cliIn(t, repo, nil, "run", "related", "--files", "README.md,docs/notes.md,internal/core/a.go")
	r := lastRun(repo)
	if r == nil || r.Kind != KindRelated || !strings.Contains(r.Command, "./internal/core") || strings.Contains(r.Command, "./...") {
		t.Fatalf("docs outside any package shouldn't force the full suite: %+v\n%s", r, out)
	}
}

func TestGoRelatedRunsTheFullSuiteForFilesAPackageMayEmbed(t *testing.T) {
	files := map[string]string{}
	for k, v := range goRelatedFiles {
		files[k] = v
	}
	files["internal/core/assets/rules.md"] = "rules\n"
	repo := relatedProject(t, "go test ./...", files)
	out, _ := cliIn(t, repo, nil, "run", "related", "--files", "internal/core/assets/rules.md")
	if r := lastRun(repo); r == nil || r.Command != "go test ./..." {
		t.Fatalf("a file under a Go package folder may be embedded, so the full suite runs: %+v\n%s", r, out)
	}
}
