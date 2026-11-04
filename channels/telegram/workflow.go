package telegram

import (
	"context"
	"strings"
)

// WorkflowHooks connect the Telegram channel to the workflow service
// (API-WF-022). cmd/lana installs them with SetWorkflowHooks.
type WorkflowHooks struct {
	// Command handles "/<command> [rest]" from an owner; handled=false lets the
	// normal command handling run.
	Command func(ctx context.Context, text string) (reply string, handled bool)
	// Callback answers a "w:<decision id>:<choice>" button; it returns the line
	// shown under the message.
	Callback func(ctx context.Context, decisionID, choice string) (string, error)
}

// SetWorkflowHooks installs the workflow hooks. It is safe to call before or
// after Start (API-WF-022).
func (c *Channel) SetWorkflowHooks(h WorkflowHooks) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.hooks = h
}

// workflowHooks returns the installed workflow hooks.
func (c *Channel) workflowHooks() WorkflowHooks {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hooks
}

// SendOwnerButtons sends text with an inline keyboard to every owner private
// chat (API-WF-022). A nil rows sends no keyboard. It returns the first error
// but tries every owner, and returns nil when there is no owner private chat.
func (c *Channel) SendOwnerButtons(ctx context.Context, text string, rows [][]Button) error {
	var keyboard *Keyboard
	if rows != nil {
		keyboard = &Keyboard{Rows: rows}
	}
	var firstErr error
	for id := range c.cfg.OwnerIDs {
		// A private chat's id equals the owner's user id, so an owner id is
		// also a route key, the same selection SendOwnerCode uses.
		if _, ok := c.cfg.Routes[id]; !ok {
			continue
		}
		if _, err := c.api.SendMessage(ctx, id, text, SendOptions{Keyboard: keyboard}); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// workflowCommand offers a non-built-in command to the workflow service. It
// reports whether a workflow took it (API-WF-022).
func (c *Channel) workflowCommand(ctx context.Context, text string) (string, bool) {
	h := c.workflowHooks()
	if h.Command == nil {
		return "", false
	}
	return h.Command(ctx, text)
}

// answerWorkflow handles a press on a workflow decision button ("w" prefix):
// data is "<decision id>:<choice>" (API-WF-022).
func (w *chat) answerWorkflow(ctx context.Context, q *CallbackQuery, data string, owner bool) {
	if !owner {
		w.ch.answerCallback(ctx, q.ID, "only the owner can answer")
		return
	}
	callback := w.ch.workflowHooks().Callback
	if callback == nil {
		w.ch.answerCallback(ctx, q.ID, "Workflows are not available")
		return
	}
	decisionID, choice, _ := strings.Cut(data, ":")
	line, err := callback(ctx, decisionID, choice)
	if err != nil {
		w.ch.answerCallback(ctx, q.ID, err.Error())
		w.editWorkflowMessage(ctx, q, "Error: "+err.Error())
		return
	}
	w.ch.answerCallback(ctx, q.ID, "Done")
	w.editWorkflowMessage(ctx, q, line)
}

// editWorkflowMessage appends the result line to the decision message and
// removes its keyboard (API-WF-022).
func (w *chat) editWorkflowMessage(ctx context.Context, q *CallbackQuery, line string) {
	if q.Message == nil {
		return
	}
	text := q.Message.Text + "\n\n" + line
	w.ch.apiErr("editMessageText", w.ch.api.EditMessageText(ctx, w.id, q.Message.MessageID, text, SendOptions{}))
}
