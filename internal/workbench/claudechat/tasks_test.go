package claudechat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// appendFile appends JSONL lines to a transcript, as Claude Code would.
func appendFile(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func taskByID(tasks []Task, id string) (Task, bool) {
	for _, task := range tasks {
		if task.ID == id {
			return task, true
		}
	}
	return Task{}, false
}

// agentToolUse is the assistant line that launches a subagent.
const agentToolUse = `{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_1","name":"Agent","input":{"description":"T-002 transcript reader","subagent_type":"dsh-coder","run_in_background":true}}]}}`

// agentResult is the user line that reveals the async agent id.
const agentResult = `{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"launched"}]},"toolUseResult":{"isAsync":true,"status":"async_launched","agentId":"a56a9a94b0e1dbb18","description":"T-002 transcript reader"}}`

func TestAgentTaskAsyncThenNotification(t *testing.T) {
	path := writeFile(t, agentToolUse, agentResult)
	tr := Open(path, "/work")

	items, metaChanged, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !metaChanged {
		t.Errorf("metaChanged should be true after a task is created")
	}
	tool, ok := itemByID(items, "a1:0")
	if !ok {
		t.Fatalf("agent tool missing: %v", idsOf(items))
	}
	if tool.Agent == nil || tool.Agent.AgentID != "a56a9a94b0e1dbb18" || tool.Agent.AgentType != "dsh-coder" || !tool.Agent.Background {
		t.Fatalf("agent ref = %+v", tool.Agent)
	}
	tasks := tr.Meta().Tasks
	task, ok := taskByID(tasks, "a56a9a94b0e1dbb18")
	if !ok {
		t.Fatalf("agent task missing: %+v", tasks)
	}
	if task.Kind != "agent" || task.Status != "running" || task.Title != "T-002 transcript reader" || task.AgentType != "dsh-coder" {
		t.Errorf("task = %+v", task)
	}
	if task.Started != "2026-11-23T12:00:00Z" || task.Ended != "" {
		t.Errorf("task times = %q %q", task.Started, task.Ended)
	}

	appendFile(t, path,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-23T12:00:05Z","content":"<task-notification>\n<task-id>a56a9a94b0e1dbb18</task-id>\n<tool-use-id>toolu_1</tool-use-id>\n<output-file>/tmp/o</output-file>\n<status>completed</status>\n<summary>Agent \"T-002\" completed</summary>\n</task-notification>"}`)

	_, metaChanged, err = tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !metaChanged {
		t.Errorf("metaChanged should be true after the notification")
	}
	task, _ = taskByID(tr.Meta().Tasks, "a56a9a94b0e1dbb18")
	if task.Status != "completed" || task.Summary != `Agent "T-002" completed` || task.Ended != "2026-11-23T12:00:05Z" {
		t.Errorf("notified task = %+v", task)
	}
}

func TestAgentTaskSyncCompleted(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_2","name":"Task","input":{"description":"sync work","subagent_type":"explore"}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:09Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"final report"}]},"toolUseResult":{"agentId":"syncagent1","description":"sync work"}}`,
	)
	tr := Open(path, "/work")
	items, _, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	tool, _ := itemByID(items, "a1:0")
	if tool.Agent == nil || tool.Agent.AgentID != "syncagent1" || tool.Agent.Background {
		t.Fatalf("agent ref = %+v", tool.Agent)
	}
	task, ok := taskByID(tr.Meta().Tasks, "syncagent1")
	if !ok {
		t.Fatalf("agent task missing: %+v", tr.Meta().Tasks)
	}
	if task.Kind != "agent" || task.Status != "completed" || task.Ended != "2026-11-23T12:00:09Z" {
		t.Errorf("task = %+v", task)
	}
}

func TestBackgroundShellTaskNotification(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"toolu_3","name":"Bash","input":{"command":"sleep 10\nmore","description":"Sleepy","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_3","content":"started"}]},"toolUseResult":{"backgroundTaskId":"bxr271nj3"}}`,
	)
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	task, ok := taskByID(tr.Meta().Tasks, "bxr271nj3")
	if !ok {
		t.Fatalf("shell task missing: %+v", tr.Meta().Tasks)
	}
	if task.Kind != "shell" || task.Status != "running" || task.Title != "Sleepy" || task.Started != "2026-11-23T12:00:00Z" {
		t.Errorf("task = %+v", task)
	}

	appendFile(t, path,
		`{"type":"user","uuid":"n1","timestamp":"2026-11-23T12:00:07Z","message":{"role":"user","content":"<task-notification>\n<task-id>bxr271nj3</task-id>\n<status>failed</status>\n<summary>Background command \"sleep 10\" failed (exit code 1)</summary>\n</task-notification>"}}`)

	_, metaChanged, err := tr.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !metaChanged {
		t.Errorf("metaChanged should be true after the notification")
	}
	task, _ = taskByID(tr.Meta().Tasks, "bxr271nj3")
	if task.Status != "failed" || task.Ended != "2026-11-23T12:00:07Z" || !strings.Contains(task.Summary, "exit code 1") {
		t.Errorf("notified task = %+v", task)
	}
}

func TestTaskStopMarksKilled(t *testing.T) {
	path := writeFile(t,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-11-23T12:00:00Z","message":{"id":"m1","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"long","description":"Long run","run_in_background":true}}]}}`,
		`{"type":"user","uuid":"r1","timestamp":"2026-11-23T12:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"started"}]},"toolUseResult":{"backgroundTaskId":"bogk0ac0c"}}`,
	)
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}

	appendFile(t, path,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-11-23T12:00:02Z","message":{"id":"m2","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t2","name":"TaskStop","input":{"task_id":"bogk0ac0c"}}]}}`,
		`{"type":"user","uuid":"r2","timestamp":"2026-11-23T12:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"stopped"}]}}`)

	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	task, ok := taskByID(tr.Meta().Tasks, "bogk0ac0c")
	if !ok {
		t.Fatalf("shell task missing: %+v", tr.Meta().Tasks)
	}
	if task.Status != "killed" || task.Ended != "2026-11-23T12:00:03Z" {
		t.Errorf("task = %+v", task)
	}
}

func TestUnknownNotificationCreatesOtherTask(t *testing.T) {
	path := writeFile(t,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-23T12:00:05Z","content":"<task-notification>\n<task-id>ghost</task-id>\n<status>completed</status>\n<summary>Something finished</summary>\n</task-notification>"}`)
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	task, ok := taskByID(tr.Meta().Tasks, "ghost")
	if !ok {
		t.Fatalf("other task missing: %+v", tr.Meta().Tasks)
	}
	if task.Kind != "other" || task.Status != "completed" || task.Title != "Something finished" {
		t.Errorf("task = %+v", task)
	}
	if n := tr.Meta().UnknownLines; n != 0 {
		t.Errorf("unknown lines = %d, want 0", n)
	}
}

func TestWaitingNotificationKeepsRunning(t *testing.T) {
	path := writeFile(t, agentToolUse, agentResult)
	tr := Open(path, "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	appendFile(t, path,
		`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-11-23T12:00:05Z","content":"<task-notification>\n<task-id>a56a9a94b0e1dbb18</task-id>\n<status>completed</status>\n<summary>Agent \"x\" is waiting on its own background work</summary>\n</task-notification>"}`)
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	task, _ := taskByID(tr.Meta().Tasks, "a56a9a94b0e1dbb18")
	if task.Status != "running" || task.Summary != "" {
		t.Errorf("task = %+v", task)
	}
}

func TestTaskCapKeepsNewest(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines,
			fmt.Sprintf(`{"type":"assistant","uuid":"a%d","timestamp":"2026-11-23T12:00:%02dZ","message":{"id":"m%d","role":"assistant","model":"m","content":[{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"cmd %d","run_in_background":true}}]}}`, i, i%60, i, i, i),
			fmt.Sprintf(`{"type":"user","uuid":"r%d","timestamp":"2026-11-23T12:00:%02dZ","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t%d","content":"ok"}]},"toolUseResult":{"backgroundTaskId":"bg%d"}}`, i, i%60, i, i),
		)
	}
	tr := Open(writeFile(t, lines...), "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	tasks := tr.Meta().Tasks
	if len(tasks) != maxTasks {
		t.Fatalf("tasks = %d, want %d", len(tasks), maxTasks)
	}
	if tasks[0].ID != "bg10" || tasks[len(tasks)-1].ID != "bg59" {
		t.Errorf("kept %s..%s, want bg10..bg59", tasks[0].ID, tasks[len(tasks)-1].ID)
	}
}

func TestOpenSubagentReadsSidechain(t *testing.T) {
	path := writeFile(t,
		`{"type":"user","uuid":"s1","isSidechain":true,"timestamp":"2026-11-23T12:00:00Z","message":{"role":"user","content":"side prompt"}}`,
		`{"type":"assistant","uuid":"s2","isSidechain":true,"timestamp":"2026-11-23T12:00:01Z","message":{"id":"m","role":"assistant","model":"m","content":[{"type":"text","text":"side text"}]}}`,
	)
	main := Open(path, "/work")
	items, _, _ := main.Poll()
	if len(items) != 0 {
		t.Fatalf("Open items = %v, want none", idsOf(items))
	}

	sub := OpenSubagent(path, "/work")
	items, _, _ = sub.Poll()
	if got := idsOf(items); !equalStrings(got, []string{"s1", "s2:0"}) {
		t.Fatalf("OpenSubagent items = %v", got)
	}
}

func TestSubagentPaths(t *testing.T) {
	jsonl, meta := SubagentPaths("/home/u/.claude/projects/-p/3f2a.jsonl", "abc")
	wantJSONL := "/home/u/.claude/projects/-p/3f2a/subagents/agent-abc.jsonl"
	wantMeta := "/home/u/.claude/projects/-p/3f2a/subagents/agent-abc.meta.json"
	if jsonl != wantJSONL || meta != wantMeta {
		t.Errorf("paths = %q %q", jsonl, meta)
	}
	for _, bad := range []string{"", "bad/id", "a b", strings.Repeat("a", 65)} {
		if j, m := SubagentPaths("/x/y.jsonl", bad); j != "" || m != "" {
			t.Errorf("SubagentPaths(%q) = %q %q, want empty", bad, j, m)
		}
	}
	if !ValidAgentID("A_b-9") || ValidAgentID("no/dot") {
		t.Errorf("ValidAgentID misclassifies")
	}
}

func TestReadSubagentMeta(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-abc.meta.json")
	if err := os.WriteFile(path, []byte(`{"agentType":"dsh-coder","description":"reader","toolUseId":"toolu_1","model":"claude-haiku-5-5","finished":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	meta, ok := ReadSubagentMeta(path)
	if !ok || meta.AgentType != "dsh-coder" || meta.Model != "claude-haiku-5-5" || !meta.Finished {
		t.Errorf("meta = %+v ok = %v", meta, ok)
	}
	if _, ok := ReadSubagentMeta(filepath.Join(t.TempDir(), "missing.json")); ok {
		t.Errorf("missing meta should not be ok")
	}
}

func TestMetaTasksCopied(t *testing.T) {
	tr := Open(writeFile(t, agentToolUse, agentResult), "/work")
	if _, _, err := tr.Poll(); err != nil {
		t.Fatal(err)
	}
	m := tr.Meta()
	m.Tasks[0].Status = "mutated"
	if got := tr.Meta().Tasks[0].Status; got != "running" {
		t.Errorf("Meta() tasks alias the reader: status = %q", got)
	}
}
