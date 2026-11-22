import argparse
import asyncio
import json
import os
import subprocess
import sys
import tempfile
import time
from pathlib import Path

from claude_agent_sdk import ClaudeAgentOptions, query
from claude_agent_sdk.types import HookMatcher, PermissionResultAllow, PermissionResultDeny

ROOT = Path(__file__).resolve().parents[2]
DENY_BASH = ("git push", "curl ", "wget ", "ssh ", "scp ", "sudo ", "gh ", "npm publish", "rsync ")
MCP_WRITE_WORDS = {"add", "write", "delete", "update", "create", "mine", "sync", "checkpoint", "invalidate",
                   "supersede", "patch", "ack", "put", "remove", "index", "watch", "build", "prune", "stop",
                   "reconnect", "append", "replace", "rename", "insert", "safe"}
DENY_TOOLS = {"WebFetch", "WebSearch"}
EDIT_TOOLS = {"Edit", "Write", "MultiEdit", "NotebookEdit"}
SCRATCH = str(Path("/tmp").resolve() / "claude-")


def prompttest(*args):
    out = subprocess.run(["go", "run", "./cmd/prompttest", *args], cwd=ROOT, capture_output=True, text=True)
    return out.returncode, out.stdout, out.stderr


def inside(path, roots):
    try:
        p = Path(path).resolve()
    except (OSError, ValueError):
        return False
    if str(p).startswith(SCRATCH):
        return True
    return any(p == r or r in p.parents for r in roots)


class Session:
    def __init__(self, scenario, fixture, log_path):
        self.scenario = scenario
        self.fixture = fixture
        self.log = open(log_path, "w")
        self.roots = [Path(fixture["repo"]).resolve(), Path(fixture["data"]).resolve()]
        self.plugin_loaded = None

    def write(self, event):
        self.log.write(json.dumps(event, default=str) + "\n")
        self.log.flush()

    def decide(self, tool, data):
        if tool in DENY_TOOLS:
            return False, "the prompt test runs offline"
        if tool in ("Bash", "Monitor"):
            cmd = " " + str(data.get("command", "")) + " "
            for bad in DENY_BASH:
                if bad in cmd:
                    return False, "the prompt test doesn't allow this command"
        if tool in EDIT_TOOLS:
            path = data.get("file_path") or data.get("notebook_path") or ""
            if not inside(path, self.roots):
                return False, "the prompt test only allows edits in its own folders"
        if tool.startswith("mcp__"):
            words = set(tool.split("__")[-1].replace("-", "_").split("_"))
            if words & MCP_WRITE_WORDS:
                return False, "the prompt test doesn't change the user's MCP data"
        return True, ""

    def answer(self, data):
        answers = {}
        for q in data.get("questions", []):
            labels = [o.get("label", "") for o in q.get("options", [])]
            pick = next((l for a in self.scenario["answers"] for l in labels if l.startswith(a)), labels[0] if labels else "")
            answers[q.get("question", "")] = pick
        return answers

    async def can_use_tool(self, tool, data, context):
        if tool == "AskUserQuestion":
            answers = self.answer(data)
            self.write({"type": "permission", "tool": tool, "input": data, "allowed": True, "answers": answers})
            return PermissionResultAllow(updated_input={"questions": data.get("questions", []), "answers": answers})
        ok, why = self.decide(tool, data)
        self.write({"type": "permission", "tool": tool, "input": data, "allowed": ok})
        if ok:
            return PermissionResultAllow(updated_input=data)
        return PermissionResultDeny(message=why)

    def record(self, message):
        kind = type(message).__name__
        if kind == "SystemMessage" and getattr(message, "subtype", "") == "init":
            plugins = (getattr(message, "data", {}) or {}).get("plugins", [])
            self.plugin_loaded = any("plumbline" in json.dumps(p).lower() for p in plugins)
            return
        if kind in ("AssistantMessage", "UserMessage"):
            content = getattr(message, "content", None)
            if not isinstance(content, list):
                return
            blocks = []
            for b in content:
                bk = type(b).__name__
                if bk == "ToolUseBlock":
                    blocks.append({"type": "tool_use", "id": b.id, "name": b.name, "input": b.input})
                elif bk == "ToolResultBlock":
                    blocks.append({"type": "tool_result", "tool_use_id": b.tool_use_id, "content": b.content, "is_error": bool(b.is_error)})
                elif bk == "TextBlock":
                    blocks.append({"type": "text", "text": b.text})
            if blocks:
                role = "assistant" if kind == "AssistantMessage" else "user"
                self.write({"type": role, "parent_tool_use_id": getattr(message, "parent_tool_use_id", None), "message": {"content": blocks}})
            return
        if kind == "ResultMessage":
            self.write({"type": "result", "subtype": message.subtype, "total_cost_usd": message.total_cost_usd or 0,
                        "num_turns": message.num_turns, "result": message.result or ""})


async def keep_open(input_data, tool_use_id, context):
    return {"continue_": True}


async def run_session(scenario, fixture, log_path, model, minutes):
    s = Session(scenario, fixture, log_path)
    repo = fixture["repo"]
    env = {
        "PLUMBLINE_HOME": fixture["data"],
        "PLUMBLINE_DEBUG": "1",
        "GOCACHE": os.path.join(repo, ".cache", "go-build"),
        "GOTMPDIR": os.path.join(repo, ".cache", "tmp"),
        "GOTOOLCHAIN": "local",
    }

    async def prompt():
        yield {"type": "user", "message": {"role": "user", "content": scenario["prompt"]}}

    options = ClaudeAgentOptions(
        cwd=repo,
        plugins=[{"type": "local", "path": str(ROOT)}],
        can_use_tool=s.can_use_tool,
        hooks={"PreToolUse": [HookMatcher(matcher=None, hooks=[keep_open])]},
        max_budget_usd=scenario["budget_usd"],
        model=model,
        env=env,
    )

    async def drive():
        async for message in query(prompt=prompt(), options=options):
            s.record(message)

    try:
        await asyncio.wait_for(drive(), timeout=minutes * 60)
        note = ""
    except asyncio.TimeoutError:
        note = f"stopped after {minutes} minutes"
    except Exception as e:
        note = f"the session stopped with an error: {e}"
    finally:
        s.log.close()
    return s.plugin_loaded, note


def main():
    code, out, err = prompttest("list")
    if code != 0:
        sys.exit("prompttest list failed: " + err)
    scenarios = {s["name"]: s for s in json.loads(out)}
    ap = argparse.ArgumentParser(description="Real-session prompt tests for Plumbline. Each run spends API money.")
    ap.add_argument("names", nargs="*", help="scenarios to run (default: all): " + ", ".join(scenarios))
    ap.add_argument("--model", default=None, help="model alias or id (default: Claude Code's default)")
    ap.add_argument("--minutes", type=int, default=30, help="time limit per scenario")
    ap.add_argument("--out", default=None, help="folder for the runs (default: a new temp folder)")
    ap.add_argument("--yes", action="store_true", help="don't ask before spending")
    args = ap.parse_args()
    names = args.names or list(scenarios)
    unknown = [n for n in names if n not in scenarios]
    if unknown:
        sys.exit("unknown scenario: " + ", ".join(unknown))
    cap = sum(scenarios[n]["budget_usd"] for n in names)
    print(f"Scenarios: {', '.join(names)}. Spending cap: about ${cap:.2f} in total (the cap is an estimate and can be passed a little).")
    if not args.yes and input("Run them? [y/N] ").strip().lower() != "y":
        sys.exit("not run")
    base = Path(args.out or tempfile.mkdtemp(prefix="plumbline-prompt-tests-")).resolve()
    stamp = time.strftime("%Y%m%d-%H%M%S")
    failed = []
    for n in names:
        sc = scenarios[n]
        work = base / f"{stamp}-{n}"
        work.mkdir(parents=True)
        code, out, err = prompttest("fixture", str(work))
        if code != 0:
            print(f"\n{n}: the throwaway repo couldn't be made: {err.strip()}")
            failed.append(n)
            continue
        fx = json.loads(out)
        log_path = work / "session.jsonl"
        print(f"\n{n}: {sc['prompt']}\n  folder: {work}")
        loaded, note = asyncio.run(run_session(sc, fx, log_path, args.model, args.minutes))
        if loaded is False:
            print("  FAIL  Plumbline didn't load as a plugin")
            failed.append(n)
            continue
        if note:
            print("  note  " + note)
        code, out, err = prompttest("check", "--scenario", n, "--log", str(log_path), "--repo", fx["repo"], "--origin", fx["origin"], "--base", fx["base"])
        print(out.rstrip())
        if err.strip():
            print("  " + err.strip())
        if code != 0:
            failed.append(n)
    print(f"\n{len(names) - len(failed)} of {len(names)} passed." + (" Failed: " + ", ".join(failed) if failed else ""))
    print(f"Runs kept in {base}")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
