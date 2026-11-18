package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Fail closed: a segment counts as read-only only when every part of it was
// understood and is on an allowlist. Anything else is treated as a change.

func harmlessRedirect(t string) bool {
	switch t {
	case "/dev/null", "/dev/stderr", "/dev/stdout", "/dev/tty", "/dev/fd/1", "/dev/fd/2":
		return true
	}
	return false
}

// Variables a command may be given without changing what it does.
var harmlessEnvNames = map[string]bool{
	"LANG": true, "LANGUAGE": true, "TZ": true, "NO_COLOR": true, "FORCE_COLOR": true, "CLICOLOR": true,
	"CLICOLOR_FORCE": true, "TERM": true, "COLUMNS": true, "LINES": true, "CI": true, "GIT_OPTIONAL_LOCKS": true,
}

// Shell variables that change what the shell itself does.
var shellSpecial = set("PATH IFS CDPATH BASH_ENV ENV PS4 PROMPT_COMMAND SHELLOPTS BASHOPTS GLOBIGNORE HOME FPATH ZDOTDIR POSIXLY_CORRECT BASH_XTRACEFD BASH_LOADABLES_PATH EXECIGNORE PATH_MAX module_path path fpath cdpath")

// harmlessEnv: setting this can't make a command run code or write files.
// A shell variable that isn't exported (and wasn't already) never reaches
// the commands, so it only matters when the shell itself reads it.
func harmlessEnv(e string, standalone bool) bool {
	name, val, _ := strings.Cut(e, "=")
	if harmlessEnvNames[name] || strings.HasPrefix(name, "LC_") {
		return true
	}
	if (name == "GIT_PAGER" || name == "PAGER") && (val == "cat" || val == "") {
		return true
	}
	if standalone && !shellSpecial[name] {
		if _, inEnv := os.LookupEnv(name); !inEnv {
			return true
		}
	}
	return false
}

// Commands that only read, whatever their arguments are.
var pureReaders = map[string]bool{}

func init() {
	for _, c := range strings.Fields(`ls cat head tail wc grep egrep fgrep pwd echo printf which type set shopt unset pushd popd dirs alias
		stat du df diff cmp cut tr jq basename dirname realpath readlink date uname whoami id hostname true false
		test [ cd column nl sha256sum sha1sum sha512sum shasum md5sum md5 cksum sum od hexdump strings comm join
		paste fold expand unexpand sleep lsof ps pgrep uptime free nproc tokei exit seq printenv tac rev sw_vers
		locale tty groups logname : read wait return break continue shift getconf arch`) {
		pureReaders[c] = true
	}
}

var gitReadOnly = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "fetch": true, "rev-parse": true,
	"ls-files": true, "blame": true, "describe": true, "shortlog": true, "cat-file": true,
	"ls-tree": true, "merge-base": true, "grep": true, "reflog": true, "whatchanged": true,
	"name-rev": true, "rev-list": true, "count-objects": true, "for-each-ref": true, "show-ref": true,
	"check-ignore": true, "version": true, "help": true, "diff-tree": true, "diff-files": true, "diff-index": true,
	"var": true, "range-diff": true, "annotate": true,
}

// GitSub returns the git subcommand of a segment and its arguments, skipping
// git's own options.
func GitSub(s Segment) (string, []string) {
	if s.Name() != "git" {
		return "", nil
	}
	_, i := gitGlobals(s)
	if i < 0 {
		return "", nil
	}
	return s.Words[i], s.Words[i+1:]
}

// shortFlags returns the letters of a short-option word like -ni (nil for a
// long option or a plain word).
func shortFlags(a string) string {
	if len(a) < 2 || a[0] != '-' || a[1] == '-' {
		return ""
	}
	return a[1:]
}

// ReadOnly reports whether a segment only reads.
func ReadOnly(s Segment) bool {
	if s.Opaque != "" {
		return false
	}
	for _, r := range s.Redirects {
		if !harmlessRedirect(r) {
			return false
		}
	}
	name := s.Name()
	for _, e := range s.Env {
		if !harmlessEnv(e, name == "" || (isDecl(name) && !s.Exported)) {
			return false
		}
	}
	if name == "" || isDecl(name) {
		return true
	}
	if pureReaders[name] {
		return true
	}
	// Arguments xargs adds are file names from a listing, but a file named
	// like a flag (-i, --output=x) is read as one by GNU tools, which take
	// flags after operands. Only "--" at the end makes them operands.
	if s.MoreArgs && s.Words[len(s.Words)-1] != "--" {
		return false
	}
	// The same for an unquoted pattern: sed -n 1p * with a file named -i
	// there edits files. Judged against the files there now.
	for i, dashed := 1, false; i < len(s.Words); i++ {
		if !s.dyn(i) && !s.glob(i) && s.Words[i] == "--" {
			dashed = true
		}
		if !dashed && s.glob(i) && globMayBeFlag(s.Words[i], s.Dir) {
			return false
		}
	}
	switch name {
	case "command":
		return strings.ContainsAny(shortFlags(s.Arg(1)), "vV") // command -v, -pv: prints where a command is
	case "env", "time", "xargs":
		return len(s.Words) == 1 // alone; with a command, the command is its own segment
	case "trap":
		return true // the trap's command is its own segment
	case "git":
		return gitReadOnlySeg(s)
	case "plumbline":
		return plumblineReads(s)
	}
	if s.dynArgs() {
		return false // an argument not known until it runs could be a flag that writes
	}
	args := s.Words[1:]
	switch name {
	case "less", "more":
		for _, a := range args {
			if strings.HasPrefix(a, "+") {
				return false // less +!cmd runs cmd
			}
		}
		return true
	case "ag":
		for _, a := range args {
			if strings.HasPrefix(a, "--pager") {
				return false
			}
		}
		return true
	case "find", "gfind":
		for _, a := range args {
			switch a {
			case "-delete", "-fprint", "-fprint0", "-fprintf", "-fls":
				return false
			}
		}
		return true // -exec commands are their own segments
	case "fd", "fdfind":
		return true // -x commands are their own segments
	case "sed", "gsed":
		return sedReadOnly(args)
	case "awk", "gawk", "mawk", "nawk":
		return awkReadOnly(args)
	case "sort":
		for _, a := range args {
			if a == "-o" || strings.HasPrefix(a, "--output") || strings.HasPrefix(a, "--compress-program") || strings.Contains(shortFlags(a), "o") {
				return false
			}
		}
		return true
	case "uniq", "xxd":
		n := 0
		for i := 0; i < len(args); i++ {
			a := args[i]
			if name == "xxd" && (a == "-r" || strings.Contains(shortFlags(a), "r")) {
				return false
			}
			if strings.HasPrefix(a, "-") && a != "-" {
				if (name == "uniq" && (a == "-f" || a == "-s" || a == "-w")) || (name == "xxd" && strings.Contains("-c -g -l -s -o -n -C", a) && len(a) == 2) {
					i++
				}
				continue
			}
			n++
		}
		return n <= 1 // a second file is the output file
	case "base64":
		for _, a := range args {
			if a == "-o" || strings.HasPrefix(a, "--output") {
				return false
			}
		}
		return true
	case "tree":
		for _, a := range args {
			if a == "-o" || a == "-R" || strings.HasPrefix(a, "--output") {
				return false
			}
		}
		return true
	case "yq":
		for _, a := range args {
			if a == "-i" || strings.HasPrefix(a, "--inplace") || (strings.Contains(shortFlags(a), "i") && !strings.HasPrefix(a, "-I")) {
				return false
			}
		}
		return true
	case "rg":
		for _, a := range args {
			if a == "--pre" || strings.HasPrefix(a, "--pre=") || strings.HasPrefix(a, "--hostname-bin") {
				return false
			}
		}
		return true
	case "bat":
		return s.Arg(1) != "cache" && !contains(args, "--generate-config-file")
	case "cloc":
		for _, a := range args {
			if strings.HasPrefix(a, "--out") || strings.HasPrefix(a, "--report-file") || strings.HasPrefix(a, "--sql") {
				return false
			}
		}
		return true
	case "file":
		return !contains(args, "-C")
	case "docker":
		switch s.Arg(1) {
		case "ps", "images", "logs", "inspect", "version", "info":
			return true
		case "compose":
			a := s.Arg(2)
			return a == "ps" || a == "logs" || a == "config" || a == "ls"
		}
		return false
	case "npm", "pnpm", "yarn":
		switch s.Arg(1) {
		case "ls", "list", "view", "info", "outdated", "why", "explain", "-v", "--version":
			return true
		case "config":
			return s.Arg(2) == "get" || s.Arg(2) == "list"
		}
		return false
	case "go":
		switch s.Arg(1) {
		case "version", "doc":
			return true
		case "env":
			for _, a := range args[1:] {
				if a == "-w" || a == "-u" {
					return false
				}
			}
			return true
		case "list", "vet":
			for _, a := range args[1:] {
				if strings.HasPrefix(a, "-export") || strings.HasPrefix(a, "-compiled") || strings.HasPrefix(a, "-toolexec") || strings.HasPrefix(a, "-vettool") || strings.HasPrefix(a, "-exec") {
					return false
				}
			}
			return true
		}
		return false
	case "node", "python", "python3", "ruby", "php", "java", "cargo", "rustc", "deno", "bun":
		return len(s.Words) == 2 && (s.Words[1] == "--version" || s.Words[1] == "-v" || s.Words[1] == "-V" || s.Words[1] == "version")
	}
	return false
}

// globMayBeFlag: an unquoted pattern the shell could expand to a file named
// like a flag: its first part is a pattern that matches such a file now (or
// the folder isn't known). "src/*" can't: its matches start with "src/".
func globMayBeFlag(pattern, dir string) bool {
	for _, p := range expandBraces(pattern) {
		if strings.HasPrefix(p, "-") {
			return true
		}
		if p == "" || !strings.ContainsAny(p[:1], "*?[") {
			continue
		}
		if dir == "" {
			return true
		}
		first, _, _ := strings.Cut(p, "/")
		m, _ := filepath.Glob(filepath.Join(dir, first))
		for _, x := range m {
			if strings.HasPrefix(filepath.Base(x), "-") {
				return true
			}
		}
	}
	return false
}

// needsDoubleDash: the segment would read only if "--" ended it, because
// xargs adds arguments that could be flags.
func needsDoubleDash(s Segment) bool {
	if !s.MoreArgs || len(s.Words) == 0 || s.Words[len(s.Words)-1] == "--" {
		return false
	}
	t := s
	t.Words = append(append([]string{}, s.Words...), "--")
	t.Dyn = append(append([]bool{}, s.Dyn...), false)
	t.Glob = append(append([]bool{}, s.Glob...), false)
	return ReadOnly(t)
}

const doubleDashHint = ` To read the files xargs adds, end the command with "--" (xargs sed -n 1p --), so a file named like a flag can't act as one.`

func isDecl(name string) bool {
	switch name {
	case "export", "declare", "local", "readonly", "typeset":
		return true
	}
	return false
}

func gitReadOnlySeg(s Segment) bool {
	configs, i := gitGlobals(s)
	if len(configs) > 0 {
		return false // git -c can point git at any program
	}
	for j := 1; j < len(s.Words) && (i < 0 || j < i); j++ {
		if strings.HasPrefix(s.Words[j], "--exec-path=") {
			return false // runs git's commands from another folder
		}
	}
	if i < 0 {
		return true // git --version, git --help
	}
	if s.dyn(i) {
		return false
	}
	sub, args := s.Words[i], s.Words[i+1:]
	paths := false // after "--", arguments are paths
	for k, a := range args {
		j := i + 1 + k
		if !paths && !s.dyn(j) && a == "--" {
			paths = true
		}
		if s.dyn(j) && !paths && !gitDynArgSafe(s, j) {
			return false // its value could be a flag (--output=x, -D)
		}
	}
	if gitReadOnly[sub] {
		for _, a := range args {
			low := strings.ToLower(a)
			switch {
			case low == "--output", strings.HasPrefix(low, "--output="), low == "--ext-diff",
				strings.HasPrefix(low, "--open-files-in-pager"), strings.HasPrefix(a, "-O") && sub == "grep",
				strings.HasPrefix(low, "--upload-pack"), low == "--textconv" && sub != "cat-file":
				return false
			}
		}
		return true
	}
	switch sub {
	case "branch", "tag":
		return gitListOnly(sub, args)
	case "remote":
		return len(args) == 0 || args[0] == "-v" || args[0] == "show" || args[0] == "get-url"
	case "stash":
		return len(args) > 0 && (args[0] == "list" || args[0] == "show")
	case "config":
		_, sets := gitConfigSets(args)
		for _, a := range args {
			switch a {
			case "--edit", "-e", "--add", "--unset", "--unset-all", "--replace-all", "--remove-section", "--rename-section":
				return false
			}
		}
		return !sets
	case "worktree":
		return len(args) > 0 && args[0] == "list"
	}
	return false
}

// gitDynArgSafe: word j of a git command isn't known until it runs, but
// can't be a flag: it starts with a literal that isn't "-" (HEAD~$n), or it
// is only $(git merge-base ...) or $(git rev-parse ...) with flags that print
// a commit id, ref name or path (their output never starts with "-").
func gitDynArgSafe(s Segment, j int) bool {
	if len(s.src) != len(s.Words) || s.src[j] == nil || len(s.src[j].Parts) == 0 {
		return false
	}
	w := s.src[j]
	switch p := w.Parts[0].(type) {
	case *syntax.Lit:
		return p.Value != "" && p.Value[0] != '-' && p.Value[0] != '\\'
	case *syntax.SglQuoted:
		return p.Value != "" && p.Value[0] != '-' && !p.Dollar
	}
	part := w.Parts[0]
	if len(w.Parts) != 1 {
		return false
	}
	if dq, ok := part.(*syntax.DblQuoted); ok && len(dq.Parts) > 0 {
		if lit, ok := dq.Parts[0].(*syntax.Lit); ok {
			return lit.Value != "" && lit.Value[0] != '-' && lit.Value[0] != '\\'
		}
		if len(dq.Parts) != 1 {
			return false
		}
		part = dq.Parts[0]
	}
	cs, ok := part.(*syntax.CmdSubst)
	if !ok || len(cs.Stmts) != 1 || len(cs.Stmts[0].Redirs) > 0 {
		return false
	}
	ce, ok := cs.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(ce.Assigns) > 0 || len(ce.Args) < 2 {
		return false
	}
	words := make([]string, len(ce.Args))
	for k, a := range ce.Args {
		if words[k] = a.Lit(); words[k] == "" {
			return false
		}
	}
	if words[0] != "git" {
		return false
	}
	switch words[1] {
	case "merge-base":
		return true
	case "rev-parse":
		for _, a := range words[2:] {
			if strings.HasPrefix(a, "-") && !revParsePrints[a] && !strings.HasPrefix(a, "--short=") {
				return false
			}
		}
		return true
	}
	return false
}

var revParsePrints = set("--abbrev-ref --short --verify -q --quiet --show-toplevel --show-prefix --git-dir --git-common-dir --symbolic-full-name --is-inside-work-tree")

// gitListOnly: git branch or git tag in list mode only (no create, delete,
// move or annotate).
var (
	gitListChange = map[string]map[string]bool{
		"branch": set("-d -D -m -M -c -C -f -u --delete --move --copy --force --set-upstream-to --unset-upstream --edit-description --create-reflog --track --no-track"),
		"tag":    set("-d -a -s -f -m -F -u -e --delete --annotate --sign --force --message --file --local-user --edit --create-reflog"),
	}
	gitListing = map[string]map[string]bool{
		"branch": set("-l --list -a --all -r --remotes --contains --no-contains --merged --no-merged --points-at --show-current --sort --format --column"),
		"tag":    set("-l --list -n --contains --no-contains --merged --no-merged --points-at --sort --format --column"),
	}
	gitListValue = set("--contains --no-contains --merged --no-merged --points-at --sort --format")
)

func gitListOnly(sub string, args []string) bool {
	list := len(args) == 0
	plain := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		n, _, hasEq := strings.Cut(a, "=")
		switch {
		case gitListChange[sub][n]:
			return false
		case gitListing[sub][n]:
			list = true
			if gitListValue[n] && !hasEq && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++ // its value: a commit, a sort key, a format
			}
		case strings.HasPrefix(a, "-v") || a == "--verbose" || a == "-i" || a == "--ignore-case" || strings.HasPrefix(a, "--color") || a == "--no-color" || a == "-q" || a == "--quiet":
		case strings.HasPrefix(a, "-"):
			return false
		default:
			plain++
		}
	}
	return list || plain == 0
}

// sedReadOnly: no in-place flag, and every script only prints.
func sedReadOnly(args []string) bool {
	var scripts []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--expression":
			if i+1 >= len(args) {
				return false
			}
			scripts = append(scripts, args[i+1])
			explicit = true
			i++
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
			explicit = true
		case a == "-f" || strings.HasPrefix(a, "--file") || strings.HasPrefix(a, "--in-place"):
			return false // a script file, or in place
		case a == "-l" || a == "--line-length":
			i++
		case strings.HasPrefix(a, "--"):
			// --posix, --quiet, --sandbox, --regexp-extended, --null-data, --separate ...
		case shortFlags(a) != "":
			f := shortFlags(a)
			for j := 0; j < len(f); j++ {
				switch f[j] {
				case 'i', 'I', 'f':
					return false
				case 'e':
					rest := f[j+1:]
					if rest == "" {
						if i+1 >= len(args) {
							return false
						}
						rest = args[i+1]
						i++
					}
					scripts = append(scripts, rest)
					explicit = true
					j = len(f)
				case 'l':
					j = len(f)
					if f[j-1] == 'l' {
						i++
					}
				}
			}
		default:
			if !explicit && len(scripts) == 0 {
				scripts = append(scripts, a)
			}
		}
	}
	for _, sc := range scripts {
		if !sedScriptReadOnly(sc) {
			return false
		}
	}
	return true
}

// sedScriptReadOnly: the script has no w, W or e command, and no s///w or
// s///e flag. Anything it can't follow counts as writing.
func sedScriptReadOnly(sc string) bool {
	n := len(sc)
	i := 0
	skipSpace := func() {
		for i < n && (sc[i] == ' ' || sc[i] == '\t') {
			i++
		}
	}
	// delimited reads a /.../ part starting at the delimiter, returning false
	// if it never ends.
	delimited := func() bool {
		d := sc[i]
		i++
		for i < n && sc[i] != d {
			if sc[i] == '\\' {
				i++
			}
			if i < n && sc[i] == '\n' && d != '\n' {
				return false
			}
			i++
		}
		if i >= n {
			return false
		}
		i++
		return true
	}
	addr := func() bool {
		switch {
		case i < n && sc[i] >= '0' && sc[i] <= '9':
			for i < n && (sc[i] >= '0' && sc[i] <= '9' || sc[i] == '~') {
				i++
			}
		case i < n && sc[i] == '$':
			i++
		case i < n && sc[i] == '/':
			if !delimited() {
				return false
			}
		case i+1 < n && sc[i] == '\\':
			i++
			if !delimited() {
				return false
			}
		default:
			return true
		}
		for i < n && (sc[i] == 'I' || sc[i] == 'M') {
			i++
		}
		return true
	}
	toEOL := func() {
		for i < n && sc[i] != '\n' {
			if sc[i] == '\\' {
				i++
			}
			i++
		}
	}
	for i < n {
		c := sc[i]
		if c == ' ' || c == '\t' || c == '\n' || c == ';' {
			i++
			continue
		}
		if !addr() {
			return false
		}
		skipSpace()
		if i < n && sc[i] == ',' {
			i++
			skipSpace()
			if i < n && (sc[i] == '+' || sc[i] == '~') {
				i++
			}
			if !addr() {
				return false
			}
		}
		skipSpace()
		for i < n && sc[i] == '!' {
			i++
			skipSpace()
		}
		if i >= n {
			return false
		}
		cmd := sc[i]
		i++
		switch cmd {
		case '{', '}', '=', 'd', 'D', 'g', 'G', 'h', 'H', 'n', 'N', 'p', 'P', 'x', 'z', 'F':
		case 'q', 'Q', 'l', 'L':
			skipSpace()
			for i < n && sc[i] >= '0' && sc[i] <= '9' {
				i++
			}
		case ':', 'b', 't', 'T':
			for i < n && sc[i] != ';' && sc[i] != '\n' {
				i++
			}
		case 'a', 'i', 'c', 'r', 'R', '#':
			toEOL()
		case 's', 'y':
			if i >= n || sc[i] == '\\' || sc[i] == '\n' {
				return false
			}
			d := sc[i]
			if !delimited() {
				return false
			}
			i-- // the closing delimiter opens the replacement
			if i >= n || sc[i] != d || !delimited() {
				return false
			}
			for i < n && sc[i] != ';' && sc[i] != '\n' && sc[i] != '}' {
				if cmd == 's' && (sc[i] == 'w' || sc[i] == 'W' || sc[i] == 'e') {
					return false
				}
				i++
			}
		default:
			return false // w, W, e, or something not known
		}
	}
	return true
}

var (
	reAwkSystem   = regexp.MustCompile(`\bsystem\s*\(|\|\s*getline|\|&|@load|@include`)
	reAwkPrintOut = regexp.MustCompile(`\bprintf?\b[^;{}\n]*?(>|\|[^|])`)
)

// awkReadOnly: the program doesn't run commands or print to files or pipes.
func awkReadOnly(args []string) bool {
	prog := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-f" || strings.HasPrefix(a, "--file") || a == "-i" || strings.HasPrefix(a, "--include") || a == "-l" || strings.HasPrefix(a, "--load") || a == "-E" || strings.HasPrefix(a, "--exec"):
			return false
		case a == "-v" || a == "-F" || a == "--assign" || a == "--field-separator":
			i++
		case a == "--":
			if i+1 < len(args) && prog == "" {
				prog = args[i+1]
			}
			i = len(args)
		case strings.HasPrefix(a, "-") && len(a) > 1:
			if strings.HasPrefix(a, "-f") {
				return false
			}
		default:
			if prog == "" {
				prog = a
			}
		}
	}
	if prog == "" {
		return false
	}
	return !reAwkSystem.MatchString(prog) && !reAwkPrintOut.MatchString(prog)
}

// Commands whose file arguments are the files they change.
var writesAllArgs = map[string]bool{
	"rm": true, "unlink": true, "shred": true, "rmdir": true, "touch": true, "truncate": true, "tee": true,
	"mv": true, "chmod": true, "chown": true, "chgrp": true, "srm": true,
}

// Commands that copy into their last argument.
var writesLastArg = map[string]bool{"cp": true, "ln": true, "install": true, "rsync": true, "ditto": true, "gcp": true}

// inPlace: tools that change the files they're given, when given this flag
// (or always, for formatters that only rewrite).
func inPlace(s Segment) bool {
	args := s.Words[1:]
	has := func(fs ...string) bool {
		for _, a := range args {
			for _, f := range fs {
				if a == f || strings.HasPrefix(a, f+"=") {
					return true
				}
			}
		}
		return false
	}
	switch s.Name() {
	case "sed", "gsed":
		for _, a := range args {
			if strings.HasPrefix(a, "--in-place") || strings.ContainsAny(shortFlags(a), "iI") {
				return true
			}
		}
	case "perl", "ruby":
		for _, a := range args {
			if strings.Contains(shortFlags(a), "i") {
				return true
			}
		}
	case "yq":
		return has("-i", "--inplace")
	case "gofmt", "goimports", "gofumpt":
		return has("-w")
	case "prettier":
		return has("--write", "-w")
	case "eslint", "stylelint":
		return has("--fix")
	case "ruff":
		return s.Arg(1) == "format" || has("--fix")
	case "black", "isort", "rustfmt", "autoflake":
		return !has("--check", "--diff")
	case "clang-format", "autopep8", "shfmt":
		return has("-i", "-w", "--in-place")
	case "git":
		sub, _ := GitSub(s)
		return sub == "rm" || sub == "mv" || sub == "checkout" || sub == "restore"
	}
	return false
}

// WriteTargets lists the files a segment writes by name: redirect targets,
// and the file arguments of commands that change their files. Words that
// aren't known are left out (the after-the-command check still sees what
// changed).
func WriteTargets(s Segment) []string {
	var out []string
	for _, r := range s.Redirects {
		if harmlessRedirect(r) {
			continue
		}
		if hasGlob(r) {
			out = append(out, expandGlob(r, s.Dir)...) // the shell expands a redirect's pattern too
			continue
		}
		out = append(out, r)
	}
	if s.Opaque != "" || len(s.Words) == 0 {
		return out
	}
	name := s.Name()
	operands := func() []string {
		var ops []string
		for i := 1; i < len(s.Words); i++ {
			a := s.Words[i]
			if s.dyn(i) {
				continue
			}
			if strings.HasPrefix(a, "-") && a != "-" {
				if (name == "truncate" && (a == "-s" || a == "-r")) || (name == "cp" || name == "mv" || name == "ln" || name == "install") && (a == "-t" || a == "-S") {
					i++
				}
				continue
			}
			if s.glob(i) {
				ops = append(ops, expandGlob(a, s.Dir)...)
				continue
			}
			ops = append(ops, a)
		}
		return ops
	}
	switch {
	case writesAllArgs[name]:
		ops := operands()
		if (name == "chmod" || name == "chown" || name == "chgrp") && len(ops) > 0 {
			ops = ops[1:] // the mode or the owner
		}
		out = append(out, ops...)
	case writesLastArg[name]:
		ops := operands()
		if len(ops) >= 2 {
			dest := ops[len(ops)-1]
			if fi, err := os.Stat(Abs(dest, s.Dir)); err == nil && fi.IsDir() && s.Dir != "" {
				for _, src := range ops[:len(ops)-1] {
					out = append(out, filepath.Join(dest, filepath.Base(src)))
				}
			} else {
				out = append(out, dest)
			}
		}
	case name == "dd":
		for _, a := range s.Words[1:] {
			if strings.HasPrefix(a, "of=") {
				out = append(out, strings.TrimPrefix(a, "of="))
			}
		}
	case inPlace(s):
		start := 1
		if name == "git" {
			_, i := gitGlobals(s)
			start = i + 1
		}
		for _, a := range operands() {
			if name == "git" && start > 1 && a == s.Words[start-1] {
				continue
			}
			if s.Dir == "" && !filepath.IsAbs(a) {
				out = append(out, a)
				continue
			}
			if _, err := os.Stat(Abs(a, s.Dir)); err == nil {
				out = append(out, a) // only real files: the script itself isn't one
			}
		}
	}
	return out
}

// TreeTargets lists folders a segment writes files anywhere inside:
// archives extracted there, and recursive copies into them.
func TreeTargets(s Segment) []string {
	if s.Opaque != "" || len(s.Words) == 0 {
		return nil
	}
	name := s.Name()
	args := s.Words[1:]
	switch name {
	case "tar", "gtar", "bsdtar":
		extract := false
		dest := "."
		for i, a := range args {
			switch {
			case a == "--extract" || a == "--get" || i == 0 && !strings.HasPrefix(a, "--") && strings.Contains(strings.TrimPrefix(a, "-"), "x"):
				extract = true
			case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "x") && len(a) <= 6:
				extract = true
			case a == "-C" || a == "--directory" || a == "--cd":
				dest = s.Arg(i + 2)
			case strings.HasPrefix(a, "--directory="):
				dest = strings.TrimPrefix(a, "--directory=")
			}
		}
		if extract {
			return []string{dest}
		}
	case "unzip":
		for i, a := range args {
			if a == "-d" {
				return []string{s.Arg(i + 2)}
			}
		}
		return []string{"."}
	case "cp", "rsync", "ditto", "gcp":
		recursive := name == "ditto"
		for _, a := range args {
			if a == "--recursive" || a == "--archive" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rRa") {
				recursive = true
			}
		}
		if recursive {
			var ops []string
			for i, a := range args {
				if !strings.HasPrefix(a, "-") && !s.dyn(i+1) {
					ops = append(ops, a)
				}
			}
			if len(ops) >= 2 {
				return []string{ops[len(ops)-1]}
			}
		}
	}
	return nil
}

// expandGlob returns the files a pattern matches now, or the pattern itself
// when nothing matches.
func expandGlob(pattern, dir string) []string {
	var out []string
	for _, p := range expandBraces(pattern) {
		if dir == "" && !filepath.IsAbs(p) {
			out = append(out, p)
			continue
		}
		m, _ := filepath.Glob(Abs(p, dir))
		if len(m) == 0 {
			out = append(out, p)
		}
		out = append(out, m...)
	}
	return out
}

// expandBraces expands {a,b} lists (not ranges).
func expandBraces(p string) []string {
	open := strings.Index(p, "{")
	if open < 0 {
		return []string{p}
	}
	depth := 0
	for i := open; i < len(p); i++ {
		switch p[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				inner := p[open+1 : i]
				if !strings.Contains(inner, ",") {
					return []string{p}
				}
				var out []string
				for _, alt := range splitTop(inner) {
					out = append(out, expandBraces(p[:open]+alt+p[i+1:])...)
				}
				return out
			}
		}
	}
	return []string{p}
}

func splitTop(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

var envFileRe = regexp.MustCompile(`(^|/)\.env(\.[A-Za-z0-9_.-]+)?$`)

// IsSecretFile reports files whose values must not be edited by Claude and
// whose content is only shown after you allow it.
func IsSecretFile(p string) bool {
	b := filepath.Base(p)
	low := strings.ToLower(b)
	if isTemplateFile(low) {
		return false
	}
	if envFileRe.MatchString(strings.ToLower(filepath.ToSlash(p))) {
		return true
	}
	switch {
	case strings.HasSuffix(low, ".pem"), strings.HasSuffix(low, ".key"), strings.HasSuffix(low, ".p12"),
		strings.HasSuffix(low, ".pfx"), strings.HasSuffix(low, ".keystore"), strings.HasSuffix(low, ".jks"):
		return true
	case low == "id_rsa" || low == "id_ed25519" || low == "id_ecdsa" || low == "id_dsa":
		return true
	case low == ".npmrc" || low == ".pypirc" || low == ".netrc" || low == "credentials" || low == ".pgpass":
		return true
	case strings.HasPrefix(low, "secrets.") && !strings.HasSuffix(low, ".md"):
		return true
	}
	return false
}

var fileReaders = map[string]bool{}

func init() {
	for _, c := range strings.Fields(`cat head tail less more grep egrep fgrep rg ag awk gawk sed bat strings xxd od
		hexdump cp base64 nl sort uniq cut diff tac rev jq yq openssl scp rsync curl wget http https vim vi nano code
		python python3 node tar zip gzip column fold paste`) {
		fileReaders[c] = true
	}
}

// fileCandidates returns the paths an argument may name: itself, a value
// after =, a file after @ (curl -d@.env, -F f=@.env), and a short option's
// attached value (-d@.env, -T.env).
func fileCandidates(a string) []string {
	out := []string{a}
	if i := strings.Index(a, "="); i >= 0 {
		out = append(out, a[i+1:])
	}
	if i := strings.Index(a, "@"); i >= 0 {
		out = append(out, a[i+1:])
	}
	if len(a) > 2 && a[0] == '-' && a[1] != '-' {
		out = append(out, a[2:])
	}
	return out
}

// ReadsSecretFile reports a segment that would print, copy or send a secret
// file. Loading one into a command (set -a; . ./.env; psql) is fine.
func ReadsSecretFile(s Segment) string {
	name := s.Name()
	check := func(p string) string {
		if p == "" {
			return ""
		}
		for _, x := range expandGlob(p, s.Dir) {
			if IsSecretFile(x) {
				return x
			}
		}
		return ""
	}
	if name != "source" && name != "." {
		for _, in := range s.Inputs {
			if f := check(in); f != "" {
				return f
			}
		}
	}
	if !fileReaders[name] {
		return ""
	}
	for i := 1; i < len(s.Words); i++ {
		if s.dyn(i) {
			continue
		}
		for _, c := range fileCandidates(s.Words[i]) {
			if strings.HasPrefix(c, "-") {
				continue
			}
			if f := check(c); f != "" {
				return f
			}
		}
	}
	return ""
}
