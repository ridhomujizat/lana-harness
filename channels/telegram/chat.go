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
)

// chat is the worker of one Telegram chat. It handles the chat's updates in order and
// runs at most one turn at a time in a child goroutine.
type chat struct {
	ch         *Channel
	id         int64
	routeAgent string
	updates    chan Update

	mu         sync.Mutex
	agent      string
	session    string
	running    bool
	cancelTurn context.CancelFunc
	liveID     int64
	tools      []*toolLine
	usageIn    int64
	usageOut   int64
	turnWG     sync.WaitGroup
}

type toolLine struct {
	callID string
	name   string
	arg    string
	mark   string
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

// editHTML edits a message with HTML, falling back to plain text when Telegram cannot parse it.
func (w *chat) editHTML(ctx context.Context, messageID int64, text string) error {
	err := w.ch.api.EditMessageText(ctx, w.id, messageID, text, SendOptions{ParseMode: "HTML"})
	if isParseError(err) {
		err = w.ch.api.EditMessageText(ctx, w.id, messageID, tgfmt.Plain(text), SendOptions{})
	}
	return err
}

func isParseError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Code == 400 && containsFold(apiErr.Description, "can't parse entities")
}

// startTurn runs one turn in a child goroutine.
func (w *chat) startTurn(ctx context.Context, trigger int64, session, text string) {
	turnCtx, cancel := context.WithCancel(ctx)
	w.mu.Lock()
	w.running, w.cancelTurn, w.tools, w.liveID = true, cancel, nil, 0
	w.mu.Unlock()
	w.turnWG.Add(1)
	go func() {
		defer w.turnWG.Done()
		defer cancel()
		w.turn(turnCtx, trigger, session, text)
		w.mu.Lock()
		w.running, w.cancelTurn = false, nil
		w.mu.Unlock()
	}()
}

type turnOutcome struct {
	result TurnResult
	err    error
}

func (w *chat) turn(ctx context.Context, trigger int64, session, prompt string) {
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
		w.finish(ctx, liveID, "", turnOutcome{err: err})
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
			select {
			case frames <- received.Value:
			case <-readCtx.Done():
				return
			}
		}
	}()
	done := make(chan turnOutcome, 1)
	go func() {
		result, err := w.ch.backend.RunTurn(ctx, session, prompt)
		done <- turnOutcome{result: result, err: err}
	}()

	var text strings.Builder
	afterTool := false
	apply := func(frame core.LiveFrame) {
		if frame.SessionID != session {
			return
		}
		switch frame.Kind {
		case "text_delta":
			if afterTool && text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
				text.WriteString("\n\n")
			}
			afterTool = false
			text.WriteString(frame.Text)
		case "tool_call":
			w.mu.Lock()
			w.tools = append(w.tools, &toolLine{callID: frame.CallID, name: frame.Name, arg: toolfmt.Cut(toolfmt.PrimaryArg(frame.Name, frame.Input), 60)})
			w.mu.Unlock()
			afterTool = true
		case "tool_result":
			w.mu.Lock()
			for _, line := range w.tools {
				if line.callID == frame.CallID {
					line.mark = statusMark(frame.Status)
				}
			}
			w.mu.Unlock()
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
			body := tgfmt.Split(w.render(text.String(), ""), tgfmt.Limit)
			if len(body) > 0 && body[0] != lastSent {
				lastSent = body[0]
				w.ch.apiErr("editMessageText", w.editHTML(ctx, liveID, body[0]))
			}
		case <-typing.C:
			_ = api.SendChatAction(ctx, w.id, "typing")
		case out = <-done:
			break loop
		}
	}
	// Frames published before RunTurn returned may still be in flight.
	deadline := time.After(500 * time.Millisecond)
drain:
	for sub.Len() > 0 || len(frames) > 0 {
		select {
		case frame := <-frames:
			apply(frame)
		case <-deadline:
			break drain
		case <-time.After(20 * time.Millisecond):
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
	w.finish(ctx, liveID, answer, out)
	slog.Info("telegram: turn finished", "chat_id", w.id, "session_id", session, "stop_reason", out.result.StopReason, "duration", time.Since(started).Round(time.Millisecond))
}

func statusMark(status string) string {
	switch status {
	case string(core.ToolStatusSuccess):
		return "✓"
	case string(core.ToolStatusError):
		return "✗"
	}
	return "⊘"
}

// endLine returns the closing status line of a turn, or "".
func endLine(out turnOutcome) string {
	switch {
	case out.err != nil && errors.Is(out.err, context.Canceled), out.result.StopReason == "cancelled":
		return "⊘ cancelled"
	case out.err != nil:
		return "✗ " + out.err.Error()
	case out.result.StopReason == "max_steps":
		return "⚠ stopped at max_steps"
	}
	return ""
}

// render builds the live message HTML: tool lines, the answer, and an optional end line.
func (w *chat) render(answer, end string) string {
	w.mu.Lock()
	lines := make([]string, 0, len(w.tools))
	for _, line := range w.tools {
		row := "🔧 " + strings.TrimSpace(line.name+" "+line.arg)
		if line.mark != "" {
			row += " " + line.mark
		}
		lines = append(lines, html.EscapeString(row))
	}
	w.mu.Unlock()
	sections := []string{}
	if len(lines) > 0 {
		sections = append(sections, strings.Join(lines, "\n"))
	}
	if body := tgfmt.ToHTML(answer); body != "" {
		sections = append(sections, body)
	}
	if end != "" {
		sections = append(sections, html.EscapeString(end))
	}
	if len(sections) == 0 {
		return "…"
	}
	return strings.Join(sections, "\n\n")
}

// finish writes the final live message and any overflow messages.
func (w *chat) finish(ctx context.Context, liveID int64, answer string, out turnOutcome) {
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
	}
	end := endLine(out)
	body := w.render(answer, end)
	if strings.TrimSpace(answer) == "" && end == "" && !w.hasTools() {
		body = "(no answer)"
	}
	parts := tgfmt.Split(body, tgfmt.Limit)
	for i, part := range parts {
		if i == 0 && liveID != 0 {
			w.ch.apiErr("editMessageText", w.editHTML(ctx, liveID, part))
			continue
		}
		_, err := w.sendHTML(ctx, part, SendOptions{})
		w.ch.apiErr("sendMessage", err)
	}
}

func (w *chat) hasTools() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.tools) > 0
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
		w.reply(ctx, m.MessageID, "I'm "+w.currentAgent(ctx)+". Commands: /new /agent /memory /compact /cancel /status")
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
		for _, agent := range w.ch.backend.AgentNames() {
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
	names := w.ch.backend.AgentNames()
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
	default:
		w.ch.apiErr("answerCallbackQuery", w.ch.api.AnswerCallbackQuery(ctx, q.ID, ""))
	}
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
