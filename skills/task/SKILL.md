---
name: task
description: Plumbline's full task workflow for any change to code. Read the task, trace the real code, plan, one checkpoint with the user, build and prove, report. Use when the user types /plumbline:task, or after they say yes to starting the task workflow. Argument is a Jira ticket id or a description.
argument-hint: "TICKET-123 or a description"
---
# Task workflow

Task: $ARGUMENTS

Hooks enforce the gates. Your job is the thinking: dig in fully, miss nothing, stay on the task, prove the result. If you got something wrong at a checkpoint, go back a step instead of pushing on.

## 1. Read the task
- Jira ticket: read the ticket and every comment with the Jira tools (comments often change the requirements); company tasks on a ticket can't reach the checkpoint without it. List any attachment you can't open by name and ask the user to share it. Never guess what an image shows. Pass the ticket to `plumbline task start --ticket`.
- The user's own words: if vague, ask all your questions in one message, each with a suggestion.

## 2. Check the setup
- `plumbline status`. No setup: stop and suggest /plumbline:setup.
- Check MemPalace, SocratiCode and Docker once, in parallel (Serena and Context7 too if the task needs them). One fix attempt each, then ask the user. A required tool that's down means the task doesn't start.
- Start the task: `plumbline task start --title "..." --ticket ... --size small|normal|big --why "..."`. Size is impact, not lines: say why. A one-line change to auth, payments, permissions, migrations, shared helpers, money math or deploys is big. The checkpoint refuses "small" when the plan's files are a high-impact kind; change it with `plumbline task size normal --why "..."` (only before approval).

## 3. Check the code
- SocratiCode finds where to look and what depends on it. Serena finds every exact caller and definition. MemPalace answers "have we seen this before?" (never facts about the code).
- Open the real files before quoting them. Every file:line you write must be a file you opened in this task; a helper's reads don't count for you.
- Big or many-part code to trace: start tracers (`subagent_type: plumbline:tracer`, several at once, one part each, in the background). Give each the part and your own file:line findings to check. Their reports (`trace-N.md`) point you to the lines; open what you quote.
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
- Big tasks: blind QA first, before any code change. Start `subagent_type: plumbline:qa` right after the checkpoint; it writes tests from `## Cases` without seeing the new code (`qa-N.md`). While it runs, your own code edits in this checkout are refused: build through builders meanwhile, or wait. If the code already changed, the user decides whether to waive it (`plumbline waive qa`). In the report's `## Review`, say what QA's tests caught.
- Independent parts of the plan can go to builders (`subagent_type: plumbline:builder`), one per part, at the same time. Each gets its own copy of the repo made from your code as it is now, so start them after anything they build on is in place. Give each its part, the plan's files and cases for it, and your file:line notes; pass `model: sonnet` for simple, clear parts (the plan says which), and leave the session's model for tricky or risky ones. When a builder stops, read its `build-N.md`, check its work, then `plumbline builder merge <n>` (refuses overlapping edits and writes nothing then: bring those parts over yourself, then `plumbline builder drop <n>`). The finish check needs every copy merged or dropped. Small tasks: build it yourself.
- A failing test first (it fails because of the bug), then the fix, then a test for every case. Follow [playbooks.md](${CLAUDE_PLUGIN_ROOT}/skills/task/playbooks.md) for bugs, refactors and performance.
- While building: `plumbline run related --files <changed files>`. Near the end: `plumbline run test`, `plumbline run lint`, `plumbline run build`. Full-stack: start the local database, backend and frontend, then `plumbline run e2e`. A long suite can go to the tester (`subagent_type: plumbline:tester`) in the background while the reviewers work; its runs are recorded like yours.
- Real-world proof for each kind in the plan, after the last edit: `plumbline run proof api -- curl -s -o /dev/null -w "%{http_code}\n" http://localhost:.../...`, `plumbline run proof ui -- npx playwright test ...`, `plumbline run proof schema -- psql "$LOCAL_DB" -c "..."`.
- Build to the end without waiting on the user. Files outside the plan that belong to the same change are fine (list them in the report). Risky files outside the plan and the user's unsaved work are refused: keep going, and put them under "For you". Packages and outside-the-laptop commands only if listed in "Needs your OK".
- A real decision (see step 7) is asked right away. Before any stop, finish what doesn't depend on the answer.
- Requirements change mid-task: `plumbline task replan`, update plan.md, ask the checkpoint again. Unrelated: suggest a separate task.
- 3 failed fix attempts: stop, say what you tried and learned, and give options.

## 9. Review
- After the proof passes, start every reviewer in the plan's `## Team` at once, in the background: the Agent tool with `subagent_type: plumbline:code-reviewer` (always), `plumbline:plan-reviewer` (normal and big tasks), and `plumbline:security-reviewer`, `plumbline:data-reviewer`, `plumbline:ux-reviewer` where the files call for them. Plumbline gives each the notes folder, the diff base (the commit the task started from), the shared checklist and its findings file (`code-review-1.md`, ...). The finish check names any you missed, including a specialist called for by a file changed outside the plan.
- Give each reviewer the task in one line. Never pass one reviewer's findings to another: each review stays independent.
- Check every finding against the code before fixing. Fix the must-fix ones that hold up. To push back on one, write it with your evidence to `<reviewer>-disputes.md` in the notes folder (`code-reviewer-disputes.md`, ...), then run that reviewer again: it answers each dispute, withdrawn or upheld. An upheld finding is the user's call: put it under "For you" (say "upheld", with both sides).
- After fixes, re-run the related tests (and the full suite, lint and build if the fix touched more), then every reviewer again: the finish check needs each one's review of the code as it is now. Tell each what changed since its last run. At most 3 runs per reviewer (the first plus 2 fix rounds); still not clean after that, or it's a trade-off: it goes to the user.
- While reviews run in the background, wait for them; don't end with "done" meanwhile.
- A project can switch reviews off in its setup (`"parts": {"reviewer": false}`, or one reviewer by name).

## 10. Report, then the user decides
Write `report.md` after the last review (format in checkpoints.md; `## Review`, `## For you` and every waiver under Proof are required), then a short chat version: bad news first, what changed, the proof, the review, and a "For you" line ("For you: nothing" when there's nothing). The finish check sends back a closing message without it. Then ask whether to commit (and, where it applies, mention the user pushes). Commit only after they say so; the commit hook checks the proof. After the commit decision: `plumbline task finish`.

Jira ticket: write the comment to `<TICKET>-jira-comment.md` in the notes folder (`TICKET-123-jira-comment.md`): only the comment, a few plain lines a teammate reads in seconds, ready for the user to copy, check and post. No AI markers, no em dashes. Put the suggested status in the report under "For you". Don't post it: Jira is draft-only, unless the user's own message asks you to post, and then they confirm the exact text.
