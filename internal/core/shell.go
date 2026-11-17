package core

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ShellWords splits a command into words, honouring quotes and backslashes.
// Operators (; && || | &) and newlines become their own words.
func ShellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case c == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				cur.WriteRune(rs[i])
				inWord = true
			}
		case c == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case c == '"':
			inWord = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune(`"\$`+"`", rs[i+1]) {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case c == ' ' || c == '\t':
			flush()
		case c == '\n' || c == ';' || c == '(' || c == ')':
			flush()
			out = append(out, string(c))
		case c == '&' || c == '|':
			flush()
			if i+1 < len(rs) && rs[i+1] == c {
				out = append(out, string([]rune{c, c}))
				i++
			} else if c == '&' && i+1 < len(rs) && rs[i+1] == '>' {
				cur.WriteString("&>")
				inWord = true
				i++
			} else {
				out = append(out, string(c))
			}
		case c == '>' || c == '<':
			flush()
			op := string(c)
			if i+1 < len(rs) && (rs[i+1] == '>' || rs[i+1] == '&' || (c == '<' && rs[i+1] == '<')) {
				op += string(rs[i+1])
				i++
			}
			out = append(out, op)
		default:
			cur.WriteRune(c)
			inWord = true
		}
	}
	flush()
	return out
}

// Segment is one simple command inside a command line.
type Segment struct {
	Words     []string // command and arguments, without env assignments and redirections
	Redirects []string // targets of > and >>
}

func (s Segment) Name() string {
	if len(s.Words) == 0 {
		return ""
	}
	return filepath.Base(s.Words[0])
}

func (s Segment) Arg(i int) string {
	if i < len(s.Words) {
		return s.Words[i]
	}
	return ""
}

var reAssignWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// Segments splits a command line into simple commands. Command substitutions
// $(...) and backticks are treated as their own commands too.
func Segments(cmd string) []Segment {
	// pull out $( ... ) and backtick bodies so their commands are checked
	extra := []string{}
	for _, re := range []*regexp.Regexp{regexp.MustCompile("\\$\\(([^()]*)\\)"), regexp.MustCompile("`([^`]*)`")} {
		for _, m := range re.FindAllStringSubmatch(cmd, -1) {
			extra = append(extra, m[1])
		}
	}
	// drop heredoc bodies: they are data, not commands
	if i := strings.Index(cmd, "<<"); i >= 0 {
		rest := strings.TrimLeft(cmd[i+2:], "-")
		rest = strings.TrimSpace(rest)
		f := strings.Fields(rest + " x")
		tag := strings.Trim(f[0], `'"`)
		if nl := strings.Index(rest, "\n"); nl >= 0 && tag != "" {
			body := rest[nl+1:]
			if end := strings.Index(body, "\n"+tag); end >= 0 {
				cmd = cmd[:i+2] + " " + f[0] + " " + body[end+1+len(tag):]
			}
		}
	}
	var segs []Segment
	for _, src := range append([]string{cmd}, extra...) {
		words := ShellWords(src)
		cur := Segment{}
		started := false
		for i := 0; i < len(words); i++ {
			w := words[i]
			switch w {
			case ";", "&&", "||", "|", "&", "\n", "(", ")":
				if len(cur.Words) > 0 || len(cur.Redirects) > 0 {
					segs = append(segs, cur)
				}
				cur = Segment{}
				started = false
				continue
			case ">", ">>", "&>", ">|":
				if i+1 < len(words) {
					cur.Redirects = append(cur.Redirects, words[i+1])
					i++
				}
				continue
			case "<", "<<", "<&", ">&":
				i++
				continue
			}
			if strings.HasPrefix(w, "&>") {
				cur.Redirects = append(cur.Redirects, strings.TrimPrefix(w, "&>"))
				continue
			}
			if !started && reAssignWord.MatchString(w) {
				continue
			}
			if !started && (w == "sudo" || w == "time" || w == "command" || w == "nohup" || w == "exec" || w == "builtin") {
				continue
			}
			if !started && w == "env" && i+1 < len(words) {
				continue
			}
			started = true
			cur.Words = append(cur.Words, w)
		}
		if len(cur.Words) > 0 || len(cur.Redirects) > 0 {
			segs = append(segs, cur)
		}
	}
	return segs
}

func harmlessRedirect(t string) bool {
	return t == "/dev/null" || t == "/dev/stderr" || t == "/dev/stdout" || strings.HasPrefix(t, "&") || t == "1" || t == "2"
}

var readOnlyCmds = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "less": true, "more": true, "wc": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "fd": true, "tree": true,
	"pwd": true, "echo": true, "printf": true, "which": true, "type": true, "file": true, "stat": true,
	"du": true, "df": true, "diff": true, "cmp": true, "sort": true, "uniq": true, "cut": true,
	"tr": true, "jq": true, "yq": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "date": true, "uname": true, "whoami": true, "id": true, "hostname": true,
	"true": true, "false": true, "test": true, "[": true, "cd": true, "column": true, "nl": true,
	"bat": true, "sha256sum": true, "shasum": true, "md5sum": true, "md5": true, "xxd": true, "od": true,
	"strings": true, "comm": true, "join": true, "paste": true, "fold": true, "expand": true,
	"sleep": true, "lsof": true, "ps": true, "pgrep": true, "uptime": true, "free": true, "nproc": true,
	"tokei": true, "cloc": true, "exit": true, "seq": true, "base64": true,
}

var gitReadOnly = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "fetch": true, "rev-parse": true,
	"ls-files": true, "blame": true, "describe": true, "shortlog": true, "cat-file": true,
	"ls-tree": true, "merge-base": true, "grep": true, "reflog": true, "whatchanged": true,
	"name-rev": true, "rev-list": true, "count-objects": true, "for-each-ref": true, "show-ref": true,
	"check-ignore": true, "version": true, "help": true,
}

// GitSub returns the git subcommand of a segment, skipping global options.
func GitSub(s Segment) (string, []string) {
	if s.Name() != "git" {
		return "", nil
	}
	w := s.Words[1:]
	for i := 0; i < len(w); i++ {
		a := w[i]
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a, w[i+1:]
	}
	return "", nil
}

// ReadOnly reports whether a segment only reads.
func ReadOnly(s Segment) bool {
	for _, r := range s.Redirects {
		if !harmlessRedirect(r) {
			return false
		}
	}
	name := s.Name()
	if name == "" {
		return true
	}
	if readOnlyCmds[name] {
		if name == "base64" || name == "xxd" {
			for _, a := range s.Words[1:] {
				if a == "-o" || strings.HasPrefix(a, "--output") {
					return false
				}
			}
		}
		return true
	}
	switch name {
	case "find":
		for _, a := range s.Words[1:] {
			switch a {
			case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls":
				return false
			}
		}
		return true
	case "sed":
		hasN := false
		for _, a := range s.Words[1:] {
			if a == "-i" || strings.HasPrefix(a, "-i") || strings.HasPrefix(a, "--in-place") {
				return false
			}
			if a == "-n" || a == "--quiet" || a == "--silent" {
				hasN = true
			}
		}
		_ = hasN
		return true
	case "awk", "gawk":
		for _, a := range s.Words[1:] {
			if strings.Contains(a, "system(") || strings.Contains(a, "print >") || strings.Contains(a, "| getline") || a == "-i" {
				return false
			}
		}
		return true
	case "git":
		sub, args := GitSub(s)
		if gitReadOnly[sub] {
			return true
		}
		switch sub {
		case "branch":
			for _, a := range args {
				if strings.HasPrefix(a, "-d") || strings.HasPrefix(a, "-D") || strings.HasPrefix(a, "-m") || strings.HasPrefix(a, "-M") || strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "-C") || a == "--delete" || a == "--move" || a == "--copy" || a == "-f" || a == "--force" || a == "-u" || strings.HasPrefix(a, "--set-upstream") || a == "--unset-upstream" || a == "--edit-description" {
					return false
				}
			}
			for _, a := range args {
				if !strings.HasPrefix(a, "-") {
					return false // creating a branch
				}
			}
			return true
		case "remote":
			return len(args) == 0 || args[0] == "-v" || args[0] == "show" || args[0] == "get-url"
		case "stash":
			return len(args) > 0 && (args[0] == "list" || args[0] == "show")
		case "tag":
			return len(args) == 0 || args[0] == "-l" || args[0] == "--list"
		case "config":
			for _, a := range args {
				if a == "--get" || a == "--get-all" || a == "--list" || a == "-l" || a == "--get-regexp" {
					return true
				}
			}
			return false
		case "worktree":
			return len(args) > 0 && args[0] == "list"
		}
		return false
	case "docker":
		if len(s.Words) > 1 {
			switch s.Words[1] {
			case "ps", "images", "logs", "inspect", "version", "info":
				return true
			case "compose":
				return len(s.Words) > 2 && (s.Words[2] == "ps" || s.Words[2] == "logs" || s.Words[2] == "config" || s.Words[2] == "ls")
			}
		}
		return false
	case "npm", "pnpm", "yarn":
		if len(s.Words) > 1 {
			switch s.Words[1] {
			case "ls", "list", "view", "info", "outdated", "why", "explain", "-v", "--version", "config":
				if s.Words[1] == "config" {
					return len(s.Words) > 2 && (s.Words[2] == "get" || s.Words[2] == "list")
				}
				return true
			}
		}
		return false
	case "go":
		if len(s.Words) > 1 {
			switch s.Words[1] {
			case "version", "env", "list", "doc", "vet":
				return true
			}
		}
		return false
	case "node", "python", "python3", "ruby", "php", "java", "cargo", "rustc", "deno", "bun":
		return len(s.Words) == 2 && (s.Words[1] == "--version" || s.Words[1] == "-v" || s.Words[1] == "-V" || s.Words[1] == "version")
	case "plumbline":
		switch s.Arg(1) {
		case "status", "stats", "docs-dir", "check", "version", "help", "":
			return true
		case "task":
			sub := s.Arg(2)
			return sub == "show" || sub == "list" || sub == ""
		}
		return false
	}
	return false
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
	if envFileRe.MatchString(filepath.ToSlash(p)) {
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

var fileReaders = map[string]bool{
	"cat": true, "head": true, "tail": true, "less": true, "more": true, "grep": true, "egrep": true,
	"rg": true, "ag": true, "awk": true, "gawk": true, "sed": true, "bat": true, "strings": true, "xxd": true,
	"od": true, "cp": true, "base64": true, "nl": true, "sort": true, "uniq": true, "cut": true, "diff": true,
	"tac": true, "jq": true, "yq": true, "openssl": true, "scp": true, "rsync": true, "curl": true, "vim": true,
	"vi": true, "nano": true, "code": true, "python": true, "python3": true, "node": true,
}

// ReadsSecretFile reports a segment that would print or copy a secret file.
// Loading one into a command (source .env && psql) is fine.
func ReadsSecretFile(s Segment) string {
	if !fileReaders[s.Name()] {
		return ""
	}
	for _, a := range s.Words[1:] {
		if strings.HasPrefix(a, "-") && !strings.Contains(a, "/") {
			continue
		}
		a = strings.TrimPrefix(a, "@")
		if IsSecretFile(a) {
			return a
		}
	}
	return ""
}
