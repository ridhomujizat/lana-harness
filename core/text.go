package core

import "strings"

// TextContent returns the concatenated text of a message's text blocks,
// skipping thinking and other non-text blocks. Display surfaces use it so
// reasoning never reaches user-facing text (REQ-MDL-006). The stored content is
// not changed.
func TextContent(blocks []ContentBlock) string {
	var builder strings.Builder
	for _, block := range blocks {
		if block.Kind != BlockText || block.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(block.Text)
	}
	return builder.String()
}

// imagePlaceholderText is the text form of an image block for a model that
// cannot see images (REQ-ATT-003).
func imagePlaceholderText(b ContentBlock) string {
	return "[image " + b.Path + ": the current model cannot see images]"
}

// TextForImages returns a copy of messages with every image block replaced by
// its placeholder text. A tool_result block keeps its output text and gains one
// placeholder line per image. The returned slice and its blocks are new; the
// input and the stored events are never changed (REQ-ATT-003).
func TextForImages(messages []Message) []Message {
	out := make([]Message, len(messages))
	for i, message := range messages {
		blocks := make([]ContentBlock, len(message.Content))
		for j, block := range message.Content {
			switch block.Kind {
			case BlockImage:
				blocks[j] = ContentBlock{Kind: BlockText, Text: imagePlaceholderText(block)}
			case BlockToolResult:
				if len(block.Images) > 0 {
					lines := make([]string, 0, len(block.Images))
					for _, image := range block.Images {
						lines = append(lines, imagePlaceholderText(image))
					}
					block.Output = appendLines(block.Output, lines)
					block.Images = nil
				}
				blocks[j] = block
			default:
				blocks[j] = block
			}
		}
		out[i] = Message{Role: message.Role, Content: blocks}
	}
	return out
}

// appendLines adds the given lines to text, newline-separated, with no leading
// newline when text is empty.
func appendLines(text string, lines []string) string {
	extra := strings.Join(lines, "\n")
	if text == "" {
		return extra
	}
	return text + "\n" + extra
}
