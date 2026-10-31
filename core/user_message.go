package core

import "strings"

// DisplayContent returns the blocks a display surface should show: the user's
// own content without the blocks lana injects ahead of it. The stored Content
// is not changed, so the model history keeps the injected <context> and
// <system-update> blocks (REQ-LTC-001).
func (m UserMessage) DisplayContent() []ContentBlock {
	if m.ContextBlocks > 0 {
		start := m.ContextBlocks
		if start > len(m.Content) {
			start = len(m.Content)
		}
		return m.Content[start:]
	}
	// Events stored before ContextBlocks existed fall back to matching the
	// injected tags, stopping at the first block that does not match.
	for i, block := range m.Content {
		if !isInjectedContextBlock(block) {
			return m.Content[i:]
		}
	}
	return m.Content[:0]
}

// isInjectedContextBlock reports whether a block is one of the blocks lana
// injects: a text block whose trimmed text opens with <context> or
// <system-update>.
func isInjectedContextBlock(block ContentBlock) bool {
	if block.Kind != BlockText {
		return false
	}
	text := strings.TrimSpace(block.Text)
	return strings.HasPrefix(text, "<context>") || strings.HasPrefix(text, "<system-update>")
}
