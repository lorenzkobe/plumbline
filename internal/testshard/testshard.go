// Package testshard runs each top-level test of a package in its own copy of
// the package's test binary, several at once. internal/core's tests share
// process state (t.Setenv, package clocks) through one harness, so they
// can't use t.Parallel; separate processes don't share it.
//
// Use it from TestMain: func TestMain(m *testing.M) { os.Exit(testshard.Main(m)) }
package testshard

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// EnvShards sets how many tests run at once; 1 runs them all in this process.
const EnvShards = "PLUMBLINE_TEST_SHARDS"

const envChild = "PLUMBLINE_TEST_SHARD_CHILD"

const logFlag = "test.testlogfile"

// With any of these set, the tests run in this process, as go test would run
// them: they pick the tests themselves, or write one file for the package.
var inProcessFlags = []string{"test.run", "test.list", "test.bench", "test.fuzz",
	"test.coverprofile", "test.gocoverdir", "test.cpuprofile", "test.memprofile",
	"test.blockprofile", "test.mutexprofile", "test.trace", "test.outputdir"}

var testlogMagic = []byte("# test log\n") // cmd/go's marker for -test.testlogfile

// Main is a TestMain body; it returns the exit code.
func Main(m *testing.M) int {
	if !flag.Parsed() {
		flag.Parse()
	}
	n := shards()
	if n <= 1 || os.Getenv(envChild) != "" || anySet(inProcessFlags) {
		return m.Run()
	}
	names, err := list()
	if err != nil {
		fmt.Printf("testshard: listing tests: %v\n%sFAIL\n", err, marker())
		return 1
	}
	return run(names, n)
}

func shards() int {
	if v, err := strconv.Atoi(os.Getenv(EnvShards)); err == nil && v >= 1 {
		return v
	}
	return min(runtime.NumCPU(), 8)
}

func anySet(names []string) bool {
	for _, n := range names {
		if f := flag.Lookup(n); f != nil && f.Value.String() != f.DefValue {
			return true
		}
	}
	return false
}

func flagValue(name string) string {
	if f := flag.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

// marker is the byte go test -json's framing puts before PASS and FAIL.
func marker() string {
	if flagValue("test.v") == "test2json" {
		return "\x16"
	}
	return ""
}

func list() ([]string, error) {
	cmd := exec.Command(os.Args[0], "-test.list", ".")
	cmd.Env = append(os.Environ(), envChild+"=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%v: %s%s", err, out.String(), errb.String())
	}
	return parseList(out.String())
}

var topName = regexp.MustCompile(`^(Test|Example|Fuzz)[\p{L}\p{N}_]*$`)

// parseList reads -test.list output. Benchmarks are left out: they run only
// with -bench, and -bench runs in process.
func parseList(s string) ([]string, error) {
	var names []string
	for _, l := range strings.Split(s, "\n") {
		switch l = strings.TrimSpace(l); {
		case l == "" || strings.HasPrefix(l, "Benchmark"):
		case topName.MatchString(l):
			names = append(names, l)
		default:
			return nil, fmt.Errorf("unexpected line from -test.list: %q", l)
		}
	}
	if len(names) == 0 {
		return nil, errors.New("no tests listed")
	}
	return names, nil
}

// run hands the tests out to n processes, prints each one's output whole as
// it ends, and prints one PASS or FAIL for the package. It keeps the whole
// run to -test.timeout, as m.Run would, and stops the running tests when
// the time is up or go test signals it: go test kills only this process.
func run(names []string, n int) int {
	logPath := flagValue(logFlag)
	base := childArgs(os.Args[1:])
	failfast := flagValue("test.failfast") == "true"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	running := map[string]time.Time{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	abort := func(why string) {
		mu.Lock()
		var now []string
		for name, at := range running {
			now = append(now, fmt.Sprintf("\t%s (%s)\n", name, time.Since(at).Round(time.Second)))
		}
		mu.Unlock()
		slices.Sort(now)
		cancel()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
		mu.Lock()
		fmt.Printf("%s\nrunning tests:\n%s%sFAIL\n", why, strings.Join(now, ""), marker())
		os.Exit(2)
	}
	if d, err := time.ParseDuration(flagValue("test.timeout")); err == nil && d > 0 {
		t := time.AfterFunc(d, func() { abort("panic: test timed out after " + d.String()) })
		defer t.Stop()
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGQUIT, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sig)
	go func() {
		if s, ok := <-sig; ok {
			abort("testshard: stopped by " + s.String())
		}
	}()
	type job struct {
		i    int
		name string
	}
	queue := make(chan job)
	logs := make([]string, len(names))
	failed, stop := false, false
	for range min(n, len(names)) {
		wg.Go(func() {
			for j := range queue {
				mu.Lock()
				skip := stop || ctx.Err() != nil
				if !skip {
					running[j.name] = time.Now()
				}
				mu.Unlock()
				if skip {
					continue
				}
				args := append([]string{}, base...)
				if logPath != "" {
					logs[j.i] = logPath + ".shard" + strconv.Itoa(j.i)
					args = append(args, "-"+logFlag+"="+logs[j.i])
				}
				out, code := child(ctx, j.name, append(args, "-test.run=^"+regexp.QuoteMeta(j.name)+"$"))
				mu.Lock()
				delete(running, j.name)
				if ctx.Err() != nil {
					mu.Unlock()
					return
				}
				_, _ = os.Stdout.Write(stripResult(out))
				if code != 0 {
					failed = true
					stop = stop || failfast
				}
				mu.Unlock()
			}
		})
	}
	for i, name := range names {
		mu.Lock()
		s := stop
		mu.Unlock()
		if s {
			break
		}
		queue <- job{i, name}
	}
	close(queue)
	wg.Wait()
	if ctx.Err() != nil {
		select {} // abort stopped the run and exits
	}
	if logPath != "" && !failed {
		if err := mergeLogs(logPath, logs); err != nil {
			fmt.Printf("testshard: not cacheable: %v\n", err)
		}
	}
	for _, l := range logs {
		if l != "" {
			_ = os.Remove(l)
		}
	}
	if failed {
		fmt.Printf("%sFAIL\n", marker())
		return 1
	}
	fmt.Printf("%sPASS\n", marker())
	return 0
}

// childArgs is this process's arguments without -test.testlogfile: each
// child gets a log of its own, since they would all truncate one file.
func childArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := strings.TrimPrefix(strings.TrimPrefix(args[i], "-"), "-")
		switch {
		case strings.HasPrefix(a, logFlag+"="):
		case a == logFlag:
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}

func child(ctx context.Context, name string, args []string) ([]byte, int) {
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), envChild+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err == nil {
		return out.Bytes(), 0
	}
	code := 1
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() > 0 {
		code = ee.ExitCode()
	}
	if code != 1 {
		b := stripResult(out.Bytes())
		out.Reset()
		out.Write(b)
		fmt.Fprintf(&out, "testshard: %s: %v\n", name, err)
	}
	return out.Bytes(), code
}

// stripResult drops a process's closing PASS or FAIL line, with or without
// test2json's marker; run prints one for the package.
func stripResult(out []byte) []byte {
	s := strings.TrimSuffix(string(out), "\n")
	i := strings.LastIndexByte(s, '\n') + 1
	if last := strings.TrimPrefix(s[i:], "\x16"); last == "PASS" || last == "FAIL" {
		return []byte(s[:i])
	}
	return out
}

// mergeLogs writes the children's test logs as one log at dst, so the go
// command's cache key covers what every test read. Each child starts in the
// package's folder, and go reads relative paths against the last chdir, so
// each child's part starts with one. It writes nothing when a log is
// missing or cut off: go then doesn't cache the run.
func mergeLogs(dst string, logs []string) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	merged := append([]byte{}, testlogMagic...)
	for _, l := range logs {
		if l == "" {
			return errors.New("a test ran without a log")
		}
		b, err := os.ReadFile(l)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(b, testlogMagic) || b[len(b)-1] != '\n' {
			return fmt.Errorf("%s is not a complete test log", l)
		}
		merged = append(merged, "chdir "+wd+"\n"...)
		merged = append(merged, b[len(testlogMagic):]...)
	}
	return os.WriteFile(dst, merged, 0o644)
}
