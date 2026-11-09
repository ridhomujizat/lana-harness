package workflow

// Tests for the Message node's channel registry and handler (REQ-WF-005).

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fakeMessageNotifier records the plain owner messages a telegram channel
// sends.
type fakeMessageNotifier struct{ sent []string }

func (n *fakeMessageNotifier) Notify(context.Context, Notice) {}

func (n *fakeMessageNotifier) SendOwner(_ context.Context, text string) error {
	n.sent = append(n.sent, text)
	return nil
}

// runMessageNode seeds a manual trigger -> message node graph and runs it,
// returning the settled run and the message node run.
func runMessageNode(t *testing.T, st *MemStore, svc *Service, params map[string]any) (Run, NodeRun) {
	t.Helper()
	ctx := context.Background()
	g := Graph{
		Nodes: []Node{
			wfNode("start", "trigger.manual", "Start", map[string]any{"enabled": true}),
			wfNode("say", "message", "Say", params),
		},
		Edges: []Edge{wfEdge("start", "out", "say")},
	}
	wf := seedWorkflow(t, st, Workflow{ID: "w", Name: "Digest", Guardian: "g"}, g)
	run := seedRun(t, st, wf, "manual", nil)
	svc.engine.Launch(ctx, run.ID)
	svc.engine.Wait()
	return mustRun(t, st, run.ID), mustNode(t, st, run.ID, "say", -1)
}

// MessageChannels lists web first, telegram second, then the rest by id, and
// reports Telegram unavailable without a notifier.
func TestMessageChannelsOrderAndAvailability(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})

	got := svc.MessageChannels()
	if len(got) != 1 || got[0].ID != "telegram" || got[0].Label != "Telegram" {
		t.Fatalf("default channels = %+v", got)
	}
	if got[0].Available || got[0].Reason != "Telegram is not connected" {
		t.Fatalf("telegram availability = %+v", got[0])
	}

	svc.SetNotifier(&fakeMessageNotifier{})
	got = svc.MessageChannels()
	if !got[0].Available || got[0].Reason != "" {
		t.Fatalf("telegram availability after notifier = %+v", got[0])
	}

	svc.RegisterChannel("slack", "Slack", false, nil, nil)
	svc.RegisterChannel("web", "Web chat", true, nil, nil)
	svc.RegisterChannel("discord", "Discord", false, nil, nil)

	ids := make([]string, 0, len(svc.MessageChannels()))
	for _, c := range svc.MessageChannels() {
		ids = append(ids, c.ID)
	}
	if want := []string{"web", "telegram", "discord", "slack"}; !slices.Equal(ids, want) {
		t.Fatalf("channel order = %v want %v", ids, want)
	}

	// Re-registering an id replaces it.
	svc.RegisterChannel("web", "Web chat v2", true, nil, nil)
	web, ok := svc.messageChannel("web")
	if !ok || web.label != "Web chat v2" {
		t.Fatalf("re-registered web = %+v ok=%v", web, ok)
	}
}

// An old message node without a channels param delivers to telegram.
func TestMessageNodeDefaultsToTelegram(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	notifier := &fakeMessageNotifier{}
	svc.SetNotifier(notifier)

	run, nodeRun := runMessageNode(t, st, svc, map[string]any{"text": "hi"})
	if run.Status != RunSucceeded {
		t.Fatalf("run = %+v", run)
	}
	if len(notifier.sent) != 1 || notifier.sent[0] != "hi" {
		t.Fatalf("sent = %v", notifier.sent)
	}
	out, _ := decodeAny(t, nodeRun.Output).(map[string]any)
	if out["sent"] != true || out["text"] != "hi" {
		t.Fatalf("output = %+v", out)
	}
	channels, _ := out["channels"].(map[string]any)
	if channels["telegram"] != "ok" {
		t.Fatalf("channels = %+v", channels)
	}
}

// web + telegram both succeed and the request carries the run identity.
func TestMessageNodeWebAndTelegram(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	var got []MessageRequest
	record := func(_ context.Context, req MessageRequest) error {
		got = append(got, req)
		return nil
	}
	svc.RegisterChannel("web", "Web chat", true, record, nil)
	svc.RegisterChannel("telegram", "Telegram", false, record, nil)

	run, nodeRun := runMessageNode(t, st, svc, map[string]any{
		"text": "report", "channels": []any{"web", "telegram"}, "agent": "assistant",
	})
	if run.Status != RunSucceeded {
		t.Fatalf("run = %+v", run)
	}
	if len(got) != 2 || got[0].Channel != "web" || got[1].Channel != "telegram" {
		t.Fatalf("requests = %+v", got)
	}
	first := got[0]
	if first.Agent != "assistant" || first.Text != "report" || first.WorkflowID != "w" ||
		first.WorkflowName != "Digest" || first.RunNumber != run.Number {
		t.Fatalf("request = %+v", first)
	}
	out, _ := decodeAny(t, nodeRun.Output).(map[string]any)
	if out["sent"] != true {
		t.Fatalf("output = %+v", out)
	}
	channels, _ := out["channels"].(map[string]any)
	if channels["web"] != "ok" || channels["telegram"] != "ok" {
		t.Fatalf("channels = %+v", channels)
	}
}

// When one channel fails and another succeeds the node succeeds, marks the
// failure in its output, and logs it.
func TestMessageNodePartialFailure(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	svc.RegisterChannel("web", "Web chat", true, func(context.Context, MessageRequest) error { return nil }, nil)
	svc.RegisterChannel("telegram", "Telegram", false, func(context.Context, MessageRequest) error {
		return errors.New("boom")
	}, nil)

	run, nodeRun := runMessageNode(t, st, svc, map[string]any{
		"text": "r", "channels": []any{"web", "telegram"}, "agent": "a",
	})
	if run.Status != RunSucceeded {
		t.Fatalf("run = %+v", run)
	}
	out, _ := decodeAny(t, nodeRun.Output).(map[string]any)
	if out["sent"] != true {
		t.Fatalf("output = %+v", out)
	}
	channels, _ := out["channels"].(map[string]any)
	if channels["web"] != "ok" || channels["telegram"] != "boom" {
		t.Fatalf("channels = %+v", channels)
	}
	if !hasLog(nodeRun.Logs, "message to telegram failed: boom") {
		t.Fatalf("logs = %v", nodeRun.Logs)
	}
}

// When every channel fails the node and the run fail, with every channel named
// in the error.
func TestMessageNodeAllFail(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	fail := func(context.Context, MessageRequest) error { return errors.New("down") }
	svc.RegisterChannel("web", "Web chat", true, fail, nil)
	svc.RegisterChannel("telegram", "Telegram", false, fail, nil)

	run, nodeRun := runMessageNode(t, st, svc, map[string]any{
		"text": "x", "channels": []any{"web", "telegram"}, "agent": "a",
	})
	if run.Status != RunFailed {
		t.Fatalf("run = %+v", run)
	}
	if nodeRun.Status != NodeFailed {
		t.Fatalf("node = %+v", nodeRun)
	}
	if !strings.Contains(nodeRun.Error, "web: down") || !strings.Contains(nodeRun.Error, "telegram: down") {
		t.Fatalf("node error = %q", nodeRun.Error)
	}
}

// An unknown id and an unavailable channel are per-channel errors; with no
// channel left the node fails.
func TestMessageNodeUnknownAndUnavailable(t *testing.T) {
	st := NewMemStore()
	svc := newTestService(t, st, newFakeDeps(), &testClock{})
	svc.RegisterChannel("web", "Web chat", true, nil,
		func() (bool, string) { return false, "Web chat is not connected" })

	run, nodeRun := runMessageNode(t, st, svc, map[string]any{
		"text": "x", "channels": []any{"mystery", "web"}, "agent": "a",
	})
	if run.Status != RunFailed {
		t.Fatalf("run = %+v", run)
	}
	if !strings.Contains(nodeRun.Error, `unknown channel "mystery"`) {
		t.Fatalf("node error = %q", nodeRun.Error)
	}
	if !strings.Contains(nodeRun.Error, "Web chat is not connected") {
		t.Fatalf("node error = %q", nodeRun.Error)
	}
}

// Validate rejects a web channel without a usable agent and a channels param
// that is not a list of non-empty ids, and accepts an unknown channel id.
func TestValidateMessageChannels(t *testing.T) {
	agentOK := func(string) bool { return true }
	agentNo := func(string) bool { return false }
	graph := func(params map[string]any) Graph {
		return Graph{
			Nodes: []Node{
				{ID: "t", Type: "trigger.manual"},
				{ID: "m", Type: "message", Params: params},
			},
			Edges: []Edge{{ID: "e", From: "t", To: "m"}},
		}
	}

	noAgent := graph(map[string]any{"text": "x", "channels": []any{"web"}})
	if !hasCodeFor(Validate(noAgent, agentOK), "m", "missing_param") {
		t.Fatalf("web without agent = %+v", Validate(noAgent, agentOK))
	}

	ghostAgent := graph(map[string]any{"text": "x", "channels": []string{"web"}, "agent": "ghost"})
	if !hasCodeFor(Validate(ghostAgent, agentNo), "m", "unknown_agent") {
		t.Fatalf("web with unknown agent = %+v", Validate(ghostAgent, agentNo))
	}

	wrongType := graph(map[string]any{"text": "x", "channels": "web"})
	if !hasCodeFor(Validate(wrongType, agentOK), "m", "bad_param") {
		t.Fatalf("channels as string = %+v", Validate(wrongType, agentOK))
	}

	emptyItem := graph(map[string]any{"text": "x", "channels": []any{"web", ""}, "agent": "a"})
	if !hasCodeFor(Validate(emptyItem, agentOK), "m", "bad_param") {
		t.Fatalf("channels with empty id = %+v", Validate(emptyItem, agentOK))
	}

	// Unknown channel ids and a valid web+agent pass on save.
	unknown := graph(map[string]any{"text": "x", "channels": []any{"telegram", "discord"}})
	if issues := Validate(unknown, agentOK); len(issues) != 0 {
		t.Fatalf("unknown channel ids = %+v", issues)
	}
	valid := graph(map[string]any{"text": "x", "channels": []any{"web"}, "agent": "assistant"})
	if issues := Validate(valid, agentOK); len(issues) != 0 {
		t.Fatalf("valid web channel = %+v", issues)
	}
}
