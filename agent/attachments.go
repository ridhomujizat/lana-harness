package agent

import (
	"fmt"

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
