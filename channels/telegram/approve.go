package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
)

type approval struct {
	id    string
	chat  *chat
	tool  string
	text  string
	reply chan core.ApprovalOutcome
	ready chan struct{}
}

func newApprovalID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Approve asks the owner through the turn's permission bubble (API-TG-004). It
// is the runtime's approval answerer; requests for sessions without a chat are
// unavailable (fail-closed).
func (c *Channel) Approve(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	w := c.chatForApproval(ctx, req.SessionID)
	if w == nil {
		return core.OutcomeUnavailable
	}
	arg, liveID := w.toolArg(req.CallID)
	tool := shortTool(req.ToolName)
	label := tool
	if arg != "" {
		label += ": " + arg
	}
	// A child approval carries the child agent name in Label (API-TEAM-004).
	if req.Label != "" {
		tool = req.Label + " · " + tool
		label = req.Label + " · " + label
	}
	a := &approval{
		id:    newApprovalID(),
		chat:  w,
		tool:  tool,
		text:  "🔐 Allow " + label + "?",
		reply: make(chan core.ApprovalOutcome, 1),
		ready: make(chan struct{}),
	}
	w.enqueueApproval(a)
	select {
	case <-a.ready:
	case <-ctx.Done():
		if w.cancelQueuedApproval(a) {
			return core.OutcomeCancelled
		}
	}
	return c.awaitApproval(ctx, a, liveID)
}

// awaitApproval shows a request at the head of the turn's approval queue, waits
// for the answer, records the outcome, and promotes the next request.
func (c *Channel) awaitApproval(ctx context.Context, a *approval, liveID int64) core.ApprovalOutcome {
	w := a.chat
	keyboard := &Keyboard{Rows: [][]Button{{
		{Text: "Allow once", Data: "a:" + a.id + ":once"},
		{Text: "Allow for session", Data: "a:" + a.id + ":session"},
		{Text: "Reject", Data: "a:" + a.id + ":reject"},
	}}}
	c.mu.Lock()
	c.approvals[a.id] = a
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.approvals, a.id)
		c.mu.Unlock()
	}()

	if ctx.Err() == nil {
		w.mu.Lock()
		bubbleID := w.bubbleID
		w.mu.Unlock()
		if bubbleID == 0 {
			messageID, err := c.api.SendMessage(ctx, w.id, a.text, SendOptions{ReplyTo: liveID, Keyboard: keyboard})
			if err != nil {
				c.apiErr("sendMessage", err)
				w.releaseApproval(a, core.OutcomeUnavailable)
				return core.OutcomeUnavailable
			}
			w.mu.Lock()
			w.bubbleID = messageID
			w.mu.Unlock()
		} else {
			c.apiErr("editMessageText", c.api.EditMessageText(ctx, w.id, bubbleID, a.text, SendOptions{Keyboard: keyboard}))
		}
	}

	timer := time.NewTimer(c.ApprovalTimeout)
	defer timer.Stop()
	var outcome core.ApprovalOutcome
	status := ""
	select {
	case outcome = <-a.reply:
	case <-timer.C:
		outcome, status = core.OutcomeUnavailable, "⊘ approval expired"
	case <-ctx.Done():
		outcome = core.OutcomeCancelled
	}
	if status == "" {
		status = outcomeText(outcome)
	}
	w.editBubble(ctx, status+" · "+a.tool)
	w.releaseApproval(a, outcome)
	return outcome
}

// enqueueApproval appends a request to the turn's FIFO and promotes it when it
// is the head.
func (w *chat) enqueueApproval(a *approval) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.approvalQueue = append(w.approvalQueue, a)
	if len(w.approvalQueue) == 1 {
		close(a.ready)
	}
}

// cancelQueuedApproval removes a request cancelled before it reached the head.
// It returns false when the request is already the head.
func (w *chat) cancelQueuedApproval(a *approval) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, queued := range w.approvalQueue {
		if queued != a {
			continue
		}
		if i == 0 {
			return false
		}
		w.approvalQueue = append(w.approvalQueue[:i], w.approvalQueue[i+1:]...)
		w.tallyOutcomeLocked(core.OutcomeCancelled)
		return true
	}
	return true
}

// releaseApproval records the outcome of the head request and promotes the next
// waiting one.
func (w *chat) releaseApproval(a *approval, outcome core.ApprovalOutcome) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tallyOutcomeLocked(outcome)
	if len(w.approvalQueue) > 0 && w.approvalQueue[0] == a {
		w.approvalQueue = w.approvalQueue[1:]
		if len(w.approvalQueue) > 0 {
			close(w.approvalQueue[0].ready)
		}
	}
}

// tallyOutcomeLocked counts one answered approval. Expired, cancelled, and
// unavailable requests count as rejected.
func (w *chat) tallyOutcomeLocked(outcome core.ApprovalOutcome) {
	w.approvalTotal++
	if outcome == core.OutcomeAllowedOnce || outcome == core.OutcomeAllowedSession {
		w.approvalAllowed++
	} else {
		w.approvalRejected++
	}
}

// chatForApproval finds the chat of a session or of its nearest bound ancestor.
// A child session inherits the root channel, so its approval is shown in the
// root chat (REQ-TEAM-010).
func (c *Channel) chatForApproval(ctx context.Context, sessionID string) *chat {
	for depth := 0; depth < 3 && sessionID != ""; depth++ {
		if w := c.chatForSession(sessionID); w != nil {
			return w
		}
		info, err := c.backend.SessionInfo(ctx, sessionID)
		if err != nil {
			return nil
		}
		sessionID = info.ParentSessionID
	}
	return nil
}

func outcomeText(outcome core.ApprovalOutcome) string {
	switch outcome {
	case core.OutcomeAllowedOnce:
		return "✓ allowed once"
	case core.OutcomeAllowedSession:
		return "✓ allowed for session"
	case core.OutcomeRejected:
		return "⊘ rejected"
	}
	return "⊘ cancelled"
}

// answerApproval handles a press on an approval button. data is "<id>:<choice>".
func (c *Channel) answerApproval(ctx context.Context, q *CallbackQuery, data string, owner bool) {
	if !owner {
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "only the owner can approve"))
		return
	}
	id, choice, _ := strings.Cut(data, ":")
	c.mu.Lock()
	a := c.approvals[id]
	c.mu.Unlock()
	var outcome core.ApprovalOutcome
	switch choice {
	case "once":
		outcome = core.OutcomeAllowedOnce
	case "session":
		outcome = core.OutcomeAllowedSession
	case "reject":
		outcome = core.OutcomeRejected
	}
	if a == nil || outcome == "" {
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "this request is no longer active"))
		return
	}
	select {
	case a.reply <- outcome:
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, ""))
	default:
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "this request is no longer active"))
	}
}
