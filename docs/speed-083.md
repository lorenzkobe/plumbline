# Speed: before and after 0.8.3

What 0.8.3 is measured against, and where the after numbers go once a real courtly or kwenta task has run on it.

## Before: Plumbline on courtly (0.7.5 or older, 2026-11-23)
From `~/.plumbline/projects/courtly-6373fde735/runs.jsonl` and session 675859e3. All four tasks ran before 0.7.6, so they had neither 0.8.0's reuse of a passing run nor 0.8.1's focused review rounds.

| Task | Check time | Full suite runs | Suite run times |
|---|---|---|---|
| 1123-2a0b55 | 29.2 min | 4 | 5.2, 4.8, 5.1, 5.3 min |
| 1123-067914 | 24.6 min | 3 | 5.3, 6.0, 5.2 min |
| 1123-695434 | 22.0 min | 3 | 5.0, 4.9, 4.8 min |
| 1123-1cd121 | 65.3 min | 7 | 7.3, 5.9, 8.1, 5.7, 6.2, 5.8, 5.9 min |

- Other checks: build median 45 s (15 runs), lint 63 s (16), typecheck 6 s (18).
- Session 675859e3 (3 tasks): 11 `plumbline run checks` calls; 12 questions to the user (7 plan questions, 2 commit, 1 each of Extra checks, Test fixtures and Merge); 21 reviewer runs (code 6, security 6, plan 4, data 4, ux 1) and 3 challenger runs.
- The builders shown as "still running" in that session were resumed with the same agent id before 0.7.6, which opened a second run whose stop closed the new one. 0.7.6 reopens the same run instead.

## Before: done-gate on courtly (0.22.0 to 0.23.1, 2026-11-10 to 11-22)
From the 97 run folders in `courtly/.claude/gate/runs/` that keep a verify history.

| Measure | Median | Middle half |
|---|---|---|
| Check time per task | 11.3 min | 7.5 to 19.2 min |
| `gate verify` calls per task | 2 | 2 to 4 |
| Full suite runs per task | 1 (1.5 for closed runs) | 1 to 2 |
| Full suite run time | 4.5 min | 4.1 to 5.3 min |
| Helper runs per task (skeptic, reviewers, QA) | 8 | 3 to 14 |
| Answers to questions per task | 1 | 0 to 3 |

Standard tier: 9.0 min of checks; large tier: 14.8 min. In 7 runs the session log shows more verify calls than the history kept, so those totals may be low. The done-gate runs are older, and courtly's suite has grown since.

## What 0.8.3 changes in that count
- The suite before the first edit is gone (lint, build and the other checks still run then, about 2 minutes on courtly); a failing test run is compared once on the approved code instead.
- `plumbline run checks` runs related tests while building; the full suite runs once with `--final`.
- Only the typecheck is skipped, after markdown-only changes; lint, format, build and security always rerun (courtly's `next build` type-checks tests).
- A normal task's review round is the code reviewer plus the specialists its non-test files call for; the plan reviewer joins only for files that decide which checks run.
- "Approve and commit when green" takes away the commit question.

Target from the brief: a normal courtly task with at most 10 minutes of check time.

## After (to fill in from a live task)
| Task | Version | Check time | Full suite runs | Base runs | Reviewer runs | Questions to you |
|---|---|---|---|---|---|---|
| | 0.8.3 | | | | | |
