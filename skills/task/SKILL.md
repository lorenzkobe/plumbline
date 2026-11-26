---
name: task
description: Plumbline's task workflow. Use when the user types /plumbline:task, or asks for any change to code (a bug fix, a feature, a refactor, a config or dependency change). Argument is a Jira ticket id or a description.
argument-hint: "TICKET-123 or a description"
---
# Task workflow

Task: $ARGUMENTS

Hooks enforce the gates and say what's missing when you hit one; this page is the order of work. Dig in fully, stay on the task, prove the result. If you got something wrong at a checkpoint, go back a step instead of pushing on.

## 1. Read the task
- Jira ticket: read the ticket and every comment with the Jira tools (comments often change the requirements). List any attachment you can't open by name and ask the user to share it; never guess what an image shows. Pass the ticket to `plumbline task start --ticket`.
- The user's own words: if vague, ask all your questions in one message, each with a suggestion.

## 2. Check the setup and start
- `plumbline status`. No setup: stop and suggest /plumbline:setup.
- Check MemPalace, SocratiCode and Docker once, in parallel (Serena and Context7 too if the task needs them). One fix attempt each, then ask the user. A required tool that's down means the task doesn't start.
- Start right away, without asking (the plan question is the user's one stop): `plumbline task start --title "..." --ticket ... --size small|normal|big --why "..."`, on its own line (no `&&`), so the task is tied to this session.
- Size is impact, not lines: say why. Small: a rename, a typo, a local fix. Never small: how often or how much the app talks to a server or what runs in the background (retries, polling, timers, caching), even one number; the built-in high-impact kinds (auth, permissions, payments, migrations, deploy, dependencies, security code; only the user can keep one small, with /plumbline:size small, and you never suggest it); files that decide which checks run. A file on the setup's risky list can still be small when the change can't reach its behaviour: say why in --why. Change it with `plumbline task size` before approval.

## 3. Check the code
- SocratiCode finds where to look and what depends on it, Serena every exact caller and definition, MemPalace "have we seen this before?" (never facts about the code).
- Every file:line you write must be a file you opened in this task (Read, or `cat`/`sed -n`/`head`/`grep -n`/`rg -n` printing it to you); a helper's reads don't count for you.
- Trace the code yourself. Tracers (`plumbline:tracer`, in the background, one part each, with your own file:line findings to check) only for a big task, or a part too large to read yourself; each costs a full helper run. Open what you quote from their `trace-N.md`.
- A codemod or one mechanical change over many files is one task: list the folder in `## Files` (`src/app/api/superadmin/` or `src/app/api/superadmin/**`; risky and check-deciding files under it still need their own line), and review the script plus a sample of its output rather than splitting the work into runs.
- Check the ticket against the code; note anything outside it (a bigger problem, the same bug elsewhere).
- Reuse: search by the idea, past the files you have open, for code that already does the job. Write the searches and results under `## Reuse` in understanding.md.
- Library questions: Context7 for the lockfile's versions, then the web. Never send company code, ticket text or names.
- Bugs: reproduce first (`plumbline run test`, one test file, or a script that only looks; all run before approval).
- Package changes (a manifest or lockfile in `## Files`, or an install in "Needs your OK"): run `plumbline run checks` before the checkpoint, so the same commands can be compared after.

## 4. Make the plan (no stop yet)
Research and plan in one go; the user usually comes back later. Formats are in [checkpoints.md](${CLAUDE_PLUGIN_ROOT}/skills/task/checkpoints.md).
- Small: only `plan.md`: `## Goal`, what changes with file:line, `## Files`, `## Cases` linked to tests, `## Guards loosened`.
- Normal and big: `understanding.md` and `plan.md` in the notes folder (`plumbline docs-dir`), with the Real-world proof, Needs your OK, Checks, Branch and Team sections too. Real choice on a big task: recommend one approach in full, the others in a few lines each.
- Every line decides something ("TBD" or "handle edge cases" is refused).
- Too unclear to plan: ask first (one message, each question with a suggestion), or use the optional `Checkpoint 1` for the understanding alone.

## 5. The challenge (normal and big)
- Start `plumbline:challenger` once both files are written; tell it the task in one line. Check each finding against the code; fix what holds up, push back on the rest with evidence. A second round only if the first changed the plan a lot (at most 2).
- Add `## Challenge note` to plan.md: what was questioned, what changed, what you disagreed with and why.
- A guard in `## Guards loosened` (any size): also `plumbline:guard-reviewer`, answered in the Challenge note.
- `"parts": {"challenger": false}` in the setup skips it.

## 6. The checkpoint
Show the plan in the chat as a message (checkpoints.md's "The plan in chat"), then AskUserQuestion, header `Checkpoint`, options `Approve`, `Approve and commit when green`, `Change something`, `Drop it`. The question itself carries the problem, the fix and the risk in two or three sentences, since it can cover the chat. Never pre-answer it. "Approve and commit when green" means the last commit runs without a second question once the finish check has nothing left and the verdict is plain Ready (never an amend); plain Approve keeps the commit question.

## 7. Decisions vs common sense
- A real decision is asked right away, with options and your recommendation: a trade-off, a choice between approaches, behaviour the plan didn't cover. Finish what doesn't depend on the answer first.
- Common-sense calls go ahead and are listed in the report: a test helper in the same module, formatting a file you touched, a generated file.

## 8. Build
- Branch per [git.md](${CLAUDE_PLUGIN_ROOT}/skills/task/git.md). Uncommitted changes when switching branches: stop and ask.
- Right after approval: `plumbline run checks` records lint, build and the other saved checks (no suite first). When a test run fails later, Plumbline runs the suite once on the approved code in a temporary copy, so failures that were already there don't count as the task's. (Hotfixes and tasks approved before 0.8.3 also run the suite first; the edit refusal says what's needed.)
- Big tasks: blind QA first (`plumbline:qa`, right after the checkpoint); it writes tests from `## Cases` without seeing the new code. While it runs, your code edits here are refused: build through builders or wait. You may fix a QA test's setup when the code's shape moved, never what it expects; name the file on the Weakened tests line. Say in `## Review` what QA's tests caught.
- Builders (`plumbline:builder`) take independent parts at the same time, each in its own copy made from your code as it is now. Give each its part, its files and cases, what it uses and hands over, your file:line notes, and `model: sonnet` for simple parts. Look at their work from the main checkout (`git -C <copy> diff`, `plumbline builder list`), never `cd` into a copy. Read `build-N.md` and its `## Status`, then `plumbline builder merge <n>` (or `drop`); every copy must be merged or dropped. Small tasks: build it yourself.
- A failing test first, then the fix, then a test for every case. Each `[bug]` case's test must be seen failing (`plumbline run related --files <test>`) while only tests changed since approval, as it is at the end; if the fix came first, `plumbline run related --before-fix --files <test>` runs it on the approved code with the current tests in a temporary copy (no stash). If it can't fail without the fix, ask the user (`plumbline waive bug-test`). [playbooks.md](${CLAUDE_PLUGIN_ROOT}/skills/task/playbooks.md) covers bugs, refactors and performance. Tag debug logs `[PLB-DEBUG]`.
- While building and fixing: `plumbline run checks` (related tests for the changed files plus the saved checks; a typecheck that passed is skipped after markdown-only changes). A frontend change: start the local database, backend and frontend, then `plumbline run e2e`. Tests aimed at a service off the laptop are refused: use a local one in `.env.test`.
- Real-world proof for each kind in the plan, after the last edit, showing the intended change (the body or the record, not only a status code): `plumbline run proof api -- curl -s -i ...`, `plumbline run proof ui -- npx playwright test ...` (or Claude in Chrome: open the changed screen locally and screenshot it; a unit test isn't ui proof; with real data, clicks ask the user first), `plumbline run proof schema -- psql "$LOCAL_DB" -c "..."`.
- Build to the end without waiting. Same-change files outside the plan are fine (list them in the report); risky files and the user's unsaved work are refused (put them under "For you"). Packages and outside-the-laptop commands only if "Needs your OK" lists them.
- Outside the laptop: dry run first where there is one, a backup check before a non-local database change. Deploys, deletes, production writes and other machines confirm each time, so run them together near the end. A pipeline project never deploys by hand. Reading production runs without a prompt; changing it or printing a secret asks. The setup's `prod_access` says how to connect.
- UI: phone first (about 360px, no sideways page scroll, finger-sized targets, nothing hover-only), plus the setup's `frontend_checks`. New screens that need a look agreed first: /plumbline:design.
- Requirements change: `plumbline task replan`, update plan.md, ask the checkpoint again. Unrelated: suggest a separate task. 3 failed fix attempts: stop, say what you learned, give options.

## 9. Review, then the final run
- Before every reviewer run, fix rounds too: `plumbline run checks` until the tests pass, then self-review (review-checklist.md) into `self-review.md`; on a fix round add `## Reach`: for each code file changed since the last review, what else calls or shows it, checked.
- Start every reviewer the finish check names at once, in the background: always `plumbline:code-reviewer` (it also checks the change against the plan), plus `plumbline:plan-reviewer` when a file that decides which checks run changed, and `plumbline:security-reviewer`, `data-reviewer`, `ux-reviewer` where non-test files call for them. Give each the task in one line; never pass one reviewer's findings to another.
- Check each finding at 80 or more against the code; fix the must-fix ones that hold up, and fix or list each should-fix ("Should-fix not fixed"). Under 80: confirm cheaply, fix what holds up, list the rest one line each. To push back, write your evidence to `<reviewer>-disputes.md` and run that reviewer again; an upheld finding goes under "For you" (say "upheld", with both sides).
- After fixes: `plumbline run checks`, then only the reviewers the finish check names again (Plumbline names who keeps a review). At most 3 runs per reviewer; then `plumbline:arbiter` rules on the open must-fix findings and any disputes, and only if one stays open comes the Review cap question.
- After the last review round: `plumbline run checks --final` (the full suite once, plus every saved check). It is the proof the commit needs.
- While reviews run, wait: code edits and builder merges are refused until they hand back. Pass the watcher's 5-minute progress line to the user in one line; a reviewer with no sign of life for 10 minutes is stuck, doesn't use a run, and you start a fresh one. Don't say "done" meanwhile. `"parts": {"reviewer": false}` (or one reviewer by name) switches reviews off.

## 10. Report, then the commit
Write `report.md` after the last review and the final run (format in checkpoints.md): `## Verdict` (Ready, Ready with noted limitations or Not ready, plus `Rollback:`), `## Review` (with "Should-fix not fixed:" when any), `## Common-sense calls`, `## For you`, every waiver under Proof, and the "Weakened tests:" line. A check that couldn't run is "couldn't run", with why and the exact command in backticks, never "skipped"; that, a waiver or an upheld finding rules out plain Ready. Then a short chat version (rules.md's "Talking to the user"): what changed in one sentence (a failure or something left out first), the checks and the review in a line each, and a "For you" line. Give the task's token use from `plumbline status` ("Tokens so far") in the report's Verdict and in the chat version. No paths, task ids or Plumbline steps.
- Approved with "Approve and commit when green": commit when the finish check has nothing left, then say so.
- Otherwise ask whether to commit (header `Commit`), and commit only after they say so; mention the user pushes.
- Then `plumbline task finish` (after the commit). The user commits themselves: `plumbline task finish --uncommitted`.

Jira ticket: write only the comment to `<TICKET>-jira-comment.md` in the notes folder, a few plain lines a teammate reads in seconds, no AI markers or em dashes. The suggested status goes under "For you". Don't post it unless the user's own message asks, and then they confirm the exact text.
