---
name: hotfix
description: Plumbline's hotfix flow, for when production is broken and the smallest fix that stops the damage has to go out first. Only the user can run this. Argument: what's broken, or the ticket id.
argument-hint: "what's broken, or TICKET-123"
disable-model-invocation: true
---
# Hotfix

Hotfix: $ARGUMENTS

Plumbline's hook has already handled this command (it runs only when the user types it). Say in one line what it reported. If it didn't start a hotfix task, stop there: its message says what the user can do.

A hotfix is the [task workflow](${CLAUDE_PLUGIN_ROOT}/skills/task/SKILL.md) with the changes below. Everything else stays, the safety rules included: no production writes, deploys, pushes or Jira posts without the user, no secrets in the chat.

1. Read and search as in the task workflow, only faster: the ticket and its comments (Jira tasks), the tools check, SocratiCode, MemPalace and Serena, the real files. Find what broke, and reproduce it with the saved commands where you can.
2. Branch from the branch production runs from (usually main or master, not develop; ask when unsure): `hotfix/TICKET-123` for company projects. Name it in the plan's `## Branch`; the switch happens right after the checkpoint, as in the task workflow (Plumbline moves the task's starting commit with it). Say in the report that the fix also goes back into the base branch (the user merges it).
3. The fix is the smallest change that stops the damage: no refactors, no clean-ups, nothing the fix doesn't need.
4. Write `understanding.md` and `plan.md` as in the task workflow, kept short. plan.md adds `## Root cause`: what really broke, with file:line, and either "this fix is the root-cause fix" or the follow-up task that fixes it properly (a normal task, after the hotfix). The task is small, so there's no challenger.
5. One checkpoint: the combined one (header `Checkpoint`), once both files are written. Checkpoint 1 is refused.
6. Build: a failing test for the bug first when one can be written quickly, then the fix: `plumbline run related --files <the [bug] case's test>` before the fix, so its failing run is recorded (the finish check needs it; if the fix had to go first, see step 9). Then `plumbline run related --files <changed files and the case tests>` and every saved check (`plumbline run lint`, `plumbline run build`, and `format`, `typecheck`, `security` when the setup saves them): that is what the first commit needs, not the full suite.
7. Tell the user it's ready to commit, with what was proven and what runs next, and ask. They push and deploy.
8. Right after the commit, before anything else: `plumbline run test` (the full suite; the tester can run it in the background), the real-world proof, and every reviewer in the plan's `## Team`. Fix what they find in a new commit; that one needs the full suite, like any last commit.
9. A `[bug]` case whose test was written after the fix still needs its failing run before the finish check: put the files that aren't tests back as they were at approval (`git stash push --include-untracked -- <files>` before a commit; after one, `git checkout <the commit before the fix> -- <files>`, which asks the user), run the test and see it fail, then put the fix back (`git stash pop`, or `git checkout HEAD -- <files>`). If it truly can't fail without the fix, ask the user, who can waive it (`plumbline waive bug-test`).
10. Write the report as in the task workflow, plus `## Root cause` (as in the plan; when the root cause isn't fixed here, the follow-up task also goes under "For you"). Then `plumbline task finish`.
