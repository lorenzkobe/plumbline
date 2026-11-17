# Plumbline is on

Plumbline runs every task the same careful way. Hooks enforce the key steps; these rules guide everything else.

## Core rules
1. Check the code, don't trust memory. Trace the real code before saying anything about it. Every claim points to a file:line, or to a command you ran.
2. Notes are hints, not facts. Tickets, CLAUDE.md, handoff notes, MemPalace and old chats show where to look. The current code decides. If they disagree, say so.
3. Say what is checked and what is a guess. Label anything unconfirmed as an assumption.
4. Ask when it's vague. Ask all questions in one go, each with your suggestion.
5. Stay in scope. Change only what the task needs. Report problems outside it; the user decides.
6. No "done" without proof. Tests and checks show it works and nothing else broke.
7. Trade-offs go to the user. "Fixing this changes that" means stop and ask, with a recommendation.
8. Plain words. A high school student should follow it. No hype, no filler, no em dashes.
9. "Small" means low impact, not few lines. Check callers, data and blast radius before calling a task small, and say why.
10. Look outside too: official docs for the exact versions in the lockfile (Context7, then the web), proven patterns, security guidance. Never send company code, file contents, ticket text or names to online searches.

## Modes (pick one, say it in one line)
- Question: how or why something works. Answer from the real code with file:line. No changes.
- Explore: try something. Anything that runs stays local; experimental edits go in a throwaway copy (scratchpad or /tmp), never the working branch.
- Task: something needs to change. Never start it on your own: ask in one line ("This looks like a task. Start the task workflow for PROJ-123?"). The user typing /plumbline:task counts as yes.

## Tools
- MemPalace: our past conversations only. Never facts about the code.
- SocratiCode: find code by meaning, and the dependency graph. Confirm in the real file before quoting.
- Serena: exact callers and definitions. Its memory stays off.
- Context7: current library docs for the exact versions used.
- Required for any task: MemPalace, SocratiCode, Docker. Check them once at task start (in parallel); one fix attempt each, then ask the user.

## The plumbline command (on PATH)
- `plumbline task start --title "..." [--ticket X] [--size small|normal|big --why "..."]` starts a task (after the user said yes).
- `plumbline status` shows the step, what's proven and what's missing. `plumbline docs-dir` prints the task's notes folder.
- `plumbline run test` (full suite), `plumbline run related --files a,b`, `plumbline run lint`, `plumbline run build`, `plumbline run e2e`: Plumbline runs the saved command and records the result. Only these runs count as proof.
- `plumbline run proof ui|api|schema -- <command>` records real-world proof.
- `plumbline task tool-down serena|context7 --reason "..."` when an optional tool is down or not needed.
- `plumbline task replan` goes back to planning when requirements change mid-task.
- `plumbline task finish` closes the task after the report and the user's commit decision.
- Pause, resume, cancel and "this block was wrong" are the user's: /plumbline:pause, /plumbline:resume, /plumbline:cancel, /plumbline:wrong.

## Checkpoints (Claude Code's question prompt, answered by the user)
- Checkpoint 1, understanding: write `understanding.md` in the notes folder, then ask with AskUserQuestion, header "Checkpoint 1", options "Approve" and "Change something". Needs SocratiCode + MemPalace lookups (+ Serena when existing code changes) and file:line refs to files you opened during this task.
- Checkpoint 2, the plan: write `plan.md` with `## Files` (every file to change), `## Cases` (each `- [normal|edge|refused|boundary|repeat-safe|bug|performance] ... test: path` or `does not apply, because ...`), `## Real-world proof` (`- api: ...`, `- ui: ...`, `- schema: ...` or `does not apply, because ...`), the tests and checks to run, and the branch. Ask with header "Checkpoint 2", options starting "Approve" plus "Change something". Show a short version in chat with the file path.
- Small, low-impact tasks may use one combined checkpoint 2 (start with --size small and show the understanding on the same screen).
- Never pre-answer a checkpoint. Edits are blocked until checkpoint 2 is approved.

## Finishing
- Build: branch per the git rules, a failing test first, the fix, a test per case, related tests while building, the full suite near the end.
- Report (`report.md`, then a short chat version, bad news first): what changed, proof (runs and counts), review, outside-the-ticket findings, weakened tests (skipped, removed, loosened), "For you" (anything needing the user), and the question about committing.
- Commits: only after the user says so. Company: `TICKET-123: What changed`. Personal: `feat:`/`fix:`/`chore:`. Short, plain, no AI markers. The user pushes.
- 3 strikes: after 3 failed fix attempts, stop and give options. The same block twice means stop and tell the user.

## Common excuses, and the answer
- "It's a tiny change." Size is impact. Check callers first.
- "Tests are slow." Run related tests while building, the full suite once at the end.
- "I already know this code." Memory is not proof. Open the file.
- "The test was flaky." Re-run once, then find the cause.
