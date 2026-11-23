package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const pemBody = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC=="

func envFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	write(t, p, body)
	return p
}

func wantNames(t *testing.T, p string, want ...string) {
	t.Helper()
	got, err := EnvNames(p)
	if err != nil {
		t.Fatalf("%s: %v", filepath.Base(p), err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: names = %q, want %q", filepath.Base(p), got, want)
	}
}

func wantRefused(t *testing.T, what, p string, secret ...string) {
	t.Helper()
	got, err := EnvNames(p)
	if err == nil {
		t.Fatalf("%s: no error, names %q", what, got)
	}
	if len(got) > 0 {
		t.Fatalf("%s: names printed with the error: %q", what, got)
	}
	for _, s := range secret {
		if strings.Contains(err.Error(), s) {
			t.Fatalf("%s: the error shows file content %q: %v", what, s, err)
		}
	}
}

func TestEnvNamesListsOnlyNames(t *testing.T) {
	p := envFile(t, ".env", "# a comment\n\nexport API_KEY=sk-live-123\nDB_URL='postgres://u:p@h/db'\n  NAME.X = \"quoted value\" # tail\nEMPTY=\n\texport\tTABBED=1\nPLAIN=a'b\"c\n")
	wantNames(t, p, "API_KEY", "DB_URL", "NAME.X", "EMPTY", "TABBED", "PLAIN")
	h := newHarness(t)
	write(t, filepath.Join(h.repo, ".env"), "KEY=1\n")
	h.expect(h.bash("plumbline env-names .env"), "allow", "")
	h.setup()
	h.startTask("normal")
	h.expect(h.bash("plumbline env-names .env"), "allow", "")
	if d, _ := decision(h.bash("./bin/plumbline env-names .env")); d == "allow" || d == "" {
		t.Errorf("a path to a program named plumbline isn't trusted with a .env: %s", d)
	}
}

func TestEnvNamesBackslashContinuationFailsClosed(t *testing.T) {
	p := envFile(t, ".env", "A=1\nKEY=abc\\\nZZZ=def\n")
	wantRefused(t, "continuation", p, "abc", "def")
	wantRefused(t, "continuation at end", envFile(t, ".env", "A=1\nKEY=abc\\\n"), "abc")
}

func pemLine(edge, kind string) string {
	return strings.Repeat("-", 5) + edge + " " + kind + " KEY" + strings.Repeat("-", 5)
}

func TestEnvNamesQuotedMultiline(t *testing.T) {
	key := "PRIVATE_KEY=\"" + pemLine("BEGIN", "PRIVATE") + "\n" + pemBody + "\nXYZ==\n" + pemLine("END", "PRIVATE") + "\"\nAFTER=1\n"
	wantNames(t, envFile(t, ".env", key), "PRIVATE_KEY", "AFTER")
	single := "K='line one\nLEAK=two\n'\nB=2\n"
	wantNames(t, envFile(t, ".env.local", single), "K", "B")
	esc := "A=\"he said \\\"hi\\\"\nLEAK=inside\n\"\nB=1\n"
	wantNames(t, envFile(t, ".env", esc), "A", "B")
	wantNames(t, envFile(t, ".env", "A=\"x\\\\\"\nB=1\n"), "A", "B")
	wantNames(t, envFile(t, ".Env", "UPPER=1\n"), "UPPER")
	wantNames(t, envFile(t, ".ENV.Production", "UPPER=1\n"), "UPPER")
}

func TestEnvNamesRefusals(t *testing.T) {
	pem := "A=1\n" + pemLine("BEGIN", "RSA PRIVATE") + "\n" + pemBody + "\n" + pemLine("END", "RSA PRIVATE") + "\n"
	wantRefused(t, "unquoted pem", envFile(t, ".env", pem), "BEGIN", pemBody)
	wantRefused(t, "bare token", envFile(t, ".env", "A=1\nsecretvalue\n"), "secretvalue")
	wantRefused(t, "unclosed quote", envFile(t, ".env", "A=1\nKEY=\"abc\nZZZ=def\n"), "abc", "def")
	wantRefused(t, "garbage after quote", envFile(t, ".env", "KEY='abc'tail\n"), "abc", "tail")
	wantRefused(t, "export alone", envFile(t, ".env", "export\n"), "export")
	wantRefused(t, "bad name", envFile(t, ".env", "1KEY=v\n"), "1KEY")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "id_rsa")
	write(t, keyFile, "ZZZ=secretpart\n")
	link := filepath.Join(dir, ".env")
	must(t, os.Symlink(keyFile, link))
	wantRefused(t, "symlink", link, "secretpart", "ZZZ")
	hard := filepath.Join(dir, ".env.local")
	must(t, os.Link(keyFile, hard))
	wantRefused(t, "hard link", hard, "secretpart", "ZZZ")
}

type failAfter struct {
	r io.Reader
	n int
}

func (f *failAfter) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("disk went away")
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	n, err := f.r.Read(p)
	f.n -= n
	return n, err
}

func TestEnvNamesReadErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".env")
	must(t, os.MkdirAll(dir, 0o755))
	wantRefused(t, "directory", dir)
	p := envFile(t, ".env", "A=1\n")
	must(t, os.Chmod(p, 0))
	t.Cleanup(func() { _ = os.Chmod(p, 0o644) })
	if f, err := os.Open(p); err == nil {
		f.Close()
		t.Skip("running as a user that reads mode-0 files")
	}
	wantRefused(t, "permission denied", p)
	body := "A=1\nB=2\nC=3\n"
	got, err := envNamesFrom(&failAfter{r: strings.NewReader(body), n: 6})
	if err == nil || len(got) > 0 {
		t.Fatalf("halfway read error: names %q err %v", got, err)
	}
}

func TestEnvNamesLongLine(t *testing.T) {
	big := strings.Repeat("a", 2<<20)
	wantNames(t, envFile(t, ".env", "BIG="+big+"\nQ=\""+big+"\"\nLAST=1\n"), "BIG", "Q", "LAST")
}

func TestSecretReadPointsToEnvNames(t *testing.T) {
	h := newHarness(t)
	write(t, filepath.Join(h.repo, ".env"), "KEY=1\n")
	h.expect(h.bash("cut -d= -f1 .env"), "ask", "plumbline env-names")
	h.expect(h.bash("cat .env"), "ask", "plumbline env-names")
	for _, c := range []string{"sed 's/=.*//' .env", "cut -d= -f1 .env", "cat .env | cut -d= -f1"} {
		h.expect(h.bash(c), "ask", ".env")
	}
	write(t, filepath.Join(h.repo, "id_rsa"), "x\n")
	if _, r := decision(h.bash("cat id_rsa")); strings.Contains(r, "env-names") {
		t.Fatalf("a key file read points to env-names: %s", r)
	}
}

func TestHelmShowFromNetworkIsOnline(t *testing.T) {
	h := newHarness(t)
	h.setup()
	cmds := []string{
		"helm show values oci://reg.example.com/c", "helm show chart https://x/c.tgz", "helm show chart --repo https://x c",
		"helm show chart --repo=https://x c", "helm show chart bitnami/nginx", "helm search hub nginx", "helm show all HTTP://x/c.tgz",
	}
	check := func(when string) {
		for _, cmd := range cmds {
			if d, r := decision(h.bash(cmd)); d != "" {
				t.Errorf("%s: %q: %s (%s), want it left to Claude Code", when, cmd, d, r)
			}
		}
		for _, cmd := range []string{"helm show values ./chart", "helm show values chart.tgz", "helm get values web", "docker inspect web", "helm search repo nginx"} {
			if d, r := decision(h.bash(cmd)); d != "allow" {
				t.Errorf("%s: %q: %q (%s), want allow", when, cmd, d, r)
			}
		}
		for _, cmd := range []string{"helm show values oci://reg.example.com/c --kubeconfig ./evil.yaml", "helm show values oci://x/c; helm uninstall web"} {
			if d, _ := decision(h.bash(cmd)); d == "allow" || d == "" {
				t.Errorf("%s: %q: %q, want it not left alone", when, cmd, d)
			}
		}
	}
	check("no task")
	must(t, os.MkdirAll(filepath.Join(h.repo, "charts", "web"), 0o755))
	if d, r := decision(h.bash("helm show values charts/web")); d != "allow" {
		t.Errorf("a chart folder in the repo: %q (%s), want allow", d, r)
	}
	h.startTask("normal")
	check("while planning")
}

func TestHarmlessAsksGoToClaudeCode(t *testing.T) {
	h := newHarness(t)
	h.setup()
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 3 }\n")
	run(t, h.repo, "git", "branch", "other")
	must(t, os.MkdirAll(filepath.Join(h.repo, "coverage"), 0o755))
	write(t, filepath.Join(h.repo, "coverage", "sprint.md"), "- [ ] x\n")
	noTask := &Task{ID: NewID(), Title: "no session", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now()}
	ended := &Task{ID: NewID(), Title: "ended", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now(), SessionID: "old-sess"}
	mine := &Task{ID: NewID(), Title: "mine", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now(), SessionID: h.sid}
	live := &Task{ID: NewID(), Title: "live", Size: "small", Root: h.repo, Branch: "main", Phase: PhaseUnderstanding, Created: Now(), SessionID: "sess-2"}
	for _, tk := range []*Task{noTask, ended, mine, live} {
		must(t, SaveTask(tk))
	}
	write(t, sessionEndedPath("old-sess"), Now().Format("2006-01-02T15:04:05Z07:00")+"\n")
	for _, cmd := range []string{
		"git switch other", "plumbline task adopt " + noTask.ID, "plumbline task adopt " + ended.ID, "plumbline task adopt " + mine.ID,
	} {
		if d, r := decision(h.bash(cmd)); d == "ask" || d == "deny" {
			t.Errorf("%q: %s (%s), want no Plumbline prompt", cmd, d, r)
		}
	}
	h.expect(h.bash("plumbline task adopt "+live.ID), "ask", "another Claude session")
	h.expect(h.bash("plumbline task adopt $ID"), "ask", "")
	h.expect(h.bash("plumbline task adopt nosuchtask"), "ask", "")
	h.expect(h.bash("plumbline setup undo-sandbox"), "ask", "sandbox settings")
	h.expect(h.bash("plumbline setup sandbox --exclude curl"), "ask", "sandbox")
	h.expect(h.bash("plumbline setup sandbox"), "ask", "sandbox")
	h.expect(h.bash("plumbline standup tracker --set coverage/sprint.md"), "ask", "sprint file")
	h.expect(h.bash("git reset --hard"), "ask", "throws away")
	h.expect(h.bash("docker push reg.example.com/x"), "ask", "registry")
}

func TestCheckoutOfAChangedFileStillAsks(t *testing.T) {
	h := buildingTask(t)
	run(t, h.repo, "git", "branch", "other")
	write(t, filepath.Join(h.repo, "src", "a.go"), "package src\n\nfunc A() int { return 4 }\n")
	for _, cmd := range []string{"git checkout src/a.go", "git checkout HEAD src/a.go", "git checkout src", "git checkout .",
		"git -C src checkout a.go", "git checkout --pathspec-from-file=paths.txt", "cd \"$X\" && git checkout a.go",
		"git restore --staged --worktree src/a.go", "git checkout -m other", "git switch -m other", "git checkout other",
		"git switch -qf other", "git switch --discard other", "git switch --for other", "git restore --staged -qW src/a.go",
		"git restore --staged --worktr src/a.go", "git -C \"$WT\" checkout a.go", "git --work-tree=../wt checkout a.go", "git switch \"$B\""} {
		if d, r := decision(h.bash(cmd)); d != "ask" {
			t.Errorf("%q with an edited file: %s (%s), want an ask", cmd, d, r)
		}
	}
	for _, cmd := range []string{"git switch other", "git checkout -b fresh", "git restore --staged src/a.go"} {
		if d, r := decision(h.bash(cmd)); d == "ask" || d == "deny" {
			t.Errorf("%q: %s (%s), want no Plumbline prompt", cmd, d, r)
		}
	}
	run(t, h.repo, "git", "checkout", "--", "src/a.go")
	if d, r := decision(h.bash("cd \"$X\" && git checkout a.go")); d != "ask" {
		t.Errorf("a checkout in a folder Plumbline can't work out: %s (%s), want an ask", d, r)
	}
	for _, cmd := range []string{"git checkout other", "git checkout src/a.go"} {
		if d, r := decision(h.bash(cmd)); d == "ask" || d == "deny" {
			t.Errorf("%q on a clean tree: %s (%s), want no Plumbline prompt", cmd, d, r)
		}
	}
}

func TestEnvNamesFreeOnlyForThePlumblineCommand(t *testing.T) {
	h := newHarness(t)
	h.setup()
	if d, r := decision(h.bash("plumbline env-names .env")); d == "ask" || d == "deny" {
		t.Errorf("plumbline env-names: %s (%s), want no Plumbline prompt", d, r)
	}
	for _, cmd := range []string{"./plumbline env-names .env", "/tmp/x/plumbline env-names .env", "PATH=/tmp/x plumbline env-names .env"} {
		if d, r := decision(h.bash(cmd)); d != "ask" && d != "deny" {
			t.Errorf("%q: %s (%s), want a prompt: it may not be Plumbline", cmd, d, r)
		}
	}
}
