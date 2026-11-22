package core

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAddedComments(t *testing.T) {
	cases := []struct {
		file, before, after string
		want                []string
	}{
		{"a.ts", "", "const a = 1 // the count\n", []string{"the count"}},
		{"a.ts", "", "// fetch the user\nconst u = get()\n", []string{"fetch the user"}},
		{"a.ts", "", "/**\n * Gets the user.\n * @param id the id\n */\nfunction g(id) {}\n", []string{"Gets the user. @param id the id"}},
		{"a.tsx", "", "const x = <p>{/* hidden */}</p>\n", []string{"hidden"}},
		{"a.ts", "", "const u = \"http://example.com\"\nconst v = 'a // b'\n", nil},
		{"a.tsx", "", "const x = <a href={u}>see http://example.com</a>\n", nil},
		{"a.tsx", "", "const x = <p>Don't worry, it's fine</p>\n", nil},
		{"a.ts", "", "const q = `line one\n// not a comment\n`\n", nil},
		{"a.ts", "", "// eslint-disable-next-line no-console\nconsole.log(1)\n// @ts-expect-error: wrong lib types\nf(2)\n/* istanbul ignore next */\n", nil},
		{"a.ts", "", "/// <reference types=\"vite/client\" />\n", nil},
		{"a.ts", "", "//\nconst a = 1\n/**/\n", nil},
		{"a.ts", "// keep me\nconst a = 1\n", "const a = 2\n// keep me\n", nil},
		{"a.ts", "// keep me\nconst a = 1\n", "const a = 1\n", nil},
		{"a.ts", "// fetch the user\nconst a = 1\n", "// fetch the users\nconst a = 1\n", []string{"fetch the users"}},
		{"a.ts", "// same\nconst a = 1\n", "// same\nconst a = 1\n// same\n", []string{"same"}},
		{"a.ts", "", "/*\n * Copyright 2026 Example Inc.\n * Licensed under the MIT license.\n */\nexport const a = 1\n", nil},
		{"a.ts", "", "// Copyright 2026 Example Inc.\n// Permission is granted to use this file.\nexport const a = 1\n", nil},
		{"a.ts", "", "export const a = 1\n// Copyright 2026 Example Inc.\n", []string{"Copyright 2026 Example Inc."}},
		{"a.go", "", "//go:build linux\n\npackage a\n\n//go:generate stringer -type=K\n//nolint:errcheck\nvar x = 1\n", nil},
		{"a.go", "", "package a\n\n// A does a thing.\nfunc A() {}\n", []string{"A does a thing."}},
		{"a.go", "", "package a\n\nvar s = `\n// raw\n`\nvar r = '\"'\n", nil},
		{"a.go", "", "package a\n\nvar s = `a\\`\nvar t = `// raw`\n", nil},
		{"a.ts", "", "const s = `a\\`` + `\n// in template\n`\n", nil},
		{"a.rs", "", "/// Adds one.\nfn f() {}\n", []string{"Adds one."}},
		{"a.rs", "", "fn f<'a>(x: &'a str) -> &'a str { x }\n", nil},
		{"a.rs", "", "#[derive(Debug)]\nstruct S;\n", nil},
		{"a.py", "", "x = 1  # the count\n", []string{"the count"}},
		{"a.py", "", "def f():\n    \"\"\"Return one.\"\"\"\n    return 1\n", []string{"Return one."}},
		{"a.py", "", "\"\"\"The module.\n\nMore.\n\"\"\"\nimport os\n", []string{"The module. More."}},
		{"a.py", "", "x = \"\"\"text\n# not a comment\n\"\"\"\ny = f(\n    \"\"\"arg\"\"\"\n)\n", nil},
		{"a.py", "", "#!/usr/bin/env python3\n# -*- coding: utf-8 -*-\nimport os  # noqa: F401\ny = os.x  # type: ignore[attr-defined]\nc = '#fff'\nd = f\"{c}#b\"\n", nil},
		{"a.sh", "", "#!/bin/sh\necho $# ${#arr} a#b\n", nil},
		{"a.sh", "", "#!/bin/sh\n# install deps\nnpm ci\n", []string{"install deps"}},
		{".github/workflows/ci.yml", "", "on: push\n# run nightly\njobs: {}\n", []string{"run nightly"}},
		{"a.yaml", "", "color: \"#fff\"\nurl: http://a.b/c#frag\nname: it's # note\n", []string{"note"}},
		{"Dockerfile", "", "# syntax=docker/dockerfile:1\nFROM node\n# install deps\nRUN npm ci\n", []string{"install deps"}},
		{"Makefile", "", "all:\n\t@echo hi # say hi\n", []string{"say hi"}},
		{"a.sql", "", "-- add the column\nalter table t add c int;\nselect '--not' from t; /* why */\n", []string{"add the column", "why"}},
		{"a.css", "", "a { background: url(http://x.y/z.png) }\n/* spacing */\n", []string{"spacing"}},
		{"a.scss", "", "a { background: url(http://x.y/z.png) }\n// spacing\n", []string{"spacing"}},
		{"a.html", "", "<!doctype html>\n<!-- prettier-ignore -->\n<p>a</p>\n<!-- the footer -->\n", []string{"the footer"}},
		{"a.vue", "", "<template><p>x</p></template>\n<script>\n// the setup\nconst a = 1\n</script>\n", []string{"the setup"}},
		{"a.php", "", "<?php\n#[Attr]\nfunction f() {}\n# old style\n", []string{"old style"}},
		{"a.lua", "", "--[[ block ]]\nlocal a = 1 -- line\n", []string{"block", "line"}},
		{"a.ini", "", "[x]\n; a note\nk=v;w\n", []string{"a note"}},
		{"README.md", "", "<!-- note -->\n# Title\n", nil},
		{"pnpm-lock.yaml", "", "# note\n", nil},
		{"data.json", "", "{\"a\": \"// x\"}\n", nil},
	}
	for _, c := range cases {
		var got []string
		for _, cm := range addedComments(commentSyntaxFor(c.file), c.before, c.after) {
			got = append(got, cm.Text)
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %q: got %q, want %q", c.file, c.after, got, c.want)
		}
	}
}

func TestCommentLines(t *testing.T) {
	got := addedComments(synC, "", "const a = 1\n\nconst b = `x\ny`\n/* one\ntwo */\n// three\n")
	if len(got) != 2 || got[0].Line != 5 || got[1].Line != 7 {
		t.Fatalf("lines: %+v", got)
	}
}

func TestNoCommentsGate(t *testing.T) {
	h := buildingTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	h.expect(h.pre("Edit", map[string]any{"file_path": a, "old_string": "return 1 }", "new_string": "return 2 } // the new value"}), "deny", `src/a.go:3 "the new value"`)
	h.expect(h.pre("Edit", map[string]any{"file_path": a, "old_string": "return 1", "new_string": "return 2"}), "", "")
	h.expect(h.pre("Write", map[string]any{"file_path": a, "content": "// Package src is the code.\npackage src\n\nfunc A() int { return 2 }\n"}), "deny", "no code comments")
	h.expect(h.pre("Write", map[string]any{"file_path": a, "content": "//go:build !windows\n\npackage src\n\nfunc A() int { return 2 }\n"}), "", "")
	h.expect(h.pre("MultiEdit", map[string]any{"file_path": a, "edits": []any{
		map[string]any{"old_string": "return 1", "new_string": "return 2"},
		map[string]any{"old_string": "func A", "new_string": "/* A is a. */\nfunc A"},
	}}), "deny", "A is a.")
	h.expect(h.pre("Edit", map[string]any{"file_path": a, "old_string": "not in the file", "new_string": "x // y"}), "deny", `src/a.go "y"`)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\n// A returns one.\nfunc A() int { return 1 }\n")
	h.expect(h.pre("Edit", map[string]any{"file_path": a, "old_string": "// A returns one.\n", "new_string": ""}), "", "")
	h.expect(h.pre("Edit", map[string]any{"file_path": a, "old_string": "// A returns one.", "new_string": "// A returns 1."}), "deny", "A returns 1.")
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "notes.md"), "content": "<!-- x -->\n# Notes\n"}), "", "")
	gen := "// Code generated by protoc. DO NOT EDIT.\n\npackage src\n\n// G is generated.\nfunc G() {}\n"
	h.expect(h.pre("Write", map[string]any{"file_path": filepath.Join(h.repo, "src", "g.go"), "content": gen}), "deny", "G is generated.")
	write(t, filepath.Join(h.repo, "src", "g.go"), gen)
	h.expect(h.pre("Edit", map[string]any{"file_path": filepath.Join(h.repo, "src", "g.go"), "old_string": "func G() {}", "new_string": "// H too.\nfunc G() {}"}), "", "")
}

func TestNoCommentsGateNotebook(t *testing.T) {
	h := buildingTask(t)
	nb := filepath.Join(h.repo, "src", "a.ipynb")
	write(t, nb, `{"metadata":{"language_info":{"name":"python"}},"cells":[{"cell_type":"code","source":["# load\n","x = 1"]}]}`)
	h.expect(h.pre("NotebookEdit", map[string]any{"notebook_path": nb, "new_source": "# load\nx = 2"}), "", "")
	h.expect(h.pre("NotebookEdit", map[string]any{"notebook_path": nb, "new_source": "x = 2  # the value"}), "deny", "the value")
	h.expect(h.pre("NotebookEdit", map[string]any{"notebook_path": nb, "cell_type": "markdown", "new_source": "# Title"}), "", "")
}

func TestNoCommentsMovedFromAnotherFile(t *testing.T) {
	g := buildingTask(t)
	write(t, filepath.Join(g.repo, "src", "b.go"), "package src\n\n// B returns two.\nfunc B() int { return 2 }\n")
	run(t, g.repo, "git", "commit", "-qam", "b doc")
	tk := g.task()
	tk.StartCommit = HeadCommit(g.repo)
	must(t, SaveTask(tk))
	c := filepath.Join(g.repo, "src", "c.go")
	g.expect(g.pre("Write", map[string]any{"file_path": c, "content": "package src\n\n// B returns two.\nfunc C() int { return 2 }\n"}), "", "")
	g.expect(g.pre("Write", map[string]any{"file_path": c, "content": "package src\n\n// B returns two\nfunc C() int { return 2 }\n"}), "deny", "B returns two")
	g.expect(g.pre("Write", map[string]any{"file_path": c, "content": "package src\n\n// returns two.\nfunc C() int { return 2 }\n"}), "deny", "returns two.")
}

func TestNoCommentsAtFinish(t *testing.T) {
	h := provenTask(t)
	a := filepath.Join(h.repo, "src", "a.go")
	write(t, a, "package src\n\n// the user's note\nfunc A() int { return 2 }\n")
	write(t, filepath.Join(h.repo, "src", "n.py"), "def f():\n    return 1  # one\n")
	write(t, filepath.Join(h.repo, "src", "gen.ts"), "// @generated by the client tool\n// the client\nexport const c = 1\n")
	if m := h.finishMissing(); !strings.Contains(m, "comments: the diff adds code comments") || !strings.Contains(m, "src/a.go:3") || !strings.Contains(m, "src/n.py:2") || strings.Contains(m, "gen.ts") {
		t.Fatalf("finish should list the new comments, not a generated file's: %s", m)
	}
	s, _, _ := LoadSetup(h.repo)
	changes, err := taskChanges(h.task(), s.IgnoreChanges)
	must(t, err)
	report := "## Proof\n- Comments kept: src/a.go (the user wrote it), src/n.py (generated)\n"
	if m := commentsMissing(h.task(), changes, report); m != nil {
		t.Fatalf("kept files are accepted: %v", m)
	}
	if m := commentsMissing(h.task(), changes, "## Proof\n- Comments kept: src/a.go\n"); len(m) != 1 || strings.Contains(m[0], "a.go") || !strings.Contains(m[0], "src/n.py:2") {
		t.Fatalf("only n.py is left: %v", m)
	}
}

func TestNoCommentsAtFinishRename(t *testing.T) {
	h := provenTask(t)
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\n// A is kept.\nfunc A() int { return 2 }\n")
	run(t, h.repo, "git", "commit", "-qam", "doc A")
	tk := h.task()
	tk.StartCommit = HeadCommit(h.repo)
	run(t, h.repo, "git", "mv", "src/a.go", "src/a2.go")
	changes, err := taskChanges(tk, nil)
	must(t, err)
	if m := commentsMissing(tk, changes, ""); m != nil {
		t.Fatalf("a moved file keeps its comments: %v", m)
	}
	run(t, h.repo, "git", "mv", "src/a2.go", "src/a.go")
	write(t, filepath.Join(h.repo, "src", "z.go"), "package src\n\n// A is kept.\nfunc Z() {}\n")
	changes, err = taskChanges(tk, nil)
	must(t, err)
	if m := commentsMissing(tk, changes, ""); m != nil {
		t.Fatalf("a comment copied from another file counts as moved: %v", m)
	}
}
