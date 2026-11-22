package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

type commentSyntax struct {
	line     []string
	block    [][2]string
	py       bool
	backtick bool
	rawTick  bool
	tripleDQ bool
	hashAttr bool
}

type codeComment struct {
	Text  string
	First string
	Line  int
	lead  bool
}

var (
	synC    = &commentSyntax{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, backtick: true, tripleDQ: true}
	synGo   = &commentSyntax{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}, backtick: true, rawTick: true}
	synCSS  = &commentSyntax{block: [][2]string{{"/*", "*/"}}}
	synSCSS = &commentSyntax{line: []string{"//"}, block: [][2]string{{"/*", "*/"}}}
	synHash = &commentSyntax{line: []string{"#"}}
	synPy   = &commentSyntax{line: []string{"#"}, py: true}
	synSQL  = &commentSyntax{line: []string{"--"}, block: [][2]string{{"/*", "*/"}}}
	synLua  = &commentSyntax{line: []string{"--"}, block: [][2]string{{"--[[", "]]"}}}
	synHs   = &commentSyntax{line: []string{"--"}, block: [][2]string{{"{-", "-}"}}}
	synHTML = &commentSyntax{block: [][2]string{{"<!--", "-->"}}}
	synSFC  = &commentSyntax{line: []string{"//"}, block: [][2]string{{"<!--", "-->"}, {"/*", "*/"}}, backtick: true}
	synPHP  = &commentSyntax{line: []string{"//", "#"}, block: [][2]string{{"/*", "*/"}}, hashAttr: true}
	synHCL  = &commentSyntax{line: []string{"#", "//"}, block: [][2]string{{"/*", "*/"}}}
	synPS   = &commentSyntax{line: []string{"#"}, block: [][2]string{{"<#", "#>"}}}
	synINI  = &commentSyntax{line: []string{"#", ";"}}
)

var commentExts = map[string]*commentSyntax{}

func init() {
	for syn, exts := range map[*commentSyntax]string{
		synGo:   "go",
		synC:    "js jsx ts tsx mjs cjs mts cts java kt kts swift c h cc cpp cxx hpp hh m mm cs rs scala sc dart groovy gradle proto zig sol jsonc json5",
		synCSS:  "css",
		synSCSS: "scss sass less styl",
		synHash: "sh bash zsh fish ksh yaml yml toml rb rake gemspec pl pm r ex exs cr jl nim tcl awk mk cmake dockerfile containerfile",
		synPy:   "py pyi pyw",
		synSQL:  "sql psql pgsql",
		synLua:  "lua",
		synHs:   "hs elm purs",
		synHTML: "html htm xhtml xml svg xsl xslt",
		synSFC:  "vue svelte astro",
		synPHP:  "php phtml",
		synHCL:  "tf tfvars hcl nomad",
		synPS:   "ps1 psm1 psd1",
		synINI:  "ini cfg conf properties editorconfig gitconfig gitmodules npmrc",
	} {
		for _, e := range strings.Fields(exts) {
			commentExts[e] = syn
		}
	}
}

var (
	reHashNames  = regexp.MustCompile(`^(dockerfile|containerfile|makefile|gnumakefile|gemfile|rakefile|podfile|vagrantfile|brewfile|procfile|justfile|codeowners|\.tool-versions|\.yarnrc|\.gitattributes|\.[\w.-]*ignore|\.env\.[\w.-]+|requirements[\w.-]*\.txt)$`)
	reLockFile   = regexp.MustCompile(`(?i)(^|/)[^/]*[-.]lock\.(ya?ml|json)$`)
	reGenerated  = regexp.MustCompile(`(?i)(\bcode generated\b|@generated\b|\bauto-?generated\b|\bdo not edit\b)`)
	reLicense    = regexp.MustCompile(`(?i)\b(copyright|spdx-license-identifier|licen[cs]ed under|all rights reserved|license)\b`)
	rePyPrefix   = regexp.MustCompile(`^[rRuUbBfF]{0,2}$`)
	reCommentDir = regexp.MustCompile(`^(?:` + strings.Join([]string{
		`eslint(?:-disable(?:-next-line|-line)?|-enable)?(?:\s|$)`, `eslint-env\s`, `globals?\s`, `exported\s`, `jshint\s`,
		`prettier-ignore`, `biome-ignore`, `deno-(?:lint|fmt)-ignore`, `stylelint-(?:disable|enable)`, `tslint:`, `svelte-ignore\s`,
		`@ts-(?:ignore|expect-error|nocheck|check)\b`, `@flow\b`, `@jsx\w*\s`, `@(?:vitest|jest)-environment\s`, `@refresh\s`,
		`@vite-ignore`, `webpack\w+:`, `(?:istanbul|c8|v8) ignore`, `@license\b`, `@preserve\b`, `@format\b`, `@prettier\b`,
		`<(?:reference|amd-module|amd-dependency)\s`, `go:\w+`, `\+build\s`, `nolint\b`, `lint:(?:ignore|file-ignore)\s`,
		`line \S+:\d+`, `NOLINT`, `clang-format (?:off|on)`, `swiftlint:`, `swift-format-ignore`, `rubocop:`,
		`frozen_string_literal:`, `-\*-.*coding`, `(?:en)?coding[:=]`, `type:\s*ignore`, `noqa\b`, `pylint:`, `mypy:`,
		`pyright:`, `fmt:\s*(?:off|on|skip)`, `isort:`, `nosec\b`, `pragma:?\s`, `pyre-`, `shellcheck\s`, `yamllint\s`,
		`syntax=`, `escape=`, `check=`, `\+migrate\s`, `migrate:(?:up|down)`, `sqlfluff`, `@formatter:`, `noinspection\s`,
		`ReSharper\s`, `NOSONAR`, `SPDX-License-Identifier:`, `vim?:\s`, `@generated`, `Code generated .* DO NOT EDIT`,
	}, "|") + `)`)
)

func commentSyntaxFor(rel string) *commentSyntax {
	rel = filepath.ToSlash(rel)
	if reLockFile.MatchString(rel) {
		return nil
	}
	base := strings.ToLower(path.Base(rel))
	if reHashNames.MatchString(base) {
		return synHash
	}
	if strings.HasSuffix(base, ".dockerfile") {
		return synHash
	}
	return commentExts[strings.TrimPrefix(path.Ext(base), ".")]
}

func generatedSource(src string) bool {
	if len(src) > 1024 {
		src = src[:1024]
	}
	return reGenerated.MatchString(src)
}

func (s *commentSyntax) blockAt(src string, i int) (string, string, bool) {
	for _, b := range s.block {
		if strings.HasPrefix(src[i:], b[0]) {
			return b[0], b[1], true
		}
	}
	return "", "", false
}

func (s *commentSyntax) lineAt(src string, i int) (string, bool) {
	for _, m := range s.line {
		if !strings.HasPrefix(src[i:], m) {
			continue
		}
		switch m {
		case "#", ";":
			if i > 0 && !strings.ContainsRune(" \t\n", rune(src[i-1])) {
				continue
			}
			if m == "#" && s.hashAttr && strings.HasPrefix(src[i:], "#[") {
				continue
			}
		case "//":
			if i > 0 && src[i-1] == ':' {
				continue
			}
		}
		return m, true
	}
	return "", false
}

func closeOnLine(src string, i int) int {
	q := src[i]
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '\n':
			return -1
		case q:
			return j + 1
		}
	}
	return -1
}

func closeAnywhere(src string, i int, q string, escapes bool) int {
	for j := i + len(q); j < len(src); j++ {
		if escapes && src[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(src[j:], q) {
			return j + len(q)
		}
	}
	return len(src)
}

func commentText(body string) (string, string) {
	var parts []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimLeft(strings.TrimSpace(l), "*/! \t")
		if l != "" {
			parts = append(parts, l)
		}
	}
	if len(parts) == 0 {
		return "", ""
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " "), strings.Join(strings.Fields(parts[0]), " ")
}

func scanComments(syn *commentSyntax, src string) []codeComment {
	var out []codeComment
	n := len(src)
	i, line, lineStart := 0, 1, 0
	seenCode := false
	var last, lineLast byte
	if strings.HasPrefix(src, "#!") {
		if j := strings.IndexByte(src, '\n'); j >= 0 {
			i = j
		} else {
			i = n
		}
	}
	jump := func(to int) {
		if k := strings.Count(src[i:to], "\n"); k > 0 {
			line += k
			lineStart = i + strings.LastIndexByte(src[i:to], '\n') + 1
			lineLast = last
		}
		i = to
	}
	code := func(c byte) {
		seenCode, last = true, c
	}
	add := func(body string, at int, lead bool) {
		if t, first := commentText(body); t != "" {
			out = append(out, codeComment{Text: t, First: first, Line: at, lead: lead})
		}
	}
	for i < n {
		ch := src[i]
		switch ch {
		case '\n':
			jump(i + 1)
			continue
		case ' ', '\t', '\r':
			i++
			continue
		}
		if open, end, ok := syn.blockAt(src, i); ok {
			at, lead := line, !seenCode
			stop := strings.Index(src[i+len(open):], end)
			next := n
			body := src[i+len(open):]
			if stop >= 0 {
				body = body[:stop]
				next = i + len(open) + stop + len(end)
			}
			add(body, at, lead)
			jump(next)
			continue
		}
		if m, ok := syn.lineAt(src, i); ok {
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				end = n - i
			}
			add(src[i+len(m):i+end], line, !seenCode)
			i += end
			continue
		}
		if syn.py && (strings.HasPrefix(src[i:], `"""`) || strings.HasPrefix(src[i:], `'''`)) {
			at, lead := line, !seenCode
			doc := rePyPrefix.MatchString(strings.TrimSpace(src[lineStart:i])) && (lineLast == ':' || lineLast == 0)
			next := closeAnywhere(src, i, src[i:i+3], true)
			if doc {
				add(strings.TrimSuffix(src[i+3:next], src[i:i+3]), at, lead)
			}
			jump(next)
			code('"')
			continue
		}
		if syn.tripleDQ && strings.HasPrefix(src[i:], `"""`) {
			jump(closeAnywhere(src, i, `"""`, true))
			code('"')
			continue
		}
		if syn.backtick && ch == '`' {
			jump(closeAnywhere(src, i, "`", !syn.rawTick))
			code('`')
			continue
		}
		if ch == '"' || ch == '\'' {
			if j := closeOnLine(src, i); j > 0 {
				i = j
				code(ch)
				continue
			}
		}
		code(ch)
		i++
	}
	return out
}

func bannedComments(syn *commentSyntax, src string) []codeComment {
	all := scanComments(syn, src)
	license := false
	for _, c := range all {
		if c.lead && reLicense.MatchString(c.Text) {
			license = true
		}
	}
	var out []codeComment
	for _, c := range all {
		if reCommentDir.MatchString(c.Text) || (c.lead && license) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func addedComments(syn *commentSyntax, before, after string) []codeComment {
	if syn == nil {
		return nil
	}
	have := map[string]int{}
	for _, c := range scanComments(syn, before) {
		have[c.Text]++
	}
	var out []codeComment
	for _, c := range bannedComments(syn, after) {
		if have[c.Text] > 0 {
			have[c.Text]--
			continue
		}
		out = append(out, c)
	}
	return out
}

func movedOut(root, base string, found []codeComment) []codeComment {
	var out []codeComment
	for i, c := range found {
		if i < 20 && c.First != "" {
			if _, err := gitOut(root, "grep", "-q", "-E", "-I", "-e", movedPattern(c.First), base, "--"); err == nil {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func movedPattern(first string) string {
	words := strings.Fields(first)
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	return `(//|#|--|/\*|\*|<!--|;|"""|'''|\{-|<#)[[:space:]/*!]*` + strings.Join(words, `[[:space:]]+`) + `[[:space:]]*(\*/|-->|-\}|\]\]|#>|"""|''')?[[:space:]]*$`
}

func taskBase(t *Task) string {
	if t == nil || t.StartCommit == "" {
		return "HEAD"
	}
	return t.StartCommit
}

func readSmall(p string) (string, bool) {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() || fi.Size() >= 2<<20 {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func applyEdit(text, old, new string, all bool) (string, bool) {
	if old == "" || !strings.Contains(text, old) {
		return "", false
	}
	if all {
		return strings.ReplaceAll(text, old, new), true
	}
	return strings.Replace(text, old, new, 1), true
}

func editTexts(tool string, input map[string]any, abs string) (before, after string, whole bool) {
	str := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	current, exists := readSmall(abs)
	switch tool {
	case "Write":
		return current, str(input, "content"), true
	case "Edit":
		if !exists {
			return "", str(input, "new_string"), true
		}
		all, _ := input["replace_all"].(bool)
		if out, ok := applyEdit(current, str(input, "old_string"), str(input, "new_string"), all); ok {
			return current, out, true
		}
		return str(input, "old_string"), str(input, "new_string"), false
	case "MultiEdit":
		edits, _ := input["edits"].([]any)
		text, full := current, exists
		var olds, news []string
		for _, e := range edits {
			m, _ := e.(map[string]any)
			olds, news = append(olds, str(m, "old_string")), append(news, str(m, "new_string"))
			all, _ := m["replace_all"].(bool)
			if !full {
				continue
			}
			if out, ok := applyEdit(text, str(m, "old_string"), str(m, "new_string"), all); ok {
				text = out
			} else {
				full = false
			}
		}
		if full {
			return current, text, true
		}
		return strings.Join(olds, "\n"), strings.Join(news, "\n"), false
	}
	return "", "", false
}

func notebookTexts(input map[string]any, abs string) (*commentSyntax, string, string) {
	src, _ := input["new_source"].(string)
	if ct, _ := input["cell_type"].(string); ct == "markdown" || src == "" {
		return nil, "", ""
	}
	if mode, _ := input["edit_mode"].(string); mode == "delete" {
		return nil, "", ""
	}
	var nb struct {
		Metadata struct {
			Lang struct {
				Name string `json:"name"`
			} `json:"language_info"`
		} `json:"metadata"`
		Cells []struct {
			Type   string          `json:"cell_type"`
			Source json.RawMessage `json:"source"`
		} `json:"cells"`
	}
	if text, ok := readSmall(abs); ok {
		_ = json.Unmarshal([]byte(text), &nb)
	}
	ext := map[string]string{"r": "r", "julia": "jl", "javascript": "js", "typescript": "ts", "ruby": "rb", "bash": "sh"}[strings.ToLower(nb.Metadata.Lang.Name)]
	syn := commentExts[ext]
	if syn == nil {
		syn = synPy
	}
	var cells []string
	for _, c := range nb.Cells {
		if c.Type != "code" {
			continue
		}
		var one string
		var many []string
		if json.Unmarshal(c.Source, &one) != nil && json.Unmarshal(c.Source, &many) == nil {
			one = strings.Join(many, "")
		}
		cells = append(cells, one)
	}
	return syn, strings.Join(cells, "\n"), src
}

func (c *hookCtx) gateComments(abs string) Output {
	rel := c.rel(abs)
	var found []codeComment
	whole := false
	if c.in.ToolName == "NotebookEdit" {
		syn, before, after := notebookTexts(c.in.Input(), abs)
		found = addedComments(syn, before, after)
	} else {
		syn := commentSyntaxFor(rel)
		if syn == nil {
			return nil
		}
		var before, after string
		before, after, whole = editTexts(c.in.ToolName, c.in.Input(), abs)
		if !generatedSource(before) {
			found = addedComments(syn, before, after)
		}
	}
	if len(found) > 0 && c.root != "" {
		found = movedOut(c.root, taskBase(c.task), found)
	}
	if len(found) == 0 {
		return nil
	}
	var shown []string
	for _, f := range found {
		where := rel
		if whole {
			where = fmt.Sprintf("%s:%d", rel, f.Line)
		}
		shown = append(shown, fmt.Sprintf("%s %q", where, limit(f.Text, 60)))
	}
	return c.deny("no-comments", fmt.Sprintf("no code comments: this edit adds %s. Leave them out and say why in the report or the commit message instead. Allowed: tool directives (`// eslint-disable-next-line`, `// @ts-expect-error`, `//go:build`, `# type: ignore`), shebangs and license headers. Existing comments may stay or go; a reworded one counts as new.", listSome(shown, 3)))
}

func commentsMissing(t *Task, changes []fileDiff, report string) []string {
	listed := labeledLines(report, "comments kept")
	found := newCommentLines(t.Root, taskBase(t), changes, func(p string) bool { return strings.Contains(listed, p) })
	if len(found) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(`comments: the diff adds code comments; remove them and say why in the report or the commit message instead. Only for ones the user wrote or a tool generated, name the file on the "Comments kept:" line under ## Proof: %s`, listSome(found, 8))}
}

func newCommentLines(root, base string, changes []fileDiff, kept func(string) bool) []string {
	var found []string
	for _, f := range changes {
		syn := commentSyntaxFor(f.Path)
		if f.Deleted || syn == nil || kept(f.Path) {
			continue
		}
		after, ok := readSmall(filepath.Join(root, filepath.FromSlash(f.Path)))
		if !ok || generatedSource(after) {
			continue
		}
		before := ""
		if f.OldPath != "" {
			before, _ = gitOut(root, "show", base+":"+f.OldPath)
		}
		for _, cm := range movedOut(root, base, addedComments(syn, before, after)) {
			found = append(found, fmt.Sprintf("%s:%d", f.Path, cm.Line))
		}
	}
	return found
}

func NewCommentsSince(root, base string) ([]string, error) {
	changes, err := changesSince(root, base, func(p string) bool { return p != "" })
	if err != nil {
		return nil, err
	}
	return newCommentLines(root, base, changes, func(string) bool { return false }), nil
}
