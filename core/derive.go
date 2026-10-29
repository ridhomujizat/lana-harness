package core

import (
	"encoding/json"
	"fmt"
)

// DeriveMessages menyusun riwayat model dari rantai event.
func DeriveMessages(events []SessionEvent) ([]Message, error) {
	var messages []Message
	var summary string
	for _, event := range events {
		switch event.Kind {
		case KindCompactionSummary:
			var payload CompactionSummary
			if err := decodePayload(event.Payload, &payload); err != nil {
				return nil, err
			}
			summary = payload.Summary
		case KindUserMessage:
			var payload UserMessage
			if err := decodePayload(event.Payload, &payload); err != nil {
				return nil, err
			}
			content := append([]ContentBlock(nil), payload.Content...)
			if summary != "" {
				content = append([]ContentBlock{{Kind: BlockText, Text: summary}}, content...)
				summary = ""
			}
			messages = append(messages, Message{Role: RoleUser, Content: content})
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
			block := ContentBlock{
				Kind:    BlockToolResult,
				ID:      payload.CallID,
				Output:  payload.Output,
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
