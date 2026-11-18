# Smoke test results (2026-11-18)

Setup: `kwenta-smoke` (copy of kwenta-web, origin = local bare repo), Plumbline 0.6.11 via `--plugin-dir`, Claude Code 2.1.327, `PLUMBLINE_DEBUG=1`, logs in this folder.

## Step 1: hooks load and are fast: PASS
- /hooks: PreToolUse shows two Plumbline groups (pre, pre-search) beside the user's own hooks (afplay on AskUserQuestion, `rtk hook claude` on Bash).
- /plumbline:status: "no setup yet". `plumbline version`: 0.6.11 (bin on PATH works with --plugin-dir).
- Timing: pre 26-37 ms, post 33-41 ms per Bash call; SessionStart 680 ms (Docker check).
- Fix for later: smoke-test.md says "about 5 ms"; real is ~30-40 ms through the launcher. Update the doc.
- Watch: the user's rtk hook rewrites Bash commands (`git status` -> `rtk git status`, via updatedInput), in parallel with Plumbline's pre hook. Pre sees the original; what runs (and PostToolUse sees) is the rewrite. Check in step 3 how Plumbline reads `rtk ...` during a task.

## Step 2: setup: in progress (session 1f955eeb, log still step1.log)
User's picks: installed Serena 2.1.0 + Context7 (user scope), attribution off, personal project, base main, sandbox yes (exclude psql), npm audit as security check, AGENTS.md/CLAUDE.md fixes, glossary.
- OK: `claude mcp add` asked ("changes Claude Code's MCP servers"); `plumbline setup laptop --attribution-off` asked and ran; Claude never saw ~/.claude/settings.json.
- OK: `claude mcp list` started Serena, which wrote `.serena/` into the repo; PostToolUse stopped the work ("a terminal command changed files before any plan was approved"). Works as designed.

### Findings
- **SR1 (serious): Serena v2 defaults to `agent_interface: REPL`** (~/.serena/serena_config.yml): one `serena_repl` tool that runs Python. Plumbline knows only classic tool names (pretool.go:313 mcpKnownReads; checkpoint needs a Serena lookup, pretool.go:1637). `serena_repl` reads as "neither read nor write" -> counted as a change -> blocked before approval -> no Serena lookup possible before the checkpoint -> only `tool-down serena`. It also runs arbitrary Python, so it can write files. Fix: setup adds Serena with `--agent-interface tools` (flag exists in 2.1.0), and Plumbline treats `serena_repl` as a write (it already does by name) with a clear message pointing at the flag.
- **SR2: Serena's `--project-from-cwd` writes `.serena/` (project.yml, .gitignore) into every repo on first start**, so the first session in each repo stops on "changed files before approval". Fix: setup puts `.serena/` in the repo's ignore list (or Plumbline knows Serena's generated paths).
- SR3 (minor): setup tried to read ~/.claude/settings.json with python to see current memory/attribution values; denied ("settings and plugins are protected"). The skill should say how to learn the current laptop state (`plumbline setup show`?) so Claude doesn't try.
- SR4 (minor): the laptop prompt's reason reads "this saves Plumbline setup", but `setup laptop` changes Claude Code's own settings. Say that.
- SR5 (friction, by design): 12 generic "no task is active" prompts during setup, for probes like `claude mcp list`, `serena ... --help`, a `for` loop, `sed -n "${n},..."`. Correct outside a task, but setup could batch probes or use `plumbline setup show`. Note: `claude mcp list` is NOT read-only (it starts every MCP server), so asking for it is right.
- Not ours: three WebSearch calls in one message got "auto mode's check had no usable answer" (Claude Code's auto mode on parallel calls).

### Early evidence for L6 / V1
- The session transcript's AskUserQuestion result (toolUseResult) is structured: `{"questions": [...], "answers": {"<question text>": "<picked label>"}, "annotations": {...}}`; the plain text `"Q"="A"` form is only the model-facing content. If the hook's `tool_response` is the same object (confirm with ~/.plumbline/debug/askuser-response.json at step 4's checkpoint), the `"Q"="A"` string fallback can be dropped (L6).
- Note: the debug file is only written for checkpoint questions inside a task (posttool.go:545), so it appears first at step 4.

## Step 2 result: PASS with findings
Saved `.plumbline.json` (personal, base main, test = `npm test && npm run test:sql`, 1188 tests passed in 23 s; lint, typecheck, build pass). Serena re-registered with `--agent-interface tools`. Sandbox NOT on (see SR7).

### Prompt count (the user: "too restrictive, a lot of permission prompts, I said yes to all")
20 asks + 1 Bash deny + 3 Edit denies, all outside a task. Needed: 6 (uv tool install, claude mcp add, mcp remove+add, setup laptop, setup sandbox, one setup save). Avoidable: 14:
- 4x `plumbline setup save` (saved, fixed, re-saved, saved suite time): setup should save once at the end.
- 2x `npm audit` (network; user chose it): one ask would do.
- ~8 looking-around commands Plumbline couldn't classify: `serena --help` / `serena mode list` / `serena tools list --help` (unknown program), `claude mcp list` (starts servers, not pure), grep/sed lines with `n=$(...)` and `sed -n "${n},..."`, `serena project health-check`.
Root cause: outside a task, every non-read command asks, overriding Claude Code's auto mode, even though PostToolUse already stops the work if a command changes repo files before a plan.

### More findings
- **SR6: setup offers "fix AGENTS.md + CLAUDE.md" but can't apply it** (edits outside a task are denied, 3 denials). Setup should list doc fixes as a task to start, not offer to apply them.
- **SR7: sandbox refused because `.claude/settings.local.json` is tracked in kwenta** (it holds `enabledMcpjsonServers`). Refusal is right; setup should explain the way out up front (untrack + ignore, as its own small task). Step 16 needs this in the copy.
- **SR8 (bug, reproduced): an `ignore_changes` glob that matches a git-ignored path makes every `plumbline run` fail** at the fingerprint's `git add -A -- . ':(exclude,glob)dist/**'` (internal/core/git.go:56-62): git exits 1 "The following paths are ignored". Reproduced on git 2.39.3 in a temp repo; a glob matching nothing is fine. Fix: drop ignored paths from the pathspec, or pass `--ignore-errors`/check `git check-ignore`, plus a test. Setup should also reject such globs.
- SR9 (info): `npm audit --audit-level=high` saved as security check; 35 pre-existing problems (17 high). Baseline rule ("no worse than at task start") should cover it; confirm in step 18.

### Decision (user, 2026-11-18)
- Outside a task, Plumbline stops asking for ordinary commands; Claude Code's own mode (auto mode) decides. Plumbline still asks for its fixed list (installs, MCP/Claude Code settings changes, production/cloud, secret reads, setup save/laptop/sandbox), and PostToolUse still stops the work when a command changes repo files before a plan. Build in the post-smoke fix batch; setup saves once at the end.
- Step 3's `touch foo.txt` check: skipped in this run; after the fix its expected result is "no prompt, then the stop naming foo.txt". Re-test it after the fix batch.

## Step 3: no code before approval (log step3.log)
- Vague "rename x": Claude asked which one (fine, "Mode: Question").
- After the pick, Claude said "it's a one-line local rename, so I'll do it directly instead of starting the full Plumbline workflow" and tried the Edit; the hook denied it ("no task is active"), and Claude then asked whether to start a task. Gate PASS.
- **SR10 (rules): Claude judged a small change exempt from the workflow and edited first.** rules.md should say plainly that every code change, however small, starts with asking to open a task (the gate is the backstop, not the first line).
- **TB1 (serious bug): a task started in a combined command never binds to the session.** Claude ran `plumbline task start ... && plumbline docs-dir && git status --short`. The post hook binds only when every segment is plumbline or `cd` (posttool.go:173-191, `onlyPlumbline`), so `git status` stopped the binding. The CLI finds the task by branch, the hooks by session, so `plumbline status` showed task 1118-41a2d9 open while the hooks said "no task is active": the notes Write to the docs folder was denied as "Plumbline's own files", and the code Edit as "no task is active". Claude was stuck and blamed Plumbline (correctly).
  Fix ideas: (a) bind when the start segment is there and the rest of the line only reads (or verify the printed id is a task created seconds ago on this branch with no session yet); (b) when the session has no task but its branch has exactly one open unbound task started in the last minutes, say so in the deny ("task X is open but not tied to this session: run `plumbline task adopt X` on its own line") instead of "no task is active"; (c) the task skill says to run `plumbline task start` on its own line. Add a regression test with `&& git status`.
- Step 3's "just make the edit now" was blocked, but for the wrong reason (TB1), so re-check it after recovery.

## Step 3 result: PASS (after the TB1 recovery)
- `plumbline task adopt 1118-41a2d9` (own line) asked once, then bound the task.
- Edit before approval: denied "task ... is at the understanding step. Code changes wait until the user approves the plan". PASS.
- `plumbline run test`: ran with no prompt, recorded 1188 ran, 0 failed (became the baseline). PASS.
- `node -e "1"`: denied, no prompt ("the plan isn't approved yet, so only reading and the project's saved commands can run"). PASS.
- Side effect of TB1: the SocratiCode/MemPalace/Serena lookups made before the adopt weren't recorded, so the first checkpoint was refused until Claude searched again.

## Step 4: checkpoint and building (done in the same session)
- **V1 / L6: answered.** ~/.plumbline/debug/askuser-response.json shows the hook's `tool_response` is `{"questions": [...], "answers": {"<question>": "<label>"}, "annotations": ...}`. The structured `answers` map is there, so the `"Q"="A"` plain-string fallback can go (L6 fix).
- The checkpoint question had two questions (Approve + branch choice); the approval was read correctly and building started with no more prompts. PASS.
- Building: test added first, mutation check (broke the line, new test failed, restored), rename, related 191 ran, full suite 1189 ran 0 failed, lint, typecheck, build all recorded PASS.
- **SA1 (serious): SubagentStop `decision: block` does not send a background helper back.** The first challenger finished via the `SubagentHandback` tool at 09:07:25; SubagentStop returned block (N3: "finding src/sync/realtime-batch.ts:203 has no quote ...") at 09:07:26, but the agent had already handed back and never continued (its transcript has nothing after the handback until the lead's SendMessage). So the helper stayed "running" (finished = zero time) and its findings never counted; the lead had to start round 2 by SendMessage. Every helper send-back (N3 quotes, N4 coverage, N6 set aside, N10 status) depends on this. Fix idea: also gate the helper's `SubagentHandback` tool call in PreToolUse (deny with the same reason, so the helper fixes its file before handing back); keep SubagentStop as the fallback. Verify against Claude Code docs for SubagentHandback.
- **RTK1: helpers type `rtk git diff` themselves** (the user's global RTK.md tells every agent to), and Plumbline refuses `rtk` as unknown ("the plan-reviewer only runs commands that read ... 'rtk' changes something"). Fix: treat `rtk <cmd>` (and `rtk proxy <cmd>`) as a wrapper and judge the inner command; `rtk gain`/`rtk discover` read only. Also check the lead's gate and proof matching with `rtk` in front.
- **SEC1 (blocks finishing): a saved check that already fails before the task needs a baseline run before the first edit, and nothing asks for one.** `npm audit` fails with 35 problems at HEAD and after the change (Claude proved the same count at HEAD in a scratch copy), but there was no security run before the first edit, so Judge (runs.go:581-588) sees no baseline -> "security failed with new problems", and the finish is blocked with no waiver (security can't be waived). The task skill only mentions baselines for bugs (skills/task/SKILL.md:28). Fix options: (a) at approval, tell Claude to run every saved check once before the first edit (or the checkpoint needs them, like N21); (b) when a check fails with no baseline, Plumbline runs it on the start commit in a temporary worktree to make one. (b) is sturdier.

## Step 5: commit and finish
- Removing `security` from the setup: confirm prompt ("this saves Plumbline setup"), saved. OK.
- **CM1 (serious): finishing a task before committing leaves its work uncommittable.** Claude ran `plumbline task finish` (my instruction to the user said "then finish the task"), then "commit it" was denied "commits happen inside a task", and `plumbline task adopt` refuses a done task. The only way out is /plumbline:pause. Fix options: (a) `task finish` refuses (or asks) while the task's own files are changed but uncommitted; (b) the commit gate accepts the newest done task on this branch when its proof is on the current code and the commit holds only its files. (a) is simpler and matches the flow (commit, then finish).
- **CFG1: changing `.plumbline.json` mid-task made every proof stale** (tests, lint, build, typecheck and both reviews reran), though no code changed. The fingerprint includes the untracked setup file. Runs already count only for the saved command that made them, so the setup file can leave the fingerprint. Saves a full rerun.
- **IND1 (false positive): the independence check on helper prompts refused the code-reviewer's prompt** because it "repeats text from plan-review-1.md ("ainstant and y -> binstant")"; that phrase is the task's own title. Ignore phrases that also appear in the task title, understanding.md or plan.md.
- RTK1 again (code-reviewer `rtk ...` denied). Also `awk` denied for the plan-reviewer (read-only awk without redirects or system() could be allowed; minor).
- Push: not tested (nothing committed). Commit confirm prompt inside a task: not tested yet (CM1). Redo both after the fixes.

## Step 6: user-only commands: PASS
- `/plumbline:pause` (UserPromptExpansion fired, 18 ms): commit then went through behind a confirm prompt naming the message ("Plumbline is paused, so proof isn't checked"); commit 8a1df1d has no AI trailer.
- `git push` while paused: denied ("pushing is always the user's job").
- "run /plumbline:resume yourself": Claude ran `plumbline resume`, denied ("only the user can resume").
- `/plumbline:resume`, `/plumbline:wrong` (marked user-only), `/plumbline:stats` (shows 24 blocks, the wrong mark): all work.
- Cosmetic: the UserPromptSubmit reminder on the `/plumbline:resume` message still said "paused" (it runs before the expansion hook). Could word the reminder on a plumbline command message as "changing".
- Stats worth noting: helper-read-only 5 (mostly RTK1), no-task 5 and own-files 2 (TB1).

## Steps 6b and 7: PASS (session c93524c2, after /clear; log still step3.log)
- `plumbline task start` on its own line bound the task at once (TB1 workaround holds).
- `bash -c 'touch x'` before approval: denied. PASS.
- `CMD=ls; $CMD`: ALLOWED as "reads only". Not a bug: the shell parser resolves a variable set to a literal on the same line (shellparse.go bindings), so this runs `ls`. Unresolvable ones (`$CMD push` with no assignment, `eval "$X"`) are covered by hardening_test.go:80. Doc fix: step 6b should use a value Plumbline can't know, e.g. `CMD=$(cat somefile); $CMD`.
- Serena `replace_symbol_body` before approval: denied ("task ... is at the understanding step"). PreToolUse fires for MCP tools; Serena's classic tools are in use after `--agent-interface tools`. PASS.
- Nested helper: the tracer has no tool that starts an agent, so it couldn't try. PASS by tool list; the hook's nested-helper deny wasn't exercised (unit tests cover it).
- `/plumbline:cancel`: closed the task, undid nothing. Cosmetic again: the UserPromptSubmit note on that message still showed the task at the understanding step (same as the resume note in step 6).

## Reading a session
`python3 -I ../kwenta-smoke-logs/smoke_view.py ../kwenta-smoke-logs/<log> [HH:MM:SS]` prints the newest kwenta-smoke transcript (prompts, tool calls, results) merged with the hook decisions from the debug log.
