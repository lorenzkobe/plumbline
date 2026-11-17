---
name: setup
description: Set up Plumbline on this laptop and in this project. Checks the tools, learns how the project runs, and saves the answers so they're asked only once.
disable-model-invocation: true
---
# Plumbline setup

Work through both parts. Ask everything you can't find out yourself in one message, each question with your suggestion. Plain words.

## Part 1: this laptop (skip what's already done; `plumbline setup show` and the checks below tell you)

Run these checks in parallel. One fix attempt per tool, then ask the user.
1. **MemPalace and SocratiCode** (required): call one cheap tool on each (for example a search). If one is missing, look up its current install steps online (don't go from memory), show them, and ask before installing.
2. **Serena** (exact lookups): check it responds. If missing, look up the current Serena docs and add it at user scope with `claude mcp add` (a confirm prompt appears). Use the context Serena provides for Claude Code, so tools Claude Code already has are turned off, and turn Serena's own memory tools off. MemPalace is the only memory.
3. **Context7** (library docs): same as Serena. It only ever sees library names and general questions, never company code.
4. **Docker** (required): `docker info`. If it's not running, try to start it once (`open -a Docker` on macOS). If it isn't installed, the user installs it.
5. **ui-ux-pro-max** (design): check whether the skill is installed; if not, ask before installing it (look up current steps).
6. **Claude Code settings**: ask whether to turn off Claude Code's built-in memory and its AI attribution on commits, and whether to add the "ask before starting a task" rule to the global CLAUDE.md. If yes, run `plumbline setup laptop --memory-off --attribution-off --claude-md` (only the parts they said yes to). It records the old values; `plumbline setup undo-laptop` puts them back.

## Part 2: this project
1. If `.claude/gate.json` exists (done-gate), read its test commands, risky paths and UI paths first so you don't ask again.
2. Learn, by reading the repo (package files, Makefile, CI config, docker-compose, README):
   - company or personal project (ask)
   - stack, test command, a related-tests command (with `{files}` where the changed files go, if the test runner supports it), lint, build, how to run the app, how to start the local database, the e2e command (Playwright) if any
   - base branch: run `git fetch` and `git branch -r`, suggest develop or dev (company) or main (personal), ask to confirm
   - Docker database: look for a compose setup; none, or it can't be committed: ask
   - frontend repo for full-stack tickets: ask which folder
   - how deploys happen (pipeline, manual command, which branch deploys where); unclear: ask
   - how production is reached (tunnel or credentials): ask. Save the method only, never a value
   - frontend checks that apply (responsive layouts, and so on): ask
   - brand kit location: repo first, then ask. Where mockups go: Claude artifacts unless the project differs
   - generated files the changed-files check should skip (coverage, snapshots, caches) as globs
   - whether to keep a glossary and decision records
3. Review CLAUDE.md, WORKFLOW.md and similar files against the real code. List what's outdated, repeated, or in conflict with Plumbline, and propose changes. Change nothing until the user approves.
4. Save. Personal projects get `.plumbline.json` committed in the repo; company projects keep it on this laptop only. Write the JSON and save it in one step (a confirm prompt appears; the user checks the commands, since saved commands are what Claude may run before approval):
   ```
   plumbline setup save <<'JSON'
   {"kind": "personal", "base_branch": "main",
    "commands": {"test": "...", "test_related": "... {files}", "lint": "...", "build": "...", "app": "...", "db_up": "...", "e2e": "..."},
    "ignore_changes": ["coverage/**"], "frontend_repo": "", "deploy_notes": "", "prod_access": "tunnel",
    "frontend_checks": [], "brand_kit": "", "mockups": "artifacts", "glossary": false}
   JSON
   ```
   Only include commands that exist. `tests_ran_pattern` (a regex with a `(?P<ran>\d+)` group) is only needed if `plumbline run test` says it couldn't tell how many tests ran.
5. Confirm by running: `plumbline run test` (and lint, build). Note how long the full suite took and save it as `suite_seconds` (save again the same way). If a command fails, re-check the repo and propose the fix; don't guess.
6. First time in this project: index it in SocratiCode to 100% and turn on its file watcher, and do one Serena lookup to confirm it works for the project's languages. If a language server needs installing, ask first.

End with a short summary: what's ready, what the user still needs to do, and anything that looked off.
