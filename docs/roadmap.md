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
- Release 3d, second part: 0.6.7 (commit 67c9126): W1, M15 (found while building it).
- Release 3d, third part: 0.6.8 (commit cfb9d24): the sandbox option (G1, P1, B1, each partly; the leftovers are in known-gaps.md).
- Release 3d, fourth part: 0.6.9 (commit 1ed00e8): the standup (S1, S4; S2 and S3 dropped).
- Release 4: 0.6.10 (commit 1a6630b): WF3, WF4.
- The open items: 0.6.11: M16, T1, W3; W2 dropped.

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

## Decisions for release 3d, sandbox (2026-11-18)
- From Claude Code's docs (checked 2026-11-18): hooks, file tools and MCP servers run outside the sandbox; sandboxed commands write only the working folder and temp; `excludedCommands` uses Bash rule syntax, and every command in a line must match (a pipe, redirect, `cd` or `$(...)` keeps it sandboxed); `.claude/settings.local.json` can set `enabled`, `excludedCommands` and `allowUnsandboxedCommands`, and Claude Code's `/sandbox` panel writes there.
- Found while planning: with the recorded settings, Plumbline's own commands couldn't save proof (they run sandboxed and need the key and their folder). Chosen: exclude `plumbline *`, so code those commands start stays outside the fence (documented). Not chosen: sealing runs in the hooks (rebuilds every proof record for a threat Docker leaves open anyway).
- `plumbline setup sandbox [--exclude <client>]` writes the settings for one checkout, behind a confirm prompt, with undo; not laptop-wide (the friction should land only where chosen). The whole data folder is fenced (`denyRead` and `denyWrite`), not only the key, which also keeps helpers' notes from sandboxed commands. `--exclude` refuses shells, interpreters and command runners.
- With the sandbox off, nothing changes.

## Decisions for release 3d, standup (2026-11-18)
- The standup is what was done since the user's last standup in that project, up to now, from MemPalace, git and Plumbline's tasks. The first one starts at the beginning of yesterday; one asked again within 12 hours keeps its start; it goes back 7 days at most. A named day (`--date`) still works and doesn't move the start.
- S2 and S3 are dropped (no list of extra repos, no following branches in personal projects). Company tickets are still followed into the repos they were already followed into.
- S4: a change to the sprint file that no prompt named is reported to Claude, to tell the user, not put back.

## Next: release 3d, bigger pieces (each its own release)
- [x] M14: two sessions in one repo (0.6.6).
- [x] W1: the hotfix flow (0.6.7), with M15.
- [x] The sandbox option: G1, P1, B1 (0.6.8, each partly).
- [x] The standup: S1, S4 (0.6.9); S2 and S3 dropped.

## Decisions for release 4 (2026-11-18)
- WF4, missing failing run: the finish check sends Claude back to show it (stash the fix, run the test, pop it); only the user can waive it (`plumbline waive bug-test`).
- WF4, hotfixes: the same rule, checked at the hotfix's finish; its first commit still goes out fast.
- WF3, hotfixes: the first commit needs every saved check, format, typecheck and security included.
- WF4, a test edited after its failing run: the run no longer counts (a stand-in test that always fails would pass otherwise); Claude sets the fix aside and runs it again.
- Found while building it: a hotfix switches to the production branch after its checkpoint, so the approval fingerprint now follows a move the start commit follows, in a line of only git checkout, switch, pull or fetch, while only tests had changed (else no failing run could count). Blind QA uses the same fingerprint, so it can start after such a move too.
- Found while building it: `ruff format --check` was read as a command that rewrites files; it only checks.

## Release 4 (0.6.10), shipped
- [x] WF3: optional saved `format`, `typecheck` and `security` commands.
- [x] WF4: a `[bug]` case needs a recorded failing run of its named test, taken while only test files had changed.

## Next: fixes from the first smoke run (0.6.12), before the rest of release 5
The smoke run (2026-11-18) passed steps 1, 2, 3, 6, 6b and 7, mostly passed 4, and found the bugs below. Evidence, logs and file:line for each id: [smoke-run-1.md](smoke-run-1.md). Test setup to reuse: `~/Documents/PersonalDevProjects/kwenta-smoke` (copy of kwenta-web; origin is the local bare `kwenta-smoke-remote.git`), sessions started with `PLUMBLINE_DEBUG=1 claude --plugin-dir <this repo> --debug hooks --debug-file ../kwenta-smoke-logs/stepN.log`; Plumbline is not installed.

Decided with the user:
- Fix these now, then retest steps 3 and 5 and carry on from step 8 (rather than finish every step first).
- Outside a task, Plumbline stops asking for ordinary commands and leaves them to Claude Code's own mode (auto mode). It still asks for its fixed list (installs, MCP and Claude Code settings changes, production and cloud, secret reads, setup save, laptop and sandbox), and PostToolUse still stops the work when a command changes repo files before a plan. Step 3's `touch foo.txt` check then expects no prompt, then the stop naming the file.
- Skip ui-ux-pro-max for now (only `/plumbline:design` uses it).

To fix (each with a test that fails first):
- [x] TB1 (0.6.12): `plumbline task start ... && <reads>` never binds the task to the session (posttool.go `onlyPlumbline`); the deny then says "no task is active". Bind when the rest of the line only reads, point the deny at `plumbline task adopt <id>` when the branch has an open unbound task, and tell the task skill to start on its own line.
- [x] SA1 (0.6.12): SubagentStop `decision: block` doesn't send a background helper back (it already handed back with `SubagentHandback`). Check the findings file when the helper calls `SubagentHandback` (PreToolUse deny with the reason); keep SubagentStop as the fallback. Check Claude Code's docs first.
- [x] RTK1 (0.6.12): treat `rtk <cmd>` and `rtk proxy <cmd>` as wrappers and judge the inner command (helpers' read-only rule, the lead's gate, proof matching); `rtk gain`/`discover` only read.
- [x] CM1 (0.6.12): `plumbline task finish` refuses while the task's own files are changed but not committed (commit first, then finish). Decided with the user: `--uncommitted` (behind a prompt) finishes anyway when the user said they'll commit themselves.
- [x] SEC1 (0.6.12): a saved check that already failed before the task has no baseline unless Claude ran it before the first edit. Decided with the user: not a temporary copy (it has no node_modules, so lint, typecheck and build would fail there by the hundreds and excuse new problems); instead the first change waits until every saved check has a run on the code as approved.
- [x] SR1 (0.6.12): setup adds Serena with `--agent-interface tools` (Serena 2.x defaults to REPL: one `serena_repl` tool Plumbline can't read); the deny for `serena_repl` names the flag.
- [x] SR2 (0.6.12): Serena writes `.serena/` into each repo on first start; setup puts it in `ignore_changes`.
- [x] SR8 (0.6.12): an `ignore_changes` glob matching a git-ignored path makes every `plumbline run` fail in the fingerprint's `git add` (git.go:56-62); skip such globs, and setup rejects them.
- [x] (0.6.12) Outside-a-task prompts (the decision above), and setup saves once at the end (SR5).
- [x] CFG1 (0.6.12): `.plumbline.json` leaves the code fingerprint, so a setup change doesn't make proof stale.
- [x] IND1 (0.6.12): the helper-prompt independence check ignores phrases that are also in the task title, understanding.md or plan.md.
- [x] (0.6.12) Smaller: SR3 (setup says how to learn the laptop state instead of reading settings.json), SR4 (laptop prompt says it changes Claude Code settings), SR6 (setup offers doc fixes as a task, not edits), SR7 (setup explains a tracked `.claude/settings.local.json` before the sandbox), SR10 (rules: every change starts by asking to open a task), L6 (drop the `"Q"="A"` string fallback: the hook gets a structured `answers` map), the stale "paused"/step note on a plumbline command message, read-only `awk` for helpers.
- [x] (0.6.12) smoke-test.md: hook time is ~30-40 ms, not 5 ms; step 6b's variable check uses `CMD=$(cat f); $CMD`; step 3's touch check per the decision above; finish comes after the commit in step 5.

## Fixes from the second smoke run (0.6.13)
The retest of step 3 (2026-11-18, session 86757349) failed again on binding. Evidence: [smoke-run-2.md](smoke-run-2.md). Decided with the user: fix it before going on, then restart Claude and redo step 3.
- [x] TB2 (0.6.13): `plumbline task start ... 2>&1 | tail -30` and `plumbline task adopt <id> | tail -20` never bound the task: a piped `tail` failed both the start's read list and adopt's plumbline-only rule, and the adopt the user approved did nothing, silently. `head`/`tail` reading only the pipe, with at most a count, may now follow either; any other stage still binds nothing, and when a real start or adopt doesn't bind, the post hook tells Claude to run adopt on its own line.
- [x] CC1 (0.6.14): after the user's own `! git commit`, the next read-only command stopped the work ("a terminal command changed files before any plan was approved"): `Changed` counted every file leaving git status as reverted. A file whose stamp is unchanged was committed, not changed.
- [x] HR1 (0.6.15): reviewers refused for `ls <notes folder>` (names only); allow it. Also the helper's shell-write refusal says "cat changes something": say to use Edit or Write for findings.
- [x] FR1 (0.6.15): lookups made in the same session just before `task start` don't count for the checkpoint, so Claude searches twice.
- [x] CM2 (0.6.15): Claude picked `finish --uncommitted` without the user saying they'd commit; the rules should make it ask.
- [x] FR2 (0.6.15): `brew --prefix` (and list, info, deps, leaves, config, --version) only read.
- [x] RP1 (0.6.16): `plumbline <subcommand> --help` ran the subcommand (a replan in the retest). Now usage only.
- [x] PL1 (0.6.16): "test: does not apply, because ..." read as a test file named "does".
- [ ] UX1 / UX2 (decided 2026-11-19: before steps 9-18): too much ceremony for small changes, and Plumbline should work under the hood with plain, well-laid-out messages for the user.
- [x] SR10 again (0.6.15): Claude tried the edit before asking 3 of 3 times (the gate held). The per-message note and the rules now lead with "your first step is to ask" and "don't try the edit first".

## Release 5, last
- The smoke test, V1 to V20 (docs/smoke-test.md).
- The items that depend on it: R2 (after V11), B6 (after V7 and V9). L6 is done (0.6.12, after V1 in smoke run 1).
- N11: the real-session prompt test script, run by hand.

## The open items (0.6.11), shipped before release 5
- [x] M16: the baseline is retaken on the code a followed branch switch or pull lands on, while nothing changed since approval.
- [x] T1: staticcheck is clean. Run it with `GOTOOLCHAIN=go1.27.4 go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (the `go` on the PATH is 1.23, and its automatic switch picks a toolchain too old for Go 1.27's standard library).
- [x] W3: the setup asks for the project's own risky files.
- Dropped: W2 (bundled servers get other tool names, and would run beside the user's own), P2, P7.
