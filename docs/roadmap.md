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
The smoke run (2026-11-18) passed steps 1, 2, 3, 6, 6b and 7, mostly passed 4, and found the bugs below. Evidence and file:line for each id: [smoke-run-1.md](smoke-run-1.md). The test setup (`kwenta-smoke`, a copy of kwenta-web with a local bare remote, sessions started with `PLUMBLINE_DEBUG=1 claude --plugin-dir <this repo> --debug hooks --debug-file <log>`) and its logs were deleted on 2026-11-23; a new run starts from [smoke-test.md](smoke-test.md).

Decided with the user:
- Fix these now, then retest steps 3 and 5 and carry on from step 8 (rather than finish every step first).
- Outside a task, Plumbline stops asking for ordinary commands and leaves them to Claude Code's own mode (auto mode). (Changed 2026-11-19 after step 10b, user's choice: a command that plainly writes a project file is now refused with no task open, like an edit.) It still asks for its fixed list (installs, MCP and Claude Code settings changes, production and cloud, secret reads, setup save, laptop and sandbox), and PostToolUse still stops the work when a command changes repo files before a plan. Step 3's `touch foo.txt` check then expects no prompt, then the stop naming the file.
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
- [x] UX1 / UX2 (0.7.0; decided 2026-11-19: before steps 9-18)
- [x] (0.7.1) The user asked to see the plan before approving, in plain words, with detail to dig into: plan-for-you.md for normal and big tasks (a file on the laptop, chosen over a claude.ai page to avoid clutter; task notes live outside the repo and are kept as history, no cleanup).
  Decisions (user, 2026-11-19):
  - A small change gets a short plan, the checks and one code review (one round). No challenger, no plan reviewer.
  - Risky-path files (setup's risky_paths, like people.ts): Claude decides small or full and says why. Floor confirmed by the user: built-in high-impact kinds (auth, permissions, payments, migrations, deploy, dependencies, security code) and files that decide which checks run stay never-small.
  - A small change stops once: Claude researches first, then one question with the plan; "Approve" starts and approves the task.
  - What the user sees: conversational cards. A plain sentence first, then a few short detail lines (risk, checks, review), then the question. No task ids, notes paths or plumbline commands.
  - Under the hood: tell Claude each step's needs up front (checklist at start, one command for every check, refusal texts that name the right tool) so refusals are rare.: too much ceremony for small changes, and Plumbline should work under the hood with plain, well-laid-out messages for the user.
- [x] (0.7.2) From the 0.7.1 retest, step A (smoke-run-2.md): SZ1 (no size at start meant normal, so the small path never ran), ENV1 (a grep `--include=*` filter read as .env), the lead's notes from the terminal, and one commit stop after "Commit it". All four chosen by the user, 2026-11-19. Then from the redo of step A: RD1 (reads in a failed line or just before the start didn't count), CF1 (commit question before the task was ready), PR1 (a unit test accepted as the real-app proof), and the user's ask for suggestions on the plan card (related vs not related). CT1 (a full pass counting for case tests) explained, not built. And the user's ask for a task goal so Claude doesn't drift: ## Goal line, checked by reviewers and the report ("Goal + checks", chosen 2026-11-19). From step B: PL2, RD2 (search results count for files the plan only mentions; files it changes must be opened), never-small server-load changes plus a cost line for reviewers, and short messages (no line limit, "only the important stuff"), all chosen 2026-11-19.
- [x] SR10 again (0.6.15): Claude tried the edit before asking 3 of 3 times (the gate held). The per-message note and the rules now lead with "your first step is to ask" and "don't try the edit first".
- [x] CTX1 (0.7.2; built as three SessionStart parts, each under the limit, instead of moving rules to skills; found 2026-11-19): the session-start text is about 20 KB (rules.md alone is 19,993 bytes). Claude Code keeps hook context over about 10 KB in a file and shows Claude only a 2 KB preview, so Claude sees rules 1-9 and misses the rest, plus the "Right now" part (open tasks, setup state, Docker) that session.go writes after the rules. Claude opened the full file in 8 of 24 kwenta-smoke sessions. Likely cause of run 2's "Claude hadn't raised the open task itself". Fix: "Right now" first, the rules under 10 KB, details moved to the skills that use them. Evidence: [smoke-run-2.md](smoke-run-2.md) "Token use and the cut-off rules".
- [x] From step 12a (0.7.2, chosen 2026-11-20): PLAN3 (plain plan in chat, Title Case, technical details stay in plan.md), HID2 (the plan question repeats the gist), PKG1 (installs outside the project aren't package changes), CASE1 (plan rules checked on every save of plan.md), SCR1 (scratchpad-only runs aren't refused before approval). Evidence: smoke-run-2.md "Step 12a".
- [x] From step 12b (0.7.2, chosen 2026-11-20): DP1 (in a pipeline project, a message about deploying carries a reminder of how it ships, so Claude doesn't offer a hand deploy), DP2 (the reminder covers previews and test deploys too). From step 12c: VER1 (a cloud or database tool's version or help doesn't ask), RD3 (a read on the same line as Plumbline's bookkeeping counts), ASK1 (prompts say what the command is for and the risk in plain words), READ2 (looks run freely, planning too; secret printers ask). From step 13: BLD3 (simple builder parts start on Sonnet). From step 14a: FL1 (a failed run names its failing tests in its last line). From step 14c: LIVE1 (a second window is told another window works here; no offer to close or take over a live task). From step 16a: SB1 (untracking a Claude settings file asks; setup hands the user the command; plumbline --help is a read). From step 17: ST1 (the standup says what came of each task). Evidence: smoke-run-2.md "Step 12b".
- [x] From step 11c (0.7.2, chosen 2026-11-20): BLD2 (tell the lead and builders the copy holds the lead's work; refuse hand merges until a merge refuses), REV2 (folder paths aren't copied text), RV2 (tell, and refuse a plan/UX/security/data rerun after a test- or doc-only fix), HLP1 (quote rule and Write tool up front; send-backs name the right tool). Evidence: smoke-run-2.md "Step 11c".
- [x] BR1 (0.7.2, decided 2026-11-20): on real data, browser clicks ask only when Claude's note for the click names a change (save, delete, submit, send, pay, confirm, sign out...), plus Enter and clicks with no note; Escape, Tab, arrows, scrolling, typing and picking don't ask. Evidence: smoke-run-2.md "BR1".

## Release 5, last
- [x] The smoke test, steps 1 to 18 and both end-to-end runs (smoke run 2 done 2026-11-22; notes in smoke-run-2.md, fixes in 0.7.2). V1-V20 ticked in known-gaps.md where the runs showed them.
- The items that depend on it: R2 (after V11), B6 (after V7 and V9). L6 is done (0.6.12, after V1 in smoke run 1).
- [x] N11 (0.7.3): the real-session prompt test script, run by hand.

## Release 0.7.4 (2026-11-23)
- [x] LOOK1 (0.7.4, decided 2026-11-23): before approval, commands that only look (a test file, a local script, a read-only local database query) run, judged by Claude Code's own mode; writes, git changes, installs and outside-the-laptop commands stay refused. Speed is left until the ten real tasks are done (user's call, the same day).

## Release 0.7.8 (2026-11-23)
- [x] FR3 (0.7.8, from the sprint-docs friction report, chosen 2026-11-23): untracked markdown notes need no task; docs-only tasks skip the suite, blind QA and e2e and get one code review, with lint and build kept; Claude may close a task after the user's "Close it" on a "Close task" question. Dropped at the plan question (user's call): reusing a recent same-code suite run as a new task's baseline. Already in 0.7.6: the expected wait shown when a suite starts.

## What's left (2026-11-22)
1. Ten real Kwenta tasks with no gate bypassed and no gate bug before Plumbline replaces done-gate anywhere (smoke-test.md). Two done: the end-to-end runs' tasks A and B.
2. [x] R2 (0.7.3): the Jira ask needs a write verb aimed at Jira, a ticket or a ticket key; "add a comment to this function" no longer opens it.
3. [x] B6 (0.7.3): a quiet helper is judged by its transcript: still mid-step counts as running, a cancel or a final answer as done.
4. [x] N11 (0.7.3): the real-session prompt test script, run by hand before a release ([prompt-tests.md](prompt-tests.md)). Chosen 2026-11-22: the Agent SDK runner (it answers the plan question, so the checks reach review and finish), not `claude -p` alone (which stops at the plan question).
4b. [x] NC1 (0.7.3), no code comments (decided 2026-11-22): Claude's edits in the user's projects may not add code comments, and Plumbline's own code gets none either. Decisions: tool directives, shebangs and license headers are allowed (`// eslint-disable-next-line`, `//go:build`, `# type: ignore`, `#!/usr/bin/env node`); docstrings and JSDoc count as comments; an edit that adds one is refused on the spot; existing comments may stay or be deleted, but not reworded (a reworded comment counts as added). Asked while building: comments written from the terminal are caught by the finish check, which accepts the user's own or generated ones named on the report's "Comments kept:" line; the ban covers every file type with comments (code, tests, CSS, HTML, SQL, shell, Dockerfile, Makefile, YAML, TOML, ini), not Markdown or text. A comment found anywhere in the repo at task start counts as moved, not new.
4c. [x] LIVE2 (0.7.3): a resumed window clears its "ended" mark; helpers from before a restart count as gone.
5. Not seen live yet: V10 (needs a Jira MCP), V13 (the closing message the Stop hook reads), V14's bare `cd`, V17's Agent `model` override, B6's helper transcript path (in a big task, press Esc on blind QA: a minute later a code edit is refused with "start it again", not "blind QA is writing tests"); V16 and V19 need a Linux machine.
6. [x] (0.8.1, the user's calls on 2026-11-23) Names-only reads of a secrets file: `plumbline env-names`, not sed/cut shapes, which can leak part of a multi-line key. `docker inspect`, `helm get values` and `aws lambda get-function-configuration` stay free. `helm show` from the network is left to Claude Code like curl ("you don't need to always ask permission if you know the action is harmless"). Commands-only tasks skip the suite and review. Also: focused later review rounds, a Fails when bar on must-fix, the should-fix line from the last round only, and the harmless asks (plain git switch, restore --staged, adopt of a free task) handed to Claude Code; checkout on a dirty tree, setting the sprint file and turning the sandbox on kept, after the security review.

## Next: lessons from the 0.8.1 task (2026-11-24), before anything else
The 0.8.1 task needed four review rounds, and two of its misses were Plumbline's own. The user's words: "please take note of those so we dont want to experience those issues again". Build these as their own task, next:
- [x] L1 (0.8.2), loosening a gate needs a map first. A plan that removes or narrows an ask or a refusal lists every command shape that reaches it (what else it was the only guard for), and the challenger checks that list against the code before the checkpoint. 0.8.1 removed the branch-switch ask without seeing it was the only guard for `git checkout <file>`, `restore --worktree` and `-m`, and called `setup sandbox` "only tightens" though it always excludes Docker and Plumbline.
- [x] L2 (0.8.2), free paths are allow lists. A loosened gate lets through only shapes the code lists exactly (`git switch <name>`, no flags); anything else asks. A deny list of flag spellings on a loosened gate is must-fix in review. 0.8.1 patched `-f`, then `-qf`, `--discard`, `--worktr` one round at a time.
- [x] L3 (0.8.2), a security pass on the plan. For plans that loosen gates or add a command that runs without asking, the security reviewer reads the plan before the checkpoint, not only the code after.
- [x] L4 (0.8.2), an arbiter at the review cap (the user's idea). When reviewers run out, one more independent reviewer checks only the open must-fix findings and the fixes since the last round, and the user is asked only if it finds one still open.
- [x] L5 (0.8.2), test what Claude Code really sends. The Review cap recorder (0.8.0) was tested with hand-made hook input, and live it dropped the user's answer: Claude Code puts the user's answers in AskUserQuestion's tool input, which the recorder read as pre-answered (fixed in 0.8.1). Every recorder of a user answer needs a test built from a real captured hook payload, and a live check in the next session.
- Built in 0.8.2 with them: the review of 0.8.1's last fixes (unreviewed when it shipped) found a new-branch checkout or switch that also forces (`-fq`, `--disc`) skipping the dirty check, and `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE`/`GIT_COMMON_DIR` or `-c core.worktree` pointing git at another checkout unseen. Both fixed with allow lists, plus the should-fix and must-fix readers (subheadings, numbered items, `[confidence N]`), `git restore -S`, `git switch -q` and globs in a plain switch. Still to do by hand: one live AskUserQuestion with `PLUMBLINE_DEBUG=1` to capture the full PostToolUse input (L5's live check).

## Speed brief (2026-11-24), shipped as one release
The user asked for every item in 0.8.3 rather than one release each. Numbers before and after: [speed-083.md](speed-083.md).
- [x] S1 (0.8.3), one full suite per task: no suite before the first edit (the other checks still run then), a failing test run compared once on the approved code in a temporary worktree, related tests while building, the full suite once with `--final`, the typecheck skipped after notes. Narrower than the brief's "skip lint and build when only tests or notes changed": lint and format read tests and markdown, builds type-check tests (courtly's `next build`), docs sites build from markdown, and secret scanners read every file.
- [x] S2 (0.8.3), fewer stops: "Approve and commit when green"; test files don't count toward the outside-the-plan cap (other files already join the plan without a question since 0.7.7). Letting work run while the plan question waits isn't possible: Claude can't act while a question is open.
- [x] S3 (0.8.3), fewer reviewer runs: the plan reviewer only for files that decide which checks run, no specialists from test paths, a clean specialist keeps its review after a cited fix.
- [x] S4, the stale "running" builder: fixed in 0.7.6 (a resume reopens the run); 0.8.3 adds a regression test.
- [x] S5 (0.8.3), a shorter task skill with the same rules.
- [ ] The live check: one real courtly or kwenta task on 0.8.3, numbers into speed-083.md.

## Release 0.8.12 (2026-11-27)
- [x] Affected tests for nx, turbo and cargo (the last item named after 0.8.9), directly or behind `npm test` (the user's call at the plan review), with `affected_final` accepting only a run with no failures for them.
- [x] The task skill: no full suite before approval unless Plumbline asks, and a bug is reproduced with one test file (from a session on another machine whose 65-minute suite ran before the plan).
- Left: a cargo run on the approved code builds from scratch (no `target/` in the temporary copy); cargo's failure names collide across crates in the full suite too.

## The open items (0.6.11), shipped before release 5
- [x] M16: the baseline is retaken on the code a followed branch switch or pull lands on, while nothing changed since approval.
- [x] T1: staticcheck is clean. Run it with `GOTOOLCHAIN=go1.27.4 go run honnef.co/go/tools/cmd/staticcheck@latest ./...` (the `go` on the PATH is 1.23, and its automatic switch picks a toolchain too old for Go 1.27's standard library).
- [x] W3: the setup asks for the project's own risky files.
- Dropped: W2 (bundled servers get other tool names, and would run beside the user's own), P2, P7.
