package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Release 3a of the limits pass (docs/known-gaps.md): one test per fixed item.

// The developer's own Docker settings must not change what the tests see.
func init() {
	os.Setenv("DOCKER_HOST", "")
	os.Setenv("DOCKER_CONTEXT", "")
	os.Setenv("DOCKER_CONFIG", filepath.Join(os.TempDir(), "plumbline-test-no-docker-config"))
}

// L2: a file printed by a terminal read (cat, sed -n, head, rg on the file)
// counts as opened, like the Read tool. Only output that reaches Claude
// counts: not one piped into wc, sent to a file or captured in a variable.
func TestShellReadsCountAsOpened(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.startTask("normal")
	a := filepath.Join(h.repo, "src", "a.go")
	b := filepath.Join(h.repo, "src", "b.go")
	opened := func(cmd string, agent string) map[string]bool {
		t.Helper()
		tk := h.task()
		tk.Reads = nil
		must(t, SaveTask(tk))
		in := h.in("Bash", map[string]any{"command": cmd}, map[string]any{"stdout": "package src\n"})
		in.AgentID = agent
		PostToolUse(in)
		got := map[string]bool{}
		for p := range h.task().Reads {
			got[p] = true
		}
		return got
	}
	for cmd, want := range map[string][]string{
		"cat src/a.go":                           {a},
		"cat -n src/a.go src/b.go":               {a, b},
		"sed -n 1,3p src/b.go":                   {b},
		"sed -n -e 2p src/a.go":                  {a},
		"cd src && head -n 3 a.go":               {a},
		"tail -5 src/a.go":                       {a},
		"nl -ba src/a.go":                        {a},
		"rg -n Func src/a.go":                    {a},
		"grep -n -A 2 Func src/a.go 2>/dev/null": {a},
		"grep --color -n Func src/a.go":          {a},
		"grep src/b.go src/a.go":                 {a}, // the first word is the pattern
		"grep -e x -- src/b.go":                  {b},
		"awk 'NR<3' src/a.go":                    {a},
		"cat -n src/a.go | head -20":             {a},
		"cat src/a.go | grep -n A | head":        {a},
		"cat src/*.go":                           {a, b},
		// RD3: Plumbline's own bookkeeping on the same line doesn't change
		// what was shown (smoke step 12c).
		"plumbline task tool-down serena --reason x; sed -n 1,5p src/a.go": {a},
		"plumbline status 2>&1 | head -40; cat src/b.go":                   {b},
		"plumbline builder merge 1; cat src/a.go":                          nil, // merge changes files
		"plumbline run format; cat src/a.go":                               nil,
		// Output that doesn't reach Claude, or a command that also changes.
		"cat src/a.go | wc -l":            nil,
		"cat src/a.go | head | wc -l":     nil,
		"cat src/a.go > /dev/null":        nil,
		"cat src/a.go >/tmp/x.txt":        nil,
		"x=$(cat src/a.go); echo ok":      nil,
		"sed -i s/1/2/ src/a.go":          nil,
		"cat src/a.go; echo 2 > src/b.go": nil,
		"rg -n Func src":                  nil, // a folder: which files matched isn't known
		"grep -c Func src/a.go":           nil, // counts only
		"rg -l Func src/a.go":             nil, // names only
		"wc -l src/a.go":                  nil,
		"cat":                             nil,
	} {
		got := opened(cmd, "")
		if len(got) != len(want) {
			t.Errorf("%q opened %v, want %v", cmd, got, want)
			continue
		}
		for _, p := range want {
			if !got[p] {
				t.Errorf("%q didn't open %s (got %v)", cmd, filepath.Base(p), got)
			}
		}
	}
	// A helper's reads aren't the lead's.
	if got := opened("cat src/a.go", "agent-1"); len(got) != 0 {
		t.Errorf("a helper's cat counted for the lead: %v", got)
	}
	// And the notes may now point at it.
	opened("sed -n 1,3p src/a.go", "")
	if p := CheckRefs(h.task(), "A returns 1 (src/a.go:3)"); len(p) != 0 {
		t.Errorf("a file read with sed still counts as not opened: %v", p)
	}
}

// G3: an MCP tool whose name says neither read nor write counts as a write,
// unless the confirmed setup lists it as a read tool of its server. A name
// that says write stays a write whatever the list says.
func TestUnclearMCPToolsCountAsWrites(t *testing.T) {
	for tool, want := range map[string]string{
		"mcp__linear__issue":                       "write",
		"mcp__notion__notion_page":                 "write",
		"mcp__playwright__browser_navigate":        "write",
		"mcp__github__pull_request_read":           "",
		"mcp__sentry__get_issue":                   "",
		"mcp__x__issueStatus":                      "",
		"mcp__github__list_issues":                 "",
		"mcp__github__merge_pull_request":          "write",
		"mcp__mempalace__mempalace_traverse":       "", // a lookup server
		"mcp__claude_ai_Claude_Docs__batch":        "", // Claude Docs is a lookup server
		"mcp__claude_ai_Slack__slack_conversation": "write",
		// Serena, which setup installs: its own tools without a read verb.
		"mcp__serena__activate_project":                         "",
		"mcp__plugin_serena_serena__check_onboarding_performed": "",
		"mcp__serena__think_about_task_adherence":               "",
		"mcp__serena__restart_language_server":                  "write",
		"mcp__serena__onboarding":                               "write", // writes memories
	} {
		if got := MCPKindIn(tool, nil); got != want {
			t.Errorf("MCPKindIn(%s) = %q, want %q", tool, got, want)
		}
	}

	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main", MCPReadTools: map[string][]string{
		"playwright": {"browser_navigate", "browser_snapshot", "browser_click_and_delete"},
	}}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	run(t, h.repo, "git", "add", ".plumbline.json")
	run(t, h.repo, "git", "commit", "-q", "-m", "chore: setup")
	h.startTask("normal")
	// Before approval: an unclear tool is refused, and the refusal says how
	// to list it; a listed one runs; a write name stays refused.
	h.expect(h.pre("mcp__linear__issue", map[string]any{"id": "X-1"}), "deny", "mcp_read_tools")
	h.expect(h.pre("mcp__playwright__browser_navigate", map[string]any{"url": "http://localhost:3000"}), "", "")
	h.expect(h.pre("mcp__playwright__browser_click_and_delete", map[string]any{}), "deny", "")
	h.expect(h.pre("mcp__other_playwright__browser_navigate", map[string]any{}), "deny", "")

	// A list widened outside the confirm prompt isn't trusted.
	write(t, filepath.Join(h.repo, ".plumbline.json"), `{"version":1,"kind":"personal","base_branch":"main","commands":{"test":"./test.sh"},"mcp_read_tools":{"playwright":["browser_navigate"],"linear":["issue"]}}`+"\n")
	h.expect(h.pre("mcp__linear__issue", map[string]any{"id": "X-1"}), "deny", "")
	h.expect(h.pre("mcp__playwright__browser_navigate", map[string]any{}), "", "")

	// A reviewer is read-only: an unclear tool is refused there too.
	g := groundedTask(t, "normal")
	g.docs(DocPlan, goodPlan)
	id, _ := g.startHelper(HelperChallenger)
	g.expect(PreToolUse(g.helperIn(id, HelperChallenger, "mcp__linear__issue", map[string]any{})), "deny", "read-only")
}

// G4: zsh is the shell on macOS. A line bash can't parse is parsed as zsh
// where zsh runs it (the line itself, eval, zsh -c), and checked like any
// other. zsh parts the checks don't follow (glob qualifiers, which can run
// code, ${~x}, expansion flags beyond plain ones) still can't be checked.
func TestZshOnlySyntaxIsChecked(t *testing.T) {
	h := newHarness(t)
	for cmd, want := range map[string]string{
		"echo ${(U)name}":                              "allow",
		"() { cat src/a.go }":                          "allow",
		"echo ${arr[(r)x]}":                            "allow",
		"zsh -c 'echo ${(U)x}'":                        "allow",
		"echo ${(U)x} >>| src/a.go":                    "deny", // zsh's clobbering append writes a project file, and no task is open
		"echo ${(U)x} &>| src/a.go":                    "deny",
		"echo ${(U)x}; ls *(e:'touch y':)":             "deny", // a glob qualifier can run code
		"echo ${(U)x}; ls **/*.go(.om[1,3])":           "deny",
		"echo ${(e)x}":                                 "deny", // (e) evaluates the value
		"echo ${~pat}":                                 "deny", // the value becomes a pattern
		"bash -c 'echo ${(U)x}'":                       "deny", // bash runs it, and bash can't parse it
		"echo ${(U)x}; rm src/a.go":                    "deny",
		"echo ${(U)x}; git push origin main":           "deny",
		"function a b { echo hi }; echo ${(U)x}":       "allow",
		"echo ${(U)x}; cat <<'EOF'\nf(x) and (y)\nEOF": "allow",
	} {
		if got, r := decision(h.bash(cmd)); got != want {
			t.Errorf("%q: %q (%s), want %q", cmd, got, r, want)
		}
	}
	// The write targets of a zsh redirect are known, so the gates name them.
	segs := ParseShell("echo ${(U)x} >>| src/a.go", h.repo)
	if len(segs) != 1 || len(WriteTargets(segs[0])) != 1 || WriteTargets(segs[0])[0] != "src/a.go" {
		t.Errorf("zsh >>| target not known: %+v", segs)
	}
	if got := ParseShell("echo ${(U)x}; ls *(e:'touch y':)", h.repo); len(got) != 1 || !strings.Contains(got[0].Opaque, "glob qualifier") {
		t.Errorf("glob qualifier not opaque: %+v", got)
	}
}

// G6: each deny rule setup adds names files Plumbline itself treats as
// secret, and templates stay readable.
func TestSecretDenyRulesMatchSecretFiles(t *testing.T) {
	rules := SecretDenyRules()
	if len(rules) < 10 {
		t.Fatalf("too few rules: %v", rules)
	}
	for _, r := range rules {
		if !strings.HasPrefix(r, "Read(**/") || !strings.HasSuffix(r, ")") {
			t.Errorf("%q: want Read(**/<name>)", r)
			continue
		}
		name := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(r, "Read(**/"), ")"), "*", "x")
		if !IsSecretFile("/repo/" + name) {
			t.Errorf("%q names %s, which IsSecretFile doesn't count as secret", r, name)
		}
	}
	for _, r := range rules {
		if strings.Contains(r, "example") || strings.Contains(r, "template") || strings.Contains(r, "sample") {
			t.Errorf("a template is denied: %s", r)
		}
	}
}

// G11: files xargs, find -exec or fd -x add aren't named in the command, so
// a command that reads files from such a listing could print .env or a key.
// It asks like a secret read, unless the listing only names files that
// can't be secret (rg -g '*.go', find -name '*.ts', fd -e go, git ls-files
// '*.go').
func TestListedFilesAreCheckedForSecrets(t *testing.T) {
	h := newHarness(t)
	for cmd, want := range map[string]string{
		"rg -l KEY | xargs cat":                              "ask",
		"rg -l --hidden KEY | xargs cat":                     "ask",
		"rg -l KEY -g '*.go' | xargs cat":                    "allow",
		"rg -l KEY --glob='*.{ts,tsx}' | xargs cat":          "allow",
		"rg -l KEY -g '*.json' | xargs cat":                  "ask", // secrets.json
		"rg -l KEY -g '*.local' | xargs cat":                 "ask", // .env.local
		"rg -l KEY -g '*.go' -g '*' | xargs cat":             "ask",
		"grep -rl KEY --include='*.py' . | xargs cat":        "allow",
		"grep -rl KEY . | xargs cat":                         "ask",
		"find . -name '*.ts' -exec cat {} +":                 "allow",
		"find . -name '*.ts' -o -name '*.go' -exec cat {} +": "allow",
		"find . -type f -exec cat {} +":                      "ask",
		"find . -name '*.pem' -exec cat {} +":                "ask",
		"find . -not -name '*.go' -exec cat {} +":            "ask",
		"fd -e go -x cat":                                    "allow",
		"fd . -x cat":                                        "ask",
		"git ls-files '*.go' | xargs cat":                    "allow",
		"git ls-files | xargs cat":                           "ask",
		"ls *.go | xargs cat":                                "allow",
		"xargs cat < list.txt":                               "ask",
		"rg -l KEY | xargs -I{} cat {}":                      "ask",
		"rg -l KEY | xargs timeout 5 cat":                    "ask",
		"rg -l KEY | xargs wc -l":                            "allow", // counts only
		"rg -l KEY | sort | xargs cat":                       "ask",
		"rg -l KEY -g '*.go' | sort | xargs cat":             "allow",
	} {
		if got, r := decision(h.bash(cmd)); got != want {
			t.Errorf("%q: %q (%s), want %q", cmd, got, r, want)
		}
	}
	h.expect(h.bash("rg -l KEY | xargs cat"), "ask", "-g '*.go'")
}

// O1: deploys made through release scripts and targets (make release,
// go-live.sh), release tools (goreleaser, semantic-release, surge, kamal,
// argocd, ...) and task runners with a deploy task are deploys. Docker
// pointed at another machine runs things there.
func TestMoreDeploysAndRemoteDocker(t *testing.T) {
	for cmd, want := range map[string]string{
		"make release":                             OpsDeploy,
		"make -C infra go-live":                    OpsDeploy,
		"npm run release":                          OpsDeploy,
		"yarn release:prod":                        OpsDeploy,
		"./scripts/go-live.sh":                     OpsDeploy,
		"bash scripts/rollout.sh prod":             OpsDeploy,
		"cargo release minor --execute":            OpsDeploy,
		"goreleaser release --clean":               OpsDeploy,
		"goreleaser":                               OpsDeploy,
		"npx semantic-release":                     OpsDeploy,
		"npx release-it":                           OpsDeploy,
		"lerna publish from-package":               OpsDeploy,
		"npx changeset publish":                    OpsDeploy,
		"surge ./dist my-site.surge.sh":            OpsDeploy,
		"kamal deploy":                             OpsDeploy,
		"argocd app sync api":                      OpsDeploy,
		"skaffold run -p prod":                     OpsDeploy,
		"copilot svc deploy":                       OpsDeploy,
		"eas update --branch production":           OpsDeploy,
		"cap production deploy":                    OpsDeploy,
		"nx run api:deploy":                        OpsDeploy,
		"fastlane release":                         OpsDeploy,
		"make release-notes":                       "",
		"npm run release:check":                    "",
		"make build":                               "",
		"goreleaser build --snapshot":              "",
		"goreleaser check":                         "",
		"npx semantic-release --dry-run":           "",
		"surge list":                               "",
		"argocd app get api":                       "",
		"kamal app logs":                           "",
		"docker --context prod ps":                 "", // READ2: a look at a remote Docker changes nothing there
		"docker -c prod compose up -d":             OpsRemote,
		"docker -H ssh://me@host ps":               "", // READ2: a look at a remote Docker changes nothing there
		"docker --host=tcp://10.0.0.5:2376 run x":  OpsRemote,
		"DOCKER_HOST=ssh://me@host docker ps":      OpsRemote,
		"DOCKER_CONTEXT=prod docker ps":            OpsRemote,
		"docker --context $CTX ps":                 OpsRemote,
		"podman --remote ps":                       OpsRemote,
		"podman --connection prod ps":              OpsRemote,
		"docker --context default ps":              "",
		"docker --context desktop-linux ps":        "",
		"docker -H unix:///var/run/docker.sock ps": "",
		"docker -H tcp://localhost:2375 ps":        "",
		"docker ps":                                "",
		"docker compose up -d":                     "",
		"docker context use prod":                  OpsRemote,
		"docker --context prod exec web ls":        OpsRemote,
		"docker -H ssh://me@host rm -f web":        OpsRemote,
		"docker context use desktop-linux":         "",
		"docker context ls":                        "",
		"npm run deploy:test":                      OpsDeploy, // a test environment is still a deploy
		"npm run deploy:docs":                      OpsDeploy,
	} {
		segs := ParseShell(cmd, "/tmp")
		if got := OpsKind(segs[len(segs)-1]); got != want {
			t.Errorf("OpsKind(%q) = %q, want %q", cmd, got, want)
		}
	}
	// Docker's current context, or the session's DOCKER_HOST, points every
	// plain docker command there.
	cfg := t.TempDir()
	t.Setenv("DOCKER_CONFIG", cfg)
	write(t, filepath.Join(cfg, "config.json"), `{"currentContext": "prod"}`)
	if got := OpsKind(ParseShell("docker compose up -d", "/tmp")[0]); got != OpsRemote {
		t.Errorf("current context prod: %q, want remote", got)
	}
	write(t, filepath.Join(cfg, "config.json"), `{"currentContext": "orbstack"}`)
	if got := OpsKind(ParseShell("docker compose up -d", "/tmp")[0]); got != "" {
		t.Errorf("current context orbstack: %q, want local", got)
	}
	t.Setenv("DOCKER_HOST", "ssh://me@host")
	if got := OpsKind(ParseShell("docker compose up -d", "/tmp")[0]); got != OpsRemote {
		t.Errorf("DOCKER_HOST=ssh in the session: %q, want remote", got)
	}
	if got := OpsKind(ParseShell("docker ps", "/tmp")[0]); got != "" {
		t.Errorf("DOCKER_HOST=ssh in the session, docker ps: %q, want a look (READ2)", got)
	}
	t.Setenv("DOCKER_HOST", "")

	// In a pipeline project a release by hand is refused; remote Docker asks.
	h := newHarness(t)
	s := &Setup{Kind: "personal", BaseBranch: "main", DeployPipeline: true}
	s.Commands.Test = "./test.sh"
	_, err := SaveSetup(h.repo, s)
	must(t, err)
	h.expect(h.bash("make release"), "deny", "pipeline")
	h.expect(h.bash("./scripts/go-live.sh"), "deny", "pipeline")
	h.expect(h.bash("docker --context prod compose up -d"), "ask", "another machine")
}

// O2: more SQL functions that change things count as writes, and so does
// SELECT ... INTO. A client given a local host through PGHOST or MYSQL_HOST
// on the command line connects to this laptop.
func TestMoreDatabaseWritesAndLocalHostEnv(t *testing.T) {
	q := func(sql string) string { return "psql -h db.example.com app -c \"" + sql + "\"" }
	for cmd, want := range map[string]string{
		q("select pg_advisory_lock(1)"):                                 OpsWrite,
		q("select pg_try_advisory_xact_lock(1)"):                        OpsWrite,
		q("select pg_switch_wal()"):                                     OpsWrite,
		q("select pg_create_restore_point('x')"):                        OpsWrite,
		q("select pg_create_logical_replication_slot('s', 'x')"):        OpsWrite,
		q("select pg_drop_replication_slot('s')"):                       OpsWrite,
		q("select pg_stat_reset()"):                                     OpsWrite,
		q("select pg_stat_statements_reset()"):                          OpsWrite,
		q("select pg_notify('c', 'x')"):                                 OpsWrite,
		q("select pg_promote()"):                                        OpsWrite,
		q("select pg_wal_replay_pause()"):                               OpsWrite,
		q("select pg_backup_start('x')"):                                OpsWrite,
		q("select pg_file_write('f', 'x', false)"):                      OpsWrite,
		q("select pg_logical_slot_get_changes('s', null, null)"):        OpsWrite,
		q("select pg_replication_origin_create('o')"):                   OpsWrite,
		q("select pg_catalog.setval('s', 1)"):                           OpsWrite,
		q("select * into archive from users"):                           OpsWrite,
		"mysql -h db.example.com -e \"select get_lock('x', 1)\"":        OpsWrite,
		"mysql -h db.example.com -e \"select 1 into outfile '/tmp/x'\"": OpsWrite,
		q("select count(*) from users"):                                 "",
		q("select pg_size_pretty(pg_database_size('app'))"):             "",
		q("select * from pg_stat_activity"):                             "",
		q("select pg_logical_slot_peek_changes('s', null, null)"):       "",
		q("select note from t where note = 'into the night'"):           "",
	} {
		if got := OpsKind(ParseShell(cmd, "/tmp")[0]); got != want {
			t.Errorf("OpsKind(%q) = %q, want %q", cmd, got, want)
		}
	}
	for cmd, local := range map[string]bool{
		"PGHOST=localhost psql app -c 'select 1'":      true,
		"PGHOST=/tmp psql app -c 'select 1'":           true,
		"MYSQL_HOST=127.0.0.1 mysql -e 'select 1'":     true,
		"PGHOST=db.example.com psql -c 'select 1'":     false,
		"psql app -c 'select 1'":                       false, // the profile's PGHOST or a service file could point anywhere
		"PGHOST=localhost psql -h db.example.com -c x": false,
	} {
		segs := ParseShell(cmd, "/tmp")
		if got := dbLocal(segs[len(segs)-1]); got != local {
			t.Errorf("dbLocal(%q) = %v, want %v", cmd, got, local)
		}
	}
}
