# Known limits to fix once the plugin is complete

Every known limit from phases 1a to 2b in one list. The user's call (2026-11-17): build the remaining phases first, then work through this list. Each item points to where the full text is (handoff-3.md section, gap review). Tick an item when it's fixed, with the version.

## Gates and parsing
- [ ] G1. Code run by interpreters isn't inspected (`python -c`, `node -e`, `awk 'BEGIN{system()}'`, a script file). Never runs before approval, but while building it can do anything. (handoff: after the hardening)
- [ ] G2. git's read commands accept arguments not known yet (`git diff $(git merge-base ...)`); an expansion producing `--output=` would slip through. (after the hardening)
- [ ] G3. MCP tools are judged by name; a write tool without a write verb isn't gated. (after the hardening)
- [ ] G4. zsh-only syntax that bash can't parse is refused. (after the hardening)
- [ ] G5. Monitor commands get the Bash gate before, but no changed-files check after. (after 0.4.1)
- [ ] G6. Grep asks only when its path or glob names a secret file; a folder search reaching a `.env` isn't caught, and Grep fails open. (after 0.4.1)
- [ ] G7. "In a row" for repeated blocks: allowed calls in between don't clear it; two identical refusals in one message count at once. (after 0.4.1)

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

## Not verified against Claude Code yet (smoke test, after phase 3)
The "Not verified yet" sections of handoff-3.md and docs/smoke-test.md steps 1 to 11. Confirm them before fixing the list above, since some fixes depend on how Claude Code behaves (for example whether helper hooks carry the lead's session id).
