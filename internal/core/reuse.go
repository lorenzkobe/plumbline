package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const reuseWindow = 6 * time.Hour

var reusableKinds = map[string]bool{KindTest: true, KindLint: true, KindBuild: true, KindFormat: true, KindTypecheck: true}

func envKey(root string) string {
	es, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	h := sha256.New()
	for _, e := range es {
		if e.IsDir() || !strings.HasPrefix(e.Name(), ".env") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s\x00%d\x00", e.Name(), len(b))
		h.Write(b)
	}
	return "v1|" + hex.EncodeToString(h.Sum(nil))
}

func ignoreKey(ignore []string) string {
	g := append([]string{}, ignore...)
	sort.Strings(g)
	return "v1|" + strings.Join(g, "\x00")
}

func ReuseBaseline(root string, t *Task, kind, command string, setup *Setup) (*Run, error) {
	if t == nil || !reusableKinds[kind] || t.Hotfix() || !t.LastCodeChange.IsZero() || strings.TrimSpace(command) == "" {
		return nil, nil
	}
	var ignore []string
	if setup != nil {
		ignore = setup.IgnoreChanges
	}
	fp, err := Fingerprint(root, ignore)
	if err != nil || fp == "" {
		return nil, nil
	}
	baseline := t.Phase != PhaseBuilding || t.ApprovalPrint != "" && fp == t.ApprovalPrint
	rebased := t.BaselinePrint != "" && fp == t.BaselinePrint
	if !baseline && !rebased {
		return nil, nil
	}
	key, env := ignoreKey(ignore), envKey(root)
	if env == "" {
		return nil, nil
	}
	now := Now()
	var src *Run
	for _, r := range LoadRuns(root) {
		if r.Kind != kind || r.Command != command || r.Root == "" || r.Root != root || r.IgnoreKey != key || r.EnvKey != env {
			continue
		}
		if r.PrintBefore != fp || r.PrintAfter != fp || !r.Complete() || r.TimedOut || r.Stopped || r.Rescued {
			continue
		}
		if r.Finished.After(now) || now.Sub(r.Finished) > reuseWindow || r.Started.After(r.Finished) {
			continue
		}
		if r.Exit != 0 || r.Failed != 0 || testKind(kind) && r.Ran <= 0 {
			continue
		}
		if src == nil || r.Finished.After(src.Finished) {
			src = r
		}
	}
	if src == nil {
		return nil, nil
	}
	reused := src.ID
	if src.Reused != "" {
		reused = src.Reused
	}
	cp := &Run{ID: newID() + "-" + kind, TaskID: t.ID, Kind: kind, Command: command, Started: src.Started, Finished: src.Finished,
		Exit: src.Exit, PrintBefore: fp, PrintAfter: fp, Ran: src.Ran, Failed: src.Failed, Skipped: src.Skipped, Problems: src.Problems,
		Baseline: true, Rebased: rebased, Root: root, IgnoreKey: key, EnvKey: env, Reused: reused}
	if err := appendRun(root, cp); err != nil {
		return nil, err
	}
	return cp, nil
}
