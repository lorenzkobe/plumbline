# Plumbline

A Claude Code plugin that runs every task the same careful way, on every project and laptop. You hand over a task and come back later: Claude digs into it, traces the real code, and stops once with the understanding and the plan. After you approve, it builds and proves to the end, stopping only for real decisions. It writes no code before you approve, and can't commit or say "done" without proof.

A plumb line is how builders check a wall is truly straight.

## What it enforces

| When | What Plumbline does |
|---|---|
| Every session | Loads the core rules (again after compaction), checks Docker, lists tasks left open from earlier sessions |
| Every message | A one-line reminder of the mode and the task's step |
| Before approval | Edits are blocked. Terminal commands: reading and the project's saved commands run; anything else is refused during a task (so nothing waits on you) and asks you outside one |
| The checkpoint | One stop with the understanding and the plan. Refused until SocratiCode and MemPalace (and Serena when existing code changes) were used, every file:line points to a file opened during the task, and the plan lists its files, a labelled case list linked to tests, the real-world proof, and what needs your OK (packages, outside-the-laptop commands) |
| Approvals | Only your answer on Claude Code's question prompt moves a task forward. Pre-answered checkpoints are refused |
| Building | Runs to the end without waiting on you. Files in the plan, and files clearly part of the same change, go ahead (the latter listed in the report; more than 3 of those means the plan was wrong, so Claude asks you). Risky files outside the plan (auth, payments, migrations, dependencies, config, deploy) and your own unsaved work are refused and listed under "For you". Packages and outside commands run only if the approved plan lists them. Real decisions are asked right away |
| Commit | Needs passing tests on the current code (related tests for a slice, the full suite for the last commit), lint and build, no new failures, no secrets, no AI markers, the right message format. Then asks you |
| Before the checkpoint | A read-only challenger attacks the understanding and the plan (skipped for small tasks, at most 2 rounds). The checkpoint needs its finished run and a challenge note in the plan |
| After building | Read-only reviewers check the change, each on its own without seeing the others' findings: the code reviewer always, the plan reviewer for normal and big tasks, and security, data and performance, or UX and accessibility specialists when the changed files call for them. Only findings scored 80 or more are reported; Claude checks each against the code before fixing. After any fix every reviewer checks again, at most 3 runs each, then it comes to you |
| Finish | "Done" needs the proof, a review of the code as it is now from every reviewer the change needs, a report written after the last change and the last review, and for a company ticket the drafted Jira comment. Never sends Claude back twice for the same reason |
| Jira | Claude reads the ticket and its comments before the checkpoint and drafts the comment in `TICKET-123-jira-comment.md` for you to copy and post. Posts, comments, status changes and edits are refused unless your own message asks for one, and then you confirm the exact text |
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

## Helpers

The challenger and the reviewers (`agents/`: `challenger`, `plan-reviewer`, `code-reviewer`, `security-reviewer`, `data-reviewer`, `ux-reviewer`) run on the same model as your session and share one checklist (`skills/task/review-checklist.md`). They can read, search and run read-only commands, and write only their own findings file in the task's notes folder (`challenge-N.md`, `code-review-N.md`, ...). A reviewer can't read another reviewer's findings or the report. Plumbline records when each starts and stops, with the code fingerprint at both ends, so a run counts only if it finished on code that didn't change under it. Specialists are called for by the changed files' paths (auth, payments, user data and secrets; migrations, queries, models and money; frontend files); the plan's `## Team` can add any of them. Switch parts off per project in the setup file: `"parts": {"challenger": false}`, `"reviewer": false` (all reviews), one reviewer by name (`"ux-reviewer": false`), or `"jira": false` (the ticket read and the drafted comment; the refusal to post stays).

claude.ai connectors other than Claude Docs (Slack, Gmail, Atlassian, ...) are judged by tool name like any MCP server: tools that send or change things wait for the approved plan during a task and ask you outside one.

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
