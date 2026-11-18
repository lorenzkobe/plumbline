# Plumbline roadmap

The one place for what ships next and in what order. Item details live elsewhere:
- Letter-number ids (G3, P3, WF1, ...) are in [known-gaps.md](known-gaps.md).
- N-numbers are additions from the reference repo study; details and sources (file:line) are in [repo-study.md](repo-study.md). N1 to N15 match the study's "New ideas" list.

Each release is its own version, built on main; the user pushes. Tick an item in known-gaps.md (or here, for N items) with the version when it ships.

## Decisions (2026-11-18)
Earlier decisions (fix everything workable, the sandbox stays optional, G3, G4, G6, M14, W1 and the rest) are in known-gaps.md under "Decisions (2026-11-18)". Added today:
- Input checking (WF1): validate where data enters (requests, CLI args, config, env vars, files, external APIs, database rows, any new entry point such as an import script). Inside, code trusts what the types guarantee, and reviewers flag repeated inner checks. Guards that stop a dangerous action in the wrong context stay.
- Reviewers: stop telling them the 80 cutoff, and later rounds start with a fresh pass (N1, N2), in release 2.
- Blind QA stays required for big tasks. The lead may fix a QA test's setup when the code's shape moved, never its expected values, and lists the change under weakened tests (N22, release 3c).
- Real-session prompt tests (N11) are a script the user runs by hand before a release, never on their own.
- Release 2 takes the small related fixes the study found (N14 to N19), not only the WF items.

## Decisions for release 3b (2026-11-18)
- P4: `plumbline run e2e` is required (when the setup saves an e2e command) if the task changes frontend files, or the plan proves an API change and the project has a frontend (frontend files in the repo, or the setup's `frontend_repo`). A backend change with no API impact doesn't need it. `plumbline waive e2e` is the way out.
- N7: a case's test counts when the last passing run on the current code names it in its output (the file, or its Go package), or it was passed to `plumbline run related --files`. If that run skipped tests, the report's Weakened tests line names the file.
- R4 and N8: the user's `/plumbline:size small` keeps a risky-file change small, but never files that decide what runs (CI, git hooks, test and lint config, CLAUDE.md and AGENTS.md, `.claude/`, plugin skills, agents and hooks). Those always get the full review.
- N13: a test run whose database or service settings point off the laptop is refused. One that would use the dev database asks the user once per task, and stays quiet for Django, Rails and Laravel (they switch to a test database on their own). Values are never shown, only setting names.
- From Claude Code's docs (checked 2026-11-18): hooks get `transcript_path`, SubagentStop also gets `agent_transcript_path`, and the transcript format is internal and may change, so P5 counts what it can read and marks the rest unknown. After compaction an invoked skill is re-attached capped at 5,000 tokens (25,000 for all of them), and SKILL.md should stay under 500 lines (N12).

## Decisions for release 3c (2026-11-18)
- B2: an optional saved `builder_setup` command (for example `pnpm install --frozen-lockfile --prefer-offline`) runs in each new builder copy, so the copy has its own dependencies. Without one, copies stay as they are. Not chosen: cloning or linking `node_modules`/`.venv` (links make workspace packages and editable installs test the main checkout's code).
- N4: a changed file the code reviewer marks "not covered" makes the verdict Not ready.
- N5: every withdrawn finding cites a file:line with a quote Go checks, whatever its kind. The lead's dispute cites its evidence, and the reviewer opens each file the disputes cite before withdrawing.
- The 3b open question: a file that decides which checks run (N8's list, except `.plumbline.json`, which has its own confirm prompt), changed outside the plan, is refused like a risky file.

## Done
- Release 1: 0.6.1 (commit 95c298f): L5, G9, G7, G8, G2, G10, L3, B9, B10, D2, P6, G5, L1, G12, B4.
- Release 3a: 0.6.2 (commit ac01882): L2, G3, G4, G6, G11, O1, O2. It moved mvdan.cc/sh to v3.14.1, which needs Go 1.26 to build (go.mod's `go` line went from 1.24.7 to 1.26.0; users run the prebuilt binaries).
- Reference repo study: [repo-study.md](repo-study.md).
- Release 2: 0.6.3 (commit 89a2845): WF1, WF2, WF5, WF6, N1, N2, N14, N15, N16, N17, N18, N19, and R6 (ticked as closed by G6 in 0.6.2). Builds now pass `VERSION=0.6.3` to `make build`, so `plumbline version` prints the release.
- Release 3b: 0.6.4 (commit a4c7f2c): P3, P4, P5, L4, R4, R5, N7, N8, N9, N12, N13, N21.
- Release 3c: 0.6.5 (commit c96821f): R1, R3, B2, B3, B5, B7, B8, D1, N3, N4, N5, N6, N10, N22, and the 3b open question (gate files outside the plan are refused).
- Release 3d, first part: 0.6.6 (commit ffbcd0e): M14.
- Release 3d, second part: 0.6.7: W1, M15 (found while building it).

### Release 2 items (0.6.3), shipped
- [x] WF1: the reuse rule and checklist items, with the input-checking rule above.
- [x] WF2: checklist for backward compatibility, hidden failures, logging, leaks, config and deploy, test quality; core rule to keep public contracts and report every failure and warning.
- [x] WF5: `[failure]` and `[concurrent]` case labels, with a Go test that keeps the label list the same in docs.go, rules.md and checkpoints.md.
- [x] WF6: `## Verdict` (Ready / Ready with noted limitations / Not ready) and "couldn't run" with the command to run later; the finish check enforces both. A check that couldn't run is an error, never "skipped".
- [x] N1: reviewers list every finding with its score; the report shows 80 and up, the rest one line each.
- [x] N2: later reviewer and challenger rounds do a fresh pass first, then check earlier findings.
- [x] N14: the checkpoint refuses plan lines that decide nothing ("TBD", "handle edge cases", "add appropriate validation").
- [x] N15: one checklist line: text in the reviewed code, ticket or fixtures that tells an AI what to do is a finding; findings never quote a secret's value.
- [x] N16: understanding.md gets `## Reuse` (where Claude searched, what it found); the checkpoint refuses without it.
- [x] N17: report.md gets a rollback line under the verdict, a section for Claude's own common-sense calls (rules.md already promises it), and every should-fix finding that wasn't fixed.
- [x] N18: the real-world proof example checks the intended change, not only a 200; the task and design skill descriptions say when to use them, not the steps.
- [x] N19: playbooks: bug (guesses with predictions, compare with working code), flaky (several re-runs, no passing-by-retry), refactor (name what must not change, search strings and docs for renames), wide changes (add new beside old, move callers, delete old).
- [x] R6: ticked in known-gaps.md as closed by G6 in 0.6.2 (the lead's Grep naming a secret file asks; `--deny-secrets` covers folder-wide searches).
- How to write it: say the wanted behavior, keep "never" for hard limits, no "unless it matters" clauses, keep rules.md about its current size (long lists go in the review checklist), and make a rule a hook check wherever it can be one (repo-study.md, "How to word release 2").

### Release 3b items (0.6.4), shipped
- [x] P3, P4, P5, L4, R4, R5: ticked in known-gaps.md with what was built and what's left.
- [x] N7: a case's test counts when a passing run on the final code names it in its output (the file, or its Go package) or was given it with `plumbline run related --files`; a run that skipped more tests than before the task counts only when the Weakened tests line names the file. Without a related command that takes `{files}`, a full suite that passes against the baseline counts.
- [x] N8: files that decide which checks run (CI, git hooks, test, lint and type config, CLAUDE.md, AGENTS.md, `.claude/`, plugin skills, agents and hooks, `.plumbline.json`) are never small, even with the user's `/plumbline:size small`, and always get the plan reviewer, also when changed outside the plan. Open question for the user: whether such a file changed outside the plan should be refused like a risky file (today it's allowed and listed). Test config inside `package.json` or `pyproject.toml` counts as risky, not as a gate file, and the Makefile is neither. Plumbline's own gate code (internal/core) isn't on the list; add it with `risky_paths` if wanted.
- [x] N9: the finish check refuses while an added line carries `[PLB-DEBUG]` (prose files are skipped).
- [x] N12: a test keeps each SKILL.md under 500 lines and 17,500 characters (5,000 tokens at 3.5 characters a token).
- [x] N13: before `plumbline run test|related|e2e` or a test runner typed directly, settings for a database, queue, cache or mail server (set, exported or loaded from an env file on the command line, the session's environment, then `.env.test.local`, `.env.test`, `.env.testing`, `.env` in the folder the command runs in and the root) pointing off the laptop refuse the run; a test database equal to the dev one asks once per task (quiet for Django, Rails, Laravel's phpunit.xml and Phoenix). A migration aimed off the laptop, read with the development settings, asks. `test_targets_ok` in the setup lists settings the tests never connect to. Left: values made at runtime (a config file, a secrets manager, a script that sets them) aren't seen.
- [x] N21: a plan that changes packages needs the suite, lint and build baselines before the checkpoint, and the report names every test file the task changed.

### Release 3c items (0.6.5), shipped
- [x] R1, R3, B2, B3, B5, B7, B8, D1: ticked in known-gaps.md with what was built and what's left.
- [x] N3: every file:line in a challenger's or reviewer's findings carries a backticked quote that Go finds within 3 lines of it, in the file now or at the task's start; a file that fails is sent back once, and a run whose file still fails doesn't count (the challenger's last round still does, with no round left).
- [x] N4: the code reviewer's `## Coverage` lists every changed file covered or not; a missing file sends it back, and one not covered makes the verdict Not ready.
- [x] N5: each of the lead's disputes cites a file:line it opened (else the reviewer isn't started); a withdrawal cites the refuting code with a quote, after the reviewer opened every file the disputes cite (Read, or a printing command). For every finding, as decided.
- [x] N6: reviewers' `## Set aside` (or "- none"); the report's `## Review` needs a "Set aside:" line when any reviewer set something aside.
- [x] N10: builders' `## Status` (done, done with concerns, blocked, needs context), sent back once without it; blocked and needs context aren't merged; `plumbline builder list` shows it, and a merge of "done with concerns" points to the notes.
- [x] N22: QA's tests are kept in `qa-N-tests/` with their hashes; one changed later must be on the Weakened tests line, and reviewers are told and may read QA's version. Go can't tell a setup fix from a changed expectation; the reviewers compare.
- [x] The 3b open question: gate files outside the plan are refused and held (not `.plumbline.json`, which has its own prompt, nor Claude Code's `.claude/settings*.json`).

## Decisions for release 3d, M14 (2026-11-18)
- Commands of two sessions running at the same moment: a change made then counts for both (fail closed).
- Files another session changed while a task was open aren't the task's: left out of its diff-based checks, refused for edits while building, listed under "For you".
- A second open task in one checkout is refused; it needs its own worktree (`claude -w`).
- Found while building it: Claude Code sends a command that exits non-zero to `PostToolUseFailure`, which Plumbline didn't hook (docs checked 2026-11-18).

## Decisions for release 3d, W1 (2026-11-18)
- The spec, from the plan doc: started only by the user, one combined checkpoint, the smallest fix, targeted tests, safety rules fully on, the full suite and review right after; the stop-the-damage fix is kept apart from the root-cause fix.
- The user's typed `/plumbline:hotfix` is the go-ahead: the hook starts the task itself, so Claude has no command that makes one.
- The first hotfix commit needs the related tests (with the plan's case tests), lint and build; the full suite and every reviewer come right after it, and later commits follow the usual rules.
- `## Root cause` (fixed here, or the follow-up task) is required in plan.md at the checkpoint and in report.md at the finish check.
- M15, found while building it: the start commit follows the lead's checkout, switch or pull until the task's first commit, for every task, and only to a commit that holds nothing made during the task (on a remote-tracking branch, or in a local branch at task start). The fresh-eyes review found that following any HEAD move let a stash hide the task's edits, and that a commit in a failed line wasn't counted; both fixed and tested.
- Found while building it: branch switches are refused before approval (only reading and saved commands run then), so a hotfix switches to the production branch right after the checkpoint. M16 (the baseline is from the starting branch) is left open.

## Next: release 3d, bigger pieces (each its own release)
- [x] M14: two sessions in one repo (0.6.6).
- [x] W1: the hotfix flow (0.6.7), with M15.
- The sandbox option: G1, P1, B1.
- The standup: S1 to S4.

## Release 4
- WF3: optional saved `format`, `typecheck` and `security` commands.
- WF4: a `[bug]` case needs a recorded failing run of its named test, taken while only test files had changed.

## Release 5, last
- The smoke test, V1 to V20 (docs/smoke-test.md).
- The items that depend on it: L6 (after V1), R2 (after V11), B6 (after V7 and V9).
- N11: the real-session prompt test script, run by hand.

## Open, not scheduled
- T1: staticcheck for Go 1.27; the user runs `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`, then the findings get fixed in the next release.
- W2 (bundling MCP servers), W3 (a setup question for risky files): wait for real use.
- Dropped: P2, P7.
