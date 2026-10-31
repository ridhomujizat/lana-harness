package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/spill"
	"github.com/ridhomujizat/lana-harness/tools"
)

// newTaskToolRunner builds a ToolRunner with the real task tool and the
// default tool pipeline.
func newTaskToolRunner(t *testing.T) (*ToolRunner, *core.Hooks) {
	t.Helper()
	hooks := core.NewHooks()
	tools.NewPipeline(hooks)
	registry := tools.NewRegistry()
	if err := registry.Register(tools.NewTask()); err != nil {
		t.Fatal(err)
	}
	return NewToolRunner(hooks, registry), hooks
}

func receiveCallID(t *testing.T, ch <-chan string, what string) string {
	t.Helper()
	select {
	case callID := <-ch:
		return callID
	case <-time.After(3 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
		return ""
	}
}

// barrierSubagents blocks every call until release is closed, so a test can
// observe how many calls run at the same time.
type barrierSubagents struct {
	started chan string
	release chan struct{}
}

var _ core.SubagentRunner = (*barrierSubagents)(nil)

func (b *barrierSubagents) Spawn(ctx context.Context, req core.SpawnRequest) (core.ToolOutput, error) {
	select {
	case b.started <- req.ParentCallID:
	case <-ctx.Done():
		return core.ToolOutput{Status: core.ToolStatusError, Output: "cancelled"}, nil
	}
	select {
	case <-b.release:
	case <-ctx.Done():
		return core.ToolOutput{Status: core.ToolStatusError, Output: "cancelled"}, nil
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func (b *barrierSubagents) Continue(ctx context.Context, req core.ContinueRequest) (core.ToolOutput, error) {
	select {
	case <-ctx.Done():
		return core.ToolOutput{Status: core.ToolStatusError, Output: "cancelled"}, nil
	default:
		return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
	}
}

// TestTaskCallsBatchInParallel covers AC-TEAM-029: two task calls in one step
// both start before either one ends.
func TestTaskCallsBatchInParallel(t *testing.T) {
	tr, hooks := newTaskToolRunner(t)
	hooks.ToolsPreExecute.SetDefault(func(context.Context, core.ToolsPreExecuteInput) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	runner := &barrierSubagents{started: make(chan string, 2), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(runner.release) }) }
	defer release()

	calls := []core.ToolCall{
		{CallID: "call_a", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"first"}`)},
		{CallID: "call_b", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"second"}`)},
	}
	done := make(chan []core.ToolOutput, 1)
	go func() {
		done <- tr.Run(context.Background(), calls, []string{"task"}, core.ToolCtx{SessionID: "parent", Subagents: runner})
	}()

	started := map[string]bool{}
	for len(started) < len(calls) {
		started[receiveCallID(t, runner.started, "both task calls to start")] = true
	}
	release()

	var outputs []core.ToolOutput
	select {
	case outputs = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not finish")
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs=%+v", outputs)
	}
	for i, output := range outputs {
		if output.Status != core.ToolStatusSuccess {
			t.Fatalf("outputs[%d]=%+v", i, output)
		}
	}
}

// approvalRecordingSubagents records whether a task call reached Spawn after
// its own approval.
type approvalRecordingSubagents struct {
	mu         sync.Mutex
	approved   map[string]bool
	unapproved atomic.Int32
}

var _ core.SubagentRunner = (*approvalRecordingSubagents)(nil)

func (a *approvalRecordingSubagents) Spawn(_ context.Context, req core.SpawnRequest) (core.ToolOutput, error) {
	a.mu.Lock()
	ok := a.approved[req.ParentCallID]
	a.mu.Unlock()
	if !ok {
		a.unapproved.Add(1)
	}
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

func (a *approvalRecordingSubagents) Continue(context.Context, core.ContinueRequest) (core.ToolOutput, error) {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: "ok"}, nil
}

// TestTaskApprovalsSequential covers AC-TEAM-030: approval prompts for the
// calls of one step are requested one after another, and each child starts
// after its own approval.
func TestTaskApprovalsSequential(t *testing.T) {
	tr, hooks := newTaskToolRunner(t)
	runner := &approvalRecordingSubagents{approved: map[string]bool{}}
	asked := make(chan string, 2)
	gate := make(chan struct{}, 1)
	var active, maxActive atomic.Int32
	hooks.ToolsPreExecute.Use(func(ctx context.Context, in core.ToolsPreExecuteInput, next core.Next[core.ToolsPreExecuteInput, core.ToolDecision]) (core.ToolDecision, error) {
		current := active.Add(1)
		for {
			old := maxActive.Load()
			if current <= old || maxActive.CompareAndSwap(old, current) {
				break
			}
		}
		asked <- in.Call.CallID
		select {
		case <-gate:
		case <-ctx.Done():
			active.Add(-1)
			return core.ToolDecision{}, ctx.Err()
		}
		active.Add(-1)
		runner.mu.Lock()
		runner.approved[in.Call.CallID] = true
		runner.mu.Unlock()
		return core.ToolDecision{Allowed: true}, nil
	})

	calls := []core.ToolCall{
		{CallID: "call_a", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"first"}`)},
		{CallID: "call_b", Name: "task", Input: json.RawMessage(`{"agent":"explorer","prompt":"second"}`)},
	}
	done := make(chan []core.ToolOutput, 1)
	go func() {
		done <- tr.Run(context.Background(), calls, []string{"task"}, core.ToolCtx{SessionID: "parent", Subagents: runner})
	}()

	first := receiveCallID(t, asked, "the first approval")
	select {
	case second := <-asked:
		t.Fatalf("approval for %s was requested while %s was still pending", second, first)
	case <-time.After(100 * time.Millisecond):
	}
	gate <- struct{}{}
	second := receiveCallID(t, asked, "the second approval")
	if second == first {
		t.Fatalf("the same call %s was approved twice", first)
	}
	if got := maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent approvals=%d want 1", got)
	}
	gate <- struct{}{}

	var outputs []core.ToolOutput
	select {
	case outputs = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not finish")
	}
	if len(outputs) != 2 {
		t.Fatalf("outputs=%+v", outputs)
	}
	for i, output := range outputs {
		if output.Status != core.ToolStatusSuccess {
			t.Fatalf("outputs[%d]=%+v", i, output)
		}
	}
	if got := runner.unapproved.Load(); got != 0 {
		t.Fatalf("%d task calls started before their approval", got)
	}
}

// newSpillToolRunner builds a ToolRunner with the real bash and read_file tools
// and allows every call, so a test can drive the spill policy.
func newSpillToolRunner(t *testing.T) *ToolRunner {
	t.Helper()
	hooks := core.NewHooks()
	tools.NewPipeline(hooks)
	registry := tools.NewRegistry()
	for _, tool := range []core.Tool{tools.NewBash(&tools.Guard{}), tools.NewReadFile(&tools.Guard{})} {
		if err := registry.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	hooks.ToolsPreExecute.SetDefault(func(context.Context, core.ToolsPreExecuteInput) (core.ToolDecision, error) {
		return core.ToolDecision{Allowed: true}, nil
	})
	return NewToolRunner(hooks, registry)
}

// spillPathFromNotice extracts the saved-file path from a spill notice.
func spillPathFromNotice(t *testing.T, out string) string {
	t.Helper()
	const marker = "Full output stored at: "
	i := strings.Index(out, marker)
	if i < 0 {
		t.Fatalf("notice missing: %q", out)
	}
	rest := out[i+len(marker):]
	j := strings.Index(rest, ". Use read_file")
	if j < 0 {
		t.Fatalf("notice path terminator missing: %q", out)
	}
	return rest[:j]
}

func TestBashOutputSpilled(t *testing.T) {
	tr := newSpillToolRunner(t)
	home := t.TempDir()
	ws := t.TempDir()
	calls := []core.ToolCall{{CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"yes a | head -c 100000"}`)}}

	out := tr.Run(context.Background(), calls, []string{"bash"}, core.ToolCtx{SessionID: "s1", Home: home, Workspace: ws})[0]
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if got := utf8.RuneCountInString(out.Output); got > 16000 {
		t.Fatalf("inline runes=%d want <= 16000", got)
	}
	if !strings.Contains(out.Output, "(Omitted ") || !strings.Contains(out.Output, "Full output stored at: ") {
		t.Fatalf("notice missing: %q", out.Output)
	}

	path := spillPathFromNotice(t, out.Output)
	wantDir := spill.SessionDir(filepath.Join(home, "spill"), "s1")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("path=%q want dir %q", path, wantDir)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 100000 {
		t.Fatalf("spilled bytes=%d want 100000", len(data))
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("file mode=%o want 600", got)
	}
	dirInfo, err := os.Stat(wantDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode=%o want 700", got)
	}
}

// failingStore makes the save path report a storage failure.
type failingStore struct{}

func (failingStore) Save(string, string, string, string) (string, error) {
	return "", errors.New("disk full")
}

func TestSpillSaveFailureKeepsOutput(t *testing.T) {
	tr := newSpillToolRunner(t)
	tr.store = failingStore{}
	ws := t.TempDir()
	calls := []core.ToolCall{{CallID: "c1", Name: "bash", Input: json.RawMessage(`{"command":"yes a | head -c 100000"}`)}}

	out := tr.Run(context.Background(), calls, []string{"bash"}, core.ToolCtx{SessionID: "s1", Home: t.TempDir(), Workspace: ws})[0]
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if strings.Contains(out.Output, "Full output stored at:") {
		t.Fatalf("output was spilled despite the save failure")
	}
	if got := utf8.RuneCountInString(out.Output); got != 100000 {
		t.Fatalf("inline runes=%d want the original 100000", got)
	}
}

func TestReadFileOutputNotSpilled(t *testing.T) {
	tr := newSpillToolRunner(t)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(strings.Repeat("z", 20000)), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := []core.ToolCall{{CallID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"big.txt"}`)}}

	out := tr.Run(context.Background(), calls, []string{"read_file"}, core.ToolCtx{SessionID: "s1", Home: t.TempDir(), Workspace: ws})[0]
	if out.Status != core.ToolStatusSuccess {
		t.Fatalf("out=%+v", out)
	}
	if strings.Contains(out.Output, "Full output stored at:") {
		t.Fatalf("read_file output was spilled")
	}
	if got := utf8.RuneCountInString(out.Output); got != 20000 {
		t.Fatalf("inline runes=%d want 20000", got)
	}
}

func TestSpillPreviewKeepsRunes(t *testing.T) {
	text := strings.Repeat("é", 20000)
	got := spillPreview(text, "/tmp/spill/example.txt")
	if count := utf8.RuneCountInString(got); count > inlineRunes {
		t.Fatalf("runes=%d want <= %d", count, inlineRunes)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("preview split a rune")
	}
}
