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

## 7. Helpers
- Ask Claude to use a helper that starts another helper. **Expected: the inner start is blocked.**

When all of these pass, run one real task with only the core on, then everything on, end to end. Ten real Kwenta tasks with no gate bypassed and no gate bug come before Plumbline replaces done-gate anywhere.
