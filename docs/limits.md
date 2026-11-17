# Known limits to fix once the plugin is complete

Every known limit from phases 1a to 3 in one list. The user's call (2026-11-17): build the remaining phases first, then work through this list. Each item points to where the full text is (handoff-3.md section, gap review). Tick an item when it's fixed, with the version.

## Gates and parsing
- [ ] G1. Code run by interpreters isn't inspected (`python -c`, `node -e`, `awk 'BEGIN{system()}'`, a script file). Never runs before approval, but while building it can do anything. (handoff: after the hardening)
- [ ] G2. git's read commands accept arguments not known yet (`git diff $(git merge-base ...)`); an expansion producing `--output=` would slip through. (after the hardening)
- [ ] G3. MCP tools are judged by name; a write tool without a write verb isn't gated. (after the hardening)
- [ ] G4. zsh-only syntax that bash can't parse is refused. (after the hardening)
- [ ] G5. Monitor commands get the Bash gate before, but no changed-files check after. (after 0.4.1)
- [ ] G6. Grep asks only when its path or glob names a secret file; a folder search reaching a `.env` isn't caught, and Grep fails open. (after 0.4.1)
- [ ] G9. Command names are matched as written: on macOS's case-insensitive disk `VERCEL --prod` or `GIT push` runs the real tool but isn't recognised by any name-based rule (found by the phase 3 review). (pre-existing)
- [ ] G10. An unconfirmed setup's `risky_paths` and `ignore_changes` are still used, so a terminal-edited setup could drop a risky path or widen what the changed-files check skips (0.6.0 covers parts and deploy_pipeline only). (pre-existing, found in phase 3)
- [ ] G7. "In a row" for repeated blocks: allowed calls in between don't clear it; two identical refusals in one message count at once. (after 0.4.1)
- [ ] G8. Arguments that xargs or find add are taken as files, not flags, so a file named like a flag (`--output=x`) gets past the read-only check. (decisions in the hardening)

## Proof and state
- [ ] P1. The task-state and run seals use a key in the data folder; a process Claude starts while building could read it and forge a record. (Known limits; after 0.4.1)
- [ ] P2. Runs recorded before 0.4.1 have no seal and are skipped, baselines included. (after 0.4.1)
- [ ] P3. Weakened-test detection is instructions only; no gate checks the diff for skipped or loosened tests. (Known limits)
- [ ] P4. Full-stack e2e: nothing requires `plumbline run e2e` for full-stack tasks. (Known limits)
- [ ] P5. Hooks can't see token usage, so logs don't record tokens. (Known limits)
- [ ] P6. A binary that exists but can't run blocks the gates but leaves nothing in errors.log. (after 0.4.1)
- [ ] P7. A task without `StartCommit` (before 0.3.0) falls back to HEAD for `ChangedSince`. (after 0.4.1)

## Checkpoint, reviews and report
- [ ] R1. Specialists are called for by file paths only. (after 2a)
- [ ] R2. `UserAsksJira` is a keyword match ("add a comment to this function" lets a Jira post reach the confirm prompt). (after 2a)
- [ ] R3. Reviewer independence is path based: a path built at run time without "plumb" in it, or findings pasted into a reviewer's prompt, aren't caught. (after 2a)
- [ ] R4. "Small" is refused for any risky-list file, even a copy-only change. (after 0.4.1)
- [ ] R5. The closing-message check can't check that bad news comes first. (after 0.4.1)
- [ ] R6. Grep and Glob go through PreToolUse only for helpers. (after 2a)

## Builders, blind QA, tracers, tester (2b)
- [ ] B1. Builder confinement and QA blindness are guard rails, not a sandbox: code a builder or QA runs can reach anywhere. For builders, changes landing in the main checkout's tracked, untracked or git files are caught after the command (not ignored files, not other copies); for QA nothing catches its own tests reading the copies. (after 2b)
- [ ] B2. A builder's copy has no ignored files (`node_modules`, build output), so its tests may not run there. (after 2b)
- [ ] B3. A builder's new outside-plan files don't count toward the 3-file cap until merged. (after 2b)
- [ ] B4. Builder writes with unknown targets (`rm "$x"`) aren't refused up front; the copy's changed-files check records them. (after 2b)
- [ ] B5. The lead's change and a builder's command at the same moment can be misattributed either way. (after 2b)
- [ ] B6. "Running" is a tool call in the last 30 minutes; a builder or QA thinking longer without one counts as finished. (after 2b)
- [ ] B7. While a builder's copy exists, blind QA can't search or glob from the repo root (only the Grep tool). (after 2b)
- [ ] B8. Test files are known by name only (Rust inline tests and other conventions need a test file). (after 2b)
- [ ] B9. `noAsk` covers workers only; a reviewer reading `.env` still prompts the user. (after 2b)
- [ ] B10. `ln -s` to anything outside the copy is refused, including harmless system paths (`/usr/bin/node`). (after 2b)

## Design, devops, standup (phase 3)
- [ ] D1. The design gate holds only design tasks; a normal task can change screens without mockups (by choice). (after phase 3)
- [ ] D2. `## Design` is found by heading prefix and the pick by its short name in that section's text; "not Direction B" would count. (after phase 3)
- [ ] O1. Deploys are known by CLI name and by scripts named for deploying (deploy, publish, ship); anything else (`go-live.sh`, a `release` make target, an unlisted deploy tool) isn't refused in a pipeline project and runs like any command while building. Docker pointed at another machine (`docker --context prod`, `-H ssh://`) isn't seen as remote. (after phase 3)
- [ ] O2. A database client that names no host (or names it in a variable, or through PGHOST or a service file) counts as remote and asks; `select` calls to functions that change things beyond the listed ones (setval, pg_terminate_backend, ...) read as reads when approved. (after phase 3)
- [ ] S1. A task keeps only when each file was last opened, so a file opened Monday and again Tuesday counts for Tuesday only in the standup. (after phase 3)
- [ ] S2. The standup's other repos are Plumbline's projects, the repo's sibling folders and its `frontend_repo`; a repo elsewhere with commits on the ticket and no task isn't found. (after phase 3)
- [ ] S3. A personal project's standup can't follow work into another repo (no tickets to follow). (after phase 3)
- [ ] S4. The sprint file is protected for the Edit and Write tools only; a terminal command that writes it follows the usual terminal rules. (after phase 3)

## Gap review (docs/gap-review-1a.md), still open
- [ ] M14. Two sessions in one repo: one session's edits stop the other.
- [ ] L1. False blocks: `npx tsc --noEmit`, `pip install -r requirements.txt` refused as installs during a task.
- [ ] L2. Only the Read tool counts as "opened"; `cat`/`sed -n`/`rg` reads give a false "didn't open this file".
- [ ] L3. Logs hold commit subjects and absolute paths in Detail.
- [ ] L4. Without failing-test ids, the baseline compares counts only; a broken-environment baseline sticks.
- [ ] L5. `test:` matches inside words like "latest:".
- [ ] L6. If the AskUserQuestion response is a plain string, the `"Q"="A"` fallback could be spoofed.

## Tooling
- [ ] T1. staticcheck didn't run for 0.5.0 (the installed one is too old for Go 1.27). Run `go run honnef.co/go/tools/cmd/staticcheck@latest ./...` and fix what it finds.

## Open work and decisions to revisit
- [ ] W1. The hotfix flow isn't built: a user-only `/plumbline:hotfix` through UserPromptExpansion that makes a small task with one combined checkpoint and marks "full suite and review right after". It's a separate step and can come before phase 3. (1a decisions; 1b decisions; Phase 3)
- [ ] W2. MCP servers aren't bundled in the plugin. Setup adds missing ones at user scope instead. Revisit if the user wants them bundled. (1a decisions)
- [ ] W3. There's no setup question for each project's risky files; `risky_paths` in the setup file can only be set by hand. Add a question if the logs show a risky file slipping through. (1a decisions)

## Not verified against Claude Code yet (smoke test, after phase 3)
Confirm these before fixing the list above, since some fixes depend on how Claude Code behaves. Full text is in the handoff-3.md section named in brackets. Steps 1 to 13 of docs/smoke-test.md cover them.
- [ ] V1. The AskUserQuestion `tool_response` shape (`PLUMBLINE_DEBUG=1` saves the real one). Settles L6 too. (1a)
- [ ] V2. A PreToolUse deny holds in auto mode. (1a)
- [ ] V3. `command_name` in UserPromptExpansion is `plumbline:pause`. (1a)
- [ ] V4. Hooks inside a helper get the lead's `session_id`; if not, bind by `agent_id`. (1a, 1b)
- [ ] V5. A hook "ask" in auto mode shows the prompt. (1a)
- [ ] V6. `agent_type` in helper hooks is exactly `plumbline:challenger` / `plumbline:reviewer` / `plumbline:code-reviewer`. (1b, 2a)
- [ ] V7. `SubagentStop` `decision: block` sends the helper back as documented. (1b)
- [ ] V8. PreToolUse fires for MCP tools with the `mcp__.*` matcher (smoke-test step 6b). (after the hardening)
- [ ] V9. The SubagentStart/Stop matchers fire for the new agent names (smoke-test step 8). (2a)
- [ ] V10. The real Jira MCP's tool names match the ones the tests cover (smoke-test step 9). (2a)
- [ ] V11. The UserPromptSubmit hook's `prompt` holds the user's words as typed. Settles R2's input. (2a)
- [ ] V12. Claude Code accepts the lookahead matchers (JavaScript regex, unanchored); a search still runs when the binary is missing while an edit is blocked (smoke-test step 10). (after 0.4.1)
- [ ] V13. `last_assistant_message` holds the closing message as shown (smoke-test step 13). (after 0.4.1)
- [ ] V14. A builder's tool hooks carry the lead's `session_id` (else `taskByAgent` finds it by agent id), and its Bash cwd persists after `cd <copy>` (smoke-test steps 11 and 13). (2b)
- [ ] V15. Claude Code lets a subagent edit files under `.claude/worktrees/` without its own prompt (smoke-test step 11). (2b)
- [ ] V16. `git merge-tree --write-tree` behaves the same on the Linux builds' git (needs 2.38+) (smoke-test step 13). (2b)
- [ ] V17. `model: sonnet` in a plugin agent and the Agent tool's `model` override work as documented (smoke-test step 13). (2b)
- [ ] V18. The "Direction" answer reaches PostToolUse in the same shape as the checkpoint's (smoke-test step 12). (phase 3)
- [ ] V19. `git log --since-as-filter` on the Linux builds' git, and the fallback before git 2.37 (smoke-test step 12). (phase 3)
- [ ] V20. MemPalace's `since`/`before` search returns the day's sessions for the standup (smoke-test step 12). (phase 3)
