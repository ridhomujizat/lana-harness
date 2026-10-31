package config

import "sort"

// maxSupportedVersion is the config format version supported by the binary.
const maxSupportedVersion = 1

const baseTOML = `
[[modules]]
id = "llm"
module = "llm"
config = {}

[[modules]]
id = "tools"
module = "tools"
config = {}

[[modules]]
id = "mcp"
module = "mcp"
config = {}

[[modules]]
id = "store"
module = "sqlite_store"
config = { path = "lana.db" }

[[modules]]
id = "agent"
module = "agent"
config = {}

[[modules]]
id = "permission"
module = "permission"
config = {}

[[modules]]
id = "approval"
module = "approval"
config = {}

[[modules]]
id = "compaction"
module = "compaction"
config = {}

[[modules]]
id = "memcapture"
module = "memcapture"
config = {}
`

const headlessTOML = `
[[modules]]
id = "runner"
module = "cli_runner"
config = {}
`

var builtinProfiles = map[string]string{
	"minimal":  "",
	"headless": headlessTOML,
}

// ProfileNames returns the built-in profile names, sorted alphabetically.
func ProfileNames() []string {
	names := make([]string, 0, len(builtinProfiles))
	for name := range builtinProfiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
