package toolbox

import (
	"reflect"
	"strings"
	"testing"
)

// goodManifest is the design example of docs/specs/34-tools/design.md.
const goodManifest = `
id = "obscura"
name = "Obscura"
description = "Headless browser for scraping and automation"
homepage = "https://www.obscura.sh"

[detect]
binary = "obscura"
version_args = ["--version"]
min_version = "0.2.0"

[[install]]
label = "Homebrew"
command = "brew install obscura"

[[install]]
label = "Download (macOS arm64)"
url = "https://example.com/obscura-darwin-arm64.tar.gz"
sha256 = "0123456789abcdef"
os = "darwin"
arch = "arm64"
extract = "obscura"

[uninstall]
command = "brew uninstall obscura"

[[commands]]
name = "fetch"
description = "Load a page in a real browser and return its text"
args = ["fetch", "--dump", "{{format}}", "{{url}}"]
risk = "read"
timeout_secs = 60
output = "json"
workdir = "workspace"

[commands.input]
url = { type = "string", description = "Page URL", required = true }
format = { type = "string", enum = ["markdown", "text", "html"], default = "markdown" }

[env]
API_KEY = "secret:OBSCURA_KEY"
`

// TestParseManifestGood parses the design example with no issues.
func TestParseManifestGood(t *testing.T) {
	manifest, issues := ParseManifest(goodManifest)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if manifest.ID != "obscura" || manifest.Name != "Obscura" {
		t.Fatalf("manifest=%+v", manifest)
	}
	if manifest.Detect.Binary != "obscura" || manifest.Detect.MinVersion != "0.2.0" {
		t.Fatalf("detect=%+v", manifest.Detect)
	}
	if len(manifest.Install) != 2 || manifest.Install[1].URL != "https://example.com/obscura-darwin-arm64.tar.gz" {
		t.Fatalf("install=%+v", manifest.Install)
	}
	if manifest.Uninstall.Command != "brew uninstall obscura" {
		t.Fatalf("uninstall=%+v", manifest.Uninstall)
	}
	if len(manifest.Commands) != 1 {
		t.Fatalf("commands=%+v", manifest.Commands)
	}
	cmd := manifest.Commands[0]
	if cmd.Risk != "read" || cmd.Output != "json" || cmd.Workdir != "workspace" || cmd.TimeoutSecs != 60 {
		t.Fatalf("command=%+v", cmd)
	}
	if !cmd.Input["url"].Required || cmd.Input["format"].Default != "markdown" {
		t.Fatalf("input=%+v", cmd.Input)
	}
	if manifest.Env["API_KEY"] != "secret:OBSCURA_KEY" {
		t.Fatalf("env=%+v", manifest.Env)
	}
	if manifest.Validate() != nil {
		t.Fatalf("Validate: %+v", manifest.Validate())
	}
}

// TestParseManifestDefaults checks the design defaults: timeout 120, output
// text, workdir data and input type string.
func TestParseManifestDefaults(t *testing.T) {
	manifest, issues := ParseManifest(`
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"

[commands.input]
q = { description = "q" }
`)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	cmd := manifest.Commands[0]
	if cmd.TimeoutSecs != defaultTimeoutSecs || cmd.Output != "text" || cmd.Workdir != "data" {
		t.Fatalf("command defaults=%+v", cmd)
	}
	if cmd.Input["q"].Type != "string" {
		t.Fatalf("input type default=%q", cmd.Input["q"].Type)
	}
}

// TestValidateRules checks that every design rule reports its field.
func TestValidateRules(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{
			name: "bad id",
			toml: `
id = "Bad_ID"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"
`,
			want: "id",
		},
		{
			name: "missing name",
			toml: `
id = "x"

[detect]
binary = "x"

[[install]]
command = "true"
`,
			want: "name",
		},
		{
			name: "missing detect binary",
			toml: `
id = "x"
name = "X"

[[install]]
command = "true"
`,
			want: "detect.binary",
		},
		{
			name: "binary with a path",
			toml: `
id = "x"
name = "X"

[detect]
binary = "bin/x"

[[install]]
command = "true"
`,
			want: "detect.binary",
		},
		{
			name: "no install method",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"
`,
			want: "install",
		},
		{
			name: "install with both command and url",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"
url = "https://example.com/x"
`,
			want: "install[0]",
		},
		{
			name: "install with neither",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
label = "nothing"
`,
			want: "install[0]",
		},
		{
			name: "bad os filter",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
url = "https://example.com/x"
os = "windows"
`,
			want: "install[0].os",
		},
		{
			name: "missing risk",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
`,
			want: "commands[0].risk",
		},
		{
			name: "bad output",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"
output = "yaml"
`,
			want: "commands[0].output",
		},
		{
			name: "bad workdir",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"
workdir = "tmp"
`,
			want: "commands[0].workdir",
		},
		{
			name: "timeout too large",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"
timeout_secs = 100000
`,
			want: "commands[0].timeout_secs",
		},
		{
			name: "bad command name",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "Go"
risk = "read"
`,
			want: "commands[0].name",
		},
		{
			name: "duplicate command",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"

[[commands]]
name = "go"
risk = "read"
`,
			want: "commands[1].name",
		},
		{
			name: "bad input type",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"

[commands.input]
q = { type = "float" }
`,
			want: "commands[0].input.q.type",
		},
		{
			name: "default outside enum",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"

[commands.input]
q = { type = "string", enum = ["a", "b"], default = "c" }
`,
			want: "commands[0].input.q.default",
		},
		{
			name: "arg references undeclared field",
			toml: `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[[commands]]
name = "go"
risk = "read"
args = ["run", "{{missing}}"]
`,
			want: "commands[0].args[1]",
		},
		{
			name: "toml syntax error",
			toml: `id = `,
			want: "toml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := ParseManifest(tc.toml)
			if !hasField(issues, tc.want) {
				t.Fatalf("want an issue on %q, got %+v", tc.want, issues)
			}
		})
	}
}

// TestEncodeRoundTrip encodes and reparses the design example unchanged.
func TestEncodeRoundTrip(t *testing.T) {
	manifest, issues := ParseManifest(goodManifest)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	text, err := Encode(manifest)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	again, issues := ParseManifest(text)
	if len(issues) != 0 {
		t.Fatalf("round trip issues: %+v\n%s", issues, text)
	}
	if !reflect.DeepEqual(manifest, again) {
		t.Fatalf("round trip mismatch:\nfirst=%+v\nsecond=%+v\ntoml=%s", manifest, again, text)
	}
}

// TestToolName checks the <pkg>_<cmd> form, with dashes turned into
// underscores.
func TestToolName(t *testing.T) {
	if got := ToolName("obscura", "fetch"); got != "obscura_fetch" {
		t.Fatalf("ToolName=%q", got)
	}
	if got := ToolName("my-pkg", "do-it"); got != "my_pkg_do-it" {
		t.Fatalf("ToolName=%q", got)
	}
}

// TestInputSchema checks the JSON Schema built from a command's input fields.
func TestInputSchema(t *testing.T) {
	schema := string(InputSchema(Command{Input: map[string]InputField{
		"url":    {Type: "string", Description: "Page URL", Required: true},
		"format": {Type: "string", Enum: []string{"markdown", "text"}, Default: "markdown"},
	}}))
	for _, want := range []string{`"type":"object"`, `"required":["url"]`, `"enum":["markdown","text"]`, `"default":"markdown"`, `"description":"Page URL"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema %s does not contain %s", schema, want)
		}
	}
}

// TestParseManifestCatalogFields reads the revision 2 fields: icon, category,
// requires and the [mcp] spec (REQ-TB-001, REQ-TB-009, REQ-TB-011).
func TestParseManifestCatalogFields(t *testing.T) {
	manifest, issues := ParseManifest(`
id = "obscura"
name = "Obscura"
icon = "globe"
category = "Browser"

[detect]
binary = "obscura"
version_args = ["--version"]

[[install]]
label = "Download (macOS)"
url = "https://example.com/obscura.tar.gz"
os = "macos"
arch = "aarch64"
extract = "*"

[[install]]
label = "Homebrew"
command = "brew install obscura"
requires = ["brew"]

[mcp]
args = ["--stealth", "--storage-dir", "{{data}}/profile", "mcp"]

[mcp.env]
TOKEN = "secret:OBSCURA_KEY"

[[commands]]
name = "fetch"
risk = "read"
args = ["fetch", "{{url}}"]

[commands.input]
url = { type = "string", required = true }
`)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	if manifest.Icon != "globe" || manifest.Category != "Browser" {
		t.Fatalf("icon=%q category=%q", manifest.Icon, manifest.Category)
	}
	if got := manifest.Install[1].Requires; len(got) != 1 || got[0] != "brew" {
		t.Fatalf("requires=%v", got)
	}
	if manifest.Install[0].Extract != "*" {
		t.Fatalf("extract=%q", manifest.Install[0].Extract)
	}
	if manifest.MCP == nil || len(manifest.MCP.Args) != 4 {
		t.Fatalf("mcp=%+v", manifest.MCP)
	}
	if manifest.MCP.Env["TOKEN"] != "secret:OBSCURA_KEY" {
		t.Fatalf("mcp env=%v", manifest.MCP.Env)
	}
}

// TestValidateMCPNeedsArgs requires at least one argument in an [mcp] spec
// (REQ-TB-011).
func TestValidateMCPNeedsArgs(t *testing.T) {
	_, issues := ParseManifest(`
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"

[mcp]
args = []
`)
	if !hasField(issues, "mcp.args") {
		t.Fatalf("issues=%+v want mcp.args", issues)
	}
}

// TestEncodeRoundTripCatalogFields keeps icon, category, requires and the mcp
// spec across an encode and reparse (REQ-TB-001).
func TestEncodeRoundTripCatalogFields(t *testing.T) {
	original, issues := ParseManifest(`
id = "obscura"
name = "Obscura"
icon = "globe"
category = "Browser"

[detect]
binary = "obscura"
version_args = ["--version"]

[[install]]
label = "Homebrew"
command = "brew install obscura"
requires = ["brew"]

[[install]]
label = "Download"
url = "https://example.com/obscura.tar.gz"
os = "darwin"
arch = "arm64"
extract = "*"

[mcp]
args = ["mcp"]

[mcp.env]
TOKEN = "literal"
`)
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	text, err := Encode(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	again, issues := ParseManifest(text)
	if len(issues) != 0 {
		t.Fatalf("round trip issues: %+v\n%s", issues, text)
	}
	if !reflect.DeepEqual(original, again) {
		t.Fatalf("round trip mismatch:\nfirst=%+v\nsecond=%+v\ntoml=%s", original, again, text)
	}
}

// filesManifest wraps one command table in a valid package (REQ-TB-012).
func filesManifest(command string) string {
	return `
id = "x"
name = "X"

[detect]
binary = "x"

[[install]]
command = "true"
` + command
}

// TestValidateOutputFiles checks the output_from, {{out}} and transform rules
// (REQ-TB-012).
func TestValidateOutputFiles(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name: "out without output_from files",
			command: `
[[commands]]
name = "go"
risk = "read"
args = ["{{out}}/x"]
`,
			want: "commands[0].args[0]",
		},
		{
			name: "output_from files without out",
			command: `
[[commands]]
name = "go"
risk = "read"
args = ["run"]
output_from = "files"
`,
			want: "commands[0].output_from",
		},
		{
			name: "unknown output_from",
			command: `
[[commands]]
name = "go"
risk = "read"
output_from = "stderr"
`,
			want: "commands[0].output_from",
		},
		{
			name: "out declared as an input",
			command: `
[[commands]]
name = "go"
risk = "read"
args = ["{{out}}"]
output_from = "files"

[commands.input]
out = { type = "string" }
`,
			want: "commands[0].input.out",
		},
		{
			name: "unknown transform",
			command: `
[[commands]]
name = "go"
risk = "read"
transform = "markdown"
`,
			want: "commands[0].transform",
		},
		{
			name: "subtitles with json output",
			command: `
[[commands]]
name = "go"
risk = "read"
output = "json"
transform = "subtitles"
`,
			want: "commands[0].transform",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, issues := ParseManifest(filesManifest(tc.command))
			if !hasField(issues, tc.want) {
				t.Fatalf("want an issue on %q, got %+v", tc.want, issues)
			}
		})
	}

	manifest, issues := ParseManifest(filesManifest(`
[[commands]]
name = "go"
risk = "read"
args = ["-o", "{{out}}/%(id)s.%(ext)s", "{{url}}"]
output_from = "files"
transform = "subtitles"

[commands.input]
url = { type = "string", required = true }

[[commands]]
name = "plain"
risk = "read"
args = ["{{out}}"]

[commands.input]
out = { type = "string" }
`))
	if len(issues) != 0 {
		t.Fatalf("valid files command issues: %+v", issues)
	}
	if manifest.Commands[0].OutputFrom != "files" || manifest.Commands[1].OutputFrom != "stdout" {
		t.Fatalf("output_from=%q,%q want files,stdout", manifest.Commands[0].OutputFrom, manifest.Commands[1].OutputFrom)
	}
}

// TestEncodeRoundTripOutputFiles keeps output_from and transform across an
// encode and reparse (REQ-TB-012).
func TestEncodeRoundTripOutputFiles(t *testing.T) {
	original, issues := ParseManifest(filesManifest(`
[[commands]]
name = "go"
risk = "read"
args = ["{{out}}"]
output_from = "files"
transform = "subtitles"
`))
	if len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
	text, err := Encode(original)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	again, issues := ParseManifest(text)
	if len(issues) != 0 {
		t.Fatalf("round trip issues: %+v\n%s", issues, text)
	}
	if !reflect.DeepEqual(original, again) {
		t.Fatalf("round trip mismatch:\nfirst=%+v\nsecond=%+v\ntoml=%s", original, again, text)
	}
	if !strings.Contains(text, `output_from = 'files'`) && !strings.Contains(text, `output_from = "files"`) {
		t.Fatalf("encoded manifest lacks output_from:\n%s", text)
	}
}

// hasField reports whether any issue sits on the wanted field.
func hasField(issues []Issue, field string) bool {
	for _, issue := range issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}
