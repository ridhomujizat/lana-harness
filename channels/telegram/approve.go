package telegram

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

// hardlineArgLimit caps the argument shown for a hardline request, so the
// bubble stays well under the Telegram message limit (REQ-HLF-007).
const hardlineArgLimit = 3000

type approval struct {
	id    string
	chat  *chat
	tool  string
	text  string
	reply chan core.ApprovalOutcome
	ready chan struct{}
	// hardline is the matched rule of a hardline request (spec 63); it selects
	// the reduced keyboard and, for the Block tier, the typed challenge
	// (REQ-HLF-007, REQ-HLF-008).
	hardline *core.HardlineMatch
	// armed and challenge hold the pending typed-challenge capture of a
	// Block-tier request. Both are guarded by *Channel.mu, the mutex of the
	// approvals map (REQ-HLF-007).
	armed     bool
	challenge string
}

// newApprovalChallenge returns a random 4-digit challenge (1000-9999) for a
// Block-tier hardline request (REQ-HLF-007).
func newApprovalChallenge() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(9000))
	if err != nil {
		return "", err
	}
	return strconv.Itoa(1000 + int(n.Int64())), nil
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
	if req.Hardline != nil {
		// REQ-HLF-007: the bubble shows the full argument, not the shortened
		// status-line one, cut only at the Telegram message limit.
		if full := toolfmt.PrimaryArg(req.ToolName, req.Input); full != "" {
			arg = full
		}
		arg = toolfmt.Cut(arg, hardlineArgLimit)
	}
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
	text := "🔐 Allow " + label + "?"
	if req.Hardline != nil {
		// REQ-HLF-007: name the matched rule and its reason.
		text += "\n⚠️ " + req.Hardline.Rule + " · " + req.Hardline.Reason
	}
	a := &approval{
		id:       newApprovalID(),
		chat:     w,
		tool:     tool,
		text:     text,
		hardline: req.Hardline,
		reply:    make(chan core.ApprovalOutcome, 1),
		ready:    make(chan struct{}),
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
	keyboard := approvalKeyboard(a)
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

// approvalKeyboard returns the inline keyboard of a request. An ordinary request
// keeps its three choices; a hardline request offers only "Allow once" and
// "Reject" (REQ-HLF-007, REQ-HLF-008). A Block-tier request confirms through the
// typed challenge, so its button carries the "verify" action.
func approvalKeyboard(a *approval) *Keyboard {
	once, onceData := "Allow once", "a:"+a.id+":once"
	reject := Button{Text: "Reject", Data: "a:" + a.id + ":reject"}
	if a.hardline != nil {
		if a.hardline.Tier == core.HardlineBlock {
			once, onceData = "Allow once…", "a:"+a.id+":verify"
		}
		return &Keyboard{Rows: [][]Button{{{Text: once, Data: onceData}, reject}}}
	}
	session := Button{Text: "Allow for session", Data: "a:" + a.id + ":session"}
	return &Keyboard{Rows: [][]Button{{{Text: once, Data: onceData}, session, reject}}}
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
// unavailable requests count as rejected. A confirmed Block-tier call counts as
// allowed, like "allow once" (REQ-HLF-007).
func (w *chat) tallyOutcomeLocked(outcome core.ApprovalOutcome) {
	w.approvalTotal++
	if outcome == core.OutcomeAllowedOnce || outcome == core.OutcomeAllowedSession || outcome == core.OutcomeAllowedVerified {
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
	case core.OutcomeAllowedVerified:
		return "✅ Allowed (confirmed)"
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
	if a == nil {
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "this request is no longer active"))
		return
	}
	if choice == "verify" {
		c.answerApprovalVerify(ctx, q, a)
		return
	}
	var outcome core.ApprovalOutcome
	switch choice {
	case "once":
		outcome = core.OutcomeAllowedOnce
	case "session":
		outcome = core.OutcomeAllowedSession
	case "reject":
		outcome = core.OutcomeRejected
	}
	if outcome == "" {
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

// answerApprovalVerify handles the "Allow once…" press of a Block-tier hardline
// request: it generates a 4-digit challenge, edits the bubble to show it,
// replaces the keyboard with Reject only, and arms the typed reply
// (REQ-HLF-007, AC-HLF-014).
func (c *Channel) answerApprovalVerify(ctx context.Context, q *CallbackQuery, a *approval) {
	if a.hardline == nil || a.hardline.Tier != core.HardlineBlock {
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "this request is no longer active"))
		return
	}
	challenge, err := newApprovalChallenge()
	if err != nil {
		c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, "could not start the confirmation"))
		return
	}
	c.mu.Lock()
	a.armed, a.challenge = true, challenge
	c.mu.Unlock()
	reject := &Keyboard{Rows: [][]Button{{{Text: "Reject", Data: "a:" + a.id + ":reject"}}}}
	w := a.chat
	bubbleID := int64(0)
	if q.Message != nil {
		bubbleID = q.Message.MessageID
	}
	if bubbleID == 0 {
		w.mu.Lock()
		bubbleID = w.bubbleID
		w.mu.Unlock()
	}
	if bubbleID != 0 {
		text := a.text + "\n\nReply with " + challenge + " to run it."
		c.apiErr("editMessageText", c.api.EditMessageText(ctx, w.id, bubbleID, text, SendOptions{Keyboard: reject}))
	}
	c.apiErr("answerCallbackQuery", c.api.AnswerCallbackQuery(ctx, q.ID, ""))
}

// armedApproval returns the head approval of w when it awaits a typed
// challenge, or nil (REQ-HLF-007).
func (c *Channel) armedApproval(w *chat) *approval {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.approvalQueue) == 0 {
		return nil
	}
	a := w.approvalQueue[0]
	c.mu.Lock()
	defer c.mu.Unlock()
	if !a.armed {
		return nil
	}
	return a
}

// captureApprovalText consumes the owner's reply to a Block-tier hardline
// challenge (REQ-HLF-007, AC-HLF-014). A reply that equals the challenge allows
// the call once and verified; any other reply rejects it. Either way the text is
// not forwarded to the agent, and the capture is disarmed after one use. It
// returns false when no capture applies, so the text follows the inbox path.
func (w *chat) captureApprovalText(ctx context.Context, m *Message, text string) bool {
	if text == "" || m.From == nil || !w.ch.isOwner(m.Chat.Type, m.From.ID) {
		return false
	}
	a := w.ch.armedApproval(w)
	if a == nil {
		return false
	}
	w.ch.mu.Lock()
	challenge := a.challenge
	a.armed = false
	w.ch.mu.Unlock()
	outcome := core.OutcomeRejected
	if strings.TrimSpace(text) == challenge {
		outcome = core.OutcomeAllowedVerified
	}
	select {
	case a.reply <- outcome:
	default:
	}
	return true
}
