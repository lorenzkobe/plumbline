# Day-one smoke test (on your laptop)

Run this once after installing, in a throwaway copy of Kwenta with done-gate turned off there only. It checks the assumptions Plumbline is built on. About 15 minutes. If a step fails, note what you saw and stop: that step needs fixing before real use.

Before you start: `claude --version` shows 2.1.295 or later. Start Claude Code in auto mode (the default).

## 1. Hooks load and are fast
- `/hooks`: Plumbline's hooks appear for SessionStart, UserPromptSubmit, UserPromptExpansion, PreToolUse, PostToolUse and Stop.
- `/plumbline:status` runs, and `plumbline version` works when Claude runs it (the plugin's `bin/` is on the PATH).
- Ask Claude to run `git status` five times. It should feel instant (each hook is about 5 ms).

## 2. Setup
- `/plumbline:setup`. Check that the confirm prompt appears for `plumbline setup save` and for `plumbline setup laptop`.
- After saving: `plumbline run test` records a run and says how many tests ran. If it says it couldn't tell, setup needs a `tests_ran_pattern`.

## 3. No code before approval (auto mode)
- Say: "Rename the variable x in some file." Claude should ask whether to start the task workflow. Say yes.
- Before the checkpoint, ask Claude to just make the edit anyway. **Expected: the edit is blocked** (deny holds in auto mode).
- Ask Claude to run the saved test command. **Expected: it runs without asking.**
- Ask Claude to run `node -e "1"`. **Expected: refused, no prompt** (so nothing waits on you during a task).
- Outside any task, ask Claude to run `touch foo.txt`. **Expected: a confirm prompt.** Allow it. **Expected: right after, the work stops** with "a terminal command changed files before any plan was approved". Delete foo.txt.

## 4. The checkpoint, then hands-off building
- Give a small real task and walk away. **Expected: one question prompt (your terminal sound), showing the understanding and the plan, with "Approve" / "Change something".** Pick "Change something" first: the task stays where it is.
- Ask again and pick "Approve". **Expected: Claude is told the user approved** and builds without any more prompts.
- If Claude says Plumbline couldn't read the approval, run Claude with `PLUMBLINE_DEBUG=1`, answer once more, and send `~/.plumbline/debug/askuser-response.json`. The answer shape needs fixing.
- While it builds, check: an edit to a file not in the plan but part of the change goes through; an edit to an auth, migration or package file not in the plan is refused and shows up under "For you" in the report.

## 5. Commit and finish
- Before running tests, ask Claude to commit. **Expected: blocked**, naming the missing proof.
- Let Claude run the tests and write the report, then ask it to commit. **Expected: a confirm prompt** showing the message. Check there's no Co-Authored-By line.
- Ask Claude to push. **Expected: blocked.**
- Ask Claude to say it's done before writing the report. **Expected: it gets sent back once**, then stops and shows you what's missing.

## 6. User-only commands
- `/plumbline:pause`: edits go through without a task; `git push` is still blocked.
- `/plumbline:resume`: gates on again.
- Ask Claude to pause Plumbline itself. **Expected: it can't.**
- `/plumbline:wrong`: marks the last block; `/plumbline:stats` shows it.

## 6b. Unclear commands and MCP tools (0.3.1)
- During a task, before the checkpoint, ask Claude to run `bash -c 'touch x'`. **Expected: refused** (only reading and saved commands).
- Ask Claude to run `CMD=ls; $CMD`. **Expected: refused**, "Plumbline can't tell what this runs".
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

When all of these pass, run one real task with only the core on, then everything on, end to end. Ten real Kwenta tasks with no gate bypassed and no gate bug come before Plumbline replaces done-gate anywhere.
