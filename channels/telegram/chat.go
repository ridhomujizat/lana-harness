package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
	"github.com/ridhomujizat/lana-harness/internal/attach"
	"github.com/ridhomujizat/lana-harness/internal/tgfmt"
	"github.com/ridhomujizat/lana-harness/internal/toolfmt"
)

const (
	// unsupportedMediaReply answers voice, audio, video, video notes, stickers,
	// and animations (REQ-ATT-005).
	unsupportedMediaReply = "I can read photos, PDFs, and text files. Voice, audio, and video are not supported yet."
	waitReply             = "· wait for the turn to finish"
	// drainingReply is the text a chat gets while the daemon drains
	// (REQ-TFB-002, API-TFB-008).
	drainingReply = "lana is restarting. Try again in a moment."
	// albumLimitReply answers an album with more parts than attach.MaxFiles
	// (REQ-ATT-005).
	albumLimitReply = "· only the first 10 files were read"
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
	// turnStarted is when the running turn began; the live status line shows
	// the time elapsed since then.
	turnStarted time.Time
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

	// albums buffers the parts of each in-flight album, keyed by media group
	// ID, and albumCh wakes run to flush one. Both are touched only from the
	// chat worker goroutine (REQ-ATT-005, AC-ATT-006).
	albums  map[string]*album
	albumCh chan albumKey
}

// album is one Telegram album being buffered while its parts arrive. gen
// counts the parts; a flush whose generation is stale is ignored.
type album struct {
	parts []*Message
	gen   int
	timer *time.Timer
}

// albumKey identifies one album flush: the media group and the generation of
// the album when the flush timer was armed. A flush handles every part queued
// before it (REQ-ATT-005), so a key whose generation is stale never flushes a
// newer buffer.
type albumKey struct {
	key string
	gen int
}

type toolLine struct {
	callID string
	agent  string
	name   string
	arg    string
	done   bool
}

func (w *chat) address() string { return strconv.FormatInt(w.id, 10) }

// run handles the chat's updates in order and flushes albums whose wait
// elapsed. Before a flush it takes the updates already queued, so a part that
// arrived while the worker was busy joins its album instead of starting a
// second turn (REQ-ATT-005, AC-ATT-006).
func (w *chat) run(ctx context.Context) {
	defer w.turnWG.Wait()
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-w.albumCh:
			w.drainQueued(ctx)
			w.flushAlbum(ctx, evt)
		case update := <-w.updates:
			w.handleUpdate(ctx, update)
		}
	}
}

// handleUpdate handles one update: a CallbackQuery to handleCallback, a Message
// to handleMessage.
func (w *chat) handleUpdate(ctx context.Context, update Update) {
	switch {
	case update.CallbackQuery != nil:
		w.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		w.handleMessage(ctx, update.Message)
	}
}

// drainQueued handles the updates already queued for the chat worker. The
// snapshot length is exact because the worker is the only receiver of
// w.updates; a queued album part re-arms its flush timer in bufferAlbum and so
// makes the pending flush stale (REQ-ATT-005). It stops early when ctx is done.
func (w *chat) drainQueued(ctx context.Context) {
	for n := len(w.updates); n > 0; n-- {
		if ctx.Err() != nil {
			return
		}
		w.handleUpdate(ctx, <-w.updates)
	}
}

func isGroup(chatType string) bool { return chatType == "group" || chatType == "supergroup" }

func (w *chat) repliesToBot(m *Message) bool {
	return m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.ID == w.ch.bot.ID
}

func (w *chat) handleMessage(ctx context.Context, m *Message) {
	group := isGroup(m.Chat.Type)
	if m.isFileMessage() {
		w.handleMedia(ctx, m, group)
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		if m.hasUnsupportedMedia() && (!group || w.repliesToBot(m)) {
			w.reply(ctx, m.MessageID, unsupportedMediaReply)
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

// addressed reports whether a message in a group is for the bot: it replies to
// the bot or its caption mentions the bot (REQ-ATT-005).
func (w *chat) addressed(m *Message) bool {
	return w.repliesToBot(m) || containsFold(m.Caption, "@"+w.ch.bot.Username)
}

// handleMedia routes a photo or document message. A single message is handled
// at once, as a group of one; an album part is buffered until the album is
// complete (REQ-ATT-005, AC-ATT-006).
func (w *chat) handleMedia(ctx context.Context, m *Message, group bool) {
	// A document whose caption is /save stores the file instead of starting a
	// turn (REQ-KNU-012, API-KNU-002).
	if m.Document != nil && w.isSaveCommand(m.Caption) {
		w.handleCommand(ctx, m, strings.TrimSpace(m.Caption))
		return
	}
	if m.MediaGroupID == "" {
		if group && !w.addressed(m) {
			return
		}
		w.mediaTurn(ctx, []*Message{m})
		return
	}
	w.bufferAlbum(ctx, m)
}

// bufferAlbum appends a part to its album and re-arms the flush timer, so the
// album is handled AlbumWait after its last part (REQ-ATT-005).
func (w *chat) bufferAlbum(ctx context.Context, m *Message) {
	a := w.albums[m.MediaGroupID]
	if a == nil {
		a = &album{}
		w.albums[m.MediaGroupID] = a
	}
	a.parts = append(a.parts, m)
	a.gen++
	gen := a.gen
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = time.AfterFunc(w.ch.AlbumWait, func() {
		select {
		case w.albumCh <- albumKey{key: m.MediaGroupID, gen: gen}:
		case <-ctx.Done():
		}
	})
}

// flushAlbum handles an album whose last part arrived AlbumWait ago, together
// with every part queued before it. A stale generation, or an unaddressed album
// in a group, is dropped (REQ-ATT-005).
func (w *chat) flushAlbum(ctx context.Context, evt albumKey) {
	a := w.albums[evt.key]
	if a == nil || a.gen != evt.gen {
		return
	}
	if a.timer != nil {
		a.timer.Stop()
	}
	delete(w.albums, evt.key)
	if isGroup(a.parts[0].Chat.Type) && !w.albumAddressed(a.parts) {
		return
	}
	w.mediaTurn(ctx, a.parts)
}

// albumAddressed reports whether any part of an album replies to the bot or
// mentions it (REQ-ATT-005).
func (w *chat) albumAddressed(parts []*Message) bool {
	for _, part := range parts {
		if w.addressed(part) {
			return true
		}
	}
	return false
}

// mediaText returns the owner text of a file message: the trimmed caption,
// mention-stripped and name-prefixed when the chat is a group and the caption
// is non-empty, as the text path does (REQ-ATT-005).
func (w *chat) mediaText(m *Message, group bool) string {
	text := strings.TrimSpace(m.Caption)
	if !group || text == "" {
		return text
	}
	text = strings.TrimSpace(replaceFold(text, "@"+w.ch.bot.Username, ""))
	if text == "" {
		return ""
	}
	name := "someone"
	if m.From != nil && m.From.FirstName != "" {
		name = m.From.FirstName
	}
	return name + ": " + text
}

// mediaTurn saves the files of a photo or document message group into the
// session workspace and hands the paths to the text path (REQ-ATT-005,
// AC-ATT-006).
func (w *chat) mediaTurn(ctx context.Context, parts []*Message) {
	trigger := parts[0]
	group := isGroup(trigger.Chat.Type)
	// A drain starts no turn and downloads nothing (REQ-TFB-002, API-TFB-008).
	if w.ch.backend.Draining() {
		w.reply(ctx, trigger.MessageID, drainingReply)
		return
	}
	session, _, err := w.ensureSession(ctx)
	if err != nil {
		w.reply(ctx, trigger.MessageID, sessionErrText(err))
		return
	}
	info, err := w.ch.backend.SessionInfo(ctx, session)
	if err != nil {
		w.reply(ctx, trigger.MessageID, "✗ "+err.Error())
		return
	}

	if len(parts) > attach.MaxFiles {
		// Cut before any download; the extra parts are dropped (REQ-ATT-005).
		parts = parts[:attach.MaxFiles]
		w.reply(ctx, trigger.MessageID, albumLimitReply)
	}

	text := ""
	for _, part := range parts {
		if t := w.mediaText(part, group); text == "" {
			text = t
		}
	}

	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		if path, ok := w.savePart(ctx, trigger.MessageID, info.Workspace, part); ok {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return
	}
	w.handleText(ctx, trigger, text, paths...)
}

// savePart downloads one photo or document into the session workspace and
// returns its workspace-relative path. A part over a limit gets the REQ-ATT-001
// reply and is skipped; another error is reported and the part is skipped.
func (w *chat) savePart(ctx context.Context, replyTo int64, workspace string, m *Message) (string, bool) {
	name, fileID, size, ok := fileOf(m)
	if !ok {
		return "", false
	}
	if size > attach.MaxFileBytes {
		w.reply(ctx, replyTo, tooLargeText(name))
		return "", false
	}
	file, err := w.ch.api.GetFile(ctx, fileID)
	if err != nil {
		w.mediaError(ctx, replyTo, name, err)
		return "", false
	}
	if file.FileSize > attach.MaxFileBytes {
		w.reply(ctx, replyTo, tooLargeText(name))
		return "", false
	}
	var buf bytes.Buffer
	if err := w.ch.api.Download(ctx, file.FilePath, &buf, attach.MaxFileBytes); err != nil {
		w.mediaError(ctx, replyTo, name, err)
		return "", false
	}
	info, err := attach.Save(workspace, name, &buf, w.ch.now())
	if err != nil {
		w.mediaError(ctx, replyTo, name, err)
		return "", false
	}
	return info.Path, true
}

// mediaError replies to a failed part: a file over the limit gets the
// REQ-ATT-001 text, any other error names the file.
func (w *chat) mediaError(ctx context.Context, replyTo int64, name string, err error) {
	if errors.Is(err, attach.ErrTooLarge) {
		w.reply(ctx, replyTo, tooLargeText(name))
		return
	}
	w.reply(ctx, replyTo, "✗ "+name+": "+err.Error())
}

// fileOf returns the saved name, file ID, and declared size of a photo or
// document message. A photo uses its largest size (REQ-ATT-005).
func fileOf(m *Message) (name, fileID string, size int64, ok bool) {
	if len(m.Photo) > 0 {
		p := m.Photo[len(m.Photo)-1]
		return fmt.Sprintf("photo-%d-1.jpg", m.MessageID), p.FileID, p.FileSize, true
	}
	if m.Document != nil {
		name := m.Document.FileName
		if name == "" {
			name = "file"
		}
		return name, m.Document.FileID, m.Document.FileSize, true
	}
	return "", "", 0, false
}

// tooLargeText is the REQ-ATT-001 reply for a file over the per-file limit.
func tooLargeText(name string) string {
	return fmt.Sprintf("✗ %s is larger than 20 MB, the limit per file.", name)
}

func (w *chat) handleText(ctx context.Context, m *Message, text string, attachments ...string) {
	// A drain starts no turn and queues nothing (REQ-TFB-002, API-TFB-008).
	if w.ch.backend.Draining() {
		w.reply(ctx, m.MessageID, drainingReply)
		return
	}
	// A message with attachments never answers a pending question (REQ-ATT-005).
	if len(attachments) == 0 && w.captureQuestionText(ctx, m, text) {
		return
	}
	w.mu.Lock()
	running, session := w.running, w.session
	w.mu.Unlock()
	if running {
		if err := w.ch.backend.Send(ctx, session, text, attachments...); err != nil {
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
	w.startTurn(ctx, m.MessageID, session, text, attachments...)
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
func (w *chat) startTurn(ctx context.Context, trigger int64, session, text string, attachments ...string) {
	w.launchTurn(ctx, trigger, session, func(ctx context.Context) (TurnResult, error) {
		return w.ch.backend.RunTurn(ctx, session, text, attachments...)
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
	w.liveID, w.tools, w.turnStarted = 0, nil, w.ch.now()
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
	liveID, err := api.SendMessage(ctx, w.id, html.EscapeString(liveStart), SendOptions{ReplyTo: trigger})
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
	// The live message starts as liveStart. Every edit tick re-renders it, even
	// without a new frame, so the spinner and the elapsed time move; an edit is
	// sent only when the text changed.
	lastSent := html.EscapeString(liveStart)
	var out turnOutcome
loop:
	for {
		select {
		case frame := <-frames:
			apply(frame)
		case <-edit.C:
			if liveID == 0 {
				continue
			}
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
	case out.result.StopReason == "loop":
		return "⚠ Stopped: lana kept repeating a step without progress."
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

// liveStart is the text of the live message when a turn starts, before the
// first edit tick replaces it with the animated status line.
const liveStart = "⏳ Thinking…"

// spinnerFrames are the frames of the live status spinner. The frame follows
// the elapsed time in edit intervals, so it moves on every edit tick.
var spinnerFrames = []string{"◐", "◓", "◑", "◒"}

// statusArgLimit is the most runes of a tool argument the status line shows.
const statusArgLimit = 40

// statusLine returns the live status line: a spinner frame, the activity, the
// step and the time elapsed since the turn started (REQ-TGL-001). The activity
// is the running tool and its short argument, "Writing…" while the answer of
// the current step streams, or "Thinking…" before it does.
func (w *chat) statusLine(writing bool) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	step := w.step
	if step < 1 {
		step = 1
	}
	elapsed := time.Duration(0)
	if !w.turnStarted.IsZero() {
		elapsed = max(w.ch.now().Sub(w.turnStarted), 0)
	}
	spinner := spinnerFrames[0]
	if interval := w.ch.EditInterval; interval > 0 {
		spinner = spinnerFrames[int(elapsed/interval)%len(spinnerFrames)]
	}
	activity := "Thinking…"
	if writing && !w.afterResult {
		activity = "Writing…"
	}
	for i := len(w.tools) - 1; i >= 0; i-- {
		if w.tools[i].done {
			continue
		}
		activity = displayTool(w.tools[i].agent, w.tools[i].name)
		// A tool without a known primary argument shows its raw JSON input;
		// the status line leaves that out.
		if arg := strings.Join(strings.Fields(w.tools[i].arg), " "); arg != "" && !strings.HasPrefix(arg, "{") && !strings.HasPrefix(arg, "[") {
			activity += " · " + toolfmt.Cut(arg, statusArgLimit)
		}
		break
	}
	return fmt.Sprintf("%s %s · step %d · %s", spinner, activity, step, formatElapsed(elapsed))
}

// formatElapsed renders a turn duration for the status line: "12s", "1m 05s"
// or "1h 02m".
func formatElapsed(d time.Duration) string {
	seconds := int(d / time.Second)
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm %02ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh %02dm", seconds/3600, seconds%3600/60)
}

// render builds the message HTML. While running it is the status line and the
// answer; when final it is the answer and the end line. The footer is sent as a
// separate message (sendFooter), never part of the answer.
func (w *chat) render(answer, end string, running bool) string {
	sections := []string{}
	if running {
		sections = append(sections, html.EscapeString(w.statusLine(strings.TrimSpace(answer) != "")))
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
	w.liveID, w.tools, w.turnStarted = 0, nil, time.Time{}
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
	case "new", "agent", "compact", "cancel", "save":
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
		w.reply(ctx, m.MessageID, "I'm "+w.currentAgent(ctx)+". Commands: /new /agent /memory /mcp /skills /compact /save /cancel /status")
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
	case "save":
		w.handleSave(ctx, m, args)
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
		// A workflow command takes a non-built-in owner command; an unhandled
		// one falls back to the unknown-command reply (API-WF-022).
		if owner {
			if reply, handled := w.ch.workflowCommand(ctx, text); handled {
				w.reply(ctx, m.MessageID, reply)
				return
			}
		}
		w.reply(ctx, m.MessageID, "· unknown command: /"+name)
	}
}

// handleSave stores the target message's document or text, the argument text,
// or the argument URL as a knowledge source with the chat's current agent
// (REQ-KNU-012, API-KNU-002). The target is the replied-to message when set,
// else the command message.
func (w *chat) handleSave(ctx context.Context, m *Message, args []string) {
	saver, ok := w.ch.backend.(KnowledgeSaver)
	if !ok {
		w.reply(ctx, m.MessageID, "✗ knowledge is not available")
		return
	}
	item, ok := w.saveItem(ctx, m, args)
	if !ok {
		return
	}
	path, duplicate, err := saver.SaveKnowledge(ctx, w.address(), w.currentAgent(ctx), item)
	switch {
	case err != nil && err.Error() == "knowledge is not available":
		w.reply(ctx, m.MessageID, "✗ knowledge is not available")
	case err != nil:
		w.reply(ctx, m.MessageID, "✗ "+err.Error())
	case duplicate:
		w.reply(ctx, m.MessageID, "· already saved as "+path)
	default:
		w.reply(ctx, m.MessageID, "✓ saved "+path+" — compiling into wiki pages")
	}
}

// saveItem builds the /save payload and replies itself on a failure. The target
// is the replied-to message when set, else the command message (REQ-KNU-012):
//  1. a document on the target with a supported extension is downloaded;
//  2. else the arguments: one URL is fetched, any other text is stored;
//  3. else the target's text or caption.
func (w *chat) saveItem(ctx context.Context, m *Message, args []string) (SaveItem, bool) {
	target := m
	hasTarget := m.ReplyTo != nil
	if hasTarget {
		target = m.ReplyTo
	}
	if target.Document != nil {
		return w.saveDocument(ctx, m.MessageID, target)
	}
	if len(args) > 0 {
		text := strings.TrimSpace(strings.Join(args, " "))
		if len(args) == 1 && isSaveURL(args[0]) {
			return SaveItem{URL: text}, true
		}
		return SaveItem{Text: text}, true
	}
	if hasTarget {
		if text := strings.TrimSpace(target.Text); text != "" {
			return SaveItem{Text: text}, true
		}
		if text := strings.TrimSpace(target.Caption); text != "" {
			return SaveItem{Text: text}, true
		}
	}
	w.reply(ctx, m.MessageID, "✗ nothing to save: reply to a message or add text")
	return SaveItem{}, false
}

// saveDocument downloads a document on the target and builds a SaveItem with
// the file name without its extension as title (REQ-KNU-012, API-KNU-002). An
// unsupported extension or a download failure gets a reply and ok=false.
func (w *chat) saveDocument(ctx context.Context, replyTo int64, m *Message) (SaveItem, bool) {
	name := m.Document.FileName
	if name == "" {
		name = "file"
	}
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".md", ".txt", ".pdf", ".html":
	default:
		w.reply(ctx, replyTo, "✗ unsupported file type "+ext)
		return SaveItem{}, false
	}
	if m.Document.FileSize > attach.MaxFileBytes {
		w.reply(ctx, replyTo, tooLargeText(name))
		return SaveItem{}, false
	}
	file, err := w.ch.api.GetFile(ctx, m.Document.FileID)
	if err != nil {
		w.mediaError(ctx, replyTo, name, err)
		return SaveItem{}, false
	}
	if file.FileSize > attach.MaxFileBytes {
		w.reply(ctx, replyTo, tooLargeText(name))
		return SaveItem{}, false
	}
	var buf bytes.Buffer
	if err := w.ch.api.Download(ctx, file.FilePath, &buf, attach.MaxFileBytes); err != nil {
		w.mediaError(ctx, replyTo, name, err)
		return SaveItem{}, false
	}
	return SaveItem{
		Title: strings.TrimSuffix(name, filepath.Ext(name)),
		Ext:   ext,
		Data:  buf.Bytes(),
	}, true
}

// isSaveCommand reports whether a caption is the /save command, with an optional
// bot-name suffix (REQ-KNU-012, API-KNU-002).
func (w *chat) isSaveCommand(caption string) bool {
	fields := strings.Fields(caption)
	if len(fields) == 0 {
		return false
	}
	name := strings.TrimPrefix(fields[0], "/")
	if at := strings.Index(name, "@"); at >= 0 {
		if !strings.EqualFold(name[at+1:], w.ch.bot.Username) {
			return false
		}
		name = name[:at]
	}
	return name == "save"
}

// isSaveURL reports whether a /save argument is a single http or https URL
// (API-KNU-002).
func isSaveURL(text string) bool {
	lower := strings.ToLower(text)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
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
	case "w":
		w.answerWorkflow(ctx, q, rest, owner)
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
