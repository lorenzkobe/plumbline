package core

import (
	"os"
	"path/filepath"
	"strings"
)

func derivedRelated(test string) string {
	test = strings.TrimSpace(test)
	if test == "" || strings.ContainsAny(test, "|&;<>()$`\\\"'*?[]{}#~\n") {
		return ""
	}
	if c, ok := ParseGoTest(test); ok {
		return strings.Join(append([]string{"go", "test"}, c.Flags...), " ") + " {files}"
	}
	words := strings.Fields(test)
	for i, w := range words {
		switch w {
		case "jest":
			return test + " --findRelatedTests {files}"
		case "vitest":
			rest := words[i+1:]
			if len(rest) > 0 && rest[0] == "run" {
				rest = rest[1:]
			}
			return strings.Join(append(append(append([]string{}, words[:i+1]...), "related", "--run"), rest...), " ") + " {files}"
		case "pytest":
			if i == 0 || i == 2 && words[0] == "python" || i == 2 && words[0] == "python3" {
				return test + " {files}"
			}
		}
	}
	return ""
}

func RelatedTemplate(s *Setup) string { return savedCommand(s, KindRelated) }

func RelatedIsDerived(s *Setup) bool {
	return strings.TrimSpace(s.Commands.TestRelated) == "" && derivedRelated(s.Commands.Test) != ""
}

func GoRelatedPkgs(root string, files []string) (pkgs []string, why string) {
	seen := map[string]bool{}
	for _, f := range files {
		f = cleanRel(f)
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) || climbsOut(filepath.Clean(f)) {
			return nil, f + " is outside the project"
		}
		f = filepath.ToSlash(filepath.Clean(f))
		base := filepath.Base(f)
		switch {
		case base == "go.mod" || base == "go.sum" || base == "go.work" || base == "go.work.sum":
			return nil, base + " changed, so every package may be affected"
		case strings.Contains("/"+f, "/testdata/") || strings.HasPrefix(f, "vendor/") || strings.HasSuffix(f, ".s") || strings.HasSuffix(f, ".c") || strings.HasSuffix(f, ".h"):
			return nil, f + " can change what a package builds or reads"
		case filepath.Ext(f) != ".go":
			if underGoPackage(root, filepath.Dir(f)) {
				return nil, f + " sits under a Go package, which may embed or read it"
			}
			continue
		}
		dir := filepath.Dir(f)
		if fi, err := os.Stat(filepath.Join(root, dir)); err != nil || !fi.IsDir() {
			return nil, "the folder of " + f + " is gone"
		}
		pkg := "./" + filepath.ToSlash(dir)
		if dir == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	if len(pkgs) == 0 {
		return nil, "no changed Go files"
	}
	return pkgs, ""
}

func PytestRelated(root string, files []string) (tests []string, why string) {
	out, err := gitOut(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, "git couldn't list the project's files"
	}
	byName := map[string][]string{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			byName[filepath.Base(p)] = append(byName[filepath.Base(p)], p)
		}
	}
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			tests = append(tests, p)
		}
	}
	for _, f := range files {
		f = cleanRel(f)
		if f == "" {
			continue
		}
		if filepath.IsAbs(f) || climbsOut(filepath.Clean(f)) {
			return nil, f + " is outside the project"
		}
		f = filepath.ToSlash(filepath.Clean(f))
		base := filepath.Base(f)
		switch base {
		case "pytest.ini", "pyproject.toml", "setup.cfg", "tox.ini", "conftest.py", "requirements.txt", "poetry.lock", "uv.lock":
			return nil, f + " can change how every test runs"
		}
		if filepath.Ext(f) != ".py" {
			continue
		}
		mod := strings.TrimSuffix(base, ".py")
		if strings.HasPrefix(mod, "test_") || strings.HasSuffix(mod, "_test") {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				add(f)
			}
			continue
		}
		found := append(append([]string{}, byName["test_"+mod+".py"]...), byName[mod+"_test.py"]...)
		if len(found) == 0 {
			return nil, "no test file found by name for " + f
		}
		for _, p := range found {
			add(p)
		}
	}
	if len(tests) == 0 {
		return nil, "no related test files"
	}
	return tests, ""
}

func isPytest(template string) bool {
	w := strings.Fields(template)
	return len(w) > 0 && (w[0] == "pytest" || len(w) > 2 && w[2] == "pytest")
}

func IsDerivedGo(s *Setup) bool {
	if !RelatedIsDerived(s) {
		return false
	}
	_, ok := ParseGoTest(s.Commands.Test)
	return ok
}

func IsDerivedPytest(s *Setup) bool { return RelatedIsDerived(s) && isPytest(s.Commands.Test) }

func hasGoFiles(dir string) bool {
	es, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range es {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

func underGoPackage(root, dir string) bool {
	for d := dir; d != "." && d != "/" && d != ""; d = filepath.Dir(d) {
		if hasGoFiles(filepath.Join(root, d)) {
			return true
		}
	}
	return false
}
