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

## Reuse
Searched: <queries and places: SocratiCode "refund state check", src/orders/, src/payments/>. Found: <src/orders/guards.ts:12 already checks the state; reused> / nothing that does this job.

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

## Goal
Done when: <what the user will see or be able to do, in their words; and what stays the same> (one line, first: the plan card opens with it, the reviewers and the report check the work against it)

## Approach
<what changes and why, in a few lines; sources for anything learned online, with links>

## Files
- src/orders/service.ts
- src/orders/service.test.ts (new)
(A task that only runs commands and changes no files: the single line `- none (commands only)`. Every case is then "does not apply, because ...". It needs no saved checks, blind QA, e2e or reviewers; real-world proof and the report still count. Any file edit is refused until a replan lists the files.)

## Cases
- [bug] refund of an already-refunded order is refused -> test: src/orders/service.test.ts
- [normal] full refund of a paid order -> test: src/orders/service.test.ts
- [edge] refund of 0.00 -> test: src/orders/service.test.ts
- [repeat-safe] the same refund request twice -> test: `src/orders/service.test.ts::refund twice is refused`
- [failure] the payment provider times out: the order stays unrefunded and the error reaches the caller -> test: src/orders/service.test.ts
- [concurrent] two refund requests for one order at once: exactly one succeeds -> test: src/orders/service.test.ts
- [performance] does not apply, because the refund path handles one order at a time

## Real-world proof
- api: POST /orders/:id/refund on a refunded order returns 409 with code "already_refunded", and the order's refunded total is unchanged
- ui: does not apply, because there is no UI change
- schema: does not apply, because no schema change

## Needs your OK
- `npm install zod` (why: schema validation for the refund body; well maintained, MIT; nothing in the project does this)
- none

## Guards loosened
- the ask before a branch switch on a dirty tree (internal/core/pretool.go:1648 `contains(args, "-b")`): reached by `git switch`, `git checkout`, combined short flags (`-qf`), abbreviated options (`--disc`), `--`, `-C <dir>`, `GIT_WORK_TREE=`; it was also the only check for `git checkout <file>`. Lets through only `git switch <name>` and `-q`.
(Every plan has this section. A guard is any ask, refusal, permission check or validation the plan removes or narrows, or a new path that runs without asking. Each one cites its code (file:line) and lists every way to reach it, and what else it was the only check for. Write the single line `- none` when the plan loosens nothing. A plan that lists a guard gets the guard reviewer before the checkpoint, at any size.)

## Suggestions
- Related: <something close to this change worth doing too, and what it adds> (for example: CLAUDE.md:450 still quotes the old label; update it too)
- Not related: <something you noticed elsewhere, left alone> (for example: "Not sent yet" and "Not synced yet" can show at once on a new device)
(Leave the section out when there's nothing worth saying. Suggestions aren't part of the plan: Approve doesn't do them. The plan card shows them; the user picks "Change something" to add one, and then it goes in the plan.)

## Checks
Related tests while building and fixing, then the full suite once after the last review, with lint and build. Suite takes about <N> min.

## Branch
fix/TICKET-123 from develop (or: main, as the user chose)

## Team
<small task: lead, code-reviewer; otherwise lead, challenger, code-reviewer; plus plan-reviewer when a file that decides which checks run changes>
<plus security-reviewer (auth, permissions, payments, user data, secrets), data-reviewer (migrations, queries, models, money math), ux-reviewer (frontend), when non-test files call for them; the checkpoint is refused without them>
<workers, if used: tracers before the checkpoint; builders with the part each builds and its model (session model for tricky or risky parts, sonnet for simple ones); qa (required for big tasks, runs first); tester>

## Challenge note
- Questioned: <finding> -> changed: <what changed in the understanding or plan>
- Questioned: <finding> -> kept, because <evidence>
(Small tasks: "skipped, small task". When the guard reviewer ran, answer its findings here too, small tasks included.)
```
Approving the plan approves exactly the commands in "Needs your OK", nothing more. Deploys, deletes, production writes and commands on other machines are still confirmed one by one when they run.

Design tasks (/plumbline:design) add, in plan.md:
```
## Design
Direction B (https://claude.ai/artifact/...): calm and editorial. Screens: order list, order detail.
Sizes: phone 360px first, tablet 768px, desktop 1280px. States: loading, empty, error, long names.
```
and `- ui: screenshots of the real screens at phone, tablet and desktop sizes against the mockup` under "Real-world proof". The direction named here must be the one the user picked.

## design.md (design tasks)
```
# Design: <title>

## Brand kit
<where it is (docs/brand.md, src/theme.ts), or the kit agreed with the user for a new project>

## Directions
- Direction A: dense and fast, <link>, best for power users; less breathing room
- Direction B: calm and editorial, <link>, easiest to scan; fewer items per screen
- Direction C: card-free list, <link>, ...
```
Bullets straight under "## Directions", at least 3.
Labels: normal, edge, refused, boundary, repeat-safe, bug, performance, failure, concurrent. Every case gets `test: <file>`, better `test: <file>::<test name>` (in backticks when the name has spaces), or `does not apply, because ...`. Naming the test lets the reviewers see when one test is the proof for several cases. Each case is one a plausible wrong version of the code would fail.
- `failure`: a dependency is down, slow or returns garbage, or the run crashes halfway. Say what's left behind and how a retry treats it (a write sent outside after the database commit has nothing to roll back).
- `concurrent`: two requests or jobs at once. Say what stops both doing the same work. The test releases both from one barrier so they really overlap; a mock that answers instantly hides the race.
- `bug`: name the test (`test: <file>::<test name>`), so its failing run before the fix is matched by name. The finish check needs a recorded run in which it failed while only tests had changed since approval, with the test file as it is at the end.
- These bugs often don't exist yet, so show the test can fail: break the guard on purpose (remove it, reorder the step), see the test fail, then put it back and check the diff shows it restored.
Every line in the plan decides something. "TBD", "handle edge cases", "add appropriate validation" or "write tests for the above" are refused: name the cases, checks or values.

## The plan in chat (before the plan question)
The plan for the user, as a chat message right before the plan question (not a file: a file shows up as a diff, which is hard to read). Most people using Plumbline want their work eased and run on its own, so this part is plain: what's wrong, what changes and what they'll notice, in full sentences, with nothing left out that they'd want to know. No file names, function names, code terms, test names or numbers only a developer reads (explain a needed term in a few words); those stay in plan.md, linked on the last line. Every heading in Title Case. Small tasks: the goal and two or three full sentences. Normal and big tasks:
```
**Done When:** <the plan's goal, as a full sentence>

## The Problem
<how it works today and why that's a problem, in a few sentences; what you assumed about the request>

## The Fix
<what changes, in plain words>

## What You'll Notice
<what the user will see or be able to do afterwards, and what stays the same>

## Risks
<each real risk and how it's handled; "Nothing outside X changes." when that's true>

## How It's Checked
<the tests and checks in plain words, and who reviews it>

## What I Need From You
<decisions or access, or "Nothing. Just Approve (or Approve and commit when green), Change Something or Drop It.">

## Also Worth Doing
<plan.md's suggestions, "Related, want it in?" and "Not related, just noticed" apart; leave the section out when there are none>

Technical details: <plan.md's full path>
```
Then the question itself (AskUserQuestion, header `Checkpoint`) holds the problem, the fix and the risk in two or three short sentences, because the question box can cover the chat (on a phone or on the web the user may see only it). When the work goes on a new branch, it says which ("I'll do it on a new branch, fix/search-deleted."), so the user can say main instead before any edit. For example: "The Profile screen is one long page, with rare tools next to Sign Out. I'll group it into Account, Activity and This Device, with the rare tools in a closed section. Risk: low, since nothing about your data changes. Approve?"

## report.md
```
# Report: <title>
<Bad news first: anything failed, couldn't run or uncertain.>

## Verdict
<Ready | Ready with noted limitations | Not ready>: <why, in one line>
Goal: met | not met: <what's missing> (against the plan's ## Goal)
Rollback: <how to undo it: revert the commit, the down migration> / <why it can't be undone: data deleted, sent outside, a public API removed>

## What changed
- <file>: <what and why>

## Proof
- Full suite: <ran>, <failed> (<new failures: none>); lint, build (and format, typecheck, security when saved): pass
- Real-world: <the api call and what the response shows changed / clicks / query>
- <check>: couldn't run, because <why>; run later: `<exact command>`
- Weakened tests: <each file the diff weakens (skipped, removed or loosened tests, silenced checks, a CI step allowed to fail), with why, or "none">
- Comments kept: <each file with a new comment the user wrote or a tool generated, with which; leave the line out if none>
- Waivers: <or "none">

## Review
- Self-review: <what your own pass over the checklist caught and fixed before the reviewers, or "nothing">
- Reviews: <reviewer>: <n> runs (code-review-1.md, ...), the last one on the final code; the same for each reviewer
- Builders (if used): <builder n>: <part>, merged / dropped, checked how
- Blind QA (big tasks): <qa-1.md>: <its tests that failed before the code, and any problem they found>
- [<score>] <finding> -> fixed in <file> / pushed back: <evidence> (findings at 80 or more, in full)
- Under 80: <one line each: finding, and what you did with it>
- Should-fix not fixed: <each, with why> / none
- Not fixed, needs you: <or "none">

## Common-sense calls
- <a call you made without asking, and what it costs if it's wrong> / none

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
