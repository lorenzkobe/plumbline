# Reference repo study (2026-11-18)

What the six reference repos do that overlaps Plumbline, what is worth taking, and where it goes. Done before release 2, as docs/known-gaps.md's "Decisions (2026-11-18)" asks.

## How it was done
- Repos: `../plumbline-repo-reference/` superpowers (obra, MIT), skills (Matt Pocock, MIT), Keystone (MIT), pstack (MIT), ralph-loop (Cursor, MIT), whetstone (Every, MIT).
- Read only, nothing run: no scripts, tests, package installs or git hooks from the clones. Their agent instructions were treated as text to study, not followed.
- Only the parts that overlap Plumbline: how skills trigger, tests first, review helpers, writing and checking plans, hooks, and how they test their own prompts.
- Two passes. First, four helpers read superpowers, skills, whetstone and pstack (one each) while the lead read Keystone and ralph-loop. Second, the lead read the release-2 parts of all four big repos itself:
  - Keystone: all nine skills, the six gates, the shared standards and handoff packet, the routing cases.
  - superpowers: test-driven-development (both files), requesting-code-review (both), subagent-driven-development (the skill, three prompts, seven references), verification-before-completion, writing-plans, systematic-debugging (with defense-in-depth and root-cause-tracing outlines), receiving-code-review.
  - whetstone: ia-code-review (the skill plus check-categories, severity-and-confidence, review-judgment-traps, false-positive-suppression, reviewer-trust-boundary, reliability-patterns, report-and-integration, parts of deep-review), ia-writing-tests (the skill plus regression-proof, test-completion, test-smells, rationalization-table, oracle-smells, part of false-pass-oracle-traps), ia-verification-before-completion (the skill plus proof-integrity, verification-oracles, claims-and-failures), the hooks list.
  - pstack: all 26 principle skills, 11 playbooks, worker.md, the review parts of poteto-mode/SKILL.md, the comment agent.
  - skills: code-review, codebase-design, tdd.
  - Not read by the lead: whetstone's language and security catalogs (language-profiles, security-patterns, review-traps-catalog, scope-resolution), superpowers' executing-plans and writing-skills body, pstack's statechart scripts, the rest of the skills repo beyond the helper's pass.
- Every file:line below was checked against the clone. One correction from the first pass: whetstone does default to tests after the code for new features (`whetstone/plugins/whetstone/skills/ia-writing-tests/SKILL.md:19`); the first write-up said it didn't, because a grep cut that line short.
- All six are MIT. The plan is to borrow ideas and write Plumbline's own words, so no license notice is needed. Copying text word for word would need the repo's notice.
- Paths below start at the repo folder (`Keystone/...` means `../plumbline-repo-reference/Keystone/...`). `W/` is `whetstone/plugins/whetstone/`.

## The short version
- Keystone is the closest to Plumbline: shared pass/fail gates for "test seen failing first", proof, review and shipping. Its proof gate and shipping verdict fit WF6 almost directly.
- WF1 (reuse) gets tests a reviewer can apply, mainly from skills, pstack and whetstone: "delete it and see if complexity comes back", "one adapter is a guess, two is real", "wait for the third copy". The repos disagree on one point Plumbline has to decide: validate at every layer, or only at boundaries (see "Where the repos disagree").
- WF2 (compatibility, hidden failures, test quality) has the most material. whetstone's reliability and test-oracle lists are the deepest; the four big repos agree on the test-quality core.
- WF5 (`[failure]`, `[concurrent]`) gets concrete proof methods: show the test failing on a broken copy, and release racing threads from one barrier instead of hoping for a collision.
- WF6 (verdict) gets a rule the finish check can enforce: a check that couldn't run is an error, never "skipped", and blocks Ready.
- Two repos measured how to word prompts; release 2 should follow it.
- Two of the repos call Plumbline's blind QA pattern an anti-pattern. That needs your call (see "Where the repos disagree").

## Release 2: what to take

### WF1. Reuse before writing new logic
Core rule (short, in rules.md):
- Search for code that already does the job by the idea, not the ticket's words, and say where you looked. `skills/skills/engineering/triage/SKILL.md:70`.
- Agents copy the nearest example, so the search has to go past the files already open. `pstack/skills/correct/SKILL.md:11`.
- A search that finds nothing proves little unless it could have found something: run it once on a case you know exists. whetstone: "Prove a search could find a known positive control" (`W/skills/ia-code-review/SKILL.md:31`). This also covers "nothing else calls this" claims.
- Suggestion: a `## Reuse` line in understanding.md ("Searched: <queries, places>. Found: <file:line> / nothing"), so the checkpoint and the challenger can see the search happened.

Review checklist (in review-checklist.md, loaded by helpers only):
- The deletion test: imagine deleting the abstraction; if complexity vanishes it only passed calls through. `skills/skills/engineering/codebase-design/SKILL.md:63`.
- One adapter is a hypothetical seam, two is a real one. `skills/skills/engineering/codebase-design/SKILL.md:65`.
- Collapse wrappers with one caller, adapters with no second implementation, pass-through methods. `pstack/skills/principle-minimize-reader-load/SKILL.md:11`.
- A pattern is suspicious when it has one call site and no variation, hides a simple operation behind a vague name, or exists because "AI defaulted to `manager`, `provider`, `service`, or `helper`". `Keystone/skills/_shared/engineering-standards.md:39-44`.
- Two copies stay inline; extract on the third. `W/skills/ia-simplifying-code/references/simplification-patterns.md:10`.
- A new branch-set (switch, if-chain, map) over a value already switched on elsewhere: one shared mapping instead. `W/skills/ia-code-review/references/check-categories.md:14`.
- A new file that repeats a job the repo already does with another tool (a second task runner, a second lint config). `W/skills/ia-code-review/references/check-categories.md:31`.
- Duplication is never fine when it touches security, billing, data access or API behavior. `pstack/skills/poteto-mode/references/bugbot-triage.md:51`.
- Self-contained files (prompts, skills, per-service config) may stay duplicated until the copies drift. `W/skills/ia-simplifying-code/SKILL.md:65`. Test helpers may repeat production setup for clarity (`W/skills/ia-code-review/references/false-positive-suppression.md:37`), but expected values still come from an independent source (see WF2).
- A reviewer's "do it properly" suggestion: check the code is actually used before building more. `superpowers/skills/receiving-code-review/SKILL.md:88-96`.
- Named smells with their fix: Duplicated Code, Speculative Generality, Middle Man, Shotgun Surgery. `skills/skills/engineering/code-review/SKILL.md:46`, `:51`, `:53`, `:55`. Not Mysterious Name: Plumbline's checklist already says naming is style, not a finding.
- How serious: over-engineering and defensive noise are simplification findings (should-fix; the fix is usually deletion), not bugs. `W/skills/ia-code-review/references/check-categories.md:53-60`. Verbatim duplication of a logic block, swallowed errors and tests that assert nothing are must-fix. `superpowers/skills/subagent-driven-development/task-reviewer-prompt.md:148-152`.

### WF2. Compatibility, hidden failures, leaks, deploy mistakes, test quality
Core rule:
- Keep public contracts (APIs, CLIs, stored data, config, env vars) working unless the user asked to break them. Internal callers can be moved over in one go instead of keeping shims; pstack bans shims only when "No external users depend on backward compatibility" (`pstack/skills/principle-migrate-callers-then-delete-legacy-apis/SKILL.md:12`).
- Report every failure and warning you saw, including ones you didn't cause. `superpowers/skills/test-driven-development/SKILL.md:185-193`. A task's scope limits what you build, not what you check (same file, `:189-190`).

Review checklist, backward compatibility:
- Callers and stored data that rely on the old behavior: public APIs, CLIs, schemas, migrations, persisted data, env vars, feature flags, config defaults; rollback, mixed versions, gradual rollout; tests and fixtures that encode the old behavior. `Keystone/skills/change-review/SKILL.md:76-81`.
- Where a symbol search stops seeing breakage: API JSON, a DB column, a wire format, another language reading the same bytes, a flag, code three hops away. `pstack/skills/blast-radius/SKILL.md:35`.
- A new enum or status value, or a wider accepted input: trace it through every consumer (switches, filters, allowlists). `W/skills/ia-code-review/references/check-categories.md:10`.
- One value now meaning two things (`null` for both "no data" and "can't summarise"). `W/skills/ia-code-review/references/check-categories.md:15`.
- Lossy round-trip: decoding, changing and re-encoding data drops fields the code doesn't know. `W/skills/ia-code-review/references/check-categories.md:17`.
- Stricter parsing of input the tests can't produce (env vars, a secret store, a human): green tests don't cover it. `W/skills/ia-verification-before-completion/references/proof-integrity.md:13`.
- A field added to a queued job class: old messages get the default, so say what an old message meant. `W/skills/ia-code-review/references/reliability-patterns.md:53`.

Review checklist, hidden failures:
- The usual ways to hide a bug: guards, retries, swallowed errors, clamped values, discarded state. `pstack/skills/principle-fix-root-causes/SKILL.md:12`.
- Swallowed errors, partial error handling, catching too broad a type, re-throwing without the cause. `W/skills/ia-code-review/references/reliability-patterns.md:7-10`.
- A fallback for "the tool is missing" that also fires on "the tool ran and failed". `pstack/skills/poteto-mode/references/bugbot-triage.md:124-137`.
- "It throws, so the queue retries" only holds if the throw escapes the handler. `W/skills/ia-code-review/references/reliability-patterns.md:28`.
- "Does failure degrade safely without hidden partial success?" `Keystone/skills/change-review/SKILL.md:184`.
- Warnings in test output are findings. `superpowers/skills/subagent-driven-development/task-reviewer-prompt.md:84`.

Review checklist, logging, leaks, config and deploy:
- When finding the cause took a deep dig, propose the cheap always-on signal that would have shown it. `pstack/skills/principle-fix-root-causes/SKILL.md:14`.
- Leftover debug output and temporary diagnostics. `Keystone/skills/root-cause-analysis/SKILL.md:117`; `W/commands/ia-verify.md:97-105`.
- Every external call has an explicit timeout; stacked retry layers multiply attempts; a retry needs a proof it can't duplicate a write. `W/skills/ia-code-review/references/reliability-patterns.md:16`, `:27`, `:23`.
- Leaks on error paths, not only success paths; an early exit added halfway through setup. `W/skills/ia-code-review/references/reliability-patterns.md:42-45`.
- Servers and processes a run started and left running. `superpowers/docs/plans/2025-11-28-skills-improvements-from-user-feedback.md:48-63`.
- A flag that turns off the writer is not a revert when readers still act on the shared list. `W/skills/ia-code-review/references/reliability-patterns.md:58`. Read a flag's default before saying a path is "behind a flag". `W/skills/ia-code-review/references/review-judgment-traps.md:35`.
- The old release keeps writing during a rolling deploy. `W/skills/ia-code-review/references/reliability-patterns.md:60`.
- Sample env files cover the required variables, config names match the code, defaults are safe. `Keystone/skills/project-audit/SKILL.md:62`.

Review checklist, test quality (all four big repos agree on the core):
- Mock only at the system's edges (outside APIs, time, randomness); never your own modules. `skills/skills/engineering/tdd/mocking.md:3-14`.
- Mocking the thing under test is always a mistake. `W/skills/ia-writing-tests/references/test-smells.md:93`. A mock earns no assertions: `superpowers/skills/test-driven-development/writing-good-tests.md:83`.
- Nuance the repos agree on: call counts are fine to assert when the calls are the contract (a callback once per item). `superpowers/skills/test-driven-development/writing-good-tests.md:114-117`; `W/skills/ia-writing-tests/references/test-smells.md:98`.
- Mocks mirror the full real structure, not just the fields the test reads. `superpowers/skills/test-driven-development/writing-good-tests.md:119-122`; `W/skills/ia-writing-tests/references/test-smells.md:79-83`.
- Expected values come from an independent source (a known literal, a worked example, the spec), not computed the way the code computes them. `skills/skills/engineering/tdd/SKILL.md:31`; `superpowers/skills/test-driven-development/writing-good-tests.md:27-39`.
- The two-edit check: break the behavior and the test must fail; change only the internals and it must still pass. `pstack/skills/principle-test-behavior-not-implementation/SKILL.md:11-16`.
- No change detectors: a test that only fails when a deliberate value changes (a constant, exact wording) sleeps through real bugs. `superpowers/skills/test-driven-development/writing-good-tests.md:41-45`.
- The mutation check before finishing: a wrong constant, a flipped branch, a missing side effect, an empty return, a missing check for empty or unauthorised input should each fail some test. `superpowers/skills/test-driven-development/writing-good-tests.md:157-169`.
- A test that builds its object below the layer the fix changed never runs the fix. Feed it the raw input. `W/skills/ia-writing-tests/references/oracle-smells.md:25-29`.
- Where a set of fields is the contract, assert absence as well as presence. `W/skills/ia-writing-tests/references/oracle-smells.md:39-43`.
- Wait for the real condition, not a guessed sleep; don't assert elapsed wall-clock time. `superpowers/skills/systematic-debugging/condition-based-waiting.md:7`; `W/skills/ia-writing-tests/references/test-smells.md:25-35`.
- A golden file or snapshot regenerated to get green, with no review of what changed. `W/skills/ia-writing-tests/references/test-smells.md:67-71`.
- "Prefer no new test over a bad test", with a definition. `pstack/skills/tdd/SKILL.md:26`.
- A failing-first test must fail for the intended reason, not because the setup is broken. `pstack/skills/tdd/SKILL.md:18`; `Keystone/skills/_shared/gates/red.md:19-22` and `:39`.

### WF5. `[failure]` and `[concurrent]` case labels
What a good case of each kind looks like (checkpoints.md and the challenger's focus):
- `[failure]`: what happens if the previous run crashed at every possible point; leftovers must be recognisable as stale. `pstack/skills/principle-make-operations-idempotent/SKILL.md:15`. Also timeouts, partial failure, cancellation: `Keystone/skills/change-review/SKILL.md:73-74`.
- `[failure]` shapes worth naming: an external write after the database commit has nothing to roll back (`W/skills/ia-code-review/references/reliability-patterns.md:32`); an "already done" retry that skips the second half of a two-step operation (`:11`); a fan-out that publishes an empty result as success when the data-bearing parts failed (`:12`).
- A fallback test needs a partner test showing the primary call still fails the way the fallback expects, or it keeps passing after upstream changes. `W/skills/ia-writing-tests/references/oracle-smells.md:81-85`.
- `[concurrent]`: remove the sharing before adding a lock; "Instructions are not concurrency control". `pstack/skills/principle-separate-before-serializing-shared-state/SKILL.md:9-11`. The question to ask: "What stops two runs doing the same work?" `skills/skills/in-progress/software-factory/SKILL.md:94`.
- `[concurrent]` proof: a zero-latency mock settles parallel calls in one tick and hides the race; release N threads from one barrier so each run enters the window. `W/skills/ia-writing-tests/references/oracle-smells.md:31-37`. For hunting an existing one, raise how often it fails (loop, parallelise): `skills/skills/engineering/diagnosing-bugs/SKILL.md:51`.
- Proof for both: these bugs often don't exist yet, so a failing-first run can't show the test works. Show it failing on a broken copy (remove the guard, reorder the step), then restore. `pstack/skills/create-formal-verification-skill/SKILL.md:24`. Check the broken copy really changed, or the "failure" proves nothing: `W/skills/ia-writing-tests/references/regression-proof.md:19`.
- Each case should be one a plausible wrong implementation fails. `W/skills/ia-writing-tests/SKILL.md:18`.
- Challenger: list the inputs and failure modes the task implies but no case covers. `superpowers/skills/writing-plans/SKILL.md:77-87`. Also illegal state changes and time (clock skew, expiry, overlapping scheduled jobs), without new labels: `W/agents/ia-spec-flow-analyzer.md:72-77`.
- `[performance]` (existing label): an upper-bound assertion at or above the unfixed count still passes when the fix is reverted. `W/skills/ia-writing-tests/references/oracle-smells.md:63-67`.
- Already have: a case that doesn't apply keeps its line with a reason (pstack does the same, `pstack/skills/poteto-mode/playbooks/feature.md:7`).
- Keep the label list in sync: the labels live in `internal/core/docs.go:162`, `internal/core/rules.md:45` and `skills/task/checkpoints.md`. Keystone fails its build when docs say something stale (`Keystone/scripts/validate-keystone.py:162-176`). A Go test that the three lists match guards WF5.

### WF6. `## Verdict` and "couldn't run" in the report
- Three verdicts, like Plumbline's: Keystone's shipping skill ends with "Shipping / Do not ship / Shipping with risk" (`Keystone/skills/shipping/SKILL.md:69`), and "If readiness is uncertain, say 'Do not ship' or 'Shipping with risk,' not 'done.'" (`:107`).
- What a verdict may claim when proof is missing: "implemented, not verified" or "blocked pending proof", with the exact fallback check. `Keystone/skills/_shared/gates/proof.md:45-56`. "Inconclusive is not a pass": `pstack/skills/figure-it-out/SKILL.md:42`.
- Couldn't run is not skipped. A check that failed to run (tool missing, crashed, unreadable output) is an error, never "skipped" or "clean", and blocks Ready; "skipped" means deliberately not run, with a reason. `W/commands/ia-verify.md:44`; `W/skills/ia-verification-before-completion/references/verification-oracles.md:29`.
- Things the tree can't show (a DNS record, a cloud setting, a secret-manager entry, a sibling repo that isn't on disk) are "unverifiable": name the system and the exact check the user runs. Each deliverable is done, partial, not done, changed (same goal, different means) or unverifiable. `W/skills/ia-verification-before-completion/references/claims-and-failures.md:37-48`.
- A bug fix whose real trigger no test can reach: report the limit and don't claim the bug fixed; cost or difficulty alone doesn't count as "no test can reach it". `W/skills/ia-writing-tests/references/test-completion.md:16-20`.
- Outside-the-laptop commands near the end: stop when one fails, and report each with its result, what's left and how to recover. `Keystone/skills/shipping/SKILL.md:94` and `:138-140`.
- Suggested finish-check rules: `Ready` is refused while any Proof line says couldn't run, a waiver exists, or a finding was upheld; those need `Ready with noted limitations`, each with the exact command to run later. `Not ready` when a run caused by the change fails or a must-fix is open.
- Proof shows the intended change, not just success: a 200 passed while the wrong provider answered. `superpowers/docs/plans/2025-11-28-skills-improvements-from-user-feedback.md:186-230`. Plumbline's own example teaches the weak form: `skills/task/SKILL.md:54` checks only the status code.
- Reversibility: say whether the change can be walked back or not (destructive migrations, removed public API, anything sent outward), and distrust your own "two-way, small". `skills/skills/engineering/pr/SKILL.md:168`; `skills/docs/engineering/pr.md:31`. Keystone's ship gate wants a rollback line: `Keystone/skills/_shared/gates/ship.md:25-27`.
- Two gaps found in Plumbline while checking:
  - `rules.md:12` says common-sense calls "are listed in the report", but checkpoints.md's report format has no section for them. superpowers collects every judgment call with what it costs if wrong (`superpowers/skills/subagent-driven-development/references/final-review.md:35-42`).
  - should-fix findings have no destination. `skills/task/SKILL.md` step 9 says "Fix the must-fix ones that hold up" and nothing about the rest, so an unfixed should-fix can vanish. superpowers: "A roll-up nobody reads is a silent discard" (`superpowers/skills/subagent-driven-development/references/fix-loop.md:9-13`). The report could list each should-fix that wasn't fixed.

### How to word release 2
- Fit the form to the failure: a plain "don't" works for one discrete action; a "don't" that shapes output did worse than no guidance; shaping output needs a positive recipe; a missing element needs a required slot in the template. `superpowers/skills/writing-skills/SKILL.md:248-263`.
- No "unless it matters" clauses; write a real exception as its own condition. `superpowers/skills/writing-skills/SKILL.md:262`.
- Say the wanted behavior; a "never X" puts X in attention. `skills/skills/productivity/writing-for-agents/SKILL.md:74`.
- Cut lines the model already obeys by default, since rules.md is loaded every session. `skills/skills/productivity/writing-for-agents/SKILL.md:81`. rules.md is 11.9 KB today; release 2 should put the long lists in review-checklist.md (helpers only) and keep rules.md about the same size.
- Where a rule can be a check, make it a check: "If the fix is structural, only use the structural fix. The instruction is the symptom." `pstack/skills/principle-encode-lessons-in-structure/SKILL.md:11`. For release 2 that means the `## Verdict`, `## Reuse` and label rules get hook checks, not just prose.
- Skill descriptions say when to use the skill, not how it proceeds. `superpowers/skills/writing-skills/references/discovery.md:71`; `whetstone/AGENTS.md:146`. Plumbline's `skills/task/SKILL.md:3` and `skills/design/SKILL.md:3` both summarise the workflow.

## Where the repos disagree
These need a decision, not just a borrowed line.
1. Validate at every layer, or only at boundaries?
   - superpowers: "Validate at EVERY layer data passes through. Make the bug structurally impossible." `superpowers/skills/systematic-debugging/defense-in-depth.md:7`.
   - pstack: validate where data crosses a system boundary; "Inside, trust the types: no re-validation and no redundant nil checks." `pstack/skills/principle-boundary-discipline/SKILL.md:9-11`. whetstone calls inside checks "defensive noise" (`W/skills/ia-code-review/references/check-categories.md:56`) but never removes trust-boundary validation, data-loss handling or security checks (`W/agents/ia-code-simplicity-reviewer.md:47`).
   - Suggested rule for WF1: validate where data enters (requests, CLI args, config, env vars, files, external APIs, database rows); inside, don't re-check what the types already guarantee. A guard that stops a dangerous action in the wrong context (a test refusing to touch a real folder, superpowers' "Layer 3", `defense-in-depth.md:52-53`) is fine.
2. Blind QA writes all the tests before any code. Two repos call that an anti-pattern:
   - skills: "Horizontal slicing: writing all tests first, then all implementation. Bulk tests verify imagined behavior" (`skills/skills/engineering/tdd/SKILL.md:32`).
   - whetstone: tests first for bug fixes; for new features, tests after each small slice by default, and "writing all tests first and all implementation afterward" is called out (`W/skills/ia-writing-tests/SKILL.md:19`).
   - For Plumbline's design: QA's tests come from cases the user approved, and blindness is the point (they test what should happen, not what was built). superpowers argues the same way for tests first (`superpowers/skills/test-driven-development/SKILL.md:228`).
   - Against: tests written before the shape exists can pin the wrong seam and break on a sound implementation.
   - Options: keep it as is; or let the lead fix a QA test's setup (not its expected values) when the seam moved, with the change listed under weakened tests. This is not release 2 work; it only matters for big tasks.
3. Tests first for features: superpowers says always (`superpowers/skills/test-driven-development/SKILL.md:34`); Keystone wants a failing signal first or a recorded reason plus a stronger proof plan (`Keystone/skills/_shared/gates/red.md:51-54`); whetstone says the order matters less than the quality for features (`W/skills/ia-writing-tests/references/regression-proof.md:21-23`). Plumbline already says a failing test first; nothing to change, but WF4 should stay limited to `[bug]` cases, where all of them agree.

## Later releases: ideas that fit open limits
- WF4 (release 4), failing run before the fix: Plumbline records failing test names (`internal/core/runs.go:192`), so the check can be "each `[bug]` case's named test appears failing in a recorded run". What counts: it fails rather than errors, for the feature missing, not a typo (`superpowers/skills/test-driven-development/SKILL.md:121-128`); it drives the real code path and checks the user's exact symptom (`skills/skills/engineering/diagnosing-bugs/SKILL.md:57-66`).
- P3 (3b), weakened tests: whetstone's tokens to scan added lines for: `.skip`, deleted assertions, `eslint-disable`, `# noqa`, `@ts-ignore`, `continue-on-error: true`, `allow_failure: true`, `--no-verify`, `[skip ci]`, a loosened threshold read by direction, a golden file changed without its test, product code branching on `CI` or `NODE_ENV`. `W/skills/ia-code-review/references/check-categories.md:48-49`. A rule loosened in a standards file is listed with what it would hide: `W/skills/ia-code-review/SKILL.md:24`.
- R1 (3c), specialists by content: Keystone's list of what needs an independent review (`Keystone/skills/_shared/gates/review.md:15`); whetstone's risk signals (more than 300 changed non-test lines, 8 files or 3 top folders, a security path, a migration, a public API) (`W/skills/ia-code-review/SKILL.md:22`).
- R3 (3c), findings pasted into a reviewer's prompt: refuse a reviewer start whose prompt shares a 24-character run with an existing findings file; whetstone uses that length to tell quoted evidence from made-up text (`whetstone/distillery/skillopt/skillopt/envs/whetstone/rubric.py:131-134`). Rule: "Never forward the first reviewer's ... claims" (`W/skills/ia-code-review/references/deep-review.md:158`). superpowers refuses steering phrases like "do not flag" (`superpowers/skills/subagent-driven-development/SKILL.md:285-290`).
- R2 (release 5, after V11), `UserAsksJira`: a fixture file of prompts that should and shouldn't match, at least 5 each, released only at a perfect score; tighten a loose pattern rather than add exceptions. `whetstone/distillery/scripts/distiller.py:1841` and `:2549`; `W/hooks/skill-patterns.sh:20-26`. Keystone's near-pairs: `Keystone/tests/routing/cases.yaml:2-60`.
- B2, B3, B5 and builder briefs (3c): each builder's part names what it uses from other parts and what it hands them (superpowers' "Interfaces: Consumes / Produces", `superpowers/skills/writing-plans/SKILL.md:102-106`; Keystone: "define contracts/interfaces before parallel work begins", `Keystone/skills/implementation/SKILL.md:133`); the plan says what runs first, which parts are independent and which files they share (`pstack/skills/poteto-mode/playbooks/feature.md:7-11`). On `model: sonnet` for simple parts: cheaper models often take 2 to 3 times the turns, so the cheapest tier fits only when the plan gives the exact code (`superpowers/skills/subagent-driven-development/SKILL.md:164-170`).
- M14 (3d), two sessions in one repo: ownership is known only from the user saying so, a file this task created, or a recorded handoff. `Keystone/skills/_shared/gates/isolation.md:29-37`.
- Package upgrades in "Needs your OK": run the suite before the first change and save it; rerun the same command set after; name every test the upgrade edited; for each changelog entry that changes behavior silently, point to a test that covers it. `W/skills/ia-verification-before-completion/references/proof-integrity.md:25-31`.
- Playbooks (small, any release):
  - Bug: write a few guesses, each with what it predicts, before testing any (`Keystone/skills/root-cause-analysis/SKILL.md:66` and `:82-88`); find similar working code and list every difference (`superpowers/skills/systematic-debugging/SKILL.md:124-136`); after two failed fixes that share an assumption, write it down and test it (`pstack/skills/principle-attack-the-premise/SKILL.md:15`); after three, question the approach, not the fix (`superpowers/skills/systematic-debugging/SKILL.md:198-212`); a guard that "might help" doesn't ship (`pstack/skills/poteto-mode/playbooks/bug-fix.md:5`).
  - Flaky: Plumbline says "Re-run once to confirm" (`skills/task/playbooks.md:16`). Keystone runs 20 to 100 times by risk (`Keystone/skills/root-cause-analysis/SKILL.md:76`); whetstone: never leave a test "passing-by-retry" (`W/skills/ia-writing-tests/references/test-smells.md:43-47`).
  - Refactor: name the behavior that must not change (`Keystone/skills/refactoring/SKILL.md:24`); "Type check and lint are not a pin"; renames miss strings, docs and back-references (`pstack/skills/poteto-mode/playbooks/refactoring.md:7` and `:11`); if the diff doesn't make the code easier to follow, revert it (same file, `:13`).
  - Changes that ripple across many callers: add the new form beside the old, move callers in batches, delete the old last. `skills/skills/engineering/to-tickets/SKILL.md:40`.
- For Plumbline's own gate code: when the same parser or allowlist needs a second exception, fix the representation instead of adding another (`W/skills/ia-code-review/references/review-judgment-traps.md:41`). Several G-items went the exception route.

## New ideas, not on the limits list yet
Decided 2026-11-18: all of them are scheduled in roadmap.md as N1 to N15 (same numbers).
1. Reviewers don't see the 80 cutoff. A judge told the pass mark drifts toward it, and a finder told to self-censor drops real findings (`W/skills/ia-orchestrating-swarms/references/anti-sycophancy.md:23`; `W/skills/ia-orchestrating-swarms/references/review-and-delivery.md:7`). whetstone also calls numeric scores "uncalibrated reviewer judgment" and keeps unproven candidates under Residual Risks with the missing check named (`W/skills/ia-code-review/references/severity-and-confidence.md:43-46`). Plumbline does the opposite: `skills/task/review-checklist.md:26` says "Only findings at 80 or more go in the list". Option: reviewers list every finding with its score; the report shows 80 and up, and the rest one line each.
2. Later rounds start fresh. Reviewers who read their earlier verdict first anchor on it and miss what the fix broke (`W/skills/ia-orchestrating-swarms/references/anti-sycophancy.md:13`); pstack also gives each fix round a fresh helper by default (`pstack/skills/poteto-mode/SKILL.md:100`). Every Plumbline helper reads its earlier findings first (`agents/challenger.md:13`, `agents/code-reviewer.md:13` and the other reviewers). Option: a fresh pass first, then the earlier findings.
3. Check the quote, not only the line. `CheckRefs` (`internal/core/docs.go:71-101`) checks the file was read and unchanged, not what the line says. whetstone: "A citation that was not measured is fabricated" (`W/skills/ia-code-review/references/severity-and-confidence.md:12`), and its grader zeroes evidence unless 24 characters of it appear in the source (`rubric.py:131-171`). Option: findings carry a short backticked quote that Go checks near the cited line.
4. Changed-file coverage decides the verdict: the code reviewer marks each changed file covered or not; any file not covered means not Ready. `W/skills/ia-code-review/references/deep-review.md:47-54`.
5. Disputes need evidence both ways. Withdrawing a finding about concurrency, compatibility, auth, injection, secrets or data loss needs a refuting file:line, not "the suite passes" (`W/skills/ia-code-review/references/severity-and-confidence.md:65`). The lead's dispute must cite the guard, and the reviewer opens it before withdrawing: "Inventing a phantom guard is worse than letting a false positive through" (`W/skills/ia-code-review/references/deep-review.md:204`).
6. Reviewers list what they set aside as outside the plan, one line each, and the lead rules on each. `superpowers/skills/requesting-code-review/code-reviewer.md:43-48`; `W/skills/ia-code-review/references/report-and-integration.md:32`.
7. A case's test must have actually run and passed, not just exist. Plumbline checks existence only (`internal/core/stop.go:29-33`), and its run parser doesn't count skipped tests. "A skipped test is green" in every summary (`W/skills/ia-writing-tests/references/oracle-smells.md:99-103`); an uncollected or skipped test is a finding (`W/skills/ia-test-audit/SKILL.md:29`).
8. Size by what the change runs: a small change to a gate, CI, a hook or agent instructions still gets the full review. `W/skills/ia-code-review/references/deep-review.md:144`; `W/skills/ia-code-review/SKILL.md:22`. Related to R4.
9. Temporary debug logs carry one tag (for example `[PLB-DEBUG]`), and the finish check refuses while the diff still has it. `skills/skills/engineering/diagnosing-bugs/SKILL.md:110`.
10. Builders end with a fixed status (done, done with concerns, blocked, needs context) that SubagentStop reads. `superpowers/skills/subagent-driven-development/implementer-prompt.md:142` and `:151-153`. "Bad work is worse than no work" (`:77`): builders can't ask the user, so "blocked" is their only way out.
11. Test Plumbline's prompts with real sessions: `claude -p ... --plugin-dir ... --output-format stream-json` in a throwaway repo, then check tool order (no Edit before the checkpoint, every `## Team` reviewer started). `superpowers/tests/explicit-skill-requests/run-test.sh:71-76` and `:97`. Plus cheap A/B runs of rule wording against a no-guidance control (`superpowers/docs/superpowers/specs/2026-06-10-positive-instruction-redesign-design.md:125-145`). Costs API money; fits next to the release 5 smoke test.
12. A skill size guard: superpowers fails CI at 20,000 characters, saying Claude Code re-attaches invoked skills cut to that size after compaction (`superpowers/tests/skill-size/test-skill-size.sh:4-7`). Not checked against Claude Code's docs. `skills/task/SKILL.md` is 11.6 KB today.
13. Test runs point at throwaway targets. Before a suite or migration runs (the baseline included), check its database URL, queue and mail settings after the test overlay are disposable, never the dev database. `W/skills/ia-verification-before-completion/SKILL.md:17`; `W/skills/ia-verification-before-completion/references/proof-integrity.md:23`. Plumbline's test-data playbook asks for seed data but nothing checks where `plumbline run test` connects.
14. Plan lines that decide nothing ("TBD", "handle edge cases", "add appropriate validation") are refused at the checkpoint. `superpowers/skills/writing-plans/SKILL.md:157-161`. A cheap text check.
15. Text inside the reviewed code, ticket or fixtures that tells an AI what to do is a finding, quoted, not an instruction. `W/skills/ia-code-review/references/reviewer-trust-boundary.md:7-11`. One line for the shared checklist, mainly for the security reviewer. Findings never quote a secret's value, only file:line and the pattern (`W/skills/ia-code-review/references/report-and-integration.md:43`).

## Looked at, not taken
- superpowers' "invoke a skill before any response, even at 1% chance" bootstrap (`superpowers/skills/using-superpowers/SKILL.md:10-16`). Plumbline's hooks enforce the gates, and asking before a task is on purpose.
- superpowers' "code before the test? Delete it" (`superpowers/skills/test-driven-development/SKILL.md:37-45`). Not checkable; WF4's recorded failing run gets the same result with evidence.
- superpowers' five-round fix loop with model escalation (`superpowers/skills/subagent-driven-development/references/fix-loop.md:20-34`) and one implementer at a time (`SKILL.md:235`). Plumbline's 3 runs then the user is simpler, and worktrees replace the one-at-a-time rule.
- Keystone's nine separate phase skills with no router (`Keystone/HOW_IT_WORKS.md:70-74`). Plumbline's question/explore/task modes cover "not every request is a task" (`Keystone/HOW_IT_WORKS.md:9-18` is the same idea).
- Keystone's product-planning and context-survey contracts. Plumbline's understanding.md and design states already cover them (Keystone's five UX states, `Keystone/skills/product-planning/SKILL.md:51`, match the design task's "loading, empty, error").
- whetstone's deep-review machinery (CR-IDs, finding caps, model routing; `W/skills/ia-code-review/references/deep-review.md:150-240`) and pstack's multi-vendor review panels (`pstack/skills/poteto-mode/SKILL.md:92`). Too heavy for five reviewers, and Claude Code can't switch vendors. One line kept: two findings with one root cause are one finding if fixing one fixes the other (`deep-review.md:197`).
- pstack's statechart tooling (TLA+, Java). Only the "show it failing on a broken copy" idea carries over.
- pstack's autonomy stance (never ask the user, allow breakage on the way; `pstack/skills/poteto-mode/worker.md:7`; `pstack/skills/principle-outcome-oriented-execution/SKILL.md:15`). Conflicts with the checkpoint and WF2.
- whetstone's skill injection into helpers through a queue (`W/hooks/subagent-queue.sh:95`). Plumbline briefs its helpers itself at SubagentStart.
- ralph-loop: a Stop hook that feeds the same prompt back until the model prints a promise tag (`ralph-loop/hooks/stop-hook.sh:44-55`, `ralph-loop/hooks/capture-response.sh:39-44`). "Done" is the model's own claim; Plumbline's finish check uses recorded proof and caps send-backs (`internal/core/stop.go:225`). It does stop cleanly on a corrupted state file (`ralph-loop/hooks/stop-hook.sh:31-41`), which Plumbline should keep doing.
- Matt Pocock's plain-chat questions instead of AskUserQuestion (`skills/.out-of-scope/native-question-tool.md:9`). Plumbline's checkpoint depends on the question tool.
- Matt Pocock's git guardrails hook; its own notes say `git -C` bypasses it and it fails open without jq (`skills/.out-of-scope/frozen-misc-skills.md:16-18`).

## Already in Plumbline (confirms the design)
- Helper findings are leads to check, not facts (`Keystone/skills/_shared/handoff-packet.md:24`; `superpowers/skills/subagent-driven-development/task-reviewer-prompt.md:66-71`).
- Review areas kept apart, never merged (`skills/skills/engineering/code-review/SKILL.md:78-80`).
- Stop the fix loop after a few rounds (`skills/docs/engineering/code-review.md:77`); three failed attempts means stop (`Keystone/skills/root-cause-analysis/SKILL.md:99`).
- State kept outside the conversation, so compaction doesn't lose the place: superpowers keeps a ledger file because lost controllers "re-dispatched entire completed task sequences" (`superpowers/skills/subagent-driven-development/SKILL.md:108-111`). Plumbline's task state does this.
- Helpers get files, not session history (`superpowers/skills/subagent-driven-development/SKILL.md:183-185` and `:220-224`).
- Builders' work is checked from the diff, not the summary (`Keystone/skills/implementation/SKILL.md:223`).
- A suite that ran nothing is not a pass (`W/skills/ia-verification-before-completion/references/verification-oracles.md:15`), and proof goes stale after a change.
