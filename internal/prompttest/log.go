package prompttest

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

type Call struct {
	ID     string
	Name   string
	Input  map[string]any
	Result string
	Error  bool
	Done   bool
	Helper bool
}

type Prompt struct {
	Tool    string
	Input   map[string]any
	Allowed bool
	Answers map[string]any
}

type Log struct {
	Calls   []*Call
	Prompts []Prompt
	Cost    float64
	Turns   int
	Outcome string
	Final   string
}

type block struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type event struct {
	Type    string  `json:"type"`
	Subtype string  `json:"subtype"`
	Parent  *string `json:"parent_tool_use_id"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Tool    string         `json:"tool"`
	Input   map[string]any `json:"input"`
	Allowed bool           `json:"allowed"`
	Answers map[string]any `json:"answers"`
	Cost    float64        `json:"total_cost_usd"`
	Turns   int            `json:"num_turns"`
	Result  string         `json:"result"`
}

func Parse(r io.Reader) (*Log, error) {
	l := &Log{}
	byID := map[string]*Call{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		switch e.Type {
		case "assistant", "user":
			var blocks []block
			if json.Unmarshal(e.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "tool_use":
					c := &Call{ID: b.ID, Name: b.Name, Input: b.Input, Helper: e.Parent != nil && *e.Parent != ""}
					l.Calls = append(l.Calls, c)
					byID[b.ID] = c
				case "tool_result":
					if c := byID[b.ToolUseID]; c != nil {
						c.Result, c.Error, c.Done = resultText(b.Content), b.IsError, true
					}
				}
			}
		case "permission":
			l.Prompts = append(l.Prompts, Prompt{Tool: e.Tool, Input: e.Input, Allowed: e.Allowed, Answers: e.Answers})
		case "result":
			l.Cost, l.Turns, l.Outcome, l.Final = e.Cost, e.Turns, e.Subtype, e.Result
		}
	}
	return l, sc.Err()
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, p := range parts {
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	return string(raw)
}

func (c *Call) Str(k string) string {
	s, _ := c.Input[k].(string)
	return s
}

func (c *Call) Refused() bool {
	return c.Error && strings.Contains(c.Result, "Plumbline")
}
