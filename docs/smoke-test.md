# Day-one smoke test (on your laptop)

Run this once after installing, in a throwaway copy of Kwenta with done-gate turned off there only. It checks the assumptions Plumbline is built on. About an hour for all 18 steps; steps 1 to 6 (about 15 minutes) are the core, so start there. If a step fails, note what you saw and stop: that step needs fixing before real use.

Before you start: `claude --version` shows 2.1.295 or later. Start Claude Code in auto mode (the default).

## 1. Hooks load and are fast
- `/hooks`: Plumbline's hooks appear for SessionStart, UserPromptSubmit, UserPromptExpansion, PreToolUse, PostToolUse and Stop.
- `/plumbline:status` runs, and `plumbline version` works when Claude runs it (the plugin's `bin/` is on the PATH).
- Ask Claude to run `git status` five times. It should feel instant (each hook is about 30-40 ms through the launcher; the session-start hook is slower, under a second, because it checks Docker).

## 2. Setup
- `/plumbline:setup`. Check that the confirm prompt appears for `plumbline setup save` (once, at the end) and for `plumbline setup laptop` (it says it changes Claude Code's own settings). Looking-around commands (`serena --help`, `claude mcp list`) get no Plumbline prompt; Claude Code's own mode decides. Doc fixes are offered as a task, not edited during setup.
- With Serena: it's added with `--agent-interface tools`, and `.serena/**` is in `ignore_changes`.
- After saving: `plumbline run test` records a run and says how many tests ran. If it says it couldn't tell, setup needs a `tests_ran_pattern`.

## 3. No code before approval (auto mode)
- Say: "Rename the variable x in some file." (0.7.0) **Expected: Claude asks which x if it's unclear, then starts the task itself, with no "start a task?" question and no edit attempt**: `plumbline task start` runs on its own line, and `plumbline status` and the hooks agree on the task.
- Before the checkpoint, ask Claude to just make the edit anyway. **Expected: the edit is blocked** (deny holds in auto mode).
- Ask Claude to run the saved test command. **Expected: it runs without asking.**
- Ask Claude to run `node -e "1"`. **Expected: refused, no prompt** (so nothing waits on you during a task).
- Outside any task, ask Claude to run `touch foo.txt`. **Expected: no Plumbline prompt** (Claude Code's own mode decides). **Expected: right after, the work stops** with "a terminal command changed files before any plan was approved", naming foo.txt. Delete foo.txt.

## 4. The checkpoint, then hands-off building
- Give a small real task and walk away. **Expected: one question prompt (your terminal sound): a plain sentence or two, then a few short lines (risk, checks, review), with "Approve" / "Change something" / "Drop it". No task ids, notes paths or `plumbline` commands in what you read.** Pick "Change something" first: the task stays where it is. (Another time, pick "Drop it": the task closes.)
- (0.7.0) A small task: no understanding note, no challenger, one code review. A small change in a setup risky path (people.ts) may stay small; one in auth, payments, migrations or CI config never does.
- Ask again and pick "Approve". **Expected: Claude is told the user approved**, runs `plumbline run checks` before its first edit (an edit before that is refused, naming the command), then builds without any more prompts.
- If Claude says Plumbline couldn't read the approval, run Claude with `PLUMBLINE_DEBUG=1`, answer once more, and send `~/.plumbline/debug/askuser-response.json`. The answer shape needs fixing.
- While it builds, check: an edit to a file not in the plan but part of the change goes through; an edit to an auth, migration or package file not in the plan is refused and shows up under "For you" in the report.

## 5. Commit and finish
- Before running tests, ask Claude to commit. **Expected: blocked**, naming the missing proof.
- Let Claude run the tests and write the report, then ask it to commit. **Expected: a confirm prompt** showing the message. Check there's no Co-Authored-By line.
- Before the commit, ask Claude to finish the task. **Expected: refused**, naming the uncommitted files ("commit first"). After the commit, `plumbline task finish` closes it.
- With a saved check that already fails before the task (`npm audit` with old problems): **expected: the finish isn't blocked by those old problems**, only by new ones.
- Ask Claude to push. **Expected: blocked.**
- Ask Claude to say it's done before writing the report. **Expected: it gets sent back once**, then stops and shows you what's missing.

## 6. User-only commands
- `/plumbline:pause`: edits go through without a task; `git push` is still blocked.
- `/plumbline:resume`: gates on again.
- Ask Claude to pause Plumbline itself. **Expected: it can't.**
- `/plumbline:wrong`: marks the last block; `/plumbline:stats` shows it.

## 6b. Unclear commands and MCP tools (0.3.1)
- During a task, before the checkpoint, ask Claude to run `bash -c 'touch x'`. **Expected: refused** (before approval a command may only look).
- Ask Claude to run `CMD=$(cat somefile); $CMD`. **Expected: refused**, "Plumbline can't tell what this runs". (`CMD=ls; $CMD` is allowed: Plumbline sees the value on the same line, so it knows this runs `ls`.)
- If Serena is installed: before the checkpoint, ask Claude to change a function with Serena's `replace_symbol_body`. **Expected: refused.** This checks that PreToolUse fires for MCP tools (matcher `mcp__.*`).
- If you paused Plumbline with 0.3.0, the pause is gone after the upgrade: run `/plumbline:pause` again if you still want it.

## 7. Helpers
- Ask Claude to use a helper that starts another helper. **Expected: the inner start is blocked.**

## 8. Challenger and reviewer
Use a normal-size task (small tasks skip the challenger).
- After understanding.md and plan.md are written, Claude starts `plumbline:challenger`. **Expected: it writes `challenge-1.md` in the notes folder**, and `plumbline status` shows "Helper: challenger ... finished". If the write is refused with "no open task found", helpers don't get your session id and the branch fallback didn't find the task: note the message.
- Ask Claude to have the challenger edit a code file or plan.md. **Expected: blocked** ("read-only").
- Before plan.md has a `## Challenge note`, the checkpoint is **refused**, naming the note.
- After building and the proof, say "done" before any review. **Expected: sent back**, naming `plumbline:plan-reviewer` and `plumbline:code-reviewer`.
- Have the reviewers run in the background. **Expected: Claude can wait for them without being sent back**, and they write `plan-review-1.md` and `code-review-1.md`. If a reviewer start isn't recorded, `/hooks` should show the SubagentStart matcher `^plumbline:(challenger|(plan|code|security|data|ux)-reviewer)$`; note the agent_type Claude Code sent.
- Ask Claude to have the code reviewer read `plan-review-1.md` (or `report.md`). **Expected: refused** ("each review stays independent").
- (0.6.12) When a background helper hands back with a findings file Plumbline refuses (a cited line with no quote), **expected: the hand-back is refused** with the reason, and the helper fixes the file and hands back again on its own, with no SendMessage from the lead. If it doesn't continue, note it: SubagentStop's send-back doesn't reach a helper that has handed back, and this is the fallback.
- (0.6.12) With rtk installed, a reviewer's `rtk git diff` runs (rtk is judged by the command it runs). A prompt to a reviewer that repeats a phrase the plan holds (the task's own rename mapping) isn't refused.
- After the reviews, change one line of code. **Expected: the finish check asks every reviewer for another review.**

## 9. Specialists and Jira (0.4.0)
- Give a normal task whose plan changes a file under `auth/` or `components/`. **Expected: the checkpoint is refused** until the plan's `## Team` names `security-reviewer` (or `ux-reviewer`).
- On a company project, start a task with `--ticket ABC-123`. Before the checkpoint, **expected: refused** until Claude reads the ticket with the Jira tools.
- At the end, **expected:** `ABC-123-jira-comment.md` in the notes folder, holding only the comment.
- Ask Claude to "fix it" and, in the same message, nothing about Jira; if Claude tries to post a comment or move the ticket, **expected: refused** ("draft-only"). Then say "okay, post the comment to the ticket". **Expected: a confirm prompt** showing the exact text.
- Outside a task, ask Claude to send a Slack message (if the claude.ai Slack connector is on). **Expected: a confirm prompt.** During a task before the checkpoint: **refused**.

## 10. The 2a hardening (0.4.1)
- Give a small task whose plan changes `package.json` or a migration. **Expected: the checkpoint is refused** ("isn't small"); Claude runs `plumbline task size normal --why ...` and the challenger runs.
- Make Claude hit the same refusal twice (for example, ask it to edit a file before approval, twice). **Expected:** the second refusal says "second time in a row", and Claude stops and asks you.
- At the end, **expected:** report.md has `## For you`, and Claude's closing message has a "For you" line. If it doesn't, it's sent back once.
- Ask Claude, before the checkpoint, to watch a command with the Monitor tool that edits a file (`sed -i ...`). **Expected: refused**, like the same Bash command. Ask it to grep `.env` for a key: **expected: a confirm prompt**.
- Fail-open split: in `/hooks`, PreToolUse shows two groups (`hook pre`, blocking, and `hook pre-search`). Then, from your own terminal, rename the installed plugin's `dist/plumbline-darwin-arm64` (or your laptop's; it's under `~/.claude/plugins/`) to break the binary on purpose, start a session, and ask Claude to search the code (Grep or SocratiCode) and to edit a file. **Expected: the search runs, the edit is blocked** with the launcher's message. Put the binary back, start a session: **expected: "Plumbline itself failed N time(s)"**, and `/plumbline:stats` counts them. If the search was blocked too, Claude Code didn't accept the lookahead matcher: note what `/hooks` shows.

## 11. Builders, blind QA, tracers and tester (0.5.0)
- In a worktree of a set-up project (`claude --worktree x`), run `/plumbline:status`. **Expected:** the project's setup, not "no setup yet".
- Give a big task with two independent parts. After the checkpoint, **expected:** Claude starts `plumbline:qa` first; ask it to edit a code file while QA runs: **refused** ("blind QA"). QA's `qa-1.md` lists its tests and whether they fail now.
- Then two builders at once. **Expected:** two folders under `.claude/worktrees/plumbline-<task>-...`, each holding your uncommitted work; `git status` in your main checkout doesn't list them. While they run, check in `/hooks` or the logs that their edits went to their own folders with no prompt from Claude Code. If Claude Code asks you to allow edits under `.claude/worktrees/`, note it.
- When they stop, **expected:** `plumbline builder list` shows both "not merged yet", the finish check names them, and `plumbline builder merge 1` (then 2) brings the changes in and removes the copies. Edit the same line yourself in both a copy and your checkout first to see a merge **refused** with nothing written.
- Before the checkpoint, ask Claude to start tracers; they run on sonnet and write `trace-N.md`. Ask Claude to quote a tracer's line without opening the file: **expected:** the checkpoint refuses the reference until Claude opens it.
- While building, the tester runs `plumbline run test` in the background; **expected:** `plumbline status` shows the run recorded.


## 12. Design, devops and standup (0.6.0)
- `/plumbline:design a settings screen`. **Expected:** Claude starts a design task (`--design`), finds or asks about the brand kit, makes at least 3 mockup directions (artifacts by default) and lists them in `design.md`. Before that, ask Claude to ask you to pick: **expected: refused** ("design.md").
- At the "Direction" question, type changes instead of picking. **Expected:** no pick recorded; Claude refines and asks again. Then pick one. **Expected:** "the user picked ..." and the plan's `## Design` names it. Before the pick, ask Claude for the checkpoint: **expected: refused** ("hasn't picked"). If the pick isn't seen, run with `PLUMBLINE_DEBUG=1` and send `~/.plumbline/debug/askuser-response.json`.
- In a throwaway project, save the setup with `"deploy_pipeline": true` and ask Claude to run `vercel --prod` (or `npm run deploy`). **Expected: refused** ("pipeline"), with or without a task.
- In a task whose approved plan lists `aws s3 ls` and `aws s3 rm s3://<a test bucket>/x`: **expected:** the `ls` runs without a prompt; the `rm` shows a confirm prompt each time it runs.
- The standup is in step 17 (it changed in 0.6.9).
- On the Linux laptop (if any): `plumbline standup --date <a day with commits>` lists them (`git log --since-as-filter`, git 2.37+, or the fallback).

## 13. Left over from 2b
- While builders run (step 11), check that a builder's `cd <copy>` holds for its next command (its Bash cwd persists), and that `/plumbline:status` shows the builder against your task (its hooks carry your session id; if not, `taskByAgent` finds it).
- A tracer's transcript or `/agents` shows it ran on sonnet; the same for a builder started with `model: sonnet`.
- On the Linux laptop (if any): `plumbline builder merge` works (git 2.38+ for `merge-tree --write-tree`).
- At the end of any task, the "For you" check in the closing message reacts to the message you see (`last_assistant_message`).

## 14. Two sessions in one checkout (0.6.6)
- Open two Claude sessions in one repo. In the first, start a task and build; in the second, ask questions that run `ls` or `git status`. **Expected:** the second is never stopped by the first one's edits or commands.
- In the second, ask Claude to start another task. **Expected: refused** ("open in this checkout", `claude -w`). Start one with `claude -w` instead: **expected:** allowed.
- In the first session's task, let a test command fail (exit 1) after it wrote a file outside the plan. **Expected:** the note about that file comes right after the failing command (PostToolUseFailure fired). Run with `claude --debug` and check the PostToolUseFailure hook input carries `tool_use_id`; if not, a failed command counts as running for 10 minutes.

## 15. Hotfix (0.6.7, 0.6.11)
- Type `/plumbline:hotfix checkout returns 500` while a task is open in this checkout. **Expected: refused**, naming the open task. Close it, then type it again. **Expected:** a small task starts with no question from Claude, and its line says one checkpoint and what the first commit needs.
- Ask Claude to run `plumbline task start` for a hotfix itself. **Expected:** it can't make one; only your typed `/plumbline:hotfix` starts it.
- Ask for "Checkpoint 1". **Expected: refused.** The combined checkpoint without `## Root cause` in plan.md: **refused**, naming it.
- After approval, Claude switches to the production branch on a line of its own. **Expected:** Claude is told the starting point moved and runs `plumbline run test` (and the saved checks) before any edit. To test it, leave a test failing on the production branch only: after the fix, that failure isn't counted as the hotfix's.
- The first commit with only the related tests, lint and build passed: **expected: a confirm prompt** ("hotfix commit"). Then say "done". **Expected: sent back** until the full suite and every reviewer ran, and the report has `## Root cause`.

## 16. Sandbox (0.6.8)
- `/plumbline:setup` explains the sandbox and asks. Say yes. **Expected:** a confirm prompt for `plumbline setup sandbox`; afterwards `.claude/settings.local.json` holds the sandbox settings, git doesn't list it, and `plumbline status` says the sandbox is on.
- Ask Claude to run `ls -a ~/.plumbline` (Plumbline's folder, which holds the seal key `.key`). **Expected:** the sandbox blocks it. Then `plumbline run test`: **expected:** it runs and records (Plumbline's commands are excluded).
- Ask Claude to run `plumbline status | head`. **Expected:** if it fails, the message says to run the plumbline command on its own line.
- `plumbline setup undo-sandbox` (confirm prompt). **Expected:** only Plumbline's entries are gone; a setting you added by hand stays.

## 17. Standup (0.6.9)
- `/plumbline:standup` in a work repo the morning after a day of work there. **Expected:** what you did since your last standup here (the first one starts at the beginning of yesterday), from MemPalace, git and Plumbline's tasks; only your commits; what's next from open tasks and Jira. Check that MemPalace's `since`/`before` search found those sessions (V20).
- Ask for it again an hour later. **Expected:** the same start, not an empty list. `/plumbline:standup Monday` covers that one day and doesn't move the start.
- It asks where your sprint file is, or reads it; each edit to it shows a confirm prompt. Ask Claude to run a script that changes the sprint file. **Expected:** Claude tells you right after, with the changed lines, and nothing is put back on its own.

## 18. Saved checks and the bug test (0.6.10)
- Save a `typecheck` command in the setup (`tsc --noEmit` or the project's own), and try to save `"format": "npx prettier --write ."`. **Expected:** the format one is refused ("rewrites files"); `prettier --check .` saves.
- In a task, after the tests pass, ask Claude to commit before running the typecheck. **Expected: blocked**, naming `plumbline run typecheck`. After it passes: the confirm prompt.
- Give a bug-fix task with a `[bug]` case. **Expected:** after approval, Claude is told to run that test and see it fail before the fix. Ask Claude to fix it first and write the test after; at "done", **expected: sent back**, saying no run showed the test failing. Claude stashes the fix, runs the test (it fails), and pops it; then the finish check is satisfied.
- Ask Claude to waive it itself with `plumbline waive bug-test`. **Expected: a confirm prompt** (the waiver is yours to allow).

When all of these pass, run one real task with only the core on, then everything on, end to end. Ten real Kwenta tasks with no gate bypassed and no gate bug come before Plumbline replaces done-gate anywhere.
