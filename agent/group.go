package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// Group rooms (owner, 2026-11-11: "saya ingin lebih natural kaya group
// kerja"). A team in group mode is one shared conversation: the owner's
// message goes to the agents it @mentions, or to everyone when it names no
// one; each agent answers as itself, in its own session, with its own tools,
// and can @mention a teammate to hand over. The lead coordinates but is not a
// gate. Discuss still runs the lead's moderated discussion.

const (
	// groupMaxReplies caps the agent replies to one owner message.
	groupMaxReplies = 10
	// groupMaxPerAgent caps one agent's turns per owner message.
	groupMaxPerAgent = 3
	// groupTranscriptLimit caps the conversation an agent's first prompt
	// carries, in characters.
	groupTranscriptLimit = 8000
	// GroupSource marks an assistant/message an agent posted in a group room.
	GroupSource = "agent"
)

var mentionRE = regexp.MustCompile(`@([A-Za-z][A-Za-z0-9_-]*)`)

// groupState remembers, per room session and agent, the agent's own session
// and the room seq it last saw.
type groupState struct {
	mu   sync.Mutex
	kids map[string]groupKid
}

type groupKid struct {
	sessionID string
	seen      int64
}

func (g *groupState) get(room, agent string) groupKid {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.kids[room+"\x00"+agent]
}

func (g *groupState) set(room, agent string, kid groupKid) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.kids == nil {
		g.kids = map[string]groupKid{}
	}
	g.kids[room+"\x00"+agent] = kid
}

// groupMode reports whether a turn of this session runs as a group turn: a
// team in group mode whose pending owner message did not turn on Discuss.
func (rn *Runner) groupMode(ctx context.Context) (core.TeamConfig, string, bool) {
	if rn.reg.isBackground(rn.sessionID) || rn.reg.isScheduled(rn.sessionID) {
		return core.TeamConfig{}, "", false
	}
	info, err := rn.reg.store.Info(ctx, rn.sessionID)
	if err != nil || info.ParentSessionID != "" {
		return core.TeamConfig{}, "", false
	}
	_, teams := rn.reg.configSnapshot()
	team, ok := teams[info.Agent]
	if !ok || team.ConversationMode() != core.TeamModeGroup || len(team.Members) == 0 {
		return core.TeamConfig{}, "", false
	}
	// A message sent with Discuss on runs the lead's moderated discussion.
	events, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			e := events[i]
			if e.Kind == core.KindTurnEnd || e.Kind == core.KindTurnStart {
				break
			}
			if e.Kind == core.KindUserMessage && strings.Contains(string(e.Payload), "turned on Discuss") {
				return core.TeamConfig{}, "", false
			}
		}
	}
	return team, info.Agent, true
}

// participants is the lead and the members, lead first.
func participants(team core.TeamConfig) []string {
	out := []string{team.Lead}
	for _, m := range team.Members {
		if m != team.Lead {
			out = append(out, m)
		}
	}
	return out
}

// mentionsIn returns the participants a text @mentions, in order, matching
// names case-insensitively, also by display name.
func (r *Registry) mentionsIn(text string, people []string) []string {
	var out []string
	for _, m := range mentionRE.FindAllStringSubmatch(text, -1) {
		token := strings.ToLower(m[1])
		for _, p := range people {
			label := strings.ToLower(strings.ReplaceAll(displayName(r, p), " ", ""))
			if (strings.ToLower(p) == token || label == token || strings.HasPrefix(strings.ToLower(p), token+"-")) && !containsString(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// runGroupTurn runs one owner message in a group room.
func (rn *Runner) runGroupTurn(ctx context.Context, initial inboxItem, turnID string, team core.TeamConfig, room string) TurnResult {
	result := TurnResult{SessionID: rn.sessionID, TurnID: turnID, StopReason: "end_turn"}
	writeCtx := context.WithoutCancel(ctx)
	events := &Events{Store: rn.reg.store, SessionID: rn.sessionID}
	if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindTurnStart, TurnID: turnID, Payload: core.TurnStart{V: core.PayloadVersion, TurnID: turnID}}); err != nil {
		return rn.failTurn(ctx, turnID, runtimeError(err))
	}
	people := participants(team)
	queue := rn.storeOwnerMessage(writeCtx, events, turnID, initial, people)
	turns := map[string]int{}
	replies := 0
	runner := &subagentRunner{reg: rn.reg, allowed: people}
	var usage core.Usage
	for len(queue) > 0 && replies < groupMaxReplies {
		if ctx.Err() != nil {
			result.StopReason = "cancelled"
			break
		}
		// The owner may write again while agents work (REQ-RDS-022).
		for {
			item, ok := rn.popInbox()
			if !ok {
				break
			}
			queue = append(queue, rn.storeOwnerMessage(writeCtx, events, turnID, item, people)...)
		}
		speaker := queue[0]
		queue = queue[1:]
		if turns[speaker] >= groupMaxPerAgent {
			continue
		}
		turns[speaker]++
		replies++
		rn.reg.hub.Publish(rn.sessionID, core.LiveFrame{SessionID: rn.sessionID, TurnID: turnID, Kind: "speaker", Name: speaker})
		reply, meta, err := rn.groupReply(ctx, runner, room, team, speaker, people)
		if err != nil {
			slog.Warn("group reply failed", "session_id", rn.sessionID, "agent", speaker, "error", err)
		}
		if meta.usage != nil {
			usage.InputTokens += meta.usage.InputTokens
			usage.OutputTokens += meta.usage.OutputTokens
			usage.CachedInputTokens += meta.usage.CachedInputTokens
		}
		passed, _ := parsePass(reply)
		if strings.TrimSpace(reply) == "" {
			passed = true
		}
		msg := core.AssistantMessage{
			V: core.PayloadVersion, StopReason: "end_turn", Source: GroupSource, SourceName: speaker,
			Passed: passed, Failed: meta.failed, Tools: meta.tools, Notes: meta.notes, Files: meta.files,
			ChildSessionID: meta.child, ChildTurnID: meta.turn,
		}
		if !passed {
			msg.Content = []core.ContentBlock{{Kind: core.BlockText, Text: reply}}
		}
		if _, err := events.Append(writeCtx, core.EventDraft{Kind: core.KindAssistantMessage, TurnID: turnID, Payload: msg}); err != nil {
			slog.Warn("group reply not stored", "session_id", rn.sessionID, "error", err)
		}
		if passed {
			continue
		}
		rn.postGroupReply(writeCtx, speaker, reply)
		// A teammate @mentioned in the reply speaks next.
		for _, next := range rn.reg.mentionsIn(reply, people) {
			if next != speaker && !containsString(queue, next) {
				queue = append(queue, next)
			}
		}
	}
	end := core.TurnEnd{V: core.PayloadVersion, TurnID: turnID, Reason: result.StopReason}
	if err := writeTurnEnd(writeCtx, events, core.EventDraft{Kind: core.KindTurnEnd, TurnID: turnID, Payload: end}); err != nil {
		slog.Error("failed to write turn/end", "session_id", rn.sessionID, "turn_id", turnID, "error", err)
	}
	result.Usage = usage
	return result
}

// storeOwnerMessage writes the owner's message and returns who should answer:
// the agents it @mentions, else everyone (lead first).
func (rn *Runner) storeOwnerMessage(ctx context.Context, events *Events, turnID string, item inboxItem, people []string) []string {
	ctxBlock := agenthome.ContextBlock(agenthome.PromptInput{Now: time.Now()})
	content := []core.ContentBlock{{Kind: core.BlockText, Text: ctxBlock}, {Kind: core.BlockText, Text: item.text}}
	if len(item.attachments) > 0 {
		if info, err := rn.reg.store.Info(ctx, rn.sessionID); err == nil {
			content = append(content, ownerAttachmentBlocks(info.Workspace, item.attachments)...)
		}
	}
	payload := core.UserMessage{V: core.PayloadVersion, Content: content, ContextBlocks: 1}
	if item.replyTo != nil {
		payload.ReplyTo = item.replyTo
	}
	if _, err := events.Append(ctx, core.EventDraft{Kind: core.KindUserMessage, TurnID: turnID, Payload: payload}); err != nil {
		slog.Warn("group owner message not stored", "session_id", rn.sessionID, "error", err)
	}
	if named := rn.reg.mentionsIn(item.text, people); len(named) > 0 {
		return named
	}
	return append([]string{}, people...)
}

// postGroupReply delivers a reply to the room's channel (Telegram).
func (rn *Runner) postGroupReply(ctx context.Context, speaker, text string) {
	if share := rn.reg.discussionShare(); share != nil {
		share.PostMessage(ctx, rn.sessionID, displayName(rn.reg, speaker), text)
	}
}

type groupMeta struct {
	child  string
	turn   string
	usage  *core.Usage
	tools  map[string]int
	notes  []string
	files  []string
	failed bool
}

// groupReply runs one agent's turn in its own session and returns its reply.
func (rn *Runner) groupReply(ctx context.Context, runner *subagentRunner, room string, team core.TeamConfig, speaker string, people []string) (string, groupMeta, error) {
	kid := rn.reg.groups.get(rn.sessionID, speaker)
	transcript, last := rn.groupTranscript(ctx, kid.seen)
	var prompt string
	if kid.sessionID == "" {
		prompt = rn.groupBriefing(ctx, room, team, speaker, people) + "\n\n## Conversation so far\n" + transcript
	} else {
		prompt = "## New in the room since your last reply\n" + transcript
	}
	prompt += "\n\nReply now as " + displayName(rn.reg, speaker) + ", or PASS."
	var out core.ToolOutput
	var err error
	if kid.sessionID == "" {
		out, err = runner.Spawn(ctx, core.SpawnRequest{ParentSessionID: rn.sessionID, Agent: speaker, Prompt: prompt, Title: displayName(rn.reg, speaker) + " in " + room, WithNotes: true})
	} else {
		out, err = runner.Continue(ctx, core.ContinueRequest{ParentSessionID: rn.sessionID, SessionID: kid.sessionID, Prompt: prompt})
	}
	meta := groupMeta{usage: out.Usage}
	if err != nil {
		meta.failed = true
		return "", meta, err
	}
	var task taskResult
	if json.Unmarshal([]byte(out.Output), &task) != nil || task.Error != "" || out.Status != core.ToolStatusSuccess {
		meta.failed = true
		reason := task.Error
		if reason == "" {
			reason = out.Output
		}
		return fmt.Sprintf("(%s could not answer: %s)", displayName(rn.reg, speaker), truncateRunes(reason, 200)), meta, nil
	}
	if task.SessionID != "" {
		kid.sessionID = task.SessionID
	}
	kid.seen = last
	rn.reg.groups.set(rn.sessionID, speaker, kid)
	meta.tools, meta.notes, meta.files = task.Tools, task.Notes, task.Files
	meta.child, meta.turn = task.SessionID, task.TurnID
	return strings.TrimSpace(task.Answer), meta, nil
}

// groupBriefing is the first prompt an agent gets in a group room.
func (rn *Runner) groupBriefing(ctx context.Context, room string, team core.TeamConfig, speaker string, people []string) string {
	agents, _ := rn.reg.configSnapshot()
	var b strings.Builder
	title := room
	if team.DisplayName != "" {
		title = team.DisplayName
	}
	fmt.Fprintf(&b, "You are %s in the group chat %q with the owner (a human) and your teammates.", displayName(rn.reg, speaker), title)
	if team.Description != "" {
		fmt.Fprintf(&b, " The room is for: %s.", team.Description)
	}
	b.WriteString("\n\n## Teammates\n")
	for _, p := range people {
		if p == speaker {
			continue
		}
		line := "- @" + p
		if cfg, ok := agents[p]; ok && cfg.Description != "" {
			line += ": " + cfg.Description
		}
		var facts []string
		if knows := rn.reg.knowsSummary(ctx, p); knows != "" {
			facts = append(facts, "knows: "+knows)
		}
		if cfg, ok := agents[p]; ok {
			if can := core.AgentCapabilities(cfg); len(can) > 0 {
				facts = append(facts, "can: "+strings.Join(can, ", "))
			}
		}
		if p == team.Lead {
			facts = append(facts, "coordinator")
		}
		if len(facts) > 0 {
			line += " (" + strings.Join(facts, "; ") + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteString(`
## How this room works
- Everyone sees the same conversation. Talk like a teammate in a work chat: short, in the owner's language, as yourself.
- Do your own part with your own tools; do not do a teammate's part. Finish your work before you reply, and do not leave it to a background process.
- Answer what the owner asked in their latest message, at the size they asked for. A question gets an answer, not a build; do not pick up older work or extra improvements unless the owner asks.
- An @mention makes that teammate work next, so @mention only to hand over a step or ask something you need now (for example @` + firstOther(people, speaker) + `). To refer to a teammate, write their name without @. Do not @mention to thank, to report that you are done, or to ask for a review the owner did not ask for.
- When the request is answered, stop: no new handoffs. If the owner @mentioned someone else, or you have nothing to add, reply exactly PASS.
`)
	if speaker == team.Lead {
		b.WriteString("- You coordinate. For a bigger request, post a short plan with one @mention per step, in order, and let each teammate do its step; for a small one, answer only if it is yours or nobody else covered it. Do not repeat what teammates said.\n")
	}
	if team.KeepsRoomMemory() {
		if mem := rn.reg.RoomMemory().Prompt(room, discussionMemoryLimit); mem != "" {
			fmt.Fprintf(&b, "\n## Room memory\n%s\n", mem)
		}
	}
	return b.String()
}

func firstOther(people []string, self string) string {
	for _, p := range people {
		if p != self {
			return p
		}
	}
	return self
}

// groupTranscript renders the room's messages after seq `after`, newest last,
// and returns the last seq it read.
func (rn *Runner) groupTranscript(ctx context.Context, after int64) (string, int64) {
	events, err := rn.reg.store.ChainEvents(ctx, rn.sessionID)
	if err != nil {
		return "", after
	}
	var lines []string
	last := after
	for _, e := range events {
		if e.Seq > last {
			last = e.Seq
		}
		if e.Seq <= after {
			continue
		}
		switch e.Kind {
		case core.KindUserMessage:
			var p core.UserMessage
			if json.Unmarshal(e.Payload, &p) != nil || p.Runtime {
				continue
			}
			var parts []string
			for i, block := range p.Content {
				if i < p.ContextBlocks || block.Kind != core.BlockText {
					continue
				}
				parts = append(parts, block.Text)
			}
			if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
				lines = append(lines, "Owner: "+text)
			}
		case core.KindAssistantMessage:
			var p core.AssistantMessage
			if json.Unmarshal(e.Payload, &p) != nil || p.Passed {
				continue
			}
			name := "Lead"
			if p.Source == GroupSource && p.SourceName != "" {
				name = displayName(rn.reg, p.SourceName)
			} else if p.Source != "" {
				continue
			}
			var parts []string
			for _, block := range p.Content {
				if block.Kind == core.BlockText {
					parts = append(parts, block.Text)
				}
			}
			if text := strings.TrimSpace(strings.Join(parts, "\n")); text != "" {
				lines = append(lines, name+": "+text)
			}
		}
	}
	text := strings.Join(lines, "\n\n")
	if len(text) > groupTranscriptLimit {
		text = "…" + text[len(text)-groupTranscriptLimit:]
	}
	return text, last
}
