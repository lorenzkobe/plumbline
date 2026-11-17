# Handoff: phases 1a, 1b, the 1a hardening, 2a, the 2a hardening and 2b done; phase 3 next

Read this, the plan doc ("Plumbline plugin plan" in Claude Docs), and the code before starting. The code decides what is true. Every known limit is collected in docs/limits.md, to fix once the plugin is complete (the user's call).

## What 1a built
- Go core in `internal/core`, CLI and hook runner in `cmd/plumbline`, prebuilt binaries in `dist/`, launcher `bin/plumbline` (also on Claude's PATH through the plugin's `bin/`).
- Hooks (`hooks/hooks.json`): SessionStart (rules, setup state, Docker, leftover tasks, daily log cleanup), UserPromptSubmit (one-line reminder, resets send-backs), UserPromptExpansion (pause, resume, cancel, wrong: user-typed only), PreToolUse (edit backstop, Bash gate, commit rule, checkpoint gate, secret reads, no nested helpers; `onFailure: block`), PostToolUse (reads, lookups, edits, checkpoint answers, changed-files check, task binding), Stop (finish check).
- Task state per task id in `~/.plumbline/projects/<key>/tasks/<id>/` with a seal (tamper detection). Session binding in `~/.plumbline/sessions/`.
- Proof: `plumbline run ...` runs the saved command, records the exit code, test counts and the code fingerprint (git tree hash from a temporary index) before and after. Unfinished runs and runs where the code changed mid-run don't count. "0 tests ran" and unreadable output don't count. The task's first runs are the baseline, so only new failures block.
- Skills: setup, task (plus checkpoints.md, playbooks.md, git.md), status, stats, pause, resume, cancel, wrong.
- Tests: `internal/core/*_test.go`, including the try-to-break cases (edit before approval, self-approval, free-text approval, quoting unread files, commit without proof, AI markers, --no-verify, push, .env, settings, nested helpers, tampering, stale proof).

## Decisions made while building (not in the plan doc yet)
- **Auto mode, not plan mode** (agreed with the user): both checkpoints use the question prompt; the edit backstop is the only thing blocking edits before approval.
- **Approval = exactly an option whose label starts with "Approve".** Free text never counts.
- **Hands-off by default** (agreed with the user, who usually hands over a task and comes back): one checkpoint (header `Checkpoint`) with the understanding and the plan together; `Checkpoint 1` is optional for very unclear tasks. During a task nothing waits on a prompt: unsaved commands before approval are refused (not asked); while building, files outside the plan that belong to the change are allowed and must be listed in the report; risky files outside the plan (auth, payments, migrations, dependencies, config, deploy, plus setup's `risky_paths`) and the user's unsaved work are refused and must be listed under "For you"; packages and outside-the-laptop commands run only if the approved plan's `## Needs your OK` lists them. Real decisions (trade-offs, choices of approach) are still asked right away. More than 3 files outside the plan (`OutsidePlanCap`) is treated as a plan change and goes to the user. The finish check refuses a report that doesn't list every outside-plan and refused file. Per-project risky files were discussed and left out on purpose: `risky_paths` in the setup file still works by hand; add a setup question only if the logs show a risky file slipping through. The user has a terminal sound for questions and finishes, so no notification work is needed.
- **Moving backwards is Claude's to do** (`plumbline task replan`): it only adds restrictions. Moving forward is only through the user's answer.
- **MCP servers are not bundled in the plugin.** Setup checks them and, if missing, adds them at user scope after looking up current install steps (the plan said "settings ship with the plugin"; bundling guessed commands risked broken or duplicate servers). Revisit if the user prefers bundling.
- **Data folder is `~/.plumbline`**, not `${CLAUDE_PLUGIN_DATA}`: the CLI run through Bash doesn't get that variable, and logs should survive a reinstall.
- **Laptop settings** written by `plumbline setup laptop`: `autoMemoryEnabled: false`, `attribution: {"commit": "", "pr": ""}`, and a marked block in `~/.claude/CLAUDE.md`. `plumbline setup undo-laptop` restores the old values.
- **Hotfix flow is not built yet.** Its own step after 1b (the user's choice): a user-only `/plumbline:hotfix` through UserPromptExpansion that creates a small task with one combined checkpoint and marks "full suite and review right after".

## Not verified yet (the smoke test covers these)
- The AskUserQuestion `tool_response` shape. `findAnswer` handles several shapes; `PLUMBLINE_DEBUG=1` saves the real one to `~/.plumbline/debug/askuser-response.json`.
- That a PreToolUse deny holds in auto mode (docs say deny rules block in every mode).
- That `command_name` in UserPromptExpansion is `plumbline:pause` (the code also accepts a plugin command whose prompt starts with `/plumbline:`).
- That hooks inside a helper get the lead's `session_id` (so helper reads count and helpers see the task). If not, bind by `agent_id` instead.
- Hook "ask" in auto mode shows the prompt (docs say hook-forced prompts still appear).

## Known limits
- Hooks can't see token usage, so the logs don't record tokens yet.
- Weakened-test detection is instructions only: the reviewer's checklist lists skipped, removed and loosened tests, but no gate checks the diff.
- The seal key lives beside the data. It detects edits made outside Plumbline; it doesn't stop a determined one. The Bash gate blocks writes to the data folder.
- Full-stack e2e: `plumbline run e2e` exists, but nothing yet requires it for full-stack tasks (the plan's "ui" proof covers clicks).

## What 1b built (v0.3.0)
- Agents `agents/challenger.md` and `agents/reviewer.md`: `model: inherit` (the user's choice: same model as the session), `disallowedTools: Agent, Edit, MultiEdit, NotebookEdit, AskUserQuestion`. `tools` is left out on purpose so they keep the session's MCP tools (SocratiCode, Serena) whatever the servers are named. One shared checklist, findings scored, only 80+ listed; the reviewer also lists weakened tests.
- Hooks `SubagentStart` / `SubagentStop` (matcher `^plumbline:(challenger|reviewer)$`) record each run in the task state (`Helpers`): agent id, start/stop time, code fingerprint at both ends, findings file, whether both docs existed at start. A helper that stops with no findings file is sent back once. Start context tells the helper its notes folder, round, diff base (`StartCommit`, recorded at `task start`) and the one file it may write.
- Helper gates (PreToolUse with `agent_type` `plumbline:challenger|reviewer`): no edits, Write only to its own `challenge-N.md` / `review-N.md`, Bash only if every segment is read-only (or a read-only `plumbline` subcommand), no AskUserQuestion, no nested helpers. The lead can't write `challenge-*.md` / `review-*.md`.
- Lead gates on the Agent tool (scoped or bare name): challenger only before building, only once understanding.md and plan.md exist, at most 2 runs; reviewer only while building, at most 3 runs (first + 2 fix rounds).
- Checkpoint (normal and big tasks, `parts.challenger` on): a finished challenger run (code unchanged during it, both docs existed, findings file present) and a non-empty `## Challenge note` in plan.md.
- Finish check (`parts.reviewer` on, all sizes): the latest finished review is on the current fingerprint, with findings; a non-empty `## Review` in report.md, written after that review. After 3 reviews and a later change, the check says to ask the user.
- Stop hook: a helper started in the last 30 minutes with no stop recorded lets the lead stop (waiting on a background helper isn't "done").
- Fallback: if a helper's hook input has a session id with no binding, the one open task on the branch is used.

## Fixes to 1a found while building 1b
- `plumbline docs-dir`, `status`, `task show|list`, `stats`, `setup show`, `version` were refused before approval during a task (the task skill needs `docs-dir` to write understanding.md). Now quiet.
- `Fingerprint` copied the index to a temp file with a fresh mtime, which turns off git's racy-clean re-check. A same-size edit in the same second git last indexed the file was fingerprinted as the old content (reproduced 3/3 on Apple Git 2.39), so proof could count for changed code. The copy now keeps the index's mtime.
- Lost updates: every hook does load, change, save on the task state; the lead's and a background helper's hooks overlap, so saves wiped each other out (36 lost records in 3 runs of the new test). Each hook now holds a per-task `flock` (`state.lock`, 10 s wait, then goes on without it) and re-reads the state under it; the CLI's `tool-down`, `replan`, `finish` and `waive` do the same.

## Decisions made in 1b
- Strict review freshness (the user's choice): any code change after the last review needs another review; the re-review checks only the fixes.
- Hotfix flow moved out of 1b to its own step (the user's choice).
- 1b was built before the day-one smoke test ran (the user's choice); smoke-test step 8 covers the helpers, including whether helper hooks carry the lead's session id.
- Commits are not gated on the review; the finish check is. A final commit normally comes after the report, which already needs the review.

## Not verified yet (beyond the 1a list above)
- That `agent_type` in helper hooks is exactly `plumbline:challenger` / `plumbline:reviewer` (docs say plugin agents are scoped this way).
- That helper tool hooks carry the lead's `session_id` (docs don't say; the branch fallback covers one open task per branch).
- That `SubagentStop` `decision: block` sends the helper back as documented.

## What the 1a hardening built (v0.3.1, on main)
- `internal/core/shellparse.go`: commands are parsed with `mvdan.cc/sh/v3` v3.12.0 (the newest release that builds with Go 1.24; 3.13 needs Go 1.25).
- Go: `go.mod` keeps `go 1.24.7` as the minimum and builds with `toolchain go1.27.4` (the newest release; 1.24 no longer gets security fixes). Bump the toolchain line when a new Go patch comes out and rebuild `dist/`. A walk finds every simple command (lists, pipelines, subshells, if/for/while/case, functions, `$(...)`, `<(...)`), tracks `cd`, and unwraps wrappers (`sh -c`, `eval`, `env`, `timeout`, `nice`, `sudo`, `xargs`, `find -exec`, `fd -x`, `trap`, `watch`, heredocs fed to a shell, git aliases from `-c` or the config, the command after `plumbline run proof --`). What can't be known is a segment with `Opaque` set; the Bash gate refuses it in every phase, paused included.
- `internal/core/shell.go`: `ReadOnly` fails closed (pure readers take any arguments; other allowlisted commands need every argument known; git's read commands are the one exception, see limits). `WriteTargets` lists the files a segment writes by name; the Bash gate runs the data-folder, settings, `.git/`, secret-file and building-phase rules (user's work, risky outside the plan, the cap) on them. `buildFile` in pretool.go is that building rule, shared with Edit and MCP tools.
- `ParseCommit` (commit.go) reads commit options the way git does: short bundles, unique long prefixes (with `--no-verbose` so `--no-ver` stays ambiguous), pathspecs, trailers, `-F`, and `-m "$(cat <<'EOF' ...)"` / `-m "$(cat file)"`.
- State: `state.json` starts with a `plumbline-seal:` line, one rename per save; `writeFileAtomic` uses unique temp names. 0.3.0 tasks (separate `state.seal`) still load. The paused flag holds a seal.
- MCP: PreToolUse matcher includes `mcp__.*`. `MCPKind` judges by tool name (shell / write / lookup; memory, code-search, docs, browser and claude.ai servers are lookups). Shell tools with a `command` go through the Bash gate, write tools naming a file through the edit gate, other write tools wait for approval (asked when there's no task). PostToolUse runs the changed-files check after them.
- Tests: `hardening_test.go` (one test per finding; every one fails on the 0.3.0 code) and `shellparse_test.go` (parser, sed scripts, commit options, write targets, MCP kinds).

## Decisions made in the hardening
- Real parser, not a hardened splitter (the user's choice).
- 1b was merged into main and the hardening was built on main (the user's choice).
- Writes whose targets aren't known (`rm "$tmp"`) aren't refused up front; the after-the-command check still records what changed. Refusing them would block normal scripts.
- Env prefixes: only locale, colour, `TERM`, `CI` and `PAGER=cat` keep a command read-only. A lowercase shell variable (`f=src/a.go`) is harmless because it isn't exported.
- An MCP write tool with no task asks instead of refusing (Jira or GitHub posts the user asked for).
- A fresh-eyes review of the first hardening pass found 10 more ways through (ANSI-C escapes, brace words, loose wrapper options, aliases made in the same line, only the first commit checked, arithmetic and zsh glob qualifiers running code, and others); all are fixed, with `TestReviewFindings*` in hardening_test.go.
- Arguments that xargs or find add (file names from a listing) are taken as files, not flags, so `rg -l x | xargs sed -n 1p` reads. The gap: a file named like a flag.

## Known limits after the hardening
- Code run by interpreters isn't inspected (`python -c`, `node -e`, `awk 'BEGIN{system(...)}'` while building, a script file). They are not read-only, so they never run before approval.
- git's read commands (`git diff $(git merge-base ...)`) accept arguments that aren't known yet; an expansion that produces `--output=` would slip through. Kept so research and the reviewer can use merge-base.
- MCP tools are judged by name. A server whose write tools don't use a write verb (edit, replace, insert, write, create, delete, rename, ...) isn't gated.
- zsh-only syntax that bash can't parse is refused (Claude Code runs commands in the user's shell, zsh on this laptop).
- Still open from the gap review: M14 (two sessions in one repo), L1 to L6. M13 was fixed in 0.4.1, M9 in 0.5.0.

## Not verified yet (the smoke test covers these)
- That PreToolUse fires for MCP tools with the `mcp__.*` matcher (smoke-test step 6b).

## What 2a built (v0.4.0, on main)
- Reviewers (`internal/core/helpers.go`): `plan-reviewer`, `code-reviewer`, `security-reviewer`, `data-reviewer`, `ux-reviewer`, all on the 1b `HelperRun` mechanism. `ReviewersFor(size, files)` gives the code reviewer always, the plan reviewer for normal and big tasks, and specialists from path signals (`reSecuritySignal`, `reDataSignal`, `reUXSignal`). The checkpoint refuses a plan whose `## Team` leaves out a specialist its `## Files` call for (`teamProblems`); `Task.Reviewers` keeps the Team's reviewers at approval. `RequiredReviewers` (finish check, `plumbline status`) adds specialists for files changed outside the plan. Per-kind cap `MaxReviews` (3), per-kind findings names (`code-review-N.md`); a 0.3.x `reviewer` run loads as `code-reviewer`.
- Independence (`reviewerNotes`): a reviewer reads, in the data folder, only understanding.md, plan.md and its own findings. Read, Grep, Glob, and Bash words, inputs and redirects are checked; a search tool (`rg`, `grep`, `find`, ...) given a folder above the data folder is refused too. PreToolUse matches Grep and Glob for this.
- Agents share `skills/task/review-checklist.md`; SubagentStart gives its path (`ChecklistPath`, from `CLAUDE_PLUGIN_ROOT` or the binary's folder).
- Jira (`JiraTool` in pretool.go): an MCP tool whose name holds atlassian, jira or confluence is Jira; reads (get, search, lookup, fetch, list, ...) are look-ups recorded as `jira`; everything else goes to `gateJiraWrite`, in every state: refused unless the user's latest message asks (`UserAsksJira`, a sealed per-session flag written by UserPromptSubmit), then asked, after the AI-marker and em-dash check. Company tasks on a Jira ticket (`Setup.JiraTicket`) need the Jira read before the checkpoint (`tool-down jira` is the escape) and `<TICKET>-jira-comment.md` at the finish check.
- claude.ai connectors: only `claude_ai_Claude_Docs` is a look-up server now; the write verbs grew (send, post, reply, comment, transition, ...).
- Tests: `internal/core/reviewers_test.go`.

## Decisions made in 2a
- The smoke test runs once the plugin is complete (after phase 3), and what it finds is fixed under the plan's "Updating Plumbline" rule (the user's choice).
- Phase 2 split into 2a and 2b (the user's choice).
- Robust re-reviews (the user asked for the most robust option): after any code change every required reviewer checks again; a re-run looks at what changed since its own last run and its earlier findings.
- Jira is draft-only, except when the user asks in their own message; then their OK on the exact text (the user's change at the checkpoint). Only the comment file carries the ticket prefix (the user's choice: it's the file they copy and post); the other notes keep their names.
- The checkpoint only demands specialists in `## Team`; the base reviewers come from the size.

## Known limits after 2a
- Specialists are called for by file paths only. A risky change in a plainly named file needs the lead to name the specialist in `## Team`.
- `UserAsksJira` is a keyword match on the user's message (jira, ticket, comment, status, transition, post). A message like "add a comment to this function" also lets a Jira post reach the confirm prompt; the prompt is the user's last guard.
- Reviewer independence is path based. Checked: Read, Grep, Glob, MCP tools that name a path, and in terminal commands every word, input, redirect, heredoc and here-string body, globs starting above the data folder, and `--flag=<path>` values. A word whose value isn't known is refused when its text holds "plumb". Not caught: a path built at run time without that text (`$(printf %s ~/.plu)mbline/...`), or findings the lead pastes into a reviewer's prompt (the rules forbid it).
- A fresh-eyes review of the first 2a pass found 5 more gaps (globs above the data folder, heredoc and here-string text, run-time paths, a 0.3.x reviewer blocked from its own findings, camelCase file names missing the specialist signals) and 2 lesser ones (MCP read tools, absolute paths in `## Files`); all are fixed, with cases in reviewers_test.go.
- Grep and Glob go through the PreToolUse hook only for helpers; the lead's return at once.

## Not verified yet (the smoke test covers these)
- That the SubagentStart/Stop matchers fire for the new agent names, and that `agent_type` is `plumbline:code-reviewer` (smoke-test step 8).
- The real Jira MCP's tool names: the Atlassian remote server's (`getJiraIssue`, `addCommentToJiraIssue`, `transitionJiraIssue`, ...) and sooperset's mcp-atlassian (`jira_get_issue`, `jira_add_comment`, ...) are covered by tests; another server's names may differ (smoke-test step 9).
- That the UserPromptSubmit hook's `prompt` holds the user's words as typed.

## What the 2a hardening built (v0.4.1, on main)
From a review of 14 suggestions against the code and done-gate's logs (the plan's decisions log has what was adopted and turned down).
- Report checks (`reportShapeMissing`, `finalMessageMissing` in stop.go): `## For you` and every waiver in report.md; the closing chat message (`last_assistant_message`) needs a "For you" line. The Stop hook now runs the finish check on a question too, once the task is proven and not yet reported; an unproven question still goes through.
- Repeated blocks (repeat.go): `hookCtx.deny` records rule, reason and target per session and helper in `~/.plumbline/sessions/<sid>[-<agent>].lastblock`; the same one twice in a row gets the stop note. UserPromptSubmit clears them.
- Sealed runs (runs.go): each line is `<seal> <json>`, sealed with the task-state key; `LoadRuns` skips bad lines. `LatestFor(runs, taskID, kind, sub, command, fp)`; `commandMatches` handles the related-tests template.
- Size (`sizeProblems` in pretool.go): "small" refused at the checkpoint for `RiskyPath` files or the security and data signals. `plumbline task size` (main.go), before approval only, quiet in the Bash gate.
- Shared tests (`PlanCheck.SharedTests`, docs.go): named refs (`file::name`) used by more than one case; SubagentStart lists them for the challenger and reviewers.
- Disputes (helpers.go): `DisputesName(kind)` = `<kind>-disputes.md`, written by the lead, readable only by that reviewer. `disputeOpen` (written after the reviewer's last run began, or no `## Disputes` in its findings) makes the finish check ask for another run; `upheld` requires "upheld" under For you; no runs left goes to the user.
- Failures (failures.go, main.go, bin/plumbline): `RecordFailure` on a panic or unreadable input; the launcher logs to errors.log and exits 1; `FailureNote` at SessionStart (failures.seen keeps the time and the errors.log offset); a `failure` event for stats; 6-month cleanup.
- hooks.json: two PreToolUse groups, `hook pre` (blocking: the tools PreToolUse gates, MCP tools except the search servers and Serena's look-ups) and `hook pre-search` (no onFailure: Grep, Glob, MemPalace, SocratiCode, Context7, Serena's find/search/list tools). Matchers are anchored JavaScript regexes; `TestHookMatchers` checks them with node, including that no tool is in both.
- From the fresh-eyes review (there before 0.4.1): `Monitor` goes through `gateBash` (and is in the blocking matcher); `grepSecret` gives a Grep that names a secret file Read's prompt; `RequiredReviewers` adds `ChangedSince(root, StartCommit)` (git.go), minus the user's own files, so specialists follow the real change.
- Tests: `internal/core/hardening2a_test.go`, `cmd/plumbline/main_test.go` (built binary: hook failure exit codes, `task size`).

## Known limits after 0.4.1
- The run seal, like the task-state seal, uses a key in the data folder. A process Claude starts while building (a `python3 -c` that builds the path from pieces, or a test file) could read it and write a sealed record. The Bash gate only catches the path when it's written out.
- Runs recorded before 0.4.1 have no seal and are skipped, baselines included. A task open across the upgrade must run its checks again; with failures that were already there and edits made, only a waiver gets past them. No task was open on the laptop at the upgrade.
- "In a row" for repeated blocks is per session and helper, cleared by a different refusal or a user message; allowed calls in between don't clear it, and two identical refused calls in one message count at once.
- A binary that exists but can't run (corrupt, wrong architecture) still blocks the gates, but the failed `exec` leaves nothing in errors.log.
- "Small" is refused for any file on the risky list, so a copy-only change to a login page needs `task size normal`.
- The closing-message check looks for a "For you" line and sends back once; it can't check that the bad news comes first.
- Grep asks only when its path or glob names a secret file; a search of a whole folder that happens to reach a `.env` isn't caught. Grep is in the fail-open group, so while the hook fails it doesn't ask either.
- Monitor commands get the Bash gate before they run, but not the changed-files check after (PostToolUse doesn't match Monitor, which returns before its command ends); the fingerprint still makes proof stale if it changes code.
- A task without `StartCommit` (made before 0.3.0) falls back to HEAD for `ChangedSince`, so committed slices don't call for specialists there.

## Not verified yet after 0.4.1 (add to the smoke test run)
- That Claude Code accepts the lookahead matchers as documented (JavaScript regex, unanchored), and that a search still runs when the binary is missing while an edit is blocked (smoke-test step 10).
- That `last_assistant_message` holds the closing message as shown (docs say "the text content of Claude's final response").

## What 2b built (v0.5.0, on main)
- M9 (paths.go): `ProjectRoot(root)` reads `<root>/.git` and the worktree's `commondir` (no git call), and `ProjectKey` hashes the main checkout, so a worktree shares setup, tasks, runs, the pause seal and the session binding (`ActiveTask` compares project keys). A plain checkout's key is unchanged (`treeKey`). Snapshots (`snapPath`) are keyed by the working tree and by `snapID()` (session, plus agent id inside a helper).
- Copies (worktree.go): `WorktreesDir` is `.claude/worktrees`, added to `.git/info/exclude`, filtered out of `DirtyFiles` and `ChangedSince`, and excluded from `Fingerprint` when git doesn't already ignore it (`worktreesUnignored`; git refuses an exclude pathspec naming an ignored path). `MakeWorktree` commits the lead's fingerprint tree on HEAD (`commit-tree`, Plumbline's own identity) and runs `git worktree add --detach` with the repo's hooks off. `MergeWorktree` snapshots both trees on the base, runs `git merge-tree --write-tree --name-only --no-messages` (git 2.38+), reads every blob first and only then writes, deletes, chmods or symlinks; on exit 1 it returns `ErrConflict` and writes nothing. It refuses paths in `.git/`, the copies' folder, secret files and `.plumbline.json`.
- Workers (workers.go, helpers.go): kinds `tracer`, `builder`, `qa`, `tester` (`WorkerKinds`); findings `trace-N.md`, `build-N.md`, `qa-N.md`, `test-N.md`. `gateHelperStart`: tracer any time; builder and tester while building; qa via `qaStartProblem` (building, `parts.qa`, at most `MaxQARuns` 2, not while one runs, and `codeSinceApproval` empty: only test files changed since `ApprovalPrint`, recorded at the first approval). Every worker can be switched off by name in `parts`.
- Builder: `SubagentStart` makes the copy outside the task lock (`makeBuilderCopy`, then re-lock and reload), named by agent id. `newCtx` makes the copy the builder's `c.root`, so `gateEdit`, `gateBash`, `buildFile` and `checkChanged` judge it there (`c.rel`). `gateBuilder`: edits only inside its copy, not `.plumbline.json`; commands must run in the copy (`s.Dir`), write nowhere in the project outside it, read-only git, plumbline only reads and `run test|related|lint|build`. `stopBuilder` removes a copy with no changes. The lead can't write in a copy (`othersCopy`). `plumbline builder list|merge|drop` (main.go); `builderMissing` holds the finish check while a copy is unmerged.
- Blind QA: `qaEdit` (test files only, by name: `reTestFile`; not in copies); `reviewerNotes` and `helperBash` treat it as `independent`, and `qaBlind` refuses the copies' paths and, while a copy exists, searches and patterns from at or above the copies' folder; `helperMayRun` lets it run `plumbline run related|test`. While it runs, `buildFile` refuses the lead's non-test files and `MergeBuilder` refuses. `qaMissing` (big tasks, unless `plumbline waive qa`, which doesn't lapse) and the report's `## Review` must mention QA. `run.Wrote` lists its test files; the stop log has the count.
- `noAsk`: a worker's "ask" becomes a refusal (`helper-no-questions`). The challenger and reviewers keep asking as before.
- Helper reads and look-ups no longer count as the lead's (posttool.go, the user's choice).
- hooks.json: SubagentStart/Stop matchers include the four workers; SubagentStart timeout 120 s.
- Agents: `tracer.md`, `tester.md` (`model: sonnet`, the user's choice), `builder.md`, `qa.md` (`model: inherit`; the lead passes `model: sonnet` for simple builder parts).
- Tests: `internal/core/workers_test.go`, `TestBuilderCommands` in cmd/plumbline/main_test.go.

- A fresh-eyes review of the first 2b pass found 10 issues (builders reaching the main checkout through links, `-C`/`-o` and interpreters; QA globs and searches above the copies; Claude Code's worktrees taken for copies; merges ignoring filters, modes and folder-to-file changes; finish during a running builder; a too-broad test-file rule; the 30-minute QA hold; pre-0.5.0 big tasks) and a shared-helper bug (`IsInside` treated `./...` as outside); all are fixed, with tests at the end of workers_test.go and paths_test.go. A second review of the fixes found more ways out (symlinks to `/` or `~`, values stuck to flags, case, computed paths, background commands, `.git/hooks`; for QA, searches with no folder, `rg -uu`, computed `cd`, git's object store, the copies' folder named exactly) and two over-strict rules (the lead's edits put on a builder; a dead QA holding the lead for 3 hours); fixed, with `TestBuilderEscapesSecondPass`, `TestBlindQASecondPass`, `TestLeadChangesArentABuildersWrites`, `TestQAHoldFollowsActivity`.

## Decisions made in 2b
- Plumbline makes the copies itself (the user's choice): Claude Code's `isolation: worktree` starts from the default branch, and a WorktreeCreate hook would replace worktree creation for all of the user's worktrees, with no docs on whether a plugin's hook fires for subagents.
- Blind QA is required on big tasks and enforced (the user's choice): it runs first, before any code, and the lead builds through builders meanwhile.
- Tracers and the tester run on sonnet (the user's choice).
- Only the lead's reads count as opened (the user's choice, after asking whether a helper's line can be trusted): a faster helper can misread a line, and the lead signs the plan.
- Builders, tracers and the tester are the lead's choice; nothing requires them. Small tasks build without builders.
- A builder's copy is detached (no branch), its base is the lead's code at start, and it never commits: the lead's normal commit rule covers the merged code.

## Known limits after 2b
- A copy holds only files git tracks or would track: `node_modules`, build output and other ignored files aren't there, so a builder's tests may not run in its copy. The lead runs them after merging.
- A builder's new files outside the plan don't count toward the 3-file cap until merged (they don't exist in the main checkout yet); the merge's changed-files check then counts them and stops past the cap.
- Builder confinement and QA blindness are guard rails against mistakes, not a sandbox: hooks see commands, not what code does. Code an interpreter runs (`python3 -c`, a test, a build script) can reach anywhere the user can. For builders the after-the-command check catches what lands in the main checkout's tracked, untracked or git files (not ignored files, and not other copies). For QA nothing catches it: its own tests, run with `plumbline run related`, could read the copies, so blindness holds while QA follows its instructions. Within the gates: QA can't name a path in the copies, search or glob from at or above their folder, use computed paths or folders, or read git's object store.
- Writes whose targets aren't known (`rm "$x"`) and code an interpreter runs aren't refused up front for builders either. The changed-files check in the copy records them, and `builderReachedMain` holds anything that changed in the main checkout while a builder's command ran; the lead's own edit at the same moment is held too ("while").
- While a builder's copy exists, blind QA can't run searches or globs from the repo root (only the Grep tool, which skips ignored folders); it searches below it (src/, tests/).
- "Running" means a tool call in the last 30 minutes (builders and QA record each one; other helpers count from their start). A builder or QA that thinks for over 30 minutes without a tool call counts as finished.
- The lead's change and a builder's command at the same moment: the lead's PostToolUse takes the change into the builders' baselines, so a builder write landing in that window is missed, and one landing before the lead's hook runs is put on the builder ("while").
- Test files are known by name (`reTestFile`); Rust inline tests in `src/*.rs` and other unnamed conventions need QA to put the test in a test file.
- `noAsk` applies to workers only; a reviewer reading `.env` still shows you the prompt (unchanged).

## Not verified yet (add to the smoke test run)
- That a builder's tool hooks carry the lead's `session_id` (if not, `taskByAgent` finds its task by agent id) and that its Bash cwd persists after `cd <copy>`.
- That Claude Code lets a subagent edit files under `.claude/worktrees/` without its own prompt.
- That `git merge-tree --write-tree` behaves the same on the Linux builds' git (needs 2.38+).
- That `model: sonnet` in a plugin agent and the Agent tool's `model` override work as documented.

## Phase 3: design workflow, devops rules, standup
Read the plan doc's "Frontend and design", "Devops and production" and "Proposed commands" sections. The hotfix flow (user-only `/plumbline:hotfix`) is its own step and can come first if the user prefers. After phase 3, run docs/smoke-test.md (add the 2b items above) before the Kwenta trial.
