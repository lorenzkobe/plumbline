# Changelog

## 0.2.0: hands-off by default
- One checkpoint with the understanding and the plan together (header "Checkpoint"); "Checkpoint 1" stays optional for very unclear tasks.
- Nothing waits on a prompt during a task: unsaved commands before approval are refused instead of asked; while building, same-change files outside the plan are allowed and must be in the report, risky files outside the plan and your unsaved work are refused and must be under "For you".
- New plan section "## Needs your OK": packages and outside-the-laptop commands it lists run without a prompt once you approve.
- Changes outside the plan while building are reported to Claude instead of stopping the work. Real decisions are still asked right away.
- Replaces the old per-file confirm prompt and the two-checkpoint default.

## 0.1.0: phase 1a, the core
- Task workflow with two checkpoints on Claude Code's question prompt (auto mode, no plan mode).
- Edit backstop, Bash gate, commit rule, finish check, changed-files check, user-only pause/resume/cancel/wrong.
- Recorded proof tied to the code fingerprint; baseline-aware "no new failures"; "0 tests ran" fails.
- Project and laptop setup, task logs with 6-month detail and 1-year summaries, stats.
