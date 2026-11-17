package core

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// ParseCommitMessage pulls the message out of a git commit command.
// ok is false when the message comes from an editor (no -m/-F).
func ParseCommitMessage(cmd, cwd string) (msg string, ok bool) {
	// heredoc form: -m "$(cat <<'EOF' ... EOF)"
	if i := strings.Index(cmd, "<<"); i >= 0 {
		rest := cmd[i+2:]
		rest = strings.TrimLeft(rest, "-")
		rest = strings.TrimSpace(rest)
		tag := strings.Trim(strings.Fields(rest + " x")[0], `'"`)
		if nl := strings.Index(rest, "\n"); nl >= 0 && tag != "" {
			body := rest[nl+1:]
			if end := strings.Index(body, "\n"+tag); end >= 0 {
				return body[:end], true
			}
			if strings.HasPrefix(body, tag) {
				return "", true
			}
		}
	}
	args := ShellWords(cmd)
	var parts []string
	found := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--message":
			if i+1 < len(args) {
				parts = append(parts, args[i+1])
				i++
				found = true
			}
		case strings.HasPrefix(a, "--message="):
			parts = append(parts, strings.TrimPrefix(a, "--message="))
			found = true
		case strings.HasPrefix(a, "-m") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			parts = append(parts, a[2:])
			found = true
		case a == "-F" || a == "--file":
			if i+1 < len(args) {
				b, err := os.ReadFile(Abs(args[i+1], cwd))
				if err == nil {
					parts = append(parts, string(b))
					found = true
				}
				i++
			}
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "m") && len(a) > 2:
			// combined flags like -am "msg"
			if strings.HasSuffix(a, "m") && i+1 < len(args) {
				parts = append(parts, args[i+1])
				i++
				found = true
			}
		}
	}
	return strings.Join(parts, "\n\n"), found
}
