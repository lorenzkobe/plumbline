package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A release bumps plugin.json and the CHANGELOG together; a top heading left
// at "Unreleased" or an older number ships the wrong version notes.
func TestPluginVersionMatchesChangelog(t *testing.T) {
	b, err := os.ReadFile("../../.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	if p.Version == "" {
		t.Fatal("plugin.json has no version")
	}
	log, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(log), "\n") {
		if strings.HasPrefix(line, "## ") {
			if want := "## " + p.Version + ":"; !strings.HasPrefix(line, want) {
				t.Fatalf("top CHANGELOG heading is %q, want it to start with %q", line, want)
			}
			return
		}
	}
	t.Fatal("CHANGELOG.md has no ## heading")
}
