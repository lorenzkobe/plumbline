package core

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMonorepoReview_RootUnitDoesNotHideOutsideFiles(t *testing.T) {
	t.Run("cargo root package", func(t *testing.T) {
		e := mrProject(t, "cargo")
		t.Setenv("MR_LIST", mrCargoMeta(t, e.repo, func(m map[string]any) {
			id := "path+file://" + e.repo + "#rootapp@0.1.0"
			m["packages"] = append(m["packages"].([]any), map[string]any{
				"name": "rootapp", "version": "0.1.0", "id": id, "dependencies": []any{},
				"manifest_path": e.repo + "/Cargo.toml",
			})
			m["workspace_members"] = append(m["workspace_members"].([]any), id)
		}))
		write(t, filepath.Join(e.repo, "fixtures", "data.json"), "{}\n")
		args, graph, why := e.affected("crates/app/src/lib.rs", "fixtures/data.json")
		if why != "" || graph {
			t.Fatalf("fixtures/data.json is in no member crate, only under the root package: graph must be false: %v %v %q", args, graph, why)
		}
		if args, graph, why := e.affected("crates/app/src/lib.rs"); why != "" || !graph || !mrSame(args, []string{"--package=app"}) {
			t.Fatalf("control: a member crate's file maps with a graph: %v %v %q", args, graph, why)
		}
	})
	t.Run("nx root project", func(t *testing.T) {
		e := mrProject(t, "nx")
		text := strings.Replace(mrText(t, "nx-graph.json"), `"nodes": {`, `"nodes": {"ws": {"name": "ws", "type": "lib", "data": {"root": "."}},`, 1)
		if !strings.Contains(text, `"ws"`) {
			t.Fatal("premise: the root project was added to the graph")
		}
		t.Setenv("MR_LIST", mrTemp(t, "graph.json", text))
		if args, graph, why := e.affected("packages/ui/index.js", "scripts/x.sh"); why != "" || graph {
			t.Fatalf("scripts/x.sh is only under the root project: graph must be false: %v %v %q", args, graph, why)
		}
		if args, graph, why := e.affected("packages/ui/index.js"); why != "" || !graph {
			t.Fatalf("control: a project's file maps with a graph: %v %v %q", args, graph, why)
		}
	})
}

func TestMonorepoReview_PrePostTestScriptsAreNotDerived(t *testing.T) {
	for _, extra := range []string{`"pretest":"npm run build"`, `"posttest":"node check.js"`} {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "package.json"), `{"name":"ws","scripts":{"test":"turbo run test",`+extra+`}}`)
		s := &Setup{Root: dir}
		s.Commands.Test = "npm test"
		if got := savedCommand(s, KindRelated); got != "" {
			t.Errorf("%s runs around npm test but not around the derived command: got %q", extra, got)
		}
	}
}

func TestMonorepoReview_FlagsThatBreakNarrowingAreNotDerived(t *testing.T) {
	for _, test := range []string{
		"cargo test --features=extra",
		"cargo test --workspace --features=app/extra",
		"cargo test --lib",
		"cargo test --doc",
		"cargo test --bins",
		"cargo test --examples",
		"nx run-many -t test --output-style=stream",
		"nx run-many -t test --output-style=stream-without-prefixes",
	} {
		if got := mrTemplate(test); got != "" {
			t.Errorf("%q: not derived, got %q", test, got)
		}
	}
	for test, want := range map[string]string{
		"cargo test --all-features":                 "cargo test --no-fail-fast --all-features {files}",
		"cargo test --tests":                        "cargo test --no-fail-fast --tests {files}",
		"nx run-many -t test --output-style=static": "nx affected -t test --output-style=static {files}",
	} {
		if got := mrTemplate(test); got != want {
			t.Errorf("%q: related %q, want %q", test, got, want)
		}
	}
}

func TestMonorepoReview_TurboRootTaskRunsTheFullSuite(t *testing.T) {
	e := mrProject(t, "turbo")
	write(t, filepath.Join(e.repo, "turbo.json"), `{"tasks":{"test":{},"//#test":{}}}`+"\n")
	if args, _, why := e.affected("packages/ui/index.js"); why == "" {
		t.Fatalf("a root task (//#test) runs under turbo run test but not under --filter: full suite, got %v", args)
	}
	write(t, filepath.Join(e.repo, "turbo.json"), "{\n  // comments are allowed in turbo.json\n  \"tasks\": {\"test\": {}}\n}\n")
	if _, _, why := e.affected("packages/ui/index.js"); why != "" {
		t.Fatalf("control: a turbo.json with comments and no root task maps: %q", why)
	}
	must(t, os.Remove(filepath.Join(e.repo, "turbo.json")))
	if args, _, why := e.affected("packages/ui/index.js"); why == "" {
		t.Fatalf("turbo.json missing: full suite, got %v", args)
	}
}

func TestMonorepoReview_ListingTimeoutKillsTheToolsChildren(t *testing.T) {
	e := mrProject(t, "nx")
	bin := t.TempDir()
	write(t, filepath.Join(bin, "npx"), "#!/bin/sh\nsleep 30 &\nwait\n")
	must(t, os.Chmod(filepath.Join(bin, "npx"), 0o755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	was := listTimeout
	listTimeout = time.Second
	t.Cleanup(func() { listTimeout = was })
	start := time.Now()
	_, _, why := e.affected("packages/ui/index.js")
	if took := time.Since(start); took > 15*time.Second {
		t.Fatalf("the listing outlived its limit by %v: the runner's child kept the pipe open", took)
	}
	if why == "" {
		t.Fatal("a listing that timed out mapped")
	}
}

func TestMonorepoReview_UnmappedFileIsNotRecordedAsRun(t *testing.T) {
	e := mrProject(t, "cargo")
	write(t, filepath.Join(e.repo, "tests", "regression.rs"), "#[test]\nfn r() {}\n")
	out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", "crates/core_lib/src/lib.rs,tests/regression.rs")
	r := lastRun(e.repo)
	if r == nil || r.Kind != KindRelated {
		t.Fatalf("a related run: %+v\n%s", r, out)
	}
	if contains(r.Files, "tests/regression.rs") {
		t.Fatalf("tests/regression.rs is in no crate, so cargo never ran it, yet the run lists it: %v", r.Files)
	}
	if !contains(r.Files, "crates/core_lib/src/lib.rs") {
		t.Fatalf("the mapped file is listed: %v", r.Files)
	}
}

func TestMonorepoReview_UnmappedFileSpelledWithDotSlashIsNotRecordedAsRun(t *testing.T) {
	e := mrProject(t, "cargo")
	write(t, filepath.Join(e.repo, "tests", "regression.rs"), "#[test]\nfn r() {}\n")
	out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", "crates/core_lib/src/lib.rs,./tests/regression.rs,tests//regression.rs")
	r := lastRun(e.repo)
	if r == nil || r.Kind != KindRelated {
		t.Fatalf("a related run: %+v\n%s", r, out)
	}
	for _, f := range r.Files {
		if strings.Contains(f, "regression.rs") {
			t.Fatalf("./tests/regression.rs is in no crate, yet the run lists it: %v", r.Files)
		}
	}
}

func TestMonorepoReview_NxGetsFilesOutsideEveryProject(t *testing.T) {
	e := mrProject(t, "nx")
	args, graph, why := e.affected("packages/ui/index.js", "tools/preset.js")
	if why != "" || graph || !mrSame(args, []string{"--files=packages/ui/index.js,tools/preset.js"}) {
		t.Fatalf("nx maps files outside its projects itself (sharedGlobals), so they go in --files, graph false: %v %v %q", args, graph, why)
	}
	out, _ := cliIn(t, e.repo, nil, "run", "related", "--files", "packages/ui/index.js,tools/preset.js")
	r := lastRun(e.repo)
	if r == nil || r.Kind != KindRelated || !strings.Contains(r.Command, "tools/preset.js") {
		t.Fatalf("the nx run passes tools/preset.js on: %+v\n%s", r, out)
	}
	if contains(r.Files, "tools/preset.js") {
		t.Fatalf("no project's tests are tools/preset.js, so it isn't recorded as run: %v", r.Files)
	}
}

func TestMonorepoReview_BeforeFixRefusesATestNoUnitHolds(t *testing.T) {
	h, _ := mrHarness(t, "cargo", nil)
	h.affEdit("tests/regression.rs", "#[test]\nfn r() {}\n")
	h.affEdit("crates/devuser/tests/it.rs", mrToolFiles("cargo")["crates/devuser/tests/it.rs"]+"// new case\n")
	s, _, err := LoadSetup(h.repo)
	must(t, err)
	r, err := RunBeforeFix(h.repo, h.task(), []string{"crates/devuser/tests/it.rs", "./tests/regression.rs"}, s, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "tests/regression.rs") {
		t.Fatalf("tests/regression.rs is in no crate, so the before-fix run can't show it failing: %+v %v", r, err)
	}
}
