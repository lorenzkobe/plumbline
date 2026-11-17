# Plumbline

A Claude Code plugin that runs every task the same careful way, on every project and laptop. Claude digs into the task, traces the real code, stops at two checkpoints with you, writes no code before you approve the plan, and can't commit or say "done" without proof.

A plumb line is how builders check a wall is truly straight.

## What it enforces

| When | What Plumbline does |
|---|---|
| Every session | Loads the core rules (again after compaction), checks Docker, lists tasks left open from earlier sessions |
| Every message | A one-line reminder of the mode and the task's step |
| Before approval | Edits are blocked. Terminal commands: reading and the project's saved commands run; anything else asks you |
| Checkpoint 1 | Refused until SocratiCode and MemPalace (and Serena when existing code changes) were used, and every file:line points to a file opened during the task |
| Checkpoint 2 | Refused until the plan lists its files, a labelled case list linked to tests, and the real-world proof |
| Approvals | Only your answer on Claude Code's question prompt moves a task forward. Pre-answered checkpoints are refused |
| Building | Files in the plan edit freely; any other file asks you. A terminal command that changes files outside the plan stops the work |
| Commit | Needs passing tests on the current code (related tests for a slice, the full suite for the last commit), lint and build, no new failures, no secrets, no AI markers, the right message format. Then asks you |
| Finish | "Done" needs the proof plus a report written after the last change. Never sends Claude back twice for the same reason |
| Always | No `git push`, no `--no-verify`, no editing `.env` or key files, reading them asks you, production and cloud commands ask you, installs ask you, Claude Code settings and plugins are protected, helpers can't start helpers |

Only you can pause (`/plumbline:pause`), resume, close a task (`/plumbline:cancel`) or mark a block as wrong (`/plumbline:wrong`). Even when paused, the safety rules stay on.

## Install (macOS or Linux)

```
claude plugin marketplace add lorenzkobe/plumbline
claude plugin install plumbline@plumbline
```

Then, in Claude Code: `/plumbline:setup` (once per laptop and once per project).

No Go needed: prebuilt binaries for Apple Silicon, Intel Mac and Linux ship in `dist/`. Needs Claude Code v2.1.295 or later (gate hooks use `onFailure: block`).

## Commands

| Command | What it does |
|---|---|
| `/plumbline:setup` | Checks the tools, learns the project, saves the answers |
| `/plumbline:task TICKET-123` | The full task workflow |
| `/plumbline:status` | Where the task is, what's proven, what's missing |
| `/plumbline:stats` | Blocks per rule, send-backs, waivers, blocks marked wrong |
| `/plumbline:pause`, `/plumbline:resume`, `/plumbline:cancel`, `/plumbline:wrong` | Yours only |

Claude uses the `plumbline` command (on its PATH) to start tasks and record proof: `plumbline run test`, `plumbline run related --files a,b`, `plumbline run lint`, `plumbline run build`, `plumbline run proof api -- curl ...`. Only runs recorded this way count as proof, and results go stale the moment the code changes.

## Where things live

- Your data: `~/.plumbline/` (task state, proof runs, logs). Never in the repo.
- Project setup: `.plumbline.json` in the repo for personal projects; `~/.plumbline/projects/<project>/setup.json` for company projects.
- Logs: kept 6 months, then rolled into monthly number summaries kept 1 year. No code, secrets or ticket text.

## Develop

```
make test     # go vet + the test cases, including the try-to-break ones
make build    # cross-compile dist/ for darwin arm64/amd64, linux amd64/arm64
make check    # both, then claude plugin validate
```

Every change re-runs the test cases before it reaches a laptop. Rule changes follow "replace, don't patch".
