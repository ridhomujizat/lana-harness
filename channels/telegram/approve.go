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
	reply chan core.ApprovalOutcome
}

func newApprovalID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Approve asks the owner through an inline keyboard (API-TG-004). It is the runtime's
// approval answerer; requests for sessions without a chat are unavailable (fail-closed).
func (c *Channel) Approve(ctx context.Context, req core.ApprovalRequest) core.ApprovalOutcome {
	w := c.chatForSession(req.SessionID)
	if w == nil {
		return core.OutcomeUnavailable
	}
	arg, liveID := w.toolArg(req.CallID)
	if arg == "" {
		arg = req.Label
	}
	label := req.ToolName
	if arg != "" {
		label += ": " + arg
	}
	a := &approval{id: newApprovalID(), chat: w, reply: make(chan core.ApprovalOutcome, 1)}
	keyboard := &Keyboard{Rows: [][]Button{{
		{Text: "Allow once", Data: "a:" + a.id + ":once"},
		{Text: "Allow for session", Data: "a:" + a.id + ":session"},
		{Text: "Reject", Data: "a:" + a.id + ":reject"},
	}}}
	msgID, err := c.api.SendMessage(ctx, w.id, "Allow "+label+"?", SendOptions{ReplyTo: liveID, Keyboard: keyboard})
	if err != nil {
		c.apiErr("sendMessage", err)
		return core.OutcomeUnavailable
	}
	c.mu.Lock()
	c.approvals[a.id] = a
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.approvals, a.id)
		c.mu.Unlock()
	}()

	timer := time.NewTimer(c.ApprovalTimeout)
	defer timer.Stop()
	var outcome core.ApprovalOutcome
	status := ""
	select {
	case outcome = <-a.reply:
	case <-timer.C:
		outcome, status = core.OutcomeRejected, "⊘ approval expired"
	case <-ctx.Done():
		outcome = core.OutcomeCancelled
	}
	if status == "" {
		status = outcomeText(outcome)
	}
	editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	c.apiErr("editMessageText", c.api.EditMessageText(editCtx, w.id, msgID, "Allow "+label+"?\n"+status, SendOptions{}))
	return outcome
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
