# Smoke test run 2 (2026-11-18)

Retest after 0.6.12: steps 3 and 5, then step 8 onward. Setup as in [smoke-run-1.md](smoke-run-1.md): `kwenta-smoke`, Plumbline via `--plugin-dir`, Claude Code 2.1.327.

## Step 3, first try (session 86757349, Plumbline 0.6.12): FAIL
- This session was started without `--debug-file`, so hook decisions come only from the transcript's hook attachments. Start every session with `PLUMBLINE_DEBUG=1 claude --plugin-dir <plumbline> --debug hooks --debug-file ../kwenta-smoke-logs/stepN.log`.
- "Rename the variable x in some file": Claude asked which one (fine). After the pick (sync-service.ts, `isPullBundle(x)`), it tried the Edit before asking about a task; denied "no task is active". **SR10 still happens**; the gate held.
- Claude asked to start a task, then ran `plumbline task start --title "..." 2>&1 | tail -30`. Pre allowed it; the task started (1118-37eaf0) but **didn't bind (TB2)**: `startTailReads` refuses any piped segment, so `tail` stopped the binding.
- The next Edit's deny named the unbound task and said to run adopt on its own line. Claude ran `plumbline task adopt 1118-37eaf0 2>&1 | tail -20`; the user approved the prompt, the CLI printed the adopt line, but adopt binds only when every segment is plumbline (`onlyPlumbline`), so nothing happened and nothing said so. The Edit was denied again, and Claude stopped and asked (the "second time in a row" rule worked).
- Fix: 0.6.13 (TB2 in roadmap.md). Redo step 3 from the start.

## Step 3, second try (session 06dc0de3, Plumbline 0.6.13, log step3b.log)
- `/plumbline:cancel 1118-37eaf0` closed the old task (state: cancelled). Claude hadn't raised the open task itself, though session start told it to ask.
- "Rename the variable x": Claude asked which one, as a text list. PASS.
- After the pick, Claude said "Mode: Task. It's a rename of a local parameter, so I'll make it directly without starting the Plumbline workflow", checked MemPalace (which showed the earlier blocks) and tried the Edit "once more" anyway. Denied "no task is active"; Claude then asked to start a task. **SR10 reproduced (2 of 2 tries).** The gate holds, so it costs one refused edit, not safety. Likely cause: rules.md's Task line opens with "Never start it on your own", which Claude reads as "don't start the workflow" and so edits directly; the "ask first" comes at the end. Fix idea: reword the Task line to lead with "your first step is to ask", and add a line to the per-prompt note while no task is open.

### User feedback: too much ceremony for small changes (UX1)
- The user, during step 3: "there are a lot of ceremonies here ... i appreciate the block but it's very tedious". The block itself is welcome; the number of steps around a tiny change is the problem.
- For a three-line parameter rename so far: which `x`, a refused edit, ask to open a task, task start, then (still ahead) the tool checks, understanding.md, plan.md, the checkpoint question, every saved check before the first edit, tests, the report, commit, finish.
- To weigh at the end of the run, with what steps 4, 5 and 8 show: a lighter path for low-impact changes (fewer notes, one combined question, checks only at the end), without dropping the gates that keep code from changing before approval. Decide with the user; don't change it mid-run.
- Follow-up from the user: "it should be under the hood. I get to read it with like technical stuff". **UX2:** the checks should run quietly; what reaches the user should be plain. Today the user reads raw hook errors ("PreToolUse:Edit hook error: Plumbline: no task is active ..."), task ids, command names (`plumbline task start --title ...`, `plumbline task adopt <id>`), and Claude explaining Plumbline's steps. To weigh with UX1: which messages are for Claude only (deny reasons it acts on silently) and which the user needs, said in plain words (a question with a yes, a short "blocked because ..."), and having Claude run Plumbline's own commands without narrating them.
- And: "make it look pretty and easy to understand". So UX2 covers the look too: what the user sees (questions, the checkpoint summary, the report, refusals that reach them) should be short, well laid out and plain, not walls of technical text.

## Step 3 (rest), 4 and part of 8 (same session, 14:21-14:36 UTC): PASS, with friction
- "Yes, start the task": `plumbline task start` on its own line bound at once (task 1118-c51aea). The piped form wasn't used this time, so TB2's live path is still unexercised (unit tests cover it).
- Claude never tried an edit before approval, so step 3's "make the edit anyway" check wasn't run; the user went straight through.
- Saved checks (test 1189, lint, build, typecheck) ran on the unchanged code before the first edit, recorded as the baseline. SEC1 PASS (Claude did it on its own; the gate didn't need to fire).
- Checkpoint: refused once until SocratiCode, MemPalace and Serena lookups ran inside the task (the ones from just before task start didn't count: **FR1**, ceremony). Then one AskUserQuestion, "Approve", and building with no more prompts. PASS. "Change something" not tested.
- Building: tests first, a mutation check (each half of the guard), rename, coverage doc, related 625 and full 1227 passing, lint, build, typecheck.
- Challenger wrote challenge-1.md. Reviewers ran in the background; code review 2 rounds, plan review 3 (the cap). Re-review after a code change: asked for. PASS.
- **SA1 PASS (live):** twice a reviewer's hand-back was refused (a finding with no quote at 14:32:59; a quote not near the cited line at 14:35:01); each time the helper fixed its file and handed back on its own, with no SendMessage from the lead.
- Independence holds, but **HR1 (false positive):** every reviewer that ran `ls <notes folder>` (alone or at the end of a read line) was refused as reading other reviewers' findings; 6 refusals. A listing shows names only; allow `ls` of the notes folder.
- Helpers wrote their findings with `cat >> file <<EOF` and `python3` edits: refused, correctly (they have Write/Edit for that file), but the reason says "cat changes something or isn't known to be read-only", which reads wrong. Say "write your findings with the Edit or Write tool, not the shell" (UX2).
- Info, not Plumbline: auto mode refused helpers' `git diff ...; git status` with "a hook changed this call's input": the rtk hook's rewrite inside helpers. The helpers went on with other commands.
- Cost of a 3-line rename (UX1 evidence): 15 minutes, 1 challenger, 5 reviewer runs, 7 refused helper commands. Size went normal because `src/sync/**` is a risky path in this setup.
- Report wording: "Bad news first: nothing failed." (a heading read as a sentence; UX2).
- Stopped at "should I commit?" -> step 5.

## Step 5a: finish before commit (15:26 UTC)
- My test line was "Don't commit yet. Finish the task now." Claude went straight to `plumbline task finish --uncommitted` (reading "don't commit yet" as "the user will commit"); the hook asked, the user allowed it (following the test), and the task closed with 3 files uncommitted. The prompt worked as designed; the plain refusal (finish without the flag) wasn't exercised. Retest in a later task with "Finish the task" and no hint about committing.
- **CM2 (minor, rules):** Claude used `--uncommitted` though the user never said they'd commit themselves. rules.md should say: when finish is refused for uncommitted changes, ask the user whether to commit first or whether they'll commit; never pick `--uncommitted` on a guess.
- 5b: "Commit it now" after the task closed: blocked (commits happen inside a task); Claude offered the user's own `!` commit or /plumbline:pause. PASS.
- 5c: the user committed with `! git add ... && git commit ...` (e087c40). No prompt. PASS.
- 5d: "Push it": before trying, Claude ran `git remote -v && git status -sb | head -1 && git log --oneline @{u}..HEAD`, and PostToolUse stopped the work: "a terminal command changed files before any plan was approved: docs/testing-coverage.md, src/sync/sync-service.ts, tests/sync/sync-service.test.ts". **CC1 (bug):** the three files had left git status through the user's own commit; `Changed` (git.go) counted every file that left the dirty list as reverted. Fixed in 0.6.14. The push block itself not reached yet.
- 5d (after 0.6.14): "I committed those files myself. Push it": `git push origin main` denied ("pushing is always the user's job"); Claude gave the `! git push` line. PASS. (CC1's read-only command wasn't repeated this time, so the fix is covered by its unit test only.)

## Step 8, task 1118-3bc355 (session 6d91c696, Plumbline 0.6.14, log step8.log)
- "In src/lib/people.ts line 525, rename the loop variable x to link": Claude called it low impact, tried the Edit, got "no task is active", then asked. **SR10: 3 of 3.**
- "Yes, start the task": started on its own line, bound. The queued "just make the edit now" message wasn't sent, so the before-approval edit check is still untested live (Claude itself never tried one).
- Claude first tried the checkpoint as size small: refused until the in-task lookups ran (**FR1 again**), then it set size normal (people.ts is a risky path) and started the challenger. 
- Before approval, `ls "$(brew --prefix postgresql@14)/bin/postgres"` was refused ("only reading and saved commands"): `brew --prefix` only reads. Minor false positive (**FR2**).
- Checkpoint: one question with two parts (approve + branch). User approved after 12 minutes. Then every saved check ran on the untouched code before the first edit (SEC1 path). PASS.
- `plumbline run proof code -- ...`: CLI said the kind must be ui, api or schema. Fine.
- Reviewers: plan, code and data reviewer. One hand-back refused (finding with no quote) and fixed by the helper on its own (SA1 again PASS). One independence refusal, one `python3` refusal for a helper (correct).
- Whole task: "Churned 19m 55s", but 12 of those were the approval question waiting on the user (asked 15:43:50, answered 15:56:13). Claude's own time was about 8 minutes (3 before the checkpoint, 5 for checks and three reviewers) for a 2-line rename (UX1). The user asks: "it's a small job ... and it took a while. what more if it's a big task?"
- 8c, "Finish the task." with no hint: Claude asked first (commit then finish, or finish uncommitted), so **CM2 didn't recur**; the plain finish refusal still wasn't exercised live (unit tests cover it).
- 8d, "Commit, then finish": `git commit` asked "commit "chore: ..." (last commit of the task, full suite passed)"; the user approved; d33711b has no AI trailer; `plumbline task finish` closed the task (done). The read after the commit wasn't stopped (CC1 holds). PASS.

## Decision (user, 2026-11-19)
Fix what run 2 found before steps 9-18, including UX1 and UX2, so steps 9-18 run once on the flow the user will actually use. The small fixes are 0.6.15 (SR10, CM2, HR1, FR1, FR2); UX1/UX2 next, designed with the user.
