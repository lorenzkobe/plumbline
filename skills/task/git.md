# Git rules

## Company projects
- Start: `git fetch`, confirm the base branch (usually develop or dev), `git checkout <base> && git pull`, then `git checkout -b fix/TICKET-123` (or feat/, hotfix/).
- Commits: `TICKET-123: Fix log format`. Always prefixed, so cherry-picks stay traceable.

## Personal projects
- Ask: work on main, or make a branch?
- Commits: `feat: ...`, `fix: ...`, `chore: ...`.

## Both
- Short, plain commit messages: one line saying what changed, plus a short body only when the reason isn't obvious. They become the PR description, so they must read clearly on their own.
- No AI markers of any kind (no Co-Authored-By: Claude, no session links, no "Generated with"). The commit hook blocks them.
- Group commits by what works together: usually one per slice. Parts that only work together stay together.
- The user pushes and opens PRs. Where pushing or merging triggers a deploy, say so ("this branch deploys to staging when pushed").
- Uncommitted changes when switching branches: stop and ask.
