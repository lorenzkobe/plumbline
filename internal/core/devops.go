package core

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Devops rules (phase 3). A command in the approved plan's "Needs your OK"
// list runs as listed, except the risky kinds below: those are confirmed by
// the user each time. A project whose pipeline deploys never deploys by hand.

// Outside-the-laptop command kinds.
const (
	OpsDeploy  = "deploy"  // ships code or config to a live system
	OpsDestroy = "destroy" // deletes something outside the laptop
	OpsWrite   = "write"   // changes data or settings outside the laptop
	OpsRemote  = "remote"  // runs commands on, or copies files to, another machine
)

const devopsPartName = "devops"

var opsWhat = map[string]string{
	OpsDeploy:  "it deploys",
	OpsDestroy: "it deletes something outside your laptop",
	OpsWrite:   "it changes data or settings outside your laptop",
	OpsRemote:  "it runs commands on, or copies files to, another machine",
}

// cloudAskReason: the prompt's reason for a cloud command that doesn't only
// look, naming the risk in plain words. What the command is for is Claude's
// own description, shown on the prompt above it.
func cloudAskReason(s Segment, name string) string {
	switch OpsKind(s) {
	case OpsDestroy:
		return "this deletes something in " + name + ", outside your laptop. Deleted data may not come back."
	case OpsWrite:
		return "this changes data or settings in " + name + ", outside your laptop."
	case OpsDeploy:
		return "this deploys with " + name + " to a live system outside your laptop."
	case OpsRemote:
		return "this runs commands on, or copies files to, another machine outside your laptop."
	}
	if cloudShowsSecret(s) {
		return "this may show a secret's value (a token, key or password) from " + name + ". Allow only if Claude really needs to see it."
	}
	return name + " reaches systems outside your laptop. It may change something there or show a secret."
}

// coreAsksOutside: the 1a outside-the-laptop checks in gateBash already ask
// about this command (they use the same helpers).
func coreAsksOutside(s Segment) bool {
	name := s.Name()
	switch {
	case cloudCmds[name], name == "supabase":
		return true
	case dbClients[name] && !dbLocal(s):
		return true
	case (name == "curl" || name == "wget" || name == "http" || name == "https") && httpSends(s):
		return true
	case name == "docker" && (dockerPushes(s) || s.Arg(1) == "login"):
		return true
	}
	return false
}

// verbs per tool: a word among the command's arguments (not a flag) that
// makes it that kind.
type opsVerbs struct{ deploy, destroy, write, remote []string }

var opsTools = map[string]opsVerbs{
	"vercel":     {deploy: []string{"deploy", "promote", "rollback", "redeploy", "alias"}, destroy: []string{"remove", "rm"}, write: []string{"env", "domains", "dns", "certs", "secrets"}},
	"netlify":    {deploy: []string{"deploy"}, destroy: []string{"sites:delete"}, write: []string{"env:set", "env:unset", "env:import"}},
	"sls":        {deploy: []string{"deploy", "rollback"}, destroy: []string{"remove"}},
	"serverless": {deploy: []string{"deploy", "rollback"}, destroy: []string{"remove"}},
	"cdk":        {deploy: []string{"deploy"}, destroy: []string{"destroy"}},
	"sam":        {deploy: []string{"deploy", "sync"}, destroy: []string{"delete"}},
	"fly":        {deploy: []string{"deploy", "launch", "release"}, destroy: []string{"destroy"}, write: []string{"secrets", "scale"}, remote: []string{"ssh", "sftp", "console"}},
	"flyctl":     {deploy: []string{"deploy", "launch", "release"}, destroy: []string{"destroy"}, write: []string{"secrets", "scale"}, remote: []string{"ssh", "sftp", "console"}},
	"firebase":   {deploy: []string{"deploy", "hosting:channel:deploy", "hosting:clone"}, destroy: []string{"hosting:disable", "functions:delete", "firestore:delete", "database:remove"}, write: []string{"database:set", "database:update", "database:push", "functions:config:set"}},
	"heroku":     {deploy: []string{"releases:rollback", "container:release", "container:push"}, destroy: []string{"apps:destroy", "destroy"}, write: []string{"config:set", "config:unset", "ps:scale", "pg:reset"}, remote: []string{"run", "ps:exec"}},
	"railway":    {deploy: []string{"up", "redeploy"}, destroy: []string{"down", "delete"}, write: []string{"variables"}, remote: []string{"run", "shell", "ssh"}},
	"wrangler":   {deploy: []string{"deploy", "publish", "rollback"}, destroy: []string{"delete"}, write: []string{"secret", "kv:key", "d1", "r2"}},
	"eb":         {deploy: []string{"deploy", "swap"}, destroy: []string{"terminate"}, write: []string{"setenv", "scale"}, remote: []string{"ssh"}},
	"render":     {deploy: []string{"deploy", "deploys"}, destroy: []string{"delete"}, remote: []string{"ssh"}},
	"amplify":    {deploy: []string{"publish", "push"}, destroy: []string{"delete"}},
	"kubectl":    {deploy: []string{"apply", "rollout", "set", "replace", "patch", "create", "scale", "autoscale", "edit"}, destroy: []string{"delete", "drain"}, write: []string{"annotate", "label", "cordon", "uncordon", "taint", "cp", "port-forward"}, remote: []string{"exec", "attach", "debug"}},
	"helm":       {deploy: []string{"install", "upgrade", "rollback"}, destroy: []string{"uninstall", "delete"}},
	"terraform":  {deploy: []string{"apply", "import", "taint", "untaint", "state"}, destroy: []string{"destroy"}},
	"tofu":       {deploy: []string{"apply", "import", "taint", "untaint", "state"}, destroy: []string{"destroy"}},
	"pulumi":     {deploy: []string{"up", "update", "refresh", "import", "cancel"}, destroy: []string{"destroy"}, write: []string{"config"}},
	"doctl":      {deploy: []string{"create-deployment", "update"}, destroy: []string{"delete"}, write: []string{"create"}, remote: []string{"ssh", "console"}},
	"gcloud":     {deploy: []string{"deploy", "update", "rollback", "submit"}, destroy: []string{"delete", "rm"}, write: []string{"create", "set", "add-iam-policy-binding", "remove-iam-policy-binding", "set-iam-policy", "import", "patch", "reset", "restart", "start", "stop", "cp", "mv", "rsync"}, remote: []string{"ssh", "scp"}},
	"gsutil":     {destroy: []string{"rm", "rb"}, write: []string{"cp", "mv", "rsync", "setmeta", "acl", "iam", "mb", "defacl", "lifecycle", "versioning", "web"}},
	"az":         {deploy: []string{"deploy", "update", "swap"}, destroy: []string{"delete", "purge"}, write: []string{"create", "set", "restart", "start", "stop"}, remote: []string{"ssh"}},
	"supabase":   {deploy: []string{"push", "deploy"}, destroy: []string{"delete"}, write: []string{"secrets", "repair"}},
	"gh":         {destroy: []string{"delete", "archive"}, write: []string{"release", "secret", "variable", "workflow", "run"}},
	"glab":       {destroy: []string{"delete", "archive"}, write: []string{"release", "variable"}},
	"eksctl":     {deploy: []string{"create", "upgrade", "update", "scale", "set"}, destroy: []string{"delete", "drain"}},
	"kamal":      {deploy: []string{"deploy", "redeploy", "rollback", "setup"}, destroy: []string{"remove"}},
	"argocd":     {deploy: []string{"sync", "rollback"}, destroy: []string{"delete"}, write: []string{"set", "patch", "create"}},
	"skaffold":   {deploy: []string{"run", "deploy", "apply"}, destroy: []string{"delete"}},
	"copilot":    {deploy: []string{"deploy"}, destroy: []string{"delete"}},
	"eas":        {deploy: []string{"update", "submit"}},
	"expo":       {deploy: []string{"publish"}},
	"lerna":      {deploy: []string{"publish", "version"}},
	"changeset":  {deploy: []string{"publish"}},
	"nomad":      {deploy: []string{"run", "promote", "revert", "scale"}, destroy: []string{"stop", "purge"}},
	"surge":      {destroy: []string{"teardown"}},
}

// Words that only look. Right after a verb they make the command a read
// (`gh release list`, `kubectl rollout status`, `supabase secrets list`).
var opsLooks = map[string]bool{
	"list": true, "ls": true, "get": true, "describe": true, "show": true, "status": true, "view": true,
	"history": true, "logs": true, "log": true, "inspect": true, "info": true, "whoami": true,
	"diff": true, "preview": true, "validate": true, "output": true, "help": true, "version": true, "pull": true,
}

// dryRunOrHelp: a dry run (--dry-run, =client, =server, =true; not =none
// or =false, which really run) or --help anywhere. (-h is a host for the
// database clients, so it isn't help here.)
func dryRunOrHelp(s Segment) bool {
	for _, w := range s.Words[1:] {
		switch w {
		case "--dry-run", "--dry-run=client", "--dry-run=server", "--dry-run=true", "--dryrun", "--help", "--version", "--preview", "--plan":
			return true
		}
	}
	return false
}

var (
	reAWSWrite   = regexp.MustCompile(`^(create|put|update|modify|start|stop|reboot|run|invoke|attach|detach|associate|disassociate|enable|disable|register|import|export|publish|send|set|tag|untag|add|restore|copy|reset|replace|execute|rotate|apply|authorize|revoke|upload|change|cancel|batch|submit|schedule|purchase|allocate|release|accept|reject|request|respond|initiate|complete|abort|confirm|resume|pause|deploy|promote|failover|reload|recover|terminate)(-|$)`)
	reAWSDestroy = regexp.MustCompile(`^(delete|terminate|remove|deregister|destroy|purge|batch-delete|schedule-key-deletion|empty)(-|$)`)
	reSQLWrite   = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|truncate|create|grant|revoke|merge|upsert|replace|vacuum|reindex|copy|call|do|comment|lock|refresh|into|setval|nextval|set_config|pg_terminate_backend|pg_cancel_backend|pg_reload_conf|pg_rotate_logfile|lo_\w+|dblink\w*|` +
		// Postgres functions that change the server, its data or its state
		`pg_(try_)?advisory\w*|pg_switch_\w+|pg_create_\w+|pg_drop_\w+|pg_copy_\w+|pg_stat\w*reset\w*|pg_notify|pg_promote|pg_(wal|xlog)_replay_\w+|pg_backup_(start|stop)|pg_(start|stop)_backup|pg_file_\w+|pg_logical_emit_message|pg_logical_slot_get_\w+|pg_replication_origin_\w+|pg_replication_slot_advance|pg_sync_replication_slots|pg_import_system_collations|pg_log_backend_memory_contexts|pg_extension_config_dump|` +
		// MySQL's named locks
		`get_lock|release_lock|release_all_locks)\b`)
	reSQLRead = regexp.MustCompile(`(?i)^\s*(select|show|explain|with|table|values|\\d|\\l|\\dt|describe|desc)\b`)
)

// A deploy through the project's own scripts: npm run deploy, yarn
// deploy:prod, make release, ./scripts/go-live.sh, npm publish. Not one
// that only checks or writes notes (release-notes, deploy:check).
var (
	reDeployName = regexp.MustCompile(`(?i)(^|[/:_.-])(deploy|publish|ship|release|rollout|go-?live)([:_.-]|$)`)
	reNotDeploy  = regexp.MustCompile(`(?i)(^|[/:_.-])(deploy|publish|ship|release|rollout|go-?live)[:_.-](notes?|check|dry-?run|build|local|lint|changelog|plan|diff|validate|status|help)([:_.-]|$)`)
)

func deployName(w string) bool { return reDeployName.MatchString(w) && !reNotDeploy.MatchString(w) }

// Flags that take a value, per tool family, so the value isn't read as the
// script or target (npm --prefix infra run deploy, make -C deploy lint).
var (
	pkgValueFlags  = map[string]bool{"--prefix": true, "-C": true, "--dir": true, "-w": true, "--workspace": true, "--filter": true, "-F": true, "--cwd": true, "--registry": true, "--tag": true, "--otp": true, "--access": true, "-p": true, "--package": true}
	makeValueFlags = map[string]bool{"-C": true, "-f": true, "--directory": true, "--file": true, "--makefile": true, "-I": true, "--include-dir": true, "-j": true, "--jobs": true, "-l": true, "-o": true, "-W": true, "--justfile": true, "-d": true, "--working-directory": true, "--taskfile": true, "-t": true}
	runValueFlags  = map[string]bool{"-m": true, "-W": true, "-X": true, "-r": true, "--require": true, "--import": true, "--loader": true, "-e": false}
	vercelValFlags = map[string]bool{"--cwd": true, "--scope": true, "-S": true, "--token": true, "-t": true, "--team": true, "-T": true, "-A": true, "--local-config": true, "-Q": true, "--global-config": true, "-e": true, "--env": true, "-b": true, "--build-env": true, "-m": true, "--meta": true, "--regions": true, "--archive": true, "--target": true}
)

// args skips flags and the values of flags that take one.
func args(s Segment, valueFlags map[string]bool) []string {
	var out []string
	words := s.Words[1:]
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w == "--" {
			out = append(out, words[i+1:]...)
			break
		}
		if strings.HasPrefix(w, "-") && len(w) > 1 {
			if valueFlags[w] && i+1 < len(words) {
				i++
			}
			continue
		}
		out = append(out, w)
	}
	return out
}

// positional: the command's arguments that aren't flags.
func positional(s Segment) []string { return args(s, nil) }

// sub is a segment for the command a runner starts (npx vercel --prod).
func sub(s Segment, from int) Segment {
	t := s
	t.Words = s.Words[from:]
	t.Dyn, t.Glob = nil, nil
	return t
}

// runnerTarget: the command a package runner starts, when it starts one
// (npx, bunx, pnpm dlx|exec, npm exec|x, yarn dlx|exec, bun x, yarn <bin>).
func runnerTarget(s Segment) (Segment, bool) {
	name := s.Name()
	at := func(start int) (Segment, bool) {
		for i := start; i < len(s.Words); i++ {
			w := s.Words[i]
			if w == "--" {
				continue
			}
			if strings.HasPrefix(w, "-") {
				if pkgValueFlags[w] && i+1 < len(s.Words) {
					i++
				}
				continue
			}
			return sub(s, i), true
		}
		return Segment{}, false
	}
	switch name {
	case "npx", "bunx", "pnpx":
		return at(1)
	case "npm", "pnpm", "yarn", "bun":
		a := args(s, pkgValueFlags)
		if len(a) == 0 {
			return Segment{}, false
		}
		switch a[0] {
		case "exec", "x", "dlx":
			for i, w := range s.Words {
				if w == a[0] {
					return at(i + 1)
				}
			}
		default:
			// yarn vercel --prod, pnpm vercel: a tool run through the
			// package manager.
			if _, tool := opsTools[a[0]]; tool && (name == "yarn" || name == "pnpm" || name == "bun") {
				for i, w := range s.Words {
					if w == a[0] {
						return sub(s, i), true
					}
				}
			}
		}
	}
	return Segment{}, false
}

var pkgOwnCmds = map[string]bool{"install": true, "i": true, "add": true, "remove": true, "rm": true, "uninstall": true, "upgrade": true, "update": true, "up": true, "why": true, "info": true, "view": true, "outdated": true, "list": true, "ls": true, "link": true, "unlink": true, "init": true, "create": true, "audit": true, "ci": true, "dedupe": true, "prune": true, "pack": true, "config": true, "cache": true, "test": true, "t": true, "start": true, "build": true}

func scriptBase(p string) string { return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) }

// scriptDeploys: a deploy through the project's scripts or a package
// registry.
func scriptDeploys(s Segment) bool {
	name := s.Name()
	switch name {
	case "npm", "pnpm", "yarn", "bun":
		a := args(s, pkgValueFlags)
		if len(a) == 0 {
			return false
		}
		if a[0] == "publish" || (name == "yarn" && a[0] == "npm" && len(a) > 1 && a[1] == "publish") {
			return true
		}
		if a[0] == "workspace" && len(a) > 2 {
			a = a[2:] // yarn workspace api deploy
		} else if (a[0] == "workspaces" || a[0] == "-r" || a[0] == "recursive") && len(a) > 2 {
			a = a[1:]
		}
		if a[0] == "run" || a[0] == "run-script" {
			return len(a) > 1 && deployName(a[1])
		}
		if name == "npm" || pkgOwnCmds[a[0]] || (name == "pnpm" && a[0] == "deploy") {
			return false // the package manager's own commands; `pnpm deploy` packs a package locally
		}
		return deployName(a[0]) // yarn deploy:prod runs the script
	case "make", "gmake", "just", "task", "mage", "rake", "cap", "mina", "dep", "fab", "envoy", "invoke", "inv", "fastlane", "nx", "turbo":
		// a target or task named for deploying (cap production deploy,
		// nx run api:deploy, fastlane release)
		for _, a := range args(s, makeValueFlags) {
			if !strings.Contains(a, "=") && deployName(a) {
				return true
			}
		}
		return false
	case "mvn", "mvnw":
		return contains(positional(s), "deploy") || anyPrefix(positional(s), "deploy:")
	case "gradle", "gradlew":
		for _, a := range positional(s) {
			if strings.HasPrefix(strings.TrimPrefix(a, ":"), "publish") || strings.Contains(a, ":publish") {
				return true
			}
		}
		return false
	case "cargo":
		// cargo release only dry-runs without --execute
		return s.Arg(1) == "publish" || s.Arg(1) == "release" && (contains(s.Words, "--execute") || contains(s.Words, "-x"))
	case "goreleaser":
		switch s.Arg(1) {
		case "build", "check", "healthcheck", "init", "completion", "man", "help", "jsonschema", "-v", "--version":
			return false
		}
		return !contains(s.Words, "--snapshot")
	case "semantic-release", "release-it", "np", "gh-pages":
		return true // they publish (their --dry-run is caught before)
	case "surge":
		switch s.Arg(1) {
		case "list", "login", "logout", "whoami", "token", "help", "teardown":
			return false
		}
		return true // surge <folder> <domain> publishes the folder
	case "twine", "gem":
		return s.Arg(1) == "upload" || s.Arg(1) == "push"
	case "bash", "sh", "zsh", "dash", "ksh", ".", "source",
		"python", "python3", "node", "ruby", "perl", "php", "deno", "tsx", "ts-node", "bun-run", "pwsh":
		if contains(s.Words, "-c") {
			return false // the command text is its own segment
		}
		a := args(s, runValueFlags)
		if name == "deno" && len(a) > 0 && a[0] == "run" {
			a = a[1:]
		}
		return len(a) > 0 && deployName(scriptBase(a[0]))
	}
	// a script file named for deploying
	w := s.Words[0]
	return (strings.Contains(w, "/") || strings.HasSuffix(w, ".sh")) && deployName(scriptBase(w))
}

func anyPrefix(words []string, p string) bool {
	for _, w := range words {
		if strings.HasPrefix(w, p) {
			return true
		}
	}
	return false
}

func anyIn(words []string, set []string) bool {
	for _, w := range words {
		for _, v := range set {
			if strings.EqualFold(w, v) {
				return true
			}
		}
	}
	return false
}

// OpsKind says what an outside-the-laptop command does: deploy, destroy,
// write, remote, or "" for reads (and for commands that stay on the laptop).
func OpsKind(s Segment) string {
	if s.Name() == "" {
		return ""
	}
	if t, ok := runnerTarget(s); ok {
		return OpsKind(t)
	}
	if dryRunOrHelp(s) {
		return ""
	}
	if scriptDeploys(s) {
		return OpsDeploy
	}
	name := s.Name()
	a := positional(s)
	switch name {
	case "ssh", "scp", "sftp", "mosh", "ansible", "ansible-playbook", "pssh", "pdsh":
		return OpsRemote
	case "rsync":
		for _, x := range a {
			if strings.Contains(x, ":") && !strings.HasPrefix(x, "/") && !strings.HasPrefix(x, "./") {
				return OpsRemote
			}
		}
		return ""
	case "aws":
		return awsKind(a)
	case "docker", "podman":
		if dockerPushes(s) {
			return OpsDeploy
		}
		if dockerRemote(s) && !ReadOnly(s) {
			return OpsRemote // looking at a remote Docker (ps, logs) changes nothing there
		}
		return ""
	case "psql", "mysql", "mariadb", "mongosh", "mongo", "redis-cli", "pg_restore":
		return dbKind(s)
	case "prisma":
		if (s.Arg(1) == "migrate" && (s.Arg(2) == "deploy" || s.Arg(2) == "reset" || s.Arg(2) == "resolve")) || (s.Arg(1) == "db" && (s.Arg(2) == "push" || s.Arg(2) == "execute")) {
			return OpsWrite
		}
		return ""
	case "curl", "wget", "http", "https":
		if httpSends(s) {
			return OpsWrite
		}
		return ""
	}
	v, ok := opsTools[name]
	if !ok {
		return ""
	}
	switch name {
	case "gh", "glab":
		if s.Arg(1) == "api" {
			return ghAPIKind(s)
		}
		if len(a) > 0 && (a[0] == "pr" || a[0] == "issue") {
			return "" // pull requests and issues aren't production
		}
	case "vercel":
		va := args(s, vercelValFlags)
		prod := contains(s.Words, "--prod") || contains(s.Words, "--production")
		switch {
		case len(va) == 0:
			return OpsDeploy // plain `vercel` deploys (a preview, or production with --prod)
		case !vercelSubs[va[0]]:
			return OpsDeploy // `vercel <folder>` deploys that folder
		case va[0] == "deploy" || (prod && va[0] != "build" && va[0] != "pull" && va[0] != "dev" && va[0] != "env"):
			return OpsDeploy
		}
		a = va
	case "gcloud", "az":
		if len(a) > 0 && (a[0] == "config" || a[0] == "auth" || a[0] == "login" || a[0] == "account") {
			return "" // the CLI's own settings on this laptop
		}
	case "kubectl":
		if len(a) > 0 && (a[0] == "auth" || a[0] == "config" || a[0] == "version" || a[0] == "explain" || a[0] == "api-resources" || a[0] == "completion" || a[0] == "cluster-info") {
			return ""
		}
	case "supabase":
		if (contains(a, "reset") || contains(a, "up")) && (contains(s.Words, "--linked") || anyPrefix(s.Words, "--db-url")) {
			return OpsWrite
		}
	}
	for i, x := range a {
		kind := ""
		switch {
		case anyIn([]string{x}, v.destroy):
			kind = OpsDestroy
		case anyIn([]string{x}, v.deploy):
			kind = OpsDeploy
		case anyIn([]string{x}, v.remote):
			kind = OpsRemote
		case anyIn([]string{x}, v.write):
			kind = OpsWrite
		default:
			continue
		}
		if i+1 < len(a) && opsLooks[strings.ToLower(a[i+1])] {
			return ""
		}
		return kind
	}
	return ""
}

var vercelSubs = map[string]bool{}

func init() {
	for _, w := range []string{"deploy", "promote", "rollback", "redeploy", "alias", "remove", "rm", "env", "domains", "dns", "certs", "secrets",
		"login", "logout", "link", "pull", "dev", "build", "init", "switch", "teams", "project", "projects", "whoami", "list", "ls", "logs", "inspect", "help", "bisect", "git", "integration", "telemetry"} {
		vercelSubs[w] = true
	}
}

// ghAPIKind: gh api is a GET unless a method says otherwise, or fields or
// an input body make it a POST.
func ghAPIKind(s Segment) string {
	method := ""
	body := false
	for i, w := range s.Words {
		switch {
		case (w == "-X" || w == "--method") && i+1 < len(s.Words):
			method = s.Words[i+1]
		case strings.HasPrefix(w, "--method="):
			method = strings.TrimPrefix(w, "--method=")
		case strings.HasPrefix(w, "-X") && len(w) > 2:
			method = w[2:]
		case w == "-f" || w == "-F" || w == "--field" || w == "--raw-field" || w == "--input" ||
			strings.HasPrefix(w, "--field=") || strings.HasPrefix(w, "--raw-field=") || strings.HasPrefix(w, "--input="):
			body = true
		}
	}
	if (method != "" && !strings.EqualFold(method, "GET")) || (method == "" && body) {
		return OpsWrite
	}
	return ""
}

// dockerPushes: an image leaves the laptop (docker push, image push,
// compose push, buildx build --push).
func dockerPushes(s Segment) bool {
	a := args(s, map[string]bool{"--context": true, "-c": true, "-H": true, "--host": true, "--config": true, "-l": true, "--log-level": true, "-f": true, "--file": true, "-p": true, "--project-name": true})
	if len(a) > 0 && a[0] == "push" {
		return true
	}
	if len(a) > 1 && (a[0] == "image" || a[0] == "compose" || a[0] == "buildx" || a[0] == "manifest") && a[1] == "push" {
		return true
	}
	return contains(s.Words, "--push") || anyPrefix(s.Words, "--output=type=registry")
}

// Docker contexts that are this laptop's own daemon.
var localDockerContexts = set("default desktop-linux desktop-windows colima orbstack rancher-desktop podman-machine-default")

// dockerRemote: Docker (or Podman) is pointed at another machine's daemon,
// by --context, -H/--host, DOCKER_HOST or DOCKER_CONTEXT (on the command or
// in the environment), Docker's current context, or Podman's --remote,
// --connection or --url. docker context use <another machine> points every
// later command there. A value not known counts as another machine.
func dockerRemote(s Segment) bool {
	podman := s.Name() == "podman"
	host := func(v string) bool {
		if v == "" || strings.Contains(v, "$") {
			return true
		}
		low := strings.ToLower(v)
		if strings.HasPrefix(low, "unix://") || strings.HasPrefix(low, "npipe://") || strings.HasPrefix(low, "fd://") {
			return false
		}
		return !localHost(hostOf(v))
	}
	context := func(v string) bool {
		if v == "" || strings.Contains(v, "$") {
			return true
		}
		low := strings.ToLower(v)
		return !localDockerContexts[low] && !strings.HasPrefix(low, "colima-")
	}
	env := append([]string{}, s.Env...)
	if !podman {
		// The session's own settings, which the command inherits unless it
		// sets its own.
		if h := os.Getenv("DOCKER_HOST"); h != "" {
			env = append([]string{"DOCKER_HOST=" + h}, env...)
		} else if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
			env = append([]string{"DOCKER_CONTEXT=" + c}, env...)
		} else if c := dockerCurrentContext(); c != "" {
			env = append([]string{"DOCKER_CONTEXT=" + c}, env...)
		}
	}
	for _, e := range env {
		name, val, _ := strings.Cut(e, "=")
		switch name {
		case "DOCKER_HOST", "CONTAINER_HOST":
			if host(val) {
				return true
			}
		case "DOCKER_CONTEXT", "CONTAINER_CONNECTION":
			if context(val) {
				return true
			}
		}
	}
	// Global options come before the command (docker --context prod ps).
	for i := 1; i < len(s.Words) && strings.HasPrefix(s.Words[i], "-"); i++ {
		w := s.Words[i]
		n, v, hasEq := strings.Cut(w, "=")
		next := func() string {
			if hasEq {
				return v
			}
			if i+1 < len(s.Words) {
				i++
				if s.dyn(i) {
					return "$"
				}
				return s.Words[i]
			}
			return ""
		}
		switch {
		case podman && (n == "--remote" || n == "-r"):
			return true
		case n == "--context" || n == "-c" || podman && n == "--connection":
			if context(next()) {
				return true
			}
		case n == "-H" || n == "--host" || podman && n == "--url":
			if host(next()) {
				return true
			}
		case strings.HasPrefix(w, "-H") && len(w) > 2:
			if host(w[2:]) {
				return true
			}
		case n == "--config" || n == "-l" || n == "--log-level" || strings.HasPrefix(n, "--tls") && n != "--tls" && n != "--tlsverify" || n == "--identity":
			next() // its value
		}
	}
	if a := positional(s); !podman && len(a) >= 3 && a[0] == "context" && a[1] == "use" {
		return context(a[2])
	}
	return false
}

// dockerCurrentContext: the context docker context use chose, from Docker's
// config file ("" when none is set or the file can't be read).
func dockerCurrentContext() string {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(h, ".docker")
	}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		CurrentContext string `json:"currentContext"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	return cfg.CurrentContext
}

// awsKind looks at every argument, since global options with values
// (--profile prod, --region x) can come before the service. A value that
// happens to look like an operation only makes it ask, never pass.
func awsKind(a []string) string {
	deployOps := map[string]bool{"deploy": true, "create-stack": true, "update-stack": true, "execute-change-set": true,
		"update-function-code": true, "publish-version": true, "update-alias": true, "update-service": true,
		"register-task-definition": true, "create-deployment": true, "update-environment": true, "start-deployment": true}
	kind := ""
	rank := map[string]int{"": 0, OpsWrite: 1, OpsDeploy: 2, OpsDestroy: 3}
	for i, x := range a {
		k := ""
		switch {
		case i > 0 && a[i-1] == "s3" && (x == "rm" || x == "rb"):
			k = OpsDestroy
		case i > 0 && a[i-1] == "s3" && (x == "cp" || x == "mv" || x == "sync" || x == "mb" || x == "website"):
			k = OpsWrite
		case i > 0 && a[i-1] == "ssm" && x == "start-session":
			k = OpsWrite
		case i > 0 && deployOps[x]:
			k = OpsDeploy
		case i > 0 && reAWSDestroy.MatchString(x):
			k = OpsDestroy
		case i > 0 && reAWSWrite.MatchString(x):
			k = OpsWrite
		}
		if rank[k] > rank[kind] {
			kind = k
		}
	}
	return kind
}

// localHost: the host is this laptop (or Docker on it).
func localHost(h string) bool {
	h = strings.ToLower(strings.Trim(h, "[]"))
	switch h {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0", "host.docker.internal":
		return true
	}
	return strings.HasSuffix(h, ".localhost") || strings.HasPrefix(h, "127.")
}

// hostOf: the host in a URL or a host[:port][/path] word, or "".
func hostOf(w string) string {
	if strings.HasPrefix(w, ":") {
		return "localhost" // httpie's :3000/x
	}
	if !strings.Contains(w, "://") {
		w = "http://" + w
	}
	u, err := url.Parse(w)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

var httpValueFlags = map[string]bool{
	"-H": true, "--header": true, "-d": true, "--data": true, "--data-raw": true, "--data-binary": true, "--data-urlencode": true,
	"--json": true, "-F": true, "--form": true, "-X": true, "--request": true, "-o": true, "--output": true, "-u": true, "--user": true,
	"-A": true, "--user-agent": true, "-e": true, "--referer": true, "-b": true, "--cookie": true, "-c": true, "--cookie-jar": true,
	"-T": true, "--upload-file": true, "-w": true, "--write-out": true, "--connect-to": true, "--resolve": true, "-x": true, "--proxy": true,
	"-m": true, "--max-time": true, "--retry": true, "-E": true, "--cert": true, "--key": true, "--cacert": true, "-O": true,
	"--post-data": true, "--post-file": true, "--body-data": true, "--body-file": true, "--method": true, "--header=": true,
	"-a": true, "--auth": true, "--session": true, "--verify": true, "--timeout": true, "-U": true, "--user-agent=": true,
}

// httpTargets: the hosts a curl, wget or httpie command talks to.
func httpTargets(s Segment) []string {
	var hosts []string
	httpie := s.Name() == "http" || s.Name() == "https"
	for _, w := range args(s, httpValueFlags) {
		if httpie && (httpMethods[strings.ToUpper(w)] || httpieItem(w)) {
			continue
		}
		if h := hostOf(w); h != "" {
			hosts = append(hosts, h)
		}
		if httpie {
			break // the URL comes first; the rest are items
		}
	}
	return hosts
}

var httpMethods = map[string]bool{"GET": true, "HEAD": true, "OPTIONS": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// httpieItem: an httpie request item (name=value, name:=json, field@file,
// header:value, param==value).
func httpieItem(w string) bool {
	return !strings.Contains(w, "://") && (strings.Contains(w, "=") || strings.Contains(w, "@") || (strings.Contains(w, ":") && !strings.HasPrefix(w, ":") && !strings.Contains(w, "/") && !reHostPort.MatchString(w)))
}

var reHostPort = regexp.MustCompile(`^[\w.-]+:\d+$`)

// httpSends: a curl, wget or httpie command that sends data or uses a
// method other than GET, to a host that isn't this laptop.
func httpSends(s Segment) bool {
	if !httpSendsData(s) {
		return false
	}
	hosts := httpTargets(s)
	if len(hosts) == 0 {
		return true // can't tell where it goes: treat it as outside
	}
	return httpOutside(hosts)
}

// httpSendsData: curl, wget or httpie sends something (a method other than
// GET or HEAD, data, a form or a file), wherever it goes.
func httpSendsData(s Segment) bool {
	sends := false
	httpie := s.Name() == "http" || s.Name() == "https"
	for i, w := range s.Words[1:] {
		switch {
		case w == "-X" || w == "--request" || w == "--method":
			if i+2 < len(s.Words) && !strings.EqualFold(s.Words[i+2], "GET") && !strings.EqualFold(s.Words[i+2], "HEAD") {
				sends = true
			}
		case strings.HasPrefix(w, "-X") && len(w) > 2 && !strings.HasPrefix(w, "--"):
			if m := w[2:]; !strings.EqualFold(m, "GET") && !strings.EqualFold(m, "HEAD") {
				sends = true
			}
		case strings.HasPrefix(w, "--request=") || strings.HasPrefix(w, "--method="):
			m := w[strings.Index(w, "=")+1:]
			sends = sends || (!strings.EqualFold(m, "GET") && !strings.EqualFold(m, "HEAD"))
		case strings.HasPrefix(w, "--data") || strings.HasPrefix(w, "--json") || strings.HasPrefix(w, "--form") ||
			strings.HasPrefix(w, "--upload-file") || strings.HasPrefix(w, "--post-") || strings.HasPrefix(w, "--body-"):
			sends = true
		case strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") && len(w) > 1:
			// short flags, alone or bundled (-d, -sSd, -F, -T)
			if s.Name() == "curl" && strings.ContainsAny(shortFlags(w), "dFT") {
				sends = true
			}
			if httpie && (w == "-f" || w == "--form") {
				sends = true
			}
		case httpie && httpMethods[strings.ToUpper(w)] && !strings.EqualFold(w, "GET") && !strings.EqualFold(w, "HEAD") && !strings.EqualFold(w, "OPTIONS"):
			sends = true
		case httpie && httpieItem(w) && !strings.Contains(w, "==") && (strings.Contains(w, "=") || strings.Contains(w, "@")):
			sends = true // a data field or a file upload
		}
	}
	return sends
}

// httpOutside: one of the hosts isn't this laptop.
func httpOutside(hosts []string) bool {
	for _, h := range hosts {
		if !localHost(h) {
			return true
		}
	}
	return false
}

// dbHost: the host a database client connects to, from -h/--host, a
// connection URI or a host= keyword; "" when none is named.
func dbHost(s Segment) string {
	for i, w := range s.Words[1:] {
		switch {
		case (w == "-h" || w == "--host") && i+2 < len(s.Words):
			return s.Words[i+2]
		case strings.HasPrefix(w, "--host="):
			return strings.TrimPrefix(w, "--host=")
		case strings.HasPrefix(w, "-h") && len(w) > 2 && !strings.HasPrefix(w, "--") && s.Name() != "redis-cli":
			return w[2:]
		case strings.Contains(w, "://"):
			if u, err := url.Parse(w); err == nil {
				if h := u.Hostname(); h != "" {
					return h
				}
				return "localhost" // postgres:///db is the local socket
			}
		}
		for _, f := range strings.Fields(w) {
			if strings.HasPrefix(f, "host=") {
				return strings.TrimPrefix(f, "host=")
			}
		}
	}
	return ""
}

// dbLocal: the client connects to this laptop. A client that names no host
// counts as outside, since the shell profile's PGHOST, a service file or a
// default can point anywhere; a PGHOST or MYSQL_HOST given on the command
// itself is the one it uses.
func dbLocal(s Segment) bool {
	h := dbHost(s)
	if h == "" {
		h = dbHostEnv(s)
	}
	return h != "" && (localHost(h) || strings.HasPrefix(h, "/")) // "/" is a socket folder
}

// dbHostEnv: the host a PGHOST or MYSQL_HOST set on the command names, or
// "" (none, or not known until it runs).
func dbHostEnv(s Segment) string {
	want := "PGHOST"
	switch s.Name() {
	case "mysql", "mariadb", "mysqldump":
		want = "MYSQL_HOST"
	case "mongosh", "mongo", "redis-cli":
		return ""
	}
	for _, e := range s.Env {
		if name, val, _ := strings.Cut(e, "="); name == want && !strings.ContainsAny(val, "$`") {
			return val
		}
	}
	return ""
}

// dbKind: a database client pointed away from the laptop. A query that
// only reads is a read; anything else (a write, a script file, a restore,
// commands from stdin or an interactive session) is a write.
func dbKind(s Segment) string {
	if dbLocal(s) {
		return ""
	}
	name := s.Name()
	if name == "pg_restore" || name == "mongosh" || name == "mongo" || name == "redis-cli" {
		if name == "redis-cli" && s.Arg(1) != "" && redisReads[strings.ToLower(lastPositional(s))] {
			return ""
		}
		return OpsWrite
	}
	var query string
	for i, w := range s.Words {
		switch {
		case (w == "-c" || w == "--command" || w == "-e" || w == "--execute") && i+1 < len(s.Words):
			query += ";" + s.Words[i+1]
		case strings.HasPrefix(w, "--command="):
			query += ";" + strings.TrimPrefix(w, "--command=")
		case strings.HasPrefix(w, "--execute="):
			query += ";" + strings.TrimPrefix(w, "--execute=")
		case w == "-f" || w == "--file" || strings.HasPrefix(w, "--file="):
			return OpsWrite
		}
	}
	if len(s.Inputs) > 0 || s.Stdin != nil || s.Piped {
		return OpsWrite
	}
	if strings.TrimSpace(strings.ReplaceAll(query, ";", "")) == "" {
		return OpsWrite // an interactive session can do anything
	}
	for _, part := range strings.Split(query, ";") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if !reSQLRead.MatchString(part) || reSQLWrite.MatchString(stripStrings(part)) {
			return OpsWrite
		}
	}
	return ""
}

var redisReads = map[string]bool{}

func init() {
	for _, c := range []string{"get", "mget", "keys", "scan", "info", "ttl", "pttl", "type", "exists", "strlen", "hget", "hgetall", "hkeys", "hlen", "llen", "lrange", "scard", "smembers", "zcard", "zrange", "ping", "dbsize"} {
		redisReads[c] = true
	}
}

// lastPositional is the redis command word: the first non-flag word after
// the connection options (redis-cli -h host -p 6379 GET key).
func lastPositional(s Segment) string {
	skipNext := false
	for _, w := range s.Words[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(w, "-") {
			switch w {
			case "-h", "-p", "-a", "-n", "-u", "--user", "--pass", "--sni", "--cacert", "--cert", "--key":
				skipNext = true
			}
			continue
		}
		return w
	}
	return ""
}

// stripStrings drops quoted text from SQL, so a word inside a string
// ('update me') isn't read as a statement.
func stripStrings(q string) string {
	var b strings.Builder
	in := byte(0)
	for i := 0; i < len(q); i++ {
		ch := q[i]
		switch {
		case in != 0 && ch == in:
			in = 0
		case in != 0:
		case ch == '\'' || ch == '"':
			in = ch
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// devopsVerdict: the phase 3 rules for one command. A deploy by hand in a
// project whose pipeline deploys is refused; a risky command the approved
// plan lists is still confirmed each time; one it doesn't list asks.
func (c *hookCtx) devopsVerdict(s Segment, approved bool, v *verdicts) {
	if !c.setup.PartOn(devopsPartName) {
		return
	}
	kind := OpsKind(s)
	if kind == "" {
		return
	}
	if kind == OpsDeploy && c.setup != nil && c.setup.DeployPipeline {
		v.d("hand-deploy", "this project deploys through its pipeline, so Claude never deploys by hand. Merge or push through the usual flow (the user pushes), and tell the user what's ready. If the pipeline is down and a hand deploy is truly needed, that's the user's call to run themselves.")
		return
	}
	if !approved {
		// The 1a checks already ask about cloud CLIs, database clients,
		// docker push and curl. The rest (a deploy script, rsync to a host,
		// a migration against a shared database) ask here.
		if !coreAsksOutside(s) {
			v.a("outside-laptop", "this reaches outside your laptop ("+opsWhat[kind]+"). Production is never changed without the user's explicit OK.")
		}
		return
	}
	v.a("risky-ops", "this is in the plan you approved, but "+opsWhat[kind]+", so it asks each time. Check the target is right (not production unless you mean it).")
}
