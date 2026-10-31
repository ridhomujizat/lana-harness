package compaction

import (
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/core"
)

// unit is one balanced piece of derived history: a user message, or an
// assistant message plus all tool results answering its calls. A cut is only
// allowed between units (REQ-LTC-013).
type unit struct {
	kind     string // "user", "assistant", or "summary"
	startSeq int64
	endSeq   int64
	messages []core.Message
	tools    []unitTool
	chars    int
}

// unitTool is one tool result inside a unit, kept so the pruner can cite its
// source seq.
type unitTool struct {
	seq    int64
	callID string
	output string
}

// buildUnits groups the derived history into units. A prior summary becomes a
// leading synthetic unit so a later compaction can merge it (REQ-LTC-015).
func buildUnits(events []core.SessionEvent, prunes map[string]string) ([]unit, error) {
	var units []unit

	var sum core.CompactionSummary
	sumSeq := int64(0)
	hasSum := false
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != core.KindCompactionSummary {
			continue
		}
		if err := json.Unmarshal(events[i].Payload, &sum); err != nil {
			return nil, err
		}
		sumSeq = events[i].Seq
		hasSum = true
		break
	}
	cutoff := int64(0)
	if hasSum {
		cutoff = sum.UptoSeq
		if sum.RetainedFromSeq == 0 {
			cutoff = sumSeq
		}
		text := sum.Summary
		if sum.RetainedFromSeq > 0 {
			text = checkpointText(sum.Summary)
		}
		units = append(units, unit{
			kind:     "summary",
			startSeq: sumSeq,
			endSeq:   sumSeq,
			messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{{Kind: core.BlockText, Text: text}}}},
		})
	}

	for _, event := range events {
		if event.Seq <= cutoff {
			continue
		}
		switch event.Kind {
		case core.KindUserMessage:
			var payload core.UserMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, err
			}
			units = append(units, unit{
				kind:     "user",
				startSeq: event.Seq,
				endSeq:   event.Seq,
				messages: []core.Message{{Role: core.RoleUser, Content: append([]core.ContentBlock(nil), payload.Content...)}},
			})
		case core.KindAssistantMessage:
			var payload core.AssistantMessage
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, err
			}
			units = append(units, unit{
				kind:     "assistant",
				startSeq: event.Seq,
				endSeq:   event.Seq,
				messages: []core.Message{{Role: core.RoleAssistant, Content: append([]core.ContentBlock(nil), payload.Content...)}},
			})
		case core.KindToolResult:
			var payload core.ToolResultPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return nil, err
			}
			output, ok := prunes[payload.CallID]
			if !ok {
				output = payload.Output
			}
			block := core.ContentBlock{
				Kind:    core.BlockToolResult,
				ID:      payload.CallID,
				Output:  output,
				IsError: payload.Status != core.ToolStatusSuccess,
			}
			tool := unitTool{seq: event.Seq, callID: payload.CallID, output: output}
			if n := len(units); n > 0 && units[n-1].kind == "assistant" {
				target := &units[n-1]
				target.endSeq = event.Seq
				if k := len(target.messages); k > 0 && target.messages[k-1].Role == core.RoleUser && allToolResults(target.messages[k-1].Content) {
					target.messages[k-1].Content = append(target.messages[k-1].Content, block)
				} else {
					target.messages = append(target.messages, core.Message{Role: core.RoleUser, Content: []core.ContentBlock{block}})
				}
				target.tools = append(target.tools, tool)
			} else {
				units = append(units, unit{
					kind:     "user",
					startSeq: event.Seq,
					endSeq:   event.Seq,
					messages: []core.Message{{Role: core.RoleUser, Content: []core.ContentBlock{block}}},
					tools:    []unitTool{tool},
				})
			}
		}
	}
	for i := range units {
		units[i].chars = messagesChars(units[i].messages)
	}
	return units, nil
}

// flatten concatenates the messages of the given units.
func flatten(units []unit) []core.Message {
	var out []core.Message
	for _, u := range units {
		out = append(out, u.messages...)
	}
	return out
}

// onlySummary reports whether a span holds nothing but a prior summary, which
// cannot be condensed further (REQ-LTC-016).
func onlySummary(units []unit) bool {
	if len(units) == 0 {
		return false
	}
	for _, u := range units {
		if u.kind != "summary" {
			return false
		}
	}
	return true
}

// tailIndex returns the index of the first retained unit. The tail walks units
// from the newest back while their char estimate fits the budget, always
// keeping at least the newest unit. With newestOnly the tail is the newest unit
// alone (REQ-LTC-013, REQ-LTC-016).
func tailIndex(units []unit, budget int64, newestOnly bool) int {
	if len(units) == 0 {
		return 0
	}
	if newestOnly {
		return len(units) - 1
	}
	start := len(units) - 1
	sum := int64(units[start].chars)
	for i := start - 1; i >= 0; i-- {
		chars := int64(units[i].chars)
		if sum+chars > budget {
			break
		}
		sum += chars
		start = i
	}
	return start
}

// pruneIndex maps a tool call id to the newest pruned output recorded for it.
func pruneIndex(events []core.SessionEvent) (map[string]string, error) {
	out := map[string]string{}
	for _, event := range events {
		if event.Kind != core.KindToolResultPruned {
			continue
		}
		var payload core.ToolResultPruned
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, err
		}
		out[payload.CallID] = payload.Output
	}
	return out, nil
}

// estimateTokens estimates the request size. With a usable step/end after the
// latest compaction it uses that real usage plus the chars of later events;
// otherwise it falls back to a char estimate of system, tools, and messages
// (REQ-LTC-010).
func estimateTokens(events []core.SessionEvent, req core.CompactRequest, derived []core.Message, prunes map[string]string) int64 {
	sumSeq := int64(0)
	for _, event := range events {
		if event.Kind == core.KindCompactionSummary {
			sumSeq = event.Seq
		}
	}
	var usage core.Usage
	stepSeq := int64(0)
	for _, event := range events {
		if event.Seq <= sumSeq || event.Kind != core.KindStepEnd {
			continue
		}
		var payload core.StepEnd
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if payload.Usage.InputTokens > 0 {
			usage = payload.Usage
			stepSeq = event.Seq
		}
	}
	if stepSeq > 0 {
		// The step/end usage already counts the tool outputs as they were sent
		// then. A prune event whose source result is inside that counted span
		// frees chars the usage still charges, so subtract them.
		chars := eventsChars(events, stepSeq, prunes) - pruneRelief(events, sumSeq, stepSeq)
		return usage.InputTokens + usage.OutputTokens + chars/4
	}
	chars := int64(len(req.System)) + int64(len(toolsJSON(req.Tools))) + int64(messagesChars(derived))
	return chars / 4
}

// eventsChars counts the chars of message-producing events with seq > afterSeq.
func eventsChars(events []core.SessionEvent, afterSeq int64, prunes map[string]string) int64 {
	var n int64
	for _, event := range events {
		if event.Seq <= afterSeq {
			continue
		}
		n += int64(eventChars(event, prunes))
	}
	return n
}

// pruneRelief is the char count a step/end usage still charges for tool results
// that the pruner has since shrunk. It counts every tool_result_pruned event
// whose source result sits in the counted span (afterSeq, stepSeq].
func pruneRelief(events []core.SessionEvent, afterSeq, stepSeq int64) int64 {
	if stepSeq == 0 {
		return 0
	}
	original := map[int64]int{}
	for _, event := range events {
		if event.Kind != core.KindToolResult {
			continue
		}
		var payload core.ToolResultPayload
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		original[event.Seq] = len(payload.Output)
	}
	var n int64
	for _, event := range events {
		if event.Kind != core.KindToolResultPruned {
			continue
		}
		var payload core.ToolResultPruned
		if json.Unmarshal(event.Payload, &payload) != nil {
			continue
		}
		if payload.SourceSeq <= afterSeq || payload.SourceSeq > stepSeq {
			continue
		}
		if delta := original[payload.SourceSeq] - len(payload.Output); delta > 0 {
			n += int64(delta)
		}
	}
	return n
}

// eventChars counts the content chars of one message-producing event, applying
// the latest pruned output of a tool result.
func eventChars(event core.SessionEvent, prunes map[string]string) int {
	switch event.Kind {
	case core.KindUserMessage:
		var payload core.UserMessage
		if json.Unmarshal(event.Payload, &payload) != nil {
			return 0
		}
		return contentChars(payload.Content)
	case core.KindAssistantMessage:
		var payload core.AssistantMessage
		if json.Unmarshal(event.Payload, &payload) != nil {
			return 0
		}
		return contentChars(payload.Content)
	case core.KindToolResult:
		var payload core.ToolResultPayload
		if json.Unmarshal(event.Payload, &payload) != nil {
			return 0
		}
		if replacement, ok := prunes[payload.CallID]; ok {
			return len(replacement)
		}
		return len(payload.Output)
	}
	return 0
}

func contentChars(blocks []core.ContentBlock) int {
	var n int
	for _, block := range blocks {
		n += len(block.Text) + len(block.Output) + len(block.Input)
	}
	return n
}

func messagesChars(messages []core.Message) int {
	var n int
	for _, message := range messages {
		n += contentChars(message.Content)
	}
	return n
}

// toolsJSON is a stable string form of the tool declarations.
func toolsJSON(tools []core.ToolSpec) string {
	if len(tools) == 0 {
		return ""
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return ""
	}
	return string(data)
}

// headText is the latest system/message text, the head prompt used to frame a
// manual summary request.
func headText(events []core.SessionEvent) string {
	text := ""
	for _, event := range events {
		if event.Kind != core.KindSystemMessage {
			continue
		}
		var payload core.SystemMessage
		if json.Unmarshal(event.Payload, &payload) == nil {
			text = payload.Text
		}
	}
	return text
}

// checkpointText frames a summary for the model view (REQ-LTC-014).
func checkpointText(summary string) string {
	return core.CheckpointPreamble + "\n\n<compacted-summary>\n" + summary + "\n</compacted-summary>"
}

func allToolResults(blocks []core.ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Kind != core.BlockToolResult {
			return false
		}
	}
	return true
}

func runeCount(text string) int {
	n := 0
	for range text {
		n++
	}
	return n
}
