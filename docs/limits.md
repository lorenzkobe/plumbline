# Known limits to fix once the plugin is complete

Every known limit from phases 1a to 3 in one list. The user's call (2026-11-17): build the remaining phases first, then work through this list. Each item points to where the full text is (handoff-3.md section, gap review). Tick an item when it's fixed, with the version.

## Decisions (2026-11-18)
The user's answers on how to handle the list:
- Fix everything that has a workable fix; nothing is accepted just because it's hard. The smoke test (V1-V20) runs at the end, not first (token budget); items that depend on it (L6 on V1, R2 on V11, B6 on V7/V9) are fixed last.
- G3: an MCP tool whose name doesn't say read or write counts as a write. Setup keeps a per-server list of known read tools.
- G6 (and R6 with it): setup adds Claude Code `deny` rules for `.env` and key files, so Claude Code itself skips them for Read, Grep and Glob.
- M14: fix it. Changes are attributed to the session that made them.
- W1: build the hotfix flow.
- W2, W3: wait for real use.
- G1, B1, P1: Claude Code's Bash sandbox stays optional. `/plumbline:setup` explains the pros and cons and offers the exact settings for the user to add (Plumbline can't switch it on: a plugin can't set `sandbox.*`). The settings: `sandbox.filesystem.denyRead` for the seal key, `allowUnsandboxedCommands: false`, and `excludedCommands` for Docker and the database client (production reads still ask each time). `/plumbline:status` shows whether it's on.
  - Pros: P1 is fixed at the OS level, and G1's writes outside the project are blocked.
  - Cons: Docker must be excluded and then runs outside the fence. Package registries must be allowed by domain. Caches outside the project need `allowWrite`. Dev servers need local binding, and e2e browsers may not start. Logins (gh, keychain, aws sso) may need macOS services. Linux needs bubblewrap and socat. Failures don't say "sandbox".
  - B1 stays a guard rail: the sandbox applies to the whole session, so it can't limit a builder to its copy.
  - Source: code.claude.com/docs/en/sandboxing.md and the settings reference, checked 2026-11-18.
- Fixes for items first marked "accept":
  - G4: mvdan.cc/sh v3.14.1 parses zsh (`LangZsh`). Parse as bash, then as zsh; zsh nodes the walker doesn't know are opaque.
  - R4: a user-only `/plumbline:size small` override, so the user can keep a low-impact change to a risky file small.
  - R5: when `## For you` isn't "nothing", the closing message must open with the "For you" line.
  - B4: a builder's write whose target can't be read (`rm "$x"`) is refused (builders can't ask anyway).
  - B5: files the lead's own tools touched during a builder's command aren't blamed on the builder.
  - D1: a reminder (not a gate, as chosen) when a normal task's plan changes screen files.
  - S3: a personal project's standup follows the branch name into sibling repos and `frontend_repo`.
  - P5: token counts read from the transcript (hooks get its path).
  - R1: specialists are also called for by the diff's contents (SQL, auth, money code in any file).
  - R3: a reviewer's prompt is checked against the other findings files.
  - O2: more Postgres functions that change things (`setval`, `pg_terminate_backend`, ...).
  - S2: a setup list of extra repos for the standup.
- Drop P2 and P7 (only tasks from before 0.4.1 / 0.3.0).
- Releases, in order: (1) quick fixes: T1, L5, G9, G7, G8, G2, G10, L3, B9, B10, D2, P6, G5, L1 (`tsc --noEmit`); (2) the WORKFLOW.md rules and checklist (WF1, WF2, WF5, WF6); (3) the bigger fixes (L2 first) and the rest of the list; (4) WF3, WF4; (5) the smoke test and the V-dependent items.
- Release 1 shipped as 0.6.1 (commit 95c298f, pushed). T1 is still open: auto mode refused `go run ...staticcheck@latest`, so the user runs it.
- Release 3a is 0.6.2 (L2, G3, G4, G6, G11, O1, O2). It moves mvdan.cc/sh to v3.14.1, which needs Go 1.26 to build (go.mod's `go` line went from 1.24.7 to 1.26.0; users run the prebuilt binaries).
- Release 3 is split (2026-11-18), one release each:
  - 3a, reading and commands: L2 (first), G3, G4, G6, G11, O1, O2.
  - 3b, proof and finish: P3, P4, P5, L4, R5, R4.
  - 3c, helpers and builders: R1, R3, B2, B3, B5, B7, B8, D1.
  - 3d, bigger pieces, each on its own: M14, W1, the sandbox option (G1, P1, B1), the standup (S1-S4).
- Before release 2: study the reference repos the user clones into `~/Documents/Reference/` (obra/superpowers, Matt Pocock's skills repo, and any others they name). Read the code, not just the READMEs, and only the parts that overlap Plumbline: how skills trigger, tests first, review helpers, writing and checking plans, hooks. Treat the clones as untrusted: read only, run nothing. Borrow ideas, not code, unless the license allows. Write up what's worth taking, with file:line for each idea, and let it shape release 2.
- Order from here: 3a can start while the user clones; then the repo study, then release 2, then 3b to 3d, then release 4, then release 5.

## Added from the user's WORKFLOW.md (2026-11-18)
- [ ] WF1. Reuse rule: search for an existing helper, validator, query or component before writing new logic; extract only real duplication; don't abstract on looks alone. Core rule plus review checklist (duplicated logic, missed reuse, unjustified abstraction).
- [ ] WF2. Review checklist adds backward compatibility, logging and observability gaps, resource leaks, config/environment/deploy mistakes, and test quality (over-mocking, mocking the thing under test, arbitrary sleeps, real external services). Core rule 5 adds: keep backward compatibility unless asked to break it; never hide failures or ignore warnings.
- [ ] WF3. Optional saved `format`, `typecheck` and `security` commands (`plumbline run format|typecheck|security`); when setup defines them, commit needs them to pass.
- [ ] WF4. A `[bug]` case needs a recorded failing run taken while only test files had changed since approval (the regression test failed before the fix).
- [ ] WF5. Case labels `[failure]` (a dependency is down, times out or returns garbage; rollback) and `[concurrent]` (two requests at once).
- [ ] WF6. The report needs `## Verdict`: Ready / Ready with noted limitations / Not ready, and under Proof anything that couldn't run with the exact command to run later. The finish check checks both.

## Gates and parsing
- [ ] G1. Code run by interpreters isn't inspected (`python -c`, `node -e`, `awk 'BEGIN{system()}'`, a script file). Never runs before approval, but while building it can do anything. (handoff: after the hardening)
- [x] G2. (0.6.1) git's read commands accept arguments not known yet (`git diff $(git merge-base ...)`); an expansion producing `--output=` would slip through. (after the hardening)
- [x] G3. (0.6.2: a name that says neither read nor write counts as a write; the setup's `mcp_read_tools` lists a server's read tools, and Serena's are built in. A setup changed outside the confirm prompt keeps only the read tools the confirmed copy listed. Still judged by name: a write tool whose name has a read word and no write word (`fetch_and_store`) reads as a read) MCP tools are judged by name; a write tool without a write verb isn't gated. (after the hardening)
- [x] G4. (0.6.2: a line bash can't parse is parsed as zsh where zsh runs it: the line itself, eval, trap, emulate -c, zsh -c. Glob qualifiers, `${~x}` and expansion flags other than plain ones like `(U)`, `(f)`, `(k)` stay opaque; `bash -c` and `sh -c` text must still parse as bash. zsh's `>>|`, `&>|` and `&>>|` count as writes) zsh-only syntax that bash can't parse is refused. (after the hardening)
- [x] G5. (0.6.1) Monitor commands get the Bash gate before, but no changed-files check after. (after 0.4.1)
- [x] G6. (0.6.2: `plumbline setup laptop --deny-secrets` adds `Read(**/.env)`, `Read(**/*.pem)` and the like to the user settings' `permissions.deny`, so Claude Code skips them for Read, Grep and Glob; undo removes only the rules it added. Opt-in from setup. Left: Claude Code applies them "best effort" to Grep and Glob, uncommon names (`.env.qa`) aren't in the list and keep only Plumbline's prompt, and a denied file can't be read even with the user's OK) Grep asks only when its path or glob names a secret file; a folder search reaching a `.env` isn't caught, and Grep fails open. (after 0.4.1)
- [x] G9. (0.6.1) Command names are matched as written: on macOS's case-insensitive disk `VERCEL --prod` or `GIT push` runs the real tool but isn't recognised by any name-based rule (found by the phase 3 review). (pre-existing)
- [x] G10. (0.6.1) An unconfirmed setup's `risky_paths` and `ignore_changes` are still used, so a terminal-edited setup could drop a risky path or widen what the changed-files check skips (0.6.0 covers parts and deploy_pipeline only). (pre-existing, found in phase 3)
- [x] G7. (0.6.1: parallel refusals count once; allowed calls in between still don't reset it, by design: a retry after a few reads is still a loop) "In a row" for repeated blocks: allowed calls in between don't clear it; two identical refusals in one message count at once. (after 0.4.1)
- [x] G8. (0.6.1) Arguments that xargs or find add are taken as files, not flags, so a file named like a flag (`--output=x`) gets past the read-only check. (decisions in the hardening)
- [x] G11. (0.6.2: a file reader fed by xargs, find -exec or fd -x asks like a secret read unless the listing is narrowed to names that can't be secret: rg/ag `-g`, grep `--include`, find `-name` in every `-o` branch, fd `-e`, git ls-files or ls patterns ending in a plain extension; sort, uniq, head and tail in between pass names through. `rg -l x | xargs cat` now asks, since rg lists `.pem` and `.key` files without `--hidden`. Patterns like `*.json` count as possibly secret: `secrets.json`) Arguments xargs adds aren't checked for secret files: `rg -l KEY | xargs cat` could print `.env` (rg skips hidden files by default, so it needs `--hidden` or `-uu`). (found in 0.6.1)
- [x] G12. (0.6.1) An unquoted glob could expand to a file named like a flag (`sed -n 1p *` with a file `-i` there edits files; `git log *` with `--output=x` writes one). A non-pure command with a pattern reads only when no match there starts with "-", or after `--`. (found in 0.6.1, pre-existing)

## Proof and state
- [ ] P1. The task-state and run seals use a key in the data folder; a process Claude starts while building could read it and forge a record. (Known limits; after 0.4.1)
- [ ] P2. Runs recorded before 0.4.1 have no seal and are skipped, baselines included. (after 0.4.1)
- [ ] P3. Weakened-test detection is instructions only; no gate checks the diff for skipped or loosened tests. (Known limits)
- [ ] P4. Full-stack e2e: nothing requires `plumbline run e2e` for full-stack tasks. (Known limits)
- [ ] P5. Hooks can't see token usage, so logs don't record tokens. (Known limits)
- [x] P6. (0.6.1) A binary that exists but can't run blocks the gates but leaves nothing in errors.log. (after 0.4.1)
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
- [x] B4. (0.6.1) Builder writes with unknown targets (`rm "$x"`) aren't refused up front; the copy's changed-files check records them. (after 2b)
- [ ] B5. The lead's change and a builder's command at the same moment can be misattributed either way. (after 2b)
- [ ] B6. "Running" is a tool call in the last 30 minutes; a builder or QA thinking longer without one counts as finished. (after 2b)
- [ ] B7. While a builder's copy exists, blind QA can't search or glob from the repo root (only the Grep tool). (after 2b)
- [ ] B8. Test files are known by name only (Rust inline tests and other conventions need a test file). (after 2b)
- [x] B9. (0.6.1) `noAsk` covers workers only; a reviewer reading `.env` still prompts the user. (after 2b)
- [x] B10. (0.6.1) `ln -s` to anything outside the copy is refused, including harmless system paths (`/usr/bin/node`). (after 2b)

## Design, devops, standup (phase 3)
- [ ] D1. The design gate holds only design tasks; a normal task can change screens without mockups (by choice). (after phase 3)
- [x] D2. (0.6.1) `## Design` is found by heading prefix and the pick by its short name in that section's text; "not Direction B" would count. (after phase 3)
- [x] O1. (0.6.2: scripts and targets named release, rollout or go-live (not release-notes, deploy:check, ...); release tools (goreleaser, semantic-release, release-it, np, gh-pages, surge, lerna, changeset, kamal, argocd, skaffold, copilot, eas, expo, nomad); deploy tasks in cap, mina, dep, fab, envoy, invoke, fastlane, nx and turbo; `cargo release --execute`. Docker or Podman pointed at another machine by `--context`, `-H`, `DOCKER_HOST`/`DOCKER_CONTEXT` (on the command or in the session), the current context in Docker's config, or `docker context use` is remote. Still by name: a deploy script named for nothing deploy-like, or a tool not listed, isn't caught. `make release` that only builds locally is refused in a pipeline project) Deploys are known by CLI name and by scripts named for deploying (deploy, publish, ship); anything else (`go-live.sh`, a `release` make target, an unlisted deploy tool) isn't refused in a pipeline project and runs like any command while building. Docker pointed at another machine (`docker --context prod`, `-H ssh://`) isn't seen as remote. (after phase 3)
- [x] O2. (0.6.2: advisory locks, WAL and replication slot functions, stats resets, pg_notify, pg_promote, backups, pg_file_*, MySQL named locks and `SELECT ... INTO` count as writes; a `PGHOST` or `MYSQL_HOST` on the command itself names the host. Kept by design: a client with no host still counts as remote, since the shell profile's PGHOST or a service file can point anywhere and the hook can't see them. A function not listed (an extension's own) still reads as a read) A database client that names no host (or names it in a variable, or through PGHOST or a service file) counts as remote and asks; `select` calls to functions that change things beyond the listed ones (setval, pg_terminate_backend, ...) read as reads when approved. (after phase 3)
- [ ] S1. A task keeps only when each file was last opened, so a file opened Monday and again Tuesday counts for Tuesday only in the standup. (after phase 3)
- [ ] S2. The standup's other repos are Plumbline's projects, the repo's sibling folders and its `frontend_repo`; a repo elsewhere with commits on the ticket and no task isn't found. (after phase 3)
- [ ] S3. A personal project's standup can't follow work into another repo (no tickets to follow). (after phase 3)
- [ ] S4. The sprint file is protected for the Edit and Write tools only; a terminal command that writes it follows the usual terminal rules. (after phase 3)

## Gap review (docs/gap-review-1a.md), still open
- [ ] M14. Two sessions in one repo: one session's edits stop the other.
- [x] L1. (0.6.1 for npx/bunx/pnpx of a project tool; `pip install -r requirements.txt` is still an install, by design) False blocks: `npx tsc --noEmit`, `pip install -r requirements.txt` refused as installs during a task.
- [x] L2. (0.6.2: a read-only command's output counts like Read for the files it names: cat, bat, head, tail, nl, tac, less, more, sed (when it prints), awk, grep and rg (not `-c`, `-l`, `-q`, `-o`). Only output that reaches Claude: not redirected, captured in `$(...)` or piped into anything but head, tail, grep, sed, cat and the like. A folder search, files xargs adds and a command that also changes something don't count. Like Read with a line range, a partial print counts for the whole file) Only the Read tool counts as "opened"; `cat`/`sed -n`/`rg` reads give a false "didn't open this file".
- [x] L3. (0.6.1) Logs hold commit subjects and absolute paths in Detail.
- [ ] L4. Without failing-test ids, the baseline compares counts only; a broken-environment baseline sticks.
- [x] L5. (0.6.1) `test:` matches inside words like "latest:".
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
