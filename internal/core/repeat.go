package core

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Repeated blocks: the same refusal twice in a row means trying again won't
// help, so Claude stops and asks instead of looping. "In a row" is per
// session (and per helper): a different refusal in between starts over, and
// a new message from the user clears it.

const repeatNote = " This is the second time in a row Plumbline refused exactly this. Don't try it again or work around it: stop, tell the user what you were doing and why it's refused, and ask how to go on (end your message with a question)."

const repeatNoteHelper = " This is the second time in a row Plumbline refused exactly this. Don't try it again or work around it: put it in your findings as an open question and finish."

func lastBlockPath(sessionID, agentID string) string {
	name := safeName(sessionID)
	if agentID != "" {
		name += "-" + safeName(agentID)
	}
	return filepath.Join(DataDir(), "sessions", name+".lastblock")
}

// noteBlock records a refusal and reports whether the one before it, in the
// same session and helper, was the same: the same rule and reason for the
// same target (file, command or tool).
func noteBlock(sessionID, agentID, rule, reason, target string) bool {
	if sessionID == "" {
		return false
	}
	sum := sha256.Sum256([]byte(rule + "\x00" + reason + "\x00" + target))
	key := hex.EncodeToString(sum[:])
	p := lastBlockPath(sessionID, agentID)
	prev, _ := os.ReadFile(p)
	_ = writeFileAtomic(p, []byte(key))
	return strings.TrimSpace(string(prev)) == key
}

// blockTarget: what a refused tool call was aimed at.
func blockTarget(in *HookInput) string {
	for _, k := range []string{"file_path", "notebook_path", "command", "path", "pattern", "subagent_type"} {
		if v := in.Str(k); v != "" {
			return in.ToolName + "\x00" + v
		}
	}
	return in.ToolName + "\x00" + string(in.ToolInput)
}

// ResetBlocks forgets the session's last refusals (the lead's and its
// helpers'), when the user sends a new message.
func ResetBlocks(sessionID string) {
	if sessionID == "" {
		return
	}
	dir := filepath.Join(DataDir(), "sessions")
	base := safeName(sessionID)
	matches, _ := filepath.Glob(filepath.Join(dir, base+"*.lastblock"))
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".lastblock")
		if name == base || strings.HasPrefix(name, base+"-") {
			_ = os.Remove(m)
		}
	}
}
