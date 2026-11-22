package prompttest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type logBuilder struct {
	lines []string
	n     int
}

func (b *logBuilder) add(v map[string]any) {
	j, _ := json.Marshal(v)
	b.lines = append(b.lines, string(j))
}

func (b *logBuilder) call(name string, input map[string]any, result string, isErr bool) {
	b.callIn("", name, input, result, isErr)
}

func (b *logBuilder) callIn(parent, name string, input map[string]any, result string, isErr bool) {
	b.n++
	id := "toolu_" + string(rune('a'+b.n))
	var p any
	if parent != "" {
		p = parent
	}
	b.add(map[string]any{"type": "assistant", "parent_tool_use_id": p, "message": map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "working"},
		map[string]any{"type": "tool_use", "id": id, "name": name, "input": input},
	}}})
	if result == "-" {
		return
	}
	b.add(map[string]any{"type": "user", "parent_tool_use_id": p, "message": map[string]any{"content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": id, "content": []any{map[string]any{"type": "text", "text": result}}, "is_error": isErr},
	}}})
}

func (b *logBuilder) parse(t *testing.T) *Log {
	t.Helper()
	b.add(map[string]any{"type": "result", "subtype": "success", "total_cost_usd": 1.25, "num_turns": 9, "result": "done"})
	l, err := Parse(strings.NewReader("not json\n" + strings.Join(b.lines, "\n") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

const repo = "/tmp/pt/repo"

func approved() map[string]any { return map[string]any{"questions": []any{}} }

func goodRun() *logBuilder {
	b := &logBuilder{}
	b.call("Bash", map[string]any{"command": `plumbline task start --title "Hi" --size small`}, "plumbline-task-id: 1", false)
	b.call("Edit", map[string]any{"file_path": repo + "/greet/greet.go"}, "PreToolUse:Edit hook error: Plumbline: task 1 is at the planning step.", true)
	b.call("AskUserQuestion", approved(), `User has answered your questions: "Plan?"="Approve".`, false)
	b.call("Edit", map[string]any{"file_path": repo + "/greet/greet.go"}, "ok", false)
	b.call("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer"}, "review done", false)
	b.call("Bash", map[string]any{"command": "plumbline task finish"}, "finished", false)
	return b
}

func TestParse(t *testing.T) {
	b := goodRun()
	b.add(map[string]any{"type": "permission", "tool": "AskUserQuestion", "allowed": true, "answers": map[string]any{"Plan?": "Approve"}})
	b.callIn("toolu_x", "Read", map[string]any{"file_path": repo + "/a"}, "-", false)
	l := b.parse(t)
	if len(l.Calls) != 7 || l.Cost != 1.25 || l.Turns != 9 || l.Outcome != "success" || l.Final != "done" {
		t.Fatalf("parsed %+v", l)
	}
	if c := l.Calls[1]; !c.Done || !c.Error || !c.Refused() || !strings.Contains(c.Result, "planning step") {
		t.Fatalf("refused edit: %+v", c)
	}
	if last := l.Calls[6]; !last.Helper || last.Done {
		t.Fatalf("helper call without a result: %+v", last)
	}
	if len(l.Prompts) != 1 || l.Prompts[0].Answers["Plan?"] != "Approve" {
		t.Fatalf("prompts: %+v", l.Prompts)
	}
	if got := Refusals(l); len(got) != 1 || !strings.Contains(got[0], "Edit /tmp/pt/repo/greet/greet.go") {
		t.Fatalf("refusals: %v", got)
	}
}

func TestChecksPass(t *testing.T) {
	l := goodRun().parse(t)
	for _, c := range []check{taskStartedFirst, noEditBeforeApproval, planApproved, reviewerRan, taskFinished, noPush} {
		if r := c(l, Env{Repo: repo}); !r.OK {
			t.Errorf("%s: %s", r.Check, r.Detail)
		}
	}
}

func TestChecksFail(t *testing.T) {
	early := &logBuilder{}
	early.call("Write", map[string]any{"file_path": "greet/new.go"}, "ok", false)
	early.call("Bash", map[string]any{"command": "plumbline task start --title x"}, "id", false)
	early.call("Bash", map[string]any{"command": "git push origin main"}, "pushed", false)
	early.call("Agent", map[string]any{"subagent_type": "plumbline:code-reviewer"}, "done", false)
	early.call("Bash", map[string]any{"command": "plumbline task finish"}, "Plumbline: not done", true)
	l := early.parse(t)
	env := Env{Repo: repo}
	want := map[string]string{
		"task started first":      "before the task: Write greet/new.go",
		"no edit before approval": "Write greet/new.go",
		"plan question answered":  "no AskUserQuestion",
		"code reviewer started":   "after approval",
		"task finished":           "never ran clean",
		"no push":                 "git push origin main",
	}
	for _, c := range []check{taskStartedFirst, noEditBeforeApproval, planApproved, reviewerRan, taskFinished, noPush} {
		r := c(l, env)
		if r.OK || !strings.Contains(r.Detail, want[r.Check]) {
			t.Errorf("%s should fail with %q: %+v", r.Check, want[r.Check], r)
		}
	}
}

func TestEditsOutsideTheRepoDontCount(t *testing.T) {
	b := &logBuilder{}
	b.call("Write", map[string]any{"file_path": "/tmp/pt/data/projects/x/tasks/1/docs/plan.md"}, "ok", false)
	b.call("Write", map[string]any{"file_path": "/tmp/pt/repository/x.go"}, "ok", false)
	b.call("Bash", map[string]any{"command": "plumbline task start --title x"}, "id", false)
	b.call("Bash", map[string]any{"command": "git push origin main"}, "Plumbline: no pushing", true)
	l := b.parse(t)
	for _, c := range []check{taskStartedFirst, noEditBeforeApproval, noPush} {
		if r := c(l, Env{Repo: repo}); !r.OK {
			t.Errorf("%s: %s", r.Check, r.Detail)
		}
	}
}

func TestRejectedPlanIsNoApproval(t *testing.T) {
	b := &logBuilder{}
	b.call("AskUserQuestion", approved(), `User has answered your questions: "Plan?"="Change something".`, false)
	b.call("AskUserQuestion", approved(), `Plumbline: pre-answered`, true)
	b.call("AskUserQuestion", approved(), `Plumbline: "Approve" can't come pre-answered`, true)
	if approvalAt(b.parse(t)) >= 0 {
		t.Fatal("neither question approved the plan")
	}
}

func TestFixtureAndRepoChecks(t *testing.T) {
	t.Setenv("PLUMBLINE_HOME", "")
	dir := t.TempDir()
	f, err := MakeFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PLUMBLINE_HOME") != f.Data {
		t.Fatalf("PLUMBLINE_HOME = %q", os.Getenv("PLUMBLINE_HOME"))
	}
	for _, p := range []string{".plumbline.json", ".claude/settings.local.json", "greet/greet.go", "greet/greet_test.go", ".cache/tmp"} {
		if _, err := os.Stat(filepath.Join(f.Repo, p)); err != nil {
			t.Fatalf("fixture misses %s: %v", p, err)
		}
	}
	if st, _ := git(f.Repo, "status", "--porcelain"); st != "" {
		t.Fatalf("fixture should start clean: %q", st)
	}
	env := Env{Repo: f.Repo, Origin: f.Origin, Base: f.Base}
	empty := (&logBuilder{}).parse(t)
	for _, c := range []check{noNewComments, noPush} {
		if r := c(empty, env); !r.OK {
			t.Fatalf("%s on a fresh fixture: %s", r.Check, r.Detail)
		}
	}
	g := filepath.Join(f.Repo, "greet", "greet.go")
	if err := os.WriteFile(g, []byte("package greet\n\nimport \"fmt\"\n\n// Greeting greets.\nfunc Greeting(name string) string {\n\treturn fmt.Sprintf(\"Hi, %s\", name)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(f.Repo, "commit", "-qam", "hi"); err != nil {
		t.Fatal(err)
	}
	if r := noNewComments(empty, env); r.OK || r.Detail != "greet/greet.go:5" {
		t.Fatalf("a committed comment counts: %+v", r)
	}
	if _, err := git(f.Repo, "push", "-q", "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if r := noPush(empty, env); r.OK || !strings.Contains(r.Detail, "origin moved") {
		t.Fatalf("a push from the terminal counts: %+v", r)
	}
}

func TestScenarios(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Scenarios {
		if s.Name == "" || s.Prompt == "" || s.BudgetUSD <= 0 || len(s.checks) == 0 || len(s.Answers) == 0 || seen[s.Name] {
			t.Fatalf("bad scenario %+v", s)
		}
		seen[s.Name] = true
		if got, ok := Find(s.Name); !ok || got.Prompt != s.Prompt {
			t.Fatalf("Find(%s)", s.Name)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Fatal("unknown scenario found")
	}
}

func TestApprovalByPrefix(t *testing.T) {
	b := &logBuilder{}
	b.call("AskUserQuestion", approved(), `User has answered your questions: "Plan?"="Approve and start".`, false)
	if approvalAt(b.parse(t)) != 0 {
		t.Fatal("an Approve... label approves")
	}
}
