package core

import (
	"encoding/json"
	"fmt"
)

// DeriveMessages builds the model history from the event chain.
//
// When the newest compaction/summary carries a RetainedFromSeq, the summary
// becomes one framed user message and every message-producing event with
// seq > UptoSeq follows it. A summary without RetainedFromSeq keeps the older
// behavior: everything up to the summary event is dropped (REQ-LTC-016).
func DeriveMessages(events []SessionEvent) ([]Message, error) {
	var summary CompactionSummary
	summarySeq := int64(0)
	found := false
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind != KindCompactionSummary {
			continue
		}
		if err := decodePayload(events[i].Payload, &summary); err != nil {
			return nil, err
		}
		summarySeq = events[i].Seq
		found = true
		break
	}

	pruned, err := pruneIndex(events)
	if err != nil {
		return nil, err
	}

	var messages []Message
	startSeq := int64(0)
	if found {
		if summary.RetainedFromSeq > 0 {
			startSeq = summary.UptoSeq
			messages = append(messages, Message{Role: RoleUser, Content: []ContentBlock{
				{Kind: BlockText, Text: checkpointMessage(summary.Summary)},
			}})
		} else {
			startSeq = summarySeq
			if summary.Summary != "" {
				messages = append(messages, Message{Role: RoleUser, Content: []ContentBlock{{Kind: BlockText, Text: summary.Summary}}})
			}
		}
	}
	for _, event := range events {
		if startSeq > 0 && event.Seq <= startSeq {
			continue
		}
		switch event.Kind {
		case KindCompactionSummary, KindToolResultPruned:
			continue
		case KindUserMessage:
			var payload UserMessage
			if err := decodePayload(event.Payload, &payload); err != nil {
				return nil, err
			}
			messages = append(messages, Message{Role: RoleUser, Content: append([]ContentBlock(nil), payload.Content...)})
		case KindAssistantMessage:
			var payload AssistantMessage
			if err := decodePayload(event.Payload, &payload); err != nil {
				return nil, err
			}
			messages = append(messages, Message{Role: RoleAssistant, Content: append([]ContentBlock(nil), payload.Content...)})
		case KindToolResult:
			var payload ToolResultPayload
			if err := decodePayload(event.Payload, &payload); err != nil {
				return nil, err
			}
			output, ok := pruned[payload.CallID]
			if !ok {
				output = payload.Output
			}
			block := ContentBlock{
				Kind:    BlockToolResult,
				ID:      payload.CallID,
				Output:  output,
				IsError: payload.Status != ToolStatusSuccess,
			}
			if n := len(messages); n > 0 && messages[n-1].Role == RoleUser && allToolResults(messages[n-1].Content) {
				messages[n-1].Content = append(messages[n-1].Content, block)
			} else {
				messages = append(messages, Message{Role: RoleUser, Content: []ContentBlock{block}})
			}
		}
	}
	return messages, nil
}

// checkpointMessage frames a summary for the model view.
func checkpointMessage(summary string) string {
	return CheckpointPreamble + "\n\n<compacted-summary>\n" + summary + "\n</compacted-summary>"
}

// pruneIndex maps a tool call id to the newest pruned output recorded for it.
func pruneIndex(events []SessionEvent) (map[string]string, error) {
	out := map[string]string{}
	for _, event := range events {
		if event.Kind != KindToolResultPruned {
			continue
		}
		var payload ToolResultPruned
		if err := decodePayload(event.Payload, &payload); err != nil {
			return nil, err
		}
		out[payload.CallID] = payload.Output
	}
	return out, nil
}

func allToolResults(blocks []ContentBlock) bool {
	if len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Kind != BlockToolResult {
			return false
		}
	}
	return true
}

func decodePayload(raw json.RawMessage, into any) error {
	if err := json.Unmarshal(raw, into); err != nil {
		return err
	}
	var version struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return err
	}
	if version.V != 0 && version.V != PayloadVersion {
		return fmt.Errorf("%w: %d", ErrUnknownPayloadVersion, version.V)
	}
	return nil
}
