import asyncio
import json
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace

sys.path.insert(0, str(Path(__file__).resolve().parent))
import run


class FakeBlock:
    pass


def block(kind, **fields):
    cls = type(kind, (FakeBlock,), {})
    b = cls()
    for k, v in fields.items():
        setattr(b, k, v)
    return b


class SessionTest(unittest.TestCase):
    def setUp(self):
        self.dir = Path(tempfile.mkdtemp()).resolve()
        (self.dir / "repo").mkdir()
        (self.dir / "data").mkdir()
        fixture = {"repo": str(self.dir / "repo"), "data": str(self.dir / "data")}
        self.log = self.dir / "s.jsonl"
        self.s = run.Session({"answers": ["Approve", "Commit it"]}, fixture, self.log)

    def tearDown(self):
        self.s.log.close()

    def events(self):
        self.s.log.flush()
        return [json.loads(l) for l in self.log.read_text().splitlines()]

    def test_refuses_pushes_network_and_outside_edits(self):
        for tool, data in [
            ("Bash", {"command": "git push origin main"}),
            ("Bash", {"command": "cd x && curl https://example.com"}),
            ("Monitor", {"command": "gh pr list"}),
            ("WebFetch", {"url": "https://example.com"}),
            ("Write", {"file_path": str(Path.home() / ".zshrc")}),
            ("Edit", {"file_path": str(self.dir / "repo-other" / "a.go")}),
            ("Write", {"file_path": str(self.dir / "origin.git" / "refs" / "heads" / "main")}),
            ("Write", {"file_path": "/tmp/elsewhere.txt"}),
            ("mcp__mempalace__mempalace_add_drawer", {}),
            ("mcp__socraticode__codebase_index", {}),
            ("mcp__serena__replace_content", {}),
        ]:
            ok, _ = self.s.decide(tool, data)
            self.assertFalse(ok, (tool, data))

    def test_allows_the_task_work(self):
        for tool, data in [
            ("Bash", {"command": "plumbline task start --title x"}),
            ("Bash", {"command": "go test ./..."}),
            ("Edit", {"file_path": str(self.dir / "repo" / "greet" / "greet.go")}),
            ("Write", {"file_path": str(self.dir / "data" / "projects" / "p" / "plan.md")}),
            ("Write", {"file_path": "/tmp/claude-501/x/scratchpad/try.go"}),
            ("mcp__mempalace__mempalace_search", {"query": "x"}),
            ("mcp__socraticode__codebase_search", {"query": "x"}),
            ("Agent", {"subagent_type": "plumbline:code-reviewer"}),
        ]:
            ok, why = self.s.decide(tool, data)
            self.assertTrue(ok, (tool, data, why))

    def test_answers_with_the_scenario_labels(self):
        q = {"questions": [
            {"question": "Plan?", "options": [{"label": "Change something"}, {"label": "Approve"}, {"label": "Drop it"}]},
            {"question": "Commit?", "options": [{"label": "Commit it"}, {"label": "Leave uncommitted"}]},
            {"question": "Other?", "options": [{"label": "A"}, {"label": "B"}]},
            {"question": "Small?", "options": [{"label": "Drop it"}, {"label": "Approve and start"}]},
        ]}
        res = asyncio.run(self.s.can_use_tool("AskUserQuestion", q, None))
        self.assertEqual(res.updated_input["answers"], {"Plan?": "Approve", "Commit?": "Commit it", "Other?": "A", "Small?": "Approve and start"})
        self.assertEqual(res.updated_input["questions"], q["questions"])
        denied = asyncio.run(self.s.can_use_tool("Bash", {"command": "git push"}, None))
        self.assertEqual(type(denied).__name__, "PermissionResultDeny")
        ev = self.events()
        self.assertEqual([e["allowed"] for e in ev], [True, False])
        self.assertEqual(ev[0]["answers"]["Plan?"], "Approve")

    def test_records_the_shape_the_checker_reads(self):
        msg = type("AssistantMessage", (), {})()
        msg.parent_tool_use_id = None
        msg.content = [block("TextBlock", text="hi"), block("ToolUseBlock", id="t1", name="Bash", input={"command": "ls"}), block("ThinkingBlock", thinking="x")]
        self.s.record(msg)
        res = type("UserMessage", (), {})()
        res.parent_tool_use_id = "p1"
        res.content = [block("ToolResultBlock", tool_use_id="t1", content="ok", is_error=None)]
        self.s.record(res)
        end = type("ResultMessage", (), {})()
        end.subtype, end.total_cost_usd, end.num_turns, end.result = "success", None, 3, None
        self.s.record(end)
        init = type("SystemMessage", (), {})()
        init.subtype, init.data = "init", {"plugins": [{"name": "plumbline", "path": "/x"}]}
        self.s.record(init)
        ev = self.events()
        self.assertEqual(ev[0]["message"]["content"][1], {"type": "tool_use", "id": "t1", "name": "Bash", "input": {"command": "ls"}})
        self.assertEqual(len(ev[0]["message"]["content"]), 2)
        self.assertEqual(ev[1]["parent_tool_use_id"], "p1")
        self.assertEqual(ev[1]["message"]["content"][0]["is_error"], False)
        self.assertEqual(ev[2], {"type": "result", "subtype": "success", "total_cost_usd": 0, "num_turns": 3, "result": ""})
        self.assertTrue(self.s.plugin_loaded)


if __name__ == "__main__":
    unittest.main()
