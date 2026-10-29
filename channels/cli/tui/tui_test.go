package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

func TestSplitCompleteBlocks(t *testing.T) {
	blocks, rest := splitCompleteBlocks("satu\n\ndua\n\nkode:\n```go\nbaris\n\nbaris\n```\n")
	if len(blocks) != 3 {
		t.Fatalf("blok = %d, mau 3: %#v", len(blocks), blocks)
	}
	if blocks[0] != "satu" || blocks[1] != "dua" {
		t.Fatalf("blok salah: %#v", blocks)
	}
	if !strings.Contains(blocks[2], "baris\n\nbaris") {
		t.Fatalf("blok kode terpotong: %#v", blocks[2])
	}
	if rest != "" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestSplitCompleteBlocksLeavesPartial(t *testing.T) {
	blocks, rest := splitCompleteBlocks("satu\n\ndua")
	if len(blocks) != 1 || blocks[0] != "satu" {
		t.Fatalf("blok = %#v", blocks)
	}
	if rest != "dua" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestRenderMarkdown(t *testing.T) {
	lines := renderMarkdownLines("## Judul\n\n- satu **tebal**\n\n```go\nfmt.Println(\"x\")\n```", 60)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Judul", "• satu", "tebal", "│ fmt.Println"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("hasil tidak memuat %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "**") {
		t.Fatalf("penanda bold masih tampil:\n%s", joined)
	}
}

func TestToolBlock(t *testing.T) {
	record := &ToolRecord{
		CallID:  "c1",
		Name:    "bash",
		Input:   []byte("{\"command\":\"go test ./...\"}"),
		Status:  core.ToolStatusSuccess,
		Output:  "ok",
		Started: time.Now().Add(-300 * time.Millisecond),
		Done:    time.Now(),
	}
	block := strings.Join(record.Block(false, 80), "\n")
	if !strings.Contains(block, "bash  go test ./...") {
		t.Fatalf("baris ringkas salah:\n%s", block)
	}
	if !strings.Contains(block, "✓ sukses") {
		t.Fatalf("status salah:\n%s", block)
	}
	verbose := strings.Join(record.Block(true, 80), "\n")
	if !strings.Contains(verbose, "input:") || !strings.Contains(verbose, "output:") {
		t.Fatalf("verbose kurang:\n%s", verbose)
	}
}

func TestPrimaryArg(t *testing.T) {
	if got := primaryArg("bash", []byte("{\"command\":\"cargo test\"}")); got != "cargo test" {
		t.Fatalf("bash = %q", got)
	}
	if got := primaryArg("read_file", []byte("{\"path\":\"src/main.go\"}")); got != "src/main.go" {
		t.Fatalf("read_file = %q", got)
	}
	if got := primaryArg("tidak_dikenal", []byte("{\"a\":1}")); got != "{\"a\":1}" {
		t.Fatalf("default = %q", got)
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(data)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type fakeBackend struct {
	obs *core.Observe[core.LiveFrame]
}

func (f *fakeBackend) CreateSession(context.Context, string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S2", Agent: "default"}, nil
}

func (f *fakeBackend) LastSession(context.Context) (core.SessionInfo, error) {
	return core.SessionInfo{}, errors.New("tidak ada")
}

func (f *fakeBackend) RunTurn(_ context.Context, sessionID, _ string) (TurnResult, error) {
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "text_delta", Text: "Halo dari fake."})
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "tool_call", CallID: "c1", Name: "bash", Input: []byte("{\"command\":\"echo hi\"}")})
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "tool_result", CallID: "c1", Name: "bash", Status: "success", Output: "hi"})
	return TurnResult{Text: "Halo dari fake.", StopReason: "end_turn", InputTokens: 12, OutputTokens: 7}, nil
}

func (f *fakeBackend) Send(context.Context, string, string) error { return nil }
func (f *fakeBackend) Cancel(context.Context, string) error       { return nil }
func (f *fakeBackend) SubscribeLive(context.Context, string) (*core.Subscription[core.LiveFrame], error) {
	return f.obs.Subscribe(), nil
}
func (f *fakeBackend) Policy(context.Context, string) (core.Policy, error) {
	return core.PolicyAsk, nil
}
func (f *fakeBackend) SetPolicy(context.Context, string, core.Policy) error { return nil }
func (f *fakeBackend) History(context.Context, string) ([]core.SessionEvent, error) {
	return nil, nil
}
func (f *fakeBackend) SessionInfo(_ context.Context, id string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: id, Agent: "default"}, nil
}
func (f *fakeBackend) ListSessions(context.Context, int) ([]core.SessionInfo, error) {
	return []core.SessionInfo{{ID: "S1", Agent: "default"}, {ID: "S2", Agent: "asisten", TurnCount: 2}}, nil
}
func (f *fakeBackend) ForkSession(context.Context, string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S9", Agent: "default"}, nil
}
func (f *fakeBackend) SetAgent(context.Context, string, string) error { return nil }
func (f *fakeBackend) SetModel(context.Context, string, string) error { return nil }
func (f *fakeBackend) Compact(context.Context, string) (CompactionInfo, error) {
	return CompactionInfo{TokensBefore: 100, TokensAfter: 40}, nil
}
func (f *fakeBackend) AgentNames() []string      { return []string{"default", "asisten"} }
func (f *fakeBackend) AgentModel(string) string  { return "mock:mock" }
func (f *fakeBackend) AgentPolicy(string) string { return "ask" }

func TestProgramFlow(t *testing.T) {
	observer := core.NewObserve[core.LiveFrame]()
	backend := &fakeBackend{obs: observer}
	reader, writer := io.Pipe()
	var out syncBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Backend: backend, Agent: "default", Model: "mock:mock", Policy: "ask",
			SessionID: "S1", Cwd: "/tmp", Version: "test", Home: t.TempDir(),
			In: reader, Out: &out,
		})
	}()
	if _, err := writer.Write([]byte("halo\r")); err != nil {
		t.Fatalf("kirim input: %v", err)
	}
	waitFor(t, &out, "Halo dari fake.")
	full := out.String()
	if !strings.Contains(full, "⏺ bash  echo hi") {
		t.Fatalf("tool call tidak tampil:\n%s", full)
	}
	if !strings.Contains(full, "✓ sukses") {
		t.Fatalf("status tool tidak tampil:\n%s", full)
	}
	if _, err := writer.Write([]byte("/agent asisten\r")); err != nil {
		t.Fatalf("kirim /agent: %v", err)
	}
	waitFor(t, &out, "· agent: asisten")
	if _, err := writer.Write([]byte("/model deepseek:deepseek-chat\r")); err != nil {
		t.Fatalf("kirim /model: %v", err)
	}
	waitFor(t, &out, "· model: deepseek:deepseek-chat")
	if _, err := writer.Write([]byte("/compact\r")); err != nil {
		t.Fatalf("kirim /compact: %v", err)
	}
	waitFor(t, &out, "· compaction 100 → 40 token")
	if _, err := writer.Write([]byte("/fork\r")); err != nil {
		t.Fatalf("kirim /fork: %v", err)
	}
	waitFor(t, &out, "· fork S9")
	if _, err := writer.Write([]byte("/sessions\r")); err != nil {
		t.Fatalf("kirim /sessions: %v", err)
	}
	waitFor(t, &out, "? pilih session")
	if _, err := writer.Write([]byte("\r")); err != nil {
		t.Fatalf("pilih session: %v", err)
	}
	waitFor(t, &out, "· pindah ke session S1")
	if _, err := writer.Write([]byte("/exit\r")); err != nil {
		t.Fatalf("kirim exit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program tidak selesai")
	}
	if !strings.Contains(out.String(), "lana") {
		t.Fatalf("banner tidak tampil:\n%s", out.String())
	}
}

func waitFor(t *testing.T, buffer *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buffer.String(), want) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("tidak menemukan %q; keluaran:\n%s", want, buffer.String())
}

func TestApproverWithoutProgram(t *testing.T) {
	approver := NewApprover()
	outcome := approver.Ask(context.Background(), core.ApprovalRequest{ToolName: "bash"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome = %q", outcome)
	}
}
