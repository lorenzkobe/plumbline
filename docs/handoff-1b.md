# Handoff: phase 1a done, phase 1b next

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
- **Hotfix flow is not built yet.** It belongs in 1b (it's small: a user-only `/plumbline:hotfix` through UserPromptExpansion that creates a small task with one combined checkpoint and marks "full suite and review right after").

## Not verified yet (the smoke test covers these)
- The AskUserQuestion `tool_response` shape. `findAnswer` handles several shapes; `PLUMBLINE_DEBUG=1` saves the real one to `~/.plumbline/debug/askuser-response.json`.
- That a PreToolUse deny holds in auto mode (docs say deny rules block in every mode).
- That `command_name` in UserPromptExpansion is `plumbline:pause` (the code also accepts a plugin command whose prompt starts with `/plumbline:`).
- That hooks inside a helper get the lead's `session_id` (so helper reads count and helpers see the task). If not, bind by `agent_id` instead.
- Hook "ask" in auto mode shows the prompt (docs say hook-forced prompts still appear).

## Known limits
- Hooks can't see token usage, so the logs don't record tokens yet.
- Weakened-test detection is a report instruction only; the 1b reviewer should check the diff for skips, removed tests and loosened assertions.
- The seal key lives beside the data. It detects edits made outside Plumbline; it doesn't stop a determined one. The Bash gate blocks writes to the data folder.
- Full-stack e2e: `plumbline run e2e` exists, but nothing yet requires it for full-stack tasks (the plan's "ui" proof covers clicks).

## Phase 1b: the challenger and one reviewer
1. Agents in `agents/`: `challenger.md` (read-only, fresh eyes, strongest model, no Agent tool) and `reviewer.md` (read-only, strongest model, no Agent tool). Same checklist: real cause? file:line claims true? what's missing (edge cases, unchecked callers, the same bug elsewhere)? would the tests really prove it? simpler way? "imagine this failed in production next week: why?"
2. Wire the challenger before the checkpoint (skipped for small tasks), at most 2 rounds, with a challenge note in plan.md. It runs without the user, so it must not add a stop. Consider a gate: checkpoint refused without a `## Challenge note` section unless size is small.
3. Wire the reviewer after building: findings scored, only 80+ reported, each checked by the lead against the code before fixing, at most 2 fix rounds. Finish check: require a `## Review` section in report.md written after the last change.
4. Helpers save findings as they go into the task's docs folder; helper reads already count for open-before-quoting.
5. Make each part switchable through `parts` in the setup file (`challenger`, `reviewer`).
6. Add test cases for the new gates, then re-run everything.
