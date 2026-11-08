package core

import (
	"hash/fnv"
	"regexp"
)

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

var agentHexColorRE = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidAgentColor reports whether value is a palette token or a "#RRGGBB"
// hex color (spec 43, API-AVT-004). "" is valid: it derives the color from
// the name.
func ValidAgentColor(value string) bool {
	if value == "" || agentHexColorRE.MatchString(value) {
		return true
	}
	for _, token := range agentColorPalette {
		if value == token {
			return true
		}
	}
	return false
}

// AgentColorMessage is the validation message of a bad agent color.
const AgentColorMessage = "color must be one of brand, success, warning, error, blue, gray, or a #RRGGBB hex color"
