package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	shardMinTests = 40
	shardMax      = 8
	shardPerTests = 15
)

type GoTestCmd struct {
	Flags []string
	Pkgs  []string
}

var (
	goTestSafeFlag = regexp.MustCompile(`^-(count=\d+|race|v|short|timeout=[0-9smh.]+)$`)
	goTestPkgArg   = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)
	goTestFuncDecl = regexp.MustCompile(`(?m)^func (Test|Example|Fuzz)[A-Za-z0-9_]*\(`)
	goTopName      = regexp.MustCompile(`^(Test|Example|Fuzz)[A-Za-z0-9_]*$`)
	goTopFail      = regexp.MustCompile(`(?m)^--- FAIL: (\S+)`)
)

func ParseGoTest(command string) (GoTestCmd, bool) {
	var p GoTestCmd
	if strings.ContainsAny(command, "|&;<>()$`\\\"'*?[]{}#~\n") {
		return p, false
	}
	words := strings.Fields(command)
	if len(words) < 2 || words[0] != "go" || words[1] != "test" {
		return p, false
	}
	for _, w := range words[2:] {
		switch {
		case strings.HasPrefix(w, "-"):
			if !goTestSafeFlag.MatchString(w) {
				return p, false
			}
			p.Flags = append(p.Flags, w)
		case goTestPkgArg.MatchString(w) && !strings.Contains(w, "="):
			p.Pkgs = append(p.Pkgs, w)
		default:
			return p, false
		}
	}
	if len(p.Pkgs) == 0 {
		p.Pkgs = []string{"."}
	}
	return p, true
}

func shardSlots() int {
	n := runtime.NumCPU() - 2
	if n > shardMax {
		n = shardMax
	}
	if n < 1 {
		n = 1
	}
	return n
}

type goShardState struct {
	Alone map[string][]string `json:"alone,omitempty"`
}

func goShardStatePath(root string) string {
	return filepath.Join(ProjectDir(root), "go-shard.json")
}

func loadGoShardState(root string) goShardState {
	var s goShardState
	if b, err := os.ReadFile(goShardStatePath(root)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func saveGoShardState(root string, s goShardState) error {
	b, _ := json.Marshal(s)
	if err := os.MkdirAll(filepath.Dir(goShardStatePath(root)), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(goShardStatePath(root), b)
}

func ClearGoShardState(root string) {
	_ = os.Remove(goShardStatePath(root))
}

var shardBinOnce sync.Once
var shardBin string

func shardBinary() string {
	shardBinOnce.Do(func() {
		var cands []string
		if exe, err := os.Executable(); err == nil && (filepath.Base(exe) == "plumbline" || strings.HasPrefix(filepath.Base(exe), "plumbline-")) {
			cands = append(cands, exe)
		}
		if p, err := exec.LookPath("plumbline"); err == nil {
			cands = append(cands, p)
		}
		for _, c := range cands {
			out, err := exec.Command(c, "gotest-shard", "--probe").Output()
			if err == nil && strings.TrimSpace(string(out)) == "gotest-shard ok" {
				shardBin = c
				return
			}
		}
	})
	return shardBin
}

func goShardWrap(root, command string) (string, bool) {
	if _, ok := ParseGoTest(command); !ok || shardSlots() < 2 {
		return command, false
	}
	bin := shardBinary()
	if bin == "" {
		return command, false
	}
	return shellQuoteWord(bin) + " gotest-shard -- " + command, true
}

func shellQuoteWord(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type goPkg struct {
	ImportPath string
	Dir        string
	Files      []string
}

func goListPkgs(dir string, c GoTestCmd) ([]goPkg, error) {
	args := []string{"list", "-e", "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .TestGoFiles \" \"}} {{join .XTestGoFiles \" \"}}"}
	for _, f := range c.Flags {
		if f == "-race" {
			args = append(args, f)
		}
	}
	args = append(args, c.Pkgs...)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s", err, strings.TrimSpace(errb.String()))
	}
	var pkgs []goPkg
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		parts := strings.SplitN(l, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		pkgs = append(pkgs, goPkg{ImportPath: parts[0], Dir: parts[1], Files: strings.Fields(parts[2])})
	}
	return pkgs, nil
}

func countTestFuncs(p goPkg) int {
	n := 0
	for _, f := range p.Files {
		b, err := os.ReadFile(filepath.Join(p.Dir, f))
		if err != nil {
			return 0
		}
		n += len(goTestFuncDecl.FindAll(b, -1))
	}
	return n
}

func goTestList(dir string, c GoTestCmd, pkg string) ([]string, string, error) {
	args := []string{"test"}
	for _, f := range c.Flags {
		if f == "-race" || f == "-short" {
			args = append(args, f)
		}
	}
	args = append(args, "-list", ".", pkg)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	var names []string
	for _, l := range strings.Split(b.String(), "\n") {
		l = strings.TrimSpace(l)
		switch {
		case l == "" || strings.HasPrefix(l, "ok ") || strings.HasPrefix(l, "ok\t"):
		case goTopName.MatchString(l):
			names = append(names, l)
		case strings.HasPrefix(l, "Benchmark"):
		default:
			return nil, b.String(), fmt.Errorf("unexpected line from go test -list: %q", l)
		}
	}
	return names, b.String(), err
}

func runPattern(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = regexp.QuoteMeta(n)
	}
	return "^(" + strings.Join(q, "|") + ")$"
}

type shardJob struct {
	names []string
	args  []string
	out   bytes.Buffer
	exit  int
}

func (j *shardJob) run(dir string) {
	cmd := exec.Command("go", j.args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &j.out, &j.out
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		j.exit = 1
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
			j.exit = ee.ExitCode()
		}
	}
}

func splitNames(names []string, k int) [][]string {
	out := make([][]string, 0, k)
	per := (len(names) + k - 1) / k
	for i := 0; i < len(names); i += per {
		end := i + per
		if end > len(names) {
			end = len(names)
		}
		out = append(out, names[i:end])
	}
	return out
}

type shardedPkg struct {
	pkg    string
	jobs   []*shardJob
	start  time.Time
	finish time.Time
}

func RunGoShard(dir, command string, out io.Writer) int {
	c, ok := ParseGoTest(command)
	if !ok {
		fmt.Fprintln(out, "plumbline: gotest-shard only runs a plain go test command; nothing ran.")
		return 2
	}
	pkgs, err := goListPkgs(dir, c)
	if err != nil || len(pkgs) == 0 {
		j := &shardJob{args: append(append([]string{"test"}, c.Flags...), c.Pkgs...)}
		j.run(dir)
		_, _ = out.Write(j.out.Bytes())
		return j.exit
	}
	var plain []string
	var big []goPkg
	for _, p := range pkgs {
		if countTestFuncs(p) >= shardMinTests {
			big = append(big, p)
		} else {
			plain = append(plain, p.ImportPath)
		}
	}
	root := RepoRoot(dir)
	if root == "" {
		root = dir
	}
	state := loadGoShardState(root)
	slots := make(chan struct{}, shardSlots())
	var wg, first sync.WaitGroup
	firstDone := make(chan struct{})
	var mu, exclusive sync.Mutex
	exit := 0
	rescuedAll := map[string][]string{}
	finishPkg := func(sp *shardedPkg) {
		sp.finish = time.Now()
		var buf bytes.Buffer
		ok := true
		var rescued []string
		if len(sp.jobs) == 1 {
			buf.Write(sp.jobs[0].out.Bytes())
			ok = sp.jobs[0].exit == 0
		} else {
			ok, rescued = mergeShards(dir, c, sp, &buf, slots)
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = out.Write(buf.Bytes())
		if !ok {
			exit = 1
		}
		if len(rescued) > 0 {
			rescuedAll[sp.pkg] = append(rescuedAll[sp.pkg], rescued...)
		}
	}
	launchPkg := func(sp *shardedPkg, after *shardJob) {
		var left sync.WaitGroup
		for _, j := range sp.jobs {
			left.Add(1)
			wg.Add(1)
			first.Add(1)
			go func(j *shardJob) {
				defer wg.Done()
				defer left.Done()
				defer first.Done()
				slots <- struct{}{}
				j.run(dir)
				<-slots
			}(j)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			left.Wait()
			if after != nil {
				<-firstDone
				exclusive.Lock()
				for i := 0; i < cap(slots); i++ {
					slots <- struct{}{}
				}
				after.run(dir)
				for i := 0; i < cap(slots); i++ {
					<-slots
				}
				exclusive.Unlock()
				sp.jobs = append(sp.jobs, after)
			}
			finishPkg(sp)
		}()
	}
	if len(plain) > 0 {
		launchPkg(&shardedPkg{start: time.Now(), jobs: []*shardJob{{args: append(append([]string{"test"}, c.Flags...), plain...)}}}, nil)
	}
	for _, p := range big {
		names, _, err := goTestList(dir, c, p.ImportPath)
		sp := &shardedPkg{pkg: p.ImportPath, start: time.Now()}
		if err != nil || len(names) < shardMinTests {
			sp.jobs = []*shardJob{{args: append(append([]string{"test"}, c.Flags...), p.ImportPath)}}
			launchPkg(sp, nil)
			continue
		}
		known := map[string]bool{}
		for _, n := range names {
			known[n] = true
		}
		alone := map[string]bool{}
		var aloneNames []string
		for _, n := range state.Alone[p.ImportPath] {
			if known[n] && !alone[n] {
				alone[n] = true
				aloneNames = append(aloneNames, n)
			}
		}
		var rest []string
		for _, n := range names {
			if !alone[n] {
				rest = append(rest, n)
			}
		}
		var after *shardJob
		if len(aloneNames) > 0 {
			after = &shardJob{args: append(append([]string{"test"}, freshFlags(c.Flags)...), "-p", "1", "-parallel", "1", p.ImportPath, "-run", runPattern(aloneNames))}
		}
		k := (len(rest) + shardPerTests - 1) / shardPerTests
		if k > shardSlots() {
			k = shardSlots()
		}
		if k < 2 {
			k = 2
		}
		if len(rest) < k {
			k = len(rest)
		}
		if k > 0 {
			for _, group := range splitNames(rest, k) {
				sp.jobs = append(sp.jobs, &shardJob{names: group, args: append(append([]string{"test"}, c.Flags...), p.ImportPath, "-run", runPattern(group))})
			}
		}
		launchPkg(sp, after)
	}
	go func() {
		first.Wait()
		close(firstDone)
	}()
	wg.Wait()
	if len(rescuedAll) > 0 {
		rememberAlone(root, rescuedAll, out)
	}
	return exit
}

func mergeShards(dir string, c GoTestCmd, sp *shardedPkg, out io.Writer, slots chan struct{}) (bool, []string) {
	summary := regexp.MustCompile(`^(ok|FAIL)\s+` + regexp.QuoteMeta(sp.pkg) + `(\s|$)`)
	var body strings.Builder
	allOK, allCached := true, true
	var bad []*shardJob
	for _, j := range sp.jobs {
		text := j.out.String()
		if j.exit != 0 {
			allOK = false
			bad = append(bad, j)
		}
		for _, l := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
			if summary.MatchString(l) {
				if !strings.Contains(l, "(cached)") {
					allCached = false
				}
				continue
			}
			if l == "PASS" || l == "FAIL" {
				continue
			}
			body.WriteString(l + "\n")
		}
	}
	text := body.String()
	elapsed := fmt.Sprintf("%.3fs", sp.finish.Sub(sp.start).Seconds())
	if allOK {
		_, _ = io.WriteString(out, text)
		if allCached {
			fmt.Fprintf(out, "ok  \t%s\t(cached)\n", sp.pkg)
		} else {
			fmt.Fprintf(out, "ok  \t%s\t%s\n", sp.pkg, elapsed)
		}
		return true, nil
	}
	alone := map[string]bool{}
	var rescued []string
	allRescued := true
	for _, j := range bad {
		named := goTopFail.FindAllStringSubmatch(j.out.String(), -1)
		if len(named) == 0 || len(j.names) == 0 || unrescuable(j.out.String()) {
			allRescued = false
			continue
		}
		again := &shardJob{args: append(append([]string{"test"}, freshFlags(c.Flags)...), "-p", "1", "-parallel", "1", sp.pkg, "-run", runPattern(j.names))}
		slots <- struct{}{}
		again.run(dir)
		<-slots
		if again.exit != 0 {
			allRescued = false
			continue
		}
		for _, m := range named {
			if !alone[m[1]] {
				alone[m[1]] = true
				rescued = append(rescued, m[1])
			}
		}
	}
	if allRescued && len(rescued) > 0 {
		lines := strings.Split(text, "\n")
		for i, l := range lines {
			trim := strings.TrimLeft(l, " ")
			if !strings.HasPrefix(trim, "--- FAIL: ") {
				continue
			}
			name := strings.Fields(strings.TrimPrefix(trim, "--- FAIL: "))[0]
			if alone[strings.SplitN(name, "/", 2)[0]] {
				lines[i] = strings.Repeat(" ", len(l)-len(trim)) + flakyMark + " " + strings.TrimPrefix(trim, "--- FAIL: ")
			}
		}
		_, _ = io.WriteString(out, strings.Join(lines, "\n"))
		fmt.Fprintf(out, "ok  \t%s\t%s\n", sp.pkg, elapsed)
		return true, rescued
	}
	_, _ = io.WriteString(out, text)
	fmt.Fprintf(out, "FAIL\t%s\t%s\n", sp.pkg, elapsed)
	return false, nil
}

func rememberAlone(root string, rescued map[string][]string, out io.Writer) {
	s := loadGoShardState(root)
	if s.Alone == nil {
		s.Alone = map[string][]string{}
	}
	var all []string
	for pkg, names := range rescued {
		have := map[string]bool{}
		for _, n := range s.Alone[pkg] {
			have[n] = true
		}
		for _, n := range names {
			if !have[n] {
				have[n] = true
				s.Alone[pkg] = append(s.Alone[pkg], n)
			}
			all = append(all, n)
		}
	}
	_ = saveGoShardState(root, s)
	fmt.Fprintf(out, "plumbline: these tests failed only when run in parallel and passed alone: %s. From now on Plumbline runs them on their own, after the parallel ones, so the rest stays parallel. Make them parallel-safe when you can; saving the setup again forgets the list.\n", strings.Join(uniq(all), ", "))
}

func unrescuable(out string) bool {
	return strings.HasPrefix(out, "panic: ") || strings.Contains(out, "\npanic: ") ||
		strings.Contains(out, "WARNING: DATA RACE") || strings.Contains(out, "race detected during execution of test")
}

const flakyMark = "--- FLAKY (failed in parallel, passed alone):"

func freshFlags(flags []string) []string {
	var out []string
	for _, f := range flags {
		if !strings.HasPrefix(f, "-count=") {
			out = append(out, f)
		}
	}
	return append(out, "-count=1")
}
