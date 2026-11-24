package core

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

var (
	WatchQuietRun    = 5 * time.Minute
	WatchQuietHelper = 10 * time.Minute
	// WatchHelperUpdate: how often a running reviewer, guard or arbiter gets a
	// progress line for the user (the user's call, 2026-11-24).
	WatchHelperUpdate = 5 * time.Minute
	WatchEvery        = 15 * time.Second
	WatchGrace        = time.Minute
	watchRunsTail     = int64(512 << 10)
)

type Watch struct {
	root, taskID string
	started      time.Time
	seen         bool
	checked      bool
	runs         map[string]bool
	runsGone     map[string]time.Time
	runQuiet     map[string]time.Time
	runLong      map[string]bool
	helpers      map[string]bool
	helperQuiet  map[string]time.Time
	helperUpdate map[string]time.Time
}

func NewWatch(root, taskID string) *Watch {
	return &Watch{root: root, taskID: taskID, started: Now(), runs: map[string]bool{}, runsGone: map[string]time.Time{},
		runQuiet: map[string]time.Time{}, runLong: map[string]bool{}, helpers: map[string]bool{}, helperQuiet: map[string]time.Time{}, helperUpdate: map[string]time.Time{}}
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func latest(ts ...time.Time) time.Time {
	var last time.Time
	for _, t := range ts {
		if t.After(last) {
			last = t
		}
	}
	return last
}

func modTime(p string) time.Time {
	if p == "" {
		return time.Time{}
	}
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "..."
	}
	return s
}

func (w *Watch) Check() (lines []string, busy bool) {
	now := Now()
	say := func(format string, a ...any) { lines = append(lines, "plumbline watch: "+fmt.Sprintf(format, a...)) }
	var suite time.Duration
	setup, _, err := LoadSetup(w.root)
	if err == nil && setup != nil {
		suite = time.Duration(setup.SuiteSeconds) * time.Second
	} else {
		setup = nil
	}
	for _, r := range loadRunsTail(w.root, watchRunsTail) {
		if !r.Finished.IsZero() {
			if w.runs[r.ID] {
				delete(w.runs, r.ID)
				if r.TimedOut {
					say("%s was stopped at its time limit: something likely hangs (%s)", r.Kind, r.Command)
				} else if r.Stopped {
					say("%s was stopped before it finished (%s)", r.Kind, r.Command)
				} else {
					say("%s finished: exit %d, %d ran, %d failed (%s)", r.Kind, r.Exit, r.Ran, r.Failed, r.Command)
				}
			}
			continue
		}
		live := liveOutputPath(w.root, r.ID)
		_, liveErr := os.Stat(live)
		if !processAlive(r.PID) && liveErr != nil || now.Sub(r.Started) > RunLimit(r.Kind, setup)+time.Minute {
			if w.runs[r.ID] {
				if at, ok := w.runsGone[r.ID]; ok && now.Sub(at) >= time.Minute {
					delete(w.runs, r.ID)
					say("%s ended without a recorded result: its process is gone (%s)", r.Kind, r.Command)
				} else {
					if !ok {
						w.runsGone[r.ID] = now
					}
					busy = true
				}
			}
			continue
		}
		busy = true
		w.runs[r.ID] = true
		quiet := now.Sub(latest(r.Started, modTime(live)))
		if said, ok := w.runQuiet[r.ID]; quiet >= WatchQuietRun && (!ok || now.Sub(said) >= WatchQuietRun) {
			w.runQuiet[r.ID] = now
			say("%s has had no output for %d min, it may be hung (%s)", r.Kind, int(quiet/time.Minute), r.Command)
		}
		took := now.Sub(r.Started)
		if suite > 0 && (r.Kind == KindTest || r.Kind == KindRelated) && !w.runLong[r.ID] && 2*took >= 3*suite {
			w.runLong[r.ID] = true
			say("%s is taking longer than usual: %s, usually %s (%s)", r.Kind, RoundMinutes(took), RoundMinutes(suite), r.Command)
		}
	}
	if w.taskID != "" {
		if t, err := LoadTask(w.root, w.taskID); err == nil {
			for i := range t.Helpers {
				r := &t.Helpers[i]
				key := fmt.Sprintf("%d-%s", i, r.AgentID)
				name := fmt.Sprintf("%s %d", r.Kind, t.helperNumber(r))
				if !r.Finished.IsZero() || !r.alive() {
					if w.helpers[key] {
						delete(w.helpers, key)
						switch {
						case r.Status == BuilderStoppedByPlumbline:
							say("%s was stopped by Plumbline, so it is finished: read its notes, then merge, drop or ask the user. Why: %s", name, shorten(r.Error, 240))
						case r.Stuck:
							say("%s was set aside as stuck: a fresh run took its place", name)
						case r.stuck():
							say("%s has shown no sign of life for %d min, so Plumbline counts it as stuck: it doesn't use one of its runs. Start a fresh one now (Agent tool, the same subagent_type) and tell the user in one line", name, int(now.Sub(latest(r.Started, r.LastSeen, modTime(r.Transcript))).Minutes()))
						case r.Finished.IsZero():
							say("%s stopped without handing back (no stop was recorded)", name)
						case r.Status != "":
							say("%s finished (%s)", name, r.Status)
						default:
							say("%s finished", name)
						}
					}
					continue
				}
				busy = true
				w.helpers[key] = true
				quiet := now.Sub(latest(r.Started, r.LastSeen, modTime(r.Transcript)))
				if ran := now.Sub(r.Started); checked(r.Kind) && ran >= WatchHelperUpdate && now.Sub(w.helperUpdate[key]) >= WatchHelperUpdate {
					w.helperUpdate[key] = now
					say("%s is still working: %d min so far, last sign of life %d min ago; tell the user in one line", name, int(ran.Minutes()), int(quiet.Minutes()))
				}
				if said, ok := w.helperQuiet[key]; quiet >= WatchQuietHelper && (!ok || now.Sub(said) >= WatchQuietHelper) {
					w.helperQuiet[key] = now
					say("%s has been quiet for %d min: no tool call and nothing written", name, int(quiet/time.Minute))
				}
			}
		}
	}
	w.seen = w.seen || busy || len(lines) > 0
	if !w.seen && now.Sub(w.started) < WatchGrace {
		return nil, true
	}
	if !w.checked && !busy && len(lines) == 0 {
		say("nothing is running")
	}
	w.checked = true
	return lines, busy
}
