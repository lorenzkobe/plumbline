package core

// Blind QA for release 0.8.12 (affected tests for nx, turbo and cargo, and
// the task skill's no-suite-before-approval rule), written from the case
// table in brief-qa-1.md (C1..C32; C23, the live run, is the lead's), not
// from the new code. Names used as the lead gave them: ParseMonorepoTest,
// MonorepoCmd, MonorepoAffected, IsDerivedMonorepo, FillRelated, Setup.Root.
//
// Tool output comes from the captures in testdata/monorepo (nx 22.7.12,
// turbo 2.11.13, cargo 1.100.0; workspace paths sanitized to /work). Fake
// nx, turbo, cargo, npx, pnpm and yarn scripts on PATH print them and log
// every call.

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// --- helpers ---

func mrFixture(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("testdata", "monorepo", name))
	must(t, err)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return p
}

func mrText(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(mrFixture(t, name))
	must(t, err)
	return string(b)
}

// mrTemp writes text to a new file and returns its path.
func mrTemp(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	write(t, p, text)
	return p
}

// mrNxPass is hand-made: nx running one project's vitest, shaped after the
// captured nx-fail.txt with the ANSI colours left out. No passing nx run was
// captured.
const mrNxPass = "\n NX   Running target test for 1 project:\n\n- @acme/ui\n\n> nx run @acme/ui:test\n\n> @acme/ui@1.0.0 test\n> vitest run\n\n RUN  v3.2.7 /work/nx-ws/packages/ui\n\n ✓ ui.test.js (2 tests) 1ms\n\n Test Files  1 passed (1)\n      Tests  2 passed (2)\n   Start at  17:49:39\n   Duration  507ms\n\n NX   Successfully ran target test for 1 project\n"

// mrFakeTools puts fake nx, turbo and cargo first on PATH. Each logs its
// argv (tab-separated, tool name first) to $MR_LOG. The listing call (nx
// graph, turbo ls, cargo metadata) prints $MR_LIST and exits $MR_LIST_EXIT;
// any other call prints $MR_OUT and exits $MR_OUT_EXIT. nx also logs its
// NX_DAEMON for the listing. npx, yarn and pnpm [exec] log a "via" line and
// hand the rest to the fake tool. Returns the log path.
func mrFakeTools(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	logArgs := `printf '%s' "$(basename "$0")" >> "$MR_LOG"; for a in "$@"; do printf '\t%s' "$a" >> "$MR_LOG"; done; printf '\n' >> "$MR_LOG"`
	tool := func(name, listing, extra string) {
		script := "#!/bin/sh\n" + logArgs + "\n" +
			"if [ \"$1\" = " + listing + " ]; then\n" + extra +
			"  cat \"$MR_LIST\" || exit 3\n  exit ${MR_LIST_EXIT:-0}\nfi\n" +
			"cat \"$MR_OUT\"\nexit ${MR_OUT_EXIT:-0}\n"
		p := filepath.Join(bin, name)
		write(t, p, script)
		must(t, os.Chmod(p, 0o755))
	}
	tool("nx", "graph", "  printf 'nxenv\\t%s\\n' \"${NX_DAEMON-unset}\" >> \"$MR_LOG\"\n")
	tool("turbo", "ls", "")
	tool("cargo", "metadata", "")
	runner := func(name, skip string) {
		script := "#!/bin/sh\nprintf 'via\\t%s' \"$(basename \"$0\")\" >> \"$MR_LOG\"; for a in \"$@\"; do printf '\\t%s' \"$a\" >> \"$MR_LOG\"; done; printf '\\n' >> \"$MR_LOG\"\n" +
			skip + "t=\"$1\"; shift\nexec \"$(dirname \"$0\")/$t\" \"$@\"\n"
		p := filepath.Join(bin, name)
		write(t, p, script)
		must(t, os.Chmod(p, 0o755))
	}
	runner("npx", "")
	runner("yarn", "")
	runner("pnpm", "if [ \"$1\" = exec ]; then shift; fi\n")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MR_LOG", log)
	t.Setenv("MR_LIST_EXIT", "0")
	t.Setenv("MR_OUT_EXIT", "0")
	return log
}

// mrCalls: the logged calls, each as its words.
func mrCalls(t *testing.T, log string) [][]string {
	t.Helper()
	b, err := os.ReadFile(log)
	if os.IsNotExist(err) {
		return nil
	}
	must(t, err)
	var out [][]string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if line != "" {
			out = append(out, strings.Split(line, "\t"))
		}
	}
	return out
}

// mrListings counts the listing calls the fake tools got.
func mrListings(t *testing.T, log string) int {
	t.Helper()
	n := 0
	for _, c := range mrCalls(t, log) {
		if len(c) > 1 && (c[0] == "nx" && c[1] == "graph" || c[0] == "turbo" && c[1] == "ls" || c[0] == "cargo" && c[1] == "metadata") {
			n++
		}
	}
	return n
}

// mrRuns: the fake tool calls that weren't listings (the test runs).
func mrRuns(t *testing.T, log string) [][]string {
	t.Helper()
	var out [][]string
	for _, c := range mrCalls(t, log) {
		if len(c) < 2 || c[0] == "via" || c[0] == "nxenv" || c[1] == "graph" || c[1] == "ls" || c[1] == "metadata" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// mrWords splits a command the way sh does, without running it, in an
// empty folder (so a quoting bug that runs a command touches nothing).
func mrWords(t *testing.T, dir, command string) []string {
	t.Helper()
	c := exec.Command("sh", "-c", "set -- "+command+"\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done")
	c.Dir = dir
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("sh couldn't split %q: %v %s", command, err, b)
	}
	s := strings.TrimSuffix(string(b), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func mrJoin(w []string) string { return strings.Join(w, " | ") }

func mrSame(got, want []string) bool { return strings.Join(got, "\x00") == strings.Join(want, "\x00") }

var mrJSFiles = map[string]string{
	"README.md":                  "# ws\n",
	"docs/notes.md":              "# notes\n",
	"scripts/x.sh":               "#!/bin/sh\necho x\n",
	"package-lock.json":          "{}\n",
	"packages/ui/package.json":   `{"name":"@acme/ui","version":"1.0.0","main":"index.js","scripts":{"test":"vitest run"}}` + "\n",
	"packages/ui/index.js":       "export const add = (a, b) => a + b\n",
	"packages/ui/old.js":         "export const old = 1\n",
	"packages/ui/ui.test.js":     "import {test,expect} from \"vitest\";import {add} from \"./index.js\";test(\"adds\",()=>expect(add(1,2)).toBe(3));\n",
	"packages/web/package.json":  `{"name":"web","version":"1.0.0","dependencies":{"@acme/ui":"*"},"scripts":{"test":"vitest run"}}` + "\n",
	"packages/web/index.js":      "import {add} from \"@acme/ui\";export const two = add(1, 1)\n",
	"packages/web/old.js":        "export const old = 1\n",
	"packages/web/web.test.js":   "import {test,expect} from \"vitest\";import {add} from \"@acme/ui\";test(\"web uses ui\",()=>expect(add(1,1)).toBe(2));\n",
	"packages/solo/package.json": `{"name":"solo","version":"1.0.0","scripts":{"test":"vitest run"}}` + "\n",
	"packages/solo/index.js":     "export const s = 1\n",
}

var mrCargoFiles = map[string]string{
	"README.md":                     "# ws\n",
	"docs/notes.md":                 "# notes\n",
	"scripts/x.sh":                  "#!/bin/sh\necho x\n",
	"Cargo.toml":                    "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n",
	"Cargo.lock":                    "version = 4\n",
	"rust-toolchain.toml":           "[toolchain]\nchannel = \"stable\"\n",
	".cargo/config.toml":            "[build]\n",
	"crates/app/Cargo.toml":         "[package]\nname = \"app\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dependencies]\ncore_lib = { path = \"../core_lib\" }\n",
	"crates/app/src/lib.rs":         "pub fn two() -> i32 { core_lib::add(1, 1) }\n",
	"crates/core_lib/Cargo.toml":    "[package]\nname = \"core_lib\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
	"crates/core_lib/src/lib.rs":    "pub fn add(a: i32, b: i32) -> i32 { a + b }\n",
	"crates/core_lib/README.md":     "# core_lib\n",
	"crates/devuser/Cargo.toml":     "[package]\nname = \"devuser\"\nversion = \"0.1.0\"\nedition = \"2024\"\n\n[dev-dependencies]\ncore_lib = { path = \"../core_lib\" }\n",
	"crates/devuser/src/lib.rs":     "pub fn d() -> i32 { 1 }\n",
	"crates/devuser/tests/it.rs":    "#[test]\nfn uses() { assert_eq!(core_lib::add(1, 1), 2); }\n",
	"crates/solo/Cargo.toml":        "[package]\nname = \"solo\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
	"crates/solo/src/lib.rs":        "pub fn s() -> i32 { 1 }\n",
	"crates/solo/src/old.rs":        "pub fn old() {}\n",
	"crates/app/inner/Cargo.toml":   "[package]\nname = \"inner\"\nversion = \"0.1.0\"\nedition = \"2024\"\n",
	"crates/app/inner/src/lib.rs":   "pub fn i() -> i32 { 1 }\n",
	"crates/apple/notacrate/x.rs":   "// not in any crate\n",
	"crates/core_lib/src/data.json": "{}\n",
}

func mrToolFiles(tool string) map[string]string {
	files := map[string]string{}
	src := mrJSFiles
	if tool == "cargo" {
		src = mrCargoFiles
	}
	for k, v := range src {
		files[k] = v
	}
	switch tool {
	case "nx":
		files["package.json"] = `{"name":"nx-ws","private":true,"workspaces":["packages/*"],"scripts":{"test":"nx run-many -t test"}}` + "\n"
		files["nx.json"] = "{}\n"
	case "turbo":
		files["package.json"] = `{"name":"turbo-ws","private":true,"workspaces":["packages/*"],"packageManager":"npm@11.0.0","scripts":{"test":"turbo run test"}}` + "\n"
		files["turbo.json"] = `{"tasks":{"test":{}}}` + "\n"
	}
	return files
}

var mrTestCmd = map[string]string{
	"nx":    "npx nx run-many -t test",
	"turbo": "turbo run test",
	"cargo": "cargo test --workspace",
}

// mrCargoMeta: the captured cargo metadata with /work/cargo-ws replaced by
// root, after edit (hand-made rows, named where a test uses one).
func mrCargoMeta(t *testing.T, root string, edit func(m map[string]any)) string {
	t.Helper()
	text := strings.ReplaceAll(mrText(t, "cargo-metadata.json"), "/work/cargo-ws", root)
	if edit != nil {
		var m map[string]any
		must(t, json.Unmarshal([]byte(text), &m))
		edit(m)
		b, err := json.Marshal(m)
		must(t, err)
		text = string(b)
	}
	return mrTemp(t, "metadata.json", text)
}

// mrUseListing points the fakes at the tool's captured listing and a
// passing run's output.
func mrUseListing(t *testing.T, tool, root string) {
	t.Helper()
	switch tool {
	case "nx":
		t.Setenv("MR_LIST", mrFixture(t, "nx-graph.json"))
		t.Setenv("MR_OUT", mrTemp(t, "nx-pass.txt", mrNxPass))
	case "turbo":
		t.Setenv("MR_LIST", mrFixture(t, "turbo-ls.json"))
		t.Setenv("MR_OUT", mrFixture(t, "turbo-prefixed-pass.txt"))
	case "cargo":
		t.Setenv("MR_LIST", mrCargoMeta(t, root, nil))
		t.Setenv("MR_OUT", mrFixture(t, "cargo-pass.txt"))
	}
}

type mrEnv struct {
	repo string
	s    *Setup
	log  string
}

// mrProject: a committed nx, turbo or cargo workspace with its test command
// saved, fakes on PATH and the listing pointed at the capture.
func mrProject(t *testing.T, tool string) *mrEnv {
	t.Helper()
	return mrProjectTest(t, tool, mrTestCmd[tool])
}

func mrProjectTest(t *testing.T, tool, test string) *mrEnv {
	t.Helper()
	log := mrFakeTools(t)
	repo := relatedProject(t, test, mrToolFiles(tool))
	mrUseListing(t, tool, repo)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = test
	s.Root = repo
	return &mrEnv{repo: repo, s: s, log: log}
}

func (e *mrEnv) affected(files ...string) ([]string, bool, string) {
	return MonorepoAffected(e.repo, e.s, files)
}

func mrTemplate(test string) string {
	s := &Setup{}
	s.Commands.Test = test
	return savedCommand(s, KindRelated)
}

// --- derivation and mapping ---

// C1
func TestMonorepo_C1_NxDerivesAffectedAndMapsFiles(t *testing.T) {
	c, ok := ParseMonorepoTest("npx nx run-many -t test")
	if !ok || c.Tool != "nx" || !mrSame(c.Prefix, []string{"npx"}) {
		t.Fatalf("npx nx run-many -t test should parse as nx with runner npx: %+v %v", c, ok)
	}
	if c, ok := ParseMonorepoTest("pnpm exec nx run-many --target=test"); !ok || c.Tool != "nx" || !mrSame(c.Prefix, []string{"pnpm", "exec"}) {
		t.Fatalf("pnpm exec nx run-many --target=test should parse as nx with runner pnpm exec: %+v %v", c, ok)
	}
	for test, want := range map[string]string{
		"npx nx run-many -t test":       "npx nx affected -t test {files}",
		"npx nx run-many -t test --all": "npx nx affected -t test {files}",
		"nx run-many --target=test":     "nx affected --target=test {files}",
	} {
		if got := mrTemplate(test); got != want {
			t.Errorf("test %q: related %q, want %q", test, got, want)
		}
	}
	e := mrProject(t, "nx")
	if !IsDerivedMonorepo(e.s) || AffectedGraph(e.s) != "nx" {
		t.Fatalf("an nx run-many setup is derived with graph nx: derived=%v graph=%q", IsDerivedMonorepo(e.s), AffectedGraph(e.s))
	}
	args, graph, why := e.affected("packages/ui/index.js")
	if why != "" || !graph || !mrSame(args, []string{"--files=packages/ui/index.js"}) {
		t.Fatalf("packages/ui/index.js: args=%v graph=%v why=%q", args, graph, why)
	}
	args, graph, why = e.affected("packages/ui/index.js", "packages/web/index.js")
	if why != "" || !graph || !mrSame(args, []string{"--files=packages/ui/index.js,packages/web/index.js"}) {
		t.Fatalf("two files go in one comma-separated --files: args=%v graph=%v why=%q", args, graph, why)
	}
	via := false
	for _, c := range mrCalls(t, e.log) {
		if strings.Join(c, " ") == "via npx nx graph --file=stdout" {
			via = true
		}
	}
	if !via {
		t.Fatalf("the listing should run through the test command's runner (npx nx graph --file=stdout): %v", mrCalls(t, e.log))
	}
}

// C2
func TestMonorepo_C2_TurboFiltersByPackageName(t *testing.T) {
	if got := mrTemplate("turbo run test"); got != "turbo run test --continue {files}" {
		t.Fatalf("turbo run test: related %q", got)
	}
	if got := mrTemplate("turbo test"); got != "turbo test --continue {files}" {
		t.Fatalf("turbo test: related %q", got)
	}
	e := mrProject(t, "turbo")
	if !IsDerivedMonorepo(e.s) || AffectedGraph(e.s) != "turbo" {
		t.Fatalf("a turbo run setup is derived with graph turbo: derived=%v graph=%q", IsDerivedMonorepo(e.s), AffectedGraph(e.s))
	}
	args, graph, why := e.affected("packages/ui/index.js")
	if why != "" || !graph || !mrSame(args, []string{"--filter=...@acme/ui"}) {
		t.Fatalf("packages/ui is @acme/ui in turbo ls: args=%v graph=%v why=%q", args, graph, why)
	}
	args, graph, why = e.affected("packages/web/index.js", "packages/ui/index.js", "packages/ui/ui.test.js")
	if why != "" || !graph || !mrSame(args, []string{"--filter=...@acme/ui", "--filter=...web"}) {
		t.Fatalf("two packages give two filters, sorted, each once: args=%v graph=%v why=%q", args, graph, why)
	}
}

// C3
func TestMonorepo_C3_CargoPackagesWithDependents(t *testing.T) {
	if c, ok := ParseMonorepoTest("cargo test --workspace"); !ok || c.Tool != "cargo" || len(c.Prefix) != 0 {
		t.Fatalf("cargo test --workspace should parse as cargo with no runner: %+v %v", c, ok)
	}
	for _, test := range []string{"cargo test --workspace", "cargo test --all", "cargo test"} {
		if got := mrTemplate(test); got != "cargo test --no-fail-fast {files}" {
			t.Errorf("test %q: related %q, want cargo test --no-fail-fast {files}", test, got)
		}
	}
	e := mrProject(t, "cargo")
	if !IsDerivedMonorepo(e.s) || AffectedGraph(e.s) != "cargo metadata" {
		t.Fatalf("a cargo test setup is derived with graph 'cargo metadata': derived=%v graph=%q", IsDerivedMonorepo(e.s), AffectedGraph(e.s))
	}
	args, graph, why := e.affected("crates/core_lib/src/lib.rs")
	if why != "" || !graph || !mrSame(args, []string{"--package=app", "--package=core_lib", "--package=devuser"}) {
		t.Fatalf("core_lib, its normal dependent app and its dev dependent devuser; not solo: args=%v graph=%v why=%q", args, graph, why)
	}
	args, _, why = e.affected("crates/app/src/lib.rs")
	if why != "" || !mrSame(args, []string{"--package=app"}) {
		t.Fatalf("nothing depends on app, so only app runs: %v %q", args, why)
	}
	// Hand-made rows: the capture with solo given a build dependency on app,
	// so core_lib reaches solo through app (transitive, build kind).
	t.Setenv("MR_LIST", mrCargoMeta(t, e.repo, func(m map[string]any) {
		for _, p := range m["packages"].([]any) {
			pkg := p.(map[string]any)
			if pkg["name"] == "solo" {
				pkg["dependencies"] = []any{map[string]any{"name": "app", "source": nil, "req": "*", "kind": "build", "rename": nil, "optional": false, "uses_default_features": true, "features": []any{}, "target": nil, "registry": nil, "path": e.repo + "/crates/app"}}
			}
		}
	}))
	args, graph, why = e.affected("crates/core_lib/src/lib.rs")
	if why != "" || !graph || !mrSame(args, []string{"--package=app", "--package=core_lib", "--package=devuser", "--package=solo"}) {
		t.Fatalf("solo build-depends on app, which depends on core_lib: args=%v graph=%v why=%q", args, graph, why)
	}
}

// C4
func TestMonorepo_C4_CargoTailStaysAfterPackages(t *testing.T) {
	for _, test := range []string{"cargo test -- --nocapture", "cargo test --workspace -- --nocapture"} {
		if got := mrTemplate(test); got != "cargo test --no-fail-fast {files} -- --nocapture" {
			t.Errorf("test %q: related %q", test, got)
		}
	}
	got := mrWords(t, t.TempDir(), FillRelated("cargo test --no-fail-fast {files} -- --nocapture", []string{"--package=app", "--package=core_lib"}, true))
	want := []string{"cargo", "test", "--no-fail-fast", "--package=app", "--package=core_lib", "--", "--nocapture"}
	if !mrSame(got, want) {
		t.Fatalf("the --package args go before the -- tail:\n got %s\nwant %s", mrJoin(got), mrJoin(want))
	}
}

// C5
func TestMonorepo_C5_SelectionFlagsAndUnknownWordsAreNotDerived(t *testing.T) {
	for _, test := range []string{
		"npx nx run-many -t test -p web",
		"npx nx run-many -t test --projects=web",
		"npx nx run-many -t test --projects web",
		"npx nx run-many -t test --exclude=solo",
		"npx nx run-many -t test --definitely-unknown",
		"npx nx affected -t test",
		"turbo run test --filter=web",
		"turbo run test -F web",
		"turbo run test --affected",
		"turbo run test --definitely-unknown",
		"turbo run test $(touch x)",
		"cargo test -p app",
		"cargo test --package app",
		"cargo test --package=app",
		"cargo test --workspace --exclude solo",
		"cargo test --manifest-path crates/app/Cargo.toml",
		"cargo test my_test_name",
		"cargo test --workspace my_test_name -- --nocapture",
		"cargo test --definitely-unknown",
		"cargo test && echo done",
		"cargo test | tee out.log",
		"cargo test; rm -rf target",
		"cargo test > out.log",
		"cargo nextest run",
		"cargo nextest run --workspace",
		"FOO=1 cargo test",
	} {
		if c, ok := ParseMonorepoTest(test); ok {
			t.Errorf("%q should not parse as an allowed monorepo test: %+v", test, c)
		}
		s := &Setup{}
		s.Commands.Test = test
		if got := savedCommand(s, KindRelated); got != "" {
			t.Errorf("%q: no related command should be derived (full suite), got %q", test, got)
		}
		if IsDerivedMonorepo(s) || AffectedGraph(s) != "" {
			t.Errorf("%q: no graph, so never the final proof: derived=%v graph=%q", test, IsDerivedMonorepo(s), AffectedGraph(s))
		}
	}
}

// C6
func TestMonorepo_C6_TopLevelAndToolchainFilesRunTheFullSuite(t *testing.T) {
	for tool, c := range map[string]struct {
		unit   string
		shared []string
	}{
		"nx":    {"packages/ui/index.js", []string{"package.json", "package-lock.json", "nx.json", "pnpm-lock.yaml", "tsconfig.base.json"}},
		"turbo": {"packages/ui/index.js", []string{"package.json", "package-lock.json", "turbo.json", "yarn.lock"}},
		"cargo": {"crates/app/src/lib.rs", []string{"Cargo.lock", "Cargo.toml", ".cargo/config.toml", "rust-toolchain", "rust-toolchain.toml"}},
	} {
		t.Run(tool, func(t *testing.T) {
			e := mrProject(t, tool)
			if args, _, why := e.affected(c.unit, "README.md"); why != "" || len(args) == 0 {
				t.Fatalf("control: a unit file plus a top-level README maps: %v %q", args, why)
			}
			for _, f := range c.shared {
				if args, _, why := e.affected(c.unit, f); why == "" {
					t.Errorf("%s reaches every unit, so the full suite should run: got args %v", f, args)
				}
			}
		})
	}
}

// C7
func TestMonorepo_C7_ListingFailuresRunTheFullSuite(t *testing.T) {
	units := map[string]string{"nx": "packages/ui/index.js", "turbo": "packages/ui/index.js", "cargo": "crates/app/src/lib.rs"}
	empty := map[string]string{
		"nx":    `{"graph":{"nodes":{},"dependencies":{}}}`,
		"turbo": `{"packageManager":"npm","packages":{"count":0,"items":[]}}`,
	}
	for _, tool := range []string{"nx", "turbo", "cargo"} {
		t.Run(tool+" exits non-zero", func(t *testing.T) {
			e := mrProject(t, tool)
			t.Setenv("MR_LIST_EXIT", "1")
			if args, _, why := e.affected(units[tool]); why == "" {
				t.Fatalf("the listing exited 1 (its output was valid), yet it mapped: %v", args)
			}
		})
		t.Run(tool+" bad JSON", func(t *testing.T) {
			e := mrProject(t, tool)
			t.Setenv("MR_LIST", mrTemp(t, "bad.json", "warning: something\n{not json"))
			if args, _, why := e.affected(units[tool]); why == "" {
				t.Fatalf("bad JSON mapped: %v", args)
			}
		})
		t.Run(tool+" no units", func(t *testing.T) {
			e := mrProject(t, tool)
			if tool == "cargo" {
				// Hand-made: the capture with every package removed.
				t.Setenv("MR_LIST", mrCargoMeta(t, e.repo, func(m map[string]any) {
					m["packages"], m["workspace_members"], m["workspace_default_members"] = []any{}, []any{}, []any{}
				}))
			} else {
				t.Setenv("MR_LIST", mrTemp(t, "empty.json", empty[tool]))
			}
			if args, _, why := e.affected(units[tool]); why == "" {
				t.Fatalf("no units listed, yet it mapped: %v", args)
			}
		})
		t.Run(tool+" missing", func(t *testing.T) {
			e := mrProject(t, tool)
			bare := t.TempDir() + string(os.PathListSeparator) + "/usr/bin" + string(os.PathListSeparator) + "/bin"
			t.Setenv("PATH", bare)
			for _, name := range []string{tool, "npx"} {
				if _, err := exec.LookPath(name); err == nil {
					t.Skipf("%s is on the system PATH", name)
				}
			}
			if args, _, why := e.affected(units[tool]); why == "" {
				t.Fatalf("the tool isn't installed, yet it mapped: %v", args)
			}
		})
	}
	t.Run("cargo workspace_root elsewhere", func(t *testing.T) {
		e := mrProject(t, "cargo")
		t.Setenv("MR_LIST", mrFixture(t, "cargo-metadata.json")) // workspace_root /work/cargo-ws
		if args, _, why := e.affected("crates/app/src/lib.rs"); why == "" {
			t.Fatalf("cargo's workspace_root isn't this project, yet it mapped: %v", args)
		}
	})
	t.Run("run related falls back to the full suite", func(t *testing.T) {
		e := mrProject(t, "cargo")
		t.Setenv("MR_LIST_EXIT", "1")
		out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", "crates/app/src/lib.rs")
		r := lastRun(e.repo)
		if r == nil || r.Kind != KindTest || r.Command != "cargo test --workspace" {
			t.Fatalf("a failing cargo metadata should run the saved full suite: %+v\n%s", r, out)
		}
	})
}

// C8
func TestMonorepo_C8_FilesOutsideTheProjectRunTheFullSuite(t *testing.T) {
	units := map[string]string{"nx": "packages/ui/index.js", "turbo": "packages/ui/index.js", "cargo": "crates/app/src/lib.rs"}
	for _, tool := range []string{"nx", "turbo", "cargo"} {
		t.Run(tool, func(t *testing.T) {
			e := mrProject(t, tool)
			for _, f := range []string{"/tmp/x.js", "../elsewhere/x.js", "packages/../../x.js", filepath.Join(filepath.Dir(e.repo), "other", "a.rs")} {
				if args, _, why := e.affected(units[tool], f); why == "" {
					t.Errorf("%s is outside the project: got args %v", f, args)
				}
			}
		})
	}
}

// C9
func TestMonorepo_C9_TurboFileOutsidePackagesRunsTheFullSuite(t *testing.T) {
	e := mrProject(t, "turbo")
	for _, files := range [][]string{{"scripts/x.sh"}, {"packages/ui/index.js", "scripts/x.sh"}, {"packages/ui/index.js", ".github/workflows/ci.yml"}} {
		if args, _, why := e.affected(files...); why == "" {
			t.Errorf("%v: turbo's globalDependencies can reach every package, so the full suite: got %v", files, args)
		}
	}
	args, graph, why := e.affected("packages/ui/index.js", "docs/notes.md", "README.md")
	if why != "" || !graph || !mrSame(args, []string{"--filter=...@acme/ui"}) {
		t.Fatalf("control: markdown outside every package is skipped: %v %v %q", args, graph, why)
	}
}

// C10
func TestMonorepo_C10_NonMarkdownOutsideUnitsKeepsRunWithoutGraph(t *testing.T) {
	t.Run("nx", func(t *testing.T) {
		e := mrProject(t, "nx")
		args, graph, why := e.affected("packages/ui/index.js", "scripts/x.sh")
		if why != "" || graph || len(args) != 1 || !strings.HasPrefix(args[0], "--files=") || !strings.Contains(args[0], "packages/ui/index.js") {
			t.Fatalf("scripts/x.sh is in no project: the run goes ahead, graph false: %v %v %q", args, graph, why)
		}
		args, graph, why = e.affected("packages/ui/index.js", "docs/notes.md", "README.md")
		if why != "" || !graph || !mrSame(args, []string{"--files=packages/ui/index.js"}) {
			t.Fatalf("markdown outside every project is skipped: %v %v %q", args, graph, why)
		}
	})
	t.Run("cargo", func(t *testing.T) {
		e := mrProject(t, "cargo")
		args, graph, why := e.affected("crates/app/src/lib.rs", "scripts/x.sh")
		if why != "" || graph || !mrSame(args, []string{"--package=app"}) {
			t.Fatalf("scripts/x.sh is in no crate: the run goes ahead, graph false: %v %v %q", args, graph, why)
		}
		args, graph, why = e.affected("crates/app/src/lib.rs", "docs/notes.md", "README.md")
		if why != "" || !graph || !mrSame(args, []string{"--package=app"}) {
			t.Fatalf("markdown outside every crate is skipped: %v %v %q", args, graph, why)
		}
		args, graph, why = e.affected("crates/core_lib/README.md")
		if why != "" || !graph || !mrSame(args, []string{"--package=app", "--package=core_lib", "--package=devuser"}) {
			t.Fatalf("markdown inside a crate counts for it (include_str!): %v %v %q", args, graph, why)
		}
	})
}

// C11
func TestMonorepo_C11_LinksLeaveNoGraph(t *testing.T) {
	cases := map[string]struct{ dir, real string }{
		"nx":    {"packages/ui", "packages/ui/index.js"},
		"turbo": {"packages/ui", "packages/ui/index.js"},
		"cargo": {"crates/app/src", "crates/app/src/lib.rs"},
	}
	for tool, c := range cases {
		t.Run(tool+" symlinked file", func(t *testing.T) {
			e := mrProject(t, tool)
			link := c.dir + "/link" + filepath.Ext(c.real)
			must(t, os.Symlink(filepath.Base(c.real), filepath.Join(e.repo, link)))
			if args, graph, why := e.affected(c.real); why != "" || !graph {
				t.Fatalf("control: the plain file maps with a graph: %v %v %q", args, graph, why)
			}
			if args, graph, why := e.affected(link); graph {
				t.Fatalf("%s is a symlink: graph must be false: %v %q", link, args, why)
			}
		})
		t.Run(tool+" hard link", func(t *testing.T) {
			e := mrProject(t, tool)
			a := filepath.Join(e.repo, c.dir, "h2"+filepath.Ext(c.real))
			b := filepath.Join(e.repo, c.dir, "h3"+filepath.Ext(c.real))
			write(t, a, "// linked\n")
			must(t, os.Link(a, b))
			rel := c.dir + "/h2" + filepath.Ext(c.real)
			if args, graph, why := e.affected(rel); graph {
				t.Fatalf("%s has a second name: graph must be false: %v %q", rel, args, why)
			}
		})
	}
	t.Run("nx project root through a symlinked folder", func(t *testing.T) {
		e := mrProject(t, "nx")
		ui := filepath.Join(e.repo, "packages", "ui")
		must(t, os.MkdirAll(filepath.Join(e.repo, "libs"), 0o755))
		must(t, os.Rename(ui, filepath.Join(e.repo, "libs", "ui")))
		must(t, os.Symlink(filepath.Join("..", "libs", "ui"), ui))
		args, graph, why := e.affected("packages/ui/index.js")
		if why != "" || graph {
			t.Fatalf("packages/ui is a symlink to libs/ui: the run goes ahead with graph false: %v %v %q", args, graph, why)
		}
	})
}

// C12
func TestMonorepo_C12_LetterCaseFoldsOnMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("letter case folds only on macOS")
	}
	t.Run("turbo", func(t *testing.T) {
		e := mrProject(t, "turbo")
		if _, err := os.Stat(filepath.Join(e.repo, "Packages", "UI", "index.js")); err != nil {
			t.Skip("this volume is case-sensitive")
		}
		args, graph, why := e.affected("Packages/UI/index.js")
		if why != "" || !graph || !mrSame(args, []string{"--filter=...@acme/ui"}) {
			t.Fatalf("Packages/UI is packages/ui on macOS: %v %v %q", args, graph, why)
		}
	})
	t.Run("cargo", func(t *testing.T) {
		e := mrProject(t, "cargo")
		if _, err := os.Stat(filepath.Join(e.repo, "Crates", "Core_Lib", "src", "lib.rs")); err != nil {
			t.Skip("this volume is case-sensitive")
		}
		args, graph, why := e.affected("Crates/Core_Lib/src/lib.rs")
		if why != "" || !graph || !mrSame(args, []string{"--package=app", "--package=core_lib", "--package=devuser"}) {
			t.Fatalf("Crates/Core_Lib is crates/core_lib on macOS: %v %v %q", args, graph, why)
		}
	})
}

// C13
func TestMonorepo_C13_NestedCrateIsTheLongestPrefix(t *testing.T) {
	e := mrProject(t, "cargo")
	// Hand-made rows: the capture plus a crate "inner" nested in crates/app.
	t.Setenv("MR_LIST", mrCargoMeta(t, e.repo, func(m map[string]any) {
		id := "path+file://" + e.repo + "/crates/app/inner#0.1.0"
		m["packages"] = append(m["packages"].([]any), map[string]any{
			"name": "inner", "version": "0.1.0", "id": id, "source": nil, "dependencies": []any{},
			"targets":       []any{map[string]any{"kind": []any{"lib"}, "crate_types": []any{"lib"}, "name": "inner", "src_path": e.repo + "/crates/app/inner/src/lib.rs", "edition": "2024", "doc": true, "doctest": true, "test": true}},
			"features":      map[string]any{},
			"manifest_path": e.repo + "/crates/app/inner/Cargo.toml",
			"edition":       "2024",
		})
		m["workspace_members"] = append(m["workspace_members"].([]any), id)
		m["workspace_default_members"] = append(m["workspace_default_members"].([]any), id)
	}))
	if args, graph, why := e.affected("crates/app/inner/src/lib.rs"); why != "" || !graph || !mrSame(args, []string{"--package=inner"}) {
		t.Fatalf("a file in the nested crate is the inner crate only: %v %v %q", args, graph, why)
	}
	if args, _, why := e.affected("crates/app/src/lib.rs"); why != "" || !mrSame(args, []string{"--package=app"}) {
		t.Fatalf("control: a file in app outside inner is app: %v %q", args, why)
	}
	// crates/apple shares the prefix "crates/app" but is no crate.
	if args, _, why := e.affected("crates/apple/notacrate/x.rs"); why == "" {
		t.Fatalf("crates/apple isn't inside crates/app; no crate holds it: got %v", args)
	}
}

// C14
func TestMonorepo_C14_NoCodeFileOrNoUnitHitRunsTheFullSuite(t *testing.T) {
	for _, tool := range []string{"nx", "turbo", "cargo"} {
		t.Run(tool, func(t *testing.T) {
			e := mrProject(t, tool)
			if args, _, why := e.affected("README.md", "docs/notes.md"); why == "" {
				t.Fatalf("only markdown outside every unit changed: full suite, got %v", args)
			}
			if tool != "turbo" { // turbo: a why for its own reason (C9)
				if args, _, why := e.affected("scripts/x.sh"); why == "" {
					t.Fatalf("no unit hit: full suite, got %v", args)
				}
			}
		})
	}
}

// C15
func TestMonorepo_C15_DeletedFileMapsByPath(t *testing.T) {
	for tool, c := range map[string]struct {
		file string
		want []string
	}{
		"nx":    {"packages/web/old.js", []string{"--files=packages/web/old.js"}},
		"turbo": {"packages/ui/old.js", []string{"--filter=...@acme/ui"}},
		"cargo": {"crates/solo/src/old.rs", []string{"--package=solo"}},
	} {
		t.Run(tool, func(t *testing.T) {
			e := mrProject(t, tool)
			must(t, os.Remove(filepath.Join(e.repo, c.file)))
			if args, _, why := e.affected(c.file); why != "" || !mrSame(args, c.want) {
				t.Fatalf("deleted %s should still map to its unit: %v %q", c.file, args, why)
			}
		})
	}
}

// --- ParseTestOutput ---

func mrSorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func mrHasAll(have []string, want ...string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// C16
func TestMonorepo_C16_NxColoredVitestOutputParses(t *testing.T) {
	c := ParseTestOutput(mrText(t, "nx-fail.txt"), "")
	if c.Ran != 3 || c.Failed != 3 {
		t.Fatalf("nx ran web (1 failed of 1) and @acme/ui (2 failed of 2): ran %d failed %d", c.Ran, c.Failed)
	}
	want := []string{"ui.test.js > adds", "ui.test.js > adds2", "web.test.js > web uses ui"}
	if !mrSame(mrSorted(c.IDs), want) {
		t.Fatalf("failure names, ANSI stripped:\n got %q\nwant %q", c.IDs, want)
	}
}

// C17
func TestMonorepo_C17_TurboPrefixedPassParsesAndPytestLineIsKept(t *testing.T) {
	c := ParseTestOutput(mrText(t, "turbo-prefixed-pass.txt"), "")
	if c.Ran != 3 || c.Failed != 0 {
		t.Fatalf("turbo ran web (1) and @acme/ui (2): ran %d failed %d", c.Ran, c.Failed)
	}
	// Hand-made pytest output (no turbo banner) with a 'path:line: ' line.
	py := "============================= test session starts ==============================\ncollected 3 items\n\ntests/a.py .F.                                                           [100%]\n\n=================================== FAILURES ===================================\n____________________________________ test_x ____________________________________\n\n    def test_x():\n>       assert 1 == 2\nE       assert 1 == 2\n\ntests/a.py:12: AssertionError\n=========================== short test summary info ============================\nFAILED tests/a.py::test_x - assert 1 == 2\n========================= 1 failed, 2 passed in 0.10s ==========================\n"
	p := ParseTestOutput(py, "")
	if p.Ran != 3 || p.Failed != 1 || !mrSame(p.IDs, []string{"tests/a.py::test_x"}) {
		t.Fatalf("pytest output parses as before: %+v", p)
	}
}

// C18
func TestMonorepo_C18_TwoVitestRunsJoinedSum(t *testing.T) {
	// Hand-made, shaped after the captured vitest blocks: 'npm run test &&
	// npm run test:db' prints two summaries.
	first := " RUN  v3.2.7 /work/app\n\n ✓ a.test.ts (2 tests) 1ms\n\n Test Files  1 passed (1)\n      Tests  2 passed (2)\n   Start at  18:02:43\n   Duration  602ms\n\n"
	second := " RUN  v3.2.7 /work/app\n\n ❯ db.test.ts (4 tests | 1 failed) 7ms\n   × db > writes 6ms\n\n FAIL  db.test.ts > db > writes\nAssertionError: expected 1 to be 2\n\n Test Files  1 failed (1)\n      Tests  1 failed | 3 passed (4)\n   Start at  18:02:44\n   Duration  700ms\n"
	if c := ParseTestOutput(first+second, ""); c.Ran != 6 || c.Failed != 1 || !mrSame(c.IDs, []string{"db.test.ts > db > writes"}) {
		t.Fatalf("two vitest summaries sum: %+v", c)
	}
	if c := ParseTestOutput(second, ""); c.Ran != 4 || c.Failed != 1 {
		t.Fatalf("one vitest summary parses as before: %+v", c)
	}
	j1 := "Test Suites: 1 failed, 1 total\nTests:       1 failed, 2 passed, 3 total\nSnapshots:   0 total\n"
	j2 := "Test Suites: 1 passed, 1 total\nTests:       4 passed, 4 total\nSnapshots:   0 total\n"
	if c := ParseTestOutput(j1+j2, ""); c.Ran != 7 || c.Failed != 1 {
		t.Fatalf("two jest summaries sum: %+v", c)
	}
	if c := ParseTestOutput(j1, ""); c.Ran != 3 || c.Failed != 1 {
		t.Fatalf("one jest summary parses as before: %+v", c)
	}
}

// C19
func TestMonorepo_C19_CargoEveryTestResultCounts(t *testing.T) {
	if c := ParseTestOutput(mrText(t, "cargo-pass.txt"), ""); c.Ran != 4 || c.Failed != 0 {
		t.Fatalf("cargo test over app, core_lib, devuser (lib and tests/it.rs) plus doc-tests ran 4: %+v", c)
	}
	if c := ParseTestOutput(mrText(t, "cargo-fail.txt"), ""); c.Ran != 1 || c.Failed != 1 || !mrSame(c.IDs, []string{"tests::two"}) {
		t.Fatalf("the failing capture: %+v", c)
	}
}

// C29
func TestMonorepo_C29_JestAndVitestInOneOutputBothCount(t *testing.T) {
	// Hand-made: a jest run then a vitest run in one output.
	text := "FAIL src/a.test.js\n  ● suite › fails\n\n    expect(received).toBe(expected)\n\nTest Suites: 1 failed, 1 total\nTests:       1 failed, 2 passed, 3 total\n\n RUN  v3.2.7 /work/app\n\n FAIL  src/b.test.ts > b > fails\nAssertionError: expected 1 to be 2\n\n Test Files  1 failed (1)\n      Tests  1 failed | 1 passed (2)\n"
	c := ParseTestOutput(text, "")
	if c.Ran != 5 || c.Failed != 2 {
		t.Fatalf("jest (3, 1 failed) and vitest (2, 1 failed): ran %d failed %d", c.Ran, c.Failed)
	}
	if !mrHasAll(c.IDs, "suite › fails", "src/b.test.ts > b > fails") {
		t.Fatalf("both runners' failure names: %q", c.IDs)
	}
}

// C30
func TestMonorepo_C30_TurboDefaultFailingOutputParses(t *testing.T) {
	c := ParseTestOutput(mrText(t, "turbo-prefixed-fail.txt"), "")
	if c.Ran != 3 || c.Failed != 3 {
		t.Fatalf("turbo's interleaved failing output: ran %d failed %d", c.Ran, c.Failed)
	}
	want := []string{"ui.test.js > adds", "ui.test.js > adds2", "web.test.js > web uses ui"}
	if !mrSame(mrSorted(c.IDs), want) {
		t.Fatalf("failure names with the pkg:task: prefix removed:\n got %q\nwant %q", c.IDs, want)
	}
}

// --- the related command ---

// C20
func TestMonorepo_C20_FlagArgsPassUnprefixedAndQuoted(t *testing.T) {
	dir := t.TempDir()
	got := mrWords(t, dir, FillRelated("turbo run test --continue {files}", []string{"--filter=...@acme/ui", "--filter=...web"}, true))
	if want := []string{"turbo", "run", "test", "--continue", "--filter=...@acme/ui", "--filter=...web"}; !mrSame(got, want) {
		t.Fatalf("flag args stay flags:\n got %s\nwant %s", mrJoin(got), mrJoin(want))
	}
	got = mrWords(t, dir, FillRelated("npx jest --findRelatedTests {files}", []string{"-u.ts", "src/a b.ts"}, false))
	if want := []string{"npx", "jest", "--findRelatedTests", "./-u.ts", "src/a b.ts"}; !mrSame(got, want) {
		t.Fatalf("a file starting with - still gets ./:\n got %s\nwant %s", mrJoin(got), mrJoin(want))
	}
	got = mrWords(t, dir, FillRelated("npx nx affected -t test {files}", []string{"--files=a$(touch PWNED).js,b;touch PWNED"}, true))
	if want := []string{"npx", "nx", "affected", "-t", "test", "--files=a$(touch PWNED).js,b;touch PWNED"}; !mrSame(got, want) {
		t.Fatalf("an arg with shell characters is one quoted word:\n got %s\nwant %s", mrJoin(got), mrJoin(want))
	}
	if _, err := os.Stat(filepath.Join(dir, "PWNED")); err == nil {
		t.Fatal("an arg ran as a shell command")
	}

	for tool, c := range map[string]struct {
		file, graph string
		argv        []string
	}{
		"nx":    {"packages/ui/index.js", "nx", []string{"nx", "affected", "-t", "test", "--files=packages/ui/index.js"}},
		"turbo": {"packages/ui/index.js", "turbo", []string{"turbo", "run", "test", "--continue", "--filter=...@acme/ui"}},
		"cargo": {"crates/app/src/lib.rs", "cargo metadata", []string{"cargo", "test", "--no-fail-fast", "--package=app"}},
	} {
		t.Run("run related "+tool, func(t *testing.T) {
			e := mrProject(t, tool)
			out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", c.file)
			r := lastRun(e.repo)
			if r == nil || r.Kind != KindRelated || r.Graph != c.graph {
				t.Fatalf("a %s related run with graph %q: %+v\n%s", tool, c.graph, r, out)
			}
			if strings.Contains(r.Command, "./-") {
				t.Fatalf("a flag arg got ./ in front: %q", r.Command)
			}
			runs := mrRuns(t, e.log)
			if len(runs) != 1 || !mrSame(runs[0], c.argv) {
				t.Fatalf("the tool should get exactly %s; got %v\n%s", mrJoin(c.argv), runs, out)
			}
			if !mrSame(r.Files, []string{c.file}) || r.Ran <= 0 {
				t.Fatalf("the run lists the changed file and its tests ran: files=%v ran=%d", r.Files, r.Ran)
			}
		})
	}
}

// C26
func TestMonorepo_C26_OneListingCallPerRun(t *testing.T) {
	for tool, files := range map[string][]string{
		"nx":    {"packages/ui/index.js", "packages/ui/ui.test.js", "packages/web/index.js", "packages/solo/index.js", "docs/notes.md"},
		"turbo": {"packages/ui/index.js", "packages/ui/ui.test.js", "packages/web/index.js", "packages/solo/index.js", "docs/notes.md"},
		"cargo": {"crates/app/src/lib.rs", "crates/core_lib/src/lib.rs", "crates/devuser/tests/it.rs", "crates/solo/src/lib.rs", "docs/notes.md"},
	} {
		t.Run(tool, func(t *testing.T) {
			e := mrProject(t, tool)
			if _, _, why := e.affected(files...); why != "" {
				t.Fatalf("premise: the files map: %q", why)
			}
			if n := mrListings(t, e.log); n != 1 {
				t.Fatalf("%d files mapped with %d listing calls, want 1: %v", len(files), n, mrCalls(t, e.log))
			}
			must(t, os.Remove(e.log))
			out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", strings.Join(files, ","))
			if n := mrListings(t, e.log); n != 1 {
				t.Fatalf("run related made %d listing calls, want 1: %v\n%s", n, mrCalls(t, e.log), out)
			}
		})
	}
}

// C27
func TestMonorepo_C27_DerivedCommandsRunEveryUnit(t *testing.T) {
	for test, want := range map[string]string{
		"cargo test":                            "cargo test --no-fail-fast {files}",
		"cargo test --no-fail-fast":             "cargo test --no-fail-fast {files}",
		"cargo test --workspace --no-fail-fast": "cargo test --no-fail-fast {files}",
		"turbo run test":                        "turbo run test --continue {files}",
		"turbo run test --continue":             "turbo run test --continue {files}",
		"npx turbo run test":                    "npx turbo run test --continue {files}",
		"npx nx run-many -t test":               "npx nx affected -t test {files}",
		"pnpm exec nx run-many -t test --all":   "pnpm exec nx affected -t test {files}",
	} {
		if got := mrTemplate(test); got != want {
			t.Errorf("test %q: related %q, want %q", test, got, want)
		}
	}
}

// C31
func TestMonorepo_C31_RootScriptDerivesWithTheManagersRunner(t *testing.T) {
	derive := func(test, pkg string) string {
		dir := t.TempDir()
		if pkg != "" {
			write(t, filepath.Join(dir, "package.json"), pkg)
		}
		s := &Setup{}
		s.Commands.Test = test
		s.Root = dir
		return savedCommand(s, KindRelated)
	}
	script := func(cmd string) string {
		b, _ := json.Marshal(map[string]any{"name": "ws", "private": true, "scripts": map[string]string{"test": cmd}})
		return string(b)
	}
	for _, c := range []struct{ test, script, want string }{
		{"npm test", "turbo run test", "npx turbo run test --continue {files}"},
		{"npm run test", "turbo run test", "npx turbo run test --continue {files}"},
		{"pnpm test", "turbo run test", "pnpm exec turbo run test --continue {files}"},
		{"yarn test", "turbo run test", "yarn turbo run test --continue {files}"},
		{"npm test", "nx run-many -t test", "npx nx affected -t test {files}"},
		{"pnpm test", "nx run-many -t test", "pnpm exec nx affected -t test {files}"},
	} {
		if got := derive(c.test, script(c.script)); got != c.want {
			t.Errorf("%q with script %q: related %q, want %q", c.test, c.script, got, c.want)
		}
	}
	for _, c := range []struct{ name, test, pkg string }{
		{"&& in the script", "npm test", script("turbo run test && echo done")},
		{"env assignment", "npm test", script("CI=1 turbo run test")},
		{"selection flag in the script", "npm test", script("turbo run test --filter=web")},
		{"missing package.json", "npm test", ""},
		{"package.json not JSON", "npm test", "{not json"},
		{"no test script", "npm test", `{"name":"ws","scripts":{"lint":"turbo run lint"}}`},
		{"bun test", "bun test", script("turbo run test")},
	} {
		if got := derive(c.test, c.pkg); got != "" {
			t.Errorf("%s: no related command should be derived, got %q", c.name, got)
		}
	}
	s := &Setup{}
	s.Commands.Test = "npm test"
	if got := savedCommand(s, KindRelated); got != "" {
		t.Errorf("no project root on the setup: nothing to read, so not derived: %q", got)
	}

	// Through LoadSetup, and the listing runs with npm's runner.
	e := mrProjectTest(t, "turbo", "npm test")
	loaded, _, err := LoadSetup(e.repo)
	must(t, err)
	if foldPath(loaded.Root) != foldPath(e.repo) {
		t.Fatalf("LoadSetup should record the project root: %q, want %q", loaded.Root, e.repo)
	}
	if got := RelatedTemplate(loaded); got != "npx turbo run test --continue {files}" {
		t.Fatalf("the root script turbo run test derives through npx: %q", got)
	}
	if !IsDerivedMonorepo(loaded) || AffectedGraph(loaded) != "turbo" {
		t.Fatalf("npm test over a turbo script has the turbo graph: %v %q", IsDerivedMonorepo(loaded), AffectedGraph(loaded))
	}
	args, _, why := MonorepoAffected(e.repo, loaded, []string{"packages/ui/index.js"})
	if why != "" || !mrSame(args, []string{"--filter=...@acme/ui"}) {
		t.Fatalf("npm test over turbo maps like turbo: %v %q", args, why)
	}
	via := false
	for _, c := range mrCalls(t, e.log) {
		if strings.Join(c, " ") == "via npx turbo ls --output=json" {
			via = true
		}
	}
	if !via {
		t.Fatalf("turbo ls should run through npx: %v", mrCalls(t, e.log))
	}
}

// C32
func TestMonorepo_C32_NxListingRunsWithoutTheDaemon(t *testing.T) {
	e := mrProject(t, "nx")
	t.Setenv("NX_DAEMON", "true")
	if _, _, why := e.affected("packages/ui/index.js"); why != "" {
		t.Fatalf("premise: maps: %q", why)
	}
	var env []string
	for _, c := range mrCalls(t, e.log) {
		if c[0] == "nxenv" && len(c) > 1 {
			env = append(env, c[1])
		}
	}
	if !mrSame(env, []string{"false"}) {
		t.Fatalf("nx graph should run once with NX_DAEMON=false, got %v", env)
	}
}

// --- the final proof and the before-fix run ---

var mrTaskFiles = map[string]struct{ src, test string }{
	"nx":    {"packages/ui/index.js", "packages/ui/ui.test.js"},
	"turbo": {"packages/ui/index.js", "packages/ui/ui.test.js"},
	"cargo": {"crates/app/src/lib.rs", "crates/devuser/tests/it.rs"},
}

var mrFailOut = map[string]string{"nx": "nx-fail.txt", "turbo": "turbo-prefixed-fail.txt", "cargo": "cargo-fail.txt"}

// mrHarness: a building task in an nx, turbo or cargo workspace with
// affected_final on, the task's start commit recorded and the baselines
// run (through the fakes, with whatever MR_OUT the caller set by then, or
// a passing run).
func mrHarness(t *testing.T, tool string, beforeApprove func(h *harness)) (*harness, string) {
	t.Helper()
	h := newHarness(t)
	log := mrFakeTools(t)
	for p, text := range mrToolFiles(tool) {
		write(t, filepath.Join(h.repo, p), text)
	}
	run(t, h.repo, "git", "add", "-A")
	run(t, h.repo, "git", "commit", "-q", "-m", "workspace")
	mrUseListing(t, tool, h.repo)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = mrTestCmd[tool]
	s.IgnoreChanges = []string{"coverage/**"}
	s.AffectedFinal = true
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("small")
	tk := h.task()
	tk.StartCommit = HeadCommit(h.repo)
	must(t, SaveTask(tk))
	for _, lookup := range []string{"mcp__socraticode__search", "mcp__mempalace__search", "mcp__serena__find_symbol"} {
		h.post(lookup, nil, nil)
	}
	h.post("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}, nil)
	h.docs(DocUnderstanding, "# U\nA returns 1 (src/a.go:3)\n## Reuse\n- Searched src/ for a helper that does this: nothing\n")
	f := mrTaskFiles[tool]
	team := ""
	if tool != "cargo" { // .js files under packages/ count as frontend
		team = "## Team\n- lead, code-reviewer, ux-reviewer\n"
	}
	h.docs(DocPlan, "# Plan\n## Goal\nDone when: add returns the sum.\n## Files\n- "+f.src+"\n- "+f.test+"\n## Cases\n- [bug] add returns the sum -> test: "+f.test+"\n## Real-world proof\n- does not apply, because no API or UI\n## Needs your OK\n- none\n## Guards loosened\n- none\n"+team)
	if beforeApprove != nil {
		beforeApprove(h)
	}
	h.approve()
	return h, log
}

// C21
func TestMonorepo_C21_FinalProofAcceptsGraphRunRefusesWithoutGraph(t *testing.T) {
	for tool, graph := range map[string]string{"nx": "nx", "turbo": "turbo", "cargo": "cargo metadata"} {
		t.Run(tool, func(t *testing.T) {
			h, _ := mrHarness(t, tool, nil)
			f := mrTaskFiles[tool]
			h.affEdit(f.src, "// changed\n"+mrToolFiles(tool)[f.src])
			r := h.affRelatedFinal(f.src)
			if r.Kind != KindRelated || r.Graph != graph || r.Failed != 0 || r.Exit != 0 || r.Ran <= 0 {
				t.Fatalf("a passing %s related run with graph %q: %+v", tool, graph, r)
			}
			p, m := h.affProof()
			if p == nil || p.ID != r.ID {
				t.Fatalf("AffectedProof should return the %s run %s: %+v (%s)", tool, r.ID, p, m)
			}
			if strings.Contains(m, "full test suite") {
				t.Fatalf("the affected run should stand in for the full suite: %s", m)
			}
			// Hand-made row: the real run with graph cleared.
			s, fp := h.affSetupFP()
			unmarked := *r
			unmarked.Graph = ""
			if AffectedProof(h.task(), s, []*Run{&unmarked}, fp) != nil {
				t.Fatal("a related run with no graph counted as the final proof")
			}
		})
	}
}

// C28
func TestMonorepo_C28_FailingMonorepoRunIsNeverTheFinalProof(t *testing.T) {
	for _, tool := range []string{"nx", "turbo", "cargo"} {
		t.Run(tool+" failing like the baseline", func(t *testing.T) {
			h, _ := mrHarness(t, tool, func(h *harness) {
				t.Setenv("MR_OUT", mrFixture(t, mrFailOut[tool]))
				t.Setenv("MR_OUT_EXIT", "1")
			})
			var base *Run
			for _, r := range LoadRuns(h.repo) {
				if r.Kind == KindTest && r.Baseline {
					base = r
				}
			}
			if base == nil || base.Failed == 0 || len(base.FailedIDs) == 0 {
				t.Fatalf("premise: the baseline suite fails with named failures: %+v", base)
			}
			f := mrTaskFiles[tool]
			h.affEdit(f.src, "// changed\n"+mrToolFiles(tool)[f.src])
			r := h.affRelatedFinal(f.src)
			if r.Kind != KindRelated || r.Failed == 0 || !mrSame(mrSorted(r.FailedIDs), mrSorted(base.FailedIDs)) {
				t.Fatalf("premise: the related run fails with the baseline's names: %+v vs %v", r, base.FailedIDs)
			}
			s, fp := h.affSetupFP()
			if p := AffectedProof(h.task(), s, []*Run{base, r}, fp); p != nil {
				t.Fatalf("a failing %s run counted as the final proof (names collide across units): %+v", tool, p)
			}
		})
		t.Run(tool+" exit non-zero", func(t *testing.T) {
			h, _ := mrHarness(t, tool, nil)
			f := mrTaskFiles[tool]
			h.affEdit(f.src, "// changed\n"+mrToolFiles(tool)[f.src])
			r := h.affRelatedFinal(f.src)
			s, fp := h.affSetupFP()
			if AffectedProof(h.task(), s, []*Run{r}, fp) == nil {
				t.Fatalf("control: the passing run counts: %+v", r)
			}
			// Hand-made row: the real passing run with exit 1.
			bad := *r
			bad.Exit = 1
			if p := AffectedProof(h.task(), s, []*Run{&bad}, fp); p != nil {
				t.Fatal("a run that exited 1 counted as the final proof")
			}
		})
	}
}

// C22
func TestMonorepo_C22_BeforeFixMapsTestFilesToUnits(t *testing.T) {
	for tool, want := range map[string][]string{
		"nx":    {"npx", "nx", "affected", "-t", "test", "--files=packages/ui/ui.test.js"},
		"turbo": {"turbo", "run", "test", "--continue", "--filter=...@acme/ui"},
		"cargo": {"cargo", "test", "--no-fail-fast", "--package=devuser"},
	} {
		t.Run(tool, func(t *testing.T) {
			h, _ := mrHarness(t, tool, nil)
			f := mrTaskFiles[tool]
			h.affEdit(f.test, mrToolFiles(tool)[f.test]+"// new case\n")
			s, _, err := LoadSetup(h.repo)
			must(t, err)
			r, err := RunBeforeFix(h.repo, h.task(), []string{f.test}, s, io.Discard)
			if err != nil {
				t.Fatalf("before-fix refused: %v", err)
			}
			if got := mrWords(t, t.TempDir(), r.Command); !mrSame(got, want) {
				t.Fatalf("before-fix command:\n got %s\nwant %s", mrJoin(got), mrJoin(want))
			}
			t.Setenv("MR_LIST_EXIT", "1")
			if r, err := RunBeforeFix(h.repo, h.task(), []string{f.test}, s, io.Discard); err == nil {
				t.Fatalf("the listing failed: before-fix should be an error, not a run: %+v", r)
			}
		})
	}
}

// --- skill and release text ---

func mrRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(rel)
	must(t, err)
	return string(b)
}

func mrLineWith(text string, words ...string) string {
	for _, line := range strings.Split(text, "\n") {
		l := strings.ToLower(line)
		ok := true
		for _, w := range words {
			if !strings.Contains(l, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			return line
		}
	}
	return ""
}

// C24
func TestMonorepo_C24_TaskSkillNoSuiteBeforeApproval(t *testing.T) {
	skill := mrRead(t, filepath.Join("..", "..", "skills", "task", "SKILL.md"))
	rules := mrRead(t, "rules-task.md")
	if strings.Contains(skill, "reproduce first (`plumbline run test`") {
		t.Error("the task skill still offers the full suite (plumbline run test) as the way to reproduce a bug")
	}
	if mrLineWith(skill, "reproduce", "one test file", "script") == "" {
		t.Error("the task skill should say to reproduce a bug with one test file or a script")
	}
	if mrLineWith(skill, "suite", "before approval", "package") == "" {
		t.Error("the task skill should say no full suite before approval unless Plumbline asks (package-changing plans)")
	}
	if mrLineWith(rules, "one test file") == "" {
		t.Error("rules-task.md should say to reproduce with one test file, as the skill does")
	}
	if mrLineWith(rules, "suite", "before approval") == "" {
		t.Error("rules-task.md should say no suite before approval, as the skill does")
	}
}

// C25
func TestMonorepo_C25_ReleaseTextNamesTheThreeTools(t *testing.T) {
	if !strings.Contains(mrRead(t, filepath.Join("..", "..", "CHANGELOG.md")), "0.8.12") {
		t.Error("CHANGELOG.md has no 0.8.12 entry")
	}
	var plugin struct{ Version string }
	must(t, json.Unmarshal([]byte(mrRead(t, filepath.Join("..", "..", ".claude-plugin", "plugin.json"))), &plugin))
	if plugin.Version != "0.8.12" {
		t.Errorf("plugin.json version %q, want 0.8.12", plugin.Version)
	}
	row := mrLineWith(mrRead(t, filepath.Join("..", "..", "README.md")), "| test speed")
	setup := mrLineWith(mrRead(t, filepath.Join("..", "..", "skills", "setup", "SKILL.md")), "affected_final")
	for _, w := range []string{"nx", "turbo", "cargo"} {
		if !strings.Contains(strings.ToLower(row), w) {
			t.Errorf("README's Test speed row should name %s", w)
		}
		if !strings.Contains(strings.ToLower(setup), w) {
			t.Errorf("the setup skill's affected_final question should name %s", w)
		}
	}
	for _, w := range []string{"include_str!", "include_bytes!"} {
		if !strings.Contains(setup, w) {
			t.Errorf("the setup skill's affected_final search should include %s", w)
		}
	}
}
