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
	"helm":       {"list", "ls", "status", "get", "history", "show", "search"},
	"vercel":     {"ls", "list", "inspect", "logs", "whoami"},
	"fly":        {"status", "logs", "list", "ls", "info", "releases", "checks", "history"},
	"flyctl":     {"status", "logs", "list", "ls", "info", "releases", "checks", "history"},
	"heroku":     {"logs", "ps", "apps", "releases", "info", "status"},
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

// cloudRead: a cloud CLI command that only looks.
func cloudRead(s Segment) bool {
	name := s.Name()
	var words []string // the command's words, without flags or their values
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		if strings.HasPrefix(w, "-") {
			continue
		}
		words = append(words, strings.ToLower(w))
	}
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, " ")
	if reCloudSecret.MatchString(joined) || name == "kubectl" && (contains(words, "secret") || contains(words, "secrets")) {
		return false
	}
	switch name {
	case "aws":
		// aws <service> <operation>: describe-*, list-*, get-* (not secret
		// values), s3 ls, logs tail, sts get-caller-identity.
		if len(words) < 2 {
			return false
		}
		op := words[1]
		return strings.HasPrefix(op, "describe-") || strings.HasPrefix(op, "list-") || strings.HasPrefix(op, "get-") ||
			words[0] == "s3" && op == "ls" || words[0] == "logs" && (op == "tail" || op == "filter-log-events")
	case "gcloud", "az":
		// gcloud <group> ... list|describe; az <group> ... list|show.
		last := words[len(words)-1]
		for _, w := range words {
			if w == "list" || w == "describe" || w == "show" || w == "read" {
				last = w
			}
		}
		return last == "list" || last == "describe" || last == "show" || name == "gcloud" && last == "read"
	case "gh":
		if words[0] == "api" {
			return ghAPIRead(s)
		}
		if words[0] == "auth" && len(words) > 1 && words[1] == "status" {
			return true
		}
	case "terraform", "tofu":
		if words[0] == "state" {
			return len(words) > 1 && (words[1] == "list" || words[1] == "show")
		}
	}
	verbs := cloudReadVerbs[name]
	if verbs == nil {
		return false
	}
	// The read verb is the first or second word (gh pr view, kubectl get pods).
	for i, w := range words {
		if i > 1 {
			break
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
	name := s.Name()
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
		case w == "-f" || w == "--file" || strings.HasPrefix(w, "--file=") || w == "-i":
			return false
		}
	}
	if len(queries) == 0 || len(s.Inputs) > 0 {
		return false // interactive, or SQL from a file or pipe
	}
	for _, q := range queries {
		low := strings.ToLower(strings.TrimSpace(q))
		start := strings.Fields(strings.TrimLeft(low, "( "))
		if len(start) == 0 || reSQLChanges.MatchString(low) || reSQLWrite.MatchString(low) || strings.Contains(low, `\!`) || strings.Contains(low, `\copy`) || strings.Contains(low, `\i`) || strings.Contains(low, `\o`) || strings.Contains(low, `\w`) {
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
