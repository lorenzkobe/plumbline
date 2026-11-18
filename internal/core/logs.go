package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Event is one line of a task log. Logs hold rule names, counts and times
// only: no code, no secrets, no ticket text.
type Event struct {
	At      time.Time `json:"at"`
	Task    string    `json:"task,omitempty"`
	Project string    `json:"project,omitempty"`
	Type    string    `json:"type"`           // block, ask, sendback, phase, run, waiver, wrong, pause, resume, task
	Rule    string    `json:"rule,omitempty"` // which rule
	Detail  string    `json:"detail,omitempty"`
	Size    string    `json:"size,omitempty"`
	Wrong   bool      `json:"wrong,omitempty"`
	// Tokens: a finished or cancelled task's token use (counts only).
	Tokens *TokenCount `json:"tokens,omitempty"`
}

func logsDir() string { return filepath.Join(DataDir(), "logs") }

var reQuoted = regexp.MustCompile(`"[^"\n]*"`)

// scrubDetail keeps a log line free of what the user wrote and of paths that
// name them: quoted and backticked text (commit subjects, plan and case
// text, commands) becomes "..." and paths are shown from the repo or ~.
func scrubDetail(s, root string) string {
	s = reQuoted.ReplaceAllString(s, `"..."`)
	s = reBacktick.ReplaceAllString(s, "`...`")
	if root != "" {
		s = strings.ReplaceAll(s, root+string(filepath.Separator), "")
		s = strings.ReplaceAll(s, root, ".")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

func LogEvent(e Event) {
	if e.At.IsZero() {
		e.At = Now()
	}
	e.Detail = scrubDetail(e.Detail, "")
	dir := filepath.Join(logsDir(), e.At.Format("2006-01"))
	_ = os.MkdirAll(dir, 0o700)
	name := e.Task
	if name == "" {
		name = "no-task"
	}
	f, err := os.OpenFile(filepath.Join(dir, safeName(name)+".jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(e)
	_, _ = f.Write(append(b, '\n'))
	if e.Type == "block" || e.Type == "sendback" {
		_ = writeFileAtomic(filepath.Join(DataDir(), "last-block.json"), b)
	}
}

// MarkLastBlockWrong is reached only from /plumbline:wrong, typed by you.
func MarkLastBlockWrong(note string) (Event, error) {
	b, err := os.ReadFile(filepath.Join(DataDir(), "last-block.json"))
	if err != nil {
		return Event{}, fmt.Errorf("no block recorded yet")
	}
	var e Event
	if err := json.Unmarshal(b, &e); err != nil {
		return Event{}, err
	}
	LogEvent(Event{Type: "wrong", Task: e.Task, Rule: e.Rule, Detail: limit(note, 200), Wrong: true})
	return e, nil
}

func limit(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func readEvents(path string) []Event {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 256*1024), 1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// Summary is a month rolled into numbers only.
type Summary struct {
	Month     string         `json:"month"`
	Tasks     int            `json:"tasks"`
	BySize    map[string]int `json:"by_size"`
	Blocks    map[string]int `json:"blocks"`
	Wrong     map[string]int `json:"wrong"`
	Sendbacks map[string]int `json:"sendbacks"`
	Waivers   int            `json:"waivers"`
	Failures  int            `json:"failures"` // Plumbline itself broke (crash, bad input, binary that won't start)
	Asks      map[string]int `json:"asks"`
	// Token use of the tasks that logged it, and how many of those had
	// transcripts that couldn't all be read.
	Tokens        TokenCount `json:"tokens"`
	TokenTasks    int        `json:"token_tasks,omitempty"`
	TokensUnknown int        `json:"tokens_unknown,omitempty"`
}

func summarize(month string, events []Event) Summary {
	s := Summary{Month: month, BySize: map[string]int{}, Blocks: map[string]int{}, Wrong: map[string]int{}, Sendbacks: map[string]int{}, Asks: map[string]int{}}
	tasks := map[string]string{}
	for _, e := range events {
		if e.Task != "" && e.Type == "task" {
			tasks[e.Task] = e.Size
		}
		switch e.Type {
		case "block":
			s.Blocks[e.Rule]++
		case "ask":
			s.Asks[e.Rule]++
		case "sendback":
			s.Sendbacks[e.Rule]++
		case "wrong":
			s.Wrong[e.Rule]++
		case "waiver":
			s.Waivers++
		case "failure":
			s.Failures++
		case "tokens":
			if e.Tokens != nil {
				s.Tokens.Add(*e.Tokens)
				s.TokenTasks++
				if e.Tokens.Unknown {
					s.TokensUnknown++
				}
			}
		}
	}
	s.Tasks = len(tasks)
	for _, sz := range tasks {
		s.BySize[sz]++
	}
	return s
}

func monthEvents(dir string) []Event {
	files, _ := os.ReadDir(dir)
	var all []Event
	for _, f := range files {
		if strings.HasSuffix(f.Name(), ".jsonl") {
			all = append(all, readEvents(filepath.Join(dir, f.Name()))...)
		}
	}
	return all
}

// Cleanup keeps detailed logs 6 months and monthly number summaries 1 year.
// It runs at most once a day, at session start. Open tasks are never touched.
func Cleanup() {
	marker := filepath.Join(DataDir(), ".cleanup")
	if b, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(b)) == Now().Format("2006-01-02") {
		return
	}
	_ = writeFileAtomic(marker, []byte(Now().Format("2006-01-02")+"\n"))
	now := Now()
	detailCut := now.AddDate(0, -6, 0).Format("2006-01")
	summaryCut := now.AddDate(-1, 0, 0).Format("2006-01")
	months, _ := os.ReadDir(logsDir())
	for _, m := range months {
		if !m.IsDir() || m.Name() >= detailCut {
			continue
		}
		dir := filepath.Join(logsDir(), m.Name())
		s := summarize(m.Name(), monthEvents(dir))
		b, _ := json.MarshalIndent(s, "", "  ")
		if writeFileAtomic(filepath.Join(DataDir(), "summaries", m.Name()+".json"), b) == nil {
			_ = os.RemoveAll(dir)
		}
	}
	sums, _ := os.ReadDir(filepath.Join(DataDir(), "summaries"))
	for _, s := range sums {
		if strings.TrimSuffix(s.Name(), ".json") < summaryCut {
			_ = os.Remove(filepath.Join(DataDir(), "summaries", s.Name()))
		}
	}
	// Crash reports older than 6 months go too (the logs keep the count).
	crashes, _ := os.ReadDir(crashDir())
	for _, e := range crashes {
		if fi, err := e.Info(); err == nil && fi.ModTime().Before(now.AddDate(0, -6, 0)) {
			_ = os.Remove(filepath.Join(crashDir(), e.Name()))
		}
	}
	// Finished task folders older than 6 months go too (logs keep the numbers).
	projects, _ := os.ReadDir(filepath.Join(DataDir(), "projects"))
	for _, p := range projects {
		tdir := filepath.Join(DataDir(), "projects", p.Name(), "tasks")
		tasks, _ := os.ReadDir(tdir)
		for _, td := range tasks {
			b, err := os.ReadFile(filepath.Join(tdir, td.Name(), "state.json"))
			if err != nil {
				continue
			}
			var t Task
			if json.Unmarshal(b, &t) != nil || t.Open() {
				continue
			}
			if t.Updated.Before(now.AddDate(0, -6, 0)) {
				_ = os.RemoveAll(filepath.Join(tdir, td.Name()))
			}
		}
	}
}

// Stats prints a plain summary of the recent logs and summaries.
func Stats(months int) string {
	if months <= 0 {
		months = 3
	}
	cut := Now().AddDate(0, -months+1, 0).Format("2006-01")
	var all []Event
	dirs, _ := os.ReadDir(logsDir())
	for _, d := range dirs {
		if d.IsDir() && d.Name() >= cut {
			all = append(all, monthEvents(filepath.Join(logsDir(), d.Name()))...)
		}
	}
	s := summarize("recent", all)
	var b strings.Builder
	fmt.Fprintf(&b, "Plumbline stats, last %d month(s)\n", months)
	fmt.Fprintf(&b, "Tasks: %d", s.Tasks)
	if len(s.BySize) > 0 {
		fmt.Fprintf(&b, " (%s)", kv(s.BySize))
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Blocks by rule: %s\n", orNone(kv(s.Blocks)))
	fmt.Fprintf(&b, "Marked wrong by you: %s\n", orNone(kv(s.Wrong)))
	fmt.Fprintf(&b, "Send-backs at finish: %s\n", orNone(kv(s.Sendbacks)))
	fmt.Fprintf(&b, "Confirm prompts by rule: %s\n", orNone(kv(s.Asks)))
	fmt.Fprintf(&b, "Waivers: %d\n", s.Waivers)
	fmt.Fprintf(&b, "Plumbline failures: %d\n", s.Failures)
	if s.TokenTasks > 0 {
		avg := s.Tokens.Total() / int64(s.TokenTasks)
		fmt.Fprintf(&b, "Tokens: %s across %d task(s), about %s a task", s.Tokens, s.TokenTasks, groupDigits(avg))
		if s.TokensUnknown > 0 {
			fmt.Fprintf(&b, "; %d task(s) had transcripts that couldn't all be read", s.TokensUnknown)
		}
		b.WriteString("\n")
	}
	var noisy []string
	for r, w := range s.Wrong {
		if bl := s.Blocks[r]; bl > 0 && w*2 >= bl {
			noisy = append(noisy, r)
		}
	}
	sort.Strings(noisy)
	if len(noisy) > 0 {
		fmt.Fprintf(&b, "Rules to look at (half or more of their blocks marked wrong): %s\n", strings.Join(noisy, ", "))
	}
	return b.String()
}

func kv(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || (m[keys[i]] == m[keys[j]] && keys[i] < keys[j]) })
	parts := []string{}
	for _, k := range keys {
		name := k
		if name == "" {
			name = "unknown"
		}
		parts = append(parts, fmt.Sprintf("%s %d", name, m[k]))
	}
	return strings.Join(parts, ", ")
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
