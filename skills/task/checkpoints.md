# Checkpoint and report formats

Plain words throughout. A high school student should follow it. Technical terms get a one-line explanation.

## understanding.md
```
# Understanding: <title>
Size: small | normal | big, because <who calls it, what data it touches, how far a mistake spreads>

## What the task is
<two or three plain sentences>

## What's really happening in the code
- <claim> (src/orders/service.ts:142)
- <claim> (src/orders/service.ts:150-171)
Checked vs assumed: <anything not confirmed in the code, labelled as an assumption>

## Normal cases and what can go wrong
- <case>

## Open questions (each with my suggestion)
- <question>? I suggest <answer>, because <reason>.

## Outside the ticket
- <bigger problem / same bug elsewhere / ticket disagrees with the code>

## Tools
- SocratiCode, MemPalace, Serena used. <Any tool not used and why.>
```
If no existing code is involved (a brand-new project), say "No existing code is involved" instead of file references.

## plan.md
```
# Plan: <title>

## Approach
<what changes and why, in a few lines; sources for anything learned online, with links>

## Files
- src/orders/service.ts
- src/orders/service.test.ts

## Cases
- [bug] refund of an already-refunded order is refused -> test: src/orders/service.test.ts
- [normal] full refund of a paid order -> test: src/orders/service.test.ts
- [edge] refund of 0.00 -> test: src/orders/service.test.ts
- [repeat-safe] the same refund request twice -> test: `src/orders/service.test.ts::refund twice is refused`
- [performance] does not apply, because the refund path handles one order at a time

## Real-world proof
- api: POST /orders/:id/refund returns 409 for a refunded order
- ui: does not apply, because there is no UI change
- schema: does not apply, because no schema change

## Needs your OK
- `npm install zod` (why: schema validation for the refund body; well maintained, MIT; nothing in the project does this)
- none

## Checks
Related tests while building, then the full suite, lint and build. Suite takes about <N> min.

## Branch
fix/TICKET-123 from develop (or: main, as the user chose)

## Team
<small task: lead, code-reviewer; otherwise lead, challenger, plan-reviewer, code-reviewer>
<plus security-reviewer (auth, permissions, payments, user data, secrets), data-reviewer (migrations, queries, models, money math), ux-reviewer (frontend), when the files call for them; the checkpoint is refused without them>

## Challenge note
- Questioned: <finding> -> changed: <what changed in the understanding or plan>
- Questioned: <finding> -> kept, because <evidence>
(Small tasks: "skipped, small task".)
```
Approving the plan approves exactly the commands in "Needs your OK", nothing more.
Labels: normal, edge, refused, boundary, repeat-safe, bug, performance. Every case gets `test: <file>`, better `test: <file>::<test name>` (in backticks when the name has spaces), or `does not apply, because ...`. Naming the test lets the reviewers see when one test is the proof for several cases.

## report.md
```
# Report: <title>
<Bad news first: anything failed, skipped or uncertain.>

## What changed
- <file>: <what and why>

## Proof
- Full suite: <ran>, <failed> (<new failures: none>); lint, build: pass
- Real-world: <api call and response code / clicks / query>
- Weakened tests: <skipped, removed or loosened tests, or "none">
- Waivers: <or "none">

## Review
- Reviews: <reviewer>: <n> runs (code-review-1.md, ...), the last one on the final code; the same for each reviewer
- <finding> -> fixed in <file> / pushed back: <evidence>
- Not fixed, needs you: <or "none">

## Outside the plan
- <file>: <what and why it belonged to this change>

## Outside the ticket
- <reported, not fixed>

## For you
- <files Claude was refused (risky or your unsaved work), with what it would change and why>
- <packages or commands that weren't in the approved list>
- <decisions or actions only the user can take>
- <findings a reviewer upheld after your dispute: "upheld by the <reviewer>: <finding>; your view: <evidence>">
- Jira (ticket tasks): suggested status <status>; the comment is in <TICKET>-jira-comment.md
- Commit? <proposed message(s)>. You push.
```

## <TICKET>-jira-comment.md (Jira tickets)
Only the comment, nothing around it, so the user can copy it as it is:
```
Fixed: a refunded order could be refunded again. The refund now checks the order's state first.
Tests: added cases for repeat refunds and zero amounts. Full suite passes.
```
