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
