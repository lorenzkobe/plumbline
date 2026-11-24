// Package fixture is driven by testshard's tests through `go test`. Each test
// prints its process id; FIXTURE_MODE makes some of them misbehave.
package fixture

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lorenzkobe/plumbline/internal/testshard"
)

func TestMain(m *testing.M) { os.Exit(testshard.Main(m)) }

func mode(name string) bool {
	for _, m := range strings.Split(os.Getenv("FIXTURE_MODE"), ",") {
		if m == name {
			return true
		}
	}
	return false
}

func pid(t *testing.T) {
	t.Helper()
	fmt.Printf("pid %s %d\n", t.Name(), os.Getpid())
	if mode("slow-others") && t.Name() != "TestAlpha" && !strings.Contains(t.Name(), "/") {
		d, err := time.ParseDuration(os.Getenv("FIXTURE_SLEEP"))
		if err != nil {
			d = time.Second
		}
		time.Sleep(d)
	}
}

func TestAlpha(t *testing.T) {
	pid(t)
	t.Setenv("FIXTURE_SHARED", "alpha")
	if mode("fail-alpha") {
		t.Fatal("alpha failed on purpose")
	}
}

func TestBeta(t *testing.T) {
	pid(t)
	if v := os.Getenv("FIXTURE_SHARED"); v != "" {
		t.Fatalf("another test's env leaked in: %q", v)
	}
	if mode("panic-beta") {
		panic("beta panicked on purpose")
	}
	if mode("exit-beta") {
		os.Exit(3)
	}
}

func TestGamma(t *testing.T) {
	pid(t)
	if mode("hang-gamma") {
		time.Sleep(time.Minute)
	}
	t.Run("sub", func(t *testing.T) { pid(t) })
}

func TestLongOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("long only")
	}
	pid(t)
}

func TestDelta(t *testing.T) { pid(t) }

func TestReadsFile(t *testing.T) {
	pid(t)
	if p := os.Getenv("FIXTURE_READ"); p != "" {
		if _, err := os.ReadFile(p); err != nil {
			t.Fatal(err)
		}
	}
}

func BenchmarkNever(b *testing.B) {
	fmt.Println("benchmark ran")
}
