package tui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

func TestSplitCompleteBlocks(t *testing.T) {
	blocks, rest := splitCompleteBlocks("one\n\ntwo\n\ncode:\n```go\nline\n\nline\n```\n")
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3: %#v", len(blocks), blocks)
	}
	if blocks[0] != "one" || blocks[1] != "two" {
		t.Fatalf("wrong blocks: %#v", blocks)
	}
	if !strings.Contains(blocks[2], "line\n\nline") {
		t.Fatalf("code block truncated: %#v", blocks[2])
	}
	if rest != "" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestSplitCompleteBlocksLeavesPartial(t *testing.T) {
	blocks, rest := splitCompleteBlocks("one\n\ntwo")
	if len(blocks) != 1 || blocks[0] != "one" {
		t.Fatalf("blocks = %#v", blocks)
	}
	if rest != "two" {
		t.Fatalf("rest = %q", rest)
	}
}

func TestRenderMarkdown(t *testing.T) {
	lines := renderMarkdownLines("## Title\n\n- one **bold**\n\n```go\nfmt.Println(\"x\")\n```", 60)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"Title", "• one", "bold", "│ fmt.Println"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("output does not contain %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "**") {
		t.Fatalf("bold marker still shown:\n%s", joined)
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
		t.Fatalf("compact line wrong:\n%s", block)
	}
	if !strings.Contains(block, "✓ success") {
		t.Fatalf("wrong status:\n%s", block)
	}
	verbose := strings.Join(record.Block(true, 80), "\n")
	if !strings.Contains(verbose, "input:") || !strings.Contains(verbose, "output:") {
		t.Fatalf("verbose output incomplete:\n%s", verbose)
	}
}

func TestToolSummaryAskUser(t *testing.T) {
	record := &ToolRecord{
		Name:  "ask_user",
		Input: []byte(`{"questions":[{"question":"One?"},{"question":"Two?"}]}`),
	}
	if got := record.Summary(); got != "ask_user · 2 questions" {
		t.Fatalf("summary = %q", got)
	}
	record.Agent = "researcher"
	if got := record.Summary(); got != "researcher · ask_user · 2 questions" {
		t.Fatalf("agent summary = %q", got)
	}
	record.Agent = ""
	record.Input = []byte(`{"questions":[]}`)
	if got := record.Summary(); got != "ask_user" {
		t.Fatalf("summary without questions = %q", got)
	}
}

func TestPrimaryArg(t *testing.T) {
	if got := primaryArg("bash", []byte("{\"command\":\"cargo test\"}")); got != "cargo test" {
		t.Fatalf("bash = %q", got)
	}
	if got := primaryArg("read_file", []byte("{\"path\":\"src/main.go\"}")); got != "src/main.go" {
		t.Fatalf("read_file = %q", got)
	}
	if got := primaryArg("unknown_tool", []byte("{\"a\":1}")); got != "{\"a\":1}" {
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
	return core.SessionInfo{}, errors.New("no session")
}

func (f *fakeBackend) RunTurn(_ context.Context, sessionID, _ string) (TurnResult, error) {
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "text_delta", Text: "Hello from fake."})
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "tool_call", CallID: "c1", Name: "bash", Input: []byte("{\"command\":\"echo hi\"}")})
	f.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "tool_result", CallID: "c1", Name: "bash", Status: "success", Output: "hi"})
	return TurnResult{Text: "Hello from fake.", StopReason: "end_turn", InputTokens: 12, OutputTokens: 7}, nil
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
	return []core.SessionInfo{{ID: "S1", Agent: "default"}, {ID: "S2", Agent: "assistant", TurnCount: 2}}, nil
}
func (f *fakeBackend) ForkSession(context.Context, string) (core.SessionInfo, error) {
	return core.SessionInfo{ID: "S9", Agent: "default"}, nil
}
func (f *fakeBackend) SetAgent(context.Context, string, string) error { return nil }
func (f *fakeBackend) SetModel(context.Context, string, string) error { return nil }
func (f *fakeBackend) Compact(context.Context, string) (CompactionInfo, error) {
	return CompactionInfo{TokensBefore: 100, TokensAfter: 40}, nil
}
func (f *fakeBackend) AgentNames() []string      { return []string{"default", "assistant"} }
func (f *fakeBackend) AgentModel(string) string  { return "mock:mock" }
func (f *fakeBackend) AgentPolicy(string) string { return "ask" }
func (f *fakeBackend) Memory(string) ([]string, []string, error) {
	return []string{"Name: Ridho"}, nil, nil
}
func (f *fakeBackend) MCPStatus(string) []core.MCPServerStatus {
	return []core.MCPServerStatus{{Name: "github", State: "running", Tools: 26}}
}
func (f *fakeBackend) Skills(string) []agenthome.Skill {
	return []agenthome.Skill{{Name: "demo", Scope: "global", Description: "Demo skill"}}
}

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
	if _, err := writer.Write([]byte("hello\r")); err != nil {
		t.Fatalf("send input: %v", err)
	}
	waitFor(t, &out, "Hello from fake.")
	full := out.String()
	if !strings.Contains(full, "⏺ bash  echo hi") {
		t.Fatalf("tool call not shown:\n%s", full)
	}
	if !strings.Contains(full, "✓ success") {
		t.Fatalf("tool status not shown:\n%s", full)
	}
	if _, err := writer.Write([]byte("/agent assistant\r")); err != nil {
		t.Fatalf("send /agent: %v", err)
	}
	waitFor(t, &out, "· agent: assistant")
	if _, err := writer.Write([]byte("/model deepseek:deepseek-chat\r")); err != nil {
		t.Fatalf("send /model: %v", err)
	}
	waitFor(t, &out, "· model: deepseek:deepseek-chat")
	if _, err := writer.Write([]byte("/compact\r")); err != nil {
		t.Fatalf("send /compact: %v", err)
	}
	waitFor(t, &out, "· compaction 100 → 40 tokens")
	if _, err := writer.Write([]byte("/fork\r")); err != nil {
		t.Fatalf("send /fork: %v", err)
	}
	waitFor(t, &out, "· fork S9")
	if _, err := writer.Write([]byte("/sessions\r")); err != nil {
		t.Fatalf("send /sessions: %v", err)
	}
	waitFor(t, &out, "? pick session")
	if _, err := writer.Write([]byte("\r")); err != nil {
		t.Fatalf("pick session: %v", err)
	}
	waitFor(t, &out, "· switched to session S1")
	if _, err := writer.Write([]byte("/exit\r")); err != nil {
		t.Fatalf("send exit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not finish")
	}
	if !strings.Contains(out.String(), "lana") {
		t.Fatalf("banner not shown:\n%s", out.String())
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
	t.Fatalf("did not find %q; output:\n%s", want, buffer.String())
}

// loopBackend ends its turn with the loop stop reason.
type loopBackend struct{ *fakeBackend }

func (b *loopBackend) RunTurn(_ context.Context, sessionID, _ string) (TurnResult, error) {
	b.obs.Publish(core.LiveFrame{SessionID: sessionID, Kind: "text_delta", Text: "here is my report."})
	return TurnResult{Text: "here is my report.", StopReason: "loop"}, nil
}

// REQ-LPG-010: a loop stop prints its own line, styled like the max_steps one.
func TestLoopStopLine(t *testing.T) {
	observer := core.NewObserve[core.LiveFrame]()
	backend := &loopBackend{&fakeBackend{obs: observer}}
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
	if _, err := writer.Write([]byte("hello\r")); err != nil {
		t.Fatalf("send input: %v", err)
	}
	waitFor(t, &out, "⚠ turn stopped: repeated a step without progress")
	if _, err := writer.Write([]byte("/exit\r")); err != nil {
		t.Fatalf("send exit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not finish")
	}
}

func TestApproverWithoutProgram(t *testing.T) {
	approver := NewApprover()
	outcome := approver.Ask(context.Background(), core.ApprovalRequest{ToolName: "bash"})
	if outcome != core.OutcomeUnavailable {
		t.Fatalf("outcome = %q", outcome)
	}
}

func TestInteractiveSlashAndMenus(t *testing.T) {
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
	send := func(s string) {
		t.Helper()
		if _, err := writer.Write([]byte(s)); err != nil {
			t.Fatalf("send %q: %v", s, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Prefix "/comp" is completed to /compact on enter.
	send("/comp")
	waitFor(t, &out, "compact session history")
	send("\r")
	waitFor(t, &out, "· compaction 100 → 40 tokens")
	// /permission without arguments opens a menu; the number 2 selects trust.
	send("/permission\r")
	waitFor(t, &out, "? pick policy")
	send("2")
	waitFor(t, &out, "· policy trust")
	// /agent without arguments opens a menu; down arrow then enter selects assistant.
	send("/agent\r")
	waitFor(t, &out, "? pick agent")
	send("\x1b[B")
	send("\r")
	waitFor(t, &out, "· agent: assistant")
	send("/memory\r")
	waitFor(t, &out, "· user 11/2000 characters, 1 entries")
	waitFor(t, &out, "1. Name: Ridho")
	waitFor(t, &out, "· memory 0/32000 characters, 0 entries")
	send("/mcp\r")
	waitFor(t, &out, "· github running, 26 tools")
	send("/skills\r")
	waitFor(t, &out, "· demo (global): Demo skill")
	send("/exit\r")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not finish")
	}
}

func TestApprovalMenuKeys(t *testing.T) {
	model := NewModel(context.Background(), Options{Backend: &fakeBackend{obs: core.NewObserve[core.LiveFrame]()}})
	reply := make(chan core.ApprovalOutcome, 1)
	model.Update(approvalMsg{req: core.ApprovalRequest{CallID: "c1", ToolName: "bash"}, reply: reply})
	if !strings.Contains(model.renderApproval(), "1. Yes, once") {
		t.Fatalf("approval choices not shown:\n%s", model.renderApproval())
	}
	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := <-reply; got != core.OutcomeAllowedSession {
		t.Fatalf("outcome = %q", got)
	}
	if model.approval != nil {
		t.Fatal("approval was not closed")
	}
}

// TestRestoreLinesHidesRuntimeNotes covers REQ-MEM-021: a restored user/message
// with runtime:true is not shown, while an owner message is.
func TestRestoreLinesHidesRuntimeNotes(t *testing.T) {
	userEvent := func(seq int, text string, runtime bool) core.SessionEvent {
		payload, err := json.Marshal(core.UserMessage{
			V:       1,
			Runtime: runtime,
			Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return core.SessionEvent{Seq: int64(seq), Kind: core.KindUserMessage, Payload: payload}
	}
	model := NewModel(context.Background(), Options{
		Backend:   &fakeBackend{obs: core.NewObserve[core.LiveFrame]()},
		SessionID: "s1",
		Restore: []core.SessionEvent{
			userEvent(1, "hello owner", false),
			userEvent(2, "runtime nudge", true),
		},
	})

	joined := strings.Join(model.restoreLines(), "\n")
	if !strings.Contains(joined, "hello owner") {
		t.Fatalf("owner message missing:\n%s", joined)
	}
	if strings.Contains(joined, "runtime nudge") {
		t.Fatalf("runtime note was shown:\n%s", joined)
	}
}

func TestHistorySearchBefore(t *testing.T) {
	history := LoadHistory(t.TempDir()+"/history", 10)
	history.Add("go test ./...")
	history.Add("hello")
	history.Add("go build")
	match, index, ok := history.SearchBefore("go", history.Len())
	if !ok || match != "go build" || index != 2 {
		t.Fatalf("first = %q %d %v", match, index, ok)
	}
	match, _, ok = history.SearchBefore("go", index)
	if !ok || match != "go test ./..." {
		t.Fatalf("second = %q %v", match, ok)
	}
}

// questionBackend runs a turn that asks one question through the TUI asker.
type questionBackend struct {
	*fakeBackend
	asker *Asker
	req   core.QuestionRequest
}

func (b *questionBackend) RunTurn(ctx context.Context, _, _ string) (TurnResult, error) {
	b.asker.Ask(ctx, b.req)
	return TurnResult{Text: "answered", StopReason: "end_turn"}, nil
}

func newQuestionModel(t *testing.T, questions ...core.Question) (*Model, chan core.QuestionOutcome) {
	t.Helper()
	model := NewModel(context.Background(), Options{Backend: &fakeBackend{obs: core.NewObserve[core.LiveFrame]()}})
	reply := make(chan core.QuestionOutcome, 1)
	model.Update(questionMsg{req: core.QuestionRequest{RequestID: "q1", CallID: "c1", Questions: questions}, reply: reply})
	return model, reply
}

func questionKey(value string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}

// AC-ASK-031: pressing 2 records option 2 and the transcript shows the answer.
func TestQuestionAnsweredInTranscript(t *testing.T) {
	asker := NewAsker()
	backend := &questionBackend{
		fakeBackend: &fakeBackend{obs: core.NewObserve[core.LiveFrame]()},
		asker:       asker,
		req: core.QuestionRequest{RequestID: "q1", CallID: "c1", Questions: []core.Question{{
			Text:    "Where should I save the weekly digest?",
			Header:  "Digest",
			Options: []core.QuestionOption{{Label: "option 1"}, {Label: "option 2"}},
		}}},
	}
	reader, writer := io.Pipe()
	var out syncBuffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Backend: backend, Asker: asker, Agent: "default", Model: "mock:mock", Policy: "ask",
			SessionID: "S1", Cwd: "/tmp", Version: "test", Home: t.TempDir(),
			In: reader, Out: &out,
		})
	}()
	if _, err := writer.Write([]byte("hello\r")); err != nil {
		t.Fatalf("send prompt: %v", err)
	}
	waitFor(t, &out, "? [Digest] Where should I save the weekly digest?  (1 of 1)")
	if _, err := writer.Write([]byte("2")); err != nil {
		t.Fatalf("send option: %v", err)
	}
	waitFor(t, &out, "✓ answered: option 2")
	if _, err := writer.Write([]byte("/exit\r")); err != nil {
		t.Fatalf("send exit: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("program did not finish")
	}
}

func TestQuestionSomethingElseInput(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:    "Where?",
		Options: []core.QuestionOption{{Label: "option 1"}, {Label: "option 2"}},
	})
	model.Update(questionKey("3"))
	if model.question == nil || !model.question.other {
		t.Fatal("Something else did not open the input line")
	}
	model.Update(questionKey("draft"))
	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if model.question.other || model.question.input != "" {
		t.Fatalf("esc did not close the input: %+v", model.question)
	}
	model.Update(questionKey("3"))
	for _, r := range "custom wording" {
		model.Update(questionKey(string(r)))
	}
	if !strings.Contains(model.renderQuestion(), "Your answer: custom wording") {
		t.Fatalf("input not shown:\n%s", model.renderQuestion())
	}
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out := <-reply
	if out.Status != core.QuestionStatusAnswered || len(out.Answers) != 1 || out.Answers[0].Other != "custom wording" {
		t.Fatalf("outcome = %+v", out)
	}
	if !strings.Contains(questionOutcomeLine(out), "✓ answered: custom wording") {
		t.Fatalf("transcript line = %q", questionOutcomeLine(out))
	}
}

func TestQuestionSkip(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:    "Where?",
		Options: []core.QuestionOption{{Label: "option 1"}, {Label: "option 2"}},
	})
	model.Update(questionKey("s"))
	out := <-reply
	if out.Status != core.QuestionStatusAnswered || len(out.Answers) != 1 || !out.Answers[0].Skipped {
		t.Fatalf("outcome = %+v", out)
	}
	if model.question != nil {
		t.Fatal("question was not closed")
	}
	if line := questionOutcomeLine(out); !strings.Contains(line, "· question skipped") {
		t.Fatalf("transcript line = %q", line)
	}
}

func TestQuestionMultiSelectToggleAndSubmit(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:        "Which sections?",
		MultiSelect: true,
		Options: []core.QuestionOption{
			{Label: "Key findings"},
			{Label: "Open questions"},
			{Label: "Action items"},
		},
	})
	model.Update(questionKey("1"))
	model.Update(questionKey("3"))
	rendered := model.renderQuestion()
	for _, want := range []string{"[x] 1) Key findings", "[ ] 2) Open questions", "[x] 3) Action items", "s) Skip      Enter) next"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render does not contain %q:\n%s", want, rendered)
		}
	}
	model.Update(questionKey("3"))
	if got := model.renderQuestion(); !strings.Contains(got, "[ ] 3) Action items") {
		t.Fatalf("second press did not untoggle:\n%s", got)
	}
	model.Update(questionKey("3"))
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	out := <-reply
	want := []string{"Key findings", "Action items"}
	if len(out.Answers) != 1 || strings.Join(out.Answers[0].Selected, ",") != strings.Join(want, ",") {
		t.Fatalf("outcome = %+v", out)
	}
	if line := questionOutcomeLine(out); !strings.Contains(line, "✓ answered: Key findings, Action items") {
		t.Fatalf("transcript line = %q", line)
	}
}

func TestQuestionMultiSelectRequiresPick(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:        "Which sections?",
		MultiSelect: true,
		Options:     []core.QuestionOption{{Label: "one"}, {Label: "two"}},
	})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(model.renderQuestion(), "Pick at least one option, or s to skip.") {
		t.Fatalf("hint not shown:\n%s", model.renderQuestion())
	}
	select {
	case out := <-reply:
		t.Fatalf("empty multi-select submitted: %+v", out)
	default:
	}
	if model.question == nil {
		t.Fatal("question closed on an empty multi-select")
	}
}

func TestQuestionCtrlCCancels(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:    "Where?",
		Options: []core.QuestionOption{{Label: "option 1"}, {Label: "option 2"}},
	})
	model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	out := <-reply
	if out.Status != core.QuestionStatusUnavailable || out.Reason != core.QuestionReasonCancelled {
		t.Fatalf("outcome = %+v", out)
	}
	if model.question != nil {
		t.Fatal("question was not closed")
	}
	if line := questionOutcomeLine(out); !strings.Contains(line, "· question cancelled") {
		t.Fatalf("transcript line = %q", line)
	}
}

func TestQuestionNumberOutOfRange(t *testing.T) {
	model, reply := newQuestionModel(t, core.Question{
		Text:    "Where?",
		Options: []core.QuestionOption{{Label: "option 1"}, {Label: "option 2"}},
	})
	model.Update(questionKey("9"))
	select {
	case out := <-reply:
		t.Fatalf("out-of-range number answered: %+v", out)
	default:
	}
	if model.question == nil {
		t.Fatal("question closed on an out-of-range number")
	}
}

func TestQuestionAskerWithoutProgram(t *testing.T) {
	outcome := NewAsker().Ask(context.Background(), core.QuestionRequest{
		Questions: []core.Question{{Text: "Where?"}},
	})
	if outcome.Status != core.QuestionStatusUnavailable || outcome.Reason != core.QuestionReasonNoResponder {
		t.Fatalf("outcome = %+v", outcome)
	}
}
