# Plumbline: writing gate code

A gate refuses what it can't prove is safe. While writing or changing one (a hook check, a refusal, a record Plumbline trusts later), check each way it usually gets bypassed, and add a test for every one that applies:

- Symlinks and hard links: a path that points somewhere else, or a file with a second name. Use `os.Lstat` and the link count (`plainFile` in internal/core/notes.go, which returns true for a missing file, so check the file exists first), not `os.Stat`.
- Letter case on macOS: `Plan.md` and `plan.md` are the same file there. Compare paths with `foldPath`.
- Large or binary files: decide by name and git, not by reading the content, and don't skip a file because it's big or not text.
- Renames and staged files: a rename is a delete plus an add (check both names); the index can differ from the work tree, and a staged file counts as tracked.
- Other worktrees: builder copies under `.claude/worktrees/`, and checkouts the user made with `git worktree`. A path inside one isn't the main checkout.
- Git errors: a git call that fails, or git not answering, refuses. Never read an error or empty output as "nothing changed" or "allowed".
- Messages, prompts and commit text: say only what happened. No "tests pass" when they didn't run, no "fixed" for what wasn't checked, no claim the code doesn't back.

Before the reviewers start, go through skills/task/review-checklist.md and this list over your own diff, and write `self-review.md` (Plumbline refuses reviewers until it's current).
