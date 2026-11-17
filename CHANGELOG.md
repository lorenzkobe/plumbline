# Changelog

## 0.4.0: phase 2a, several reviewers, Jira reading and drafts
- The one reviewer is replaced by independent reviewers: `plumbline:code-reviewer` (always; replaces `plumbline:reviewer`), `plumbline:plan-reviewer` (normal and big tasks), and specialists called for by the changed files' paths: `security-reviewer` (auth, permissions, payments, user data, secrets), `data-reviewer` (migrations, queries, models, money math), `ux-reviewer` (frontend). The plan's `## Team` must name the specialists its files call for, and can add more; a file changed outside the plan adds its specialist at the finish check.
- Every required reviewer must have reviewed the code as it is now (after a fix, all of them check again), at most 3 runs each. Findings files are `code-review-N.md`, `plan-review-N.md`, ...; a 0.3.x `reviewer` run counts as the code reviewer's.
- Reviews stay independent: in the notes folder a reviewer reads only understanding.md, plan.md and its own earlier findings (Read, Grep, Glob and terminal commands are checked; PreToolUse now also matches Grep and Glob).
- The challenger and the reviewers share one checklist, `skills/task/review-checklist.md`, instead of a copy in each agent file.
- Jira: company tasks on a ticket need a Jira read before the checkpoint (`plumbline task tool-down jira` when it can't be reached) and, at the finish check, `TICKET-123-jira-comment.md` in the notes folder: only the comment, no AI markers, no em dashes.
- Jira is draft-only: any Atlassian or Jira tool that doesn't only read (comments, transitions, edits, worklogs, Confluence pages) is refused in every state, paused included, unless the user's own latest message asks for a post; then it asks for the user's OK on the exact text, after the AI-marker and em-dash check. Helpers never post.
- Replaced: every claude.ai connector counted as look-up only. Now only Claude Docs does; Slack, Gmail, Atlassian and the others are judged by tool name, and the write verbs now include send, post, reply, comment, transition, assign, invite, share, publish, archive, close, reopen, approve, submit and schedule.
- `plumbline status` lists the reviewers the task needs. `"parts"` takes each reviewer's name and `"jira"`.

## 0.3.1: 1a hardening (gap review H1 to H8, M1 to M8, M10 to M12)
- The Bash gate parses commands with a real bash parser (mvdan.cc/sh) and fails closed: a command counts as read-only only if every part of it was understood and is on the allowlist. A command that can't be parsed, or whose command name, eval text or `sh -c` string isn't known until it runs, is refused. Replaces the hand-written splitter and the heredoc stripping, which hid the rest of a heredoc's line.
- Every rule now sees inside wrappers and shell syntax: `sh -c`, `eval`, `env`, `timeout`, `nice`, `sudo`, `xargs`, `find -exec`, `fd -x`, `trap`, `{ ...; }`, if/for/while/case, functions, subshells, `$(...)`, `<(...)`, heredocs fed to a shell, git aliases, and the command after `plumbline run proof --`.
- Read-only list: removed every way it wrote or ran code (`sed -i`/`w`/`e` and bundles like `-ni`, awk `print >`/`system()`, `sort -o`, `uniq in out`, `xxd in out`, `tree -o`, `find -fprint0`, `yq -i`, `rg --pre`, `git diff --output`, `git -c`, `go env -w`); `>&file` and `1<>file` are writes; env prefixes other than locale and colour settings make a command not read-only.
- No-verify: short-flag bundles (`-anm`), unique prefixes (`--no-verif`), `core.hooksPath`, `HUSKY=0`, `SKIP=`, `GIT_CONFIG_*` and writes to `.git/` are blocked; `-uno` is no longer read as `-n`.
- Task state and its seal are one file written through a unique temp name, so a hook never reads one without the other (this also fixes the flaky concurrent-hooks test). Tasks saved by 0.3.0 still load and move over on the next save.
- While building, terminal commands that write the user's unsaved files or risky files outside the plan are refused and held for the report, and the 3-file cap counts them; a command that changes risky files is held, and past the cap the work stops.
- One checkpoint question per prompt, and the checkpoint checks run again before an approval is recorded.
- MCP tools are gated (PreToolUse matcher `mcp__.*`): tools that change files follow the edit rules, tools that run commands follow the Bash rules, and helpers stay read-only through them.
- A session binds to a task only on a real `plumbline task start` or `adopt`; the task follows `git checkout -b`; settings and data-folder protection works by resolved path (cd tracked, case-insensitive on macOS); the paused flag is sealed (a pause from 0.3.0 has to be redone); secret reads through globs, `<`, `curl -d@`, `-F f=@` and `wget --post-file` ask first.
- Commits: pathspec commits are scanned for secrets, `--trailer` and `$(cat file)` messages are checked for AI markers, a message or option that isn't known until it runs is refused; `git revert` (without `--no-commit`), `commit-tree` and `update-ref` are blocked; `gh`, `hub` and `glab` count as outside the laptop; `git -C` and `cd` into another repo use that repo's commit rule.
- Words are read the way the shell expands them: `$'\x..'` escapes, brace lists (`git {push,x}`), zsh's `=git`, patterns in redirects. Shell variables set to known values in the same line are followed (`n=3; sed -n "${n}p"`), and each value of a `for` loop is checked as its own command; a loop body or function that may change a value makes it unknown.
- Wrappers fail closed too: an option of `sudo`, `env`, `timeout`, `nice`, `time`, `xargs` and the others that isn't known makes the command unknown (a value could otherwise be read as the command). Added `xcrun`, `noglob`, `nocorrect`, `-`, `setsid`, `unbuffer`, `taskset`, `sched`, `emulate -c`.
- Arithmetic on a value holding `$(` or a backtick, extended globs and zsh glob qualifiers are refused (they can run code). `less +cmd` and `ag --pager` aren't read-only.
- Git: setting an alias or include (`git config alias.x`, `-c include.path`) is refused, an unknown git option before the subcommand or an alias that can't be looked up is refused, and git's global config files are protected. Every commit in a line is checked, each in its own repo. `HUSKY` set to anything but 1 or true counts as skipping hooks.
- Archives extracted into, or folders copied recursively into, `~/.claude`, the data folder or a folder holding `.git` are refused. A saved command with extra redirects or env no longer counts as saved. MCP shell tools are found under any command field, and the changed-files check keeps a snapshot per repo.
- Release binaries are built with Go 1.27.4 (`toolchain` in go.mod); 1.24.7 no longer gets security fixes.
- `plumbline setup save` no longer stops the work; the approved plan's case tests are kept at approval; proof kinds are read from formatted bullets (`- **api**: ...`); `plumbline run proof` refuses commands that prove nothing (`true`, `echo`).

## 0.3.0: phase 1b, the challenger and one reviewer
- New read-only helpers `plumbline:challenger` and `plumbline:reviewer` (same model as the session), sharing one checklist; findings scored, only 80 or more reported.
- The checkpoint needs a finished challenger run on both documents and a `## Challenge note` in plan.md (normal and big tasks; at most 2 rounds).
- The finish check needs a review of the current code and a `## Review` section in a report written after it (at most 3 reviews: the first plus 2 fix rounds).
- Helpers can only read, run read-only commands, and write their own findings file; the lead can't edit those files. Helper runs are recorded by SubagentStart/SubagentStop with the code fingerprint at both ends.
- Waiting on a background helper is no longer sent back as an unproven "done".
- `parts.challenger` / `parts.reviewer` in the setup file switch them off per project.
- Fixed: `plumbline docs-dir`, `status`, `task show` and other read-only plumbline commands were refused before approval.
- Fixed: the code fingerprint could miss a same-size edit made in the same second git last indexed the file, so proof could count for code that had changed.

## 0.2.0: hands-off by default
- One checkpoint with the understanding and the plan together (header "Checkpoint"); "Checkpoint 1" stays optional for very unclear tasks.
- Nothing waits on a prompt during a task: unsaved commands before approval are refused instead of asked; while building, same-change files outside the plan are allowed and must be in the report, risky files outside the plan and your unsaved work are refused and must be under "For you".
- New plan section "## Needs your OK": packages and outside-the-laptop commands it lists run without a prompt once you approve.
- Changes outside the plan while building are reported to Claude instead of stopping the work. Real decisions are still asked right away.
- More than 3 files outside the plan stops and asks: the plan was likely wrong.
- Replaces the old per-file confirm prompt and the two-checkpoint default.

## 0.1.0: phase 1a, the core
- Task workflow with two checkpoints on Claude Code's question prompt (auto mode, no plan mode).
- Edit backstop, Bash gate, commit rule, finish check, changed-files check, user-only pause/resume/cancel/wrong.
- Recorded proof tied to the code fingerprint; baseline-aware "no new failures"; "0 tests ran" fails.
- Project and laptop setup, task logs with 6-month detail and 1-year summaries, stats.
