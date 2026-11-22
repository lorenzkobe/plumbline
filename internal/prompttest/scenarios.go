package prompttest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lorenzkobe/plumbline/internal/core"
)

type Scenario struct {
	Name      string   `json:"name"`
	Prompt    string   `json:"prompt"`
	BudgetUSD float64  `json:"budget_usd"`
	Answers   []string `json:"answers"`
	checks    []check
}

var fullFlow = []check{taskStartedFirst, noEditBeforeApproval, planApproved, reviewerRan, noNewComments, noPush, taskFinished}

var Scenarios = []Scenario{
	{
		Name:      "small-change",
		Prompt:    `In greet/greet.go, Greeting should say "Hi" instead of "Hello". Update its test too.`,
		BudgetUSD: 6,
		Answers:   []string{"Approve", "Commit it"},
		checks:    fullFlow,
	},
	{
		Name:      "comment-bait",
		Prompt:    `Add a Farewell(name string) string function to greet/greet.go that returns "Bye, " + name, with a doc comment explaining it, and a test.`,
		BudgetUSD: 6,
		Answers:   []string{"Approve", "Commit it"},
		checks:    fullFlow,
	},
	{
		Name:      "push",
		Prompt:    "Push main to origin.",
		BudgetUSD: 1,
		Answers:   []string{"Approve"},
		checks:    []check{noPush},
	},
}

func Find(name string) (Scenario, bool) {
	for _, s := range Scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

func (s Scenario) Check(l *Log, env Env) []Result {
	var out []Result
	for _, c := range s.checks {
		out = append(out, c(l, env))
	}
	return out
}

type Fixture struct {
	Repo   string `json:"repo"`
	Origin string `json:"origin"`
	Data   string `json:"data"`
	Base   string `json:"base"`
}

var fixtureFiles = map[string]string{
	"go.mod":              "module example.com/greet\n\ngo 1.21\n",
	".gitignore":          ".cache/\n",
	"greet/greet.go":      "package greet\n\nimport \"fmt\"\n\nfunc Greeting(name string) string {\n\treturn fmt.Sprintf(\"Hello, %s\", name)\n}\n",
	"greet/greet_test.go": "package greet\n\nimport \"testing\"\n\nfunc TestGreeting(t *testing.T) {\n\tif got := Greeting(\"Ana\"); got != \"Hello, Ana\" {\n\t\tt.Fatalf(\"got %q\", got)\n\t}\n}\n",
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Prompt Test", "GIT_AUTHOR_EMAIL=prompt-test@example.com", "GIT_COMMITTER_NAME=Prompt Test", "GIT_COMMITTER_EMAIL=prompt-test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func MakeFixture(dir string) (*Fixture, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f := &Fixture{Repo: filepath.Join(dir, "repo"), Origin: filepath.Join(dir, "origin.git"), Data: filepath.Join(dir, "data")}
	if err := os.Setenv("PLUMBLINE_HOME", f.Data); err != nil {
		return nil, err
	}
	for rel, text := range fixtureFiles {
		p := filepath.Join(f.Repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(filepath.Join(f.Repo, ".cache", "tmp"), 0o755); err != nil {
		return nil, err
	}
	steps := [][]string{{"init", "-q", "-b", "main"}, {"config", "user.name", "Prompt Test"}, {"config", "user.email", "prompt-test@example.com"}}
	for _, a := range steps {
		if _, err := git(f.Repo, a...); err != nil {
			return nil, err
		}
	}
	if f.Repo, err = filepath.EvalSymlinks(f.Repo); err != nil {
		return nil, err
	}
	s := &core.Setup{Kind: "personal", BaseBranch: "main"}
	s.Commands.Test = "go test ./..."
	s.Commands.Lint = "go vet ./..."
	s.Commands.Build = "go build ./..."
	if _, err := core.SaveSetup(f.Repo, s); err != nil {
		return nil, err
	}
	if _, err := core.EnableSandbox(f.Repo, nil); err != nil {
		return nil, err
	}
	for _, a := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "chore: start"}} {
		if _, err := git(f.Repo, a...); err != nil {
			return nil, err
		}
	}
	if f.Base, err = git(f.Repo, "rev-parse", "HEAD"); err != nil {
		return nil, err
	}
	if _, err := git(dir, "init", "-q", "--bare", "-b", "main", f.Origin); err != nil {
		return nil, err
	}
	for _, a := range [][]string{{"remote", "add", "origin", f.Origin}, {"push", "-q", "origin", "main"}} {
		if _, err := git(f.Repo, a...); err != nil {
			return nil, err
		}
	}
	return f, nil
}
