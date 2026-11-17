# Gap review of the committed 1a code (2026-11-17)

**Status (0.3.1):** H1 to H8, M1 to M8 and M10 to M12 are fixed, each with a test in `internal/core/hardening_test.go` that fails on the 0.3.0 code. **0.4.1:** M13 fixed (sealed run records, proof tied to the task and the saved command; `TestRunsSealed`, `TestRunsBelongToTheTaskAndCommand` in `internal/core/hardening2a_test.go`). **0.5.0:** M9 fixed (worktrees keyed by their main checkout; `TestWorktreeSharesTheProject` in `internal/core/workers_test.go`). Still open: M14 and L1 to L6.

A read-only, fresh-eyes review of the 1a code as committed on `main` at 56d9d08, done while 1b was built. Each finding was confirmed against the code, most with a throwaway test. H1 and H7 were also reproduced on the 1b branch. Already fixed on the 1b branch: the lost updates part of H5 (the per-task lock), the `plumbline docs-dir`/`status` refusal, and the fingerprint missing same-second edits.

The common cause of H1 to H4: the Bash gate is a blocklist over a partial shell parser. Anything it doesn't understand is treated as harmless. The fix that closes the class is to fail closed: a command counts as read-only only if every part of it was understood and is on the allowlist. Deny rules also need to look inside wrappers.

## High
- **H1** Heredoc: the rebuilt command drops the rest of the tag line (shell.go:111-123). `cat <<EOF > src/a.go`, a heredoc then `; git push`, writes to `~/.claude/settings.json` or the `paused` flag: all allowed, even before approval. Fix: strip only the body lines; parse the rest of the tag line.
- **H2** The "read-only" list has commands that write files or run code: `sed -Ei`/`-ni`/`w file`, awk `print >`/`| "sh"`, `sort -o`, `uniq in out`, `xxd -r in out`, `tree -o`, `find -fprint0`, `yq -i`, `fd -x`, `rg --pre`, `git diff --output=f`, `git -c diff.external=`/`core.fsmonitor=`, `go env -w`. The parser also misses `>&file` and `1<>file`, and strips env prefixes (`GIT_EXTERNAL_DIFF=`, `BASH_ENV=`, `PATH=`). Fix: env prefix means not read-only, record those redirects, deny those flags, `git -c` is not read-only.
- **H3** Wrappers and shell syntax hide every deny rule while building: `bash -c`, `sh -c`, `eval`, `/usr/bin/env`, `timeout`, `nice`, `xargs`, `{ ...; }`, `if/for/while`, `!`, `$VAR` as the command, `git -c alias.p=push p`, and commands after `plumbline run proof ... --` (only checked before approval). `sh -c 'git commit ...'` skips the commit rule. Fix: recurse into `-c`, `eval` and wrapper arguments; treat compound keywords as prefixes; deny unknown `$VAR` command names and `git -c alias.*`; run every check on the command after `proof --`.
- **H4** No-verify: `git commit -anm` (bundle with m) and `--no-verif` (unique prefix) skip hooks unflagged; so do `-c core.hooksPath=/dev/null` and `HUSKY=0`. False block: `git commit -uno -m`. Fix: parse short-flag bundles; any `--no-v` prefix; deny `core.hooksPath` and `HUSKY=0`.
- **H5** `state.json` and `state.seal` are two renames through one shared `.tmp` name: parallel hooks (fixed by the lock) or a hook killed by its timeout between the two writes leave the task "tampered", which kills it. Fix: one file holding data and seal, unique temp names.
- **H6** Building-phase Bash ignores the user's-work and risky-file refusals and the 3-file cap: `rm src/b.go` (user's unsaved work), `sed -i ... package.json` run without objection; afterBash only notes them, doesn't add them to `Held`, and only messages past the cap. Fix: refuse write commands naming those files; add them to `Held`; stop past the cap.
- **H7** Two checkpoint questions in one prompt: the pre gate checks the last, the post hook approves the first and never re-runs `CheckPlan` (pretool.go:696-719, posttool.go:356-394). A plan of "whatever" was approved with no files, cases or proof. Fix: exactly one checkpoint question per prompt; re-run the checkpoint checks before recording approval.
- **H8** MCP tools aren't gated (PreToolUse matcher has no `mcp__.*`): Serena's symbol-editing tools or a filesystem MCP can change code before approval. Fix: add `mcp__.*` and deny known write tools before building.

## Medium
- **M1** `echo plumbline-adopt-id: <id>` binds the session to another task with no confirm prompt (posttool.go:138-153). Bind only on a real `plumbline task start|adopt`.
- **M2** `Task.Branch` never updates: after `git checkout -b` (which git.md requires), `docs-dir`, `status`, `finish`, `replan`, `waive` say "no open task", and runs record with no task id (no baseline).
- **M3** Settings and data-folder protection is substring matching, skipped for read-only segments, and case-sensitive (macOS disks aren't): `cd ~/.claude && cp ...`, `touch ~/.PLUMBLINE/.../paused`, an Edit to `SRC/B.go` past the user's-work refusal. The `paused` flag has no seal.
- **M4** Secret reads and sends without the prompt: `cat .env*`, `head .e?v`, `curl -d@.env`, `wget --post-file=.env`.
- **M5** `git commit <pathspec> -m` skips the secret scan.
- **M6** `--trailer "Co-authored-by: Claude ..."` and `-m "$(cat file)"` skip the AI-marker check.
- **M7** `git revert`, `commit-tree` + `update-ref` skip the commit rule; `gh` and `hub` aren't in the outside-the-laptop list (`gh pr create` pushes).
- **M8** A session started outside the repo (a parent folder) turns the commit rule off: `git -C <repo> commit` isn't checked. Work out the root from `-C` or the file path.
- **M9** Worktrees count as new projects (setup unconfirmed, tasks missing). Key projects by `git rev-parse --git-common-dir`.
- **M10** `plumbline setup save` writes `.plumbline.json` and the next post hook stops the work ("a terminal command changed files before any plan was approved").
- **M11** `PlanHash` is never checked; the finish check re-reads plan.md, so cases can be deleted after approval. Store the case tests at approval.
- **M12** Proof kinds come only from bullets starting exactly `ui:`/`api:`/`schema:` (`- **api**: ...` slips through); any exit-0 command counts as proof (`plumbline run proof api -- true`).
- **M13** `runs.jsonl` has no seal (forgeable through H1/H3), and `LatestFor` ignores the task and the command.
- **M14** Two sessions in one repo: one session's edits stop the other (snapshot is per session, dirty files per repo). Not run, from reading the code.

## Low
- **L1** False blocks: `npx tsc --noEmit`, `pip install -r requirements.txt` refused as installs during a task.
- **L2** Only the Read tool counts as "opened"; `cat`/`sed -n`/`rg` reads give a false "didn't open this file".
- **L3** Logs hold commit subjects and absolute paths in Detail (README says no ticket text).
- **L4** Without failing-test ids (vitest, mocha, rspec, cargo, phpunit), the baseline compares counts only, so a new failure can hide behind a fixed old one. A broken-environment baseline sticks.
- **L5** `test:` matches inside words like "latest:".
- **L6** Unconfirmed: if the real AskUserQuestion response is a plain string, the `"Q"="A"` fallback could be spoofed by a question text containing `"X"="Approve"`.
