package core

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)

func EnvNames(path string) ([]string, error) {
	if !envFileRe.MatchString(strings.ToLower(filepath.Base(filepath.ToSlash(path)))) {
		return nil, fmt.Errorf("%s isn't a .env file (.env, .env.local, .env.production and the like), and env-names reads only those", path)
	}
	notPlain := fmt.Errorf("%s isn't a plain file (it is a link, a second name of another file, or not a file), so its names aren't listed", path)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || !plainFile(path) {
		return nil, notPlain
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := after.Sys().(*syscall.Stat_t)
	if !after.Mode().IsRegular() || !ok || st.Nlink != 1 || !os.SameFile(before, after) {
		return nil, notPlain
	}
	names, err := envNamesFrom(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return names, nil
}

func envNamesFrom(r io.Reader) ([]string, error) {
	br := bufio.NewReader(r)
	names := []string{}
	seen := map[string]bool{}
	var quote byte
	opened, n := 0, 0
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("reading stopped at line %d (%v), so no names are shown", n+1, err)
		}
		if line == "" && err == io.EOF {
			break
		}
		n++
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if quote != 0 {
			if rest, closed := closeQuote(text, quote); closed {
				quote = 0
				if !envTailOK(rest) {
					return nil, badEnvLine(n)
				}
			}
		} else {
			name, q, ok := envLine(text)
			if !ok {
				return nil, badEnvLine(n)
			}
			if name != "" && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
			if q != 0 {
				quote, opened = q, n
			}
		}
		if err == io.EOF {
			break
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the quoted value that starts on line %d is never closed, so no names are shown", opened)
	}
	return names, nil
}

func badEnvLine(n int) error {
	return fmt.Errorf("line %d isn't a name=value line, a comment or blank, so no names are shown", n)
}

func envLine(text string) (name string, quote byte, ok bool) {
	t := strings.TrimSpace(text)
	if t == "" || strings.HasPrefix(t, "#") {
		return "", 0, true
	}
	if len(t) > 6 && strings.HasPrefix(t, "export") && (t[6] == ' ' || t[6] == '\t') {
		t = strings.TrimLeft(t[6:], " \t")
	}
	k := strings.IndexByte(t, '=')
	if k < 0 {
		return "", 0, false
	}
	name = strings.TrimRight(t[:k], " \t")
	if !envNameRe.MatchString(name) {
		return "", 0, false
	}
	val := strings.TrimLeft(t[k+1:], " \t")
	if val != "" && (val[0] == '"' || val[0] == '\'' || val[0] == '`') {
		rest, closed := closeQuote(val[1:], val[0])
		if !closed {
			return name, val[0], true
		}
		if !envTailOK(rest) {
			return "", 0, false
		}
		return name, 0, true
	}
	if strings.HasSuffix(val, `\`) {
		return "", 0, false
	}
	return name, 0, true
}

func closeQuote(s string, q byte) (rest string, closed bool) {
	for i := 0; i < len(s); i++ {
		if q == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return s[i+1:], true
		}
	}
	return "", false
}

func envTailOK(rest string) bool {
	r := strings.TrimSpace(rest)
	return r == "" || strings.HasPrefix(r, "#")
}
