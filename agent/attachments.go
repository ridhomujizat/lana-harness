package agent

import (
	"fmt"
	"strings"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/attach"
)

// ownerAttachmentBlocks builds the attachment content blocks of one owner
// message (REQ-ATT-002). Each path is built on its own so a rejected or
// unreadable path does not hide the others; a path that does not resolve
// becomes a text block that says so, and the turn keeps going.
func ownerAttachmentBlocks(workspace string, paths []string) []core.ContentBlock {
	if len(paths) == 0 {
		return nil
	}
	out := make([]core.ContentBlock, 0, len(paths))
	for _, p := range paths {
		blocks, err := attach.Blocks(workspace, []string{p})
		if err != nil {
			out = append(out, core.ContentBlock{
				Kind: core.BlockText,
				Text: fmt.Sprintf("Attachment %s was not found in the workspace attachments.", p),
			})
			continue
		}
		out = append(out, blocks...)
	}
	return out
}

// imagePlaceholderText is the text form of an image block for a model that
// cannot see images (REQ-ATT-003).
func imagePlaceholderText(b core.ContentBlock) string {
	return "[image " + b.Path + ": the current model cannot see images]"
}

// textForImages returns a copy of messages with every image block replaced by
// its placeholder text. A tool_result block keeps its output text and gains one
// placeholder line per image. The returned slice and its blocks are new; the
// input and the stored events are never changed (REQ-ATT-003).
func textForImages(messages []core.Message) []core.Message {
	out := make([]core.Message, len(messages))
	for i, message := range messages {
		blocks := make([]core.ContentBlock, len(message.Content))
		for j, block := range message.Content {
			switch block.Kind {
			case core.BlockImage:
				blocks[j] = core.ContentBlock{Kind: core.BlockText, Text: imagePlaceholderText(block)}
			case core.BlockToolResult:
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
		out[i] = core.Message{Role: message.Role, Content: blocks}
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
