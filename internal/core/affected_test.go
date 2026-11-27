package core

// Blind QA for the affected-final-run task, written from the case table in
// brief-qa-1.md (C1..C25), not from the new code. New names used as the lead
// gave them: GoAffectedPkgs, JSAffectedFiles, AffectedGraph, AffectedProof,
// Run.Graph and Setup.AffectedFinal.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- helpers ---

// affHas: pkgs names the package at rel (repo-relative folder), whether
// written as ./rel, rel or the import path module/rel.
func affHas(pkgs []string, module, rel string) bool {
	for _, p := range pkgs {
		p = strings.TrimSuffix(p, "/")
		if p == "./"+rel || p == rel || p == module+"/"+rel {
			return true
		}
	}
	return false
}

func affFiles(extra map[string]string) map[string]string {
	files := map[string]string{}
	for k, v := range goRelatedFiles {
		files[k] = v
	}
	for k, v := range extra {
		files[k] = v
	}
	return files
}

// affGoFiles: goRelatedFiles with cmd/x importing internal/core, a package
// importing it only from an in-package test, one only from an external
// (_test package) test, and a chain top -> mid -> core.
var affGoFiles = affFiles(map[string]string{
	"cmd/x/b.go":                   "package main\n\nimport \"example.com/rel/internal/core\"\n\nfunc main() { _ = core.A() }\n",
	"internal/t1/t1.go":            "package t1\n\nfunc T() int { return 1 }\n",
	"internal/t1/t1_test.go":       "package t1\n\nimport (\n\t\"testing\"\n\n\t\"example.com/rel/internal/core\"\n)\n\nfunc TestT(t *testing.T) { _ = core.A() }\n",
	"internal/x1/x1.go":            "package x1\n\nfunc X() int { return 1 }\n",
	"internal/x1/x1_test.go":       "package x1_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/rel/internal/core\"\n)\n\nfunc TestX(t *testing.T) { _ = core.A() }\n",
	"internal/mid/mid.go":          "package mid\n\nimport \"example.com/rel/internal/core\"\n\nfunc M() int { return core.A() }\n",
	"internal/top/top.go":          "package top\n\nimport \"example.com/rel/internal/mid\"\n\nfunc T() int { return mid.M() }\n",
	"internal/top/top_test.go":     "package top\n\nimport \"testing\"\n\nfunc TestTop(t *testing.T) { _ = T() }\n",
	"hooks/hooks.json":             "{}\n",
	"README.md":                    "# r\n",
	"internal/other/other_test.go": "package other\n\nimport \"testing\"\n\nfunc TestO(t *testing.T) {}\n",
})

func affGoCmd(t *testing.T, test string) GoTestCmd {
	t.Helper()
	c, ok := ParseGoTest(test)
	if !ok {
		t.Fatalf("ParseGoTest(%q) refused", test)
	}
	return c
}

func affGoEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", "off")
}

// affFakeGo: a go on PATH whose "go list" does what listScript says, and
// that hands every other subcommand to the real go.
func affFakeGo(t *testing.T, listScript string) string {
	t.Helper()
	real, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	return fakeTool(t, "go", "#!/bin/sh\nif [ \"$1\" = list ]; then\n"+listScript+"\nfi\nexec '"+real+"' \"$@\"\n")
}

// affectedHarness: goHarness with an importer of src (cmd/y, whose test
// fails while AFFECTED_QA_FAIL is set), a package nothing imports and with
// no tests (lonely), and Setup.AffectedFinal as given. The suite runs with
// -count=1 so no result comes from go's test cache. The task gets the start
// commit a real task start records.
func affectedHarness(t *testing.T, affectedFinal bool) *harness {
	t.Helper()
	affGoEnv(t)
	h := newHarness(t)
	write(t, filepath.Join(h.repo, "go.mod"), "module example.com/r\n\ngo 1.21\n")
	write(t, filepath.Join(h.repo, "src", "a_test.go"), "package src\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(B())\n\t}\n}\n")
	write(t, filepath.Join(h.repo, "cmd", "y", "y.go"), "package main\n\nimport \"example.com/r/src\"\n\nfunc main() { _ = src.A() }\n")
	write(t, filepath.Join(h.repo, "cmd", "y", "y_test.go"), "package main\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestY(t *testing.T) {\n\tif os.Getenv(\"AFFECTED_QA_FAIL\") != \"\" {\n\t\tt.Fatal(\"asked to fail\")\n\t}\n}\n")
	write(t, filepath.Join(h.repo, "lonely", "l.go"), "package lonely\n\nfunc L() int { return 1 }\n")
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "go module")
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "go test -count=1 ./..."
	s.IgnoreChanges = []string{"coverage/**"}
	s.AffectedFinal = affectedFinal
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("small")
	tk := h.task()
	tk.StartCommit = HeadCommit(h.repo)
	must(t, SaveTask(tk))
	for _, tool := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(tool, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	h.docs(DocPlan, "# Plan\n## Goal\nDone when: A returns 2.\n## Files\n- src/a.go\n- src/a_test.go\n## Cases\n- [bug] A returns 2 -> test: src/a_test.go\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n")
	h.approve()
	return h
}

func (h *harness) affEdit(rel, body string) {
	h.t.Helper()
	p := filepath.Join(h.repo, rel)
	write(h.t, p, body)
	h.post("Edit", map[string]any{"file_path": p}, nil)
}

func (h *harness) affFix() { h.affEdit("src/a.go", "package src\n\nfunc A() int { return 2 }\n") }

func (h *harness) affCLI(env []string, args ...string) (string, int) {
	h.t.Helper()
	return cliIn(h.t, h.repo, env, append(args, "--task", h.task().ID)...)
}

// affRelatedFinal runs plumbline run related --files <files> --final and
// returns the run it recorded.
func (h *harness) affRelatedFinal(files string) *Run {
	h.t.Helper()
	before := len(LoadRuns(h.repo))
	out, _ := h.affCLI(nil, "run", "related", "--files", files, "--final")
	runs := LoadRuns(h.repo)
	if len(runs) == before {
		h.t.Fatalf("run related --final recorded nothing\n%s", out)
	}
	return runs[len(runs)-1]
}

func (h *harness) affProof() (*Run, string) {
	h.t.Helper()
	s, _, err := LoadSetup(h.repo)
	must(h.t, err)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(h.t, err)
	return AffectedProof(h.task(), s, LoadRuns(h.repo), fp), strings.Join(ProofMissing(h.task(), s, true), "; ")
}

func (h *harness) affSetupFP() (*Setup, string) {
	h.t.Helper()
	s, _, err := LoadSetup(h.repo)
	must(h.t, err)
	fp, err := Fingerprint(h.repo, s.IgnoreChanges)
	must(h.t, err)
	return s, fp
}

// affNewRuns: the runs recorded after the first n.
func affNewRuns(root string, n int) []*Run {
	runs := LoadRuns(root)
	if len(runs) <= n {
		return nil
	}
	return runs[n:]
}

func affKinds(runs []*Run) string {
	var k []string
	for _, r := range runs {
		k = append(k, r.Kind+" "+r.Command)
	}
	return strings.Join(k, " | ")
}

// --- Go graph (GoAffectedPkgs) ---

// C1
func TestAffected_C1_GoChangeRunsImportersNotUnrelated(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	pkgs, graph, why := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
	if !graph || why != "" {
		t.Fatalf("a change to internal/core maps through go list: graph=%v why=%q pkgs=%v", graph, why, pkgs)
	}
	if !affHas(pkgs, "example.com/rel", "internal/core") || !affHas(pkgs, "example.com/rel", "cmd/x") {
		t.Fatalf("the changed package and its importer cmd/x should run: %v", pkgs)
	}
	if affHas(pkgs, "example.com/rel", "internal/other") {
		t.Fatalf("internal/other imports nothing changed, yet it runs: %v", pkgs)
	}
}

// C1 through the CLI: while building too, the importers run and the run is
// marked with its graph.
func TestAffected_C1_RunRelatedRunsImportersAndMarksTheGraph(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	out, code := cliIn(t, repo, nil, "run", "related", "--files", "internal/core/a.go")
	r := lastRun(repo)
	if r == nil || r.Kind != KindRelated {
		t.Fatalf("no related run: %d %+v\n%s", code, r, out)
	}
	if !strings.Contains(r.Command, "internal/core") || !strings.Contains(r.Command, "cmd/x") || strings.Contains(r.Command, "internal/other") || strings.Contains(r.Command, "./...") {
		t.Fatalf("the related run should be the changed package plus its importers: %q", r.Command)
	}
	if r.Graph != "go list" {
		t.Fatalf("every file mapped through go list, so the run is marked: graph %q", r.Graph)
	}
	if code != 0 || r.Failed != 0 || r.Ran <= 0 {
		t.Fatalf("the related run should pass: %d %+v\n%s", code, r, out)
	}
}

// C1: the root handed in through a symlink (macOS /var -> /private/var)
// still maps.
func TestAffected_C1_RootThroughSymlinkStillMaps(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	link := filepath.Join(t.TempDir(), "via")
	must(t, os.Symlink(repo, link))
	pkgs, graph, why := GoAffectedPkgs(link, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
	if !graph || why != "" || !affHas(pkgs, "example.com/rel", "cmd/x") {
		t.Fatalf("the same project reached through a symlinked path should map: graph=%v why=%q %v", graph, why, pkgs)
	}
}

// C2
func TestAffected_C2_TestOnlyImportersAreAffected(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	pkgs, graph, why := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
	if !graph || why != "" {
		t.Fatalf("graph=%v why=%q", graph, why)
	}
	if !affHas(pkgs, "example.com/rel", "internal/t1") {
		t.Fatalf("internal/t1 imports the change from its in-package test: %v", pkgs)
	}
	if !affHas(pkgs, "example.com/rel", "internal/x1") {
		t.Fatalf("internal/x1 imports the change from its _test package: %v", pkgs)
	}
}

// C3
func TestAffected_C3_TransitiveImporterIsAffected(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	pkgs, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
	if !graph || !affHas(pkgs, "example.com/rel", "internal/mid") || !affHas(pkgs, "example.com/rel", "internal/top") {
		t.Fatalf("top imports mid imports core: both run: graph=%v %v", graph, pkgs)
	}
}

// C4: go list failing, or printing what can't be read, means no graph.
func TestAffected_C4_GoListFailsMeansNoGraph(t *testing.T) {
	for _, c := range []struct{ name, script string }{
		{"exit 1", "echo 'go: broken' >&2\nexit 1"},
		{"unreadable output", "echo 'not json at all'\nexit 0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			affGoEnv(t)
			repo := relatedProject(t, "go test ./...", affGoFiles)
			t.Setenv("PATH", affFakeGo(t, c.script)+":"+os.Getenv("PATH"))
			_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
			if graph {
				t.Fatal("go list didn't answer, yet the change counts as mapped")
			}
		})
	}
}

// C4 through the CLI: while building, the changed packages only and the run
// not marked; with --final, the full suite.
func TestAffected_C4_GoListFailsBuildingDirectFinalFull(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	env := []string{"PATH=" + affFakeGo(t, "echo 'go: broken' >&2\nexit 1") + ":" + os.Getenv("PATH")}
	out, _ := h.affCLI(env, "run", "related", "--files", "src/a.go")
	r := lastRun(h.repo)
	if r == nil || r.Kind != KindRelated || !strings.Contains(r.Command, "src") || strings.Contains(r.Command, "cmd/y") || strings.Contains(r.Command, "./...") {
		t.Fatalf("with go list failing, the building run is the changed package only: %+v\n%s", r, out)
	}
	if r.Graph != "" {
		t.Fatalf("a run go list didn't map is marked %q", r.Graph)
	}
	n := len(LoadRuns(h.repo))
	out, _ = h.affCLI(env, "run", "related", "--files", "src/a.go", "--final")
	got := affNewRuns(h.repo, n)
	if len(got) == 0 || got[len(got)-1].Kind != KindTest || got[len(got)-1].Command != "go test -count=1 ./..." {
		t.Fatalf("with go list failing, --final runs the full suite: %s\n%s", affKinds(got), out)
	}
}

// C5
func TestAffected_C5_SymlinkedOrUnlistedPackageDirMeansNoGraph(t *testing.T) {
	t.Run("symlinked package dir", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affGoFiles)
		must(t, os.Symlink(filepath.Join(repo, "internal", "core"), filepath.Join(repo, "linked")))
		_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"linked/a.go"})
		if graph {
			t.Fatal("a symlinked package folder is mapped")
		}
	})
	t.Run("nested module", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
			"nested/go.mod": "module example.com/nested\n\ngo 1.21\n",
			"nested/n.go":   "package n\n\nfunc N() int { return 1 }\n",
		}))
		_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"nested/n.go"})
		if graph {
			t.Fatal("a folder go list ./... doesn't list (another module) is mapped")
		}
	})
	t.Run("underscore dir", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
			"_skip/s.go": "package s\n\nfunc S() int { return 1 }\n",
		}))
		_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"_skip/s.go"})
		if graph {
			t.Fatal("a folder go list ignores (_skip) is mapped")
		}
	})
	t.Run("testdata", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
			"internal/core/testdata/t.go": "package t\n",
		}))
		_, graph, why := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/testdata/t.go"})
		if graph || why == "" {
			t.Fatalf("testdata keeps the full suite with its reason: graph=%v why=%q", graph, why)
		}
	})
}

// C6
func TestAffected_C6_GoModOrNonGoFileKeepsTheFullSuite(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	c := affGoCmd(t, "go test ./...")
	pkgs, graph, why := GoAffectedPkgs(repo, c, []string{"go.mod", "internal/core/a.go"})
	if graph || len(pkgs) != 0 || !strings.Contains(why, "go.mod") {
		t.Fatalf("go.mod changed: full suite, saying why: graph=%v pkgs=%v why=%q", graph, pkgs, why)
	}
	pkgs, graph, why = GoAffectedPkgs(repo, c, []string{"internal/core/data.json"})
	if graph || len(pkgs) != 0 || !strings.Contains(why, "internal/core/data.json") {
		t.Fatalf("a non-Go file under a package: full suite, saying why: graph=%v pkgs=%v why=%q", graph, pkgs, why)
	}
}

// C15
func TestAffected_C15_PathCaseDifferingFromGoListStillMaps(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("letter case folds only on macOS")
	}
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	if _, err := os.Stat(filepath.Join(repo, "Internal", "Core", "a.go")); err != nil {
		t.Skip("this volume is case-sensitive")
	}
	pkgs, graph, why := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"Internal/Core/a.go"})
	if !graph || why != "" || !affHas(pkgs, "example.com/rel", "cmd/x") {
		t.Fatalf("Internal/Core is internal/core on macOS and should map: graph=%v why=%q %v", graph, why, pkgs)
	}
}

// C22
func TestAffected_C22_GoListPackageErrorMeansNoGraph(t *testing.T) {
	t.Run("two package names in one folder", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
			"internal/other/bad.go": "package wrong\n",
		}))
		_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
		if graph {
			t.Fatal("go list reports a broken package, yet the change counts as mapped")
		}
	})
	t.Run("missing import", func(t *testing.T) {
		affGoEnv(t)
		repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
			"internal/core/b.go": "package core\n\nimport _ \"example.com/rel/internal/nope\"\n",
		}))
		_, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"})
		if graph {
			t.Fatal("the changed package's imports can't be loaded, yet the change counts as mapped")
		}
	})
}

// C23
func TestAffected_C23_NonGoFileOutsidePackagesIsNotMarked(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affGoFiles)
	c := affGoCmd(t, "go test ./...")
	if _, graph, _ := GoAffectedPkgs(repo, c, []string{"hooks/hooks.json", "internal/core/a.go"}); graph {
		t.Fatal("hooks/hooks.json isn't in the Go graph, yet the change counts as mapped")
	}
	if _, graph, why := GoAffectedPkgs(repo, c, []string{"README.md", "internal/core/a.go"}); !graph || why != "" {
		t.Fatalf("markdown is skipped, so the rest still maps: graph=%v why=%q", graph, why)
	}
	out, _ := cliIn(t, repo, nil, "run", "related", "--files", "hooks/hooks.json,internal/core/a.go")
	if r := lastRun(repo); r == nil || r.Graph != "" {
		t.Fatalf("the run with hooks/hooks.json in it must not be marked: %+v\n%s", r, out)
	}
}

// --- JS graph (JSAffectedFiles) ---

var affJSFiles = map[string]string{
	"package.json":        "{\"name\":\"x\"}\n",
	"package-lock.json":   "{}\n",
	"pnpm-lock.yaml":      "x\n",
	"yarn.lock":           "x\n",
	"tsconfig.json":       "{}\n",
	"jest.config.js":      "module.exports = {}\n",
	"jest.config.ts":      "export default {}\n",
	"vitest.config.ts":    "export default {}\n",
	"vite.config.ts":      "export default {}\n",
	"babel.config.js":     "module.exports = {}\n",
	".babelrc":            "{}\n",
	"vitest.workspace.ts": "export default []\n",
	"jest.setup.ts":       "\n",
	"vitest.setup.ts":     "\n",
	"src/setupTests.ts":   "\n",
	"src/__mocks__/fs.ts": "export {}\n",
	"src/a.ts":            "export const a = 1\n",
	"src/a.test.ts":       "test('a', () => {})\n",
	"src/b.tsx":           "export const B = () => null\n",
	"src/c.js":            "export const c = 1\n",
	"src/d.jsx":           "export const D = () => null\n",
	"src/data.json":       "{}\n",
	"src/a.css":           "a {}\n",
	"src/gone-tracked.ts": "export const g = 1\n",
	"README.md":           "# r\n",
	"docs/notes.md":       "notes\n",
}

// C7
func TestAffected_C7_JSSourceFilesMapThroughTheGraph(t *testing.T) {
	repo := relatedProject(t, "npx jest", affJSFiles)
	if why := JSAffectedFiles(repo, []string{"src/a.ts", "src/b.tsx", "src/c.js", "src/d.jsx", "src/a.test.ts"}); why != "" {
		t.Fatalf("JS/TS source files map through the graph: %q", why)
	}
	if why := JSAffectedFiles(repo, []string{"src/a.ts", "README.md", "docs/notes.md"}); why != "" {
		t.Fatalf("markdown is skipped: %q", why)
	}
}

// C7 through the CLI: jest and vitest related runs are marked.
func TestAffected_C7_JestAndVitestRunsAreMarked(t *testing.T) {
	for _, c := range []struct{ test, out, graph string }{
		{"npx jest", "Tests:       1 passed, 1 total", "jest"},
		{"npx vitest run", " Tests  1 passed (1)", "vitest"},
	} {
		t.Run(c.graph, func(t *testing.T) {
			bin := fakeTool(t, "npx", "#!/bin/sh\necho '"+c.out+"'\n")
			repo := relatedProject(t, c.test, affJSFiles)
			out, _ := cliIn(t, repo, []string{"PATH=" + bin + ":" + os.Getenv("PATH")}, "run", "related", "--files", "src/a.ts,src/b.tsx")
			r := lastRun(repo)
			if r == nil || r.Kind != KindRelated {
				t.Fatalf("no related run: %+v\n%s", r, out)
			}
			if r.Graph != c.graph {
				t.Fatalf("a derived %s related run over source files should be marked %q, got %q", c.test, c.graph, r.Graph)
			}
		})
	}
}

// C8
func TestAffected_C8_JSFilesOutsideTheGraphMeanNoGraph(t *testing.T) {
	repo := relatedProject(t, "npx jest", affJSFiles)
	must(t, os.Remove(filepath.Join(repo, "src", "gone-tracked.ts")))
	for _, f := range []string{
		"src/never-there.ts", "src/gone-tracked.ts",
		"src/data.json", "src/a.css", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "tsconfig.json",
		"jest.config.js", "jest.config.ts", "vitest.config.ts", "vite.config.ts", "babel.config.js", ".babelrc", "vitest.workspace.ts",
		"jest.setup.ts", "vitest.setup.ts", "src/setupTests.ts", "src/__mocks__/fs.ts",
	} {
		if why := JSAffectedFiles(repo, []string{"src/a.ts", f}); why == "" {
			t.Errorf("%s can't be followed through the import graph, yet the change counts as mapped", f)
		}
	}
	// Control: the same good file alone maps.
	if why := JSAffectedFiles(repo, []string{"src/a.ts"}); why != "" {
		t.Fatalf("control: src/a.ts alone should map: %q", why)
	}
}

// C9
func TestAffected_C9_LinkedJSFileMeansNoGraph(t *testing.T) {
	repo := relatedProject(t, "npx jest", affJSFiles)
	must(t, os.Symlink("a.ts", filepath.Join(repo, "src", "link.ts")))
	outside := filepath.Join(t.TempDir(), "out.ts")
	write(t, outside, "export const o = 1\n")
	must(t, os.Symlink(outside, filepath.Join(repo, "src", "out.ts")))
	must(t, os.Link(filepath.Join(repo, "src", "c.js"), filepath.Join(repo, "src", "hard.js")))
	for _, f := range []string{"src/link.ts", "src/out.ts", "src/hard.js", "src/c.js"} {
		if why := JSAffectedFiles(repo, []string{f}); why == "" {
			t.Errorf("%s is a link (symlink or a second name), yet it counts as mapped", f)
		}
	}
}

// --- which setups have a graph (AffectedGraph) ---

// C10
func TestAffected_C10_OnlyDerivedGoJestVitestHaveAGraph(t *testing.T) {
	for _, c := range []struct{ test, related, want string }{
		{"go test ./...", "", "go list"},
		{"go test -count=1 -race ./...", "", "go list"},
		{"npx jest", "", "jest"},
		{"jest", "", "jest"},
		{"npx vitest run", "", "vitest"},
		{"pytest", "", ""},
		{"python -m pytest", "", ""},
		{"./test.sh", "", ""},
		{"npm test", "", ""},
		{"make test", "", ""},
		{"go test ./... | tee out.log", "", ""},
		{"go test ./...", "go test {files}", ""},
		{"npx jest", "npx jest --ci --findRelatedTests {files}", ""},
		{"npm test", "npm test -- --findRelatedTests {files}", ""},
		{"npx vitest run", "./related.sh {files}", ""},
	} {
		s := &Setup{AffectedFinal: true}
		s.Commands.Test, s.Commands.TestRelated = c.test, c.related
		if got := AffectedGraph(s); got != c.want {
			t.Errorf("test %q related %q: graph %q, want %q", c.test, c.related, got, c.want)
		}
	}
}

// C10 through the CLI: a pytest related run is never marked.
func TestAffected_C10_PytestRunIsNotMarked(t *testing.T) {
	bin := fakeTool(t, "pytest", "#!/bin/sh\necho '== 1 passed in 0.1s =='\n")
	repo := relatedProject(t, "pytest", map[string]string{"src/foo.py": "x = 1\n", "tests/test_foo.py": "def test_foo():\n    pass\n"})
	out, _ := cliIn(t, repo, []string{"PATH=" + bin + ":" + os.Getenv("PATH")}, "run", "related", "--files", "src/foo.py")
	r := lastRun(repo)
	if r == nil || r.Kind != KindRelated || r.Graph != "" {
		t.Fatalf("pytest's name match is not an import graph: %+v\n%s", r, out)
	}
}

// --- run checks --final ---

// C11
func TestAffected_C11_RunChecksFinalRunsAffectedAndSaysSo(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	n := len(LoadRuns(h.repo))
	out, code := h.affCLI(nil, "run", "checks", "--final")
	if code != 0 {
		t.Fatalf("run checks --final: %d\n%s", code, out)
	}
	got := affNewRuns(h.repo, n)
	for _, r := range got {
		if r.Kind == KindTest {
			t.Fatalf("with a graph, --final runs the affected tests, not the full suite: %s\n%s", affKinds(got), out)
		}
	}
	if len(got) == 0 || got[len(got)-1].Kind != KindRelated || got[len(got)-1].Graph != "go list" || !strings.Contains(strings.Join(got[len(got)-1].Files, ","), "src/a.go") {
		t.Fatalf("the affected run should be a graph-marked related run over the task's files: %s\n%s", affKinds(got), out)
	}
	if !strings.Contains(out, "affected") || !strings.Contains(out, "full suite not run") {
		t.Fatalf("the summary should say the affected tests ran and the full suite did not:\n%s", out)
	}
	if _, m := h.affProof(); strings.Contains(m, "full test suite") {
		t.Fatalf("the affected --final run is the final proof: %s", m)
	}
}

// C11: no graph (hooks/hooks.json changed too): the full suite runs, and the
// output doesn't claim it didn't.
func TestAffected_C11_RunChecksFinalWithoutGraphRunsFullSuite(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	write(t, filepath.Join(h.repo, "hooks", "hooks.json"), "{}\n")
	n := len(LoadRuns(h.repo))
	out, code := h.affCLI(nil, "run", "checks", "--final")
	if code != 0 {
		t.Fatalf("run checks --final: %d\n%s", code, out)
	}
	got := affNewRuns(h.repo, n)
	full := false
	for _, r := range got {
		full = full || (r.Kind == KindTest && r.Command == "go test -count=1 ./...")
	}
	if !full {
		t.Fatalf("with no graph, --final runs the full suite: %s\n%s", affKinds(got), out)
	}
	if strings.Contains(out, "full suite not run") {
		t.Fatalf("the full suite ran, yet the output says it didn't:\n%s", out)
	}
}

// C24
func TestAffected_C24_RunChecksFinalGitErrorRunsFullSuite(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	tk := h.task()
	tk.StartCommit = "0123456789abcdef0123456789abcdef01234567"
	must(t, SaveTask(tk))
	n := len(LoadRuns(h.repo))
	out, _ := h.affCLI(nil, "run", "checks", "--final")
	got := affNewRuns(h.repo, n)
	full := false
	for _, r := range got {
		full = full || (r.Kind == KindTest && r.Command == "go test -count=1 ./...")
	}
	if !full {
		t.Fatalf("git can't list the task's changes, so --final runs the full suite: %s\n%s", affKinds(got), out)
	}
	if strings.Contains(out, "full suite not run") {
		t.Fatalf("the full suite ran, yet the output says it didn't:\n%s", out)
	}
}

// C21
func TestAffected_C21_FinalRunWithNoTestsFallsBackToFullSuite(t *testing.T) {
	h := affectedHarness(t, true)
	write(t, filepath.Join(h.repo, "lonely", "l.go"), "package lonely\n\nfunc L() int { return 2 }\n")
	n := len(LoadRuns(h.repo))
	out, _ := h.affCLI(nil, "run", "related", "--files", "lonely/l.go", "--final")
	got := affNewRuns(h.repo, n)
	if len(got) == 0 || got[len(got)-1].Kind != KindTest || got[len(got)-1].Command != "go test -count=1 ./..." {
		t.Fatalf("lonely has no tests and no importers, so no test ran: --final must run the full suite: %s\n%s", affKinds(got), out)
	}
}

// --- the final proof (AffectedProof, ProofMissing, the commit gate) ---

// C12
func TestAffected_C12_FinalProofAcceptsGraphRunAndCommitSaysSo(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	r := h.affRelatedFinal("src/a.go")
	if r.Kind != KindRelated || r.Graph != "go list" || r.Failed != 0 || r.Ran <= 0 || !strings.Contains(r.Command, "cmd/y") {
		t.Fatalf("the affected run should be a passing go list run that includes the importer cmd/y: %+v", r)
	}
	p, m := h.affProof()
	if p == nil || p.ID != r.ID {
		t.Fatalf("AffectedProof should return the affected run %s: %+v", r.ID, p)
	}
	if strings.Contains(m, "full test suite") {
		t.Fatalf("the affected run should stand in for the full suite: %s", m)
	}
	o := h.bash(`git commit -am "fix: A returns 2"`)
	h.expect(o, "ask", "affected tests passed (go list)")
	if _, reason := decision(o); !strings.Contains(reason, "full suite didn't run") {
		t.Fatalf("the commit text should say the full suite didn't run: %s", reason)
	}
}

// C13
func TestAffected_C13_FinalProofRefusals(t *testing.T) {
	t.Run("a changed file the run didn't get", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		h.affEdit("src/b.go", "package src\n\n// B is two.\nfunc B() int { return 2 }\n")
		h.affRelatedFinal("src/a.go")
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("src/b.go changed but the affected run didn't get it: %+v %s", p, m)
		}
	})
	t.Run("a file committed in a slice", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affEdit("src/b.go", "package src\n\n// B is two.\nfunc B() int { return 2 }\n")
		run(t, h.repo, "git", "commit", "-q", "-am", "feat: note B")
		h.affFix()
		h.affRelatedFinal("src/a.go")
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("src/b.go changed since the task start (in a slice commit) but the run didn't get it: %+v %s", p, m)
		}
		// Control: with both files the same proof holds.
		h.affRelatedFinal("src/a.go,src/b.go")
		if p, m := h.affProof(); p == nil || strings.Contains(m, "full test suite") {
			t.Fatalf("control: a run over every changed file should count: %+v %s", p, m)
		}
	})
	t.Run("unmarked or another graph", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		r := h.affRelatedFinal("src/a.go")
		s, fp := h.affSetupFP()
		if AffectedProof(h.task(), s, []*Run{r}, fp) == nil {
			t.Fatalf("control: the real affected run alone should count: %+v", r)
		}
		// Hand-made rows: copies of the real run with only Graph changed.
		unmarked, jest := *r, *r
		unmarked.Graph, jest.Graph = "", "jest"
		if p := AffectedProof(h.task(), s, []*Run{&unmarked}, fp); p != nil {
			t.Fatal("a related run without a graph counted as the final proof")
		}
		if p := AffectedProof(h.task(), s, []*Run{&jest}, fp); p != nil {
			t.Fatal("a jest-marked run counted for a go test setup")
		}
	})
	t.Run("an unmarked related run from ExecuteRelated", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		s, _ := h.affSetupFP()
		r, err := ExecuteRelated(h.repo, h.task(), "go test -count=1 ./src/ ./cmd/y", []string{"src/a.go"}, s, io.Discard)
		must(t, err)
		if r.Failed != 0 {
			t.Fatalf("premise: the run should pass: %+v", r)
		}
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("a related run nobody mapped through the graph counted: %+v %s", p, m)
		}
	})
	t.Run("a failing run", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affEdit("src/a_test.go", "package src\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(B())\n\t}\n}\n\nfunc TestA(t *testing.T) {\n\tif A() != 2 {\n\t\tt.Fatal(A())\n\t}\n}\n")
		h.affEdit("src/a.go", "package src\n\nfunc A() int { return 3 }\n")
		r := h.affRelatedFinal("src/a.go,src/a_test.go")
		if r.Failed == 0 {
			t.Fatalf("premise: TestA should fail: %+v", r)
		}
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("a failing affected run counted: %+v %s", p, m)
		}
	})
	t.Run("a run on older code", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		h.affRelatedFinal("src/a.go")
		h.affEdit("src/a.go", "package src\n\n// A is two.\nfunc A() int { return 2 }\n")
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("the code changed after the affected run, yet it counted: %+v %s", p, m)
		}
	})
}

// C14
func TestAffected_C14_FinalProofRefusesWhenGitCantListChanges(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	h.affRelatedFinal("src/a.go")
	if p, _ := h.affProof(); p == nil {
		t.Fatal("control: the affected run should count before git breaks")
	}
	tk := h.task()
	tk.StartCommit = "0123456789abcdef0123456789abcdef01234567"
	must(t, SaveTask(tk))
	if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
		t.Fatalf("git can't diff from the start commit, so the task's files are unknown: %+v %s", p, m)
	}
}

// C16
func TestAffected_C16_FullSuiteAndSliceProofUnchanged(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	out, _ := h.affCLI(nil, "run", "related", "--files", "src/a.go")
	s, _ := h.affSetupFP()
	if m := strings.Join(ProofMissing(h.task(), s, false), "; "); strings.Contains(m, "related tests") {
		t.Fatalf("a passing related run still proves a slice commit: %s\n%s", m, out)
	}
	if _, err := Execute(h.repo, h.task(), KindTest, "", "go test -count=1 ./...", s, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, m := h.affProof(); strings.Contains(m, "full test suite") {
		t.Fatalf("a passing full suite is still the final proof: %s", m)
	}
	o := h.bash(`git commit -am "fix: A returns 2"`)
	h.expect(o, "ask", "full suite passed")
	if _, reason := decision(o); strings.Contains(reason, "didn't run") {
		t.Fatalf("the full suite ran; the commit text says otherwise: %s", reason)
	}
}

// C17
func TestAffected_C17_BeforeFixRunUsesDirectPackagesOnly(t *testing.T) {
	h := affectedHarness(t, true)
	h.affEdit("src/a_test.go", "package src\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n\tif B() != 2 {\n\t\tt.Fatal(B())\n\t}\n}\n\nfunc TestA(t *testing.T) {\n\tif A() != 2 {\n\t\tt.Fatal(A())\n\t}\n}\n")
	h.affFix()
	s, _ := h.affSetupFP()
	r, err := RunBeforeFix(h.repo, h.task(), []string{"src/a_test.go"}, s, io.Discard)
	if err != nil {
		t.Fatalf("before-fix run refused: %v", err)
	}
	if !strings.Contains(r.Command, "src") || strings.Contains(r.Command, "cmd/y") || strings.Contains(r.Command, "./...") {
		t.Fatalf("a before-fix run is the test's own package, not its importers: %q", r.Command)
	}
}

// C18
func TestAffected_C18_OffByDefaultNeedsTheFullSuite(t *testing.T) {
	var zero Setup
	if zero.AffectedFinal {
		t.Fatal("affected_final must be off unless saved")
	}
	h := affectedHarness(t, false)
	h.affFix()
	r := h.affRelatedFinal("src/a.go")
	if r.Kind == KindRelated && r.Failed == 0 {
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("affected_final is off: the final proof needs the full suite: %+v %s", p, m)
		}
	}
	n := len(LoadRuns(h.repo))
	out, _ := h.affCLI(nil, "run", "checks", "--final")
	got := affNewRuns(h.repo, n)
	full := false
	for _, x := range got {
		full = full || (x.Kind == KindTest && x.Command == "go test -count=1 ./...")
	}
	if !full || strings.Contains(out, "full suite not run") {
		t.Fatalf("affected_final is off: run checks --final runs the full suite: %s\n%s", affKinds(got), out)
	}
}

// C19
func TestAffected_C19_FailedFullSuiteRefusesDespiteAffectedPass(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	h.affRelatedFinal("src/a.go")
	if p, _ := h.affProof(); p == nil {
		t.Fatal("control: the affected run should count before the full suite fails")
	}
	s, _ := h.affSetupFP()
	t.Setenv("AFFECTED_QA_FAIL", "1")
	full, err := Execute(h.repo, h.task(), KindTest, "", "go test -count=1 ./...", s, io.Discard)
	must(t, err)
	if full.Failed == 0 {
		t.Fatalf("premise: the full suite should fail: %+v", full)
	}
	if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
		t.Fatalf("a failing full suite on this code must refuse the final proof: %+v %s", p, m)
	}
}

// C20
func TestAffected_C20_LaterFailedRunRefusesLaterPassingCaseRunDoesnt(t *testing.T) {
	t.Run("a related run failed after it", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		h.affRelatedFinal("src/a.go")
		s, _ := h.affSetupFP()
		t.Setenv("AFFECTED_QA_FAIL", "1")
		r, err := ExecuteRelated(h.repo, h.task(), "go test -count=1 ./cmd/y", []string{"cmd/y/y_test.go"}, s, io.Discard)
		must(t, err)
		if r.Failed == 0 {
			t.Fatalf("premise: the later related run should fail: %+v", r)
		}
		if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
			t.Fatalf("a related run that failed after the affected one must refuse: %+v %s", p, m)
		}
	})
	t.Run("a passing per-case run after it", func(t *testing.T) {
		h := affectedHarness(t, true)
		h.affFix()
		aff := h.affRelatedFinal("src/a.go")
		s, _ := h.affSetupFP()
		r, err := ExecuteRelated(h.repo, h.task(), "go test -count=1 ./src/", []string{"src/a_test.go"}, s, io.Discard)
		must(t, err)
		if r.Failed != 0 {
			t.Fatalf("premise: the case run should pass: %+v", r)
		}
		p, m := h.affProof()
		if p == nil || p.ID != aff.ID || strings.Contains(m, "full test suite") {
			t.Fatalf("a later passing per-case run must not displace the affected proof: %+v %s", p, m)
		}
	})
}

// --- the setup question ---

// C25
func TestAffected_C25_SetupSkillAsksAfterSearchingTheTests(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "skills", "setup", "SKILL.md"))
	must(t, err)
	skill := string(b)
	for _, want := range []string{"affected_final", "go build", "../"} {
		if !strings.Contains(skill, want) {
			t.Errorf("the setup skill should search the tests (go build, built binaries, ../ reads) and save affected_final: %q missing", want)
		}
	}
}

// --- review round 1 ---

func TestAffected_R1_HotfixNeedsTheFullSuite(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	h.affRelatedFinal("src/a.go")
	if p, _ := h.affProof(); p == nil {
		t.Fatal("control: the affected run counts for a normal task")
	}
	tk := h.task()
	tk.Kind = KindHotfix
	must(t, SaveTask(tk))
	if p, m := h.affProof(); p != nil || !strings.Contains(m, "full test suite") {
		t.Fatalf("a hotfix's full suite (W1) can't be replaced by affected tests: %+v %s", p, m)
	}
}

func TestAffected_R1_SymlinkedGoFileInAnotherPackageMeansNoGraph(t *testing.T) {
	affGoEnv(t)
	repo := relatedProject(t, "go test ./...", affFiles(map[string]string{
		"internal/twin/twin.go":      "package core\n\nfunc T() int { return A() }\n",
		"internal/twin/twin_test.go": "package core\n\nimport \"testing\"\n\nfunc TestT(t *testing.T) { _ = T() }\n",
	}))
	must(t, os.Symlink("../core/a.go", filepath.Join(repo, "internal", "twin", "a_link.go")))
	if _, graph, _ := GoAffectedPkgs(repo, affGoCmd(t, "go test ./..."), []string{"internal/core/a.go"}); graph {
		t.Fatal("internal/twin compiles internal/core/a.go through a symlink go list doesn't show, yet the change counts as mapped")
	}
}

func TestAffected_R1_UnnamedFailuresNeverStandIn(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	r := h.affRelatedFinal("src/a.go")
	s, fp := h.affSetupFP()
	base := *r
	base.ID, base.Kind, base.Command, base.Baseline, base.Graph, base.PrintBefore, base.PrintAfter = "base-full", KindTest, s.Commands.Test, true, "", "before", "before"
	base.Ran, base.Failed, base.FailedIDs = 10, 3, nil
	bad := *r
	bad.Failed, bad.FailedIDs = 2, nil
	if v := Judge(&bad, &base); !v.OK {
		t.Fatalf("premise: by counts alone, 2 unnamed failures pass against a baseline with 3: %s", v.Reason)
	}
	if p := AffectedProof(h.task(), s, []*Run{&base, &bad}, fp); p != nil {
		t.Fatal("a failure the output didn't name can't be told apart from the baseline's, yet the affected run counted")
	}
	named := *r
	named.Failed, named.FailedIDs = 2, []string{"TestNew1", "TestNew2"}
	if v := Judge(&named, &base); !v.OK {
		t.Fatalf("premise: against an unnamed baseline, 2 named failures pass by count: %s", v.Reason)
	}
	if p := AffectedProof(h.task(), s, []*Run{&base, &named}, fp); p != nil {
		t.Fatal("the baseline's failures have no names, so named new failures were compared by count, yet the affected run counted")
	}
}

func TestAffected_R1_DeclarationFileMeansNoGraph(t *testing.T) {
	files := map[string]string{"src/types.d.ts": "declare const g: number\n", "src/a.ts": "export const a = 1\n"}
	repo := relatedProject(t, "npx jest", files)
	if why := JSAffectedFiles(repo, []string{"src/a.ts", "src/types.d.ts"}); why == "" {
		t.Fatal("an ambient .d.ts is never imported, yet it counts as mapped")
	}
}

func TestAffected_R1_PreexistingFileTheTaskEditedIsCovered(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	tk := h.task()
	tk.Preexisting = map[string]string{"src/a.go": "x"}
	must(t, SaveTask(tk))
	files, err := TaskChangedFiles(h.task())
	must(t, err)
	if strings.Join(files, ",") != "src/a.go" {
		t.Fatalf("a file that was dirty at the start and changed since is the task's to test: %v", files)
	}
}

func TestAffected_R1_JSRunWithNoTestsFallsBackToFullSuite(t *testing.T) {
	bin := fakeTool(t, "npx", "#!/bin/sh\ncase \"$*\" in *findRelatedTests*) echo 'No tests found, exiting with code 1'; exit 1;; esac\necho 'Tests:       1 passed, 1 total'\n")
	repo := relatedProject(t, "npx jest", affJSFiles)
	s, _, err := LoadSetup(repo)
	must(t, err)
	s.AffectedFinal = true
	_, err = SaveSetup(repo, s)
	must(t, err)
	out, _ := cliIn(t, repo, []string{"PATH=" + bin + ":" + os.Getenv("PATH")}, "run", "related", "--files", "src/a.ts", "--final")
	if r := lastRun(repo); r == nil || r.Kind != KindTest || r.Command != "npx jest" {
		t.Fatalf("jest found no related test, so --final runs the full suite: %+v\n%s", r, out)
	}
}

func TestAffected_R1_RescuedAffectedRunNeedsTheReportLine(t *testing.T) {
	h := affectedHarness(t, true)
	h.affFix()
	r := h.affRelatedFinal("src/a.go")
	rescued := *r
	rescued.Rescued = true
	must(t, appendRun(h.repo, &rescued))
	h.docs(DocReport, "# Report\n## For you\n- nothing\n")
	s, _ := h.affSetupFP()
	if m := strings.Join(FinishMissing(h.task(), s), "; "); !strings.Contains(m, "only in parallel") {
		t.Fatalf("the affected proof passed only after tests were rerun alone; the report must say so: %s", m)
	}
}
