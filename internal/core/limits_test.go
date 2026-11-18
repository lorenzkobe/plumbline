package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Release 1 of the limits pass (docs/known-gaps.md): one test per fixed item.

// G9: macOS's disk ignores case, so GIT runs git. Name-based rules must too.
func TestCommandNamesIgnoreCase(t *testing.T) {
	h := newHarness(t)
	h.expect(h.bash("GIT push origin main"), "deny", "pushing")
	h.expect(h.bash("/usr/bin/Git push"), "deny", "pushing")
	h.expect(h.bash("CLAUDE plugin disable plumbline"), "deny", "claude plugin")
	for cmd, want := range map[string]string{
		"VERCEL --prod":       OpsDeploy,
		"Terraform apply":     OpsDeploy,
		"KUBECTL delete ns x": OpsDestroy,
	} {
		if got := OpsKind(ParseShell(cmd, "/tmp")[0]); got != want {
			t.Errorf("OpsKind(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// G7: two identical refusals in one message (parallel calls, hooks fired at
// the same moment) are one attempt, not "twice in a row": Claude hadn't seen
// the first refusal yet.
func TestSameBlockInOneMessageIsNotARepeat(t *testing.T) {
	h := groundedTask(t, "normal")
	at := time.Unix(1_800_000_000, 0)
	blockNow = func() time.Time { return at }
	a := map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}
	for i := 0; i < 2; i++ {
		if _, r := decision(h.pre("Edit", a)); strings.Contains(r, "second time") {
			t.Fatalf("call %d in the same message counted as a repeat: %s", i+1, r)
		}
	}
	at = at.Add(sameMessageWindow) // the next message
	h.expect(h.pre("Edit", a), "deny", "second time in a row")
}

// G8: files xargs adds could be named like flags (-i, --output=x), and GNU
// tools read a later flag as one. A command that isn't a pure reader only
// reads when "--" ends its options.
func TestXargsAddedArgsNeedDoubleDash(t *testing.T) {
	h := newHarness(t)
	for cmd, want := range map[string]string{
		"rg -l x -g '*.go' | xargs sed -n 1p":         "", // not a read: Claude Code's mode decides
		"rg -l x -g '*.go' | xargs sed -n 1p --":      "allow",
		"git ls-files | xargs git log --oneline":      "",
		"git ls-files | xargs git log --oneline --":   "allow",
		"rg -l x -g '*.go' | xargs sort":              "",
		"rg -l x -g '*.go' | xargs sort --":           "allow",
		"rg -l x -g '*.go' | xargs cat":               "allow", // cat has no flag that writes
		"rg -l x | xargs cat":                         "ask",   // G11: the listing could name .env or a key
		"rg -l x -g '*.go' | xargs wc -l":             "allow",
		"rg -l x -g '*.go' | xargs -I{} sed -n 1p {}": "", // {} isn't known either
	} {
		if got, r := decision(h.bash(cmd)); got != want {
			t.Errorf("%q: %q (%s), want %q", cmd, got, r, want)
		}
	}
	// The refusal before approval says how to fix it.
	h.expect(h.bash("rg -l x | xargs sed -n 1p"), "ask", "could include .env")
	g := groundedTask(t, "normal")
	g.expect(g.bash("rg -l x | xargs sed -n 1p"), "deny", `end the command with "--"`)
	if _, r := decision(g.bash("rg -l x | xargs rm")); strings.Contains(r, `"--"`) {
		t.Errorf("no hint when -- wouldn't help: %s", r)
	}
}

// G2: a git read command's argument that isn't known yet could expand to a
// flag that writes (--output=x) or changes (git branch -D). It reads only
// when it can't be a flag.
func TestGitUnknownArgsMustNotBeFlags(t *testing.T) {
	for cmd, want := range map[string]bool{
		`git diff $(git merge-base HEAD main)`:            true,
		`git diff "$(git merge-base origin/main HEAD)"`:   true,
		`git log $(git rev-parse --abbrev-ref HEAD)`:      true,
		`git show HEAD~$n`:                                true,
		`git log -- "$f"`:                                 true,
		`git diff $X`:                                     false,
		`git diff "$X"`:                                   false,
		`git log $(echo --output=x)`:                      false,
		`git log $(git rev-parse --output=x)`:             false, // rev-parse prints flags it doesn't know
		`git log $(git rev-parse -- --output=x)`:          false,
		`git log \-$X`:                                    false,
		`git branch $X main`:                              false,
		`git config $X`:                                   false,
		`git diff $(git merge-base HEAD main) > /tmp/o`:   false, // the redirect writes
		`git diff "$(git merge-base HEAD main 2>/tmp/e)"`: false,
	} {
		got := true
		for _, sg := range ParseShell(cmd, "/tmp") {
			got = got && ReadOnly(sg)
		}
		if got != want {
			t.Errorf("ReadOnly(%q) = %v, want %v", cmd, got, want)
		}
	}
}

// G10: a setup changed since the user confirmed it can't drop a risky path or
// widen what the changed-files check skips: risky paths are the union with
// the confirmed copy, skipped paths only the ones both agree on.
func TestUnconfirmedSetupCantRelaxPaths(t *testing.T) {
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "./test.sh"
	s.RiskyPaths = []string{"billing/**"}
	s.IgnoreChanges = []string{"coverage/**"}
	p, err := SaveSetup(h.repo, s)
	must(t, err)
	if got, ok, _ := LoadSetup(h.repo); !ok || len(got.RiskyPaths) != 1 || len(got.IgnoreChanges) != 1 {
		t.Fatalf("confirmed setup: %+v %v", got, ok)
	}
	// a terminal edit: drops the risky path, skips src too
	write(t, p, `{"version":1,"kind":"personal","base_branch":"main","commands":{"test":"./test.sh"},"risky_paths":["ops/**"],"ignore_changes":["coverage/**","src/**"]}`)
	got, ok, err := LoadSetup(h.repo)
	must(t, err)
	if ok {
		t.Fatal("an edited setup isn't confirmed")
	}
	if !MatchAny(got.RiskyPaths, "billing/pay.go") || !MatchAny(got.RiskyPaths, "ops/run.sh") {
		t.Errorf("risky paths %v should keep billing/** and add ops/**", got.RiskyPaths)
	}
	if MatchAny(got.IgnoreChanges, "src/a.go") || !MatchAny(got.IgnoreChanges, "coverage/x") {
		t.Errorf("ignore_changes %v should be only coverage/**", got.IgnoreChanges)
	}
	// No confirmed copy (a setup confirmed before 0.6.1): nothing is skipped.
	must(t, os.Remove(confirmedPath(h.repo)+".json"))
	got, _, _ = LoadSetup(h.repo)
	if len(got.IgnoreChanges) != 0 {
		t.Errorf("without a confirmed copy nothing is skipped: %v", got.IgnoreChanges)
	}
	// Confirming again makes the edit the new truth.
	must(t, ConfirmSetup(h.repo))
	got, ok, _ = LoadSetup(h.repo)
	if !ok || MatchAny(got.RiskyPaths, "billing/x") || !MatchAny(got.IgnoreChanges, "src/a.go") {
		t.Errorf("after confirming: %+v %v", got, ok)
	}
}

// L3: logs keep rule names and counts, not commit subjects, plan text or
// paths that name the user.
func TestLogsDropQuotedTextAndPaths(t *testing.T) {
	h := groundedTask(t, "normal")
	home, _ := os.UserHomeDir()
	c := &hookCtx{root: h.repo}
	c.log("ask", "commit", `commit "fix: refund for ACME-42 customer" (final).`)
	c.log("block", "plan", "case \"refund the Smith order\" needs a label; see `src/pay.go` and "+filepath.Join(h.repo, "src", "a.go")+" and "+filepath.Join(home, "notes.md"))
	var all strings.Builder
	files, _ := filepath.Glob(filepath.Join(logsDir(), "*", "*.jsonl"))
	for _, f := range files {
		b, _ := os.ReadFile(f)
		all.Write(b)
	}
	got := all.String()
	for _, leak := range []string{"ACME-42", "Smith", "src/pay.go", h.repo, home} {
		if strings.Contains(got, leak) {
			t.Errorf("log holds %q:\n%s", leak, got)
		}
	}
	for _, keep := range []string{`"rule":"commit"`, "needs a label", "src/a.go", "~/notes.md"} {
		if !strings.Contains(got, keep) {
			t.Errorf("log lost %q:\n%s", keep, got)
		}
	}
}

// B9: reviewers and the challenger run without the user too, so a call that
// would ask the user (reading .env) is refused for the lead instead.
func TestReviewersNeverAskTheUser(t *testing.T) {
	h := provenTask(t)
	write(t, filepath.Join(h.repo, ".env"), "KEY=1\n")
	id, _ := h.startHelper(HelperCodeReviewer)
	hi := func(tool string, input any) Output {
		return PreToolUse(h.helperIn(id, HelperCodeReviewer, tool, input))
	}
	h.expect(hi("Read", map[string]any{"file_path": filepath.Join(h.repo, ".env")}), "deny", "runs without the user")
	h.expect(hi("Bash", map[string]any{"command": "cat .env"}), "deny", "Put it in your findings")
	h.expect(hi("Read", map[string]any{"file_path": filepath.Join(h.repo, "src", "a.go")}), "", "")
}

// B10: a builder may link to the system's tools (/usr/bin/node), never to
// the home folder, another repo or the root.
func TestBuilderLinksToSystemTools(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	bash := func(cmd string) Output {
		return h.hi(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && " + cmd})
	}
	for _, c := range []string{"ln -s /usr/bin/node node", "ln -sf /opt/homebrew/bin/python3 bin/py", "ln -s /bin/sh sh"} {
		h.expect(bash(c), "", "")
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{"ln -s / r", "ln -s /usr/../" + strings.TrimPrefix(home, "/") + " h", "ln -s " + home + " h", "ln -s /usr/bin/../../" + strings.TrimPrefix(h.repo, "/") + " m"} {
		h.expect(bash(c), "deny", "outside your copy")
	}
}

// B4: a builder's write whose target can't be read is refused up front.
func TestBuilderUnknownWriteTargetsRefused(t *testing.T) {
	h := buildingTask(t)
	id, _ := h.startHelper(HelperBuilder)
	wt := h.run(HelperBuilder, id).Worktree
	bash := func(cmd string) Output {
		return h.hi(id, HelperBuilder, "Bash", map[string]any{"command": "cd " + wt + " && " + cmd})
	}
	for _, c := range []string{`rm "$x"`, `rm -rf $DIR/build`, `mv src/a.go "$T"`, `touch $(cat list)`, `echo x > "$F"`, `sed -i '' s/a/b/ "$f"`} {
		if d, r := decision(bash(c)); d != "deny" {
			t.Errorf("%q: %q (%s), want deny", c, d, r)
		}
	}
	h.expect(bash(`rm src/old.go`), "", "")
}

// D2: "## Design" must open with the direction the user picked, so a line
// that only mentions it ("not Direction B") doesn't count as naming it.
func TestDesignSectionOpensWithThePick(t *testing.T) {
	h := designTaskH(t)
	h.docs(DocDesign, designDoc)
	h.pick(directionQ("Direction A: bold", "Direction B: calm", "Direction C: dense"), "Direction B: calm")
	base := strings.TrimSuffix(designPlan, "## Design\nDirection B, mockup https://claude.ai/artifact/b\n")
	for _, design := range []string{
		"Direction A, not Direction B, mockup https://claude.ai/artifact/a\n",
		"We dropped Direction B for now.\n",
	} {
		h.docs(DocPlan, base+"## Design\n"+design)
		h.expect(h.pre("AskUserQuestion", checkpointQ()), "deny", "must start with the direction the user picked")
	}
	for _, design := range []string{
		"- **Direction B** (https://claude.ai/artifact/b): calm and editorial.\n",
		"direction b, mockup https://claude.ai/artifact/b\nNot Direction A, which was too loud.\n",
	} {
		h.docs(DocPlan, base+"## Design\n"+design)
		if d, r := decision(h.pre("AskUserQuestion", checkpointQ())); d == "deny" && strings.Contains(r, "Design") {
			t.Errorf("%q: %s", design, r)
		}
	}
}

// P6: a hook binary that is there but can't run (killed at start, refused by
// the system, crashing outside Go) is logged like a missing one, and exits 1
// (2 would block every prompt). Plumbline's own codes 0 and 2 pass through.
func TestLauncherLogsABinaryThatCantRun(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("the launcher is for macOS and Linux")
	}
	src, err := filepath.Abs("../../bin/plumbline")
	must(t, err)
	dir := t.TempDir()
	b, err := os.ReadFile(src)
	must(t, err)
	launcher := filepath.Join(dir, "bin", "plumbline")
	write(t, launcher, string(b))
	must(t, os.Chmod(launcher, 0o755))
	bin := filepath.Join(dir, "dist", "plumbline-"+runtime.GOOS+"-"+runtime.GOARCH)
	home := filepath.Join(dir, "home")
	runIt := func(body string, args ...string) (int, string) {
		write(t, bin, "#!/bin/sh\n"+body+"\n")
		must(t, os.Chmod(bin, 0o755))
		_ = os.RemoveAll(home)
		cmd := exec.Command(launcher, args...)
		cmd.Env = append(os.Environ(), "PLUMBLINE_HOME="+home)
		cmd.Stdin = strings.NewReader(`{"tool_name":"Bash"}`)
		out, _ := cmd.CombinedOutput()
		logged, _ := os.ReadFile(filepath.Join(home, "errors.log"))
		return cmd.ProcessState.ExitCode(), string(out) + string(logged)
	}
	for _, body := range []string{"kill -9 $$", "exit 126", "exit 139"} {
		code, out := runIt(body, "hook", "pre")
		if code != 1 || !strings.Contains(out, "launcher: the Plumbline binary couldn't run") {
			t.Errorf("%q: exit %d, %s", body, code, out)
		}
	}
	for body, want := range map[string]int{"cat >/dev/null; echo '{}'": 0, "exit 2": 2} {
		if code, out := runIt(body, "hook", "pre"); code != want || strings.Contains(out, "launcher:") {
			t.Errorf("%q: exit %d, want %d, %s", body, code, want, out)
		}
	}
	// Not a hook: the command's own exit code, unlogged (plumbline run test
	// passes the test suite's code through).
	if code, out := runIt("exit 127", "run", "test"); code != 127 || strings.Contains(out, "launcher:") {
		t.Errorf("run: exit %d, %s", code, out)
	}
	// Input still reaches the binary.
	if _, out := runIt(`cat`, "hook", "pre"); !strings.Contains(out, `"tool_name":"Bash"`) {
		t.Errorf("stdin didn't reach the binary: %s", out)
	}
}

// G5: a Monitor command gets the changed-files check after it, like Bash.
func TestMonitorGetsTheChangedFilesCheck(t *testing.T) {
	h := groundedTask(t, "normal")                                      // before approval
	h.post("Bash", map[string]any{"command": "true"}, map[string]any{}) // snapshot
	write(t, filepath.Join(h.repo, "src", "b.go"), "package src\n\nfunc B() int { return 3 }\n")
	o := h.post("Monitor", map[string]any{"command": "npm run gen -- --watch", "description": "watch"}, map[string]any{})
	if o["continue"] != false || !strings.Contains(fmtOut(o), "src/b.go") {
		t.Fatalf("a Monitor command that changed files before approval should stop the work: %v", o)
	}
	// Its WebSocket form runs no command, so it changes nothing.
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 9 }\n")
	if o := h.post("Monitor", map[string]any{"url": "ws://localhost:9/x"}, map[string]any{}); o != nil {
		t.Fatalf("a WebSocket Monitor runs no command: %v", o)
	}
}

// L1: npx runs the project's own copy when node_modules/.bin has it, so
// that's no install. Without it, or with -p/--package, it downloads one.
func TestNpxOfAProjectToolIsNoInstall(t *testing.T) {
	h := buildingTask(t)
	bin := filepath.Join(h.repo, "web", "node_modules", ".bin")
	write(t, filepath.Join(bin, "tsc"), "#!/bin/sh\n")
	write(t, filepath.Join(h.repo, "node_modules", ".bin", "eslint"), "#!/bin/sh\n")
	web := filepath.Join(h.repo, "web")
	for cmd, want := range map[string]string{
		"cd " + web + " && npx tsc --noEmit":             "",
		"cd " + web + " && npx --yes tsc --noEmit":       "",
		"cd " + web + " && npx eslint src":               "", // found further up, inside the repo
		"cd " + web + " && bunx tsc -p .":                "",
		"cd " + web + " && npx --no-install prettier .":  "",
		"cd " + web + " && npx prettier --check .":       "deny",
		"cd " + web + " && npx -p typescript tsc":        "deny",
		"cd " + web + " && npx --package=typescript tsc": "deny",
		"npx tsc --noEmit":                               "deny", // the repo root has no tsc
	} {
		d, r := decision(h.bash(cmd))
		if want == "deny" && (d != "deny" || !strings.Contains(r, "installs a package")) || want == "" && strings.Contains(r, "installs a package") {
			t.Errorf("%q: %q (%s)", cmd, d, r)
		}
	}
}

// G12 (found while fixing G8): an unquoted glob can expand to a file named
// like a flag (sed -n 1p * with a file "-i" there). It reads only when no
// match there starts with "-", or after "--". Pure readers (cat) stay fine.
func TestGlobsThatExpandToFlags(t *testing.T) {
	bad := t.TempDir()
	write(t, filepath.Join(bad, "-i"), "")
	write(t, filepath.Join(bad, "a.txt"), "")
	write(t, filepath.Join(bad, "src", "-i"), "")
	good := t.TempDir()
	write(t, filepath.Join(good, "a.txt"), "")
	for _, c := range []struct {
		cmd, dir string
		want     bool
	}{
		{"sed -n 1p *", bad, false},
		{"sort *", bad, false},
		{"git log *", bad, false},
		{"sed -n 1p ?i", bad, false},
		{"sed -n 1p {-i,a.txt}", good, false}, // braces write the flag out
		{"sed -n 1p -- *", bad, true},
		{"git log -- *", bad, true},
		{"sed -n 1p src/*", bad, true}, // expands to src/-i
		{"cat *", bad, true},
		{"sed -n 1p *", good, true},
		{"git diff *.txt", good, true},
	} {
		got := true
		for _, s := range ParseShell(c.cmd, c.dir) {
			got = got && ReadOnly(s)
		}
		if got != c.want {
			t.Errorf("ReadOnly(%q in %s) = %v, want %v", c.cmd, filepath.Base(c.dir), got, c.want)
		}
	}
}
