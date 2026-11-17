package core

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

var secretPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"AWS access key", regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"private key", regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |DSA |PGP )?PRIVATE KEY`)},
	{"GitHub token", regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{30,}\b|\bgithub_pat_[A-Za-z0-9_]{40,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"Stripe key", regexp.MustCompile(`\b(sk|rk)_(live|test)_[A-Za-z0-9]{16,}`)},
	{"API key", regexp.MustCompile(`\bsk-(ant-|proj-)?[A-Za-z0-9_-]{20,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"connection string with password", regexp.MustCompile(`\b[a-z][a-z0-9+]*://[^\s:/@]+:[^\s@/]{4,}@[^\s]+`)},
}

var reAssign = regexp.MustCompile(`(?i)\b(password|passwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key|client[_-]?secret)\b\s*[:=]\s*["']([^"'\s]{8,})["']`)

func entropy(s string) float64 {
	if s == "" {
		return 0
	}
	freq := map[rune]float64{}
	for _, r := range s {
		freq[r]++
	}
	var h float64
	n := float64(len([]rune(s)))
	for _, c := range freq {
		p := c / n
		h -= p * math.Log2(p)
	}
	return h
}

// ScanSecrets looks at added lines of a diff and reports file:line and the
// kind of secret. The value itself is never printed.
func ScanSecrets(diff string) []string {
	var out []string
	file := ""
	line := 0
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(l, "+++ "), "b/")
		case strings.HasPrefix(l, "@@"):
			// @@ -a,b +c,d @@
			if i := strings.Index(l, "+"); i >= 0 {
				rest := l[i+1:]
				n := 0
				for _, ch := range rest {
					if ch < '0' || ch > '9' {
						break
					}
					n = n*10 + int(ch-'0')
				}
				line = n
			}
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			body := l[1:]
			if isTemplateFile(file) {
				line++
				continue
			}
			for _, p := range secretPatterns {
				if p.re.MatchString(body) {
					out = append(out, fmt.Sprintf("%s:%d looks like a %s", file, line, p.name))
				}
			}
			if m := reAssign.FindStringSubmatch(body); m != nil {
				v := m[2]
				low := strings.ToLower(v)
				placeholder := strings.Contains(low, "example") || strings.Contains(low, "changeme") || strings.Contains(low, "your") || strings.HasPrefix(v, "${") || strings.HasPrefix(v, "process.env") || strings.Contains(low, "xxxx") || strings.Contains(low, "dummy") || strings.Contains(low, "test")
				if !placeholder && entropy(v) >= 3.0 {
					out = append(out, fmt.Sprintf("%s:%d looks like a hard-coded %s", file, line, strings.ToLower(m[1])))
				}
			}
			line++
		case strings.HasPrefix(l, " "):
			line++
		}
	}
	return out
}

func isTemplateFile(p string) bool {
	b := strings.ToLower(filepath.Base(p))
	return strings.HasSuffix(b, ".example") || strings.HasSuffix(b, ".sample") || strings.HasSuffix(b, ".template") || strings.HasSuffix(b, ".dist")
}

var aiMarkers = []*regexp.Regexp{
	regexp.MustCompile(`(?i)co-authored-by:.*(claude|anthropic)`),
	regexp.MustCompile(`(?i)generated (with|by) .*claude`),
	regexp.MustCompile(`(?i)claude\.(ai|com)/code`),
	regexp.MustCompile(`(?i)claude-session`),
	regexp.MustCompile(`(?i)noreply@anthropic\.com`),
	regexp.MustCompile(`🤖`),
	regexp.MustCompile(`(?i)\b(written|coded|authored) by (claude|an? ai)\b`),
}

// AIMarkers lists any AI attribution found in text written in your name.
func AIMarkers(msg string) []string {
	var out []string
	for _, re := range aiMarkers {
		if m := re.FindString(msg); m != "" {
			out = append(out, m)
		}
	}
	return out
}

var reConventional = regexp.MustCompile(`^(feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert)(\([^)]+\))?!?: \S`)

// CheckCommitMessage applies the git rules for this project kind.
func CheckCommitMessage(msg string, s *Setup) []string {
	var out []string
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return []string{"commit message is empty (use -m \"...\")"}
	}
	if m := AIMarkers(msg); len(m) > 0 {
		out = append(out, "remove AI markers from the message ("+strings.Join(m, "; ")+")")
	}
	subject := strings.SplitN(msg, "\n", 2)[0]
	if len(subject) > 72 {
		out = append(out, fmt.Sprintf("subject line is %d characters; keep it under 72", len(subject)))
	}
	if s != nil {
		switch s.Kind {
		case "company":
			tr := s.TicketRegex
			if tr == "" {
				tr = `[A-Z][A-Z0-9]+-\d+`
			}
			if ok, _ := regexp.MatchString(`^`+tr+`: \S`, subject); !ok {
				out = append(out, "company commits start with the ticket: \"TICKET-123: What changed\"")
			}
		case "personal":
			if !reConventional.MatchString(subject) {
				out = append(out, "personal commits start with feat:, fix:, chore: (or docs/refactor/test/perf/build/ci/style)")
			}
		}
	}
	return out
}

// CommitInfo is what a git commit command does, as git reads its options.
type CommitInfo struct {
	Message  string   // -m, -F, --trailer text together
	MsgFound bool     // a message was given (not the editor)
	MsgBad   string   // the message can't be known before it runs, and why
	NoVerify bool     // -n, --no-verify (or a unique prefix of it)
	All      bool     // -a: every tracked change
	Paths    []string // pathspecs: those files' working-tree changes are committed
	Amend    bool
}

// git commit's long options, for git's unique-prefix rule (--no-verif is
// --no-verify; --no-ver is ambiguous with --no-verbose and refused by git).
var commitLong = strings.Fields(`all patch reuse-message reedit-message fixup squash reset-author short branch
	porcelain long null file author date message template signoff no-signoff trailer no-verify verify allow-empty
	allow-empty-message cleanup edit no-edit amend no-amend no-post-rewrite include only pathspec-from-file
	pathspec-file-nul untracked-files verbose no-verbose quiet no-quiet dry-run status no-status gpg-sign
	no-gpg-sign no-all no-patch no-include no-only`)

var commitLongArg = map[string]bool{"reuse-message": true, "reedit-message": true, "fixup": true, "squash": true,
	"file": true, "author": true, "date": true, "message": true, "template": true, "trailer": true, "cleanup": true,
	"pathspec-from-file": true}

// resolveLong maps an abbreviated long option to the full name ("" when
// unknown or ambiguous).
func resolveLong(name string) string {
	var hits []string
	for _, o := range commitLong {
		if o == name {
			return o
		}
		if strings.HasPrefix(o, name) {
			hits = append(hits, o)
		}
	}
	if len(hits) == 1 {
		return hits[0]
	}
	return ""
}

// ParseCommit reads a git commit segment. The message parts that can't be
// read before it runs ($VAR, $(cmd)) make MsgBad, so AI markers can't hide.
func ParseCommit(s Segment) CommitInfo {
	var ci CommitInfo
	_, sub := gitGlobals(s)
	if sub < 0 {
		return ci
	}
	var parts []string
	value := func(i int) (string, bool) {
		if i >= len(s.Words) {
			return "", false
		}
		if !s.dyn(i) {
			return s.Words[i], true
		}
		if i < len(s.src) {
			if v, ok := catValue(s, i); ok {
				return v, true
			}
		}
		ci.MsgBad = "the message " + s.Words[i] + " isn't known until it runs; write it out with -m \"...\""
		return "", false
	}
	addMsg := func(i int) {
		if v, ok := value(i); ok {
			parts = append(parts, v)
		}
		ci.MsgFound = true
	}
	addFile := func(f string) {
		ci.MsgFound = true
		if f == "-" {
			if s.Stdin != nil {
				parts = append(parts, *s.Stdin)
				return
			}
			ci.MsgBad = "the message comes from standard input; write it out with -m \"...\""
			return
		}
		b, err := os.ReadFile(Abs(f, s.Dir))
		if err != nil {
			ci.MsgBad = "the message file " + f + " can't be read"
			return
		}
		parts = append(parts, string(b))
	}
	words := s.Words
	afterDash := false
	for i := sub + 1; i < len(words); i++ {
		a := words[i]
		if !afterDash && s.dyn(i) {
			// it could be --no-verify or -a
			ci.MsgBad = "the option " + a + " isn't known until it runs; write the commit command out in full"
			continue
		}
		if afterDash || !strings.HasPrefix(a, "-") || a == "-" {
			if s.dyn(i) {
				ci.All = true // an unknown path: scan every change
				continue
			}
			ci.Paths = append(ci.Paths, a)
			continue
		}
		if a == "--" {
			afterDash = true
			continue
		}
		if strings.HasPrefix(a, "--") {
			name, val, hasVal := strings.Cut(a[2:], "=")
			full := resolveLong(name)
			if full == "" {
				continue // git refuses it
			}
			if commitLongArg[full] && !hasVal {
				i++
				val = s.Arg(i)
			}
			switch full {
			case "no-verify":
				ci.NoVerify = true
			case "all":
				ci.All = true
			case "amend":
				ci.Amend = true
			case "message":
				if hasVal {
					parts = append(parts, val)
					ci.MsgFound = true
				} else {
					addMsg(i)
				}
			case "file":
				addFile(val)
			case "trailer":
				parts = append(parts, val)
			case "pathspec-from-file":
				ci.All = true // can't tell which files: scan everything
			}
			continue
		}
		f := a[1:]
		for j := 0; j < len(f); j++ {
			switch f[j] {
			case 'n':
				ci.NoVerify = true
			case 'a':
				ci.All = true
			case 'm', 'F', 'C', 'c', 't':
				attached := f[j+1:]
				if attached == "" {
					i++
				}
				switch f[j] {
				case 'm':
					if attached != "" {
						parts = append(parts, attached)
						ci.MsgFound = true
					} else {
						addMsg(i)
					}
				case 'F':
					if attached == "" {
						attached = s.Arg(i)
					}
					addFile(attached)
				case 'C', 'c':
					ci.MsgFound = true
					ci.MsgBad = "the message is taken from another commit; write it out with -m \"...\""
				}
				j = len(f)
			case 'u', 'S':
				j = len(f) // optional value, attached only
			}
		}
	}
	ci.Message = strings.Join(parts, "\n\n")
	return ci
}

// catValue reads -m "$(cat <<'EOF' ... EOF)" and -m "$(cat file)".
func catValue(s Segment, i int) (string, bool) {
	w := s.src[i]
	if w == nil || len(w.Parts) != 1 {
		return "", false
	}
	part := w.Parts[0]
	if dq, ok := part.(*syntax.DblQuoted); ok && len(dq.Parts) == 1 {
		part = dq.Parts[0]
	}
	cs, ok := part.(*syntax.CmdSubst)
	if !ok || len(cs.Stmts) != 1 {
		return "", false
	}
	st := cs.Stmts[0]
	ce, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || len(ce.Assigns) > 0 || len(ce.Args) == 0 || ce.Args[0].Lit() != "cat" {
		return "", false
	}
	trim := func(v string) string { return strings.TrimRight(v, "\n") }
	if len(ce.Args) == 1 {
		for _, r := range st.Redirs {
			if r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc {
				if body, ok := heredocBody(r); ok {
					return trim(body), true
				}
			}
		}
		return "", false
	}
	if len(ce.Args) == 2 && len(st.Redirs) == 0 {
		f, lit, glob := wordValue(ce.Args[1], s.Dir, nil)
		if !lit || glob {
			return "", false
		}
		b, err := os.ReadFile(Abs(f, s.Dir))
		if err != nil {
			return "", false
		}
		return trim(string(b)), true
	}
	return "", false
}

// ParseCommitMessage pulls the message out of a git commit command.
// ok is false when the message comes from an editor (no -m/-F) or can't be
// known before it runs.
func ParseCommitMessage(cmd, cwd string) (msg string, ok bool) {
	for _, s := range ParseShell(cmd, cwd) {
		if sub, _ := GitSub(s); sub == "commit" {
			ci := ParseCommit(s)
			return ci.Message, ci.MsgFound && ci.MsgBad == ""
		}
	}
	return "", false
}
