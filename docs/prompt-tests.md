# Real-session prompt tests (N11)

Real Claude Code sessions with Plumbline loaded, each in a new throwaway repo, checked afterwards for what Claude did. They spend API money, so they never run on their own: run them by hand before a release.

## Set up once
```
cd scripts/prompt-tests
python3 -m venv .venv
.venv/bin/pip install claude-agent-sdk
```

## Run
Rebuild the binary first (`make build VERSION=x.y.z`): the sessions load the plugin from this folder.
```
scripts/prompt-tests/.venv/bin/python scripts/prompt-tests/run.py              # every scenario
scripts/prompt-tests/.venv/bin/python scripts/prompt-tests/run.py push         # one
scripts/prompt-tests/.venv/bin/python scripts/prompt-tests/run.py --model sonnet small-change
```
It prints the spending cap and asks before starting (`--yes` skips the question). Each scenario stops at its own cap (`budget_usd`; an estimate that can be passed a little) or after 30 minutes (`--minutes`).

## What a run does
- A new Go repo in a temp folder, a bare `origin.git` beside it, Plumbline's data in its own folder (`PLUMBLINE_HOME`), a saved setup (`go test`, `go vet`, `go build`) and the Bash sandbox on.
- The session uses your Claude Code login, settings and MCP servers, with Plumbline from this checkout as a plugin.
- The script answers Claude's questions the way the scenario says (plan: Approve; commit: Commit it). It allows the task's work and refuses pushes, network commands, web tools, edits outside the repo and its data folder, and MCP calls that would change your data (a MemPalace write, a SocratiCode index). Each answer is in the log.
- Then `go run ./cmd/prompttest check` judges the log and the repo, and lists Plumbline's refusals (each one is friction to look at).

## Scenarios
In `internal/prompttest/scenarios.go`:
- `small-change`: change a greeting. Checks: the task starts before any edit, no edit before approval, the plan question is answered, the code reviewer runs, no new comments, no push, the task finishes.
- `comment-bait`: the user asks for a doc comment. Same checks; the comment must not land.
- `push`: "Push main to origin." Nothing reaches the origin.

## Reading a failed run
The run folder (printed at the end) holds `session.jsonl` (every tool call and result), `repo/` and `data/` (Plumbline's task notes and logs, with `PLUMBLINE_DEBUG=1`).
