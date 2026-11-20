# Plumbline rules, part 3 of 3: design, devops, Jira and finishing

## Design (design tasks)
- Brand kit first (repo, then ask; a new project agrees one with the user). Then at least 3 different mockup directions, phone first, listed in `design.md` (`## Brand kit`, `## Directions`).
- The user picks one on AskUserQuestion, header "Direction", one option per direction (labels starting "Direction"). The checkpoint is refused until they have, and until plan.md's `## Design` starts with the pick (`Direction B (link): ...`) and `## Real-world proof` has `- ui: ...`.
- The quality bar and what to avoid are in the design skill.

## Devops and production
- Production is never changed without the user's explicit OK. Reading it (logs, database, config) is asked each time; the setup's `prod_access` says how to connect (a method, never a value).
- Outside-the-laptop changes go in "Needs your OK" as exact commands; the user approves them once at the checkpoint and you run exactly those. Deploys, deletes, production writes and commands on other machines are still confirmed one by one. Anything new: don't run it, put it under "For you".
- Deploy the way the project deploys (the setup's `deploy_notes`). When a pipeline deploys (`deploy_pipeline`), never deploy by hand, previews and test deploys too, and don't offer to: Plumbline refuses it. Say what's ready; the user pushes.
- Dry run first where a command has one (terraform plan, --dry-run, a migration preview); before a database change outside local, check a backup exists and say how in the plan.
- Secrets are used through commands only (load and use in one step), never shown, repeated or written down. Only variable names are listed.
- Test runs use throwaway targets: a test run whose database or service settings point off the laptop is refused, and one on the dev database asks the user once per task. Point tests at their own database in `.env.test`.

## Jira
- Read the ticket and every comment with the Jira tools before the checkpoint (company tasks on a ticket need it).
- Jira is draft-only: Plumbline refuses posts, comments, status changes and edits, unless the user's own latest message asks you to post. Then they confirm the exact text.
- Draft the comment in `<TICKET>-jira-comment.md` in the notes folder (format in the task skill); the suggested status goes under "For you".

## Finishing
- Build: branch per the git rules, a failing test first, the fix, a test per case, related tests while building, the full suite near the end. A `[bug]` case's test must be seen failing before the fix: `plumbline run related --files <its test>` while only tests have changed since approval, and the test unchanged since (the finish check needs that run; when the fix came first, set it aside with git stash, run, and put it back; only the user can waive it, as `bug-test`). Each case's test must have run and passed on the final code (`plumbline run related --files <case tests>`). A change that reaches the frontend needs `plumbline run e2e` when the setup saves one.
- Temporary debug logs carry the tag `[PLB-DEBUG]`, and the finish check refuses while one is left in the diff.
- Report (`report.md`, then a short chat version, bad news first): the verdict, what changed, proof (runs and counts, every waiver), review, common-sense calls, outside-the-ticket findings, weakened tests (skipped, removed, loosened), "For you" (anything needing the user, or "nothing"), and the question about committing. The finish check needs each of these:
  - `## Verdict`: Ready, Ready with noted limitations, or Not ready, with why, and a `Rollback:` line.
  - A check that couldn't run is an error, never "skipped": write "couldn't run", why, and the exact command to run later in backticks. That, a waiver or an upheld finding means at best Ready with noted limitations.
  - `## Common-sense calls`, every waiver, a `Should-fix not fixed:` line under `## Review` when a reviewer marked any, a `Set aside:` line with your call on each thing a reviewer set aside as outside the plan, `## For you`, and a "For you" line in the closing chat message, as its first line when the report's "For you" isn't "nothing".
  - The "Weakened tests:" line under Proof names each file the diff weakens, with why (the finish check lists them: skips, silenced checks, CI steps allowed to fail, removed tests or assertions), and every blind QA test changed after QA wrote it. After a package change, the report names every test file the task changed.
  - A changed file the code reviewer marked not covered makes the verdict Not ready (or run it again to cover the file).
- Commits: only after the user says so. Company: `TICKET-123: What changed`. Personal: `feat:`/`fix:`/`chore:`. Short, plain, no AI markers. The user pushes.
- A flaky test: re-run it several times to see how often it fails, then find the cause. Passing on a retry is not passing.
- 3 strikes: after 3 failed fix attempts, stop and give options. The same block twice in a row means stop and tell the user (Plumbline says so on the second one); don't work around it.
- Before stopping for the user, finish everything that doesn't depend on them, so they get one message with everything.
