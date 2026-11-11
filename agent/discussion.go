package agent

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/roommemory"
)

// Room discussion (spec 47). A team lead's root turn gets a discussionRunner
// on ToolCtx.Discussions; the open discussion of a session lives on the
// registry so the owner's Stop (API-RDS-011) can reach it.

const (
	// discussionParallel is how many member asks of one discussion run at
	// once (REQ-RDS-012). It stays under maxActiveChildren.
	discussionParallel = 3
	// discussionMemoryLimit caps the room memory in prompts (REQ-RDS-041).
	discussionMemoryLimit = 4000
	// discussionReplyWords is the word budget a member is asked to keep.
	discussionReplyWords = 120
)

// discussionMember is one member's state in an open discussion.
type discussionMember struct {
	notes    []noteRef
	angle    string
	childID  string
	asks     int
	seen     int // log length after the member's last reply
	passed   bool
	lastText string
}

// discussionLine is one line of the discussion log members read.
type discussionLine struct {
	name string
	text string
}

// discussionState is the open discussion of one session.
type discussionState struct {
	mu        sync.Mutex
	id        string
	sessionID string
	turnID    string
	team      string
	lead      string
	topic     string
	cfg       core.TeamConfig
	members   map[string]*discussionMember
	order     []string
	log       []discussionLine
	limit     int
	extra     int
	stop      string
	sem       chan struct{}
}

// round is the highest round asked so far.
func (st *discussionState) round() int {
	n := 0
	for _, m := range st.members {
		if m.asks > n {
			n = m.asks
		}
	}
	return n
}

// discussions is the registry's table of open discussions by session id.
type discussions struct {
	mu   sync.Mutex
	open map[string]*discussionState
}

func (d *discussions) get(sessionID string) *discussionState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.open[sessionID]
}

func (d *discussions) put(st *discussionState) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.open == nil {
		d.open = map[string]*discussionState{}
	}
	if _, ok := d.open[st.sessionID]; ok {
		return false
	}
	d.open[st.sessionID] = st
	return true
}

func (d *discussions) drop(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.open, sessionID)
}

// SetDiscussionShare installs what happens after a discussion: channel posts
// and member summaries (REQ-RDS-031, REQ-RDS-033).
func (r *Registry) SetDiscussionShare(share core.DiscussionShare) {
	r.discMu.Lock()
	r.discShare = share
	r.discMu.Unlock()
}

func (r *Registry) discussionShare() core.DiscussionShare {
	r.discMu.Lock()
	defer r.discMu.Unlock()
	return r.discShare
}

// RoomMemory returns the room memory store of the registry's home.
func (r *Registry) RoomMemory() *roommemory.Store {
	r.discMu.Lock()
	defer r.discMu.Unlock()
	if r.roomMemory == nil {
		r.roomMemory = roommemory.New(r.home)
	}
	return r.roomMemory
}

// StopDiscussion marks the open discussion of sessionID stopped
// (API-RDS-011). mode is core.DiscussionStop or core.DiscussionDirect. It
// reports whether a discussion was open.
func (r *Registry) StopDiscussion(ctx context.Context, sessionID, mode string) (bool, error) {
	if mode != core.DiscussionDirect {
		mode = core.DiscussionStop
	}
	st := r.disc.get(sessionID)
	if st == nil {
		return false, nil
	}
	st.mu.Lock()
	already := st.stop != ""
	if !already {
		st.stop = mode
	}
	round := st.round()
	st.mu.Unlock()
	if already {
		return true, nil
	}
	err := r.appendDiscussion(ctx, sessionID, st.turnID, core.KindDiscussionMessage, core.DiscussionMessagePayload{
		V: core.PayloadVersion, ID: st.id, Role: core.DiscussionRoleOwner, Stop: mode, Round: round,
	})
	return true, err
}

func (r *Registry) appendDiscussion(ctx context.Context, sessionID, turnID, kind string, payload any) error {
	_, err := r.store.Append(context.WithoutCancel(ctx), sessionID, []core.EventDraft{{Kind: kind, TurnID: turnID, Payload: payload}})
	return err
}

// discussionRunner is the DiscussionService of one team lead turn.
type discussionRunner struct {
	reg     *Registry
	team    string
	lead    string
	cfg     core.TeamConfig
	allowed []string
}

var _ core.DiscussionService = (*discussionRunner)(nil)

func textOut(format string, args ...any) core.ToolOutput {
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: fmt.Sprintf(format, args...)}
}

func newDiscussionID() string {
	var raw [10]byte
	_, _ = rand.Read(raw[:])
	return "d_" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))
}

// Start opens a discussion (REQ-RDS-011).
func (d *discussionRunner) Start(ctx context.Context, tc core.ToolCtx, in core.DiscussStart) (core.ToolOutput, error) {
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		return toolError("topic is required"), nil
	}
	if strings.TrimSpace(in.Opening) == "" {
		return toolError("opening is required: one short message to the owner saying who you bring in"), nil
	}
	if len(in.Members) == 0 {
		return toolError("members is required"), nil
	}
	st := &discussionState{
		id: newDiscussionID(), sessionID: tc.SessionID, turnID: tc.TurnID, team: d.team, lead: d.lead,
		topic: topic, cfg: d.cfg, members: map[string]*discussionMember{}, limit: d.cfg.RoundLimit(),
		sem: make(chan struct{}, discussionParallel),
	}
	// Knowledge-aware discussions: what each team member knows about the topic.
	relevant := d.reg.relevantNotes(ctx, topic, d.cfg.Members)
	members := make([]core.DiscussionMember, 0, len(in.Members))
	for _, m := range in.Members {
		name := strings.TrimSpace(m.Agent)
		if !containsString(d.cfg.Members, name) {
			return toolError(fmt.Sprintf("agent '%s' is not a member of this team (members: %s)", name, strings.Join(d.cfg.Members, ", "))), nil
		}
		if _, dup := st.members[name]; dup {
			continue
		}
		notes := relevant[name]
		st.members[name] = &discussionMember{angle: strings.TrimSpace(m.Angle), notes: notes}
		st.order = append(st.order, name)
		members = append(members, core.DiscussionMember{Agent: name, Angle: strings.TrimSpace(m.Angle), Notes: len(notes), Titles: noteTitles(notes, 5)})
	}
	var skipped []core.DiscussionSkip
	var missed []string
	for _, name := range d.cfg.Members {
		if _, in := st.members[name]; in {
			continue
		}
		if n := len(relevant[name]); n > 0 {
			missed = append(missed, fmt.Sprintf("%s (%d notes: %s)", name, n, strings.Join(noteTitles(relevant[name], 3), ", ")))
			continue
		}
		skipped = append(skipped, core.DiscussionSkip{Agent: name, Reason: "no notes on this"})
	}
	if !d.reg.disc.put(st) {
		return toolError("a discussion is already open in this session; end it with discuss_end first"), nil
	}
	st.log = append(st.log, discussionLine{name: d.lead, text: strings.TrimSpace(in.Opening)})
	payload := core.DiscussionStartedPayload{
		V: core.PayloadVersion, ID: st.id, CallID: tc.CallID, Topic: topic, Opening: strings.TrimSpace(in.Opening), Lead: d.lead,
		Members: members, Rounds: st.limit, ExtraRounds: d.cfg.ExtraMode(), Requested: d.requested(ctx, tc),
		Skipped: skipped,
	}
	if err := d.reg.appendDiscussion(ctx, tc.SessionID, tc.TurnID, core.KindDiscussionStarted, payload); err != nil {
		d.reg.disc.drop(tc.SessionID)
		return toolError(internalErrorMessage), nil
	}
	d.post(ctx, tc.SessionID, d.lead, payload.Opening)
	var knows strings.Builder
	for _, m := range members {
		if m.Notes > 0 {
			fmt.Fprintf(&knows, " %s has %d matching notes (%s).", m.Agent, m.Notes, strings.Join(m.Titles, ", "))
		} else {
			fmt.Fprintf(&knows, " %s has no notes on this; ask only if its role still fits.", m.Agent)
		}
	}
	if len(missed) > 0 {
		fmt.Fprintf(&knows, " Not invited but has notes: %s. You can still ask them with discuss_ask.", strings.Join(missed, "; "))
	}
	return textOut("Discussion %s is open with %s. Round limit: %d (hard cap %d).%s Ask members with discuss_ask, one short @mention message each, and build on what each one knows; several discuss_ask calls in one step run in parallel. When members agree, when two replies bring nothing new, or when the difference is one of priorities, call discuss_end.",
		st.id, strings.Join(st.order, ", "), st.limit, core.TeamRoundsMax, knows.String()), nil
}

// requested reports whether the owner turned on Discuss for this turn's
// message (the runtime note of REQ-RDS-020).
func (d *discussionRunner) requested(ctx context.Context, tc core.ToolCtx) bool {
	events, err := d.reg.store.ChainEvents(ctx, tc.SessionID)
	if err != nil {
		return false
	}
	for _, e := range events {
		if e.TurnID != tc.TurnID || e.Kind != core.KindUserMessage {
			continue
		}
		if strings.Contains(string(e.Payload), "turned on Discuss") {
			return true
		}
	}
	return false
}

// post delivers one discussion message to the session's channel when the
// team shows every message there (REQ-RDS-033).
func (d *discussionRunner) post(ctx context.Context, sessionID, name, text string) {
	if !d.cfg.TelegramAllMessages() {
		return
	}
	if share := d.reg.discussionShare(); share != nil && strings.TrimSpace(text) != "" {
		share.PostMessage(context.WithoutCancel(ctx), sessionID, displayName(d.reg, name), text)
	}
}

func displayName(r *Registry, name string) string {
	if cfg, ok := r.AgentConfig(name); ok && strings.TrimSpace(cfg.DisplayName) != "" {
		return strings.TrimSpace(cfg.DisplayName)
	}
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// stopMessage is the tool output after the owner stopped the discussion.
func stopMessage(mode string) string {
	if mode == core.DiscussionDirect {
		return "The owner asked for a direct answer instead of a discussion. Call discuss_end now with reason \"direct\" and what you have, then answer the owner yourself."
	}
	return "The owner stopped the discussion. Call discuss_end now with reason \"stopped\" and a summary of what you have; ask no one else."
}

// askResult is the JSON output of discuss_ask.
type askResult struct {
	Agent  string `json:"agent"`
	Round  int    `json:"round"`
	Limit  int    `json:"limit"`
	Reply  string `json:"reply"`
	Passed bool   `json:"passed"`
	Failed bool   `json:"failed"`
	Error  string `json:"error,omitempty"`
}

// Ask asks one member (REQ-RDS-012, REQ-RDS-013).
func (d *discussionRunner) Ask(ctx context.Context, tc core.ToolCtx, in core.DiscussAsk) (core.ToolOutput, error) {
	st := d.reg.disc.get(tc.SessionID)
	if st == nil {
		return toolError("no discussion is open; call discuss_start first"), nil
	}
	name := strings.TrimSpace(in.Agent)
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return toolError("message is required"), nil
	}
	st.mu.Lock()
	if st.stop != "" {
		mode := st.stop
		st.mu.Unlock()
		return toolError(stopMessage(mode)), nil
	}
	m, ok := st.members[name]
	if !ok {
		if !containsString(st.cfg.Members, name) {
			st.mu.Unlock()
			return toolError(fmt.Sprintf("agent '%s' is not a member of this team (members: %s)", name, strings.Join(st.cfg.Members, ", "))), nil
		}
		// A team member the lead did not invite joins when asked.
		m = &discussionMember{notes: d.reg.relevantNotes(ctx, st.topic, []string{name})[name]}
		st.members[name] = m
		st.order = append(st.order, name)
	}
	round := m.asks + 1
	if round > st.limit {
		limit := st.limit
		st.mu.Unlock()
		return toolError(fmt.Sprintf("round limit reached: %s has answered %d of %d rounds. End the discussion with discuss_end, or call discuss_more if the last round brought something new and one concrete question is still open.", name, round-1, limit)), nil
	}
	m.asks = round
	st.log = append(st.log, discussionLine{name: st.lead, text: message})
	limit := st.limit
	st.mu.Unlock()

	_ = d.reg.appendDiscussion(ctx, tc.SessionID, tc.TurnID, core.KindDiscussionMessage, core.DiscussionMessagePayload{
		V: core.PayloadVersion, ID: st.id, CallID: tc.CallID, Role: core.DiscussionRoleLead, Agent: name, Round: round, Text: message,
	})
	d.post(ctx, tc.SessionID, st.lead, message)

	select {
	case st.sem <- struct{}{}:
	case <-ctx.Done():
		return stopToolOutput(ctx), nil
	}
	defer func() { <-st.sem }()

	st.mu.Lock()
	prompt := d.memberPrompt(st, name, m, message, in)
	childID := m.childID
	st.mu.Unlock()

	runner := &subagentRunner{reg: d.reg, allowed: d.allowed}
	var out core.ToolOutput
	var err error
	if childID == "" {
		out, err = runner.Spawn(ctx, core.SpawnRequest{
			ParentSessionID: tc.SessionID, ParentCallID: tc.CallID, Depth: tc.Depth,
			Agent: name, Prompt: prompt, Title: "Discussion: " + truncateRunes(st.topic, 60),
		})
	} else {
		out, err = runner.Continue(ctx, core.ContinueRequest{ParentSessionID: tc.SessionID, SessionID: childID, Prompt: prompt})
	}
	if err != nil {
		return core.ToolOutput{}, err
	}
	res := askResult{Agent: name, Round: round, Limit: limit}
	var task taskResult
	if out.Status == core.ToolStatusSuccess && json.Unmarshal([]byte(out.Output), &task) == nil && task.Error == "" && strings.TrimSpace(task.Answer) != "" {
		res.Reply = strings.TrimSpace(task.Answer)
	} else {
		res.Failed = true
		res.Error = out.Output
		if task.Error != "" {
			res.Error = task.Error
		}
		if res.Error == "" {
			res.Error = "no answer"
		}
	}
	if pass, reason := parsePass(res.Reply); pass {
		res.Passed = true
		res.Reply = reason
	}
	var usedNotes []string
	newPoint := false
	if !res.Failed && !res.Passed {
		res.Reply, usedNotes = splitNotesLine(res.Reply)
		res.Reply, newPoint = markNewPoint(res.Reply)
		usedNotes = mergeNotes(usedNotes, task.Notes)
	}

	st.mu.Lock()
	if task.SessionID != "" {
		m.childID = task.SessionID
	}
	switch {
	case res.Failed:
		st.log = append(st.log, discussionLine{name: name, text: "(could not answer)"})
	case res.Passed:
		m.passed = true
		st.log = append(st.log, discussionLine{name: name, text: "PASS: " + res.Reply})
	default:
		m.passed = false
		m.lastText = res.Reply
		st.log = append(st.log, discussionLine{name: name, text: res.Reply})
	}
	m.seen = len(st.log)
	st.mu.Unlock()

	msg := core.DiscussionMessagePayload{
		V: core.PayloadVersion, ID: st.id, CallID: tc.CallID, Role: core.DiscussionRoleMember, Agent: name, Round: round,
		Text: res.Reply, Passed: res.Passed, Failed: res.Failed, ChildSessionID: task.SessionID,
		Notes: usedNotes, NewPoint: newPoint,
		ReplyTo: strings.TrimSpace(in.ReplyTo), Quote: truncateRunes(strings.TrimSpace(in.Quote), 200),
	}
	if res.Failed {
		msg.Text = res.Error
	}
	_ = d.reg.appendDiscussion(ctx, tc.SessionID, tc.TurnID, core.KindDiscussionMessage, msg)
	if !res.Failed && !res.Passed {
		d.post(ctx, tc.SessionID, name, res.Reply)
	}
	raw, _ := json.Marshal(res)
	return core.ToolOutput{Status: core.ToolStatusSuccess, Output: string(raw), Usage: out.Usage}, nil
}

// parsePass reads a "PASS: reason" reply. A reply that only starts with a
// word like "Passing" is a normal reply.
func parsePass(reply string) (bool, string) {
	t := strings.TrimSpace(reply)
	if len(t) < 4 || !strings.EqualFold(t[:4], "pass") {
		return false, reply
	}
	rest := t[4:]
	if rest != "" && !strings.ContainsAny(rest[:1], ":-.— \n") && !strings.HasPrefix(rest, "—") {
		return false, reply
	}
	rest = strings.TrimSpace(strings.TrimLeft(rest, ":-.— \n"))
	if rest == "" {
		rest = "nothing new to add"
	}
	return true, rest
}

// memberPrompt builds the message for one ask: the full briefing on the first
// ask, then only the new lines (REQ-RDS-012).
func (d *discussionRunner) memberPrompt(st *discussionState, name string, m *discussionMember, message string, in core.DiscussAsk) string {
	var b strings.Builder
	leadName := displayName(d.reg, st.lead)
	if m.childID == "" {
		fmt.Fprintf(&b, "You are taking part in a discussion in the group room %q, moderated by %s (the lead).\n", st.team, leadName)
		fmt.Fprintf(&b, "Topic: %s\n", st.topic)
		if m.angle != "" {
			fmt.Fprintf(&b, "Your angle: %s\n", m.angle)
		}
		others := make([]string, 0, len(st.order))
		for _, o := range st.order {
			if o != name {
				others = append(others, displayName(d.reg, o))
			}
		}
		if len(others) > 0 {
			fmt.Fprintf(&b, "Other members: %s\n", strings.Join(others, ", "))
		}
		if len(m.notes) > 0 {
			b.WriteString("\nYour knowledge pages that match this topic (read them with knowledge_read when you need detail):\n")
			for _, n := range m.notes {
				fmt.Fprintf(&b, "- %s (%s)\n", n.title, n.path)
			}
		}
		if st.cfg.KeepsRoomMemory() {
			if mem := d.reg.RoomMemory().Prompt(st.team, discussionMemoryLimit); mem != "" {
				fmt.Fprintf(&b, "\nRoom memory (what this room already knows):\n%s\n", mem)
			}
		}
		b.WriteString("\nDiscussion so far:\n")
		for _, line := range st.log[:len(st.log)-1] {
			fmt.Fprintf(&b, "%s: %s\n", displayName(d.reg, line.name), line.text)
		}
	} else {
		b.WriteString("New in the discussion since your last reply:\n")
		for _, line := range st.log[m.seen : len(st.log)-1] {
			fmt.Fprintf(&b, "%s: %s\n", displayName(d.reg, line.name), line.text)
		}
	}
	fmt.Fprintf(&b, "\n%s asks you: %s\n", leadName, message)
	if in.ReplyTo != "" && in.Quote != "" {
		fmt.Fprintf(&b, "(This answers %s: %q)\n", displayName(d.reg, in.ReplyTo), in.Quote)
	}
	fmt.Fprintf(&b, "\nReply as one chat message to the group in the owner's language, in at most %d words. Address others by name when you answer their point. Use your tools only if you need a fact. Build on what you know: if your notes hold something important the lead did not ask about, raise it and start that sentence with \"New point:\". When you relied on your knowledge pages, end with one line \"Notes: <page titles, comma separated>\". If you have nothing new to add, reply exactly \"PASS: <short reason>\".", discussionReplyWords)
	return b.String()
}

// More adds one round (REQ-RDS-014).
func (d *discussionRunner) More(ctx context.Context, tc core.ToolCtx, in core.DiscussMore) (core.ToolOutput, error) {
	st := d.reg.disc.get(tc.SessionID)
	if st == nil {
		return toolError("no discussion is open"), nil
	}
	message := strings.TrimSpace(in.Message)
	if message == "" {
		return toolError("message is required: what you tell the room, and what is still missing"), nil
	}
	st.mu.Lock()
	if st.stop != "" {
		mode := st.stop
		st.mu.Unlock()
		return toolError(stopMessage(mode)), nil
	}
	mode := st.cfg.ExtraMode()
	limit, extra := st.limit, st.extra
	st.mu.Unlock()
	if mode == core.TeamExtraNever {
		return toolError("extra rounds are off for this room. Call discuss_end and put the open question to the owner."), nil
	}
	if limit >= core.TeamRoundsMax {
		return toolError(fmt.Sprintf("a discussion has at most %d rounds. Call discuss_end now.", core.TeamRoundsMax)), nil
	}
	if mode == core.TeamExtraAsk || extra >= 1 {
		if tc.Questions == nil {
			return toolError("the owner cannot be asked for another round here. Call discuss_end."), nil
		}
		outcome, err := tc.Questions.Ask(ctx, core.QuestionRequest{
			SessionID: tc.SessionID, TurnID: tc.TurnID, CallID: tc.CallID, AgentName: tc.AgentName,
			Questions: []core.Question{{
				Text: message, Header: "Discussion",
				Options: []core.QuestionOption{{Label: "Add 1 round"}, {Label: "Sum up now"}},
			}},
		})
		if err != nil {
			return toolError(err.Error()), nil
		}
		yes := outcome.Status == core.QuestionStatusAnswered && len(outcome.Answers) > 0 &&
			len(outcome.Answers[0].Selected) > 0 && outcome.Answers[0].Selected[0] == "Add 1 round"
		if !yes {
			return textOut("The owner wants the summary now. Call discuss_end."), nil
		}
	}
	st.mu.Lock()
	st.limit++
	st.extra++
	limit = st.limit
	st.log = append(st.log, discussionLine{name: st.lead, text: message})
	st.mu.Unlock()
	_ = d.reg.appendDiscussion(ctx, tc.SessionID, tc.TurnID, core.KindDiscussionMessage, core.DiscussionMessagePayload{
		V: core.PayloadVersion, ID: st.id, CallID: tc.CallID, Role: core.DiscussionRoleLead, Text: message, Extra: true, Round: limit,
	})
	d.post(ctx, tc.SessionID, st.lead, message)
	return textOut("Round %d added (limit now %d). Ask only what is still missing.", limit, limit), nil
}

var endReasons = []string{"agreed", "no_progress", "limit", "priorities", "stopped", "direct", "done"}

// End closes the discussion (REQ-RDS-015, REQ-RDS-030..032).
func (d *discussionRunner) End(ctx context.Context, tc core.ToolCtx, in core.DiscussEnd) (core.ToolOutput, error) {
	st := d.reg.disc.get(tc.SessionID)
	if st == nil {
		return toolError("no discussion is open"), nil
	}
	conclusion := strings.TrimSpace(in.Conclusion)
	if conclusion == "" {
		return toolError("conclusion is required"), nil
	}
	reason := strings.TrimSpace(in.Reason)
	st.mu.Lock()
	if st.stop != "" {
		reason = st.stop
		if reason == core.DiscussionStop {
			reason = "stopped"
		}
	}
	if !containsString(endReasons, reason) {
		reason = "done"
	}
	rounds, limit := st.round(), st.limit
	order := append([]string{}, st.order...)
	st.mu.Unlock()
	d.reg.disc.drop(tc.SessionID)

	ended := core.DiscussionEndedPayload{
		V: core.PayloadVersion, ID: st.id, CallID: tc.CallID, Topic: st.topic, Reason: reason, Rounds: rounds, Limit: limit,
		Summary: core.DiscussionSummary{
			Conclusion: conclusion, Agreed: cleanPoints(in.Agreed), Different: cleanPoints(in.Different), Open: cleanPoints(in.Open),
		},
		Parts: map[string]string{}, SharedWith: []string{}, Learning: []string{},
	}
	for k, v := range in.Parts {
		if containsString(order, k) && strings.TrimSpace(v) != "" {
			ended.Parts[k] = truncateRunes(strings.TrimSpace(v), 400)
		}
	}
	// REQ-RDS-030: room memory.
	var notesOut []string
	if st.cfg.KeepsRoomMemory() {
		for _, n := range in.Notes {
			kind := strings.TrimSpace(n.Kind)
			text := truncateRunes(strings.TrimSpace(n.Text), roommemory.MaxText)
			if _, err := d.reg.RoomMemory().Add(st.team, kind, text, st.lead, st.id, n.Replaces); err != nil {
				notesOut = append(notesOut, fmt.Sprintf("note %q not saved: %v", truncateRunes(text, 40), err))
				continue
			}
			ended.NotesSaved++
		}
	}
	at := time.Now()
	// REQ-RDS-031: the summary in each member's room. Posting waits for a
	// member's running turn, so it runs in the background.
	if share := d.reg.discussionShare(); share != nil && st.cfg.SharesSummary() {
		for _, member := range order {
			text := memberSummaryText(d.reg, st, ended, member)
			ended.SharedWith = append(ended.SharedWith, member)
			go func(member, text string) {
				if err := share.ShareSummary(context.WithoutCancel(ctx), st.team, member, text); err != nil {
					slog.Warn("discussion: sharing summary failed", "team", st.team, "member", member, "error", err)
				}
			}(member, text)
		}
	}
	// REQ-RDS-032: members with a domain learn from it.
	if st.cfg.Learns() {
		if learner, ok := d.reg.knowledgeService().(discussionLearner); ok && learner != nil {
			for _, member := range order {
				if !learner.HasDomain(member) {
					continue
				}
				block := journalSummaryBlock(d.reg, st, ended, member, at)
				if err := learner.NoteDiscussion(context.WithoutCancel(ctx), member, at, block); err != nil {
					slog.Warn("discussion: journal failed", "member", member, "error", err)
					continue
				}
				ended.Learning = append(ended.Learning, member)
			}
		}
	}
	if err := d.reg.appendDiscussion(ctx, tc.SessionID, tc.TurnID, core.KindDiscussionEnded, ended); err != nil {
		return toolError(internalErrorMessage), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Discussion ended (%s, %d rounds). Saved %d notes to room memory", reason, rounds, ended.NotesSaved)
	if len(ended.SharedWith) > 0 {
		fmt.Fprintf(&b, "; summary shared with %s", strings.Join(ended.SharedWith, ", "))
	}
	if len(ended.Learning) > 0 {
		fmt.Fprintf(&b, "; %s will learn from it", strings.Join(ended.Learning, ", "))
	}
	b.WriteString(".")
	for _, line := range notesOut {
		b.WriteString(" " + line + ".")
	}
	b.WriteString(" Now answer the owner in 2–4 sentences: the conclusion, what is still different or open, and a question if a decision is theirs. The room shows the full summary behind a Summary button, so do not repeat every point.")
	return textOut("%s", b.String()), nil
}

// discussionLearner is the knowledge service surface REQ-RDS-032 needs.
type discussionLearner interface {
	HasDomain(agent string) bool
	NoteDiscussion(ctx context.Context, agent string, at time.Time, block string) error
}

func cleanPoints(in []core.DiscussionPoint) []core.DiscussionPoint {
	out := make([]core.DiscussionPoint, 0, len(in))
	for _, p := range in {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		out = append(out, core.DiscussionPoint{Text: truncateRunes(text, 400), Agent: strings.TrimSpace(p.Agent)})
	}
	return out
}

func memberSummaryText(r *Registry, st *discussionState, ended core.DiscussionEndedPayload, member string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", ended.Summary.Conclusion)
	if part := ended.Parts[member]; part != "" {
		fmt.Fprintf(&b, "\nYour part: %s\n", part)
	}
	for _, p := range ended.Summary.Open {
		fmt.Fprintf(&b, "\nOpen: %s", p.Text)
	}
	return strings.TrimSpace(b.String())
}

func journalSummaryBlock(r *Registry, st *discussionState, ended core.DiscussionEndedPayload, member string, at time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## %s · %s · discussion · room %s\n", at.Format("15:04"), member, st.team)
	fmt.Fprintf(&b, "- Topic: %s\n", st.topic)
	fmt.Fprintf(&b, "- Conclusion: %s\n", ended.Summary.Conclusion)
	if part := ended.Parts[member]; part != "" {
		fmt.Fprintf(&b, "- My part: %s\n", part)
	}
	for _, p := range ended.Summary.Agreed {
		fmt.Fprintf(&b, "- Agreed: %s\n", p.Text)
	}
	for _, p := range ended.Summary.Open {
		fmt.Fprintf(&b, "- Open: %s\n", p.Text)
	}
	return strings.TrimRight(b.String(), "\n")
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// noteRef is one knowledge page a member has on the topic.
type noteRef struct {
	title string
	path  string
}

func noteTitles(notes []noteRef, n int) []string {
	out := make([]string, 0, n)
	for i, note := range notes {
		if i >= n {
			break
		}
		out = append(out, note.title)
	}
	return out
}

// discussionNoteLimit is how many matching pages a member's relevance check
// counts.
const discussionNoteLimit = 8

// relevantNotes returns, per member, its domain pages that match topic
// (knowledge-aware discussions). Members without a domain get none.
func (r *Registry) relevantNotes(ctx context.Context, topic string, members []string) map[string][]noteRef {
	out := map[string][]noteRef{}
	know := r.knowledgeService()
	if know == nil {
		return out
	}
	domains, _ := know.(core.KnowledgeDomains)
	for _, name := range members {
		if domains != nil && !domains.HasDomain(name) {
			continue
		}
		hits, err := know.Recall(ctx, topic, discussionNoteLimit, []string{name}, name, nil)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, h := range hits {
			if h.Path == "" || seen[h.Path] || strings.Contains(h.Path, "/journal/") {
				continue
			}
			seen[h.Path] = true
			title := strings.TrimSpace(h.Title)
			if title == "" {
				title = strings.TrimSuffix(h.Path[strings.LastIndex(h.Path, "/")+1:], ".md")
			}
			out[name] = append(out[name], noteRef{title: title, path: h.Path})
		}
	}
	return out
}

// knowsSummary is the roster line of a member's knowledge: "48 pages · a, b, c".
func (r *Registry) knowsSummary(ctx context.Context, name string) string {
	domains, ok := r.knowledgeService().(core.KnowledgeDomains)
	if !ok || domains == nil || !domains.HasDomain(name) {
		return ""
	}
	pages, total, err := domains.Pages(ctx, name, "", 4)
	if err != nil || total == 0 {
		return ""
	}
	titles := make([]string, 0, len(pages))
	for _, p := range pages {
		if p.Title != "" {
			titles = append(titles, p.Title)
		}
	}
	line := fmt.Sprintf("%d pages", total)
	if len(titles) > 0 {
		line += " · " + strings.Join(titles, ", ")
	}
	return line
}

// splitNotesLine removes a trailing "Notes: a, b" line from a reply and
// returns the note titles it named.
func splitNotesLine(reply string) (string, []string) {
	lines := strings.Split(strings.TrimRight(reply, "\n "), "\n")
	if len(lines) == 0 {
		return reply, nil
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	plain := strings.TrimLeft(last, "*_ ")
	lower := strings.ToLower(plain)
	for _, prefix := range []string{"notes:", "catatan:", "notes used:"} {
		if strings.HasPrefix(lower, prefix) {
			body := strings.Trim(plain[len(prefix):], "*_ ")
			var notes []string
			for _, part := range strings.Split(body, ",") {
				if t := strings.Trim(strings.TrimSpace(part), "`\"*_"); t != "" && !strings.EqualFold(t, "none") && t != "-" {
					notes = append(notes, t)
				}
			}
			return strings.TrimSpace(strings.Join(lines[:len(lines)-1], "\n")), notes
		}
	}
	return reply, nil
}

// markNewPoint reports a "New point:" marker and removes the marker text.
func markNewPoint(reply string) (string, bool) {
	found := false
	for _, marker := range []string{"New point:", "new point:", "**New point:**", "Poin baru:"} {
		if strings.Contains(reply, marker) {
			reply = strings.ReplaceAll(reply, marker, "")
			found = true
		}
	}
	return strings.TrimSpace(strings.ReplaceAll(reply, "  ", " ")), found
}

func mergeNotes(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range [][]string{a, b} {
		for _, n := range list {
			key := strings.ToLower(n)
			if n == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, n)
		}
	}
	return out
}
