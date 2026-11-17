package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Failure reports: when Plumbline itself breaks (a crash, unreadable hook
// input, or a binary the launcher can't start), it leaves a record, and the
// next session start tells the user. Reports never hold the tool's input, so
// no code, file contents or secrets end up in them.

func crashDir() string     { return filepath.Join(DataDir(), "crashes") }
func errorsLog() string    { return filepath.Join(DataDir(), "errors.log") }
func failuresSeen() string { return filepath.Join(DataDir(), "failures.seen") }

// RecordFailure writes a crash report and logs a "failure" event. what is
// the panic value or error; it never includes the hook's input.
func RecordFailure(version, event, tool, what string, stack []byte) string {
	at := Now()
	name := fmt.Sprintf("%s-%s.txt", at.Format("20060102T150405.000000000"), safeName(event))
	var b strings.Builder
	fmt.Fprintf(&b, "Plumbline %s failed in the %q hook at %s\n", version, event, at.Format(time.RFC3339))
	if tool != "" {
		fmt.Fprintf(&b, "Tool: %s\n", tool)
	}
	fmt.Fprintf(&b, "What: %s\n", limit(what, 500))
	if len(stack) > 0 {
		fmt.Fprintf(&b, "\nWhere:\n%s\n", stack)
	}
	p := filepath.Join(crashDir(), name)
	_ = writeFileAtomic(p, []byte(b.String()))
	LogEvent(Event{Type: "failure", Rule: event, Detail: limit(what, 160)})
	return p
}

// FailureNote counts failures since the last session start and marks them
// seen. Launcher failures (written by bin/plumbline, which can't log events)
// are logged as events here, so /plumbline:stats counts them too. It returns
// "" when there were none.
func FailureNote() string {
	// failures.seen: when the last note was made (for crash reports) and how
	// much of errors.log was read then (its lines only have whole seconds).
	var seen struct {
		At     time.Time `json:"at"`
		Offset int64     `json:"log_offset"`
	}
	if b, err := os.ReadFile(failuresSeen()); err == nil {
		_ = json.Unmarshal(b, &seen)
	}
	since := seen.At
	now := Now()
	var crashes []string
	entries, _ := os.ReadDir(crashDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue // a report still being written
		}
		if fi, err := e.Info(); err == nil && !e.IsDir() && fi.ModTime().After(since) {
			crashes = append(crashes, filepath.Join(crashDir(), e.Name()))
		}
	}
	sort.Strings(crashes)
	launcher := 0
	if f, err := os.Open(errorsLog()); err == nil {
		if fi, err := f.Stat(); err == nil && fi.Size() < seen.Offset {
			seen.Offset = 0 // the log was cleared: read it from the start
		}
		if _, err := f.Seek(seen.Offset, io.SeekStart); err == nil {
			r := bufio.NewReader(f)
			for {
				line, err := r.ReadString('\n')
				if err != nil {
					break // a line still being written is read next time
				}
				seen.Offset += int64(len(line))
				stamp, rest, _ := strings.Cut(strings.TrimSpace(line), " ")
				if _, perr := time.Parse(time.RFC3339, stamp); perr != nil {
					continue
				}
				launcher++
				LogEvent(Event{Type: "failure", Rule: "launcher", Detail: limit(rest, 160)})
			}
		}
		f.Close()
	}
	seen.At = now
	b, _ := json.Marshal(seen)
	_ = writeFileAtomic(failuresSeen(), b)
	n := len(crashes) + launcher
	if n == 0 {
		return ""
	}
	var where []string
	if len(crashes) > 0 {
		where = append(where, "latest crash report: "+crashes[len(crashes)-1])
	}
	if launcher > 0 {
		where = append(where, fmt.Sprintf("%d time(s) the binary couldn't start, see %s", launcher, errorsLog()))
	}
	return fmt.Sprintf("Plumbline itself failed %d time(s) since the last session start (%s). While it fails, edits, commands and reads are blocked and only searches go through.", n, strings.Join(where, "; "))
}
