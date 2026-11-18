package core

import "strings"

// rtk (Rust Token Killer) runs a command and trims its output before Claude
// reads it. Its hook rewrites the lead's commands (git status runs as rtk git
// status), and its instructions make helpers type rtk themselves, so what rtk
// runs is judged, not rtk (RTK1).

var (
	// rtkNative: subcommands named after the program they run, with the
	// same arguments (rtk git diff runs git diff).
	rtkNative = set("ls tree git gh glab aws psql pnpm find diff docker kubectl oc grep rg wget wc cargo npm npx bun bunx curl go jest vitest ctest prisma tsc next prettier playwright ruff pytest mypy pip uv deno dotnet php phpunit phpstan pest paratest ecs pint rake rubocop rspec sbt gt golangci-lint gradlew mvn mvnd")
	// rtkRenamed: subcommands that run a program by another name.
	rtkRenamed = map[string]string{"lint": "eslint"}
	// rtkRunners: subcommands that run the rest of their arguments as a
	// command (rtk proxy git push runs git push).
	rtkRunners = set("proxy err test summary")
	// rtkReads: rtk's own subcommands that only show things.
	rtkReads = set("gain discover session cc-economics help deps rewrite --help -h --version -V")
	// rtkGlobal: options rtk takes before the subcommand, or right after it,
	// none with a value.
	rtkGlobal = set("-v -vv -vvv --verbose --ultra-compact --skip-env")
	// rtkReadVal: rtk read's options that take a value.
	rtkReadVal = set("-l --level -m --max-lines --tail-lines")
)

// rtkSub: the index of rtk's subcommand, past its own options.
func rtkSub(s Segment) int {
	i := 1
	for i < len(s.Words) && !s.dyn(i) && rtkGlobal[s.Words[i]] {
		i++
	}
	return i
}

// rtkReadOnly: rtk's own subcommands that only show things.
func rtkReadOnly(s Segment) bool {
	i := rtkSub(s)
	return i < len(s.Words) && !s.dyn(i) && rtkReads[s.Words[i]]
}

// unwrapRtk replaces rtk with the command it runs. A subcommand that isn't
// known stays rtk, which counts as a change.
func unwrapRtk(s Segment, depth int, pc *parseCtx) []Segment {
	i := rtkSub(s)
	if i >= len(s.Words) {
		return []Segment{s}
	}
	if s.dyn(i) {
		s.Opaque = "rtk's subcommand comes from a variable or substitution (" + s.Words[i] + ")"
		return []Segment{s}
	}
	sub := s.Words[i]
	rest := i + 1
	for rest < len(s.Words) && !s.dyn(rest) && rtkGlobal[s.Words[rest]] {
		rest++
	}
	switch {
	case rtkNative[sub], rtkRenamed[sub] != "":
		// the program's own options follow, so none are taken as rtk's
		in := s.tail(i, "rtk "+sub)
		in.Words[0], in.Dyn[0], in.Glob[0] = sub, false, false
		if n := rtkRenamed[sub]; n != "" {
			in.Words[0] = n
		}
		in.src = nil // the program's name is now a known word, not the one written
		return unwrap(in, depth+1, pc)
	case rtkRunners[sub]:
		if rest >= len(s.Words) {
			return []Segment{s}
		}
		in := s.tail(rest, "rtk "+sub)
		if in.dyn(0) || in.glob(0) {
			in.Opaque = "the command name comes from a variable, substitution or pattern (" + in.Words[0] + ")"
			return []Segment{in}
		}
		return unwrap(in, depth+1, pc)
	case sub == "run":
		// rtk run -c 'cmd' or rtk run cmd args: through sh -c
		text := []string{}
		for j := rest; j < len(s.Words); j++ {
			if s.dyn(j) {
				s.Opaque = "rtk run runs text that isn't known until it runs"
				return []Segment{s}
			}
			w := s.Words[j]
			switch {
			case w == "-c" || w == "--command":
				continue
			case strings.HasPrefix(w, "--command="):
				w = strings.TrimPrefix(w, "--command=")
			}
			text = append(text, w)
		}
		if len(text) == 0 {
			return []Segment{s}
		}
		return append(s.carrier("rtk run"), parseShell(strings.Join(text, " "), s.Dir, depth+1, "rtk run", pc)...)
	case sub == "read":
		// rtk read [options] files: reads them like cat
		in := s.tail(rest, "rtk read")
		files := Segment{Words: []string{"cat"}, Dyn: []bool{false}, Glob: []bool{false}}
		for j := 0; j < len(in.Words); j++ {
			w := in.Words[j]
			switch {
			case !in.dyn(j) && rtkReadVal[w]:
				j++ // and its value
			case !in.dyn(j) && strings.HasPrefix(w, "-") && w != "-":
			default:
				files.Words = append(files.Words, w)
				files.Dyn = append(files.Dyn, in.dyn(j))
				files.Glob = append(files.Glob, in.glob(j))
			}
		}
		out := in
		out.Words, out.Dyn, out.Glob, out.src = files.Words, files.Dyn, files.Glob, nil
		return []Segment{out}
	}
	return []Segment{s}
}
