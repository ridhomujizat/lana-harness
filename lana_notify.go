package lana

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/notify"
	"github.com/ridhomujizat/lana-harness/internal/workflow"
)

// The runtime side of macOS notifications (spec #42). The runtime owns the
// notification bus. The daemon starts the feeders with StartNotifications:
// agent replies come from the session event hook, workflow runs from the
// workflow change stream. The web brokers publish approvals and questions to
// the same bus, because only they know the request id the answer endpoint
// takes. A library or test runtime never starts the feeders, so it never
// publishes.

// notifyReplyChannel is the only channel whose replies notify: Telegram
// replies already reach Telegram, and cli, library and workflow sessions are
// not chats the owner waits on in the dashboard.
const notifyReplyChannel = "web"

// notifyMetaCacheMax bounds the per-session metadata cache of the reply feeder.
const notifyMetaCacheMax = 2048

// workflowStartSlack lets a run that started just before the feeder count as
// new, so a daemon start that launches a run still announces it.
const workflowStartSlack = 5 * time.Second

// notifyState holds the bus, created on first use, and the feeder start
// guard.
type notifyState struct {
	once  sync.Once
	bus   *notify.Bus
	start sync.Once
}

func (rt *Runtime) notifyState() *notifyState { return &rt.notify }

// Notifications returns the notification bus of the runtime (spec #42). It is
// never nil.
func (rt *Runtime) Notifications() *notify.Bus {
	state := rt.notifyState()
	state.once.Do(func() { state.bus = notify.NewBus() })
	return state.bus
}

// StartNotifications feeds the notification bus until ctx ends: the reply of
// every finished web turn and the start, end, failure and owner decisions of
// every workflow run. It is a no-op after the first call.
func (rt *Runtime) StartNotifications(ctx context.Context) {
	if ctx == nil {
		ctx = rt.ctx
	}
	bus := rt.Notifications()
	rt.notifyState().start.Do(func() {
		if rt.coreCtx != nil && rt.coreCtx.Hooks != nil && rt.coreCtx.Hooks.SessionEvent != nil && rt.coreCtx.Sessions != nil {
			feeder := newReplyFeeder(bus, rt.coreCtx.Sessions.Info)
			sub := rt.coreCtx.Hooks.SessionEvent.Subscribe()
			go func() {
				defer sub.Close()
				for {
					received, err := sub.Recv(ctx)
					if err != nil {
						return
					}
					if received.Lagged > 0 {
						continue
					}
					feeder.handle(ctx, received.Value)
				}
			}()
		}
		if rt.workflows != nil {
			tracker := newWorkflowRunNotifier(bus, rt.workflows, time.Now().Add(-workflowStartSlack))
			changes, unsubscribe := rt.workflows.Subscribe()
			go func() {
				defer unsubscribe()
				for {
					select {
					case <-ctx.Done():
						return
					case change, ok := <-changes:
						if !ok {
							return
						}
						tracker.handle(ctx, change)
					}
				}
			}()
		}
	})
}

// sessionMeta is what the reply feeder keeps per session.
type sessionMeta struct {
	notify bool
}

// replyFeeder turns the session event stream into reply events: it keeps the
// text of the last assistant message of each turn and publishes it when the
// turn ends.
type replyFeeder struct {
	bus    *notify.Bus
	lookup func(context.Context, string) (core.SessionInfo, error)
	meta   map[string]sessionMeta
	last   map[string]string
}

func newReplyFeeder(bus *notify.Bus, lookup func(context.Context, string) (core.SessionInfo, error)) *replyFeeder {
	return &replyFeeder{bus: bus, lookup: lookup, meta: map[string]sessionMeta{}, last: map[string]string{}}
}

// replySession reports whether a session's replies notify: a root session on
// the web channel.
func replySession(info core.SessionInfo) bool {
	return info.Channel == notifyReplyChannel && info.ParentSessionID == ""
}

func (f *replyFeeder) wants(ctx context.Context, sessionID string) bool {
	if meta, ok := f.meta[sessionID]; ok {
		return meta.notify
	}
	info, err := f.lookup(ctx, sessionID)
	if err != nil {
		return false
	}
	if len(f.meta) >= notifyMetaCacheMax {
		f.meta = map[string]sessionMeta{}
	}
	meta := sessionMeta{notify: replySession(info)}
	f.meta[sessionID] = meta
	return meta.notify
}

func (f *replyFeeder) handle(ctx context.Context, event core.SessionEvent) {
	switch event.Kind {
	case core.KindTurnStart, core.KindAssistantMessage, core.KindTurnEnd:
	default:
		return
	}
	if !f.wants(ctx, event.SessionID) {
		return
	}
	switch event.Kind {
	case core.KindTurnStart:
		delete(f.last, event.SessionID)
	case core.KindAssistantMessage:
		var msg core.AssistantMessage
		if json.Unmarshal(event.Payload, &msg) != nil {
			return
		}
		if text := assistantText(msg); text != "" {
			f.last[event.SessionID] = text
		}
	case core.KindTurnEnd:
		text := f.last[event.SessionID]
		delete(f.last, event.SessionID)
		var end core.TurnEnd
		_ = json.Unmarshal(event.Payload, &end)
		if text == "" || end.Reason == "cancelled" {
			return
		}
		turnID := end.TurnID
		if turnID == "" {
			turnID = event.TurnID
		}
		// Read the session again for the newest title and agent.
		info, err := f.lookup(ctx, event.SessionID)
		if err != nil || !replySession(info) {
			return
		}
		f.bus.Publish(notify.Reply(info.Agent, info.Title, info.ID, turnID, text))
	}
}

// assistantText joins the text blocks of an assistant message.
func assistantText(msg core.AssistantMessage) string {
	parts := make([]string, 0, len(msg.Content))
	for _, block := range msg.Content {
		if block.Kind == core.BlockText && strings.TrimSpace(block.Text) != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

// workflowRunSource is the part of the workflow service the run notifier reads.
type workflowRunSource interface {
	RunDetail(ctx context.Context, id, ref string) (workflow.RunDetail, error)
	Get(ctx context.Context, id string) (workflow.Detail, error)
}

// workflowRunNotifier turns workflow changes into run events. A change only
// names a workflow and a run, so it reads the run and compares it with what it
// saw before.
type workflowRunNotifier struct {
	bus          *notify.Bus
	source       workflowRunSource
	startedAfter int64
	// open holds the pending decisions announced per run, by decision id.
	open map[string]map[string]bool
}

func newWorkflowRunNotifier(bus *notify.Bus, source workflowRunSource, startedAfter time.Time) *workflowRunNotifier {
	return &workflowRunNotifier{bus: bus, source: source, startedAfter: startedAfter.UnixMilli(), open: map[string]map[string]bool{}}
}

func (n *workflowRunNotifier) handle(ctx context.Context, change workflow.Change) {
	if change.RunID == "" || change.WorkflowID == "" {
		return
	}
	detail, err := n.source.RunDetail(ctx, change.WorkflowID, change.RunID)
	if err != nil {
		return
	}
	run := detail.Run
	// A run that started before the feeder is resumed, not new: it does not
	// announce its start again. A run that ended before it is old news.
	terminal := run.Status == workflow.RunSucceeded || run.Status == workflow.RunFailed || run.Status == workflow.RunCancelled
	if terminal && run.EndedAt > 0 && run.EndedAt < n.startedAfter {
		delete(n.open, run.ID)
		return
	}
	name := run.WorkflowID
	if wf, err := n.source.Get(ctx, run.WorkflowID); err == nil && strings.TrimSpace(wf.Name) != "" {
		name = wf.Name
	}
	info := notify.WorkflowRun{
		WorkflowID:   run.WorkflowID,
		WorkflowName: name,
		RunID:        run.ID,
		Number:       run.Number,
		Trigger:      run.Trigger,
		Summary:      run.Summary,
		Error:        run.Error,
		StartedAt:    run.StartedAt,
		EndedAt:      run.EndedAt,
	}
	if run.StartedAt >= n.startedAfter {
		n.bus.Publish(notify.WorkflowStarted(info))
	}

	announced := n.open[run.ID]
	pending := map[string]bool{}
	for _, d := range detail.Decisions {
		if d.Status != "pending" {
			continue
		}
		pending[d.ID] = true
		approvalID := ""
		if d.Kind == "approval" {
			approvalID = notify.WorkflowApprovalID(run.WorkflowID, d.ID)
		}
		n.bus.Publish(notify.WorkflowWaiting(info, d.ID, d.Reason, approvalID))
	}
	for id := range announced {
		if pending[id] {
			continue
		}
		approvalID := ""
		for _, d := range detail.Decisions {
			if d.ID == id && d.Kind == "approval" {
				approvalID = notify.WorkflowApprovalID(run.WorkflowID, id)
			}
		}
		outcome := "resolved"
		for _, d := range detail.Decisions {
			if d.ID == id && d.Choice != "" {
				outcome = d.Choice
			}
		}
		n.bus.Publish(notify.WorkflowDecided(info, id, approvalID, outcome))
	}
	if len(pending) > 0 {
		n.open[run.ID] = pending
	} else {
		delete(n.open, run.ID)
	}

	switch run.Status {
	case workflow.RunSucceeded:
		if msg := lastRunMessage(detail.Nodes); msg != "" {
			info.Summary = msg
		}
		n.bus.Publish(notify.WorkflowFinished(info))
	case workflow.RunFailed:
		n.bus.Publish(notify.WorkflowFailed(info))
	}
}

// lastRunMessage returns a short text from the last successful output of a run,
// so a "Workflow done" banner shows the run's final message instead of just
// "N nodes ok". It considers succeeded and fallback rows with a non-empty
// output, ordered by EndedAt descending (ties: later in the slice first), and
// returns the first non-empty string among the text, output, markdown, content
// and result keys of the output object. It returns "" when there is none.
func lastRunMessage(nodes []workflow.NodeRun) string {
	order := make([]int, 0, len(nodes))
	for i := range nodes {
		n := nodes[i]
		if n.Status != workflow.NodeSucceeded && n.Status != workflow.NodeFallback {
			continue
		}
		if len(n.Output) == 0 {
			continue
		}
		order = append(order, i)
	}
	sort.Slice(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if nodes[ia].EndedAt != nodes[ib].EndedAt {
			return nodes[ia].EndedAt > nodes[ib].EndedAt
		}
		return ia > ib
	})
	for _, i := range order {
		if msg := nodeMessage(nodes[i].Output); msg != "" {
			return msg
		}
	}
	return ""
}

// nodeMessage decodes one node output as a JSON object and returns the first
// non-empty trimmed string among its text, output, markdown, content and result
// keys. A non-string value is skipped.
func nodeMessage(out json.RawMessage) string {
	var obj map[string]any
	if json.Unmarshal(out, &obj) != nil {
		return ""
	}
	for _, key := range []string{"text", "output", "markdown", "content", "result"} {
		s, ok := obj[key].(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}
