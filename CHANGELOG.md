# Changelog

## 0.3.0: phase 1b, the challenger and one reviewer
- New read-only helpers `plumbline:challenger` and `plumbline:reviewer` (same model as the session), sharing one checklist; findings scored, only 80 or more reported.
- The checkpoint needs a finished challenger run on both documents and a `## Challenge note` in plan.md (normal and big tasks; at most 2 rounds).
- The finish check needs a review of the current code and a `## Review` section in a report written after it (at most 3 reviews: the first plus 2 fix rounds).
- Helpers can only read, run read-only commands, and write their own findings file; the lead can't edit those files. Helper runs are recorded by SubagentStart/SubagentStop with the code fingerprint at both ends.
- Waiting on a background helper is no longer sent back as an unproven "done".
- `parts.challenger` / `parts.reviewer` in the setup file switch them off per project.
- Fixed: `plumbline docs-dir`, `status`, `task show` and other read-only plumbline commands were refused before approval.
- Fixed: the code fingerprint could miss a same-size edit made in the same second git last indexed the file, so proof could count for code that had changed.

## 0.2.0: hands-off by default
- One checkpoint with the understanding and the plan together (header "Checkpoint"); "Checkpoint 1" stays optional for very unclear tasks.
- Nothing waits on a prompt during a task: unsaved commands before approval are refused instead of asked; while building, same-change files outside the plan are allowed and must be in the report, risky files outside the plan and your unsaved work are refused and must be under "For you".
- New plan section "## Needs your OK": packages and outside-the-laptop commands it lists run without a prompt once you approve.
- Changes outside the plan while building are reported to Claude instead of stopping the work. Real decisions are still asked right away.
- More than 3 files outside the plan stops and asks: the plan was likely wrong.
- Replaces the old per-file confirm prompt and the two-checkpoint default.

## 0.1.0: phase 1a, the core
- Task workflow with two checkpoints on Claude Code's question prompt (auto mode, no plan mode).
- Edit backstop, Bash gate, commit rule, finish check, changed-files check, user-only pause/resume/cancel/wrong.
- Recorded proof tied to the code fingerprint; baseline-aware "no new failures"; "0 tests ran" fails.
- Project and laptop setup, task logs with 6-month detail and 1-year summaries, stats.
