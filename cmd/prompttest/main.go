package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/lorenzkobe/plumbline/internal/prompttest"
)

func main() {
	if len(os.Args) < 2 {
		fail("usage: prompttest list | fixture <dir> | check --scenario s --log f --repo r --origin o --base b")
	}
	switch os.Args[1] {
	case "list":
		emit(prompttest.Scenarios)
	case "fixture":
		if len(os.Args) != 3 {
			fail("usage: prompttest fixture <dir>")
		}
		f, err := prompttest.MakeFixture(os.Args[2])
		if err != nil {
			fail(err.Error())
		}
		emit(f)
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		name := fs.String("scenario", "", "")
		logPath := fs.String("log", "", "")
		var env prompttest.Env
		fs.StringVar(&env.Repo, "repo", "", "")
		fs.StringVar(&env.Origin, "origin", "", "")
		fs.StringVar(&env.Base, "base", "", "")
		_ = fs.Parse(os.Args[2:])
		s, ok := prompttest.Find(*name)
		if !ok {
			fail("no scenario " + *name)
		}
		f, err := os.Open(*logPath)
		if err != nil {
			fail(err.Error())
		}
		l, err := prompttest.Parse(f)
		f.Close()
		if err != nil {
			fail(err.Error())
		}
		bad := 0
		for _, r := range s.Check(l, env) {
			mark := "PASS"
			if !r.OK {
				mark, bad = "FAIL", bad+1
			}
			line := fmt.Sprintf("  %s  %s", mark, r.Check)
			if r.Detail != "" {
				line += ": " + r.Detail
			}
			fmt.Println(line)
		}
		refused := prompttest.Refusals(l)
		fmt.Printf("  info  %d Plumbline refusals, %d calls, %d turns, $%.2f, ended %s\n", len(refused), len(l.Calls), l.Turns, l.Cost, l.Outcome)
		for _, r := range refused {
			fmt.Println("        - " + r)
		}
		if bad > 0 {
			os.Exit(1)
		}
	default:
		fail("unknown command " + os.Args[1])
	}
}

func emit(v any) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fail(err.Error())
	}
	fmt.Println(string(b))
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "prompttest:", msg)
	os.Exit(2)
}
