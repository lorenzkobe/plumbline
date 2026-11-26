package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Repeated blocks: the same refusal twice in a row means trying again won't
// help, so Claude stops and asks instead of looping. "In a row" is per
// session (and per helper): a different refusal in between starts over, and
// a new message from the user clears it. Identical refusals within
// sameMessageWindow are one message's parallel calls: Claude hadn't seen the
// first yet, so they count once.

const repeatNote = " This is the second time in a row Plumbline refused exactly this. Don't try it again or work around it: stop, tell the user what you were doing and why it's refused, and ask how to go on (end your message with a question)."

const repeatNoteHelper = " This is the second time in a row Plumbline refused exactly this. Don't try it again or work around it: put it in your findings as an open question and finish."

func lastBlockPath(sessionID, agentID string) string {
	name := safeName(sessionID)
	if agentID != "" {
		name += "-" + safeName(agentID)
	}
	return filepath.Join(DataDir(), "sessions", name+".lastblock")
}

const sameMessageWindow = 2 * time.Second

// blockNow is the clock for repeat checks; tests move it.
var blockNow = time.Now

// noteBlock records a refusal and reports whether the one before it, in the
// same session and helper, was the same (the same rule and reason for the
// same target: file, command or tool) and came in an earlier message.
func noteBlock(sessionID, agentID, rule, reason, target string) bool {
	if sessionID == "" {
		return false
	}
	sum := sha256.Sum256([]byte(rule + "\x00" + reason + "\x00" + target))
	key := hex.EncodeToString(sum[:])
	p := lastBlockPath(sessionID, agentID)
	prev, _ := os.ReadFile(p)
	now := blockNow()
	_ = writeFileAtomic(p, []byte(key+" "+strconv.FormatInt(now.UnixNano(), 10)))
	prevKey, prevAt, timed := strings.Cut(strings.TrimSpace(string(prev)), " ")
	if prevKey != key {
		return false
	}
	if !timed { // written before the time was kept
		return true
	}
	n, err := strconv.ParseInt(prevAt, 10, 64)
	return err != nil || now.Sub(time.Unix(0, n)) >= sameMessageWindow
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

// A helper that rewords a refused command keeps meeting the same rule with
// a new target, which noteBlock doesn't see as a repeat. From the third
// refusal under one rule in a helper's run, it is told to go on without it
// (0.8.8). Refusals within sameMessageWindow of the last one counted under
// that rule are one message's parallel calls and count once. Two hooks
// writing the file at once can lose a count, which only delays the note.
const ruleNoteAt = 3

const ruleNoteHelper = " Plumbline has refused %d of your commands under this rule; rewording won't get through. Go on without it, and note it in your findings if it matters."

type ruleCount struct {
	N  int   `json:"n"`
	At int64 `json:"at"`
}

func ruleCountPath(sessionID, agentID string) string {
	return filepath.Join(DataDir(), "sessions", safeName(sessionID)+"-"+safeName(agentID)+".rulecount")
}

// noteRule counts a helper's refusal under rule and returns the note to add,
// or "".
func noteRule(sessionID, agentID, rule string) string {
	if sessionID == "" || agentID == "" {
		return ""
	}
	p := ruleCountPath(sessionID, agentID)
	counts := map[string]ruleCount{}
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &counts)
	}
	now := blockNow()
	c := counts[rule]
	if c.At == 0 || now.Sub(time.Unix(0, c.At)) >= sameMessageWindow {
		c.N++
		c.At = now.UnixNano()
	}
	counts[rule] = c
	if b, err := json.Marshal(counts); err == nil {
		_ = writeFileAtomic(p, b)
	}
	if c.N >= ruleNoteAt {
		return fmt.Sprintf(ruleNoteHelper, c.N)
	}
	return ""
}

// The lead often starts several reviewers at once; each start is refused on
// its own for the same missing self-review. After the first, the refusal is
// one line (0.8.8).
const shortRepeatWindow = time.Minute

const shortRepeatReason = "refused for the same reason as the reviewer start just before: write self-review.md first."

// shortRepeat returns reason, or the one-line text when the self-review is
// missing and that was said in this session within shortRepeatWindow. Other
// self-review problems (a link, a stale file, Reach) keep their full text.
func shortRepeat(sessionID, reason string) string {
	if sessionID == "" || !strings.HasPrefix(reason, selfReviewMissing) {
		return reason
	}
	sum := sha256.Sum256([]byte(reason))
	key := hex.EncodeToString(sum[:])
	p := filepath.Join(DataDir(), "sessions", safeName(sessionID)+".selfreview")
	prev, _ := os.ReadFile(p)
	now := blockNow()
	_ = writeFileAtomic(p, []byte(key+" "+strconv.FormatInt(now.UnixNano(), 10)))
	prevKey, prevAt, _ := strings.Cut(strings.TrimSpace(string(prev)), " ")
	if n, err := strconv.ParseInt(prevAt, 10, 64); err == nil && prevKey == key && now.Sub(time.Unix(0, n)) < shortRepeatWindow {
		return shortRepeatReason
	}
	return reason
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
	counts, _ := filepath.Glob(filepath.Join(dir, base+"*.rulecount"))
	for _, m := range append(matches, counts...) {
		name := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(m), ".lastblock"), ".rulecount")
		if name == base || strings.HasPrefix(name, base+"-") {
			_ = os.Remove(m)
		}
	}
}
