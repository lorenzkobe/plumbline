# Handoff: phases 1a, 1b and the 1a hardening done; smoke test, then phase 2

Read this, the plan doc ("Plumbline plugin plan" in Claude Docs), and the code before starting. The code decides what is true.

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
- `internal/core/shellparse.go`: commands are parsed with `mvdan.cc/sh/v3` v3.12.0 (the newest release that builds with Go 1.24; 3.13 needs Go 1.25). A walk finds every simple command (lists, pipelines, subshells, if/for/while/case, functions, `$(...)`, `<(...)`), tracks `cd`, and unwraps wrappers (`sh -c`, `eval`, `env`, `timeout`, `nice`, `sudo`, `xargs`, `find -exec`, `fd -x`, `trap`, `watch`, heredocs fed to a shell, git aliases from `-c` or the config, the command after `plumbline run proof --`). What can't be known is a segment with `Opaque` set; the Bash gate refuses it in every phase, paused included.
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
- Still open from the gap review: M9 (worktrees as new projects), M13 (`runs.jsonl` unsealed), M14 (two sessions in one repo), L1 to L6.

## Not verified yet (the smoke test covers these)
- That PreToolUse fires for MCP tools with the `mcp__.*` matcher (smoke-test step 6b).

## Phase 2: second reviewer and specialists, builders, blind QA, Jira reading and drafts
Read the plan doc's "Agents and speed" and "Git and Jira" sections. Reuse `HelperRun` and the SubagentStart/Stop records for the new helpers rather than adding a second mechanism; builders are the first helpers that edit, so the helper gates need a per-kind policy instead of "read-only".
