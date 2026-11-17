package core

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Segment is one simple command found anywhere in a command line: in a list
// or pipeline, a subshell, an if/for/while/case body, a function body, a
// command substitution, or inside a wrapper (sh -c, eval, xargs, timeout,
// find -exec, plumbline run proof --).
type Segment struct {
	Words     []string // command and arguments; a word that isn't fully known is kept as written
	Dyn       []bool   // Dyn[i]: Words[i] holds an expansion or substitution, so its value isn't known
	Glob      []bool   // Glob[i]: Words[i] is an unquoted pattern (*, ?, [...])
	Redirects []string // write targets: > >> >| &> &>> <> and >&file
	Inputs    []string // < file
	Env       []string // NAME=value set for this command (or, with no words, for the shell)
	Exported  bool     // Env reaches later commands (export, declare -x, set -a)
	Stdin     *string  // literal heredoc or here-string body fed to the command
	Piped     bool     // its input comes from a pipe
	MoreArgs  bool     // more arguments are added when it runs (xargs, find -exec)
	Dir       string   // working directory when it runs; "" when not known
	Via       string   // the wrapper it was found in, for messages
	Opaque    string   // non-empty: what this runs can't be known, and why
	src       []*syntax.Word
	// stdinUnknown: a heredoc with expansions feeds it, so a shell reading
	// it runs commands that can't be known.
	stdinUnknown bool
}

func (s Segment) Name() string {
	if len(s.Words) == 0 {
		return ""
	}
	return filepath.Base(s.Words[0])
}

func (s Segment) Arg(i int) string {
	if i < len(s.Words) {
		return s.Words[i]
	}
	return ""
}

func (s Segment) dyn(i int) bool  { return i < len(s.Dyn) && s.Dyn[i] }
func (s Segment) glob(i int) bool { return i < len(s.Glob) && s.Glob[i] }

// dynArgs: an argument written in the command isn't known until it runs.
func (s Segment) dynArgs() bool {
	for i := 1; i < len(s.Words); i++ {
		if s.dyn(i) {
			return true
		}
	}
	return false
}

// unknownArgs: some argument isn't known when the gate runs, including ones
// xargs or find add.
func (s Segment) unknownArgs() bool { return s.MoreArgs || s.dynArgs() }

// tail returns the segment's words from i on as a new segment that runs in
// the same place (a wrapper's inner command).
func (s Segment) tail(i int, via string) Segment {
	in := Segment{Dir: s.Dir, Piped: s.Piped, Stdin: s.Stdin, stdinUnknown: s.stdinUnknown, Via: via, MoreArgs: s.MoreArgs, Exported: s.Exported}
	in.Env = append(in.Env, s.Env...)
	in.Redirects = append(in.Redirects, s.Redirects...)
	in.Inputs = append(in.Inputs, s.Inputs...)
	if i < len(s.Words) {
		in.Words = append(in.Words, s.Words[i:]...)
		in.Dyn = append(in.Dyn, s.Dyn[i:]...)
		in.Glob = append(in.Glob, s.Glob[i:]...)
		if len(s.src) == len(s.Words) {
			in.src = append(in.src, s.src[i:]...)
		}
	}
	return in
}

// carrier keeps a replaced wrapper's redirects and env, so they are still
// checked once the wrapper is replaced by what it runs.
func (s Segment) carrier(via string) []Segment {
	if len(s.Redirects) == 0 && len(s.Env) == 0 && len(s.Inputs) == 0 {
		return nil
	}
	return []Segment{{Redirects: s.Redirects, Env: s.Env, Inputs: s.Inputs, Dir: s.Dir, Via: via}}
}

// opaque makes a one-segment result that can't be checked.
func opaque(why, dir, via string) []Segment {
	return []Segment{{Opaque: why, Dir: dir, Via: via}}
}

const maxShellDepth = 6

// parseCtx is shared by one command line and every command string nested in
// it (sh -c, eval), so a value set in one and used in another is still seen.
type parseCtx struct {
	arith      bool // arithmetic is evaluated somewhere (it can run $(...) found in a value)
	suspicious bool // a quoted value holds $( or a backtick
	extglob    bool // an extended glob or a zsh glob qualifier, which can run code
}

// Segments splits a command line into simple commands, without a working
// directory (paths in it stay relative).
func Segments(cmd string) []Segment { return ParseShell(cmd, "") }

// ParseShell parses a command line as bash and returns every simple command
// in it. dir is the directory it runs in. A line that can't be parsed comes
// back as one segment marked Opaque: what can't be read isn't run unchecked.
func ParseShell(cmd, dir string) []Segment {
	pc := &parseCtx{}
	segs := parseShell(cmd, dir, 0, "", pc)
	switch {
	case pc.extglob:
		return opaque("it uses an extended glob or a zsh glob qualifier, which can run code", dir, "")
	case pc.arith && pc.suspicious:
		return opaque("it does arithmetic on a value that holds $( or a backtick, which the shell would run", dir, "")
	}
	return segs
}

func parseShell(cmd, dir string, depth int, via string, pc *parseCtx) []Segment {
	if depth > maxShellDepth {
		return opaque("it nests commands too deeply to check", dir, via)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(cmd), "")
	if err != nil {
		return opaque("it couldn't be parsed as a shell command ("+err.Error()+")", dir, via)
	}
	w := &walker{dir: dir, depth: depth, via: via, pc: pc, vars: map[string][]string{}, funcs: map[string]bool{}}
	syntax.Walk(f, w.visit)
	return w.segs
}

// walker visits every node of a parsed command line. Besides the commands,
// it follows the working directory (cd) and the shell variables the line
// sets to known values, so "n=3; sed -n "${n}p" f" is read as sed -n 3p.
type walker struct {
	segs   []Segment
	dir    string
	depth  int
	via    string
	pc     *parseCtx
	stack  []syntax.Node
	dirs   []string
	vars   map[string][]string // shell variables with known values (a for loop's: each of them)
	funcs  map[string]bool     // functions defined in the line
	sub    int                 // inside a subshell: assignments there don't last
	cond   int                 // inside a branch that may not run: assignments there may not happen
	inFunc int                 // inside a function body: it runs later, with values not known now
	export bool                // set -a: every assignment is exported
}

// conditional nodes may not run (if/case branches, loop bodies, the right
// side of && and ||, a command sent to the background).
func (w *walker) conditional(n syntax.Node) bool {
	switch n := n.(type) {
	case *syntax.IfClause, *syntax.CaseClause, *syntax.WhileClause, *syntax.ForClause:
		return true
	case *syntax.Stmt:
		if n.Background || n.Coprocess {
			return true
		}
		if len(w.stack) >= 2 {
			if b, ok := w.stack[len(w.stack)-2].(*syntax.BinaryCmd); ok && (b.Op == syntax.AndStmt || b.Op == syntax.OrStmt) && b.Y == n {
				return true
			}
		}
	}
	return false
}

// subshellish nodes run in their own shell: a cd inside doesn't last.
func subshellish(n syntax.Node) bool {
	switch n := n.(type) {
	case *syntax.Subshell, *syntax.CmdSubst, *syntax.ProcSubst:
		return true
	case *syntax.BinaryCmd:
		return n.Op == syntax.Pipe || n.Op == syntax.PipeAll
	}
	return false
}

func (w *walker) visit(n syntax.Node) bool {
	if n == nil {
		top := w.stack[len(w.stack)-1]
		if w.conditional(top) {
			w.cond--
		}
		w.stack = w.stack[:len(w.stack)-1]
		if subshellish(top) {
			w.dir = w.dirs[len(w.dirs)-1]
			w.dirs = w.dirs[:len(w.dirs)-1]
			w.sub--
		}
		switch top := top.(type) {
		case *syntax.FuncDecl:
			w.inFunc--
		case *syntax.ForClause:
			if wi, ok := top.Loop.(*syntax.WordIter); ok && wi.Name != nil {
				delete(w.vars, wi.Name.Value) // after the loop: the last value, treated as unknown
			}
		}
		return true
	}
	w.stack = append(w.stack, n)
	if subshellish(n) {
		w.dirs = append(w.dirs, w.dir)
		w.sub++
	}
	if w.conditional(n) {
		w.cond++
	}
	switch n := n.(type) {
	case *syntax.Stmt:
		if _, isCall := n.Cmd.(*syntax.CallExpr); !isCall && len(n.Redirs) > 0 {
			// { ...; } > file, while ...; done < file, a bare > file
			s := Segment{Dir: w.dir, Via: w.via}
			w.redirects(&s, n.Redirs, w.binding(nil))
			s.Stdin = nil
			w.segs = append(w.segs, s)
		}
	case *syntax.CallExpr:
		w.call(n)
	case *syntax.DeclClause:
		w.decl(n)
	case *syntax.FuncDecl:
		if n.Name != nil {
			w.funcs[n.Name.Value] = true
		}
		w.inFunc++
	case *syntax.ForClause:
		w.forgetAssigned(n)
		switch l := n.Loop.(type) {
		case *syntax.CStyleLoop:
			w.pc.arith = true
		case *syntax.WordIter:
			w.bindLoop(l)
		}
	case *syntax.WhileClause:
		w.forgetAssigned(n)
	case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
		w.pc.arith = true
		w.forgetAll()
	case *syntax.TestClause:
		w.pc.arith = true // [[ $x -eq 0 ]] evaluates $x as arithmetic
	case *syntax.ParamExp:
		if n.Index != nil || n.Slice != nil {
			w.pc.arith = true
		}
		if n.Exp != nil && (n.Exp.Op.String() == "=" || n.Exp.Op.String() == ":=") {
			w.forgetAll() // ${x:=v} assigns
		}
	case *syntax.ExtGlob:
		w.pc.extglob = true
	case *syntax.Lit:
		w.suspect(n.Value)
	case *syntax.SglQuoted:
		w.suspect(n.Value)
	}
	return true
}

func (w *walker) suspect(v string) {
	if strings.Contains(v, "$(") || strings.Contains(v, "`") {
		w.pc.suspicious = true
	}
}

func (w *walker) forgetAll() { w.vars = map[string][]string{} }

// forgetAssigned: a loop body runs again, so a value it sets later may be
// the one an earlier command in it sees.
func (w *walker) forgetAssigned(loop syntax.Node) {
	syntax.Walk(loop, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 {
				for _, a := range n.Assigns {
					if a.Name != nil {
						delete(w.vars, a.Name.Value)
					}
				}
			} else if name := n.Args[0].Lit(); changesVars[name] || w.funcs[name] {
				w.forgetAll()
			}
		case *syntax.DeclClause:
			for _, a := range n.Args {
				if a.Name != nil {
					delete(w.vars, a.Name.Value)
				}
			}
		case *syntax.WordIter:
			if n.Name != nil {
				delete(w.vars, n.Name.Value)
			}
		case *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.LetClause:
			w.forgetAll()
		}
		return true
	})
}

// Builtins that can change shell variables in ways not followed here.
var changesVars = map[string]bool{"eval": true, "source": true, ".": true, "mapfile": true, "readarray": true,
	"getopts": true, "printf": true, "read": true, "let": true, "set": true, "unset": true, "shift": true,
	"select": true, "trap": true, "wait": true, "alias": true, "unalias": true, "enable": true, "vared": true,
	"zparseopts": true, "typeset": true, "declare": true, "local": true, "export": true, "readonly": true}

func (w *walker) bindLoop(l *syntax.WordIter) {
	if l.Name == nil {
		return
	}
	name := l.Name.Value
	delete(w.vars, name)
	if len(l.Items) == 0 || w.inFunc > 0 {
		return
	}
	var vals []string
	b := w.binding(nil)
	for _, it := range l.Items {
		v, lit, glob := wordValue(it, w.dir, b)
		if !lit {
			return
		}
		for _, x := range braceWords(it, v) {
			if glob {
				if w.dir == "" && !filepath.IsAbs(x) {
					return
				}
				m, _ := filepath.Glob(Abs(x, w.dir))
				if len(m) == 0 {
					m = []string{x}
				} else if !filepath.IsAbs(x) {
					for i := range m {
						if r, err := filepath.Rel(Abs(w.dir, "/"), m[i]); err == nil {
							m[i] = r
						}
					}
				}
				vals = append(vals, m...)
			} else {
				vals = append(vals, x)
			}
		}
	}
	if len(vals) > 0 && len(vals) <= 32 {
		w.vars[name] = vals
	}
}

// binding returns the variables with one known value, plus the given ones.
func (w *walker) binding(extra map[string]string) map[string]string {
	if w.inFunc > 0 {
		return extra
	}
	b := map[string]string{}
	for k, v := range w.vars {
		if len(v) == 1 {
			b[k] = v[0]
		}
	}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func (w *walker) parentStmt() (*syntax.Stmt, bool) {
	if len(w.stack) < 2 {
		return nil, false
	}
	st, ok := w.stack[len(w.stack)-2].(*syntax.Stmt)
	if !ok {
		return nil, false
	}
	piped := false
	if len(w.stack) >= 3 {
		if b, ok := w.stack[len(w.stack)-3].(*syntax.BinaryCmd); ok && (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) && b.Y == st {
			piped = true
		}
	}
	return st, piped
}

// combos lists every assignment of the loop variables a command uses (each
// for-loop value), so each run of it is checked; at most 32.
func (w *walker) combos(ce *syntax.CallExpr, st *syntax.Stmt) []map[string]string {
	if w.inFunc > 0 {
		return []map[string]string{nil}
	}
	used := []string{}
	seen := map[string]bool{}
	look := func(n syntax.Node) {
		syntax.Walk(n, func(x syntax.Node) bool {
			if p, ok := x.(*syntax.ParamExp); ok && p.Param != nil && len(w.vars[p.Param.Value]) > 1 && !seen[p.Param.Value] {
				seen[p.Param.Value] = true
				used = append(used, p.Param.Value)
			}
			return true
		})
	}
	look(ce)
	if st != nil {
		for _, r := range st.Redirs {
			look(r)
		}
	}
	out := []map[string]string{{}}
	for _, name := range used {
		var next []map[string]string
		for _, c := range out {
			for _, v := range w.vars[name] {
				m := map[string]string{name: v}
				for k, x := range c {
					m[k] = x
				}
				next = append(next, m)
			}
		}
		if len(next) > 32 {
			return []map[string]string{nil} // too many: leave them unknown
		}
		out = next
	}
	return out
}

func (w *walker) call(ce *syntax.CallExpr) {
	st, piped := w.parentStmt()
	var first *Segment
	for _, extra := range w.combos(ce, st) {
		b := w.binding(extra)
		s := w.segment(ce, st, piped, b)
		if first == nil {
			c := s
			first = &c
		}
		if changesVars[s.Name()] || w.funcs[s.Name()] || wrappers[s.Name()].env || s.Name() == "builtin" || s.Name() == "command" {
			w.forgetAll() // checked before unwrapping: eval's own name is gone after it
		}
		for _, x := range unwrap(s, w.depth, w.pc) {
			w.segs = append(w.segs, x)
			if changesVars[x.Name()] || w.funcs[x.Name()] {
				w.forgetAll()
			}
			if x.Name() == "set" {
				for _, a := range x.Words[1:] {
					if a == "allexport" || strings.HasPrefix(a, "-") && strings.Contains(a, "a") {
						w.export = true
					}
				}
			}
		}
	}
	s := *first
	if len(ce.Args) == 0 {
		// NAME=value on its own sets a shell variable
		for _, a := range ce.Assigns {
			w.assign(a)
		}
	}
	switch {
	case s.Opaque != "":
	case s.Name() == "cd" || s.Name() == "pushd":
		w.dir = cdTarget(s, w.dir)
	case s.Name() == "popd":
		w.dir = ""
	}
}

func (w *walker) assign(a *syntax.Assign) {
	if a.Name == nil {
		return
	}
	name := a.Name.Value
	if w.sub > 0 || w.cond > 0 || w.inFunc > 0 || a.Value == nil || a.Append || a.Index != nil {
		delete(w.vars, name)
		return
	}
	if v, lit, glob := wordValue(a.Value, w.dir, w.binding(nil)); lit && !glob {
		w.vars[name] = []string{v}
	} else {
		delete(w.vars, name)
	}
}

// segment builds the command's segment with these variable values.
func (w *walker) segment(ce *syntax.CallExpr, st *syntax.Stmt, piped bool, b map[string]string) Segment {
	s := Segment{Dir: w.dir, Via: w.via, Piped: piped, Exported: w.export}
	for _, a := range ce.Assigns {
		s.Env = append(s.Env, assignText(a, w.dir, b))
	}
	for i, wd := range ce.Args {
		v, lit, glob := wordValue(wd, w.dir, b)
		if i == 0 && lit && isBareLit(wd) && len(v) > 1 && v[0] == '=' {
			v = v[1:] // zsh: =git runs git
		}
		for _, x := range braceWords(wd, v) {
			s.Words = append(s.Words, x)
			s.Dyn = append(s.Dyn, !lit)
			s.Glob = append(s.Glob, glob && hasGlob(x))
			s.src = append(s.src, wd)
		}
	}
	if st != nil {
		w.redirects(&s, st.Redirs, b)
	}
	if len(s.Words) > 0 && (s.Dyn[0] || s.Glob[0]) {
		if !knownName(ce.Args[0]) || s.Glob[0] {
			s.Opaque = "the command name comes from a variable, substitution or pattern (" + s.Words[0] + ")"
		}
	}
	return s
}

// isBareLit: the word is written without quotes or expansions.
func isBareLit(w *syntax.Word) bool {
	for _, p := range w.Parts {
		if _, ok := p.(*syntax.Lit); !ok {
			return false
		}
	}
	return true
}

// braceWords applies brace expansion: git {push,x} is git push x.
func braceWords(w *syntax.Word, v string) []string {
	if !isBareLit(w) || !strings.Contains(v, "{") {
		return []string{v}
	}
	for _, p := range w.Parts {
		if strings.Contains(p.(*syntax.Lit).Value, `\`) {
			return []string{v} // escaped braces stay as written
		}
	}
	return expandBraces(v)
}

func (w *walker) decl(d *syntax.DeclClause) {
	s := Segment{Dir: w.dir, Via: w.via}
	variant := d.Variant.Value
	s.Words = []string{variant}
	s.Dyn = []bool{false}
	s.Glob = []bool{false}
	exported := variant == "export" || w.export
	b := w.binding(nil)
	for _, a := range d.Args {
		if a.Naked && a.Name == nil && a.Value != nil {
			// a flag like -x
			v, lit, _ := wordValue(a.Value, w.dir, b)
			s.Words = append(s.Words, v)
			s.Dyn = append(s.Dyn, !lit)
			s.Glob = append(s.Glob, false)
			if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+") {
				if strings.Contains(v, "x") {
					exported = true
				}
				if strings.Contains(v, "i") {
					w.pc.arith = true // declare -i evaluates values as arithmetic
				}
				if strings.Contains(v, "n") {
					w.forgetAll() // a nameref
				}
			}
			continue
		}
		s.Env = append(s.Env, assignText(a, w.dir, b))
	}
	for _, a := range d.Args {
		if a.Name != nil {
			w.assign(a)
		}
	}
	s.Exported = exported
	w.segs = append(w.segs, s)
}

func cdTarget(s Segment, dir string) string {
	args := []string{}
	for i := 1; i < len(s.Words); i++ {
		if s.Words[i] == "-P" || s.Words[i] == "-L" || s.Words[i] == "--" {
			continue
		}
		if s.dyn(i) || s.glob(i) {
			return ""
		}
		args = append(args, s.Words[i])
	}
	if len(args) == 0 {
		if s.Name() == "pushd" {
			return ""
		}
		h, _ := os.UserHomeDir()
		return h
	}
	if args[0] == "-" || strings.HasPrefix(args[0], "+") || strings.HasPrefix(args[0], "-") {
		return ""
	}
	if dir == "" && !filepath.IsAbs(args[0]) && !strings.HasPrefix(args[0], "~") {
		return ""
	}
	return Abs(args[0], dir)
}

func (w *walker) redirects(s *Segment, rs []*syntax.Redirect, b map[string]string) {
	for _, r := range rs {
		var target string
		if r.Word != nil {
			target, _, _ = wordValue(r.Word, w.dir, b)
		}
		switch r.Op {
		case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
			s.Redirects = append(s.Redirects, target)
		case syntax.DplOut:
			if !isFD(target) {
				s.Redirects = append(s.Redirects, target) // >&file is &>file
			}
		case syntax.RdrIn:
			s.Inputs = append(s.Inputs, target)
		case syntax.Hdoc, syntax.DashHdoc:
			if body, ok := heredocBody(r); ok {
				s.Stdin = &body
			} else {
				s.stdinUnknown = true
			}
		case syntax.WordHdoc:
			if v, lit, _ := wordValue(r.Word, w.dir, b); lit {
				v += "\n"
				s.Stdin = &v
			} else {
				s.stdinUnknown = true
			}
		}
	}
}

func isFD(t string) bool {
	if t == "-" || t == "" {
		return true
	}
	for _, c := range t {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func assignText(a *syntax.Assign, dir string, b map[string]string) string {
	name := ""
	if a.Name != nil {
		name = a.Name.Value
	}
	if a.Value == nil {
		if a.Array != nil {
			return name + "=(...)"
		}
		return name
	}
	v, _, _ := wordValue(a.Value, dir, b)
	return name + "=" + v
}

// knownName: a command name like $(go env GOROOT)/bin/gofmt still names a
// known program by its last part.
func knownName(w *syntax.Word) bool {
	if len(w.Parts) == 0 {
		return false
	}
	l, ok := w.Parts[len(w.Parts)-1].(*syntax.Lit)
	if !ok {
		return false
	}
	i := strings.LastIndex(l.Value, "/")
	return i >= 0 && i < len(l.Value)-1 && !strings.ContainsAny(l.Value[i+1:], "*?[{")
}

func knownVar(name, dir string, b map[string]string) (string, bool) {
	if v, ok := b[name]; ok {
		return v, true
	}
	switch name {
	case "HOME":
		h, err := os.UserHomeDir()
		return h, err == nil && h != ""
	case "PWD":
		return dir, dir != ""
	case "TMPDIR", "USER", "CLAUDE_PLUGIN_ROOT", "CLAUDE_PROJECT_DIR":
		v := os.Getenv(name)
		return v, v != ""
	}
	return "", false
}

func simpleParam(p *syntax.ParamExp) bool {
	return !p.Excl && !p.Length && !p.Width && p.Index == nil && p.Slice == nil && p.Repl == nil && p.Names == 0 && p.Exp == nil && p.Param != nil
}

// wordValue resolves a word the way the shell would, where that can be
// known without running anything. lit is false when part of it can't be
// known; the value is then the word as written. glob is true for an unquoted
// pattern.
func wordValue(w *syntax.Word, dir string, b map[string]string) (val string, lit bool, glob bool) {
	var sb strings.Builder
	lit = true
	param := func(p *syntax.ParamExp) {
		if v, ok := knownVar(p.Param.Value, dir, b); ok && simpleParam(p) {
			sb.WriteString(v)
		} else {
			lit = false
		}
	}
	for i, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			v := p.Value
			if i == 0 && (v == "~" || strings.HasPrefix(v, "~/")) {
				h, _ := os.UserHomeDir()
				v = h + v[1:]
			}
			if hasGlob(v) {
				glob = true
			}
			sb.WriteString(unescapeBare(v))
		case *syntax.SglQuoted:
			if p.Dollar {
				v, ok := ansiC(p.Value)
				if !ok {
					lit = false
				}
				sb.WriteString(v)
			} else {
				sb.WriteString(p.Value)
			}
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				switch q := q.(type) {
				case *syntax.Lit:
					sb.WriteString(unescapeDouble(q.Value))
				case *syntax.ParamExp:
					param(q)
				default:
					lit = false
				}
			}
		case *syntax.ParamExp:
			param(p)
		case *syntax.ExtGlob:
			glob = true
			lit = false
		default:
			lit = false
		}
	}
	if !lit {
		return printWord(w), false, glob
	}
	return sb.String(), true, glob
}

func printWord(w *syntax.Word) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, w)
	return b.String()
}

// hasGlob: an unescaped *, ? or [ ... ].
func hasGlob(v string) bool {
	for i := 0; i < len(v); i++ {
		switch v[i] {
		case '\\':
			i++
		case '*', '?':
			return true
		case '[':
			if strings.Contains(v[i:], "]") {
				return true
			}
		}
	}
	return false
}

func unescapeBare(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
			if v[i] == '\n' {
				continue
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

func unescapeDouble(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) && strings.IndexByte("$`\"\\\n", v[i+1]) >= 0 {
			i++
			if v[i] == '\n' {
				continue
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// ansiC decodes a $'...' string as bash and zsh do. ok is false for an
// escape it doesn't know, so the word is treated as unknown.
func ansiC(v string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' || i+1 >= len(v) {
			b.WriteByte(c)
			continue
		}
		i++
		switch e := v[i]; e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'e', 'E':
			b.WriteByte(27)
		case 'f':
			b.WriteByte(12)
		case 'v':
			b.WriteByte(11)
		case '\\', '\'', '"', '?':
			b.WriteByte(e)
		case 'x', 'u', 'U':
			max := map[byte]int{'x': 2, 'u': 4, 'U': 8}[e]
			j := i + 1
			for j < len(v) && j-i-1 < max && isHex(v[j]) {
				j++
			}
			if j == i+1 {
				return "", false
			}
			n, _ := strconv.ParseUint(v[i+1:j], 16, 32)
			if e == 'x' {
				b.WriteByte(byte(n))
			} else {
				b.WriteRune(rune(n))
			}
			i = j - 1
		case '0', '1', '2', '3', '4', '5', '6', '7':
			j := i
			for j < len(v) && j-i < 3 && v[j] >= '0' && v[j] <= '7' {
				j++
			}
			n, _ := strconv.ParseUint(v[i:j], 8, 32)
			b.WriteByte(byte(n))
			i = j - 1
		case 'c':
			if i+1 >= len(v) {
				return "", false
			}
			i++
			b.WriteByte(v[i] & 0x1f)
		default:
			return "", false
		}
	}
	return b.String(), true
}

// heredocBody returns a heredoc's text when it's fully known: a quoted tag
// (<<'EOF') keeps the body as written; an unquoted one may not hold
// expansions.
func heredocBody(r *syntax.Redirect) (string, bool) {
	if r.Hdoc == nil {
		return "", true
	}
	quoted := false
	if r.Word != nil {
		for _, p := range r.Word.Parts {
			switch p := p.(type) {
			case *syntax.SglQuoted, *syntax.DblQuoted:
				quoted = true
			case *syntax.Lit:
				if strings.Contains(p.Value, `\`) {
					quoted = true
				}
			}
		}
	}
	var b strings.Builder
	for _, p := range r.Hdoc.Parts {
		l, ok := p.(*syntax.Lit)
		if !ok {
			return "", false
		}
		if quoted {
			b.WriteString(l.Value)
		} else {
			b.WriteString(unescapeHeredoc(l.Value))
		}
	}
	return b.String(), true
}

func unescapeHeredoc(v string) string {
	if !strings.Contains(v, `\`) {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) && strings.IndexByte("$`\\\n", v[i+1]) >= 0 {
			i++
			if v[i] == '\n' {
				continue
			}
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

// Wrappers that run the rest of their arguments as a command, with every
// option they take. An option not listed here makes the command unknown:
// it might take a value, and then the wrong word would be read as the
// command.
type wrapSpec struct {
	val        string // options that take a value (short like -u, long like --user)
	flags      string // options without a value (short ones may be bundled: -nE)
	prints     string // options that make it print instead of run a command
	positional int    // plain arguments before the command (timeout's duration)
	env        bool   // NAME=value may come before the command
}

var wrappers = map[string]wrapSpec{
	"sudo": {val: "-u -g -C -D -h -p -r -t -U -T -R --user --group --close-from --chdir --host --prompt --role --type --other-user --command-timeout --chroot",
		flags:  "-A -b -E -H -n -P -S -s -i --askpass --background --preserve-env --set-home --login --non-interactive --preserve-groups --stdin --shell",
		prints: "-e -l -v -V -k -K --edit --list --validate --version --help --remove-timestamp --reset-timestamp", env: true},
	"doas":       {val: "-u -C", flags: "-n -s", prints: "-L"},
	"nohup":      {},
	"nice":       {val: "-n --adjustment"},
	"time":       {val: "-o -f --output --format", flags: "-p -l -h -v -a -q --append --verbose --portability --quiet"},
	"timeout":    {val: "-s -k --signal --kill-after", flags: "-v --preserve-status --foreground --verbose", positional: 1},
	"gtimeout":   {val: "-s -k --signal --kill-after", flags: "-v --preserve-status --foreground --verbose", positional: 1},
	"stdbuf":     {val: "-i -o -e --input --output --error"},
	"caffeinate": {val: "-t -w", flags: "-d -i -m -s -u"},
	"chronic":    {flags: "-e -v"},
	"ionice":     {val: "-c -n -p -P -u", flags: "-t"},
	"command":    {flags: "-p", prints: "-v -V"},
	"builtin":    {},
	"exec":       {val: "-a", flags: "-c -l"},
	"env":        {val: "-u -C -S -P --unset --chdir --split-string", flags: "-i -0 -v --ignore-environment --null --debug", env: true},
	"arch":       {val: "-arch -e -d", flags: "-c -32 -64 -x86_64 -arm64 -arm64e -i386 -h"},
	"noglob":     {},
	"nocorrect":  {},
	"-":          {},
	"xcrun":      {val: "--sdk --toolchain", flags: "-v -l -n -r -k --verbose --log --no-cache --run --kill-cache", prints: "-f --find --show-sdk-path --show-sdk-version --show-sdk-build-version --show-sdk-platform-path --show-sdk-platform-version --help -h --version"},
	"setsid":     {flags: "-c -f -w --ctty --fork --wait"},
	"unbuffer":   {flags: "-p"},
	"taskset":    {flags: "-a -p -c --all-tasks --pid --cpu-list", positional: 1},
	"sched":      {positional: 1},
}

var wrapSets = map[string]map[string]map[string]bool{}

func init() {
	for name, sp := range wrappers {
		wrapSets[name] = map[string]map[string]bool{"val": set(sp.val), "flags": set(sp.flags), "prints": set(sp.prints)}
	}
}

func set(list string) map[string]bool {
	m := map[string]bool{}
	for _, f := range strings.Fields(list) {
		m[f] = true
	}
	return m
}

type wrapResult struct {
	i       int      // index of the command
	env     []string // NAME=value given to it
	dir     string   // where it runs (env -C)
	split   string   // env -S: a string to run as a command
	prints  bool     // it only prints
	unknown string   // an option not known
}

func skipWrapperOpts(s Segment, spec wrapSpec, name string) wrapResult {
	sets := wrapSets[name]
	val, flags, prints := sets["val"], sets["flags"], sets["prints"]
	r := wrapResult{dir: s.Dir}
	// special handles the value of an option that changes where or what it runs.
	special := func(opt, v string, dyn bool) bool {
		switch {
		case name == "env" && (opt == "-C" || opt == "--chdir"):
			if dyn {
				r.dir = ""
			} else {
				r.dir = Abs(v, r.dir)
			}
		case name == "env" && (opt == "-S" || opt == "--split-string"):
			if dyn {
				r.unknown = opt
				return true
			}
			r.split = v
			return true
		}
		return false
	}
	i := 1
	for i < len(s.Words) {
		w := s.Words[i]
		if s.dyn(i) {
			break
		}
		if w == "--" {
			i++
			break
		}
		if w == "-" && name == "env" {
			i++
			continue
		}
		if !strings.HasPrefix(w, "-") || w == "-" {
			if spec.env && reAssignWord.MatchString(w) {
				r.env = append(r.env, w)
				i++
				continue
			}
			break
		}
		if strings.HasPrefix(w, "--") {
			n, v, hasEq := strings.Cut(w, "=")
			switch {
			case prints[n]:
				r.prints = true
				return r
			case val[n]:
				dyn := false
				if !hasEq {
					v, dyn = s.Arg(i+1), s.dyn(i+1)
					i++
				}
				i++
				if special(n, v, dyn) {
					r.i = i
					return r
				}
			case flags[n]:
				i++
			default:
				r.unknown = w
				return r
			}
			continue
		}
		if val[w] || flags[w] || prints[w] {
			// a whole word option: -arch, -x86_64, or a short one
			switch {
			case prints[w]:
				r.prints = true
				return r
			case val[w]:
				v, dyn := s.Arg(i+1), s.dyn(i+1)
				i += 2
				if special(w, v, dyn) {
					r.i = i
					return r
				}
			default:
				i++
			}
			continue
		}
		if name == "nice" && isFD(strings.TrimPrefix(w, "-")) {
			i++ // nice -10
			continue
		}
		letters := w[1:]
		for j := 0; j < len(letters); j++ {
			opt := "-" + string(letters[j])
			switch {
			case prints[opt]:
				r.prints = true
				return r
			case val[opt]:
				v, dyn := letters[j+1:], false
				if v == "" {
					v, dyn = s.Arg(i+1), s.dyn(i+1)
					i++
				}
				if special(opt, v, dyn) {
					r.i = i + 1
					return r
				}
				j = len(letters)
			case flags[opt]:
			default:
				r.unknown = w
				return r
			}
		}
		i++
	}
	r.i = i + spec.positional
	return r
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "fish": true}

// unwrap replaces a wrapper with the command it runs, and adds the commands
// hidden in xargs, find -exec, fd -x, eval, sh -c, trap and the command after
// plumbline run proof --.
func unwrap(s Segment, depth int, pc *parseCtx) []Segment {
	if s.Opaque != "" || len(s.Words) == 0 {
		return []Segment{s}
	}
	if depth > maxShellDepth {
		s.Opaque = "it nests commands too deeply to check"
		return []Segment{s}
	}
	name := s.Name()
	if spec, ok := wrappers[name]; ok {
		r := skipWrapperOpts(s, spec, name)
		switch {
		case r.prints:
			return []Segment{s}
		case r.unknown != "":
			s.Opaque = fmt.Sprintf("%s has an option Plumbline doesn't know (%s), so the command it runs can't be found", name, r.unknown)
			return []Segment{s}
		case r.split != "":
			return append(s.carrier(name), parseShell(r.split+" "+joinRest(s, r.i), r.dir, depth+1, name+" -S", pc)...)
		}
		if r.i >= len(s.Words) {
			return []Segment{s} // env alone prints, time alone times nothing
		}
		in := s.tail(r.i, name)
		in.Env = append(in.Env, r.env...)
		in.Dir = r.dir
		if in.dyn(0) || in.glob(0) {
			if len(in.src) == 0 || !knownName(in.src[0]) || in.glob(0) {
				in.Opaque = "the command name comes from a variable, substitution or pattern (" + in.Words[0] + ")"
			}
		}
		return unwrap(in, depth+1, pc)
	}
	switch {
	case shells[name]:
		return unwrapShell(s, depth, pc)
	case name == "eval":
		for i := 1; i < len(s.Words); i++ {
			if s.dyn(i) {
				s.Opaque = "eval runs text that isn't known until it runs"
				return []Segment{s}
			}
		}
		return append(s.carrier("eval"), parseShell(strings.Join(s.Words[1:], " "), s.Dir, depth+1, "eval", pc)...)
	case name == "emulate":
		for i := 1; i < len(s.Words); i++ {
			if s.Words[i] == "-c" {
				if s.dyn(i + 1) {
					s.Opaque = "emulate -c runs text that isn't known until it runs"
					return []Segment{s}
				}
				return append(s.carrier("emulate"), parseShell(s.Arg(i+1), s.Dir, depth+1, "emulate -c", pc)...)
			}
		}
	case name == "trap":
		ai := 1
		if s.Arg(1) == "--" {
			ai = 2
		}
		a := s.Arg(ai)
		if a == "" || a == "-" || a == "-p" || a == "-l" || len(s.Words) <= ai+1 {
			return []Segment{s} // lists or resets traps
		}
		if s.dyn(ai) {
			s.Opaque = "trap runs text that isn't known until it runs"
			return []Segment{s}
		}
		return append([]Segment{{Words: []string{"trap"}, Dyn: []bool{false}, Glob: []bool{false}, Dir: s.Dir, Via: s.Via}}, parseShell(a, s.Dir, depth+1, "trap", pc)...)
	case name == "watch":
		i := 1
		for i < len(s.Words) && strings.HasPrefix(s.Words[i], "-") {
			if s.Words[i] == "-n" || s.Words[i] == "--interval" || s.Words[i] == "-q" {
				i++
			}
			i++
		}
		if i >= len(s.Words) {
			return []Segment{s}
		}
		return append(s.carrier("watch"), parseShell(joinRest(s, i), s.Dir, depth+1, "watch", pc)...)
	case name == "xargs":
		return unwrapXargs(s, depth, pc)
	case name == "find" || name == "gfind":
		return unwrapFind(s, depth, pc)
	case name == "fd" || name == "fdfind":
		return unwrapFd(s, depth, pc)
	case name == "alias":
		for _, a := range s.Words[1:] {
			if strings.Contains(a, "=") {
				s.Opaque = "it defines an alias, which can change what later commands run"
			}
		}
		return []Segment{s}
	case name == "source" || name == ".":
		if s.dyn(1) {
			s.Opaque = "it runs a file whose name isn't known until it runs"
		}
		return []Segment{s}
	case isPlumblineBin(name) && s.Arg(1) == "run" && s.Arg(2) == "proof":
		for i := 3; i < len(s.Words); i++ {
			if s.Words[i] == "--" && !s.dyn(i) && i+1 < len(s.Words) {
				in := s.tail(i+1, "plumbline run proof")
				in.Redirects, in.Env, in.Inputs, in.Stdin = nil, nil, nil, nil
				return append([]Segment{s}, unwrap(in, depth+1, pc)...)
			}
		}
	case name == "git":
		return unwrapGit(s, depth, pc)
	}
	return []Segment{s}
}

func joinRest(s Segment, i int) string {
	if i >= len(s.Words) {
		return ""
	}
	parts := make([]string, 0, len(s.Words)-i)
	for j := i; j < len(s.Words); j++ {
		parts = append(parts, syntaxQuote(s.Words[j], s.dyn(j)))
	}
	return strings.Join(parts, " ")
}

// syntaxQuote quotes a known word so it parses back as one word; an unknown
// one is kept as written so it stays unknown.
func syntaxQuote(w string, dyn bool) string {
	if dyn {
		return w
	}
	q, err := syntax.Quote(w, syntax.LangBash)
	if err != nil {
		return w
	}
	return q
}

var reAssignWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

func unwrapShell(s Segment, depth int, pc *parseCtx) []Segment {
	name := s.Name()
	i := 1
	hasC := false
	cmdString := ""
	for i < len(s.Words) {
		w := s.Words[i]
		if s.dyn(i) {
			break
		}
		if w == "--" || w == "-" {
			i++
			break
		}
		if strings.HasPrefix(w, "--command=") {
			hasC, cmdString = true, strings.TrimPrefix(w, "--command=")
			i++
			break
		}
		if w == "--command" {
			hasC = true
			i++
			break
		}
		if (strings.HasPrefix(w, "-") || strings.HasPrefix(w, "+")) && len(w) > 1 {
			if w == "-o" || w == "+o" || w == "-O" || w == "+O" || w == "--rcfile" || w == "--init-file" {
				i += 2
				continue
			}
			if w[0] == '-' && !strings.HasPrefix(w, "--") && strings.ContainsRune(w[1:], 'c') {
				hasC = true
			}
			i++
			continue
		}
		break
	}
	if hasC {
		if cmdString == "" {
			if i >= len(s.Words) {
				return []Segment{s}
			}
			if s.dyn(i) {
				s.Opaque = name + " -c runs a command string that isn't known until it runs"
				return []Segment{s}
			}
			cmdString = s.Words[i]
		}
		return append(s.carrier(name+" -c"), parseShell(cmdString, s.Dir, depth+1, name+" -c", pc)...)
	}
	if i < len(s.Words) {
		if s.dyn(i) {
			s.Opaque = name + " runs a script whose name isn't known until it runs"
		}
		return []Segment{s} // runs a script file
	}
	if s.Stdin != nil {
		return append(s.carrier(name), parseShell(*s.Stdin, s.Dir, depth+1, name+" <<", pc)...)
	}
	if s.stdinUnknown {
		s.Opaque = name + " runs commands from a heredoc with $ expansions, which aren't known until it runs"
		return []Segment{s}
	}
	if s.Piped {
		s.Opaque = name + " runs commands from a pipe, which Plumbline can't read"
	}
	return []Segment{s}
}

// xargs's options, GNU and BSD. Any other option makes the command unknown.
var (
	xargsVal     = set("-I -J -L -n -P -s -E -d -a -R -S --arg-file --delimiter --eof --max-lines --max-args --max-procs --max-chars --process-slot-var")
	xargsFlags   = set("-0 -p -r -t -x -o --null --interactive --no-run-if-empty --verbose --exit --open-tty --show-limits")
	xargsOptVals = "iel" // GNU: -i[R], -e[E], -l[N], value attached only
)

func unwrapXargs(s Segment, depth int, pc *parseCtx) []Segment {
	repl := ""
	i := 1
	bad := ""
	for i < len(s.Words) && bad == "" {
		w := s.Words[i]
		if s.dyn(i) {
			break
		}
		if w == "--" {
			i++
			break
		}
		if !strings.HasPrefix(w, "-") || len(w) < 2 {
			break
		}
		if strings.HasPrefix(w, "--") {
			n, v, hasEq := strings.Cut(w, "=")
			switch {
			case n == "--replace":
				repl = "{}"
				if hasEq {
					repl = v
				}
			case xargsVal[n]:
				if !hasEq {
					i++
				}
			case xargsFlags[n]:
			default:
				bad = w
			}
			i++
			continue
		}
		letters := w[1:]
		for j := 0; j < len(letters); j++ {
			opt := "-" + string(letters[j])
			switch {
			case xargsVal[opt]:
				v := letters[j+1:]
				if v == "" {
					v = s.Arg(i + 1)
					i++
				}
				if opt == "-I" || opt == "-J" {
					repl = v
				}
				j = len(letters)
			case strings.IndexByte(xargsOptVals, letters[j]) >= 0:
				if letters[j] == 'i' {
					repl = letters[j+1:]
					if repl == "" {
						repl = "{}"
					}
				}
				j = len(letters)
			case xargsFlags[opt]:
			default:
				bad = w
				j = len(letters)
			}
		}
		i++
	}
	if bad != "" {
		s.Opaque = "xargs has an option Plumbline doesn't know (" + bad + "), so the command it runs can't be found"
		return []Segment{s}
	}
	var in Segment
	if i >= len(s.Words) {
		in = Segment{Words: []string{"echo"}, Dyn: []bool{false}, Glob: []bool{false}, Dir: s.Dir}
	} else {
		in = s.tail(i, "xargs")
		in.Redirects, in.Env, in.Inputs, in.Stdin, in.Piped = nil, nil, nil, nil, false
	}
	in.Via = "xargs"
	if repl != "" {
		for j := range in.Words {
			if strings.Contains(in.Words[j], repl) {
				in.Dyn[j] = true
			}
		}
	} else {
		in.MoreArgs = true
	}
	if in.dyn(0) {
		in.Opaque = "xargs runs a command whose name comes from its input"
	}
	keep := Segment{Words: []string{"xargs"}, Dyn: []bool{false}, Glob: []bool{false}, Redirects: s.Redirects, Env: s.Env, Inputs: s.Inputs, Dir: s.Dir, Piped: s.Piped, Via: s.Via}
	return append([]Segment{keep}, unwrap(in, depth+1, pc)...)
}

var findExec = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}

func unwrapFind(s Segment, depth int, pc *parseCtx) []Segment {
	keep := s
	keep.Words, keep.Dyn, keep.Glob, keep.src = nil, nil, nil, nil
	var inners []Segment
	for i := 0; i < len(s.Words); i++ {
		if !findExec[s.Words[i]] || s.dyn(i) {
			keep.Words = append(keep.Words, s.Words[i])
			keep.Dyn = append(keep.Dyn, s.dyn(i))
			keep.Glob = append(keep.Glob, s.glob(i))
			continue
		}
		j := i + 1
		for j < len(s.Words) && s.Words[j] != ";" && s.Words[j] != "+" {
			j++
		}
		in := Segment{Dir: s.Dir, Via: "find " + s.Words[i]}
		for k := i + 1; k < j; k++ {
			in.Words = append(in.Words, s.Words[k])
			in.Dyn = append(in.Dyn, s.dyn(k) || strings.Contains(s.Words[k], "{}"))
			in.Glob = append(in.Glob, s.glob(k))
		}
		if len(in.Words) > 0 {
			if in.dyn(0) {
				in.Opaque = "find runs a command whose name isn't known"
			}
			inners = append(inners, unwrap(in, depth+1, pc)...)
		}
		i = j
	}
	return append([]Segment{keep}, inners...)
}

// fd's short options that take a value; -x and -X start the command.
const fdShortVal = "etEdjcSoMp"

func unwrapFd(s Segment, depth int, pc *parseCtx) []Segment {
	for i := 1; i < len(s.Words); i++ {
		w := s.Words[i]
		if s.dyn(i) || !strings.HasPrefix(w, "-") {
			continue
		}
		var first string
		start := -1
		switch {
		case w == "--exec" || w == "--exec-batch":
			start = i + 1
		case strings.HasPrefix(w, "--exec=") || strings.HasPrefix(w, "--exec-batch="):
			_, first, _ = strings.Cut(w, "=")
			start = i + 1
		case strings.HasPrefix(w, "--"):
			continue
		default:
			letters := w[1:]
			for j := 0; j < len(letters); j++ {
				if letters[j] == 'x' || letters[j] == 'X' {
					first = letters[j+1:]
					start = i + 1
					break
				}
				if strings.IndexByte(fdShortVal, letters[j]) >= 0 {
					if j == len(letters)-1 {
						i++ // its value is the next word
					}
					break
				}
			}
			if start < 0 {
				continue
			}
		}
		end := start
		for end < len(s.Words) && s.Words[end] != ";" {
			end++
		}
		keep := s
		keep.Words, keep.Dyn, keep.Glob, keep.src = append([]string{}, s.Words[:i]...), append([]bool{}, s.Dyn[:i]...), append([]bool{}, s.Glob[:i]...), nil
		in := Segment{Dir: s.Dir, Via: "fd -x", MoreArgs: true}
		if first != "" {
			in.Words, in.Dyn, in.Glob = []string{first}, []bool{false}, []bool{false}
		}
		for k := start; k < end; k++ {
			in.Words = append(in.Words, s.Words[k])
			in.Dyn = append(in.Dyn, s.dyn(k) || strings.Contains(s.Words[k], "{"))
			in.Glob = append(in.Glob, s.glob(k))
		}
		if len(in.Words) == 0 {
			return []Segment{keep}
		}
		if in.dyn(0) {
			in.Opaque = "fd runs a command whose name isn't known"
		}
		return append([]Segment{keep}, unwrap(in, depth+1, pc)...)
	}
	return []Segment{s}
}

// gitBuiltins are git's own commands; anything else may be an alias.
var gitBuiltins = set(`add am annotate apply archive bisect blame branch bundle cat-file check-attr check-ignore
	check-mailmap check-ref-format checkout checkout-index cherry cherry-pick citool clean clone column commit
	commit-graph commit-tree config count-objects credential describe diagnose diff diff-files diff-index diff-tree
	difftool fast-export fast-import fetch fetch-pack filter-branch for-each-ref for-each-repo format-patch fsck gc
	get-tar-commit-id grep gui hash-object help hook index-pack init instaweb interpret-trailers log ls-files
	ls-remote ls-tree mailinfo mailsplit maintenance merge merge-base merge-file merge-index merge-tree mergetool
	mktag mktree multi-pack-index mv name-rev notes pack-objects pack-redundant pack-refs prune prune-packed pull
	push range-diff read-tree rebase reflog remote repack replace request-pull rerere reset restore rev-list
	rev-parse revert rm send-email send-pack shortlog show show-branch show-index show-ref sparse-checkout stage
	stash status stripspace submodule switch symbolic-ref tag unpack-file unpack-objects update-index update-ref
	update-server-info var verify-commit verify-pack verify-tag version whatchanged worktree write-tree lfs`)

// git's own options, before the subcommand.
var (
	gitGlobalVal   = set("-C -c --git-dir --work-tree --namespace --super-prefix --config-env --attr-source")
	gitGlobalFlags = set(`-v --version -h --help -p --paginate -P --no-pager --bare --no-replace-objects --no-lazy-fetch
		--no-optional-locks --no-advice --literal-pathspecs --glob-pathspecs --noglob-pathspecs --icase-pathspecs
		--html-path --man-path --info-path --exec-path --list-cmds`)
)

type gitInfo struct {
	configs  []string // -c and --config-env settings
	sub      int      // index of the subcommand, -1 when none
	unknown  string   // an option before the subcommand that isn't known
	dir      string   // where it runs, after any -C ("" when not known)
	otherGit bool     // --git-dir, --work-tree or --namespace point it elsewhere
}

func gitParse(s Segment) gitInfo {
	g := gitInfo{sub: -1, dir: s.Dir}
	for i := 1; i < len(s.Words); i++ {
		a := s.Words[i]
		if s.dyn(i) {
			g.sub = i
			return g
		}
		if !strings.HasPrefix(a, "-") {
			g.sub = i
			return g
		}
		n, v, hasEq := strings.Cut(a, "=")
		switch {
		case gitGlobalVal[n] && strings.HasPrefix(n, "--"):
			if !hasEq {
				i++
				v = s.Arg(i)
			}
			if n == "--config-env" {
				g.configs = append(g.configs, v)
			} else if n != "--attr-source" && n != "--super-prefix" {
				g.otherGit = true
			}
		case a == "-c" || a == "-C":
			i++
			if a == "-c" {
				g.configs = append(g.configs, s.Arg(i))
			} else if s.dyn(i) || g.dir == "" && !filepath.IsAbs(s.Arg(i)) {
				g.dir = ""
			} else {
				g.dir = Abs(s.Arg(i), g.dir)
			}
		case strings.HasPrefix(a, "-c") && len(a) > 2:
			g.configs = append(g.configs, a[2:])
		case strings.HasPrefix(a, "-C") && len(a) > 2:
			if g.dir != "" || filepath.IsAbs(a[2:]) {
				g.dir = Abs(a[2:], g.dir)
			}
		case gitGlobalFlags[n]:
		default:
			g.unknown = a
			return g
		}
	}
	return g
}

// gitGlobals returns the -c settings given before the subcommand and the
// subcommand's index (-1 when there is none).
func gitGlobals(s Segment) (configs []string, subIdx int) {
	g := gitParse(s)
	return g.configs, g.sub
}

// gitDir is where a git segment runs: its directory moved by any -C.
func gitDir(s Segment) string { return gitParse(s).dir }

// gitConfigSets reports a git config command that sets a key, and the key.
func gitConfigSets(args []string) (string, bool) {
	var plain []string
	reading := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--get" || a == "--get-all" || a == "--get-regexp" || a == "--list" || a == "-l" || a == "--get-urlmatch":
			reading = true
		case a == "--file" || a == "-f" || a == "--blob" || a == "--type" || a == "--default":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			plain = append(plain, a)
		}
	}
	if len(plain) > 0 && (plain[0] == "get" || plain[0] == "list") {
		return "", false
	}
	if len(plain) > 0 && (plain[0] == "set" || plain[0] == "unset") {
		plain = plain[1:]
		reading = false
	}
	if reading || len(plain) < 2 {
		if len(plain) == 1 {
			for _, a := range args {
				if a == "--unset" || a == "--unset-all" || a == "--add" || a == "--replace-all" || a == "--remove-section" || a == "--rename-section" {
					return plain[0], true
				}
			}
		}
		return "", false
	}
	return plain[0], true
}

// unwrapGit expands a git alias (from -c or the git config) so the command
// it stands for is checked, and refuses what would make that unknown.
func unwrapGit(s Segment, depth int, pc *parseCtx) []Segment {
	g := gitParse(s)
	if g.unknown != "" {
		s.Opaque = "git has an option before its command that Plumbline doesn't know (" + g.unknown + ")"
		return []Segment{s}
	}
	for _, c := range g.configs {
		if strings.HasPrefix(strings.ToLower(c), "alias.") || strings.HasPrefix(strings.ToLower(c), "include") {
			s.Opaque = "it sets a git alias or include with -c, which can stand for any command"
			return []Segment{s}
		}
	}
	if g.sub < 0 {
		return []Segment{s}
	}
	if s.dyn(g.sub) || s.glob(g.sub) {
		s.Opaque = "the git command comes from a variable, substitution or pattern"
		return []Segment{s}
	}
	sub := s.Words[g.sub]
	if sub == "config" {
		if key, sets := gitConfigSets(s.Words[g.sub+1:]); sets {
			low := strings.ToLower(key)
			if strings.HasPrefix(low, "alias.") || strings.HasPrefix(low, "include") {
				s.Opaque = "it sets a git alias or include, which can make a later git command run anything"
				return []Segment{s}
			}
		}
	}
	if gitBuiltins[sub] {
		return []Segment{s}
	}
	for _, e := range s.Env {
		if strings.HasPrefix(e, "GIT_") {
			s.Opaque = "it runs a git command that may be an alias, with git's settings changed by " + strings.SplitN(e, "=", 2)[0]
			return []Segment{s}
		}
	}
	if g.dir == "" || g.otherGit || len(g.configs) > 0 {
		s.Opaque = "it runs git " + sub + ", which may be an alias that can't be looked up here"
		return []Segment{s}
	}
	out, err := gitOut(g.dir, "config", "--get", "alias."+sub)
	alias := strings.TrimSpace(out)
	if err != nil || alias == "" {
		return []Segment{s} // an external git-<sub> program
	}
	rest := joinRest(s, g.sub+1)
	if strings.HasPrefix(alias, "!") {
		return append(s.carrier("git alias"), parseShell(alias[1:]+" "+rest, g.dir, depth+1, "git alias "+sub, pc)...)
	}
	prefix := joinRest(Segment{Words: s.Words[:g.sub], Dyn: s.Dyn[:g.sub]}, 0)
	return append(s.carrier("git alias"), parseShell(prefix+" "+alias+" "+rest, s.Dir, depth+1, "git alias "+sub, pc)...)
}
