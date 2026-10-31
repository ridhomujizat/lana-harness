package memcapture

import (
	"fmt"
	"strings"
)

// captureInstruction is the final user message of a capture request. It is the
// design §2.1 text, filled with the current entries of the owner file and the
// agent memory file.
func captureInstruction(userEntries, memoryEntries []string) string {
	return fmt.Sprintf(`<memory-capture>
This is an automatic memory check, not a message from the owner. Do not call tools.
Look only at what the owner said in the latest turn. Find durable facts worth keeping for future conversations: identity, how to address the owner, language or style preferences, standing instructions, decisions, lasting facts about their environment. Skip task status, one-off requests, and anything temporary. Never include passwords, API keys, or tokens.

Current owner file (target=user):
%s

Current agent memory (target=memory):
%s

Rules:
- A fact already present: skip it.
- A fact that contradicts or updates an entry: {"target":..., "action":"replace", "old_text":"<exact text from that entry>", "content":"<new entry>"}.
- A new fact: {"target":..., "action":"add", "content":"<one short sentence>"}.
- An entry the owner says is wrong and has no replacement: {"target":..., "action":"remove", "old_text":"..."}.
Reply with only a JSON array, at most 5 items. Reply [] when there is nothing to save.
</memory-capture>`, entryBlock(userEntries), entryBlock(memoryEntries))
}

// entryBlock renders entries one per line, or "(empty)" when there are none.
func entryBlock(entries []string) string {
	if len(entries) == 0 {
		return "(empty)"
	}
	return strings.Join(entries, "\n")
}
