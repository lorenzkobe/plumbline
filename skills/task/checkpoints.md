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
- [repeat-safe] the same refund request twice -> test: src/orders/service.test.ts
- [performance] does not apply, because the refund path handles one order at a time

## Real-world proof
- api: POST /orders/:id/refund returns 409 for a refunded order
- ui: does not apply, because there is no UI change
- schema: does not apply, because no schema change

## Checks
Related tests while building, then the full suite, lint and build. Suite takes about <N> min.

## Branch
fix/TICKET-123 from develop (or: main, as the user chose)

## Team
<lead only for small tasks; who else will run, roughly>
```
Labels: normal, edge, refused, boundary, repeat-safe, bug, performance. Every case gets `test: <file>` or `does not apply, because ...`.

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
<what was checked, findings and what was fixed>

## Outside the ticket
- <reported, not fixed>

## For you
- <decisions or actions only the user can take>
- Commit? <proposed message(s)>. You push.
```
