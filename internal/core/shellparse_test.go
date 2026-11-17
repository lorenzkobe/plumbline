package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func names(segs []Segment) string {
	var out []string
	for _, s := range segs {
		n := s.Name()
		if s.Opaque != "" {
			n = "?" + n
		}
		out = append(out, n)
	}
	return strings.Join(out, ",")
}

func TestParseShellFindsEveryCommand(t *testing.T) {
	cases := map[string]string{
		"a | b && c; d &":                          "a,b,c,d",
		"if a; then b; elif c; then d; else e; fi": "a,b,c,d,e",
		"for x in 1 2; do a $x; done":              "a,a",
		"f() { a; }; f":                            "a,f",
		"(a; b) | c":                               "a,b,c",
		"x=$(a $(b)) c":                            "c,a,b",
		"cat <<EOF\n$(a)\nEOF":                     "cat,a",
		"cat <<'EOF'\n$(a)\nEOF":                   "cat",
		"timeout 5 nice -n 2 git push":             "git",
		"env -i A=1 git status":                    "git",
		"sh -c 'a; b' && bash -lc \"c\"":           "a,b,c",
		"eval 'a | b'":                             "a,b",
		"echo x | xargs -0 rm -f":                  "echo,xargs,rm",
		"find . -exec a {} \\; -exec b {} +":       "find,a,b",
		"$X y":                                     "?$X",
		"echo x | sh":                              "echo,?sh",
		"if [":                                     "?",
		"case $x in a) b;; esac":                   "b",
	}
	for cmd, want := range cases {
		if got := names(ParseShell(cmd, "/tmp")); got != want {
			t.Errorf("%q: got %s, want %s", cmd, got, want)
		}
	}
}

func TestParseShellWordsAndRedirects(t *testing.T) {
	s := ParseShell(`FOO=1 git -C "x y" log 'a b' c\ d >out 2>&1 >&f2 1<>f3 <in`, "/w")[0]
	if strings.Join(s.Words, "|") != "git|-C|x y|log|a b|c d" {
		t.Errorf("words: %q", s.Words)
	}
	if strings.Join(s.Redirects, "|") != "out|f2|f3" || strings.Join(s.Inputs, "|") != "in" || s.Env[0] != "FOO=1" {
		t.Errorf("redirects %q inputs %q env %q", s.Redirects, s.Inputs, s.Env)
	}
	h, _ := os.UserHomeDir()
	s = ParseShell(`cat ~/a "$HOME/b" "$X/c" src/*.go`, "/w")[0]
	if s.Words[1] != h+"/a" || s.Words[2] != h+"/b" || !s.Dyn[3] || !s.Glob[4] {
		t.Errorf("expansions: %q dyn %v glob %v", s.Words, s.Dyn, s.Glob)
	}
	// cd moves where later commands run; a subshell's cd doesn't last.
	segs := ParseShell("cd sub && touch a; (cd /x; touch b); touch c; cd $D; touch d", "/w")
	dirs := []string{}
	for _, s := range segs {
		if s.Name() == "touch" {
			dirs = append(dirs, s.Dir)
		}
	}
	if strings.Join(dirs, "|") != "/w/sub|/x|/w/sub|" {
		t.Errorf("dirs: %q", dirs)
	}
}

func TestSedScripts(t *testing.T) {
	ro := []string{"p", "1,20p", "/a/,/b/p", "s/a/b/g", "s#a/b#c#2p", "$d", "1~2p", "y/ab/cd/", "/x/!d", "s/a\\/b/c/", "=;q", "{p;d}", "/w/p", "s/w/e/"}
	for _, sc := range ro {
		if !sedScriptReadOnly(sc) {
			t.Errorf("%q should be read-only", sc)
		}
	}
	rw := []string{"w out", "1w out", "s/a/b/w out", "s/a/b/e", "e ls", "/x/W out", "s/a/b", "p;w x", "/a/{w x\n}"}
	for _, sc := range rw {
		if sedScriptReadOnly(sc) {
			t.Errorf("%q should not be read-only", sc)
		}
	}
}

func TestParseCommitOptions(t *testing.T) {
	get := func(cmd string) CommitInfo {
		for _, s := range ParseShell(cmd, "/w") {
			if sub, _ := GitSub(s); sub == "commit" {
				return ParseCommit(s)
			}
		}
		t.Fatalf("no commit in %q", cmd)
		return CommitInfo{}
	}
	if ci := get(`git commit -anm "fix: x"`); !ci.NoVerify || !ci.All || ci.Message != "fix: x" {
		t.Errorf("-anm: %+v", ci)
	}
	if ci := get(`git commit -uno -m "fix: x"`); ci.NoVerify || ci.Message != "fix: x" {
		t.Errorf("-uno: %+v", ci)
	}
	if ci := get(`git commit --no-verif --mess="fix: x"`); !ci.NoVerify || ci.Message != "fix: x" {
		t.Errorf("prefixes: %+v", ci)
	}
	if ci := get(`git commit --no-ver -m "fix: x"`); ci.NoVerify {
		t.Errorf("--no-ver is ambiguous: %+v", ci)
	}
	if ci := get(`git commit -mfix:x src/a.go -- b.go`); ci.Message != "fix:x" || strings.Join(ci.Paths, ",") != "src/a.go,b.go" {
		t.Errorf("paths: %+v", ci)
	}
	if ci := get("git commit -m \"$(cat <<'EOF'\nfix: x\n\nbody\nEOF\n)\""); ci.Message != "fix: x\n\nbody" || ci.MsgBad != "" {
		t.Errorf("heredoc: %+v", ci)
	}
	if ci := get(`git commit -m "fix" --trailer "Co-authored-by: Claude"`); !strings.Contains(ci.Message, "Co-authored-by") {
		t.Errorf("trailer: %+v", ci)
	}
	if ci := get(`git commit $FLAGS -m x`); ci.MsgBad == "" {
		t.Errorf("unknown option should be refused: %+v", ci)
	}
	if ci := get(`git commit -C HEAD`); ci.MsgBad == "" {
		t.Errorf("reused message should be refused: %+v", ci)
	}
	if _, ok := ParseCommitMessage("git commit", "/"); ok {
		t.Error("editor commit has no message")
	}
}

func TestWriteTargets(t *testing.T) {
	dir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(dir, "d"), 0o755))
	write(t, filepath.Join(dir, "a.go"), "x")
	write(t, filepath.Join(dir, "b.go"), "x")
	cases := map[string]string{
		"rm -f a.go b.go":           "a.go,b.go",
		"cp a.go d":                 "d/a.go",
		"mv a.go c.go":              "a.go,c.go",
		"chmod 644 a.go":            "a.go",
		"sed -i '' s/a/b/ a.go":     "a.go",
		"sed s/a/b/ a.go":           "",
		"gofmt -w a.go":             "a.go",
		"gofmt -l a.go":             "",
		"echo x > a.go 2>/dev/null": "a.go",
		"dd if=a.go of=c.go":        "c.go",
		"git checkout -- a.go":      "a.go",
		"rm $X":                     "",
	}
	for cmd, want := range cases {
		var got []string
		for _, s := range ParseShell(cmd, dir) {
			got = append(got, WriteTargets(s)...)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
	var globbed []string
	for _, s := range ParseShell("rm *.go", dir) {
		globbed = append(globbed, WriteTargets(s)...)
	}
	if len(globbed) != 2 {
		t.Errorf("glob targets: %q", globbed)
	}
}

func TestMCPKinds(t *testing.T) {
	cases := map[string]string{
		"mcp__serena__replace_symbol_body":         "write",
		"mcp__plugin_serena_serena__rename_symbol": "write",
		"mcp__serena__execute_shell_command":       "shell",
		"mcp__serena__find_symbol":                 "",
		"mcp__serena__search_for_pattern":          "",
		"mcp__filesystem__write_file":              "write",
		"mcp__filesystem__read_text_file":          "",
		"mcp__ide__executeCode":                    "shell",
		"mcp__ide__getDiagnostics":                 "",
		"mcp__mempalace__mempalace_add_drawer":     "",
		"mcp__socraticode__codebase_update":        "",
		"mcp__claude-in-chrome__javascript_tool":   "",
		"mcp__github__create_pull_request":         "write",
		"mcp__atlassian__getJiraIssue":             "",
		"mcp__atlassian__addCommentToJiraIssue":    "write",
	}
	for tool, want := range cases {
		if got := MCPKind(tool); got != want {
			t.Errorf("%s: %q, want %q", tool, got, want)
		}
	}
}

func TestRealCaseAndBraces(t *testing.T) {
	if got := strings.Join(expandBraces("src/{a,b{1,2}}.go"), ","); got != "src/a.go,src/b1.go,src/b2.go" {
		t.Errorf("braces: %s", got)
	}
	if runtime.GOOS != "darwin" {
		return
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	write(t, filepath.Join(dir, "Src", "B.go"), "x")
	if got := realCase(filepath.Join(dir, "SRC", "b.GO")); got != filepath.Join(dir, "Src", "B.go") {
		t.Errorf("realCase: %s", got)
	}
	if got := realCase(filepath.Join(dir, "src", "new.go")); got != filepath.Join(dir, "Src", "new.go") {
		t.Errorf("realCase of a new file: %s", got)
	}
}

func TestTrivialProof(t *testing.T) {
	for _, c := range []string{"true", "echo ok", ":", "exit 0", "sh -c 'true'", "true && echo done"} {
		if !TrivialProof(c) {
			t.Errorf("%q proves nothing", c)
		}
	}
	for _, c := range []string{"curl -sf localhost:3000/api/x", "npx playwright test e2e/a.spec.ts", "psql -h localhost -c 'select 1'"} {
		if TrivialProof(c) {
			t.Errorf("%q is a real proof", c)
		}
	}
}
