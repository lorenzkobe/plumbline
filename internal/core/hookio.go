package core

import (
	"encoding/json"
	"io"
	"os"
	"strings"
)

// HookInput holds the fields Plumbline reads from Claude Code's hook input.
type HookInput struct {
	SessionID       string          `json:"session_id"`
	TranscriptPath  string          `json:"transcript_path"`
	Cwd             string          `json:"cwd"`
	ScratchpadDir   string          `json:"scratchpad_dir"`
	PermissionMode  string          `json:"permission_mode"`
	Event           string          `json:"hook_event_name"`
	ToolName        string          `json:"tool_name"`
	ToolInput       json.RawMessage `json:"tool_input"`
	ToolResponse    json.RawMessage `json:"tool_response"`
	ToolUseID       string          `json:"tool_use_id"`
	AgentID         string          `json:"agent_id"`
	AgentType       string          `json:"agent_type"`
	AgentTranscript string          `json:"agent_transcript_path"`
	Source          string          `json:"source"`
	Prompt          string          `json:"prompt"`
	StopHookActive  bool            `json:"stop_hook_active"`
	LastMessage     string          `json:"last_assistant_message"`
	ExpansionType   string          `json:"expansion_type"`
	CommandName     string          `json:"command_name"`
	CommandArgs     string          `json:"command_args"`
	CommandSource   string          `json:"command_source"`
	Error           string          `json:"error"` // PostToolUseFailure
	Raw             []byte          `json:"-"`
}

func (in *HookInput) Input() map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(in.ToolInput, &m)
	return m
}

func (in *HookInput) Str(key string) string {
	v, _ := in.Input()[key].(string)
	return v
}

func ReadHookInput(r io.Reader) (*HookInput, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var in HookInput
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	in.Raw = b
	if in.Cwd == "" {
		in.Cwd = os.Getenv("CLAUDE_PROJECT_DIR")
	}
	if in.Cwd == "" {
		in.Cwd, _ = os.Getwd()
	}
	return &in, nil
}

// Output is what a hook prints. Empty output means "no objection".
type Output map[string]any

func Decision(event, decision, reason string) Output {
	return Output{"hookSpecificOutput": map[string]any{
		"hookEventName":            event,
		"permissionDecision":       decision,
		"permissionDecisionReason": reason,
	}}
}

func Context(event, text string) Output {
	return Output{"hookSpecificOutput": map[string]any{
		"hookEventName":     event,
		"additionalContext": text,
	}}
}

func StopAll(reason string) Output {
	return Output{"continue": false, "stopReason": reason}
}

func (o Output) With(key string, v any) Output {
	if o == nil {
		o = Output{}
	}
	o[key] = v
	return o
}

func (o Output) Write(w io.Writer) {
	if len(o) == 0 {
		return
	}
	b, _ := json.Marshal(o)
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n"))
}

// Plain-words prefix on every message Plumbline shows.
func msg(parts ...string) string {
	return "Plumbline: " + strings.Join(parts, " ")
}
