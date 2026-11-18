package core

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RepoSetupFile is the committed setup file for personal projects.
const RepoSetupFile = ".plumbline.json"

// Setup is what Plumbline learned about a project. Saved answers are hints:
// commands are re-checked when they fail.
type Setup struct {
	Version     int    `json:"version"`
	Kind        string `json:"kind"` // "company" or "personal"
	BaseBranch  string `json:"base_branch,omitempty"`
	TicketRegex string `json:"ticket_regex,omitempty"` // company commit prefix, default [A-Z][A-Z0-9]+-\d+

	Commands struct {
		Test        string `json:"test,omitempty"`
		TestRelated string `json:"test_related,omitempty"` // may contain {files}
		Lint        string `json:"lint,omitempty"`
		Build       string `json:"build,omitempty"`
		App         string `json:"app,omitempty"`
		DBUp        string `json:"db_up,omitempty"`
		E2E         string `json:"e2e,omitempty"`
		// Optional checks (WF3): when saved, every commit needs them to
		// pass. Format only checks (prettier --check), never rewrites.
		Format    string `json:"format,omitempty"`
		Typecheck string `json:"typecheck,omitempty"`
		Security  string `json:"security,omitempty"`
		// BuilderSetup runs in each new builder copy, so it has its own
		// dependencies (pnpm install --frozen-lockfile --prefer-offline).
		BuilderSetup string `json:"builder_setup,omitempty"`
	} `json:"commands"`
	// Extra commands that may run before approval (for reproducing a bug).
	ExtraAllowed []string `json:"extra_allowed,omitempty"`

	TestsRanPattern string   `json:"tests_ran_pattern,omitempty"` // regex with a (?P<ran>\d+) group
	IgnoreChanges   []string `json:"ignore_changes,omitempty"`    // globs that the changed-files check skips
	RiskyPaths      []string `json:"risky_paths,omitempty"`       // extra globs never touched outside the plan
	// TestTargetsOK: service settings (DATABASE_URL, ...) that point off
	// the laptop but that the tests never connect to, so a test run isn't
	// refused for them. Only the user adds these.
	TestTargetsOK []string `json:"test_targets_ok,omitempty"`
	// TestFiles: globs for test files named in a way Plumbline doesn't know
	// (blind QA writes only test files). Only the user adds these.
	TestFiles    []string `json:"test_files,omitempty"`
	SuiteSeconds int      `json:"suite_seconds,omitempty"`

	FrontendRepo string `json:"frontend_repo,omitempty"`
	DeployNotes  string `json:"deploy_notes,omitempty"`
	// DeployPipeline: a pipeline deploys this project, so Claude never
	// deploys by hand (vercel --prod, sls deploy, kubectl apply, ...).
	DeployPipeline bool     `json:"deploy_pipeline,omitempty"`
	ProdAccess     string   `json:"prod_access,omitempty"` // method only, never values
	FrontendChecks []string `json:"frontend_checks,omitempty"`
	BrandKit       string   `json:"brand_kit,omitempty"`
	Mockups        string   `json:"mockups,omitempty"`
	Glossary       bool     `json:"glossary,omitempty"`

	// MCPReadTools: per MCP server (as its tool names show it), tools that
	// only read although their names don't say so. A name that says write
	// stays a write.
	MCPReadTools map[string][]string `json:"mcp_read_tools,omitempty"`

	// Parts after the core can be switched off one by one.
	Parts map[string]bool `json:"parts,omitempty"`
}

// TicketPattern is the company's ticket id pattern.
func (s *Setup) TicketPattern() string {
	if s == nil || s.TicketRegex == "" {
		return `[A-Z][A-Z0-9]+-\d+`
	}
	return s.TicketRegex
}

// JiraTicket: a company task whose ticket looks like a Jira ticket, with
// the Jira part on.
func (s *Setup) JiraTicket(ticket string) bool {
	if s == nil || s.Kind != "company" || strings.TrimSpace(ticket) == "" || !s.PartOn("jira") {
		return false
	}
	ok, err := regexp.MatchString(`^(`+s.TicketPattern()+`)$`, strings.TrimSpace(ticket))
	return err == nil && ok
}

func (s *Setup) PartOn(name string) bool {
	if s == nil || s.Parts == nil {
		return true
	}
	v, ok := s.Parts[name]
	return !ok || v
}

func laptopSetupPath(root string) string {
	return filepath.Join(ProjectDir(root), "setup.json")
}

// SetupPath returns where this project's setup lives (repo file wins).
// A worktree uses its main checkout's setup.
func SetupPath(root string) (string, bool) {
	p := filepath.Join(ProjectRoot(root), RepoSetupFile)
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	p = laptopSetupPath(root)
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	return "", false
}

func confirmedPath(root string) string {
	return filepath.Join(ProjectDir(root), "setup.confirmed")
}

func fileHash(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// LoadSetup loads the setup and reports whether the current content is the
// one you confirmed. Unconfirmed commands are never run by Plumbline.
func LoadSetup(root string) (*Setup, bool, error) {
	p, ok := SetupPath(root)
	if !ok {
		return nil, false, errors.New("no setup")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	var s Setup
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, false, err
	}
	conf, _ := os.ReadFile(confirmedPath(root))
	ok = strings.TrimSpace(string(conf)) == fileHash(p)
	if !ok {
		// Changed since the user confirmed it (for example by a terminal
		// command): nothing in it may relax a rule until they confirm it
		// again. Every part counts as on, deploys by hand are refused, no
		// risky path the user confirmed is dropped, and the changed-files
		// check skips only what the confirmed copy skipped too.
		s.Parts = nil
		s.DeployPipeline = true
		var was Setup
		if b, err := os.ReadFile(confirmedPath(root) + ".json"); err == nil && json.Unmarshal(b, &was) == nil {
			s.RiskyPaths = uniq(append(s.RiskyPaths, was.RiskyPaths...))
			var both []string
			for _, g := range s.IgnoreChanges {
				if contains(was.IgnoreChanges, g) {
					both = append(both, g)
				}
			}
			s.IgnoreChanges = both
			s.MCPReadTools = bothReadTools(s.MCPReadTools, was.MCPReadTools)
			var ok []string
			for _, k := range s.TestTargetsOK {
				if contains(was.TestTargetsOK, k) {
					ok = append(ok, k)
				}
			}
			s.TestTargetsOK = ok
			var tests []string
			for _, g := range s.TestFiles {
				if contains(was.TestFiles, g) {
					tests = append(tests, g)
				}
			}
			s.TestFiles = tests
			if s.Commands.BuilderSetup != was.Commands.BuilderSetup {
				s.Commands.BuilderSetup = "" // a command runs only as you confirmed it
			}
		} else {
			s.IgnoreChanges = nil
			s.MCPReadTools = nil
			s.TestTargetsOK = nil
			s.TestFiles = nil
			s.Commands.BuilderSetup = ""
		}
	}
	return &s, ok, nil
}

// bothReadTools keeps the read tools listed in both setups.
func bothReadTools(now, was map[string][]string) map[string][]string {
	out := map[string][]string{}
	for server, tools := range now {
		for _, t := range tools {
			if contains(was[server], t) {
				out[server] = append(out[server], t)
			}
		}
	}
	return out
}

// MCPReadTool: the setup lists this tool of this server as a read tool.
func (s *Setup) MCPReadTool(server, name string) bool {
	if s == nil {
		return false
	}
	for k, tools := range s.MCPReadTools {
		if !strings.EqualFold(k, server) {
			continue
		}
		for _, t := range tools {
			if strings.EqualFold(t, name) {
				return true
			}
		}
	}
	return false
}

// ConfirmSetup records the current setup content as approved by you.
func ConfirmSetup(root string) error {
	p, ok := SetupPath(root)
	if !ok {
		return errors.New("no setup")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	// The confirmed copy, so an unconfirmed edit can be held to it (LoadSetup).
	if err := writeFileAtomic(confirmedPath(root)+".json", b); err != nil {
		return err
	}
	return writeFileAtomic(confirmedPath(root), []byte(fileHash(p)+"\n"))
}

// SaveSetup writes the setup (repo file for personal, laptop file for company)
// and confirms it. Only reachable through a confirm prompt.
func SaveSetup(root string, s *Setup) (string, error) {
	if s.Kind != "company" && s.Kind != "personal" {
		return "", errors.New(`kind must be "company" or "personal"`)
	}
	if formatRewrites(s.Commands.Format) {
		return "", errors.New("the format command rewrites files (" + s.Commands.Format + "); save one that only checks and fails when a file isn't formatted: prettier --check ., ruff format --check, black --check ., test -z \"$(gofmt -l .)\"")
	}
	s.Version = 1
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	b = append(b, '\n')
	var p string
	if s.Kind == "personal" {
		p = filepath.Join(ProjectRoot(root), RepoSetupFile)
		_ = os.Remove(laptopSetupPath(root))
	} else {
		p = laptopSetupPath(root)
		_ = os.Remove(filepath.Join(ProjectRoot(root), RepoSetupFile))
	}
	if err := writeFileAtomic(p, b); err != nil {
		return "", err
	}
	return p, ConfirmSetup(root)
}

// formatRewrites: the format command changes the files it checks (prettier
// --write, gofmt -w, black without --check), also when a package runner
// starts it (npx prettier --write). A check that rewrites passes whatever
// the code looks like.
func formatRewrites(cmd string) bool {
	for _, s := range Segments(cmd) {
		if inPlace(s) {
			return true
		}
		if t, ok := runnerTarget(s); ok && inPlace(t) {
			return true
		}
	}
	return false
}

// SavedCommands are the exact commands Claude may run before approval.
func (s *Setup) SavedCommands() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, c := range []string{s.Commands.Test, s.Commands.Lint, s.Commands.Build, s.Commands.App, s.Commands.DBUp, s.Commands.E2E,
		s.Commands.Format, s.Commands.Typecheck, s.Commands.Security} {
		if strings.TrimSpace(c) != "" {
			out = append(out, strings.TrimSpace(c))
		}
	}
	for _, c := range s.ExtraAllowed {
		if strings.TrimSpace(c) != "" {
			out = append(out, strings.TrimSpace(c))
		}
	}
	return out
}
