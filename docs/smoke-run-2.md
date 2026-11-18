# Smoke test run 2 (2026-11-18)

Retest after 0.6.12: steps 3 and 5, then step 8 onward. Setup as in [smoke-run-1.md](smoke-run-1.md): `kwenta-smoke`, Plumbline via `--plugin-dir`, Claude Code 2.1.327.

## Step 3, first try (session 86757349, Plumbline 0.6.12): FAIL
- This session was started without `--debug-file`, so hook decisions come only from the transcript's hook attachments. Start every session with `PLUMBLINE_DEBUG=1 claude --plugin-dir <plumbline> --debug hooks --debug-file ../kwenta-smoke-logs/stepN.log`.
- "Rename the variable x in some file": Claude asked which one (fine). After the pick (sync-service.ts, `isPullBundle(x)`), it tried the Edit before asking about a task; denied "no task is active". **SR10 still happens**; the gate held.
- Claude asked to start a task, then ran `plumbline task start --title "..." 2>&1 | tail -30`. Pre allowed it; the task started (1118-37eaf0) but **didn't bind (TB2)**: `startTailReads` refuses any piped segment, so `tail` stopped the binding.
- The next Edit's deny named the unbound task and said to run adopt on its own line. Claude ran `plumbline task adopt 1118-37eaf0 2>&1 | tail -20`; the user approved the prompt, the CLI printed the adopt line, but adopt binds only when every segment is plumbline (`onlyPlumbline`), so nothing happened and nothing said so. The Edit was denied again, and Claude stopped and asked (the "second time in a row" rule worked).
- Fix: 0.6.13 (TB2 in roadmap.md). Redo step 3 from the start.
