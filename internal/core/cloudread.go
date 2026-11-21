package core

import (
	"regexp"
	"strings"
)

// Looking at systems outside the laptop doesn't change them, so it needn't
// ask (decided with the user 2026-11-19: "checking and verifying ... no
// need", changes are what's hard to undo). These are the read-only forms
// of the cloud and database tools Plumbline otherwise asks about. Anything
// that changes something, or prints a secret's value, still asks.

// cloudReadVerbs: per tool, the words that make a command a read, matched
// against the command's words (not flags). A tool missing here always asks.
var cloudReadVerbs = map[string][]string{
	"gh":         {"view", "list", "ls", "diff", "checks", "status", "search", "watch"},
	"glab":       {"view", "list", "ls", "diff", "status", "ci-view"},
	"kubectl":    {"get", "describe", "logs", "top", "explain", "api-resources", "api-versions", "version", "cluster-info"},
	"oc":         {"get", "describe", "logs", "top", "explain", "api-resources", "api-versions", "version", "cluster-info", "whoami", "status", "projects"},
	"gsutil":     {"ls", "du", "stat", "cat"},
	"bq":         {"ls", "show", "head"},
	"eksctl":     {"get", "info", "version"},
	"argocd":     {"list", "get", "version"},
	"helm":       {"list", "ls", "status", "get", "history", "show", "search", "lint"},
	"vercel":     {"ls", "list", "inspect", "logs", "whoami"},
	"fly":        {"status", "logs", "list", "ls", "info", "releases", "checks", "history"},
	"flyctl":     {"status", "logs", "list", "ls", "info", "releases", "checks", "history"},
	"heroku":     {"logs", "ps", "apps", "releases", "info", "status", "apps:info", "domains", "addons", "auth:whoami", "pg:info", "releases:info", "ps:type", "regions", "stack"},
	"netlify":    {"status", "logs", "sites:list", "watch"},
	"doctl":      {"list", "get", "ls"},
	"railway":    {"status", "logs", "list"},
	"render":     {"list", "logs", "services"},
	"wrangler":   {"whoami", "tail", "list"},
	"eb":         {"status", "logs", "health", "list", "events"},
	"sam":        {"logs", "list", "validate"},
	"cdk":        {"diff", "synth", "ls", "list", "doctor"},
	"terraform":  {"plan", "show", "validate", "providers", "version", "graph"},
	"tofu":       {"plan", "show", "validate", "providers", "version", "graph"},
	"pulumi":     {"preview", "whoami", "about", "version"},
	"firebase":   {"projects:list", "apps:list", "hosting:channel:list", "functions:list"},
	"serverless": {"info", "logs", "print"},
	"sls":        {"info", "logs", "print"},
}

// Reads that print a secret's value: they ask, like opening .env.
var reCloudSecret = regexp.MustCompile(`(?i)(^|\s)(secret|secrets|get-secret-value|get-parameters?|keyvault|config:get|env\s+pull|output|vault|credentials?|token|auth\s+token)(\s|$)`)

// versionOrHelp (VER1): the tool asked only for its version or help, which
// it prints without connecting anywhere (aws --version, psql -V). A word
// like "version" or "help", or -v/-h, means a host or database name, verbose
// or host for ssh, scp and the database clients (ssh help, psql -h), so
// those get only the unambiguous flags.
func versionOrHelp(s Segment) bool {
	if len(s.Words) < 2 {
		return false
	}
	name := s.Name()
	strict := dbClients[name] || name == "ssh" || name == "scp"
	for i := 1; i < len(s.Words); i++ {
		if s.dyn(i) {
			return false
		}
		switch s.Words[i] {
		case "--version", "-V", "--help":
		case "-v", "-version", "-h", "version", "help":
			if strict {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// cloudValFlags: per tool, the global and common flags that take a value as
// the next word (aws --profile prod s3 ls), so the value isn't read as the
// command (READ2).
var cloudValFlags = map[string]map[string]bool{
	"aws":     set("--profile --region --output --query --endpoint-url --color --ca-bundle --cli-read-timeout --cli-connect-timeout --page-size --max-items --starting-token"),
	"kubectl": set("--context -n --namespace --kubeconfig --cluster --user -o --output -l --selector --field-selector -c --container --tail --since --since-time -s --server --token --as --as-group --request-timeout --sort-by --template"),
	"oc":      set("--context -n --namespace --kubeconfig --cluster --user -o --output -l --selector --field-selector -c --container --tail --since -s --server --token --as"),
	"helm":    set("--kube-context -n --namespace --kubeconfig --kube-apiserver --kube-token -o --output --max --filter"),
	"gcloud":  set("--project --format --account --region --zone --configuration --impersonate-service-account --filter --limit --sort-by --page-size --verbosity"),
	"az":      set("--subscription -o --output -g --resource-group --query -n --name --location -l"),
	"doctl":   set("--context -t --access-token -o --output --format"),
	"heroku":  set("-a --app -r --remote"),
	"fly":     set("-a --app -c --config -o --org -r --region"),
	"flyctl":  set("-a --app -c --config -o --org -r --region"),
	"gh":      set("-R --repo -L --limit --json --jq --template --branch --workflow --env"),
	"glab":    set("-R --repo -P --per-page"),
	"argocd":  set("--server --auth-token --grpc-web-root-path -o --output -p --project"),
	"eksctl":  set("--region --profile -o --output --name --cluster"),
	"bq":      set("--project_id --location --format --dataset_id -n --max_results"),
}

// cloudSecretFlags: flags that make a read print a secret's value.
var cloudSecretFlags = set("--with-decryption --show-token --show-secrets --reveal --raw --flatten --decrypt --show-sensitive --include-values")

// cloudEndpointFlags: per tool, flags that point it at another server or
// login file. The tool would send the user's credentials there, or run
// what that file says, so the command isn't a plain look (READ2).
var cloudEndpointFlags = map[string]map[string]bool{
	"aws":     set("--endpoint-url"),
	"kubectl": set("-s --server --kubeconfig --token --as --as-group --certificate-authority --client-certificate --client-key"),
	"oc":      set("-s --server --kubeconfig --token --as --certificate-authority"),
	"helm":    set("--kubeconfig --kube-apiserver --kube-token --kube-as-user"),
	"argocd":  set("--server --auth-token --config --header"),
	"gsutil":  set("-o"),
	"bq":      set("--api --api_version"),
	"doctl":   set("--api-url -u --config"),
	"gcloud":  set("--access-token-file --impersonate-service-account"),
	"az":      set("--subscription-endpoint"),
}

// pointsElsewhere: the command names another server or login file.
func pointsElsewhere(s Segment) bool {
	flags := cloudEndpointFlags[s.Name()]
	for _, w := range s.Words[1:] {
		f, _, _ := strings.Cut(w, "=")
		if flags[f] || f == "--endpoint-url" || f == "--api-url" {
			return true
		}
		if len(w) > 2 && w[0] == '-' && w[1] != '-' && flags[w[:2]] {
			return true // -oValue, -uhttps://...
		}
	}
	return false
}

// cloudWords: a cloud command's words without its flags or their values,
// lower-cased, and whether a flag asks for a secret's value.
func cloudWords(s Segment) (words []string, secretFlag bool) {
	vals := cloudValFlags[s.Name()]
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		if strings.HasPrefix(w, "-") {
			flag := w
			if k := strings.Index(w, "="); k > 0 {
				flag = w[:k]
			}
			if cloudSecretFlags[flag] {
				secretFlag = true
			}
			if s.Name() == "gh" && (flag == "-t" || flag == "--show-token") && contains(words, "auth") {
				secretFlag = true
			}
			if vals[w] {
				i++ // the flag's value
			}
			continue
		}
		words = append(words, strings.ToLower(w))
	}
	return words, secretFlag
}

// cloudShowsSecret: a cloud command that prints a secret's value (a token,
// key or password), so it asks like opening .env. Listing secrets' names
// doesn't: gh secret list, kubectl get secrets without -o.
func cloudShowsSecret(s Segment) bool {
	words, secretFlag := cloudWords(s)
	if secretFlag {
		return true
	}
	name := s.Name()
	joined := strings.Join(words, " ")
	if name == "kubectl" || name == "oc" {
		if name == "oc" && len(words) > 0 && words[0] == "whoami" && segHasFlag(s, "-t", "--show-token") {
			return true
		}
		if len(words) > 0 && (words[0] == "get" || words[0] == "describe") && segHasFlag(s, "-f", "--filename", "-k", "--kustomize", "-R") && !onlyFlags(s, secretListFlags) {
			return true // a file can define Secrets, printed with their values
		}
		if kubeVerbose(s) {
			return true // verbose logging (level 6 and up) prints requests and responses
		}
		if names := secretResource(words); names && len(words) > 0 {
			// get secrets lists names; any output format, template or other
			// verb (edit, create) can print or carry the values. describe
			// shows only sizes.
			if words[0] == "describe" && onlyFlags(s, secretListFlags) {
				return false
			}
			return words[0] != "get" || !onlyFlags(s, secretListFlags)
		}
		return false
	}
	if namesOnly := (contains(words, "secret") || contains(words, "secrets")) && (contains(words, "list") || contains(words, "ls")) && !contains(words, "access"); namesOnly {
		return false
	}
	if name == "heroku" && len(words) > 0 && (words[0] == "config" || strings.HasPrefix(words[0], "config:get")) {
		return true
	}
	if name == "az" && contains(words, "keys") && contains(words, "list") {
		return true
	}
	if name == "doctl" {
		if len(words) > 0 && words[0] == "databases" && !contains(words, "list") {
			return true // doctl databases get, connection, user, pool print passwords or URIs with them
		}
		for _, w := range words {
			switch w {
			case "connection", "user", "users", "kubeconfig", "credentials", "token", "password", "spec", "pool":
				return true
			}
		}
	}
	if name == "aws" && len(words) >= 2 {
		op := words[1]
		if strings.Contains(op, "token") || strings.Contains(op, "credential") || strings.Contains(op, "password") ||
			strings.Contains(op, "secret-value") || strings.Contains(op, "access-details") || op == "get-login" {
			return true
		}
	}
	return reCloudSecret.MatchString(joined)
}

// segHasFlag: the segment has one of the flags (alone, =value, or -oyaml).
func segHasFlag(s Segment, flags ...string) bool {
	for _, w := range s.Words[1:] {
		for _, f := range flags {
			if w == f || strings.HasPrefix(w, f+"=") || len(f) == 2 && strings.HasPrefix(w, f) && len(w) > 2 && !strings.HasPrefix(w, "--") {
				return true
			}
		}
	}
	return false
}

// cloudRead: a cloud CLI command that only looks.
func cloudRead(s Segment) bool {
	if versionOrHelp(s) {
		return true
	}
	name := s.Name()
	words, _ := cloudWords(s)
	if len(words) == 0 || s.dynArgs() || !harmlessCloudEnv(s) || cloudShowsSecret(s) || pointsElsewhere(s) {
		return false
	}
	for _, w := range words {
		if cloudChangeWords[w] {
			return false // heroku apps destroy x, gcloud compute ssh vm ... list
		}
	}
	switch name {
	case "aws":
		// aws <service> <operation>: describe-*, list-*, get-* (not secret
		// values), s3 ls, logs tail, sts get-caller-identity.
		if len(words) < 2 {
			return false
		}
		op := words[1]
		if strings.HasPrefix(op, "get-object") {
			return false // writes the object to a local file
		}
		return strings.HasPrefix(op, "describe-") || strings.HasPrefix(op, "list-") || strings.HasPrefix(op, "get-") ||
			words[0] == "s3" && op == "ls" || words[0] == "logs" && (op == "tail" || op == "filter-log-events") ||
			words[0] == "configure" && op == "list"
	case "gcloud", "az":
		// gcloud <group> ... list|describe; az <group> ... list|show.
		last := words[len(words)-1]
		for _, w := range words {
			if w == "list" || w == "describe" || w == "show" || w == "read" {
				last = w
			}
		}
		if name == "gcloud" && (words[0] == "info" || len(words) > 1 && words[0] == "config" && (words[1] == "get-value" || words[1] == "list")) {
			return true
		}
		return last == "list" || last == "describe" || last == "show" || name == "gcloud" && last == "read"
	case "gh":
		if words[0] == "api" {
			return ghAPIRead(s)
		}
		if words[0] == "auth" && len(words) > 1 && words[1] == "status" {
			return true
		}
	case "kubectl", "oc":
		if len(words) > 1 {
			switch words[0] {
			case "config":
				return words[1] == "get-contexts" || words[1] == "current-context" || words[1] == "view" || words[1] == "get-clusters" || words[1] == "get-users"
			case "rollout":
				return words[1] == "status" || words[1] == "history"
			case "auth":
				return words[1] == "can-i" || words[1] == "whoami"
			}
		}
	case "terraform", "tofu":
		if words[0] == "state" || words[0] == "workspace" {
			return len(words) > 1 && (words[1] == "list" || words[1] == "show")
		}
	}
	if name == "doctl" {
		// doctl <group> [<resource>] <verb>: the verb sits second or third.
		r := func(i int) bool {
			return i < len(words) && (words[i] == "list" || words[i] == "get" || words[i] == "ls")
		}
		return r(1) || r(2) && !strings.Contains(words[1], "-")
	}
	verbs := cloudReadVerbs[name]
	if verbs == nil {
		return false
	}
	// The read verb is the first or second word (gh pr view, kubectl get pods).
	for i, w := range words {
		if i > 1 || i == 1 && strings.Contains(words[0], ":") {
			break // heroku apps:destroy apps: the first word is the command
		}
		if contains(verbs, w) {
			return true
		}
	}
	return false
}

// ghAPIRead: gh api with no method other than GET and no fields (fields
// make it a POST).
func ghAPIRead(s Segment) bool {
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		switch {
		case w == "-X" || w == "--method":
			if i+1 < len(s.Words) && !strings.EqualFold(s.Words[i+1], "GET") {
				return false
			}
		case strings.HasPrefix(w, "--method=") && !strings.EqualFold(strings.TrimPrefix(w, "--method="), "GET"),
			strings.HasPrefix(w, "-X") && len(w) > 2 && !strings.EqualFold(w[2:], "GET"):
			return false
		case w == "-f" || w == "-F" || w == "--field" || w == "--raw-field" || w == "--input" ||
			strings.HasPrefix(w, "--field=") || strings.HasPrefix(w, "--raw-field=") || strings.HasPrefix(w, "--input="):
			return false
		}
	}
	return true
}

// supabaseRead: supabase CLI commands that only look at the hosted project.
func supabaseRead(s Segment) bool {
	if versionOrHelp(s) {
		return true
	}
	a1, a2 := s.Arg(1), s.Arg(2)
	switch a1 {
	case "start", "stop", "status", "projects", "orgs", "inspect":
		return a1 != "projects" && a1 != "orgs" || a2 == "list"
	case "functions", "branches", "migration", "secrets", "backups", "snippets":
		return a2 == "list" // secrets list shows names, not values
	}
	return false
}

// SQL that only reads: no statement that writes, changes the schema, grants,
// copies or calls a function that might.
var reSQLChanges = regexp.MustCompile(`(?i)\b(insert|update|delete|merge|upsert|drop|alter|create|truncate|grant|revoke|copy|call|do|vacuum|reindex|cluster|refresh|lock|comment|security|set\s+role|replace|load|rename|into)\b|\bselect\b[^;]*\b(nextval|setval|pg_terminate_backend|pg_cancel_backend|pg_reload_conf|lo_import|lo_export|dblink)\b`)

// dbReadOnly: a database client given one or more queries on the command
// line (psql -c, mysql -e) that only read. An interactive client, a file of
// SQL (-f) or a query that might write still asks.
func dbReadOnly(s Segment) bool {
	if versionOrHelp(s) {
		return true
	}
	name := s.Name()
	if !harmlessDBEnv(s) {
		return false
	}
	if name == "redis-cli" {
		return redisRead(s)
	}
	var queries []string
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		switch {
		case name == "psql" && (w == "-c" || w == "--command"), (name == "mysql" || name == "mariadb") && (w == "-e" || w == "--execute"):
			if i+1 >= len(s.Words) || s.dyn(i+1) {
				return false
			}
			queries = append(queries, s.Words[i+1])
			i++
		case strings.HasPrefix(w, "--command="), strings.HasPrefix(w, "--execute="):
			queries = append(queries, w[strings.Index(w, "=")+1:])
		case w == "-f" || w == "--file" || strings.HasPrefix(w, "--file=") || w == "-i",
			strings.HasPrefix(w, "-o"), strings.HasPrefix(w, "--o"), strings.HasPrefix(w, "-L"), strings.HasPrefix(w, "--log"),
			dbRiskyFlag(w):
			return false
		}
	}
	if len(queries) == 0 || len(s.Inputs) > 0 {
		return false // interactive, or SQL from a file or pipe
	}
	for _, q := range queries {
		low := strings.ToLower(strings.TrimSpace(q))
		start := strings.Fields(strings.TrimLeft(low, "( "))
		if len(start) == 0 || reSQLChanges.MatchString(low) || reSQLWrite.MatchString(low) || reClientCmd.MatchString(q) {
			return false
		}
		switch start[0] {
		case "select", "with", "show", "explain", "table", "values", `\d`, `\dt`, `\d+`, `\dn`, `\df`, `\di`, `\dv`, `\l`, `\du`, "describe", "desc":
		default:
			if !strings.HasPrefix(start[0], `\d`) {
				return false
			}
		}
		if strings.HasPrefix(start[0], "explain") && strings.Contains(low, "analyze") {
			return false // EXPLAIN ANALYZE runs the query
		}
	}
	return true
}

// redisReadCmds: redis commands that only read.
var redisReadCmds = set("get mget getrange strlen exists type ttl pttl expiretime keys scan dbsize info ping echo time hget hmget hgetall hkeys hvals hlen hexists hscan hstrlen lrange llen lindex smembers scard sismember smismember sscan srandmember zrange zrangebyscore zrevrange zcard zscore zrank zrevrank zscan zcount xlen xrange xrevrange xinfo pfcount bitcount getbit memory lastsave role")

// redisValFlags: redis-cli flags that take a value.
var redisValFlags = set("-h -p -s -a -u -n -r -i -d --user --pass --sni --cacert --cacertdir --cert --key --tls-ciphers --tls-ciphersuites --pattern --count --quoted-pattern")

// redisRead: redis-cli given one command on the line that only reads
// (READ2). Interactive, --eval, --pipe or anything else still asks.
func redisRead(s Segment) bool {
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		if s.dyn(i) {
			return false
		}
		if strings.HasPrefix(w, "-") {
			switch w {
			case "--eval", "--pipe", "--rdb", "--functions-rdb", "--replica", "--memkeys-samples", "-x":
				return false
			}
			if redisValFlags[w] {
				i++
			}
			continue
		}
		low := strings.ToLower(w)
		if low == "memory" {
			return i+1 < len(s.Words) && strings.EqualFold(s.Words[i+1], "usage") || i+1 < len(s.Words) && strings.EqualFold(s.Words[i+1], "stats")
		}
		return redisReadCmds[low]
	}
	return false
}

// secretResource: the kubectl words name secrets (secret, secrets,
// secret/db, all,secrets, secret.v1, secrets.v1.).
func secretResource(words []string) bool {
	for _, w := range words {
		for _, part := range strings.Split(strings.ToLower(w), ",") {
			part, _, _ = strings.Cut(part, "/")
			part, _, _ = strings.Cut(part, ".")
			if part == "secret" || part == "secrets" {
				return true
			}
		}
	}
	return false
}

// secretListFlags: kubectl flags that only pick which secrets to list, so
// get secrets with only these prints names, not values.
var secretListFlags = set("-n --namespace --context -l --selector -A --all-namespaces --field-selector --no-headers --show-labels --show-kind --chunk-size --request-timeout -L --label-columns --sort-by --ignore-not-found --cluster --user")

// onlyFlags: every flag on the line is in the set (alone or with =value).
func onlyFlags(s Segment, ok map[string]bool) bool {
	for _, w := range s.Words[1:] {
		if !strings.HasPrefix(w, "-") {
			continue
		}
		f, _, _ := strings.Cut(w, "=")
		if !ok[f] {
			return false
		}
	}
	return true
}

// cloudChangeWords: words that make a cloud command a change wherever they
// stand (heroku apps destroy x).
var cloudChangeWords = set("destroy delete remove rm create set unset add update restart scale rollback promote transfer rename kill drop apply patch edit replace annotate cordon drain taint exec attach login logout ssh scp")

// harmlessCloudEnv: settings on the line that only pick a profile, region
// or pager. Others (KUBECONFIG, AWS_ENDPOINT_URL, ARGOCD_SERVER) point the
// tool at another server or login file.
func harmlessCloudEnv(s Segment) bool {
	for _, e := range s.Env {
		name, _, _ := strings.Cut(e, "=")
		switch name {
		case "AWS_PROFILE", "AWS_REGION", "AWS_DEFAULT_REGION", "AWS_PAGER", "AWS_DEFAULT_OUTPUT", "CLOUDSDK_CORE_PROJECT", "PAGER", "NO_COLOR", "TERM", "GH_PAGER":
		default:
			return false
		}
	}
	return true
}

// reClientCmd: client commands inside a query that write a file, run a
// program or read SQL from a file (psql \o \w \g \s \i \! \copy, mysql
// source, tee, system, pager, \. \T \!).
var reClientCmd = regexp.MustCompile(`\\[.!oOwWiITePrR]|\\copy|\\gx?\s+\S|\\s\s+\S|(?i)(^|;)\s*(source|tee|system|pager|nopager|edit|charset|connect|ssl_session_data_print|prompt|rehash|use)(\s|$|;)`)

// dbRiskyFlag: a mysql or mariadb option (any spelling: _ or -, loose-
// prefix) that runs SQL or a program or writes a file.
func dbRiskyFlag(w string) bool {
	if !strings.HasPrefix(w, "--") {
		return false
	}
	f, _, _ := strings.Cut(strings.TrimPrefix(w, "--"), "=")
	f = strings.TrimPrefix(strings.ReplaceAll(f, "_", "-"), "loose-")
	for _, bad := range []string{"init-command", "tee", "pager", "defaults-file", "defaults-extra-file", "plugin-dir", "system-command", "default-auth", "load-data-local-dir"} {
		if f == bad || strings.HasPrefix(bad, f) && len(f) >= 3 {
			return true
		}
	}
	return false
}

// harmlessDBEnv: settings on the line that only pick the server, user or
// password. Others (PSQLRC, MYSQL_HOME, PAGER) load a startup file or run a
// program.
func harmlessDBEnv(s Segment) bool {
	for _, e := range s.Env {
		name, _, _ := strings.Cut(e, "=")
		switch name {
		case "PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGSSLMODE", "PGCONNECT_TIMEOUT", "PGAPPNAME",
			"MYSQL_PWD", "MYSQL_HOST", "MYSQL_TCP_PORT", "REDISCLI_AUTH", "NO_COLOR", "TERM":
		default:
			return false
		}
	}
	return true
}

// kubeVerbose: kubectl -v at level 6 or more (or a level it can't read),
// which logs requests and responses, Secret values too.
func kubeVerbose(s Segment) bool {
	for i, w := range s.Words[1:] {
		var lvl string
		switch {
		case w == "-v" || w == "--v":
			if i+2 < len(s.Words) {
				lvl = s.Words[i+2]
			}
		case strings.HasPrefix(w, "-v=") || strings.HasPrefix(w, "--v="):
			_, lvl, _ = strings.Cut(w, "=")
		case strings.HasPrefix(w, "-v") && len(w) > 2 && !strings.HasPrefix(w, "--"):
			lvl = w[2:]
		default:
			continue
		}
		if len(lvl) != 1 || lvl[0] < '0' || lvl[0] > '5' {
			return true
		}
	}
	return false
}
