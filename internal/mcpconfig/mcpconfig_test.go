package mcpconfig

import (
	"reflect"
	"testing"
)

// TestRender checks the exact bytes of the table the web save handler writes.
func TestRender(t *testing.T) {
	cases := []struct {
		name string
		def  Definition
		want string
	}{
		{
			name: "command server with args and env",
			def: Definition{
				Type:    "command",
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/Users/ridho/notes"},
				Env:     map[string]string{"TOKEN": "${TOKEN}", "LOG": "debug"},
			},
			want: `[mcp.docs]
command = 'npx'
args = ['-y', '@modelcontextprotocol/server-filesystem', '/Users/ridho/notes']
env = { LOG = 'debug', TOKEN = '${TOKEN}' }
`,
		},
		{
			name: "URL server with headers",
			def: Definition{
				Type:    "url",
				URL:     "https://example.com/mcp",
				Headers: map[string]string{"Authorization": "Bearer remote-secret"},
			},
			want: `[mcp.remote]
url = 'https://example.com/mcp'
headers = { Authorization = 'Bearer remote-secret' }
`,
		},
		{
			name: "empty definition writes only the header",
			def:  Definition{Type: "command"},
			want: "[mcp.empty]\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			name := "docs"
			if tc.name == "URL server with headers" {
				name = "remote"
			}
			if tc.name == "empty definition writes only the header" {
				name = "empty"
			}
			if got := Render(name, tc.def); got != tc.want {
				t.Fatalf("Render:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestRenderList checks the TOML string array format and its empty case.
func TestRenderList(t *testing.T) {
	cases := []struct {
		name   string
		values []string
		want   string
	}{
		{name: "values", values: []string{"--verbose", "--port", "0"}, want: `['--verbose', '--port', '0']`},
		{name: "empty", values: []string{}, want: "[]"},
		{name: "nil", values: nil, want: "[]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RenderList(tc.values); got != tc.want {
				t.Fatalf("RenderList = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestUsage(t *testing.T) {
	doc := Document{Agents: map[string]map[string]any{
		"writer": {"mcp": []any{"docs", "remote"}},
		"coder":  {"mcp": []any{"docs"}},
		"idle":   {"provider": "mock"},
		"other":  {"mcp": []string{"remote"}},
	}}
	want := map[string][]string{
		"docs":   {"coder", "writer"},
		"remote": {"other", "writer"},
	}
	if got := Usage(doc); !reflect.DeepEqual(got, want) {
		t.Fatalf("Usage = %v, want %v", got, want)
	}
}

func TestAgentList(t *testing.T) {
	doc := Document{Agents: map[string]map[string]any{
		"coder": {"mcp": []any{"docs", "remote"}},
		"idle":  {"provider": "mock"},
		"typed": {"mcp": []string{"fs"}},
	}}
	if got, want := AgentList(doc, "coder"), []string{"docs", "remote"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentList(coder) = %v, want %v", got, want)
	}
	if got := AgentList(doc, "idle"); got != nil {
		t.Fatalf("AgentList(idle) = %v, want nil", got)
	}
	if got := AgentList(doc, "missing"); got != nil {
		t.Fatalf("AgentList(missing) = %v, want nil", got)
	}
	if got, want := AgentList(doc, "typed"), []string{"fs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentList(typed) = %v, want %v", got, want)
	}
}

func TestNameRE(t *testing.T) {
	for _, name := range []string{"docs", "my-server", "a1", "0"} {
		if !NameRE.MatchString(name) {
			t.Fatalf("NameRE rejected %q", name)
		}
	}
	for _, name := range []string{"", "Docs", "my_server", "my.server", "with space", "a/b"} {
		if NameRE.MatchString(name) {
			t.Fatalf("NameRE accepted %q", name)
		}
	}
}

func TestRedactValues(t *testing.T) {
	message := "failed with secret-token and header-secret and mask *** and empty "
	got := RedactValues(message,
		map[string]string{"TOKEN": "secret-token", "MASK": "***", "EMPTY": ""},
		map[string]string{"Authorization": "header-secret"},
	)
	want := "failed with *** and *** and mask *** and empty "
	if got != want {
		t.Fatalf("RedactValues = %q, want %q", got, want)
	}
	if got := RedactValues("unchanged"); got != "unchanged" {
		t.Fatalf("RedactValues with no values = %q", got)
	}
}

func TestFromMap(t *testing.T) {
	command := FromMap(map[string]any{
		"command": "npx",
		"args":    []any{"-y", "server", 3},
		"env":     map[string]any{"TOKEN": "${TOKEN}", "N": 1},
	})
	wantCommand := Definition{
		Type:    "command",
		Command: "npx",
		Args:    []string{"-y", "server"},
		Env:     map[string]string{"TOKEN": "${TOKEN}"},
	}
	if !reflect.DeepEqual(command, wantCommand) {
		t.Fatalf("FromMap command = %+v, want %+v", command, wantCommand)
	}

	remote := FromMap(map[string]any{
		"url":     "https://example.com/mcp",
		"headers": map[string]any{"Authorization": "Bearer secret"},
	})
	wantRemote := Definition{
		Type:    "url",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "Bearer secret"},
	}
	if !reflect.DeepEqual(remote, wantRemote) {
		t.Fatalf("FromMap remote = %+v, want %+v", remote, wantRemote)
	}

	if got := FromMap(nil); got.Type != "command" || got.Command != "" || got.URL != "" {
		t.Fatalf("FromMap(nil) = %+v, want an empty command definition", got)
	}
}

func TestNormalize(t *testing.T) {
	command := Definition{
		Type:    "command",
		Command: "npx",
		Args:    []string{"-y"},
		Env:     map[string]string{"TOKEN": "x"},
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "x"},
	}
	Normalize(&command)
	if command.URL != "" || command.Headers != nil {
		t.Fatalf("Normalize command kept URL fields: %+v", command)
	}
	if command.Command != "npx" || len(command.Args) != 1 || command.Env["TOKEN"] != "x" {
		t.Fatalf("Normalize command dropped command fields: %+v", command)
	}

	remote := Definition{
		Type:    "url",
		Command: "npx",
		Args:    []string{"-y"},
		Env:     map[string]string{"TOKEN": "x"},
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "x"},
	}
	Normalize(&remote)
	if remote.Command != "" || remote.Args != nil || remote.Env != nil {
		t.Fatalf("Normalize url kept command fields: %+v", remote)
	}
	if remote.URL == "" || len(remote.Headers) != 1 {
		t.Fatalf("Normalize url dropped URL fields: %+v", remote)
	}
}

// TestParse covers a small config fixture, the empty document, and the error
// path.
func TestParse(t *testing.T) {
	const fixture = `[mcp.docs]
command = "docs-server"
args = ["--verbose"]
env = { TOKEN = "docs-secret" }

[mcp.remote]
url = "https://example.com/mcp"

[agents.coder]
provider = "mock"
mcp = ["docs"]

[agents.writer]
mcp = ["docs", "remote"]
`
	document, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(document.MCP) != 2 {
		t.Fatalf("MCP servers = %d, want 2", len(document.MCP))
	}
	docs := FromMap(document.MCP["docs"])
	if docs.Type != "command" || docs.Command != "docs-server" || len(docs.Args) != 1 {
		t.Fatalf("docs definition = %+v", docs)
	}
	if docs.Env["TOKEN"] != "docs-secret" {
		t.Fatalf("docs env = %v", docs.Env)
	}
	remote := FromMap(document.MCP["remote"])
	if remote.Type != "url" || remote.URL != "https://example.com/mcp" {
		t.Fatalf("remote definition = %+v", remote)
	}
	if got, want := Usage(document), map[string][]string{
		"docs":   {"coder", "writer"},
		"remote": {"writer"},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Usage = %v, want %v", got, want)
	}
	if got, want := AgentList(document, "coder"), []string{"docs"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentList(coder) = %v, want %v", got, want)
	}

	for _, raw := range [][]byte{nil, []byte("  \n")} {
		empty, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q): %v", raw, err)
		}
		if len(empty.MCP) != 0 || len(empty.Agents) != 0 {
			t.Fatalf("Parse(%q) = %+v, want an empty document", raw, empty)
		}
	}
	if _, err := Parse([]byte("[mcp.docs\n")); err == nil {
		t.Fatal("Parse accepted invalid TOML")
	}
}
