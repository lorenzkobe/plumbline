package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A same-size edit made in the same second git last indexed the file must
// still change the fingerprint (git's "racy clean" case). Otherwise proof
// recorded on the old code would look current.
func TestFingerprintSeesQuickSameSizeEdit(t *testing.T) {
	for attempt := 0; attempt < 5; attempt++ {
		h := newHarness(t)
		for time.Now().Nanosecond() > 200e6 { // start early in a second
			time.Sleep(10 * time.Millisecond)
		}
		sec := time.Now().Unix()
		a := filepath.Join(h.repo, "src", "a.go")
		write(t, a, "package src\n\nfunc A() int { return 5 }\n")
		run(t, h.repo, "git", "commit", "-qam", "five")
		write(t, a, "package src\n\nfunc A() int { return 6 }\n") // same size
		if time.Now().Unix() != sec {
			continue // crossed a second boundary; not the racy case
		}
		for time.Now().Unix() == sec { // the fingerprint runs a second later
			time.Sleep(20 * time.Millisecond)
		}
		fp, err := Fingerprint(h.repo, nil)
		must(t, err)
		head := strings.TrimSpace(run(t, h.repo, "git", "rev-parse", "HEAD^{tree}"))
		if fp == head {
			t.Fatal("fingerprint missed a same-size edit made in the same second as the commit")
		}
		if fi, _ := os.Stat(a); fi == nil {
			t.Fatal("missing file")
		}
		return
	}
	t.Skip("couldn't stay inside one second")
}
