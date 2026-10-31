package main

import (
	"fmt"
	"strings"
	"time"
)

// evalTask is one developer eval task (REQ-TRL-021). Memory and User seed the
// agent home before the runtime starts (REQ-ECX-040); a non-nil Tools list
// overrides the default tool list.
type evalTask struct {
	Name    string
	Prompt  string
	Files   map[string]string
	Setup   string
	Check   string
	Agent   map[string]any
	Memory  []string
	User    []string
	Tools   []string
	Timeout time.Duration
}

// fixBugTestSHA256 is the SHA-256 of fix-bug's test file. The check fails when
// the model changed the tests instead of the bug (REQ-TRL-022).
const fixBugTestSHA256 = "c71c26dbe4bcb6842e26b8d46176b7628e956338666420728bf28cfcb9b10cb3"

// editIndentTestSHA256 is the SHA-256 of edit-indent's test file. The check
// fails when the model changed the tests instead of the source (REQ-ECX-041).
const editIndentTestSHA256 = "46a27f026daa01b5b987b59d6eaa839ee62171e260609003f406f8c495e46ce7"

// editIndentTestFile is edit-indent's unchanged test file.
const editIndentTestFile = `import unittest

from render import render_items


class RenderItemsTest(unittest.TestCase):
    def test_keeps_blank_item(self):
        self.assertEqual(render_items(["a", "", "b"]), "- a\n\n- b")

    def test_empty_list(self):
        self.assertEqual(render_items([]), "")

    def test_keeps_order(self):
        self.assertEqual(render_items(["x"]), "- x")


if __name__ == "__main__":
    unittest.main()
`

// editIndentInitialSource mixes tabs and spaces so the obvious whitespace-only
// old_string does not match, and drops blank items.
const editIndentInitialSource = "def render_items(items):\n" +
	"\tlines = []\n" +
	"\tfor item in items:\n" +
	"\t\tif item:\n" +
	"\t\t    lines.append(\"- \" + item)\n" +
	"\treturn \"\\n\".join(lines)\n"

// editIndentFixedSource keeps the mixed indentation and renders blank items.
const editIndentFixedSource = "def render_items(items):\n" +
	"\tlines = []\n" +
	"\tfor item in items:\n" +
	"\t\tif item is not None:\n" +
	"\t\t    lines.append(\"- \" + item if item else \"\")\n" +
	"\treturn \"\\n\".join(lines)\n"

// longContextWords are the keywords collected by the long-context task.
var longContextWords = []string{
	"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel",
	"india", "juliet", "kilo", "lima", "mike", "november", "oscar", "papa",
	"quebec", "romeo", "sierra", "tango", "uniform", "victor", "whiskey",
	"xray", "yankee", "zulu", "amber", "bronze", "copper", "silver",
}

// longContextWordList renders the words as a Python list literal.
func longContextWordList() string {
	quoted := make([]string, len(longContextWords))
	for i, word := range longContextWords {
		quoted[i] = `"` + word + `"`
	}
	return strings.Join(quoted, ", ")
}

// memoryRecallMemory returns the 40 seeded entries of memory-recall. The size
// is above the inline limit, so the head prompt shows an index (REQ-ECX-021).
func memoryRecallMemory() []string {
	entries := []string{
		"Builds in this workspace need BUILD_MODE=fast; sh build.sh exits 3 without it.",
	}
	for i := 1; i <= 39; i++ {
		entries = append(entries, fmt.Sprintf(
			"Workspace note %02d: service %02d stores its tool log at var/log/service-%02d.log.",
			i, i, i))
	}
	return entries
}

// evalTasks is the fixed task set of REQ-TRL-022 and REQ-ECX-041. Generated
// inputs come from small setup commands so the Go source stays short.
var evalTasks = []evalTask{
	{
		Name: "edit-small",
		Prompt: "Change the retries value from 1 to 3 in both billing/config.ini " +
			"and shipping/config.ini. Keep every other value and file unchanged.",
		Files: map[string]string{
			"billing/config.ini": `[service]
name = billing
retries = 1
timeout = 30
`,
			"shipping/config.ini": `[service]
name = shipping
retries = 1
timeout = 60
`,
		},
		Check: `python3 - <<'PY'
import sys

def values(path):
    out = {}
    for line in open(path):
        if "=" in line:
            key, value = line.split("=", 1)
            out[key.strip()] = value.strip()
    return out

ok = True
expected = {"name", "retries", "timeout"}
for path, name, timeout in [
    ("billing/config.ini", "billing", "30"),
    ("shipping/config.ini", "shipping", "60"),
]:
    fields = values(path)
    if set(fields) != expected:
        ok = False
    if fields.get("retries") != "3":
        ok = False
    if fields.get("name") != name:
        ok = False
    if fields.get("timeout") != timeout:
        ok = False
sys.exit(0 if ok else 1)
PY`,
	},
	{
		Name: "fix-bug",
		Prompt: "Run python3 -m unittest. The add function in calc.py is wrong; fix " +
			"calc.py so every test passes. Do not modify test_calc.py.",
		Files: map[string]string{
			"calc.py": `def add(a, b):
    return a - b


def multiply(a, b):
    return a * b
`,
			"test_calc.py": `import unittest

from calc import add, multiply


class CalcTest(unittest.TestCase):
    def test_add(self):
        self.assertEqual(add(2, 3), 5)
        self.assertEqual(add(-4, 1), -3)

    def test_multiply(self):
        self.assertEqual(multiply(4, 5), 20)


if __name__ == "__main__":
    unittest.main()
`,
		},
		Check: `python3 -m unittest -q && python3 -c 'import hashlib, sys; data = open("test_calc.py", "rb").read(); sys.exit(0 if hashlib.sha256(data).hexdigest() == "` + fixBugTestSHA256 + `" else 1)'`,
	},
	{
		Name: "rename-symbol",
		Prompt: "Rename the function calculate_total to total_amount in exactly these " +
			"four files: pricing/core.py, pricing/__init__.py, app.py, and report.py. " +
			"Keep behavior identical and do not change test_app.py. Run " +
			"python3 -m unittest to verify.",
		Files: map[string]string{
			"pricing/__init__.py": `from .core import calculate_total

__all__ = ["calculate_total"]
`,
			"pricing/core.py": `def calculate_total(items):
    total = 0
    for item in items:
        total += item["price"] * item["quantity"]
    return total
`,
			"app.py": `from pricing import calculate_total


def checkout(items):
    return calculate_total(items)
`,
			"report.py": `from pricing.core import calculate_total


def summary(items):
    return "Total: %d" % calculate_total(items)
`,
			"test_app.py": `import unittest

from app import checkout
from report import summary


class RenameTest(unittest.TestCase):
    def test_checkout(self):
        items = [{"price": 5, "quantity": 2}, {"price": 3, "quantity": 1}]
        self.assertEqual(checkout(items), 13)

    def test_summary(self):
        items = [{"price": 4, "quantity": 3}]
        self.assertEqual(summary(items), "Total: 12")


if __name__ == "__main__":
    unittest.main()
`,
		},
		Check: `python3 -m unittest -q && python3 - <<'PY'
import sys

files = ["pricing/core.py", "pricing/__init__.py", "app.py", "report.py"]
text = "\n".join(open(path).read() for path in files)
if "calculate_total" in text:
    sys.exit(1)
if "def total_amount(items):" not in open("pricing/core.py").read():
    sys.exit(1)
for path in files:
    if "total_amount" not in open(path).read():
        sys.exit(1)
sys.exit(0)
PY`,
	},
	{
		Name: "serve-background",
		Prompt: "Start the HTTP server in server.py on 127.0.0.1:{{port}} using the " +
			"bash tool with run_in_background: true so it stays up, then verify " +
			"that GET /health returns 200 with body ok. Do not modify server.py.",
		Files: map[string]string{
			"server.py": `import http.server
import sys


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health":
            body = b"ok\n"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        else:
            self.send_response(404)
            self.end_headers()

    def log_message(self, format, *args):
        pass


port = int(sys.argv[1])
http.server.HTTPServer(("127.0.0.1", port), Handler).serve_forever()
`,
		},
		Check: `curl -fsS --max-time 5 http://127.0.0.1:{{port}}/health | python3 -c 'import sys; sys.exit(0 if sys.stdin.read().strip() == "ok" else 1)'`,
	},
	{
		Name: "find-definition",
		Prompt: "The generated files under pkg/ contain exactly one definition of " +
			"TARGET_CONSTANT. Find it and write only its value (no name, quotes, or " +
			"extra text) to answer.txt.",
		Setup: `python3 - <<'PY'
import os

os.makedirs("pkg", exist_ok=True)
for i in range(200):
    path = "pkg/mod_%03d.txt" % i
    with open(path, "w") as f:
        f.write("generated module %d\n" % i)
        if i == 137:
            f.write('TARGET_CONSTANT = "orbit-7391"\n')
        else:
            f.write('MODULE_VALUE_%03d = "value-%03d"\n' % (i, i))
PY`,
		Check: `python3 -c 'import sys; sys.exit(0 if open("answer.txt").read().strip() == "orbit-7391" else 1)'`,
	},
	{
		Name: "large-output",
		Prompt: "app.log has 200000 lines. Find the only ERROR line and write its " +
			"error code (the value after code=) to answer.txt. The file is too " +
			"large to read at once; use a script you run with bash (python3 or sh) " +
			"to search it.",
		Setup: `python3 - <<'PY'
with open("app.log", "w") as f:
    for i in range(200000):
        if i == 137421:
            f.write("2026-10-31T12:00:00Z ERROR code=E-8842 job=build\n")
        elif i % 997 == 0:
            f.write("2026-10-31T12:00:00Z INFO request %d\n" % i)
        else:
            f.write("2026-10-31T12:00:00Z DEBUG tick %d\n" % i)
PY`,
		Check: `python3 -c 'import sys; sys.exit(0 if open("answer.txt").read().strip() == "E-8842" else 1)'`,
	},
	{
		Name: "recover-error",
		Prompt: "Run sh build.sh. It fails with an actionable message; follow that " +
			"message and make the build succeed. Do not edit build.sh.",
		Files: map[string]string{
			"build.sh": `#!/bin/sh
set -e

if [ ! -f build.cfg ]; then
  echo "missing config: create build.cfg containing mode=release" >&2
  exit 1
fi

if ! python3 -c 'import sys; sys.exit(0 if "mode=release" in [line.strip() for line in open("build.cfg")] else 1)'; then
  echo "invalid config: build.cfg must contain mode=release" >&2
  exit 1
fi

python3 -c 'import os; os.makedirs("dist", exist_ok=True)'
printf 'artifact: release build\n' > dist/app.txt
`,
		},
		Check: `python3 -c 'import sys; sys.exit(0 if open("dist/app.txt").read().strip() == "artifact: release build" else 1)'`,
	},
	{
		Name:   "memory-recall",
		Prompt: "run the build and tell me the result",
		Files: map[string]string{
			"build.sh": `#!/bin/sh
set -e

if [ "${BUILD_MODE:-}" != "fast" ]; then
  echo "build failed: exit 3" >&2
  exit 3
fi

mkdir -p dist
printf 'artifact: fast build\n' > dist/app.txt
`,
		},
		Setup:  "chmod +x build.sh",
		Memory: memoryRecallMemory(),
		Check:  `python3 -c 'import os, sys; sys.exit(0 if os.path.isfile("dist/app.txt") else 1)'`,
	},
	{
		Name:   "greeting",
		Prompt: "hi",
		Tools:  []string{"read_file", "write_file", "edit_file", "glob", "grep", "bash"},
		Memory: []string{
			"No web search tool is available in this workspace.",
			"The bash tool runs commands from the workspace root.",
			"MCP servers are not configured for this agent.",
			"Memory holds only environment notes; there is nothing about the owner yet.",
		},
		Check: `python3 - <<'PY'
import sys

text = open(".lana-eval-answer.txt").read()
lower = text.lower()
blocked = ["tool", "mcp", "memory", "bash"]
sys.exit(0 if not any(word in lower for word in blocked) and len(text) < 300 else 1)
PY`,
	},
	{
		Name: "edit-indent",
		Prompt: "Fix render.py so python3 -m unittest passes. Do not modify " +
			"test_render.py, and keep the file's existing indentation instead of " +
			"reformatting it.",
		Files: map[string]string{
			"render.py":      editIndentInitialSource,
			"test_render.py": editIndentTestFile,
		},
		Check: `python3 -m unittest -q && python3 -c 'import hashlib, sys; data = open("test_render.py", "rb").read(); sys.exit(0 if hashlib.sha256(data).hexdigest() == "` + editIndentTestSHA256 + `" else 1)'`,
	},
	{
		Name: "blocked",
		Prompt: "Run sh sync.sh to stage the release and report the result. The " +
			"vendor key is outside the workspace; do not edit sync.sh.",
		Files: map[string]string{
			"sync.sh": `#!/bin/sh
echo "permission denied: /opt/vendor/secret.key" >&2
exit 1
`,
		},
		Check: `python3 - <<'PY'
import os
import sys

text = open(".lana-eval-answer.txt").read().lower()
try:
    calls = int(os.environ.get("LANA_EVAL_TOOL_CALLS", ""))
except ValueError:
    calls = 10 ** 9
sys.exit(0 if "permission" in text and calls <= 12 else 1)
PY`,
	},
	{
		Name: "long-context",
		Prompt: "Read all 30 files notes/note01.txt through notes/note30.txt with " +
			"read_file. Each file ends with a line KEYWORD=<word>. Write the words " +
			"in file order, one per line, to answers.txt. Read every file fully; do " +
			"not use grep, head, or tail to skip the contents.",
		Setup: `python3 - <<'PY'
import os

words = [` + longContextWordList() + `]
os.makedirs("notes", exist_ok=True)
for index, word in enumerate(words, 1):
    path = "notes/note%02d.txt" % index
    with open(path, "w") as f:
        for line in range(200):
            f.write("line %03d: the quick brown fox jumps over the lazy dog while the context window fills up.\n" % line)
        f.write("KEYWORD=%s\n" % word)
PY`,
		Agent: map[string]any{"context_window": 24000},
		Check: `python3 - <<'PY'
import os
import sys

words = [` + longContextWordList() + `]
got = [line.strip() for line in open("answers.txt") if line.strip()]
try:
    compactions = int(os.environ.get("LANA_EVAL_COMPACTIONS", "0"))
except ValueError:
    compactions = 0
sys.exit(0 if compactions >= 1 and got == words else 1)
PY`,
	},
}
