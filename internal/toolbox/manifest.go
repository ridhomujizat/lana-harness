// Package toolbox implements installable tools (spec #34): the tool.toml
// manifest format and its validation, the file store under $LANA_HOME/tools,
// binary detection with status, install/uninstall jobs with live logs, command
// execution and the Service that ties them together. It is the whole of
// REQ-TB-001..008 with no dependency beyond the standard library, the TOML
// library already in go.mod and this module's core/internal packages.
package toolbox

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// idPattern is the package id form of the design: it must start with a letter
// or digit, hold lowercase letters, digits and dashes, and be at most 40
// characters (REQ-TB-001).
var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Source values of a package: "catalog" for a built-in manifest, "custom" for
// one stored on disk (REQ-TB-001).
const (
	SourceCatalog = "catalog"
	SourceCustom  = "custom"
)

// wholeArchive is the extract value that unpacks every file of an archive
// (REQ-TB-010).
const wholeArchive = "*"

// commandNamePattern is the command name form of the design:
// `^[a-z][a-z0-9_]{0,29}$` (REQ-TB-004).
var commandNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,29}$`)

// refRE matches one {{field}} reference inside an argv template (REQ-TB-004).
var refRE = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// Defaults and bounds of a command (design "Manifest").
const (
	defaultTimeoutSecs = 120
	maxTimeoutSecs     = 3600
)

// Manifest is one package definition, the tool.toml file of REQ-TB-001.
type Manifest struct {
	ID          string            `toml:"id" json:"id"`
	Name        string            `toml:"name" json:"name"`
	Description string            `toml:"description,omitempty" json:"description"`
	Homepage    string            `toml:"homepage,omitempty" json:"homepage"`
	Icon        string            `toml:"icon,omitempty" json:"icon,omitempty"`
	Category    string            `toml:"category,omitempty" json:"category,omitempty"`
	Detect      Detect            `toml:"detect" json:"detect"`
	Install     []InstallMethod   `toml:"install,omitempty" json:"install"`
	Uninstall   UninstallSpec     `toml:"uninstall" json:"uninstall"`
	Commands    []Command         `toml:"commands,omitempty" json:"commands"`
	Env         map[string]string `toml:"env,omitempty" json:"env"`
	MCP         *MCPSpec          `toml:"mcp,omitempty" json:"mcp,omitempty"`
}

// MCPSpec is the optional MCP server a package provides: the args of the
// resolved binary, with the {{data}} and {{bin}} templates, and the env it
// starts with (REQ-TB-011).
type MCPSpec struct {
	Args []string          `toml:"args" json:"args"`
	Env  map[string]string `toml:"env,omitempty" json:"env,omitempty"`
}

// Detect finds the package binary and its version (REQ-TB-002).
type Detect struct {
	Binary      string   `toml:"binary" json:"binary"`
	VersionArgs []string `toml:"version_args,omitempty" json:"version_args"`
	MinVersion  string   `toml:"min_version,omitempty" json:"min_version"`
}

// InstallMethod is one way to install a package: a shell command or a direct
// download, filtered by OS and arch (REQ-TB-003). Requires names programs that
// must be on PATH for the method to apply (REQ-TB-009).
type InstallMethod struct {
	Label    string   `toml:"label,omitempty" json:"label"`
	Command  string   `toml:"command,omitempty" json:"command"`
	URL      string   `toml:"url,omitempty" json:"url"`
	SHA256   string   `toml:"sha256,omitempty" json:"sha256"`
	OS       string   `toml:"os,omitempty" json:"os"`
	Arch     string   `toml:"arch,omitempty" json:"arch"`
	Extract  string   `toml:"extract,omitempty" json:"extract"`
	Requires []string `toml:"requires,omitempty" json:"requires,omitempty"`
}

// UninstallSpec is the optional cleanup command of an uninstall (REQ-TB-008).
type UninstallSpec struct {
	Command string `toml:"command" json:"command"`
}

// Command is one CLI call a package exposes as a lana tool (REQ-TB-004).
type Command struct {
	Name        string                `toml:"name" json:"name"`
	Description string                `toml:"description,omitempty" json:"description"`
	Args        []string              `toml:"args,omitempty" json:"args"`
	Risk        string                `toml:"risk" json:"risk"`
	TimeoutSecs int                   `toml:"timeout_secs" json:"timeout_secs"`
	Output      string                `toml:"output" json:"output"`
	Workdir     string                `toml:"workdir" json:"workdir"`
	Input       map[string]InputField `toml:"input,omitempty" json:"input"`
}

// InputField is one declared input of a command (REQ-TB-004).
type InputField struct {
	Type        string   `toml:"type" json:"type"`
	Description string   `toml:"description,omitempty" json:"description"`
	Required    bool     `toml:"required" json:"required"`
	Default     any      `toml:"default,omitempty" json:"default,omitempty"`
	Enum        []string `toml:"enum,omitempty" json:"enum,omitempty"`
}

// Issue is one manifest validation problem (design "Manifest").
type Issue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ParseManifest parses a tool.toml document, applies the design defaults and
// validates the result. A TOML syntax error yields one issue with a field of
// "toml" (REQ-TB-001).
func ParseManifest(text string) (Manifest, []Issue) {
	var m Manifest
	if err := toml.Unmarshal([]byte(text), &m); err != nil {
		return Manifest{}, []Issue{{Field: "toml", Message: strings.TrimSpace(err.Error())}}
	}
	applyDefaults(&m)
	return m, m.Validate()
}

// applyDefaults fills the defaults of the design: timeout_secs 120, output
// "text", workdir "data" and input type "string". risk has no default: an
// empty risk is a validation error (REQ-TB-004).
func applyDefaults(m *Manifest) {
	for i := range m.Commands {
		c := &m.Commands[i]
		if c.TimeoutSecs == 0 {
			c.TimeoutSecs = defaultTimeoutSecs
		}
		if c.Output == "" {
			c.Output = "text"
		}
		if c.Workdir == "" {
			c.Workdir = "data"
		}
		for name, f := range c.Input {
			if f.Type == "" {
				f.Type = "string"
				c.Input[name] = f
			}
		}
	}
}

// Validate checks a manifest against every rule of the design section
// "Manifest". The issues are sorted by field then message.
func (m Manifest) Validate() []Issue {
	var issues []Issue
	add := func(field, format string, args ...any) {
		issues = append(issues, Issue{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	if !idPattern.MatchString(m.ID) {
		add("id", "id %q must match %s", m.ID, idPattern.String())
	}
	if strings.TrimSpace(m.Name) == "" {
		add("name", "name is required")
	}
	switch {
	case m.Detect.Binary == "":
		add("detect.binary", "detect.binary is required")
	case strings.ContainsAny(m.Detect.Binary, `/\`):
		add("detect.binary", "detect.binary must be a file name without a path")
	}
	if len(m.Install) == 0 {
		add("install", "at least one install method is required")
	}
	for i, md := range m.Install {
		field := fmt.Sprintf("install[%d]", i)
		hasCommand := strings.TrimSpace(md.Command) != ""
		hasURL := strings.TrimSpace(md.URL) != ""
		switch {
		case hasCommand && hasURL:
			add(field, "an install method has both command and url; exactly one is allowed")
		case !hasCommand && !hasURL:
			add(field, "an install method needs a command or a url")
		}
		if md.OS != "" && !validOS(md.OS) {
			add(field+".os", "os %q must be darwin, macos or linux", md.OS)
		}
		if md.Arch != "" && !validArch(md.Arch) {
			add(field+".arch", "arch %q must be arm64, aarch64, amd64 or x86_64", md.Arch)
		}
	}

	seen := make(map[string]bool, len(m.Commands))
	for i, c := range m.Commands {
		field := fmt.Sprintf("commands[%d]", i)
		if !commandNamePattern.MatchString(c.Name) {
			add(field+".name", "command name %q must match %s", c.Name, commandNamePattern.String())
		}
		if seen[c.Name] {
			add(field+".name", "command name %q is used more than once", c.Name)
		}
		seen[c.Name] = true
		if !validRisk(c.Risk) {
			add(field+".risk", "risk %q is required and must be read, write or admin", c.Risk)
		}
		if !validOutput(c.Output) {
			add(field+".output", "output %q must be text or json", c.Output)
		}
		if !validWorkdir(c.Workdir) {
			add(field+".workdir", "workdir %q must be data or workspace", c.Workdir)
		}
		if c.TimeoutSecs < 0 || c.TimeoutSecs > maxTimeoutSecs {
			add(field+".timeout_secs", "timeout_secs must be between 0 and %d", maxTimeoutSecs)
		}
		for _, name := range sortedNames(c.Input) {
			f := c.Input[name]
			inputField := field + ".input." + name
			if !validInputType(f.Type) {
				add(inputField+".type", "input type %q must be string, number, integer or boolean", f.Type)
			}
			if f.Default != nil && len(f.Enum) > 0 && !containsString(f.Enum, formatValue(f.Default)) {
				add(inputField+".default", "default %v is not one of the allowed values", f.Default)
			}
		}
		for j, arg := range c.Args {
			for _, ref := range refRE.FindAllStringSubmatch(arg, -1) {
				if _, ok := c.Input[ref[1]]; !ok {
					add(fmt.Sprintf("%s.args[%d]", field, j), "arg references undeclared field %q", ref[1])
				}
			}
		}
	}

	if m.MCP != nil && len(m.MCP.Args) == 0 {
		add("mcp.args", "an mcp spec needs at least one argument")
	}

	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Field != issues[j].Field {
			return issues[i].Field < issues[j].Field
		}
		return issues[i].Message < issues[j].Message
	})
	return issues
}

// Encode renders a manifest as a tool.toml document (REQ-TB-001).
func Encode(m Manifest) (string, error) {
	b, err := toml.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// MethodFits reports whether install method i applies to this machine: its
// os/arch filter matches (with the macos, x86_64 and aarch64 aliases) and every
// program in Requires is on PATH. The string explains why it does not fit
// (REQ-TB-009).
func (m Manifest) MethodFits(i int) (bool, string) {
	if i < 0 || i >= len(m.Install) {
		return false, fmt.Sprintf("install method %d does not exist", i)
	}
	md := m.Install[i]
	if md.OS != "" && normalizeOS(md.OS) != runtime.GOOS {
		return false, fmt.Sprintf("os %q does not match %s", md.OS, runtime.GOOS)
	}
	if md.Arch != "" && normalizeArch(md.Arch) != runtime.GOARCH {
		return false, fmt.Sprintf("arch %q does not match %s", md.Arch, runtime.GOARCH)
	}
	for _, program := range md.Requires {
		if strings.TrimSpace(program) == "" {
			continue
		}
		if _, err := exec.LookPath(program); err != nil {
			return false, fmt.Sprintf("requires %s on PATH", program)
		}
	}
	return true, ""
}

// AutoMethod returns the first install method that fits this machine, or -1 and
// false when none does (REQ-TB-009).
func (m Manifest) AutoMethod() (int, bool) {
	for i := range m.Install {
		if ok, _ := m.MethodFits(i); ok {
			return i, true
		}
	}
	return -1, false
}

// ToolName is the tool name of one command: the package id with dashes turned
// into underscores, then the command name (REQ-TB-004).
func ToolName(pkg, cmd string) string {
	return strings.ReplaceAll(pkg, "-", "_") + "_" + cmd
}

// validOS reports whether an install method os filter is known, accepting the
// macos alias of darwin (REQ-TB-009).
func validOS(os string) bool {
	switch normalizeOS(os) {
	case "darwin", "linux":
		return true
	default:
		return false
	}
}

// validArch reports whether an install method arch filter is known, accepting
// the x86_64 and aarch64 aliases (REQ-TB-009).
func validArch(arch string) bool {
	switch normalizeArch(arch) {
	case "arm64", "amd64":
		return true
	default:
		return false
	}
}

// normalizeOS maps an os filter to its runtime.GOOS value (REQ-TB-009).
func normalizeOS(os string) string {
	switch strings.ToLower(strings.TrimSpace(os)) {
	case "macos", "darwin":
		return "darwin"
	case "linux":
		return "linux"
	default:
		return strings.ToLower(strings.TrimSpace(os))
	}
}

// normalizeArch maps an arch filter to its runtime.GOARCH value (REQ-TB-009).
func normalizeArch(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

// validRisk reports whether risk is one of the design values. risk is required,
// so "read" is not a default.
func validRisk(risk string) bool {
	switch risk {
	case "read", "write", "admin":
		return true
	default:
		return false
	}
}

// validOutput reports whether output is text or json.
func validOutput(output string) bool {
	return output == "text" || output == "json"
}

// validWorkdir reports whether workdir is data or workspace.
func validWorkdir(workdir string) bool {
	return workdir == "data" || workdir == "workspace"
}

// validInputType reports whether an input field type is allowed.
func validInputType(t string) bool {
	switch t {
	case "string", "number", "integer", "boolean":
		return true
	default:
		return false
	}
}

// sortedNames returns the map keys in sorted order.
func sortedNames(m map[string]InputField) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// containsString reports whether list holds want.
func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
