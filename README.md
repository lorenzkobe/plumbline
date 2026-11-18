# Plumbline

A Claude Code plugin that runs every task the same careful way, on every project and laptop. You hand over a task and come back later: Claude digs into it, traces the real code, and stops once with the understanding and the plan. After you approve, it builds and proves to the end, stopping only for real decisions. It writes no code before you approve, and can't commit or say "done" without proof.

A plumb line is how builders check a wall is truly straight.

## What it enforces

| When | What Plumbline does |
|---|---|
| Every session | Loads the core rules (again after compaction), checks Docker, lists tasks left open from earlier sessions |
| Two sessions in one checkout | Each change is put on the session that made it: one session's edits never stop the other, and a task leaves out (and won't touch) files another session changed while it was open. When both sessions' commands run at the same moment, a change then counts for both. Only one task can be open per checkout; a second one needs its own worktree (`claude -w`) |
| Every message | A one-line reminder of the mode and the task's step |
| Before approval | Edits are blocked. Terminal commands: reading and the project's saved commands run; anything else is refused during a task (so nothing waits on you) and asks you outside one |
| The checkpoint | One stop with the understanding and the plan. Refused until SocratiCode and MemPalace (and Serena when existing code changes) were used, every file:line points to a file opened during the task (with Read, or printed by `cat`, `sed -n`, `head`, `grep` or `rg` on that file), the understanding says where Claude searched for code that already does the job (`## Reuse`), and the plan lists its files, a labelled case list linked to tests (`[failure]` and `[concurrent]` among the labels), the real-world proof, and what needs your OK (packages, outside-the-laptop commands). Plan lines that decide nothing ("TBD", "handle edge cases", "add appropriate validation") are refused |
| Approvals | Only your answer on Claude Code's question prompt moves a task forward. Pre-answered checkpoints are refused |
| Building | Runs to the end without waiting on you. Files in the plan, and files clearly part of the same change, go ahead (the latter listed in the report; more than 3 of those means the plan was wrong, so Claude asks you). Risky files outside the plan (auth, payments, migrations, dependencies, config, deploy), files that decide which checks run (CI, git hooks, test and lint config, CLAUDE.md, AGENTS.md, plugin skills and agents) and your own unsaved work are refused and listed under "For you". A plan that changes screens in a normal task gets a one-time reminder that a design task mocks them up first. Packages and outside commands run only if the approved plan lists them. Real decisions are asked right away |
| Commit | Needs passing tests on the current code (related tests for a slice, the full suite for the last commit), lint and build, no new failures, no secrets, no AI markers, the right message format. Then asks you |
| Before the checkpoint | A read-only challenger attacks the understanding and the plan (skipped for small tasks, at most 2 rounds). The checkpoint needs its finished run and a challenge note in the plan |
| After building | Read-only reviewers check the change, each on its own without seeing the others' findings: the code reviewer always, the plan reviewer for normal and big tasks, and security, data and performance, or UX and accessibility specialists when the changed files call for them. Reviewers list every finding with a score and aren't told a cutoff; the report shows those at 80 or more in full and the rest one line each, and Claude checks each against the code before fixing. Each cited file:line carries a quote that Plumbline checks against the file; the code reviewer marks every changed file covered or not (one not covered means Not ready); reviewers list what they set aside as outside the plan, and Claude rules on each in the report. Claude's disputes cite the code that shows a finding wrong, and a reviewer withdraws one only by citing the code that refutes it, after opening what the dispute cites. A reviewer's prompt can't carry other reviewers' findings or tell it what to skip. After any fix every reviewer checks again (a fresh pass first, then its earlier findings), at most 3 runs each, then it comes to you |
| While building | Builders can build independent parts of the plan, each in its own copy of the repo that Plumbline makes from your task's code (under `.claude/worktrees/`). A builder edits and runs commands only in its copy, under the same plan rules, and can't commit, push or ask you anything; Claude brings a copy back with `plumbline builder merge <n>`, which refuses overlapping edits and writes nothing then. A builder ends with a status (done, done with concerns, blocked, needs context), and a blocked one isn't merged. The setup's `builder_setup` command installs each copy's dependencies. A builder's new files outside the plan count toward the 3-file limit before they're merged. Big tasks start with blind QA: tests written from the plan's cases before any code changes, without seeing the new code; Claude's own code edits wait while it runs. Claude may later fix a QA test's setup, never what it expects, and names it on the Weakened tests line; reviewers see QA's version. A tester can run the saved checks |
| Finish | "Done" needs the proof, a review of the code as it is now from every reviewer the change needs (disputed findings answered by the reviewer, upheld ones under "For you"), every builder's copy merged or dropped, blind QA's tests on big tasks (and the report saying what they caught), a report written after the last change and the last review with a verdict (Ready, Ready with noted limitations or Not ready, plus how to roll back; plain Ready is refused while a check couldn't run, a check is waived or a reviewer upheld a finding), checks that couldn't run written as such with the command to run later (never "skipped"), the common-sense calls Claude made, every should-fix finding left unfixed, a "For you" section and every waiver, a closing chat message with a "For you" line, and for a company ticket the drafted Jira comment. Never sends Claude back twice for the same reason |
| Design | `/plumbline:design`: the brand kit, at least 3 mockup directions (phone first), and your pick on the question prompt before the plan. The checkpoint is refused until you've picked and the plan names your pick, with screenshots of the real screens as its proof |
| Devops | Commands outside the laptop run only as the approved plan lists them, and deploys, deletes, production writes and commands on other machines are still confirmed each time. Production reads ask each time. A project whose pipeline deploys (`"deploy_pipeline": true`) never deploys by hand |
| Jira | Claude reads the ticket and its comments before the checkpoint and drafts the comment in `TICKET-123-jira-comment.md` for you to copy and post. Posts, comments, status changes and edits are refused unless your own message asks for one, and then you confirm the exact text |
| Always | No `git push`, no `--no-verify`, no editing `.env` or key files, reading them (Read, a Grep that names them, or a command reading files from a listing that could include them, like `rg -l x | xargs cat`) asks you, and setup can add Claude Code deny rules so it skips them altogether, production and cloud commands ask you, installs ask you, Claude Code settings and plugins are protected, helpers can't start helpers |

Only you can pause (`/plumbline:pause`), resume, close a task (`/plumbline:cancel`), mark a block as wrong (`/plumbline:wrong`) or keep a risky change small (`/plumbline:size small`). Even when paused, the safety rules stay on.

The same refusal twice in a row tells Claude to stop and ask you instead of trying again. Proof runs are sealed with a key kept on your laptop and count only for the task and the saved command that made them. "Small" is refused at the checkpoint when the plan changes a high-impact kind of file.

**When Plumbline itself breaks** (a crash, or a binary that won't start): edits, commands, reads, Jira, the checkpoint question and helpers stay blocked, while searches (Grep, Glob, MemPalace, SocratiCode, Context7, Serena's look-ups) go through so Claude can look around and tell you. A crash report goes to `~/.plumbline/crashes/` (no tool input, code or secrets), a binary that won't start is logged in `~/.plumbline/errors.log`, and the next session start tells you. `/plumbline:stats` counts them. The way out is yours, from your own terminal: reinstall, or `claude plugin disable plumbline`.

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
| `/plumbline:design what to design` | The design workflow: brand kit, 3 mockup directions, your pick, then the task workflow |
| `/plumbline:standup [day]` | Your own work that day, by ticket, across the repos it touched (not unrelated projects), what's next from open tasks and Jira, and suggested updates to your sprint file |
| `/plumbline:status` | Where the task is, what's proven, what's missing |
| `/plumbline:stats` | Blocks per rule, send-backs, waivers, blocks marked wrong, tokens per task |
| `/plumbline:pause`, `/plumbline:resume`, `/plumbline:cancel`, `/plumbline:wrong` | Yours only |
| `/plumbline:size small [why]` | Yours only: keeps a low-impact change to a risky file small (not for files that decide which checks run) |

Claude uses the `plumbline` command (on its PATH) to start tasks and record proof: `plumbline run test`, `plumbline run related --files a,b`, `plumbline run lint`, `plumbline run build`, `plumbline run proof api -- curl ...`. Only runs recorded this way count as proof, and results go stale the moment the code changes. Builders' copies: `plumbline builder list`, `plumbline builder merge <n>`, `plumbline builder drop <n>`.

## Helpers

The challenger and the reviewers (`agents/`: `challenger`, `plan-reviewer`, `code-reviewer`, `security-reviewer`, `data-reviewer`, `ux-reviewer`) run on the same model as your session and share one checklist (`skills/task/review-checklist.md`). They can read, search and run read-only commands, and write only their own findings file in the task's notes folder (`challenge-N.md`, `code-review-N.md`, ...). A reviewer can't read another reviewer's findings or the report. Plumbline records when each starts and stops, with the code fingerprint at both ends, so a run counts only if it finished on code that didn't change under it. Specialists are called for by the changed files' paths (auth, payments, user data and secrets; migrations, queries, models and money; frontend files) and by what the change adds in any file (password hashing, tokens, permissions, SQL statements, ORM writes, prices and refunds); the plan's `## Team` can add any of them. The workers (2b) are `tracer` and `tester` (read-only, on sonnet: tracers trace code and report with file and line; the tester runs only `plumbline run test|related|lint|build|e2e` and writes `test-N.md`), `builder` and `qa` (same model as your session). A builder gets its own copy of the repo at start, made from the lead's code as it is (uncommitted work included), and writes `build-N.md`; `plumbline builder list|merge <n>|drop <n>` handles the copies, and a copy with no changes is removed when the builder stops. Blind QA writes only test files (by name, or the setup's `test_files` patterns) and `qa-N.md`, can start only while nothing but tests changed since the plan was approved, and can't read the builders' copies or other helpers' notes; on big tasks the finish check needs it (or your waiver, `plumbline waive qa`). No helper ever prompts you: anything that would need your OK is refused and goes to the lead. These rules are guard rails against mistakes, not a sandbox: code a builder or QA runs (a test, a script) can reach further than the gates see. For builders, whatever lands in your main checkout is caught after the command and listed for you. Only Claude's own reads and searches count as "opened": a helper's report points to the lines, and Claude opens what it quotes. Worktrees share their main checkout's setup, tasks, proof and pause. Switch parts off per project in the setup file: `"parts": {"challenger": false}`, `"reviewer": false` (all reviews), one reviewer by name (`"ux-reviewer": false`), `"jira": false` (the ticket read and the drafted comment; the refusal to post stays), a worker by name (`"builder": false`, `"qa": false`, `"tracer": false`, `"tester": false`), `"design": false` (the direction pick) or `"devops": false` (the per-command confirm and the hand-deploy refusal; the basic prompt for outside commands stays).

claude.ai connectors other than Claude Docs (Slack, Gmail, Atlassian, ...) are judged by tool name like any MCP server: tools that send or change things wait for the approved plan during a task and ask you outside one. A tool whose name says neither read nor write (`linear__issue`, `browser_navigate`) counts as a change; list the ones that only read in the setup (`"mcp_read_tools": {"playwright": ["browser_navigate"]}`). A name that says write stays a write.

## Where things live

- Your data: `~/.plumbline/` (task state, proof runs, logs). Never in the repo.
- Project setup: `.plumbline.json` in the repo for personal projects; `~/.plumbline/projects/<project>/setup.json` for company projects.
- Your sprint file (optional, for `/plumbline:standup`): wherever you keep it; `plumbline standup tracker --set <path>` remembers it per project. One inside a repo must be ignored by git. Edits to it always ask you.
- Logs: kept 6 months, then rolled into monthly number summaries kept 1 year. No code, secrets or ticket text.

## Develop

```
make test     # go vet + the test cases, including the try-to-break ones
make build    # cross-compile dist/ for darwin arm64/amd64, linux amd64/arm64
make check    # both, then claude plugin validate
```

Every change re-runs the test cases before it reaches a laptop. Rule changes follow "replace, don't patch".
