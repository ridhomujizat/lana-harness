package webfiles

import (
	"errors"
	"strings"
	"testing"
)

// TestSetTableKey covers replacing and appending one key inside a table while
// every byte outside the value stays identical.
func TestSetTableKey(t *testing.T) {
	cases := []struct {
		name    string
		initial string
		header  string
		key     string
		value   string
		want    string
	}{
		{
			name: "replace single-line value and keep comments and tables",
			initial: `# top comment
[agents.lana]
provider = "openai"
mcp = ["docs"]

# before web
[web]
enabled = true
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["docs", "fs"]`,
			want: `# top comment
[agents.lana]
provider = "openai"
mcp = ["docs", "fs"]

# before web
[web]
enabled = true
`,
		},
		{
			name: "replace multi-line array and keep the inline comment",
			initial: `[agents.lana]
mcp = [
  "docs",
  "fs",
] # keep this
model = "m"
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["docs"]`,
			want: `[agents.lana]
mcp = ["docs"] # keep this
model = "m"
`,
		},
		{
			name: "append missing key at the end of the table body",
			initial: `[agents.lana]
provider = "openai"

# web settings
[web]
enabled = true
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["docs"]`,
			want: `[agents.lana]
provider = "openai"
mcp = ["docs"]

# web settings
[web]
enabled = true
`,
		},
		{
			name:    "append at EOF without a trailing newline",
			initial: "[agents.lana]\nprovider = \"openai\"",
			header:  "[agents.lana]",
			key:     "mcp",
			value:   `["docs"]`,
			want:    "[agents.lana]\nprovider = \"openai\"\nmcp = [\"docs\"]\n",
		},
		{
			name:    "replace on a file without a trailing newline",
			initial: "[agents.lana]\nmcp = [\"docs\"]",
			header:  "[agents.lana]",
			key:     "mcp",
			value:   `["fs"]`,
			want:    "[agents.lana]\nmcp = [\"fs\"]",
		},
		{
			name:    "CRLF line endings stay identical",
			initial: "[agents.lana]\r\nprovider = \"openai\"\r\nmcp = [\"docs\"]\r\n\r\n[web]\r\nenabled = true\r\n",
			header:  "[agents.lana]",
			key:     "mcp",
			value:   `["fs"]`,
			want:    "[agents.lana]\r\nprovider = \"openai\"\r\nmcp = [\"fs\"]\r\n\r\n[web]\r\nenabled = true\r\n",
		},
		{
			name: "basic string with bracket and hash",
			initial: `[agents.lana]
command = "echo ] # done" # real comment
model = "m"
`,
			header: "[agents.lana]",
			key:    "command",
			value:  `'printf ] #'`,
			want: `[agents.lana]
command = 'printf ] #' # real comment
model = "m"
`,
		},
		{
			name: "literal string with bracket and hash",
			initial: `[agents.lana]
command = 'echo ] # done' # real comment
model = "m"
`,
			header: "[agents.lana]",
			key:    "command",
			value:  `"printf ] #"`,
			want: `[agents.lana]
command = "printf ] #" # real comment
model = "m"
`,
		},
		{
			name: "multi-line array with brackets and hashes in strings",
			initial: `[agents.lana]
args = [
  "a]",
  'b#',
  # comment ] ignored
  "c#d]",
]
model = "m"
`,
			header: "[agents.lana]",
			key:    "args",
			value:  `['x']`,
			want: `[agents.lana]
args = ['x']
model = "m"
`,
		},
		{
			name: "multi-line string with brackets and hashes",
			initial: `[agents.lana]
prompt = """
close ] and # hash
"""
model = "m"
`,
			header: "[agents.lana]",
			key:    "prompt",
			value:  `"hello"`,
			want: `[agents.lana]
prompt = "hello"
model = "m"
`,
		},
		{
			name: "key-like line inside a multi-line string is not a key",
			initial: `[agents.lana]
prompt = """
mcp = ["fake"]
"""
mcp = ["real"]
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["fs"]`,
			want: `[agents.lana]
prompt = """
mcp = ["fake"]
"""
mcp = ["fs"]
`,
		},
		{
			name: "sub-table key is not replaced by the parent",
			initial: `[agents.lana]
mcp = ["docs"]

[agents.lana.rules]
mcp = ["rules"]
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["fs"]`,
			want: `[agents.lana]
mcp = ["fs"]

[agents.lana.rules]
mcp = ["rules"]
`,
		},
		{
			name: "key only in a sub-table is appended to the parent",
			initial: `[agents.lana]
provider = "openai"

[agents.lana.rules]
mcp = ["rules"]
`,
			header: "[agents.lana]",
			key:    "mcp",
			value:  `["fs"]`,
			want: `[agents.lana]
provider = "openai"
mcp = ["fs"]

[agents.lana.rules]
mcp = ["rules"]
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SetTableKey([]byte(tc.initial), tc.header, tc.key, tc.value)
			if err != nil {
				t.Fatalf("SetTableKey: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("candidate:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestSetTableKeyMissingTable asserts that a missing table is an error and not
// an append, unlike RewriteTable.
func TestSetTableKeyMissingTable(t *testing.T) {
	candidate, err := SetTableKey([]byte("[web]\nenabled = true\n"), "[agents.lana]", "mcp", `["docs"]`)
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("error = %v, want ErrTableNotFound", err)
	}
	if candidate != nil {
		t.Fatalf("candidate = %q, want nil", candidate)
	}
}

// TestSetTableKeyPreservesBytes checks the byte-preservation rule directly:
// only the value range changes.
func TestSetTableKeyPreservesBytes(t *testing.T) {
	const initial = `# before
[agents.lana]
provider = "openai"
mcp = ["docs"] # servers

# after
[web]
enabled = true
`
	got, err := SetTableKey([]byte(initial), "[agents.lana]", "mcp", `["docs", "fs"]`)
	if err != nil {
		t.Fatalf("SetTableKey: %v", err)
	}
	const want = `# before
[agents.lana]
provider = "openai"
mcp = ["docs", "fs"] # servers

# after
[web]
enabled = true
`
	if string(got) != want {
		t.Fatalf("candidate:\n got %q\nwant %q", got, want)
	}
	prefix := strings.SplitAfter(initial, "provider = \"openai\"\n")[0]
	if !strings.HasPrefix(string(got), prefix) {
		t.Fatalf("prefix changed:\n got %q\nwant prefix %q", got, prefix)
	}
}
