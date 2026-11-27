package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Affected tests in nx, turbo and cargo workspaces: the tool lists its
// units (projects, packages, crates), Plumbline maps the changed files to
// them, and the tool runs the units and what depends on them. Only test
// commands whose every word is on the lists below are derived; anything
// else runs the full suite, as before.

type MonorepoCmd struct {
	Tool   string   // "nx", "turbo" or "cargo"
	Prefix []string // the runner: npx, pnpm [exec], yarn or bunx
	words  []string // the related command without the prefix, before {files}
	tail   []string // cargo: "--" and what follows
}

var listTimeout = 2 * time.Minute

var (
	reNxTarget   = regexp.MustCompile(`^[\w:.-]+(,[\w:.-]+)*$`)
	reNxFlag     = regexp.MustCompile(`^(--parallel(=\d+)?|--skip-nx-cache|--skipNxCache|--verbose|--output-style=static|--configuration=[\w-]+)$`)
	reTurboTask  = regexp.MustCompile(`^[\w:.-]+$`)
	reTurboFlag  = regexp.MustCompile(`^(--continue|--force|--no-cache|--no-daemon|--concurrency=\d+%?|--output-logs=[\w-]+|--log-order=[\w-]+|--log-prefix=[\w-]+|--ui=[\w-]+|--env-mode=[\w-]+)$`)
	reCargoFlag  = regexp.MustCompile(`^(--all-features|--no-default-features|--release|--locked|--offline|--frozen|-q|--quiet|-v|--verbose|--all-targets|--tests|--jobs=\d+|--profile=[\w-]+|--target=[\w.-]+)$`)
	reMonoUnsafe = "|&;<>()$`\\\"'*?[]{}#~\n"
)

// ParseMonorepoTest: the test command as an nx run-many, turbo run or cargo
// test that Plumbline can narrow to the affected units.
func ParseMonorepoTest(test string) (MonorepoCmd, bool) {
	var c MonorepoCmd
	test = strings.TrimSpace(test)
	if test == "" || strings.ContainsAny(test, reMonoUnsafe) {
		return c, false
	}
	w := strings.Fields(test)
	switch {
	case w[0] == "pnpm" && len(w) > 1 && w[1] == "exec":
		c.Prefix, w = []string{"pnpm", "exec"}, w[2:]
	case w[0] == "npx" || w[0] == "pnpm" || w[0] == "yarn" || w[0] == "bunx":
		c.Prefix, w = []string{w[0]}, w[1:]
	}
	if len(w) < 2 {
		return c, false
	}
	var ok bool
	switch {
	case w[0] == "nx" && w[1] == "run-many":
		c.Tool, ok = "nx", c.parseNx(w[2:])
	case w[0] == "turbo":
		c.Tool, ok = "turbo", c.parseTurbo(w[1:])
	case w[0] == "cargo" && w[1] == "test" && len(c.Prefix) == 0:
		c.Tool, ok = "cargo", c.parseCargo(w[2:])
	}
	return c, ok
}

func (c *MonorepoCmd) parseNx(rest []string) bool {
	c.words = []string{"nx", "affected"}
	target := false
	for i := 0; i < len(rest); i++ {
		w := rest[i]
		switch {
		case w == "--all":
		case w == "-t" || w == "--target" || w == "--targets":
			if i+1 >= len(rest) || !reNxTarget.MatchString(rest[i+1]) {
				return false
			}
			c.words = append(c.words, w, rest[i+1])
			i++
			target = true
		case strings.HasPrefix(w, "--target=") || strings.HasPrefix(w, "--targets="):
			if !reNxTarget.MatchString(w[strings.Index(w, "=")+1:]) {
				return false
			}
			c.words = append(c.words, w)
			target = true
		case reNxFlag.MatchString(w):
			c.words = append(c.words, w)
		default:
			return false
		}
	}
	return target
}

func (c *MonorepoCmd) parseTurbo(rest []string) bool {
	c.words = []string{"turbo"}
	if len(rest) > 0 && rest[0] == "run" {
		c.words, rest = append(c.words, "run"), rest[1:]
	}
	tasks, flags, cont := 0, false, false
	for _, w := range rest {
		switch {
		case !strings.HasPrefix(w, "-") && !flags && reTurboTask.MatchString(w) && w != "run":
			tasks++
		case reTurboFlag.MatchString(w):
			flags = true
			if w == "--continue" {
				cont = true
			}
		default:
			return false
		}
		c.words = append(c.words, w)
	}
	if !cont {
		c.words = append(c.words, "--continue")
	}
	return tasks > 0
}

func (c *MonorepoCmd) parseCargo(rest []string) bool {
	c.words = []string{"cargo", "test", "--no-fail-fast"}
	for i, w := range rest {
		switch {
		case w == "--":
			c.tail = rest[i:]
			return true
		case w == "--workspace" || w == "--all" || w == "--no-fail-fast":
		case reCargoFlag.MatchString(w):
			c.words = append(c.words, w)
		default:
			return false
		}
	}
	return true
}

func (c MonorepoCmd) template() string {
	w := append(append(append([]string{}, c.Prefix...), c.words...), "{files}")
	return strings.Join(append(w, c.tail...), " ")
}

// graphName: the name AffectedGraph gives the tool's graph.
func (c MonorepoCmd) graphName() string {
	if c.Tool == "cargo" {
		return "cargo metadata"
	}
	return c.Tool
}

var scriptRunner = map[string][]string{
	"npm test": {"npx"}, "npm run test": {"npx"}, "npm t": {"npx"},
	"pnpm test": {"pnpm", "exec"}, "pnpm run test": {"pnpm", "exec"},
	"yarn test": {"yarn"}, "yarn run test": {"yarn"},
}

// monorepoOf: the setup's test command as a monorepo command: the command
// itself, or, for a bare npm, pnpm or yarn test, the root package.json's
// test script run through the manager's runner.
func monorepoOf(s *Setup) (MonorepoCmd, bool) {
	if s == nil {
		return MonorepoCmd{}, false
	}
	test := strings.Join(strings.Fields(s.Commands.Test), " ")
	if c, ok := ParseMonorepoTest(test); ok {
		return c, true
	}
	runner, ok := scriptRunner[test]
	if !ok || s.Root == "" {
		return MonorepoCmd{}, false
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "package.json"))
	if err != nil {
		return MonorepoCmd{}, false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	// npm and yarn run pretest and posttest around the test script, and
	// the derived command wouldn't.
	if json.Unmarshal(b, &pkg) != nil || pkg.Scripts["pretest"] != "" || pkg.Scripts["posttest"] != "" {
		return MonorepoCmd{}, false
	}
	c, ok := ParseMonorepoTest(pkg.Scripts["test"])
	if !ok {
		return MonorepoCmd{}, false
	}
	if len(c.Prefix) == 0 && c.Tool != "cargo" {
		c.Prefix = runner
	}
	return c, true
}

// monorepoTemplate: the derived related command for the setup's test
// command, or "".
func monorepoTemplate(s *Setup) string {
	if c, ok := monorepoOf(s); ok {
		return c.template()
	}
	return ""
}

func IsDerivedMonorepo(s *Setup) bool {
	if s == nil || strings.TrimSpace(s.Commands.TestRelated) != "" {
		return false
	}
	_, ok := monorepoOf(s)
	return ok
}

type monoUnit struct {
	name, dir string // dir: relative to the root, slash-separated, "." for the root
	deps      []string
}

// MonorepoAffected: the arguments that narrow the related command to the
// units the changed files are in (and, for turbo and cargo, the units that
// depend on them). why non-empty: run the full suite. graph is true only
// when every changed file mapped to a unit through plain files.
func MonorepoAffected(root string, s *Setup, files []string) (args []string, graph bool, why string) {
	args, graph, _, why = MonorepoAffectedRun(root, s, files)
	return args, graph, why
}

// MonorepoAffectedRun is MonorepoAffected that also names the changed
// files no unit holds (cleaned, as Run.Files holds them): no unit's tests
// are those files, so a run doesn't count them as run.
func MonorepoAffectedRun(root string, s *Setup, files []string) (args []string, graph bool, outside []string, why string) {
	args, graph, outside, why = monorepoAffected(root, s, files)
	if why != "" {
		return nil, false, nil, why
	}
	return args, graph, outside, ""
}

func monorepoAffected(root string, s *Setup, files []string) (args []string, graph bool, outside []string, why string) {
	c, ok := monorepoOf(s)
	if !ok {
		return nil, false, nil, "the test command isn't an nx, turbo or cargo command Plumbline can narrow"
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false, nil, "the project folder can't be read"
	}
	if c.Tool == "turbo" {
		if why := turboRootTask(real); why != "" {
			return nil, false, nil, why
		}
	}
	units, why := listUnits(real, c)
	if why != "" {
		return nil, false, nil, why
	}
	graph = true
	hit := map[string]bool{}
	var nxFiles []string
	code := false
	for _, f := range files {
		f = cleanRel(f)
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) || climbsOut(filepath.Clean(f)) {
			return nil, false, nil, f + " is outside the project"
		}
		f = filepath.ToSlash(filepath.Clean(f))
		md := isMarkdown(f)
		if !md {
			code = true
		}
		if path.Dir(f) == "." && !md {
			return nil, false, nil, f + " is at the top of the project, where it can change every unit"
		}
		if c.Tool == "cargo" && strings.HasPrefix(foldPath(f), foldPath(".cargo/")) {
			return nil, false, nil, f + " is cargo's own config, which changes how every crate builds"
		}
		u := unitOf(units, f)
		if u == nil {
			if md {
				continue
			}
			if c.Tool == "turbo" {
				return nil, false, nil, f + " is outside every package, and turbo's global inputs can reach them all"
			}
			graph = false
			outside = append(outside, f)
			if c.Tool == "nx" {
				nxFiles = append(nxFiles, f) // nx maps some itself (sharedGlobals)
			}
			continue
		}
		if u.dir == "." && !md {
			graph = false // only the root unit holds it; members may read it too
		}
		if !plainPath(real, f) {
			graph = false
		}
		hit[u.name] = true
		nxFiles = append(nxFiles, f)
	}
	if len(hit) == 0 {
		if !code {
			return nil, false, nil, "no changed code files"
		}
		return nil, false, nil, "no changed file is in one of the " + c.Tool + " workspace's units"
	}
	switch c.Tool {
	case "nx":
		return []string{"--files=" + strings.Join(nxFiles, ",")}, graph, outside, ""
	case "turbo":
		for n := range hit {
			args = append(args, "--filter=..."+n)
		}
	case "cargo":
		for n := range dependents(units, hit) {
			args = append(args, "--package="+n)
		}
	}
	sort.Strings(args)
	return args, graph, outside, ""
}

// turboRootTask: why a root task (//#test in turbo.json) rules out a
// package filter, which never selects it; "" when there is none.
func turboRootTask(real string) string {
	b, err := os.ReadFile(filepath.Join(real, "turbo.json"))
	if err != nil {
		return "turbo.json can't be read"
	}
	if bytes.Contains(b, []byte(`"//#`)) {
		return "turbo.json has a root task (//#...), which runs only without a package filter"
	}
	return ""
}

// unitOf: the unit whose folder holds f most closely.
func unitOf(units []monoUnit, f string) *monoUnit {
	var best *monoUnit
	ff := foldPath(f)
	for i := range units {
		d := units[i].dir
		if d != "." && !strings.HasPrefix(ff, foldPath(d)+"/") {
			continue
		}
		if best == nil || best.dir == "." || d != "." && len(d) > len(best.dir) {
			best = &units[i]
		}
	}
	return best
}

// plainPath: f, or for a deleted file its nearest folder on disk, is
// reached without a symlink and has one name.
func plainPath(real, f string) bool {
	abs := filepath.Join(real, filepath.FromSlash(f))
	p := abs
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		if p == real {
			return false
		}
		p = filepath.Dir(p)
	}
	got, err := filepath.EvalSymlinks(p)
	if err != nil || foldPath(got) != foldPath(p) {
		return false
	}
	return p != abs || plainFile(abs)
}

// dependents: the hit units and every unit that depends on one, directly
// or through others.
func dependents(units []monoUnit, hit map[string]bool) map[string]bool {
	out := map[string]bool{}
	for n := range hit {
		out[n] = true
	}
	for grew := true; grew; {
		grew = false
		for _, u := range units {
			if out[u.name] {
				continue
			}
			for _, d := range u.deps {
				if out[d] {
					out[u.name], grew = true, true
					break
				}
			}
		}
	}
	return out
}

// listUnits asks the tool for its units, once per related run.
func listUnits(real string, c MonorepoCmd) ([]monoUnit, string) {
	var args []string
	switch c.Tool {
	case "nx":
		args = []string{"nx", "graph", "--file=stdout"}
	case "turbo":
		args = []string{"turbo", "ls", "--output=json"}
	case "cargo":
		args = []string{"cargo", "metadata", "--format-version", "1", "--no-deps", "--offline"}
	}
	words := append(append([]string{}, c.Prefix...), args...)
	ctx, cancel := context.WithTimeout(context.Background(), listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, words[0], words[1:]...)
	cmd.Dir = real
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	if c.Tool == "nx" {
		cmd.Env = append(cmd.Env, "NX_DAEMON=false")
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, strings.Join(words, " ") + " failed (" + err.Error() + ")"
	}
	text := out.Bytes()
	if i := bytes.IndexByte(text, '{'); i > 0 {
		text = text[i:]
	}
	// yarn v1 prints "Done in 0.46s." after the tool's JSON.
	var first json.RawMessage
	if json.NewDecoder(bytes.NewReader(text)).Decode(&first) == nil {
		text = first
	}
	var units []monoUnit
	var why string
	switch c.Tool {
	case "nx":
		units, why = nxUnits(text)
	case "turbo":
		units, why = turboUnits(text)
	case "cargo":
		units, why = cargoUnits(real, text)
	}
	if why != "" {
		return nil, strings.Join(words, " ") + ": " + why
	}
	if len(units) == 0 {
		return nil, strings.Join(words, " ") + " listed no units"
	}
	return units, ""
}

func relUnitDir(d string) (string, bool) {
	if d == "" {
		d = "."
	}
	d = filepath.ToSlash(filepath.Clean(filepath.FromSlash(d)))
	if filepath.IsAbs(d) || climbsOut(d) {
		return "", false
	}
	return d, true
}

func nxUnits(text []byte) ([]monoUnit, string) {
	var g struct {
		Graph struct {
			Nodes map[string]struct {
				Data struct {
					Root string `json:"root"`
				} `json:"data"`
			} `json:"nodes"`
		} `json:"graph"`
	}
	if err := json.Unmarshal(text, &g); err != nil {
		return nil, "its output isn't the project graph"
	}
	var units []monoUnit
	for name, n := range g.Graph.Nodes {
		d, ok := relUnitDir(n.Data.Root)
		if !ok {
			return nil, "project " + name + " is outside the workspace"
		}
		units = append(units, monoUnit{name: name, dir: d})
	}
	return units, ""
}

func turboUnits(text []byte) ([]monoUnit, string) {
	var l struct {
		Packages struct {
			Items []struct {
				Name string `json:"name"`
				Path string `json:"path"`
			} `json:"items"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(text, &l); err != nil {
		return nil, "its output isn't the package list"
	}
	var units []monoUnit
	for _, p := range l.Packages.Items {
		d, ok := relUnitDir(p.Path)
		if !ok || p.Name == "" {
			return nil, "package " + p.Name + " is outside the workspace"
		}
		units = append(units, monoUnit{name: p.Name, dir: d})
	}
	return units, ""
}

func cargoUnits(real string, text []byte) ([]monoUnit, string) {
	var m struct {
		WorkspaceRoot    string   `json:"workspace_root"`
		WorkspaceMembers []string `json:"workspace_members"`
		Packages         []struct {
			Name         string `json:"name"`
			ID           string `json:"id"`
			ManifestPath string `json:"manifest_path"`
			Dependencies []struct {
				Path string `json:"path"`
			} `json:"dependencies"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(text, &m); err != nil {
		return nil, "its output isn't cargo metadata"
	}
	ws, err := filepath.EvalSymlinks(m.WorkspaceRoot)
	if err != nil || foldPath(ws) != foldPath(real) {
		return nil, "the cargo workspace isn't this project (" + m.WorkspaceRoot + ")"
	}
	member := map[string]bool{}
	for _, id := range m.WorkspaceMembers {
		member[id] = true
	}
	rel := func(p string) (string, bool) {
		r, err := filepath.Rel(m.WorkspaceRoot, p)
		if err != nil {
			return "", false
		}
		return relUnitDir(r)
	}
	byDir := map[string]string{}
	var units []monoUnit
	for _, p := range m.Packages {
		if !member[p.ID] {
			continue
		}
		d, ok := rel(filepath.Dir(p.ManifestPath))
		if !ok {
			return nil, "crate " + p.Name + " is outside the workspace"
		}
		byDir[foldPath(d)] = p.Name
		units = append(units, monoUnit{name: p.Name, dir: d})
	}
	for i, p := range m.Packages {
		if !member[p.ID] {
			continue
		}
		for _, dep := range p.Dependencies {
			if dep.Path == "" {
				continue
			}
			if d, ok := rel(dep.Path); ok && byDir[foldPath(d)] != "" {
				for j := range units {
					if units[j].name == m.Packages[i].Name {
						units[j].deps = append(units[j].deps, byDir[foldPath(d)])
					}
				}
			}
		}
	}
	return units, ""
}

// FillRelated puts the arguments in the related command's {files}, each
// shell-quoted. Paths that start with "-" get "./" so the runner reads
// them as files; flags (flags true) go in as they are.
func FillRelated(template string, args []string, flags bool) string {
	q := make([]string, 0, len(args))
	for _, a := range args {
		if !flags && strings.HasPrefix(a, "-") {
			a = "./" + a
		}
		q = append(q, ShellQuote(a))
	}
	return strings.ReplaceAll(template, "{files}", strings.Join(q, " "))
}

// ShellQuote: s as one shell word, quoted only when it needs it.
func ShellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '=' || r == ',' || r == '@' || r == '+' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
