package telegram

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/tgfmt"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	textOnlyReply = "Only text messages are supported for now."
	waitReply     = "· wait for the turn to finish"
	// drainingReply is the text a chat gets while the daemon drains
	// (REQ-TFB-002, API-TFB-008).
	drainingReply = "lana is restarting. Try again in a moment."
)

// chat is the worker of one Telegram chat. It handles the chat's updates in order and
// runs at most one turn at a time in a child goroutine.
type chat struct {
	ch         *Channel
	id         int64
	routeAgent string
	updates    chan Update

	mu          sync.Mutex
	agent       string
	session     string
	running     bool
	cancelTurn  context.CancelFunc
	liveID      int64
	tools       []*toolLine
	step        int
	afterResult bool
	calls       int
	// stepTextLen and stepTools mark the answer length and tool count at the
	// start of the current step. A step_retry frame drops what a failed step
	// streamed (REQ-LTC-032).
	stepTextLen int
	stepTools   int
	// Permission bubble state of the running turn (REQ-TGL-002).
	bubbleID         int64
	approvalQueue    []*approval
	approvalTotal    int
	approvalAllowed  int
	approvalRejected int
	usageIn          int64
	usageOut         int64
	turnWG           sync.WaitGroup
}

type toolLine struct {
	callID string
	agent  string
	name   string
	arg    string
	done   bool
}

func (w *chat) address() string { return strconv.FormatInt(w.id, 10) }

func (w *chat) run(ctx context.Context) {
	defer w.turnWG.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case update := <-w.updates:
			switch {
			case update.CallbackQuery != nil:
				w.handleCallback(ctx, update.CallbackQuery)
			case update.Message != nil:
				w.handleMessage(ctx, update.Message)
			}
		}
	}
}

func isGroup(chatType string) bool { return chatType == "group" || chatType == "supergroup" }

func (w *chat) repliesToBot(m *Message) bool {
	return m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.ID == w.ch.bot.ID
}

func (w *chat) handleMessage(ctx context.Context, m *Message) {
	group := isGroup(m.Chat.Type)
	text := strings.TrimSpace(m.Text)
	if text == "" {
		if m.HasMedia() && (!group || w.repliesToBot(m)) {
			w.reply(ctx, m.MessageID, textOnlyReply)
		}
		return
	}
	if strings.HasPrefix(text, "/") {
		w.handleCommand(ctx, m, text)
		return
	}
	if group {
		mention := "@" + w.ch.bot.Username
		if !containsFold(text, mention) && !w.repliesToBot(m) {
			return
		}
		text = strings.TrimSpace(replaceFold(text, mention, ""))
		name := "someone"
		if m.From != nil && m.From.FirstName != "" {
			name = m.From.FirstName
		}
		text = name + ": " + text
	}
	w.handleText(ctx, m, text)
}

func (w *chat) handleText(ctx context.Context, m *Message, text string) {
	// A drain starts no turn and queues nothing (REQ-TFB-002, API-TFB-008).
	if w.ch.backend.Draining() {
		w.reply(ctx, m.MessageID, drainingReply)
		return
	}
	if w.captureQuestionText(ctx, m, text) {
		return
	}
	w.mu.Lock()
	running, session := w.running, w.session
	w.mu.Unlock()
	if running {
		if err := w.ch.backend.Send(ctx, session, text); err != nil {
			w.reply(ctx, m.MessageID, "✗ "+err.Error())
			return
		}
		_ = w.ch.api.SetMessageReaction(ctx, w.id, m.MessageID, "👀")
		return
	}
	session, _, err := w.ensureSession(ctx)
	if err != nil {
		w.reply(ctx, m.MessageID, sessionErrText(err))
		return
	}
	w.startTurn(ctx, m.MessageID, session, text)
}

func sessionErrText(err error) string {
	var held *core.LeaseHeldError
	if errors.As(err, &held) {
		return "✗ session is in use by another process"
	}
	return "✗ " + err.Error()
}

// agentNames returns the agents and teams that /agent accepts.
func (w *chat) agentNames() []string {
	return append(w.ch.backend.AgentNames(), w.ch.backend.TeamNames()...)
}

// currentAgent returns the active agent of the chat (API-TG-002).
func (w *chat) currentAgent(ctx context.Context) string {
	w.mu.Lock()
	agent := w.agent
	w.mu.Unlock()
	if agent != "" {
		return agent
	}
	if active, err := w.ch.backend.ActiveAgent(ctx, w.address()); err == nil && active != "" {
		return active
	}
	return w.routeAgent
}

// ensureSession resolves, leases, and binds the chat session.
func (w *chat) ensureSession(ctx context.Context) (string, string, error) {
	w.mu.Lock()
	session, agent := w.session, w.agent
	w.mu.Unlock()
	if session != "" {
		return session, agent, nil
	}
	return w.useSession(ctx, w.currentAgent(ctx), false)
}

// useSession switches the chat to the agent's running session, or to a new one.
func (w *chat) useSession(ctx context.Context, agent string, fresh bool) (string, string, error) {
	var info core.SessionInfo
	var err error
	if fresh {
		info, err = w.ch.backend.NewSession(ctx, w.address(), agent)
	} else {
		info, err = w.ch.backend.ChatSession(ctx, w.address(), agent)
	}
	if err != nil {
		return "", "", err
	}
	if err := w.ch.backend.Lease(ctx, info.ID); err != nil {
		return "", "", err
	}
	w.ch.bindSession(info.ID, w)
	w.mu.Lock()
	w.session, w.agent = info.ID, agent
	w.mu.Unlock()
	return info.ID, agent, nil
}

func (w *chat) reply(ctx context.Context, replyTo int64, text string) {
	_, err := w.ch.api.SendMessage(ctx, w.id, text, SendOptions{ReplyTo: replyTo})
	w.ch.apiErr("sendMessage", err)
}

// sendHTML sends HTML, falling back to plain text when Telegram cannot parse it.
func (w *chat) sendHTML(ctx context.Context, text string, opts SendOptions) (int64, error) {
	opts.ParseMode = "HTML"
	id, err := w.ch.api.SendMessage(ctx, w.id, text, opts)
	if isParseError(err) {
		opts.ParseMode = ""
		id, err = w.ch.api.SendMessage(ctx, w.id, tgfmt.Plain(text), opts)
	}
	return id, err
}

// editHTML edits a message with HTML, falling back to plain text when Telegram
// cannot parse it. A non-nil keyboard is attached.
func (w *chat) editHTML(ctx context.Context, messageID int64, text string, keyboard *Keyboard) error {
	err := w.ch.api.EditMessageText(ctx, w.id, messageID, text, SendOptions{ParseMode: "HTML", Keyboard: keyboard})
	if isParseError(err) {
		err = w.ch.api.EditMessageText(ctx, w.id, messageID, tgfmt.Plain(text), SendOptions{Keyboard: keyboard})
	}
	return err
}

func isParseError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == 400 && containsFold(apiErr.Description, "can't parse entities")
}

// startTurn runs one turn in a child goroutine.
func (w *chat) startTurn(ctx context.Context, trigger int64, session, text string) {
	w.launchTurn(ctx, trigger, session, func(ctx context.Context) (TurnResult, error) {
		return w.ch.backend.RunTurn(ctx, session, text)
	})
}

// startContinueTurn runs a continue turn in a child goroutine (API-TFB-005).
func (w *chat) startContinueTurn(ctx context.Context, trigger int64, session string) {
	w.launchTurn(ctx, trigger, session, func(ctx context.Context) (TurnResult, error) {
		return w.ch.backend.ContinueTurn(ctx, session)
	})
}

// launchTurn resets the per-turn layout state and runs run in a child goroutine.
func (w *chat) launchTurn(ctx context.Context, trigger int64, session string, run func(context.Context) (TurnResult, error)) {
	turnCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.running, w.cancelTurn = true, cancel
	w.liveID, w.tools = 0, nil
	w.step, w.afterResult, w.calls = 1, false, 0
	w.stepTextLen, w.stepTools = 0, 0
	w.bubbleID, w.approvalQueue = 0, nil
	w.approvalTotal, w.approvalAllowed, w.approvalRejected = 0, 0, 0
	w.mu.Unlock()
	w.turnWG.Add(1)
	go func() {
		defer w.turnWG.Done()
		defer cancel()
		w.turn(turnCtx, trigger, session, run)
		w.mu.Lock()
		w.running, w.cancelTurn = false, nil
		w.mu.Unlock()
	}()
}

type turnOutcome struct {
	result TurnResult
	err    error
}

// resolveFrameAgent fills the agent name of a child frame so its tool lines can
// be labelled. The root turn's frames keep the lead's plain line style.
func (w *chat) resolveFrameAgent(ctx context.Context, frame *core.LiveFrame, root string) {
	if frame.SessionID == root || frame.Agent != "" {
		return
	}
	// Only tool frames are shown for a child; its text is not part of the reply.
	if frame.Kind != "tool_call" && frame.Kind != "tool_result" {
		return
	}
	if info, err := w.ch.backend.SessionInfo(ctx, frame.SessionID); err == nil {
		frame.Agent = info.Agent
	}
}

func (w *chat) turn(ctx context.Context, trigger int64, session string, run func(context.Context) (TurnResult, error)) {
	started := time.Now()
	api := w.ch.api
	_ = api.SendChatAction(ctx, w.id, "typing")
	liveID, err := api.SendMessage(ctx, w.id, "…", SendOptions{ReplyTo: trigger})
	if err != nil {
		w.ch.apiErr("sendMessage", err)
	}
	w.mu.Lock()
	w.liveID = liveID
	w.mu.Unlock()

	sub, err := w.ch.backend.SubscribeLive(ctx, session)
	if err != nil {
		w.finish(ctx, liveID, "", turnOutcome{err: err}, session)
		return
	}
	defer sub.Close()
	readCtx, stopRead := context.WithCancel(ctx)
	defer stopRead()
	frames := make(chan core.LiveFrame, 256)
	go func() {
		for {
			received, err := sub.Recv(readCtx)
			if err != nil {
				return
			}
			if received.Lagged > 0 {
				continue
			}
			frame := received.Value
			w.resolveFrameAgent(readCtx, &frame, session)
			select {
			case frames <- frame:
			case <-readCtx.Done():
				return
			}
		}
	}()
	done := make(chan turnOutcome, 1)
	go func() {
		result, err := run(ctx)
		done <- turnOutcome{result: result, err: err}
	}()

	var text strings.Builder
	afterCall := false
	apply := func(frame core.LiveFrame) {
		lead := frame.SessionID == session
		// Child text deltas are not part of the lead's answer; child tool calls
		// still update the status line (REQ-TEAM-024).
		if !lead && frame.Kind != "tool_call" && frame.Kind != "tool_result" {
			return
		}
		agent := ""
		if !lead {
			agent = frame.Agent
		}
		switch frame.Kind {
		case "text_delta":
			if afterCall && text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
				text.WriteString("\n\n")
			}
			afterCall = false
			text.WriteString(frame.Text)
			w.mu.Lock()
			if w.afterResult {
				w.step, w.afterResult = w.step+1, false
			}
			w.mu.Unlock()
		case "tool_call":
			w.mu.Lock()
			w.calls++
			if lead && w.afterResult {
				w.step, w.afterResult = w.step+1, false
			}
			w.tools = append(w.tools, &toolLine{callID: frame.CallID, agent: agent, name: frame.Name, arg: toolfmt.Cut(toolfmt.PrimaryArg(frame.Name, frame.Input), 60)})
			w.mu.Unlock()
			afterCall = true
		case "tool_result":
			w.mu.Lock()
			for _, line := range w.tools {
				if line.callID == frame.CallID {
					line.done = true
				}
			}
			if lead {
				w.afterResult = true
				// The next step starts here; a step_retry rewinds to this mark.
				w.stepTextLen = text.Len()
				w.stepTools = len(w.tools)
			}
			w.mu.Unlock()
		case "step_retry":
			// REQ-LTC-032: drop the partial text and tool lines of the failed
			// step; the retried step streams them again.
			w.mu.Lock()
			if text.Len() > w.stepTextLen {
				trimmed := text.String()[:w.stepTextLen]
				text.Reset()
				text.WriteString(trimmed)
			}
			if len(w.tools) > w.stepTools {
				w.tools = w.tools[:w.stepTools]
			}
			w.mu.Unlock()
			afterCall = false
		}
	}
	edit := time.NewTicker(w.ch.EditInterval)
	defer edit.Stop()
	typing := time.NewTicker(w.ch.TypingInterval)
	defer typing.Stop()
	dirty, lastSent := false, ""
	var out turnOutcome
loop:
	for {
		select {
		case frame := <-frames:
			apply(frame)
			dirty = true
		case <-edit.C:
			if !dirty || liveID == 0 {
				continue
			}
			dirty = false
			body := tgfmt.Split(w.render(text.String(), "", true), tgfmt.Limit)
			if len(body) > 0 && body[0] != lastSent {
				lastSent = body[0]
				w.ch.apiErr("editMessageText", w.editHTML(ctx, liveID, body[0], nil))
			}
		case <-typing.C:
			_ = api.SendChatAction(ctx, w.id, "typing")
		case out = <-done:
			break loop
		}
	}
	// Frames published before RunTurn returned may still be in flight. Drain
	// until the reader and the forwarding channel are quiet for a short grace
	// period.
	deadline := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case frame := <-frames:
			apply(frame)
		case <-deadline:
			break drain
		case <-time.After(20 * time.Millisecond):
			if sub.Len() == 0 && len(frames) == 0 {
				break drain
			}
		}
	}
	answer := text.String()
	if strings.TrimSpace(answer) == "" {
		answer = out.result.Text
	}
	w.mu.Lock()
	w.usageIn += out.result.InputTokens
	w.usageOut += out.result.OutputTokens
	w.mu.Unlock()
	w.finish(ctx, liveID, answer, out, session)
	slog.Info("telegram: turn finished", "chat_id", w.id, "session_id", session, "stop_reason", out.result.StopReason, "duration", time.Since(started).Round(time.Millisecond))
}

// shortTool returns the display name of a tool: the part after the last "__"
// for MCP tools.
func shortTool(name string) string {
	if i := strings.LastIndex(name, "__"); i >= 0 {
		return name[i+2:]
	}
	return name
}

// displayTool labels a tool with the child agent name when it is not the lead's.
func displayTool(agent, name string) string {
	name = shortTool(name)
	if agent != "" {
		return agent + " · " + name
	}
	return name
}

// endLine returns the closing status line of an abnormal turn, or "" for a
// normal end (API-TFB-005).
func (w *chat) endLine(out turnOutcome) string {
	switch {
	case out.err != nil && errors.Is(out.err, context.Canceled), out.result.StopReason == "cancelled":
		return "⊘ Stopped by you."
	case out.result.StopReason == "interrupted":
		return "⚠ lana restarted while working."
	case out.err != nil, out.result.StopReason == "error":
		message := ""
		if out.err != nil {
			message = out.err.Error()
		}
		return "✗ Stopped by an error: " + message
	case out.result.StopReason == "max_steps":
		return fmt.Sprintf("⚠ Stopped after the step limit (%d steps).", w.stepCount())
	}
	return ""
}

// stepCount returns the number of steps the turn ran, at least 1.
func (w *chat) stepCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.step < 1 {
		return 1
	}
	return w.step
}

// continueKeyboard is the inline keyboard of an abnormal end message
// (API-TFB-005).
func continueKeyboard(sessionID string) *Keyboard {
	return &Keyboard{Rows: [][]Button{{{Text: "Continue", Data: "c:" + sessionID}}}}
}

// statusLine returns the live status line: the running tool and step, or the
// writing step when no tool runs (REQ-TGL-001).
func (w *chat) statusLine() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	step := w.step
	if step < 1 {
		step = 1
	}
	for i := len(w.tools) - 1; i >= 0; i-- {
		if w.tools[i].done {
			continue
		}
		return fmt.Sprintf("⏳ %s · step %d", displayTool(w.tools[i].agent, w.tools[i].name), step)
	}
	return fmt.Sprintf("✍️ step %d", step)
}

// render builds the message HTML. While running it is the status line and the
// answer; when final it is the answer and the end line. The footer is sent as a
// separate message (sendFooter), never part of the answer.
func (w *chat) render(answer, end string, running bool) string {
	sections := []string{}
	if running {
		sections = append(sections, html.EscapeString(w.statusLine()))
	}
	if body := tgfmt.ToHTML(answer); body != "" {
		sections = append(sections, body)
	}
	if !running && end != "" {
		sections = append(sections, html.EscapeString(end))
	}
	return strings.Join(sections, "\n\n")
}

// footer returns the closing stats line, or "" when the turn made no tool call.
func (w *chat) footer() string {
	w.mu.Lock()
	steps, calls := w.step, w.calls
	w.mu.Unlock()
	if calls == 0 {
		return ""
	}
	if steps < 1 {
		steps = 1
	}
	return fmt.Sprintf("⚙️ %s · %s", plural(steps, "step"), plural(calls, "tool call"))
}

// plural renders "<n> <unit>" with an English plural suffix.
func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// summaryText is the final permission bubble text (REQ-TGL-003).
func (w *chat) summaryText() string {
	w.mu.Lock()
	total, allowed, rejected := w.approvalTotal, w.approvalAllowed, w.approvalRejected
	w.mu.Unlock()
	parts := []string{}
	if allowed > 0 {
		parts = append(parts, fmt.Sprintf("%d allowed", allowed))
	}
	if rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d rejected", rejected))
	}
	summary := "🔐 " + plural(total, "approval")
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	return summary
}

// finish writes the final answer. With a permission bubble it edits the bubble
// to the summary, sends the answer as new messages, and deletes the live
// message; otherwise it edits the live message in place (REQ-TGL-003). The
// footer, when the turn made a tool call, follows the answer as its own message.
// An abnormal end attaches a Continue button to the final message (API-TFB-005).
func (w *chat) finish(ctx context.Context, liveID int64, answer string, out turnOutcome, session string) {
	defer w.resetTurn()
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	end := w.endLine(out)
	body := w.render(answer, end, false)
	if strings.TrimSpace(answer) == "" && end == "" && !w.hasTools() {
		body = "(no answer)"
	}
	parts := tgfmt.Split(body, tgfmt.Limit)
	if len(parts) == 0 {
		parts = []string{"(no answer)"}
	}
	var keyboard *Keyboard
	if end != "" && session != "" {
		keyboard = continueKeyboard(session)
	}
	if !w.hasBubble() {
		w.editLive(ctx, liveID, parts, keyboard)
	} else {
		w.editBubble(ctx, w.summaryText())
		if !w.sendParts(ctx, parts, keyboard) {
			w.editLive(ctx, liveID, parts, keyboard)
		} else if liveID != 0 {
			w.ch.apiErr("deleteMessage", w.ch.api.DeleteMessage(ctx, w.id, liveID))
		}
	}
	w.sendFooter(ctx)
}

// sendFooter sends the closing stats footer as its own plain message after the
// final answer, in the same chat, thread, and reply target as the answer. It
// sends nothing when the turn made no tool call. A failed send is logged and
// does not fail the turn (REQ-TGL-003).
func (w *chat) sendFooter(ctx context.Context) {
	footer := w.footer()
	if footer == "" {
		return
	}
	_, err := w.ch.api.SendMessage(ctx, w.id, footer, SendOptions{})
	w.ch.apiErr("sendMessage", err)
}

// editLive edits the live message with the first part and sends the rest. The
// keyboard, when set, goes on the last part.
func (w *chat) editLive(ctx context.Context, liveID int64, parts []string, keyboard *Keyboard) {
	for i, part := range parts {
		var keys *Keyboard
		if i == len(parts)-1 {
			keys = keyboard
		}
		if i == 0 && liveID != 0 {
			w.ch.apiErr("editMessageText", w.editHTML(ctx, liveID, part, keys))
			continue
		}
		_, err := w.sendHTML(ctx, part, SendOptions{Keyboard: keys})
		w.ch.apiErr("sendMessage", err)
	}
}

// sendParts sends the final answer as new messages. It reports whether the
// first part was sent, so finish can fall back to the live message when it was
// not. The keyboard, when set, goes on the last part.
func (w *chat) sendParts(ctx context.Context, parts []string, keyboard *Keyboard) bool {
	for i, part := range parts {
		var keys *Keyboard
		if i == len(parts)-1 {
			keys = keyboard
		}
		_, err := w.sendHTML(ctx, part, SendOptions{Keyboard: keys})
		if err == nil {
			continue
		}
		w.ch.apiErr("sendMessage", err)
		if i == 0 {
			return false
		}
	}
	return true
}

func (w *chat) hasTools() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls > 0
}

func (w *chat) hasBubble() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.bubbleID != 0
}

// editBubble edits the turn's permission bubble and removes its buttons.
func (w *chat) editBubble(ctx context.Context, text string) {
	w.mu.Lock()
	messageID := w.bubbleID
	w.mu.Unlock()
	if messageID == 0 {
		return
	}
	editCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	w.ch.apiErr("editMessageText", w.ch.api.EditMessageText(editCtx, w.id, messageID, text, SendOptions{}))
}

// resetTurn clears the per-turn layout state.
func (w *chat) resetTurn() {
	w.mu.Lock()
	w.liveID, w.tools = 0, nil
	w.step, w.afterResult, w.calls = 0, false, 0
	w.stepTextLen, w.stepTools = 0, 0
	w.bubbleID, w.approvalQueue = 0, nil
	w.approvalTotal, w.approvalAllowed, w.approvalRejected = 0, 0, 0
	w.mu.Unlock()
}

// toolArg returns the shown argument of a tool call of the running turn.
func (w *chat) toolArg(callID string) (string, int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, line := range w.tools {
		if line.callID == callID {
			return line.arg, w.liveID
		}
	}
	return "", w.liveID
}

func (w *chat) handleCommand(ctx context.Context, m *Message, text string) {
	fields := strings.Fields(text)
	name := strings.TrimPrefix(fields[0], "/")
	if at := strings.Index(name, "@"); at >= 0 {
		if !strings.EqualFold(name[at+1:], w.ch.bot.Username) {
			return
		}
		name = name[:at]
	}
	args := fields[1:]
	owner := m.From != nil && w.ch.isOwner(m.Chat.Type, m.From.ID)
	switch name {
	case "new", "agent", "compact", "cancel":
		if isGroup(m.Chat.Type) && !owner {
			w.reply(ctx, m.MessageID, "✗ only the owner can use /"+name)
			return
		}
	}
	w.mu.Lock()
	running, session := w.running, w.session
	w.mu.Unlock()
	switch name {
	case "start", "help":
		w.reply(ctx, m.MessageID, "I'm "+w.currentAgent(ctx)+". Commands: /new /agent /memory /mcp /skills /compact /cancel /status")
	case "new":
		if running {
			w.reply(ctx, m.MessageID, waitReply)
			return
		}
		id, _, err := w.useSession(ctx, w.currentAgent(ctx), true)
		if err != nil {
			w.reply(ctx, m.MessageID, sessionErrText(err))
			return
		}
		w.reply(ctx, m.MessageID, "· new session "+id)
	case "agent":
		if len(args) > 0 {
			w.switchAgent(ctx, m.MessageID, args[0])
			return
		}
		rows := [][]Button{}
		for _, agent := range w.agentNames() {
			rows = append(rows, []Button{{Text: agent, Data: "g:" + agent}})
		}
		_, err := w.ch.api.SendMessage(ctx, w.id, "Current agent: "+w.currentAgent(ctx), SendOptions{ReplyTo: m.MessageID, Keyboard: &Keyboard{Rows: rows}})
		w.ch.apiErr("sendMessage", err)
	case "memory":
		user, memory, err := w.ch.backend.Memory(w.currentAgent(ctx))
		if err != nil {
			w.reply(ctx, m.MessageID, "✗ memory: "+err.Error())
			return
		}
		lines := memoryLines("user", user, agenthome.UserLimit)
		lines = append(lines, memoryLines("memory", memory, agenthome.MemoryLimit)...)
		w.reply(ctx, m.MessageID, strings.Join(lines, "\n"))
	case "mcp":
		agent := w.currentAgent(ctx)
		w.reply(ctx, m.MessageID, strings.Join(mcpLines(w.ch.backend.MCPStatus(agent), agent), "\n"))
	case "skills":
		agent := w.currentAgent(ctx)
		w.reply(ctx, m.MessageID, strings.Join(skillLines(w.ch.backend.Skills(agent), agent), "\n"))
	case "compact":
		if running {
			w.reply(ctx, m.MessageID, waitReply)
			return
		}
		session, _, err := w.ensureSession(ctx)
		if err != nil {
			w.reply(ctx, m.MessageID, sessionErrText(err))
			return
		}
		w.reply(ctx, m.MessageID, "· compacting…")
		before, after, err := w.ch.backend.Compact(ctx, session)
		if err != nil {
			w.reply(ctx, m.MessageID, "✗ compaction: "+err.Error())
			return
		}
		w.reply(ctx, m.MessageID, fmt.Sprintf("· compaction %d → %d token", before, after))
	case "cancel":
		if !running {
			w.reply(ctx, m.MessageID, "· nothing to cancel")
			return
		}
		if err := w.ch.backend.Cancel(ctx, session); err != nil {
			w.reply(ctx, m.MessageID, "✗ "+err.Error())
		}
	case "status":
		session, agent, err := w.ensureSession(ctx)
		if err != nil {
			w.reply(ctx, m.MessageID, sessionErrText(err))
			return
		}
		info, err := w.ch.backend.SessionInfo(ctx, session)
		if err != nil {
			w.reply(ctx, m.MessageID, "✗ "+err.Error())
			return
		}
		policy, _ := w.ch.backend.Policy(ctx, session)
		w.reply(ctx, m.MessageID, fmt.Sprintf("agent %s · model %s · policy %s · session %s · %d turns", agent, w.ch.backend.AgentModel(agent), policy, session, info.TurnCount))
	default:
		w.reply(ctx, m.MessageID, "· unknown command: /"+name)
	}
}

func (w *chat) switchAgent(ctx context.Context, replyTo int64, agent string) {
	w.mu.Lock()
	running := w.running
	w.mu.Unlock()
	if running {
		w.reply(ctx, replyTo, waitReply)
		return
	}
	names := w.agentNames()
	known := false
	for _, name := range names {
		known = known || name == agent
	}
	if !known {
		w.reply(ctx, replyTo, fmt.Sprintf("✗ unknown agent %q (available: %s)", agent, strings.Join(names, ", ")))
		return
	}
	if _, _, err := w.useSession(ctx, agent, false); err != nil {
		w.reply(ctx, replyTo, sessionErrText(err))
		return
	}
	w.reply(ctx, replyTo, "· agent: "+agent)
}

func memoryLines(target string, entries []string, limit int) []string {
	lines := []string{fmt.Sprintf("· %s %d/%d characters, %d entries", target, agenthome.Size(entries), limit, len(entries))}
	for i, entry := range entries {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, strings.ReplaceAll(entry, "\n", "\n     ")))
	}
	return lines
}

// mcpLines renders the MCP server status of an agent.
func mcpLines(statuses []core.MCPServerStatus, agent string) []string {
	if len(statuses) == 0 {
		return []string{"· no MCP servers for " + agent}
	}
	lines := make([]string, 0, len(statuses))
	for _, status := range statuses {
		switch status.State {
		case "running":
			lines = append(lines, fmt.Sprintf("· %s running, %d tools", status.Name, status.Tools))
		case "stopped":
			lines = append(lines, "· "+status.Name+" stopped")
		case "unavailable":
			lines = append(lines, fmt.Sprintf("· %s unavailable (%s)", status.Name, status.Reason))
		default:
			lines = append(lines, "· "+status.Name+" "+status.State)
		}
	}
	return lines
}

// skillLines renders the skills visible to an agent.
func skillLines(skills []agenthome.Skill, agent string) []string {
	if len(skills) == 0 {
		return []string{"· no skills for " + agent}
	}
	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		lines = append(lines, fmt.Sprintf("· %s (%s): %s", skill.Name, skill.Scope, skill.Description))
	}
	return lines
}

func (w *chat) handleCallback(ctx context.Context, q *CallbackQuery) {
	chatType := ""
	if q.Message != nil {
		chatType = q.Message.Chat.Type
	}
	owner := w.ch.isOwner(chatType, q.From.ID)
	kind, rest, _ := strings.Cut(q.Data, ":")
	switch kind {
	case "a":
		w.ch.answerApproval(ctx, q, rest, owner)
	case "q":
		w.ch.answerQuestion(ctx, q, rest, owner)
	case "g":
		if !owner {
			w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, "only the owner can switch agents"))
			return
		}
		w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, ""))
		var replyTo int64
		if q.Message != nil {
			replyTo = q.Message.MessageID
		}
		w.switchAgent(ctx, replyTo, rest)
	case "c":
		w.answerContinue(ctx, q, rest, owner)
	default:
		w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, ""))
	}
}

// answerContinue handles a press on the Continue button of an abnormal end
// message (data "c:<session id>"). Only the owner may press it; the press
// removes the button and starts the continue turn, or answers with the refusal
// text (API-TFB-005).
func (w *chat) answerContinue(ctx context.Context, q *CallbackQuery, sessionID string, owner bool) {
	if !owner {
		w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, "only the owner can continue"))
		return
	}
	var messageID int64
	if q.Message != nil {
		messageID = q.Message.MessageID
	}
	if messageID != 0 {
		w.ch.apiErr("editMessageReplyMarkup", w.ch.api.EditMessageReplyMarkup(ctx, w.id, messageID, nil))
	}
	if err := w.ch.backend.CanContinue(ctx, sessionID); err != nil {
		w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, err.Error()))
		return
	}
	w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, ""))
	w.startContinueTurn(ctx, messageID, sessionID)
}

func containsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}

func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lower, lowerOld := strings.ToLower(s), strings.ToLower(old)
	var out strings.Builder
	for {
		i := strings.Index(lower, lowerOld)
		if i < 0 {
			out.WriteString(s)
			return out.String()
		}
		out.WriteString(s[:i])
		out.WriteString(repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}
