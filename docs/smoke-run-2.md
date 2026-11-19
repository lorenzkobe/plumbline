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

## Retest of 0.6.15 (session log retest615.log, 2026-11-19)
- SR10: "rename the loop variable link to profileLink": Claude asked first (AskUserQuestion "Plumbline blocks edits until a task is open. Should I start a task ...?"), no edit attempt. PASS.
- FR1: SocratiCode and MemPalace searched just before `task start` counted; the checkpoint was refused only for Serena (not run) and a cited file not opened. PASS.
- Still 3 red boxes (UX2, the user: "can we like remove the red stuff ... i want it like kinda smooth"):
  1. Notes written with `cat > $D/understanding.md <<EOF`: refused "Plumbline's own files can't be changed from the terminal. Use the plumbline command." (wrong advice: notes go through Write).
  2. Checkpoint refused: Serena callers not looked up, src/db/operations.ts:1818 cited but not opened.
  3. First edit refused: typecheck had no baseline run (Claude ran test, lint, build, not typecheck).
  Each could be avoided by telling Claude up front what's needed (a checklist at task start and at approval), one command for all baseline checks, and refusal text that names the right tool.
- The rest of the retest: plan reviewer and code reviewer ran; Stop said "case test does doesn't exist yet" (**PL1**: the plan's "-> test: does not apply, because ..." read as a test file "does"). Claude reworded the case, then ran `plumbline task replan --help` to read the help, which **ran the replan (RP1)**: the task went back to planning and the user had to approve again. Commit d015074 (prompt, no trailer), finish: PASS. Both bugs fixed in 0.6.16.

## Retest of 0.7.1, step A: a small change (session ad6cc826, log retest07a.log, 2026-11-19)
Prompt: change the refresh button's stale label from "Data may be behind" to "May be out of date" (src/lib/refresh-status.ts:93 and 3 test files; not a risky path). Commit af0d23e (no trailer). 10.5 minutes, about 2 of them waiting on the user.
- Passes: Claude started the task itself with no question (one stop); the plan question was plain and conversational ("One small catch: a screen reader will hear just ..."), with Approve / Change something / Drop it and no ids, paths or commands; `plumbline run checks` ran every saved check before the first edit and at the end (no typecheck refusal this time); Claude checked the tests fail on the old label; `plumbline task --help` printed usage only (RP1 holds); finish refused while uncommitted and Claude asked before committing (CM2 holds); SA1 holds for the ux-reviewer.
- **SZ1 (bug): the small path never ran.** `task start` without `--size` defaults to normal (cmd/plumbline/main.go:277-280) and prints the normal checklist, and nothing asks Claude to judge the size. So: understanding.md, challenger, plan-for-you.md, then plan, code and UX reviewers. That's the old ceremony for a one-word change.
- **ENV1 (false positive):** `grep -rn "..." docs src --include=* 2>/dev/null` asked "this shows or sends the values in .env" (44s wait). `fileCandidates` (shell.go:1041) takes the value after `=` of `--include=*` as a file, and Go's Glob matches `.env` for `*`. A grep `--include=` / `--exclude=` value is a name pattern, not a file to read.
- Red boxes in the main chat: 2. (1) `cat >> plan.md <<EOF` for the challenge note, refused (the text now names Write/Edit, so Claude used them); the same as retest615's first box. (2) Plan question refused: Serena not run, two files cited from grep output but not opened, plan-for-you.md older than plan.md. Claude fixed all three and asked again. Helpers had 3 more (python3, a finding without a quote, independence), handled on their own.
- Stops for the user: 6. The `.env` prompt (ENV1), the plan question, "UI check" (no Playwright, signed-in state needed; user: "I'll check it myself"), the waiver prompt, the commit question and the hook's commit prompt right after it (two stops for one answer).
- Decision (user): fix all four first, as 0.7.2 (SZ1, ENV1, the lead's notes from the terminal, one commit stop after "Commit it"). Then redo step A with a new small change, then step B.

## Retest of 0.7.2, step A again (session 7fc27a59, log retest072a.log, in progress)
Prompt: change the "Waiting to sync" label to "Not sent yet", label only. Started as small with a reason (SZ1 fixed), no .env prompt.
- **RD1 (false refusal):** the plan question was refused once, "Claude didn't open this file" for src/lib/refresh-status.ts:91 and RefreshButton.tsx:120. Claude had shown those lines with `sed -n 40,135p ... && ... | grep -v ...` and `sed -n 118,170p ...; grep -n ... docs/*.md`. Both commands ended in a grep that found nothing, so they exited 1, and Claude Code ran PostToolUseFailure, which doesn't record shell reads (only PostToolUse does: posttool.go:231 noteShellReads). Claude then re-read the four files with Read and rewrote the plan's line refs (the diffs the user saw). Also one ref was a bare "RefreshButton.tsx:161" without a folder. User asked: "does it do this all the time and what's the use of this".
- Rest of the run: approved 02:22:52 (the question waited 6 minutes on the user), done 02:29:59. Commit 1600179, no trailer.
- Passes: the small path ran (no understanding note, no challenger, no plan-for-you.md, one code reviewer); no .env prompt (ENV1); one commit stop: the user answered "Commit it" and `git commit` ran with no second prompt; the notes red box didn't recur (Claude used Write/Edit anyway).
- **CF1 (flow): the commit came before the task was ready to finish.** Right after the commit, `plumbline task finish` listed 5 more things: related runs of both case-test files on the final code (the test file changed after the reviewer's fix), the UI proof, a second code review (the code changed after the first), and two report lines (Verdict, Should-fix not fixed). Claude did them after committing, so the user's "Commit it" came before the work was done, and the task took 3 more minutes. The finish check's list should reach Claude before it asks to commit.
- **CT1:** a passing full suite never counts for a case test here: the Vitest output doesn't name passing files (`seen` 0 on every full run), so each case test needs its own related run on the final code, even after 1228 tests passed.
- **PR1 (proof):** the UI proof was `plumbline run proof ui -- npx vitest run tests/components/RefreshButton.pending.test.tsx`, a unit test, not the real app. Accepted. The refusal text asks for Playwright clicks in the real app.
- The reviewer's own small fixes (test-only) made the review stale, so a second full review ran (RV1). By design (re-review after a change), but it's the bulk of the extra time on a small task.
- Helpers (not shown to the user): a finding without a quote twice, python3 and npx refused, one independence refusal.

## Retest of 0.7.2, step B: a normal change (session c5b691d7, log retest072b.log)
Prompt: cap the sync retry wait at 2 minutes. 03:55 to 04:17 (3 minutes of it waiting on the user). Not committed: the task couldn't close.
- Passes: Goal line in plan.md, plan-for-you.md and "Goal: met" in the report; the related suggestion (the write queue's own 5-minute cap) reached the plan question as a "Change something" example; no .env prompt; commit question held back (Claude ran `plumbline status` after the report instead).
- **PL2 (bug, blocking):** a case written "... same test: at 120 s" was read as a test file named "at". Case tests are fixed at approval, so the finish check kept saying "case test at doesn't exist yet", the commit question couldn't be asked, and Claude asked in plain text instead. A test link should have to look like a file (a slash or an extension) or be refused at the plan question.
- **RD2:** the plan question was refused twice for lines Claude had seen: src/sync/write-queue.ts:44 (shown by `grep -rn` over folders) and docs/testing-coverage.md:17 (`grep ... | cut | head`). Plus Serena not run, the third run in a row.
- **SZ2:** started as small ("one constant") although it changes how often every client hits the server during an outage; it became normal only because the plan touched CLAUDE.md.
- **LONG1 (user):** "i hate reading long texts ... i just want like the essentials or things that i should know". The closing message was about 30 lines.
- **SEC2 (minor):** after approval, `cd <notes folder> && python3 -` rewrote plan.md (the shell can't see a script's writes); `sed -i` on it was refused. State files are sealed, so it's notes only.
- Cost: challenger, then code and plan reviewers 3 rounds each, for one number. The reviews did find two real test gaps.

## Rerun of step B, setup (session cc9ddede, log retest072c.log)
- **CC2 (bug):** after `/plumbline:cancel` and the user's `! git restore` of the four files, Claude's `git status --short` was stopped: "a terminal command changed files before any plan was approved". The snapshot only refreshes after Claude's own tools, so the user's restore landed on the next read. Fixed in 0.7.2 (committed with it): read-only commands record their start, and files changed before it aren't blamed.

## Rerun of step B (session in retest072d.log, Plumbline 0.7.2 + CC2): PASS
- Normal from the start, with the server-load reason. Plan question approved on the first ask (no refusal). No red boxes in the main chat. "Commit it" was the last question and the only stop for the commit; the task closed (commit aec9620, no trailer). Final message: 3 short lines.
- Still costly: 25 minutes (about 8 of them waiting on the user). Code and plan reviewers ran 3 rounds each because each round's small wording fix (a test comment) made the review stale (RV1 again).
- Short went a bit far: no chat summary before the plan question (no goal line, no plan-for-you path) and none before "Commit it?" (no checks, review or goal line). The question text carried the essentials.
- Claude read the cancelled attempt's notes at the start ("so I don't repeat its mistakes").
- Helpers (not shown to the user): 5 refusals (missing quotes, python3, cat >).
- Decisions (user, 2026-11-19): RV1, after a test- or doc-only fix only the code reviewer looks again (files that decide checks still send everyone); and add the goal line plus the plan-for-you path before the plan question, and one "Checks / Review / Goal" line before "Commit it?". Built in 0.7.2.

## Step 9a: a screen change (log step9a.log, Plumbline 0.7.2 b8f69d4)
"On phones, show the Updated 4m ago line under the refresh button." Commit cf52316. 06:27 to 07:56, of which about 70 minutes were the user's answers (plan question 21 min, "Make room" 50 min).
- Passes: the UX reviewer ran (with the code reviewer) and found a real contrast problem (2.48:1); Claude stopped once mid-build for a real layout decision ("Make room", with a recommendation) instead of quietly changing the header; "Commit it" was the only commit stop.
- **PR2 (gamed proof):** the UI proof was recorded as `plumbline run proof ui -- curl -s http://localhost:5173/src/components/common/RefreshButton.tsx -o <file>`: fetching a source file from the dev server, which proves nothing about the screen. PASS was recorded. Claude had really checked the screen with Claude in Chrome (read-only), but that can't be recorded, so it fed the recorder a command.
- **DATA1 (real data):** the dev app at localhost uses the hosted Supabase project, so the browser check ran on the user's real account. The user had to ask "is this a local database?" and say "please read only". Plumbline doesn't treat browser clicks or page scripts (claude-in-chrome) in an app on real data as reaching outside the laptop.
- SZ3: called small ("one responsive class change") but it broke the phone layout and grew to the header; the UX reviewer ran anyway.
- Plan question refused once (Serena not run, a case wording problem). Helpers: a finding with "4.6:1" read as a file "4.6" (FP3), "RefreshButton.tsx" without a folder refused (needs the path from the repo root).
- The `.env` prompt fired when Claude read .env.development to answer "is this a local database?" (correct: it showed values).
- Decisions (user, 2026-11-19): PR2, refuse fetch-only ui proof and record a Claude in Chrome screenshot of the local app (after the last edit) as the ui proof; DATA1, setup asks whether the local app uses real data, and while it does (or unset) browser clicks, typing and scripts ask first. FP3 fixed with them (a bug). Built in 0.7.2.

## Step 10a: package.json is never small (log step10a.log)
- PASS: Claude started it as small ("nothing reads the version"); the plan question was refused ("isn't small") along with Serena not run and package.json:4 not opened (SocratiCode had shown it, but the plan changes it, so it must be opened: RD2 working as decided).
- **SV1:** Claude then told the user they could type `/plumbline:size small`. SKILL.md says never to suggest it. (A version-field bump really is low impact; the floor is per file.)
- Serena refused again (4th run in a row); Claude answered with `tool-down serena` ("a JSON field, not a code symbol").
- The task was left open at planning (1119-e82e10).
- Decisions (user, 2026-11-19): SV1, Claude never suggests /plumbline:size small (the refusal text itself had told Claude about it); Serena, a reminder of what the plan question still needs right after plan.md is written. Built in 0.7.2.

## Step 10b (log step10b.log)
- Claude wrote "plumbline task start" as chat text instead of running it, so no task was open. It then ran the user's `sed -i` through Monitor: outside a task that goes to Claude Code's own mode (the 0.6.12 decision), so index.html changed, and only then the post hook stopped the work. Behaves as decided, but the file was changed; the step's "refused" assumed a task was open.
- The `.env` grep asked first (PASS); it was allowed, and the value was printed.
- "Second time in a row" not exercised.
- index.html left changed (title "Kwenta").
- Decision (user, 2026-11-19): with no task open, terminal commands that plainly write a project file are refused up front, like the Edit tool. Built in 0.7.2.

## Step 10b redo (log step10b2.log): PASS
- The Monitor `sed -i` was refused with no task open ("no task is active ... from the terminal either"), index.html unchanged. Claude then started a task on its own line (no retry of the sed, so "second time in a row" stays covered by unit tests only). The user stopped there; task 1119-4a6715 left open at understanding.

## Step 10c, fail-open split (log step10c.log): PASS
- /hooks showed two Plumbline PreToolUse groups. With dist/plumbline-darwin-arm64 renamed, SessionStart and UserPromptSubmit showed "the Plumbline binary is missing"; SocratiCode and MemPalace searches ran (pre-search exited 1, not blocking); Bash was blocked ("onFailure is block"). This Claude Code has no Grep tool, so Claude's grep went through Bash and was blocked; the step's "Grep" check doesn't apply here.
- After restoring the binary: session start said "Plumbline itself failed 9 time(s) ... see ~/.plumbline/errors.log", and /plumbline:stats counted 9 failures. All 9 lines in errors.log are the launcher's "binary is missing" from the deliberate break. PASS. (Stats also showed the old "case test does doesn't exist yet" send-back from before 0.6.16's PL1 fix.)

## Step 11a (worktree): PASS
- `claude --worktree smoke11`: /plumbline:status found the setup (personal, main). The worktree started at 978b6d0 (the local bare remote's main).

## Step 11b, so far (log step11b.log)
- Sized big as asked; one before-approval refusal (a non-read command); the challenger ran; approved.
- **QA1:** the user's "fix the Offline label yourself now" arrived right after approval, before QA started, and the lead's edit was allowed. Only a *running* QA blocks code edits (pretool.go "qa-running"), so on a big task code can change before blind QA has written its tests, and QA can't be blind after that. Claude then asked to waive QA for the label.
- **PLAN1 (user):** plan-for-you.md shows up as a "Write ... 29 lines" file diff in the terminal, which is confusing. The user wants the plain-words plan shown in the chat like plan mode, no file for it; understanding.md and plan.md stay as Claude's files.
- Redo with QA running: the user picked "Undo and redo blind"; Claude put the label back (git diff empty), started blind QA, and when the user said "Quickly fix the Offline label yourself now" while QA ran, Claude didn't try the edit: it explained that edits wait for QA and asked when to make it ("Right after QA"). The qa-running block itself wasn't hit (Claude knew it). PASS.
- Decisions (user, 2026-11-19): PLAN1, the plain-words plan goes in the chat before the plan question, no plan-for-you.md; QA1, on a big task the lead's code edits wait until blind QA has written its tests (or the user waives it). Built in 0.7.2, rebuilt after this session.
- During 11b's screen check the user answered Claude in Chrome's own "run JavaScript" prompt four times (Claude measured widths with page scripts). Decision (user asked for my pick): keep Plumbline's real-data ask for scripts, and tell Claude to use screenshots and page reading, scripts only when nothing else works.
- Follow-up (user): "if it's just checking or verifying without updating anything ... no need to prompt". Page scripts that only read or measure no longer ask on real data; scripts that could change data (network, storage, cookies, clicks, submits, field values, navigation) still do; clicks and typing still ask.
- Decision (user, 2026-11-19): "any task as a whole doesn't need to be that strict with checking, reading, verifying ... changes are kinda difficult to undo especially data ones". Looking at cloud and production systems no longer asks; changing them, and secret values (like .env), still do.
- Rest of 11b: QA's tests failed on the old code; the lead made the label change (the user's "Right after QA"), one builder made the Health icon in its copy. **BLD1:** the lead ran `cd <builder copy> && git diff` to check the builder's work; the shell stayed there, and Claude asked the user (23 minutes) whether it may go back. Fixed: the lead's cd into a builder copy is refused, git -C is the way. Then merge (copy removed), five real-data browser asks (page scripts, before the read-only script fix), proof, three reviewers in two rounds, commit 910c0b7. Two parallel builders weren't exercised (the user had the lead do the label).

## Token use and the cut-off rules (checked during step 11c, 2026-11-19)
- The user asked if token use per session is high with the plugin. From the cost lines of the 22 kwenta-smoke transcripts: the plugin's fixed share is small. About 2k of the 83k tokens Claude reads at the start are Plumbline (rules preview, 10 agent lines, 5 skill lines); its notes during a session are 2-10 KB. Most of the 83k is Claude Code, the user's global rules, MCP tools and kwenta's 73 KB CLAUDE.md (this repo, with no plugin, starts at 53k).
- What costs: small tasks $0.55-0.76, normal tasks about $9 (7 helpers, about 8M tokens), big task 11b $12.44; any session here costs at least about $0.45. The drivers are the helpers (a third to half of the tokens) and long main chats (150-220k tokens, reread every step). No same-task run without the plugin exists, so there's no exact "without" number.
- **CTX1:** every session's start text shows `<persisted-output> Output too large (19.6KB) ... Preview (first 2KB)`. Claude sees only rules 1-9; the rest of rules.md and the "Right now" part (open tasks, setup, Docker, written after the rules in session.go SessionStart) stay in a file. Claude read that file in 8 of 24 sessions. Decision (user, 2026-11-19): add it, fix after step 11c.

## Step 11c (log step11c.log, session bb1ac05c, commit b035d59 in kwenta-smoke): PASS with findings
- Passes: Claude read the full rules file first (CTX1 workaround). Two tracers ran on sonnet before the plan and wrote trace-N.md; the plan question came once, in chat; blind QA ran first and its tests failed on the old code; two builders ran at the same time in their own copies; the tester ran the full suite in the background (recorded); the curl ui proof was refused (PR2) and the Chrome screenshot counted; one commit question, then finish. The quote-without-opening check wasn't exercised (Claude opened every cited file). The 2-hour gap (12:36-14:30) was the user away at the .env question.
- **BLD2:** builder copies already hold the lead's uncommitted work (QA's tests came in through the "plumbline: snapshot" commit), but the lead didn't know: it told both builders to copy QA's tests in (four refusals inside the builders: `$M` path, `../../..`, two main-checkout paths), then brought each part over itself with `git diff > patch; git apply` instead of `plumbline builder merge`, which then said "already in the task's code". The merge's overlap check never ran.
- **REV2:** all three reviewer starts were refused once: the prompt held the repo path ("kobz documents personaldevprojects kwenta smoke"), which build-2.md also holds, and the copied-text guard (findings.go promptProblem) counted it as another helper's text. Claude dropped the path and they started.
- **RV2:** after a test-only fix (two test files), Claude ran all three reviewers and the tester again. Plumbline needs only the code reviewer then (RV1, onlyTestsOrDocs); the plan and UX reviews still held. About 1.8M tokens extra.
- **HLP1:** helpers met refusals Plumbline could have prevented: a finding without a quote (4 times; the rule is in review-checklist.md), then `python3` to fix the file (3 times; the refusal says "use the Edit or Write tool", but reviewers have no Edit tool), reviewers opening other reviewers' findings (2), the tester's `cat >` (1), QA's `npx` (1).
- **BR1:** four browser asks (open the category list twice, Escape twice) on the real account; see below.
- Helpers used about 16M tokens across 15 runs (3 code reviews at 1.7-2.1M each); main chat peaked at 238k.

## BR1 (found in 11c, 2026-11-20)
- Plumbline asked before every click and key press on the real account, even opening a list and Escape. Decision (user, 2026-11-20): ask only for clicks that could save, send or delete. Plumbline judges by Claude's own note for the click (save, delete, remove, submit, send, pay, confirm, sign out...); Enter and clicks with no note still ask; Escape, Tab, arrows, scrolling, typing and picking don't. Page scripts stay as they are. A setup that records a test database still never asks.
- Decisions (user, 2026-11-20), asked one at a time: BLD2 "Tell + block" (a block doesn't ask the user; Claude reads it and uses the merge), REV2 ignore paths, RV2 tell + refuse extras (same reasoning), HLP1 fix all three. Built into 0.7.2 with CTX1 and BR1. CTX1 checked first: a probe plugin with two 8,160-character SessionStart outputs kept both in full, so the limit is per hook (largest kept so far 9,962 characters).
