package core

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// e2eMissing (P4): when the setup saves an e2e command and the change
// reaches the frontend, a passing e2e run on the current code. It reaches
// the frontend when it changes frontend files, or when the plan proves an
// API change and the project has a frontend. `plumbline waive e2e` lets it
// go when it truly can't run.
func e2eMissing(t *Task, s *Setup, fp string, runs []*Run, changed []string) []string {
	if s.Commands.E2E == "" || waived(t, "e2e", fp) {
		return nil
	}
	why := ""
	for _, f := range changed {
		if reUXSignal.MatchString(f) && !reTestFile.MatchString(f) {
			why = "the task changes frontend files (" + f + ")"
			break
		}
	}
	if why == "" && contains(t.ProofKinds, "api") && projectHasFrontend(t.Root, s) {
		why = "the task changes an API in a project with a frontend"
	}
	if why == "" {
		return nil
	}
	v := Judge(LatestFor(runs, t.ID, KindE2E, "", s.Commands.E2E, fp), BaselineFor(runs, t.ID, KindE2E))
	if v.OK {
		return nil
	}
	return []string{fmt.Sprintf("e2e: %s, and %s, so the full-stack test must pass: start the local database, backend and frontend, then plumbline run e2e (if it truly can't run, stop and ask the user, who can waive it)", v.Reason, why)}
}

// projectHasFrontend: the setup names a frontend repo, or this repo holds
// frontend files.
func projectHasFrontend(root string, s *Setup) bool {
	if s.FrontendRepo != "" {
		return true
	}
	out, err := gitOut(root, "ls-files", "-z")
	if err != nil {
		return false
	}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" && reUXSignal.MatchString(p) && !reTestFile.MatchString(p) && !reProse.MatchString(p) {
			return true
		}
	}
	return false
}

// Dependency manifests and lockfiles: a change to one adds, removes or
// upgrades packages.
var reDepFile = regexp.MustCompile(`(?i)^(package\.json|package-lock\.json|npm-shrinkwrap\.json|yarn\.lock|pnpm-lock\.yaml|bun\.lockb?|go\.mod|go\.sum|pyproject\.toml|poetry\.lock|uv\.lock|requirements[\w.-]*\.(txt|in)|pipfile(\.lock)?|gemfile(\.lock)?|composer\.(json|lock)|cargo\.(toml|lock)|mix\.(exs|lock)|pubspec\.(yaml|lock)|podfile(\.lock)?|build\.gradle(\.kts)?|pom\.xml)$`)

// Commands that add or upgrade a project's packages.
var reUpgradeCmd = regexp.MustCompile(`^(npm (i|install|add|update|up|upgrade)|yarn (add|upgrade|up)|pnpm (add|i|install|update|up|upgrade)|bun (add|install|i|update)|pip3? install|uv (add|lock|pip install)|poetry (add|update)|pipenv (install|update)|go get|cargo (add|update)|composer (require|update)|bundle (add|update))\b`)

// upgradePlan: the plan changes packages, by its files or its approved
// commands.
func upgradePlan(pc PlanCheck) bool {
	for _, f := range pc.Files {
		if reDepFile.MatchString(path.Base(cleanRel(f))) {
			return true
		}
	}
	for _, c := range pc.Approved {
		if reUpgradeCmd.MatchString(strings.TrimSpace(c)) {
			return true
		}
	}
	return false
}

// upgradeProblems (N21): a plan that changes packages saves the checks'
// before-state first, so the same commands can be compared after.
func (c *hookCtx) upgradeProblems(pc PlanCheck) []string {
	if !upgradePlan(pc) || c.setup == nil {
		return nil
	}
	runs := LoadRuns(c.task.Root)
	var need []string
	for _, k := range []string{KindTest, KindLint, KindBuild} {
		// Any finished run counts here, even one where everything failed:
		// it records how the checks stood. After a replan the code has
		// changed already, so no run can be a baseline: any finished run
		// of the task is the closest record.
		if savedCommand(c.setup, k) != "" && len(baselineRuns(runs, c.task.ID, k)) == 0 && !(!c.task.LastCodeChange.IsZero() && anyComplete(runs, c.task.ID, k)) {
			need = append(need, "plumbline run "+k)
		}
	}
	if len(need) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("the plan changes packages, so save how the checks stand before any change: run %s now (before the checkpoint), then ask it again", strings.Join(need, ", "))}
}

// anyComplete: the task has a finished run of this kind.
func anyComplete(runs []*Run, taskID, kind string) bool {
	for _, r := range runs {
		if r.TaskID == taskID && r.Kind == kind && r.Complete() {
			return true
		}
	}
	return false
}

// upgradeMissing (N21): after a package change, the report names every
// test file the task changed, so a test edited to fit the upgrade is seen.
func upgradeMissing(t *Task, changes []fileDiff, report string) []string {
	if !t.Upgrade {
		return nil
	}
	var unlisted []string
	for _, f := range changes {
		if reTestFile.MatchString(f.Path) && !strings.Contains(report, f.Path) {
			unlisted = append(unlisted, f.Path)
		}
	}
	if len(unlisted) == 0 {
		return nil
	}
	return []string{"package upgrade: name every test file the task changed in the report, with what changed in it and why: " + listSome(unlisted, 8)}
}
