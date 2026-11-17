# Playbooks

## Bug
1. Reproduce on the real app or with a test first (`plumbline run test`, the saved app command).
2. Find the root cause with a run that shows it, naming the exact line. A symptom fix is not a fix.
3. Check whether the same cause appears elsewhere (SocratiCode, Serena). Report it; fix it only if it's in scope.

## Refactor
1. Before moving code, add tests that lock in the current behaviour, and confirm they pass.
2. Move in small steps, running related tests after each.

## Performance (notes in the plan)
- The busy code paths, the data sizes they see, and what must not happen at that size (N+1 queries, full table scans, unbounded lists).

## Flaky tests
- Re-run once to confirm. Then find the cause and fix the test. Related to the task: fix it as part of it. Unrelated: report it with a proposed fix; fix only with the user's OK, in its own commit. Never ignore it.

## New package
- Ask first: why it's needed, whether it's well maintained, its license, and whether something already in the project could do the job.

## Test data
- Seed or fake data only. Never copy production data locally unless the user approves it for a specific bug.

## No tests or no Docker setup
- New project: ask, then set them up.
- Established project: ask whether test files can be added. If not, keep them local only and never commit them. The proof still exists; it just stays off the repo.

## Secrets
- Use secrets through commands (load and use in one step), so values never appear in the chat. Never repeat a value, never write one to a file, note, memory, Jira or report. List variable names only.
