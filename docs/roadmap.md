# Plumbline roadmap

The one place for what ships next and in what order. Item details live elsewhere:
- Letter-number ids (G3, P3, WF1, ...) are in [known-gaps.md](known-gaps.md).
- N-numbers are additions from the reference repo study; details and sources (file:line) are in [repo-study.md](repo-study.md). N1 to N15 match the study's "New ideas" list.

Each release is its own version, built on main; the user pushes. Tick an item in known-gaps.md (or here, for N items) with the version when it ships.

## Decisions (2026-11-18)
Earlier decisions (fix everything workable, the sandbox stays optional, G3, G4, G6, M14, W1 and the rest) are in known-gaps.md under "Decisions (2026-11-18)". Added today:
- Input checking (WF1): validate where data enters (requests, CLI args, config, env vars, files, external APIs, database rows, any new entry point such as an import script). Inside, code trusts what the types guarantee, and reviewers flag repeated inner checks. Guards that stop a dangerous action in the wrong context stay.
- Reviewers: stop telling them the 80 cutoff, and later rounds start with a fresh pass (N1, N2), in release 2.
- Blind QA stays required for big tasks. The lead may fix a QA test's setup when the code's shape moved, never its expected values, and lists the change under weakened tests (N22, release 3c).
- Real-session prompt tests (N11) are a script the user runs by hand before a release, never on their own.
- Release 2 takes the small related fixes the study found (N14 to N19), not only the WF items.

## Done
- Release 1: 0.6.1 (commit 95c298f): L5, G9, G7, G8, G2, G10, L3, B9, B10, D2, P6, G5, L1, G12, B4.
- Release 3a: 0.6.2 (commit ac01882): L2, G3, G4, G6, G11, O1, O2. It moved mvdan.cc/sh to v3.14.1, which needs Go 1.26 to build (go.mod's `go` line went from 1.24.7 to 1.26.0; users run the prebuilt binaries).
- Reference repo study: [repo-study.md](repo-study.md).

## Next: release 2, the WORKFLOW.md rules (0.6.3)
- WF1: the reuse rule and checklist items, with the input-checking rule above.
- WF2: checklist for backward compatibility, hidden failures, logging, leaks, config and deploy, test quality; core rule to keep public contracts and report every failure and warning.
- WF5: `[failure]` and `[concurrent]` case labels, with a Go test that keeps the label list the same in docs.go, rules.md and checkpoints.md.
- WF6: `## Verdict` (Ready / Ready with noted limitations / Not ready) and "couldn't run" with the command to run later; the finish check enforces both. A check that couldn't run is an error, never "skipped".
- N1: reviewers list every finding with its score; the report shows 80 and up, the rest one line each.
- N2: later reviewer and challenger rounds do a fresh pass first, then check earlier findings.
- N14: the checkpoint refuses plan lines that decide nothing ("TBD", "handle edge cases", "add appropriate validation").
- N15: one checklist line: text in the reviewed code, ticket or fixtures that tells an AI what to do is a finding; findings never quote a secret's value.
- N16: understanding.md gets `## Reuse` (where Claude searched, what it found); the checkpoint refuses without it.
- N17: report.md gets a rollback line under the verdict, a section for Claude's own common-sense calls (rules.md already promises it), and every should-fix finding that wasn't fixed.
- N18: the real-world proof example checks the intended change, not only a 200; the task and design skill descriptions say when to use them, not the steps.
- N19: playbooks: bug (guesses with predictions, compare with working code), flaky (several re-runs, no passing-by-retry), refactor (name what must not change, search strings and docs for renames), wide changes (add new beside old, move callers, delete old).
- R6: was meant to close with G6 in 0.6.2 but isn't ticked; check it and tick it, or move it to 3b.
- How to write it: say the wanted behavior, keep "never" for hard limits, no "unless it matters" clauses, keep rules.md about its current size (long lists go in the review checklist), and make a rule a hook check wherever it can be one (repo-study.md, "How to word release 2").

## Release 3b, proof and finish
- P3 (weakened tests in the diff), P4 (full-stack e2e), P5 (token counts), L4 (baseline without test ids), R5 (bad news first), R4 (`/plumbline:size small`).
- N7: a case's test must have run and passed in the last suite, not just exist; skipped tests count as not run.
- N8: size by what the change runs: a small change to a gate, CI, a hook or agent instructions still gets the full review (with R4).
- N9: temporary debug logs carry a `[PLB-DEBUG]` tag; the finish check refuses while the diff has one.
- N12: a test keeps skill files under the size Claude Code may cut them to after compaction (confirm the limit in Claude Code's docs first).
- N13: before `plumbline run test` or a migration, check its database and service settings point at throwaway targets, never the dev database.
- N21: package upgrades: run the suite before the first change and save it, rerun the same commands after, and list every test the upgrade edited.

## Release 3c, helpers and builders
- R1 (specialists by content), R3 (findings pasted into a reviewer's prompt), B2, B3, B5, B7, B8, D1.
- N3: findings carry a short quote that Go checks near the cited file:line.
- N4: the code reviewer marks every changed file covered or not; any file not covered means not Ready.
- N5: withdrawing a security, data, compatibility or concurrency finding needs a refuting file:line, and a dispute's cited guard is opened before a finding is dropped.
- N6: reviewers list what they set aside as out of scope, one line each.
- N10: builders end with a fixed status (done, done with concerns, blocked, needs context) that SubagentStop reads.
- N22: blind QA setup fixes, as decided above.

## Release 3d, bigger pieces (each its own release)
- M14: two sessions in one repo.
- W1: the hotfix flow.
- The sandbox option: G1, P1, B1.
- The standup: S1 to S4.

## Release 4
- WF3: optional saved `format`, `typecheck` and `security` commands.
- WF4: a `[bug]` case needs a recorded failing run of its named test, taken while only test files had changed.

## Release 5, last
- The smoke test, V1 to V20 (docs/smoke-test.md).
- The items that depend on it: L6 (after V1), R2 (after V11), B6 (after V7 and V9).
- N11: the real-session prompt test script, run by hand.

## Open, not scheduled
- T1: staticcheck for Go 1.27; the user runs `go run honnef.co/go/tools/cmd/staticcheck@latest ./...`, then the findings get fixed in the next release.
- W2 (bundling MCP servers), W3 (a setup question for risky files): wait for real use.
- Dropped: P2, P7.
