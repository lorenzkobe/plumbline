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
	} `json:"commands"`
	// Extra commands that may run before approval (for reproducing a bug).
	ExtraAllowed []string `json:"extra_allowed,omitempty"`

	TestsRanPattern string   `json:"tests_ran_pattern,omitempty"` // regex with a (?P<ran>\d+) group
	IgnoreChanges   []string `json:"ignore_changes,omitempty"`    // globs that the changed-files check skips
	RiskyPaths      []string `json:"risky_paths,omitempty"`       // extra globs never touched outside the plan
	SuiteSeconds    int      `json:"suite_seconds,omitempty"`

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
		// again. Every part counts as on, and deploys by hand are refused.
		s.Parts = nil
		s.DeployPipeline = true
	}
	return &s, ok, nil
}

// ConfirmSetup records the current setup content as approved by you.
func ConfirmSetup(root string) error {
	p, ok := SetupPath(root)
	if !ok {
		return errors.New("no setup")
	}
	return writeFileAtomic(confirmedPath(root), []byte(fileHash(p)+"\n"))
}

// SaveSetup writes the setup (repo file for personal, laptop file for company)
// and confirms it. Only reachable through a confirm prompt.
func SaveSetup(root string, s *Setup) (string, error) {
	if s.Kind != "company" && s.Kind != "personal" {
		return "", errors.New(`kind must be "company" or "personal"`)
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

// SavedCommands are the exact commands Claude may run before approval.
func (s *Setup) SavedCommands() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, c := range []string{s.Commands.Test, s.Commands.Lint, s.Commands.Build, s.Commands.App, s.Commands.DBUp, s.Commands.E2E} {
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
