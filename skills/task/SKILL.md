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

## 5. The challenge (skip for small tasks)
- Once both files are written, start the challenger: the Agent tool with `subagent_type: plumbline:challenger`. Tell it the task in one line; Plumbline gives it the notes folder and its findings file. It reads, attacks both files, and writes `challenge-1.md`.
- Check each finding against the code before acting on it. Fix what holds up in understanding.md and plan.md; push back on the rest with evidence.
- A second round only if the first changed the plan a lot (at most 2).
- Add `## Challenge note` to plan.md: what was questioned, what changed, and anything you disagreed with and why. The checkpoint is refused without it and without a finished challenger run.
- It runs without the user, so it adds no stop. Small tasks, or a project with `"parts": {"challenger": false}` in its setup, skip it.

## 6. The checkpoint
AskUserQuestion with header `Checkpoint`, options `Approve` and `Change something`. First show a short version of the understanding, the plan and the challenge note in chat, with the file paths. Never pre-answer it.

## 7. Decisions vs common sense
- A real decision is asked right away, with options and your recommendation: a trade-off ("fixing this changes that"), a choice between approaches, anything that changes behaviour the plan didn't cover.
- Common-sense calls go ahead and are listed in the report: a test helper in the same module, formatting a file you touched, a generated file.

## 8. Build and prove
- Branch per [git.md](${CLAUDE_PLUGIN_ROOT}/skills/task/git.md). Uncommitted changes when switching branches: stop and ask.
- A failing test first (it fails because of the bug), then the fix, then a test for every case. Follow [playbooks.md](${CLAUDE_PLUGIN_ROOT}/skills/task/playbooks.md) for bugs, refactors and performance.
- While building: `plumbline run related --files <changed files>`. Near the end: `plumbline run test`, `plumbline run lint`, `plumbline run build`. Full-stack: start the local database, backend and frontend, then `plumbline run e2e`.
- Real-world proof for each kind in the plan, after the last edit: `plumbline run proof api -- curl -s -o /dev/null -w "%{http_code}\n" http://localhost:.../...`, `plumbline run proof ui -- npx playwright test ...`, `plumbline run proof schema -- psql "$LOCAL_DB" -c "..."`.
- Build to the end without waiting on the user. Files outside the plan that belong to the same change are fine (list them in the report). Risky files outside the plan and the user's unsaved work are refused: keep going, and put them under "For you". Packages and outside-the-laptop commands only if listed in "Needs your OK".
- A real decision (see step 7) is asked right away. Before any stop, finish what doesn't depend on the answer.
- Requirements change mid-task: `plumbline task replan`, update plan.md, ask the checkpoint again. Unrelated: suggest a separate task.
- 3 failed fix attempts: stop, say what you tried and learned, and give options.

## 9. Review
- After the proof passes, start the reviewer: the Agent tool with `subagent_type: plumbline:reviewer`. Plumbline gives it the notes folder, the diff base (the commit the task started from) and its findings file. It writes `review-1.md`.
- Check every finding against the code before fixing. Fix the must-fix ones that hold up; push back on the rest with evidence in the report.
- After fixes, re-run the related tests (and the full suite, lint and build if the fix touched more), then the reviewer again: the finish check needs a review of the code as it is now. At most 3 reviews (the first plus 2 fix rounds); still not clean after that, or it's a trade-off: it goes to the user.
- If a background review is running, wait for it; don't end with "done" meanwhile.
- A project with `"parts": {"reviewer": false}` in its setup skips it.

## 10. Report, then the user decides
Write `report.md` after the last review (format in checkpoints.md; `## Review` is required), then a short chat version: bad news first, "For you" for anything needing the user. Then ask whether to commit (and, where it applies, mention the user pushes). Commit only after they say so; the commit hook checks the proof. After the commit decision: `plumbline task finish`.
