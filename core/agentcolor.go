package core

import "hash/fnv"

// agentColorPalette is the avatar palette of an agent (API-ROOM-006). The order
// is part of the contract: a derived color is palette[fnv32a(name) % 6].
var agentColorPalette = []string{"brand", "success", "warning", "error", "blue", "gray"}

// ResolveAgentColor returns color when it is set, otherwise a stable color from
// the palette derived from name, so an agent keeps one identity without an
// explicit color (REQ-ROOM-009, API-ROOM-006).
func ResolveAgentColor(name, color string) string {
	if color != "" {
		return color
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(name))
	return agentColorPalette[hash.Sum32()%uint32(len(agentColorPalette))]
}
