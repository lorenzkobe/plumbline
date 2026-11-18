package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// TokenCount is a task's token use, read from Claude Code's transcripts.
// The transcript format is internal to Claude Code and can change between
// versions, so what can't be read is marked Unknown, never guessed.
type TokenCount struct {
	Input      int64 `json:"input,omitempty"`
	Output     int64 `json:"output,omitempty"`
	CacheRead  int64 `json:"cache_read,omitempty"`
	CacheWrite int64 `json:"cache_write,omitempty"`
	Messages   int   `json:"messages,omitempty"`
	Unknown    bool  `json:"unknown,omitempty"` // a transcript couldn't be read, or showed no usage
}

func (c TokenCount) Total() int64 { return c.Input + c.Output + c.CacheRead + c.CacheWrite }

func (c *TokenCount) Add(o TokenCount) {
	c.Input += o.Input
	c.Output += o.Output
	c.CacheRead += o.CacheRead
	c.CacheWrite += o.CacheWrite
	c.Messages += o.Messages
	c.Unknown = c.Unknown || o.Unknown
}

func (c TokenCount) String() string {
	s := fmt.Sprintf("%s tokens (input %s, output %s, cache read %s, cache write %s)", groupDigits(c.Total()), groupDigits(c.Input), groupDigits(c.Output), groupDigits(c.CacheRead), groupDigits(c.CacheWrite))
	if c.Unknown {
		s += ", some unknown"
	}
	return s
}

// groupDigits writes 1234567 as 1,234,567.
func groupDigits(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := n < 0
	if neg {
		s = s[1:]
	}
	var b []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, s[i])
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

type transcriptEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string `json:"id"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// TranscriptTokens sums the usage of the assistant messages written at or
// after since. One API message can show on several lines with the same
// usage, so each message id counts once (its last line wins).
func TranscriptTokens(path string, since time.Time) TokenCount {
	f, err := os.Open(path)
	if err != nil {
		return TokenCount{Unknown: true}
	}
	defer f.Close()
	byID := map[string]TokenCount{}
	var order []string
	unreadable, noUsage := false, 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16<<20)
	n := 0
	for sc.Scan() {
		n++
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"assistant"`)) {
			continue
		}
		var e transcriptEntry
		if json.Unmarshal(line, &e) != nil || e.Type != "assistant" {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Timestamp)
		if err != nil {
			unreadable = true // can't tell whether it's the task's
			continue
		}
		if at.Before(since) {
			continue
		}
		if e.Message.Usage == nil {
			noUsage++
			continue
		}
		id := e.Message.ID
		if id == "" {
			id = e.RequestID
		}
		if id == "" {
			id = "line-" + strconv.Itoa(n)
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
		}
		u := e.Message.Usage
		byID[id] = TokenCount{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite, Messages: 1}
	}
	var total TokenCount
	for _, id := range order {
		total.Add(byID[id])
	}
	// A line the scanner gave up on, a time that doesn't parse, or the
	// task's assistant lines all without usage mean the format moved: say
	// unknown instead of a low count.
	if sc.Err() != nil || unreadable || (noUsage > 0 && len(order) == 0) {
		total.Unknown = true
	}
	return total
}

// recordTokens keeps the token count of one transcript (the session's own,
// or a helper's) on the task, counted from the task's start. It's counted
// again in full each time, so a repeat call never adds twice.
func (t *Task) recordTokens(key, path string) {
	if path == "" {
		return
	}
	if t.Tokens == nil {
		t.Tokens = map[string]TokenCount{}
	}
	t.Tokens[key] = TranscriptTokens(path, t.Created)
}

// TokenTotal: the task's tokens across the session and its helpers.
func (t *Task) TokenTotal() (TokenCount, bool) {
	var total TokenCount
	for _, c := range t.Tokens {
		total.Add(c)
	}
	return total, len(t.Tokens) > 0
}

// LogTokens logs a closed task's token use, when any was counted.
func LogTokens(t *Task, project string) {
	if tc, ok := t.TokenTotal(); ok {
		LogEvent(Event{Type: "tokens", Task: t.ID, Size: t.Size, Project: project, Tokens: &tc})
	}
}
