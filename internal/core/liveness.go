package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	helperStuck       = 3 * time.Hour
	interruptSettle   = time.Minute
	transcriptTailMax = 1 << 20
)

type transcriptState int

const (
	transcriptUnknown transcriptState = iota
	transcriptWorking
	transcriptAnswered
	transcriptInterrupted
)

func helperTranscriptPath(transcript, agentID string) string {
	if transcript == "" || agentID == "" || strings.ContainsAny(agentID, `/\`) {
		return ""
	}
	if strings.HasPrefix(filepath.Base(transcript), "agent-") {
		return transcript
	}
	return filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

func readTranscriptState(path string) (time.Time, transcriptState) {
	if path == "" {
		return time.Time{}, transcriptUnknown
	}
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, transcriptUnknown
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return time.Time{}, transcriptUnknown
	}
	off := fi.Size() - transcriptTailMax
	if off < 0 {
		off = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, off, fi.Size()-off))
	if err != nil {
		return fi.ModTime(), transcriptUnknown
	}
	lines := strings.Split(string(b), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if s := entryState(lines[i]); s != transcriptUnknown {
			return fi.ModTime(), s
		}
	}
	return fi.ModTime(), transcriptUnknown
}

func entryState(line string) transcriptState {
	line = strings.TrimSpace(line)
	if line == "" {
		return transcriptUnknown
	}
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &e) != nil {
		return transcriptUnknown
	}
	switch e.Type {
	case "api-request":
		return transcriptWorking
	case "user", "assistant":
	default:
		return transcriptUnknown
	}
	var text string
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(e.Message.Content, &text) != nil {
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return transcriptUnknown
		}
	}
	if strings.HasPrefix(strings.TrimSpace(text), "[Request interrupted by user") {
		return transcriptInterrupted
	}
	onlyText := e.Type == "assistant" && len(blocks) > 0
	for _, bl := range blocks {
		if bl.Type == "text" && strings.HasPrefix(strings.TrimSpace(bl.Text), "[Request interrupted by user") {
			return transcriptInterrupted
		}
		if bl.Type != "text" {
			onlyText = false
		}
	}
	if onlyText {
		return transcriptAnswered
	}
	return transcriptWorking
}

func sessionGoneAfter(sessionID string, t time.Time) bool {
	if sessionID == "" {
		return false
	}
	for _, p := range []string{sessionEndedPath(sessionID), sessionStartedPath(sessionID)} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(b)))
		if err == nil && !at.Before(t.Truncate(time.Second)) {
			return true
		}
	}
	return false
}

func (r HelperRun) alive() bool {
	if !r.Finished.IsZero() || sessionGoneAfter(r.Session, r.Started) {
		return false
	}
	last := r.Started
	if r.LastSeen.After(last) {
		last = r.LastSeen
	}
	wrote, state := readTranscriptState(r.Transcript)
	if wrote.After(last) {
		last = wrote
	}
	quiet := Now().Sub(last)
	switch {
	case state == transcriptInterrupted && quiet >= interruptSettle:
		return false
	case quiet < helperAlive:
		return true
	case state == transcriptWorking:
		return quiet < helperStuck
	}
	return false
}
