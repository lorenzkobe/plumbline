---
name: task
description: Plumbline's full task workflow for any change to code. Read the task, trace the real code, plan, one checkpoint with the user, build and prove, report. Use when the user types /plumbline:task, or after they say yes to starting the task workflow. Argument is a Jira ticket id or a description.
argument-hint: "TICKET-123 or a description"
---
# Task workflow

Task: $ARGUMENTS

Hooks enforce the gates. Your job is the thinking: dig in fully, miss nothing, stay on the task, prove the result. If you got something wrong at a checkpoint, go back a step instead of pushing on.

## 1. Read the task
- Jira ticket: read the ticket and every comment (comments often change the requirements). List any attachment you can't open by name and ask the user to share it. Never guess what an image shows.
- The user's own words: if vague, ask all your questions in one message, each with a suggestion.

## 2. Check the setup
- `plumbline status`. No setup: stop and suggest /plumbline:setup.
- Check MemPalace, SocratiCode and Docker once, in parallel (Serena and Context7 too if the task needs them). One fix attempt each, then ask the user. A required tool that's down means the task doesn't start.
- Start the task: `plumbline task start --title "..." --ticket ... --size small|normal|big --why "..."`. Size is impact, not lines: say why. A one-line change to auth, payments, permissions, migrations, shared helpers, money math or deploys is big.

## 3. Check the code
- SocratiCode finds where to look and what depends on it. Serena finds every exact caller and definition. MemPalace answers "have we seen this before?" (never facts about the code).
- Open the real files before quoting them. Every file:line you write must be a file you opened in this task.
- Check the ticket against the code. Note anything outside the ticket (a bigger problem, the same bug elsewhere).
- For library questions, Context7 for the exact versions in the lockfile, then the web. Never send company code, ticket text or names.
- Bugs: reproduce first with the saved commands (allowed before approval), e.g. `plumbline run test`. The first runs are recorded as the task's starting point, so failures that were already there won't block you later.

## 4. Make the plan (no stop yet)
The user usually hands over the task and comes back later, so research and plan in one go.
Write `understanding.md` and `plan.md` in the notes folder (`plumbline docs-dir`), using [checkpoints.md](${CLAUDE_PLUGIN_ROOT}/skills/task/checkpoints.md): what changes and where, `## Files`, `## Cases` linked to tests, `## Real-world proof`, `## Needs your OK` (packages, outside-the-laptop commands), the checks, the branch, and which helpers will run. Real choice on a big task: recommend one approach and plan it in full, with the others in a few lines each so the user can pick at the same stop.
If the task is so unclear that planning would likely be wasted, ask your questions first (one message, each with a suggestion), or use the optional `Checkpoint 1` for the understanding alone.

## 5. The checkpoint
AskUserQuestion with header `Checkpoint`, options `Approve` and `Change something`. First show a short version of the understanding and the plan in chat, with both file paths. Never pre-answer it.

## 6. Decisions vs common sense
- A real decision is asked right away, with options and your recommendation: a trade-off ("fixing this changes that"), a choice between approaches, anything that changes behaviour the plan didn't cover.
- Common-sense calls go ahead and are listed in the report: a test helper in the same module, formatting a file you touched, a generated file.

## 7. Build and prove
- Branch per [git.md](${CLAUDE_PLUGIN_ROOT}/skills/task/git.md). Uncommitted changes when switching branches: stop and ask.
- A failing test first (it fails because of the bug), then the fix, then a test for every case. Follow [playbooks.md](${CLAUDE_PLUGIN_ROOT}/skills/task/playbooks.md) for bugs, refactors and performance.
- While building: `plumbline run related --files <changed files>`. Near the end: `plumbline run test`, `plumbline run lint`, `plumbline run build`. Full-stack: start the local database, backend and frontend, then `plumbline run e2e`.
- Real-world proof for each kind in the plan, after the last edit: `plumbline run proof api -- curl -s -o /dev/null -w "%{http_code}\n" http://localhost:.../...`, `plumbline run proof ui -- npx playwright test ...`, `plumbline run proof schema -- psql "$LOCAL_DB" -c "..."`.
- Build to the end without waiting on the user. Files outside the plan that belong to the same change are fine (list them in the report). Risky files outside the plan and the user's unsaved work are refused: keep going, and put them under "For you". Packages and outside-the-laptop commands only if listed in "Needs your OK".
- A real decision (see step 6) is asked right away. Before any stop, finish what doesn't depend on the answer.
- Requirements change mid-task: `plumbline task replan`, update plan.md, ask the checkpoint again. Unrelated: suggest a separate task.
- 3 failed fix attempts: stop, say what you tried and learned, and give options.

## 8. Report, then the user decides
Write `report.md` (format in checkpoints.md), then a short chat version: bad news first, "For you" for anything needing the user. Then ask whether to commit (and, where it applies, mention the user pushes). Commit only after they say so; the commit hook checks the proof. After the commit decision: `plumbline task finish`.
