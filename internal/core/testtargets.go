package core

import (
	"bufio"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Test runs point at throwaway targets (N13). Before a test run, the
// settings that pick a database, queue, cache or mail server are read as
// the tests would see them: the command's own variables, then the session's
// environment, then the test overlay (.env.test.local, .env.test,
// .env.testing) over .env. Values are only compared, never shown.

// Settings that point at a database, queue, cache or mail server, with an
// optional prefix (APP_DATABASE_URL).
var reServiceVar = regexp.MustCompile(`^(?:[A-Z0-9_]*_)?(DATABASE_URL|DB_URL|DB_HOST|DATABASE_HOST|POSTGRES_URL|POSTGRES_HOST|PGHOST|MYSQL_URL|MYSQL_HOST|MONGO_URL|MONGO_URI|MONGODB_URL|MONGODB_URI|MONGO_HOST|REDIS_URL|REDIS_HOST|AMQP_URL|RABBITMQ_URL|SMTP_HOST|MAIL_HOST|EMAIL_HOST)$`)

// Database settings among them: the ones a test run could share with
// development.
var reDBVar = regexp.MustCompile(`(DATABASE_URL|DB_URL|DB_HOST|DATABASE_HOST|POSTGRES_URL|POSTGRES_HOST|PGHOST|MYSQL_URL|MYSQL_HOST|MONGO_URL|MONGO_URI|MONGODB_URL|MONGODB_URI|MONGO_HOST)$`)

var (
	testEnvFiles = []string{".env.test.local", ".env.test", ".env.testing", ".env"}
	devEnvFiles  = []string{".env.development.local", ".env.local", ".env.development", ".env"}
)

// envSetting is one setting as a run would see it, and where it came from.
type envSetting struct {
	Value, From string
}

// readDotenv reads KEY=value lines (with "export", quotes and comments).
func readDotenv(p string) map[string]string {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		l = strings.TrimSpace(strings.TrimPrefix(l, "export "))
		k, v, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[k] = v
	}
	return out
}

// targetQuery: what a run would see besides the session's environment:
// settings from its own command line (set on it, exported or loaded from an
// env file earlier on the line) and the folders whose env files count,
// nearest first, relative to the repo root ("" is the root).
type targetQuery struct {
	Env  []string
	Dirs []string
}

// envChain: each service setting from the first source that sets it: the
// command line, the session's environment, then each folder's env files.
func envChain(root string, q targetQuery, procEnv []string, files []string) map[string]envSetting {
	out := map[string]envSetting{}
	set := func(k, v, from string) {
		if _, done := out[k]; !done && v != "" && reServiceVar.MatchString(k) { // empty is unset
			out[k] = envSetting{v, from}
		}
	}
	for _, e := range q.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			set(k, v, "the command")
		}
	}
	for _, e := range procEnv {
		if k, v, ok := strings.Cut(e, "="); ok {
			set(k, v, "the session's environment")
		}
	}
	dirs := append(append([]string{}, q.Dirs...), "")
	for _, d := range dirs {
		for _, f := range files {
			rel := filepath.ToSlash(filepath.Join(d, f))
			for k, v := range readDotenv(filepath.Join(root, filepath.FromSlash(rel))) {
				set(k, v, rel)
			}
		}
	}
	return out
}

var (
	reMySQLTCP    = regexp.MustCompile(`@tcp\(([^)]*)\)`)
	reRailsTestDB = regexp.MustCompile(`(?m)^test:`)
	reLaravelDB   = regexp.MustCompile(`name="DB_(DATABASE|CONNECTION)"`)
	// Shell variables in a value: replaced by a marker so the rest still
	// reads; a host that is only a variable stays unknown.
	reShellVar = regexp.MustCompile(`\$\{[^}]*\}|\$\([^)]*\)|\$[A-Za-z_][A-Za-z0-9_]*`)
)

const varMark = "plbvar"

// serviceHosts: the hosts a setting points at (a URL may list several), and
// the database it names, when it shows one. A host that is only a shell
// variable isn't known and is left out; ok is false when nothing is known.
func serviceHosts(v string) (hosts []string, db string, ok bool) {
	v = reShellVar.ReplaceAllString(strings.TrimSpace(v), varMark)
	keep := func(hs ...string) {
		for _, h := range hs {
			h = strings.TrimSpace(h)
			if hh, _, err := net.SplitHostPort(h); err == nil {
				h = hh // host:port, [v6]:port
			}
			if h = strings.Trim(h, "[]"); !strings.Contains(h, varMark) {
				hosts = append(hosts, h)
			}
		}
	}
	low := strings.ToLower(v)
	switch {
	case strings.HasPrefix(low, "sqlite") || strings.HasPrefix(low, "file:") || strings.HasPrefix(v, "/"):
		return []string{"localhost"}, v, true
	case strings.Contains(v, "@unix(") || strings.Contains(v, "@/"): // MySQL over a socket, or the default host
		after := v[strings.LastIndex(v, "/")+1:]
		db, _, _ = strings.Cut(after, "?")
		return []string{"localhost"}, db, true
	}
	if m := reMySQLTCP.FindStringSubmatch(v); m != nil {
		keep(m[1])
		_, after, _ := strings.Cut(v, ")/")
		db, _, _ = strings.Cut(after, "?")
		return hosts, db, len(hosts) > 0
	}
	if _, rest, found := strings.Cut(v, "://"); found {
		hostPart, path, _ := strings.Cut(rest, "/")
		query := ""
		if i := strings.Index(hostPart, "?"); i >= 0 {
			hostPart, query = hostPart[:i], hostPart[i+1:]
		}
		if i := strings.LastIndex(hostPart, "@"); i >= 0 {
			hostPart = hostPart[i+1:] // user and password
		}
		path, q2, _ := strings.Cut(path, "?")
		if query == "" {
			query = q2
		}
		port := ""
		if first, _, _ := strings.Cut(hostPart, ","); first != "" {
			if _, p, err := net.SplitHostPort(first); err == nil {
				port = p
			}
		}
		if hostPart == "" {
			if qv, err := url.ParseQuery(query); err == nil && qv.Get("host") != "" {
				hostPart = qv.Get("host") // postgres:///db?host=/var/run/postgresql
			} else {
				hostPart = "localhost"
			}
		}
		keep(strings.Split(hostPart, ",")...)
		return hosts, path + ":" + port, len(hosts) > 0
	}
	if strings.Contains(v, "=") { // key=value DSN: host=... dbname=...
		for _, f := range strings.Fields(v) {
			k, x, _ := strings.Cut(f, "=")
			switch strings.ToLower(k) {
			case "host":
				keep(x)
			case "dbname":
				db = x
			}
		}
		return hosts, db, len(hosts) > 0
	}
	keep(strings.Split(v, ",")...) // plain hosts (DB_HOST, KAFKA-style lists)
	return hosts, "", len(hosts) > 0
}

// serviceLocal: a test target on this laptop: what localHost accepts,
// any loopback address, a unix socket, or a one-word name such as a
// Docker Compose service ("db", "postgres").
func serviceLocal(h string) bool {
	h = strings.Trim(h, "[]")
	if h == "" || strings.HasPrefix(h, "/") || localHost(h) {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsUnspecified()
	}
	return !strings.Contains(h, ".") && !strings.Contains(h, ":")
}

// testDBSwitchedByFramework: Django, Rails, Laravel's phpunit.xml and
// Phoenix make their own test database, so the dev settings aren't what
// the tests use.
func testDBSwitchedByFramework(root string) bool {
	if _, err := os.Stat(filepath.Join(root, "manage.py")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(root, "config", "test.exs")); err == nil {
		return true
	}
	if b, err := os.ReadFile(filepath.Join(root, "config", "database.yml")); err == nil && reRailsTestDB.Match(b) {
		return true
	}
	for _, f := range []string{"phpunit.xml", "phpunit.xml.dist"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil && reLaravelDB.Match(b) {
			return true
		}
	}
	return false
}

// TestTargets is what a run would connect to that isn't throwaway:
// settings pointing off this laptop, and database settings the tests
// share with development. Only setting names and their sources are kept.
type TestTargets struct {
	Remote []string // "DATABASE_URL (from .env)"
	DevDB  []string
}

// CheckTestTargets: what a test run in root would connect to, with the
// command's own settings.
func CheckTestTargets(root string, cmdEnv []string, s *Setup) TestTargets {
	return checkTargets(root, targetQuery{Env: cmdEnv}, s, false)
}

// checkTargets reads the settings as a test run would see them (the test
// overlay over .env), or, for a migration, as development does.
func checkTargets(root string, q targetQuery, s *Setup, migration bool) TestTargets {
	var tt TestTargets
	files := testEnvFiles
	if migration {
		files = devEnvFiles
	}
	seen := envChain(root, q, os.Environ(), files)
	dev := envChain(root, targetQuery{Dirs: q.Dirs}, nil, devEnvFiles)
	framework := testDBSwitchedByFramework(root)
	names := make([]string, 0, len(seen))
	for k := range seen {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		if s != nil && contains(s.TestTargetsOK, k) {
			continue
		}
		e := seen[k]
		hosts, db, ok := serviceHosts(e.Value)
		if !ok {
			continue
		}
		label := k + " (from " + e.From + ")"
		remote := false
		for _, h := range hosts {
			remote = remote || !serviceLocal(h)
		}
		if remote {
			tt.Remote = append(tt.Remote, label)
			continue
		}
		if migration || framework || !reDBVar.MatchString(k) || db == "" || db == ":" {
			continue // a host alone doesn't say which database
		}
		d, found := dev[k]
		if !found || e.From == "the command" {
			continue
		}
		dh, ddb, ok := serviceHosts(d.Value)
		if ok && len(dh) > 0 && strings.EqualFold(dh[0], hosts[0]) && ddb == db && !strings.Contains(strings.ToLower(db), "test") {
			tt.DevDB = append(tt.DevDB, label)
		}
	}
	return tt
}

var (
	reMigration = regexp.MustCompile(`^(?:(?:npx|bunx|pnpx|yarn|pnpm(?: exec)?|bun x|bundle exec|poetry run|uv run|pipenv run) )?(?:prisma (?:migrate (?:dev|deploy|reset)|db push)|knex migrate:\S+|sequelize(?:-cli)? db:migrate\S*|drizzle-kit (?:push|migrate)|typeorm migration:run|(?:bin/)?rails db:\S+|rake db:\S+|python3? manage\.py migrate|alembic (?:upgrade|downgrade)|goose\b.*\b(?:up|down|reset|redo)\b|migrate\b.*\b(?:up|down|goto|force)\b|php artisan migrate\S*|flyway migrate|dbmate (?:up|down|migrate|rollback))|^(?:npm run|yarn(?: run)?|pnpm(?: run)?|bun run|make) \S*migrat`)
	// Test runners typed directly, not through plumbline run.
	reTestRunner = regexp.MustCompile(`^(?:(?:npx|bunx|pnpx|pnpm(?: exec)?|yarn|bun x|bundle exec|poetry run|uv run|pipenv run) )?(?:jest|vitest|mocha|ava|playwright test|cypress run|pytest|python3? -m pytest|go test|cargo test|rspec|phpunit|vendor/bin/phpunit|php artisan test|python3? manage\.py test|mix test|(?:npm|yarn|pnpm|bun) (?:run )?test(?::\S*)?|make test)\b`)
)

// isMigration: the segment runs database migrations.
func isMigration(s Segment) bool {
	return reMigration.MatchString(strings.Join(s.Words, " "))
}

// runsTests: plumbline run test, related or e2e, or a test runner typed
// directly.
func runsTests(s Segment) bool {
	if isPlumblineBin(s.Name()) {
		return s.Arg(1) == "run" && (s.Arg(2) == "test" || s.Arg(2) == "related" || s.Arg(2) == "e2e")
	}
	return reTestRunner.MatchString(strings.Join(s.Words, " "))
}

// queryFor: the settings and folders segs[i] runs with. Settings set on it
// come first, then those exported or loaded from an env file earlier on the
// line (the later one wins). Its folder counts, and for plumbline run, the
// folder the saved command changes into.
func queryFor(root string, segs []Segment, i int, s *Setup) targetQuery {
	q := targetQuery{Env: append([]string{}, segs[i].Env...)}
	for j := i - 1; j >= 0; j-- {
		p := segs[j]
		if p.Exported {
			q.Env = append(q.Env, p.Env...)
		}
		if n := p.Name(); (n == "source" || n == ".") && p.Arg(1) != "" {
			f := p.Arg(1)
			if !filepath.IsAbs(f) {
				f = filepath.Join(p.Dir, f)
			}
			if IsInside(f, root) {
				for k, v := range readDotenv(f) {
					q.Env = append(q.Env, k+"="+v)
				}
			}
		}
	}
	addDir := func(d string) {
		if d == "" || !IsInside(d, root) {
			return
		}
		if rel, err := filepath.Rel(root, d); err == nil && rel != "." && !contains(q.Dirs, rel) {
			q.Dirs = append(q.Dirs, filepath.ToSlash(rel))
		}
	}
	addDir(segs[i].Dir)
	if isPlumblineBin(segs[i].Name()) && s != nil {
		kind := segs[i].Arg(2)
		if kind == "related" {
			kind = KindRelated
		}
		if cmd := savedCommand(s, kind); cmd != "" {
			if run := ParseShell(cmd, root); len(run) > 0 {
				addDir(run[len(run)-1].Dir)
			}
		}
	}
	return q
}

// testTargetVerdict refuses a test run that would reach a database or
// service off this laptop, and asks once per task when it would use the dev
// database. A migration that reaches off the laptop asks.
func (c *hookCtx) testTargetVerdict(segs []Segment, i int, v *verdicts) {
	if c.root == "" {
		return
	}
	s := segs[i]
	tests, migration := runsTests(s), isMigration(s)
	if !tests && !migration {
		return
	}
	tt := checkTargets(c.root, queryFor(c.root, segs, i, c.setup), c.setup, migration)
	if migration {
		if len(tt.Remote) > 0 {
			v.a("migration-target", "this migration would run against a database that isn't on this laptop ("+strings.Join(tt.Remote, ", ")+"). Check it's the target you mean before allowing.")
		}
		return
	}
	if len(tt.Remote) > 0 {
		v.d("test-target", "the test run would connect to a database or service that isn't on this laptop ("+strings.Join(tt.Remote, ", ")+"), and tests can change or wipe what they connect to. Point the tests at a local one (set it in .env.test), or, if the tests never connect to it, the user can list that setting under test_targets_ok in the setup (/plumbline:setup).")
		return
	}
	if len(tt.DevDB) > 0 && (c.task == nil || !c.task.DevDBOK) {
		v.a("test-dev-db", "the tests would use the same database as development ("+strings.Join(tt.DevDB, ", ")+", with no test value in .env.test), so they could change or wipe your dev data. Allowing it counts for the rest of this task; or point the tests at their own database in .env.test.")
	}
}

// noteDevDBAllowed: the user allowed a test run on the dev database (the
// command ran), so this task doesn't ask again.
func (c *hookCtx) noteDevDBAllowed(segs []Segment) {
	if c.task == nil || c.task.DevDBOK {
		return
	}
	for i := range segs {
		if runsTests(segs[i]) && len(checkTargets(c.root, queryFor(c.root, segs, i, c.setup), c.setup, false).DevDB) > 0 {
			c.task.DevDBOK = true
			_ = SaveTask(c.task)
			return
		}
	}
}

// MarkDevDBAllowed is plumbline run's side of it: a test run that uses the
// dev database only gets here after the user allowed it (or typed it), and
// a failing run may not reach PostToolUse, so the run itself records it.
func MarkDevDBAllowed(root, taskID string, s *Setup) {
	if taskID == "" || len(CheckTestTargets(root, nil, s).DevDB) == 0 {
		return
	}
	unlock := LockTask(root, taskID)
	defer unlock()
	if t, err := LoadTask(root, taskID); err == nil && !t.DevDBOK {
		t.DevDBOK = true
		_ = SaveTask(t)
	}
}
