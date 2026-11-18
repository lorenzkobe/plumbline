# Playbooks

## Bug
1. Reproduce on the real app or with a test first (`plumbline run test`, the saved app command).
2. Before testing any idea, write down a few guesses, each with what it predicts you'll see. Then run the checks that tell them apart.
3. Find similar code that works and list every difference from the broken path.
4. Find the root cause with a run that shows it, naming the exact line. A symptom fix is not a fix, and a guard that "might help" doesn't ship. Tag temporary debug logs `[PLB-DEBUG]` so they're easy to find and remove; the finish check refuses while one is left.
5. Two failed fixes that share an assumption: write the assumption down and test it. After three, question the approach, not the fix (and the 3-strikes rule applies).
6. Check whether the same cause appears elsewhere (SocratiCode, Serena). Report it; fix it only if it's in scope.

## Refactor
1. Name the behaviour that must not change, in the plan.
2. Before moving code, add tests that lock in that behaviour, and confirm they pass. Type checks and lint don't lock in behaviour.
3. Move in small steps, running related tests after each.
4. Renames: search strings, docs, config and messages for the old name too, not only the code that refers to it.
5. If the result isn't easier to follow than before, revert it.

## Changes that reach many callers
1. Add the new form beside the old one.
2. Move the callers over in batches, running related tests after each.
3. Delete the old form last, once a search finds no caller left.

## Performance (notes in the plan)
- The busy code paths, the data sizes they see, and what must not happen at that size (N+1 queries, full table scans, unbounded lists).

## Flaky tests
- Re-run it several times (more for a test guarding money, auth or data) to see how often it fails. Then find the cause and fix the test. A test that passes only on a retry isn't passing, so never fix it with a retry. Related to the task: fix it as part of it. Unrelated: report it with a proposed fix; fix only with the user's OK, in its own commit. Never ignore it.

## New package
- Ask first: why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.

## Package upgrade
1. Before any change, run the suite, lint and build and keep the results (the checkpoint asks for them).
2. Read the changelog between the two versions. For each change that alters behaviour without an error, point to a test that covers it, or add one.
3. After the upgrade, run the same commands again and compare.
4. Name every test you edited in the report, with what changed and why: a test changed to fit the upgrade may hide a break.

## Test data
- Seed or fake data only. Never copy production data locally unless the user approves it for a specific bug.
- Tests run against throwaway targets: their own database (in `.env.test`), never the dev database or anything off the laptop.

## No tests or no Docker setup
- New project: ask, then set them up.
- Established project: ask whether test files can be added. If not, keep them local only and never commit them. The proof still exists; it just stays off the repo.

## Secrets
- Use secrets through commands (load and use in one step), so values never appear in the chat. Never repeat a value, never write one to a file, note, memory, Jira or report. List variable names only.
