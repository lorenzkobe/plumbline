package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Seen: lines of a file the lead saw in search results (grep -n or rg -n
// over folders, the Grep tool, SocratiCode chunks), with the file as it was
// then (RD2). A file:line the plan only mentions may point to one of these
// lines; a file the plan changes must be opened, so Claude sees the code
// around what it edits.
type Seen struct {
	Hash  string    `json:"hash"`
	Lines []int     `json:"lines"`
	At    time.Time `json:"at"`
}

// maxSeenLines keeps a file's list small; a search that shows more lines
// than this was a big dump, and the first ones are enough to cite.
const maxSeenLines = 200

// "src/a.ts:44: const x" or "src/a.ts:44-  context" (grep -n, rg -n, Grep).
var reHitLine = regexp.MustCompile(`(?m)^(?:\./)?([^\s:]+\.[A-Za-z0-9]+):(\d+)[:-]`)

// SocratiCode: "--- src/a.ts (lines 11-28) [typescript] score: 0.5 ---".
var reChunk = regexp.MustCompile(`(?m)^--- (\S+) \(lines (\d+)-(\d+)\)`)

func (t *Task) noteSeen(abs, h string, lines []int) {
	if t.Seen == nil {
		t.Seen = map[string]Seen{}
	}
	s := t.Seen[abs]
	if s.Hash != h {
		s = Seen{Hash: h} // the file changed: its old lines say nothing now
	}
	for _, n := range lines {
		if len(s.Lines) >= maxSeenLines {
			break
		}
		if !containsInt(s.Lines, n) {
			s.Lines = append(s.Lines, n)
		}
	}
	s.At = Now()
	t.Seen[abs] = s
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// near: a cited line within 3 lines of one the search showed.
func (s Seen) near(line int) bool {
	for _, n := range s.Lines {
		if line >= n-3 && line <= n+3 {
			return true
		}
	}
	return false
}

// SearchHits reads the file:line hits out of a search's output. Paths are
// resolved against each of dirs (the command's folders) and kept only when
// the file exists inside root.
func SearchHits(text, root string, dirs []string) map[string][]int {
	hits := map[string][]int{}
	add := func(rel string, lines ...int) {
		for _, d := range dirs {
			abs := realCase(Abs(rel, d))
			if !IsInside(abs, root) {
				continue
			}
			if fi, err := os.Stat(abs); err == nil && fi.Mode().IsRegular() {
				hits[abs] = append(hits[abs], lines...)
				return
			}
		}
	}
	for _, m := range reHitLine.FindAllStringSubmatch(text, -1) {
		if n, err := strconv.Atoi(m[2]); err == nil && n > 0 {
			add(m[1], n)
		}
	}
	for _, m := range reChunk.FindAllStringSubmatch(text, -1) {
		from, err1 := strconv.Atoi(m[2])
		to, err2 := strconv.Atoi(m[3])
		if err1 != nil || err2 != nil || from <= 0 || to < from || to-from > maxSeenLines {
			continue
		}
		var lines []int
		for n := from; n <= to; n++ {
			lines = append(lines, n)
		}
		add(m[1], lines...)
	}
	return hits
}

// noteSearchHits records what a lead's search showed, for its task.
func (c *hookCtx) noteSearchHits(text string, dirs []string) {
	if c.task == nil || c.in.AgentID != "" || text == "" {
		return
	}
	hits := SearchHits(text, c.root, dirs)
	if len(hits) == 0 {
		return
	}
	for abs, lines := range hits {
		if h := fileHash(abs); h != "" {
			c.task.noteSeen(abs, h, lines)
		}
	}
	_ = SaveTask(c.task)
}

// shellDirs: the folders a command's relative paths are read from.
func shellDirs(segs []Segment, cwd string) []string {
	dirs := []string{cwd}
	for _, s := range segs {
		if s.Dir != "" && !contains(dirs, s.Dir) {
			dirs = append(dirs, s.Dir)
		}
	}
	return dirs
}

// onlyReads: a search line whose output is what the files hold: every part
// only reads, one part is a search (grep, rg, ag, git grep), and nothing
// reads Plumbline's folder, so Claude's own notes (cat plan.md) can't pass
// for a search result.
func onlyReads(segs []Segment) bool {
	search := false
	for _, s := range segs {
		if !ReadOnly(s) || mentionsDataDir(s) {
			return false
		}
		switch s.Name() {
		case "grep", "egrep", "fgrep", "rg", "ag":
			search = true
		case "git":
			if sub, _ := GitSub(s); sub == "grep" {
				search = true
			}
		}
	}
	return search
}

// planFiles: the files plan.md says it changes, relative to the root.
func planFiles(t *Task) []string {
	text, _, err := ReadDoc(t, DocPlan)
	if err != nil {
		return nil
	}
	return CheckPlan(text).Files
}

func findSeenBySuffix(t *Task, rel string) string {
	hit := ""
	for p := range t.Seen {
		if strings.HasSuffix(filepath.ToSlash(p), "/"+rel) {
			if hit != "" {
				return ""
			}
			hit = p
		}
	}
	return hit
}
