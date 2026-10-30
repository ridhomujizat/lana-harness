package webfiles

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// errRejected is the sentinel error the fake checkers return when a candidate
// fails their rule.
var errRejected = errors.New("rejected")

// acceptCandidate is a checker that accepts every candidate.
func acceptCandidate(context.Context, []byte) error { return nil }

// rejectCandidate returns a checker that returns errRejected when the candidate
// contains literal, and nil otherwise.
func rejectCandidate(literal string) func(context.Context, []byte) error {
	return func(_ context.Context, candidate []byte) error {
		if strings.Contains(string(candidate), literal) {
			return errRejected
		}
		return nil
	}
}

// validateTOML is a checker that returns errRejected when the candidate is not
// syntactically valid TOML, matching the parse-error path of the production
// config validation.
func validateTOML(_ context.Context, candidate []byte) error {
	var document map[string]any
	if err := toml.Unmarshal(candidate, &document); err != nil {
		return errRejected
	}
	return nil
}

// mustTomlHeader parses a header the same way RewriteTable does.
func mustTomlHeader(t *testing.T, header string) tableHeader {
	t.Helper()
	parsed, err := requestedHeader(header)
	if err != nil {
		t.Fatalf("header %q: %v", header, err)
	}
	return parsed
}

// TestTomlSectionRewriteTable covers AC-WEB-045 and AC-WEB-046: comments and
// unrelated tables stay byte for byte, dotted sub-tables belong to the parent,
// header detection skips multi-line strings and arrays, and a missing table is
// appended at the end.
func TestTomlSectionRewriteTable(t *testing.T) {
	const commentFixture = `# top comment
[agents.coder]
provider = "openai"
model = "old"

# provider comment
[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`
	const newCoder = `[agents.coder]
provider = "openai"
model = "new"
`
	const routesFixture = `[agents.coder]
model = "old"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 2 }
`
	const dottedFixture = `[agents.coder]
provider = "openai"
model = "old"

# rules for coder
[agents.coder.rules]
allow = ["bash(git *)"]

[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`
	const dottedTable = `[agents.coder]
provider = "openai"
model = "new"

# rules for coder
[agents.coder.rules]
allow = ["bash(git *)"]
`
	const multilineFixture = `[agents.coder]
provider = "openai"
model = "old"
prompt_file = """
[providers.fake]
kind = "anthropic"
"""
tools = [
  ["agents.evil"]
]

[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`
	const providersFixture = `# top
[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`
	const missingTable = `[agents.coder]
provider = "openai"
model = "new"
`

	cases := []struct {
		name        string
		initial     string
		header      string
		table       string
		want        string
		wantChanged []string
		reject      string
	}{
		{
			name:        "comments and other tables kept",
			initial:     commentFixture,
			header:      "[agents.coder]",
			table:       newCoder,
			want:        strings.Replace(commentFixture, "model = \"old\"", "model = \"new\"", 1),
			wantChanged: []string{"agents.coder.model"},
		},
		{
			name:    "table before routes",
			initial: routesFixture,
			header:  "[agents.coder]",
			table: `[agents.coder]
model = "new"
`,
			want: strings.Replace(routesFixture, "model = \"old\"", "model = \"new\"", 1),
		},
		{
			name:    "dotted sub-table in parent span",
			initial: dottedFixture,
			header:  "[agents.coder]",
			table:   dottedTable,
			want:    strings.Replace(dottedFixture, "model = \"old\"", "model = \"new\"", 1),
		},
		{
			name:    "multi-line string and array lines are not headers",
			initial: multilineFixture,
			header:  "[agents.coder]",
			table: `[agents.coder]
provider = "openai"
model = "new"
`,
			want: `[agents.coder]
provider = "openai"
model = "new"

[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`,
		},
		{
			name:        "missing table appended",
			initial:     providersFixture,
			header:      "[agents.coder]",
			table:       missingTable,
			want:        providersFixture + "\n" + missingTable,
			wantChanged: []string{"agents.coder.model", "agents.coder.provider"},
		},
		{
			name:    "header without brackets",
			initial: commentFixture,
			header:  "agents.coder",
			table:   newCoder,
			want:    strings.Replace(commentFixture, "model = \"old\"", "model = \"new\"", 1),
		},
		{
			name:    "invalid candidate",
			initial: commentFixture,
			header:  "[agents.coder]",
			table: `[agents.coder]
provider = "nope"
model = "new"
`,
			reject: `provider = "nope"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			check := acceptCandidate
			if tc.reject != "" {
				check = rejectCandidate(tc.reject)
			}
			candidate, changed, err := RewriteTable(context.Background(), check, []byte(tc.initial), tc.header, tc.table)
			if tc.reject != "" {
				if !errors.Is(err, errRejected) {
					t.Fatalf("error = %v, want the checker error errRejected", err)
				}
				if candidate != nil {
					t.Fatalf("candidate = %q, want nil after a rejected candidate", candidate)
				}
				if changed != nil {
					t.Fatalf("changed = %v, want nil after a rejected candidate", changed)
				}
				return
			}
			if err != nil {
				t.Fatalf("RewriteTable: %v", err)
			}
			if string(candidate) != tc.want {
				t.Fatalf("candidate:\n got %q\nwant %q", candidate, tc.want)
			}
			if tc.wantChanged != nil && !reflect.DeepEqual(changed, tc.wantChanged) {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

// TestTomlSectionRewriteRoute covers replacing one [[routes]] entry by chat_id
// and appending a missing entry at the end of the routes group.
func TestTomlSectionRewriteRoute(t *testing.T) {
	const routesFixture = `[agents.coder]
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 2 }
`
	const groupFixture = `[agents.coder]
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }

# web settings
[web]
enabled = true
addr = "127.0.0.1:18300"
`
	const noRoutesFixture = `[agents.coder]
model = "m"
`
	const newEntry = `[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 3 }
`

	cases := []struct {
		name        string
		initial     string
		chatID      int64
		entry       string
		want        string
		wantChanged []string
	}{
		{
			name:        "replace one entry",
			initial:     routesFixture,
			chatID:      2,
			entry:       newEntry,
			want:        strings.Replace(routesFixture, "chat_id = 2", "chat_id = 3", 1),
			wantChanged: []string{"routes.1.match.chat_id"},
		},
		{
			name:    "missing entry appended to group",
			initial: groupFixture,
			chatID:  9,
			entry: `[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 9 }
`,
			want: `[agents.coder]
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }
[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 9 }

# web settings
[web]
enabled = true
addr = "127.0.0.1:18300"
`,
		},
		{
			name:    "missing entry appended without routes",
			initial: noRoutesFixture,
			chatID:  5,
			entry: `[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 5 }
`,
			want: noRoutesFixture + "\n" + `[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 5 }
`,
		},
		{
			name: "other entries stay byte identical",
			initial: `[agents.coder]
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = -100 }
`,
			chatID: -100,
			entry: `[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = -200 }
`,
			want: `[agents.coder]
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = -200 }
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate, changed, err := RewriteRoute(context.Background(), acceptCandidate, []byte(tc.initial), tc.chatID, tc.entry)
			if err != nil {
				t.Fatalf("RewriteRoute: %v", err)
			}
			if string(candidate) != tc.want {
				t.Fatalf("candidate:\n got %q\nwant %q", candidate, tc.want)
			}
			if tc.wantChanged != nil && !reflect.DeepEqual(changed, tc.wantChanged) {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

// TestTomlSectionByteCheck covers AC-WEB-045: every byte before and after the
// rewritten span is unchanged.
func TestTomlSectionByteCheck(t *testing.T) {
	const original = `# before coder
[agents.coder]
model = "old"
provider = "openai"

# before provider
[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"

# trailing note
`
	span, ok := findTableSpan([]byte(original), mustTomlHeader(t, "[agents.coder]"))
	if !ok {
		t.Fatal("findTableSpan did not find [agents.coder]")
	}
	if got, want := original[:span.start], "# before coder\n"; got != want {
		t.Fatalf("prefix = %q, want %q", got, want)
	}
	if !strings.Contains(original[span.start:span.end], "[agents.coder]") {
		t.Fatalf("span = %q, want the coder table", original[span.start:span.end])
	}
	if strings.Contains(original[span.start:span.end], "[providers.openai]") {
		t.Fatalf("span swallowed the provider table: %q", original[span.start:span.end])
	}

	replacement := `[agents.coder]
model = "new"
provider = "openai"
`
	candidate, _, err := RewriteTable(context.Background(), acceptCandidate, []byte(original), "[agents.coder]", replacement)
	if err != nil {
		t.Fatalf("RewriteTable: %v", err)
	}
	if got, want := string(candidate[:span.start]), original[:span.start]; got != want {
		t.Fatalf("prefix changed:\n got %q\nwant %q", got, want)
	}
	suffixLen := len(original) - span.end
	gotSuffix := string(candidate[len(candidate)-suffixLen:])
	if gotSuffix != original[span.end:] {
		t.Fatalf("suffix changed:\n got %q\nwant %q", gotSuffix, original[span.end:])
	}
	if !reflect.DeepEqual(candidate[:span.start], []byte(original[:span.start])) {
		t.Fatal("prefix is not byte identical")
	}
}

// TestTomlSectionHeaderDetection covers lines that look like headers but are
// inside a multi-line string or a multi-line array.
func TestTomlSectionHeaderDetection(t *testing.T) {
	cases := []struct {
		name       string
		data       string
		wantNames  []string
		notInNames []string
	}{
		{
			name: "multi-line basic string",
			data: `[agents.coder]
prompt_file = """
[providers.fake]
kind = "anthropic"
"""
model = "m"
`,
			wantNames:  []string{"agents.coder"},
			notInNames: []string{"providers.fake"},
		},
		{
			name: "multi-line literal string",
			data: `[agents.coder]
prompt_file = '''
[web]
addr = "127.0.0.1:1"
'''
model = "m"
`,
			wantNames:  []string{"agents.coder"},
			notInNames: []string{"web"},
		},
		{
			name: "multi-line array",
			data: `[agents.coder]
tools = [
  ["agents.evil"]
]
model = "m"
`,
			wantNames:  []string{"agents.coder"},
			notInNames: []string{`"agents.evil"`},
		},
		{
			name: "header comment and array of tables",
			data: `[agents.coder] # coder
model = "m"

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }
`,
			wantNames:  []string{"agents.coder", "routes"},
			notInNames: []string{"agents.coder]", "routes]"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := splitSourceLines([]byte(tc.data))
			headers := scanHeaders([]byte(tc.data), lines)
			names := make([]string, 0, len(headers))
			for _, header := range headers {
				names = append(names, header.header.name)
			}
			if !reflect.DeepEqual(names, tc.wantNames) {
				t.Fatalf("headers = %v, want %v", names, tc.wantNames)
			}
			for _, forbidden := range tc.notInNames {
				for _, name := range names {
					if name == forbidden {
						t.Fatalf("detected header %q inside a multi-line value", forbidden)
					}
				}
			}
		})
	}
}

// TestTomlSectionSubTableSpanCoversParent checks the span rule for dotted
// sub-tables: [agents.coder.rules] belongs to the [agents.coder] span.
func TestTomlSectionSubTableSpanCoversParent(t *testing.T) {
	const original = `[agents.coder]
model = "m"

[agents.coder.rules]
allow = ["bash(git *)"]

[[routes]]
channel = "telegram"
agent = "coder"
match = { chat_id = 1 }
`
	span, ok := findTableSpan([]byte(original), mustTomlHeader(t, "[agents.coder]"))
	if !ok {
		t.Fatal("findTableSpan did not find [agents.coder]")
	}
	got := original[span.start:span.end]
	for _, want := range []string{"[agents.coder]", "[agents.coder.rules]", "allow ="} {
		if !strings.Contains(got, want) {
			t.Fatalf("span %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "[[routes]]") {
		t.Fatalf("span swallowed the routes block: %q", got)
	}
	if !strings.HasSuffix(got, "allow = [\"bash(git *)\"]\n") {
		t.Fatalf("span = %q, want it to end at the sub-table value", got)
	}
}

// TestTomlSectionStaleVersion covers the stale case of AC-WEB-046 and the
// version helper the save handlers use.
func TestTomlSectionStaleVersion(t *testing.T) {
	original := []byte("[agents.coder]\nmodel = \"old\"\n")
	version := ConfigVersion(original)

	if err := VerifyVersion(original, version); err != nil {
		t.Fatalf("VerifyVersion on the read bytes: %v", err)
	}
	if version == ConfigVersion([]byte("[agents.coder]\nmodel = \"new\"\n")) {
		t.Fatal("changed bytes produced the same version")
	}
	if err := VerifyVersion([]byte("[agents.coder]\nmodel = \"new\"\n"), version); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("VerifyVersion on changed bytes = %v, want ErrStaleVersion", err)
	}
	if err := VerifyVersion(nil, ""); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("VerifyVersion with no expected version = %v, want ErrStaleVersion", err)
	}
}

// TestTomlSectionMissingTableAppendsAtEOF asserts the separator rule: one blank
// line between the old end of file and the new table.
func TestTomlSectionMissingTableAppendsAtEOF(t *testing.T) {
	cases := []struct {
		name    string
		initial string
		want    string
	}{
		{
			name:    "empty file",
			initial: "",
			want:    "[providers.mock]\nkind = \"openai_compat\"\n",
		},
		{
			name:    "file ends with one newline",
			initial: "[web]\nenabled = true\n",
			want:    "[web]\nenabled = true\n\n[providers.mock]\nkind = \"openai_compat\"\n",
		},
		{
			name:    "file ends with two newlines",
			initial: "[web]\nenabled = true\n\n",
			want:    "[web]\nenabled = true\n\n[providers.mock]\nkind = \"openai_compat\"\n",
		},
		{
			name:    "file has no final newline",
			initial: "[web]\nenabled = true",
			want:    "[web]\nenabled = true\n\n[providers.mock]\nkind = \"openai_compat\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidate, _, err := RewriteTable(context.Background(), acceptCandidate, []byte(tc.initial),
				"[providers.mock]", "[providers.mock]\nkind = \"openai_compat\"")
			if err != nil {
				t.Fatalf("RewriteTable: %v", err)
			}
			if string(candidate) != tc.want {
				t.Fatalf("candidate:\n got %q\nwant %q", candidate, tc.want)
			}
		})
	}
}

// TestTomlSectionInvalidTOML covers the parse-error path of an invalid
// candidate: the checker rejects it and the writer returns no candidate to
// write.
func TestTomlSectionInvalidTOML(t *testing.T) {
	const original = `[providers.openai]
kind = "openai_compat"
base_url = "https://example.test/v1"
`
	table := `[providers.openai]
kind = "openai_compat
base_url = "https://example.test/v1"
`
	candidate, changed, err := RewriteTable(context.Background(), validateTOML, []byte(original), "[providers.openai]", table)
	if !errors.Is(err, errRejected) {
		t.Fatalf("error = %v, want the checker error errRejected", err)
	}
	if candidate != nil {
		t.Fatalf("candidate = %q, want nil", candidate)
	}
	if changed != nil {
		t.Fatalf("changed = %v, want nil", changed)
	}
}

// TestTomlSectionNilChecker asserts that a nil checker fails closed instead of
// panicking.
func TestTomlSectionNilChecker(t *testing.T) {
	data := []byte("[agents.coder]\nmodel = \"m\"\n")
	candidate, changed, err := RewriteTable(context.Background(), nil, data, "[agents.coder]", string(data))
	if err == nil {
		t.Fatal("RewriteTable with a nil checker succeeded")
	}
	if candidate != nil {
		t.Fatalf("candidate = %q, want nil", candidate)
	}
	if changed != nil {
		t.Fatalf("changed = %v, want nil", changed)
	}
}

// TestTomlSectionDeleteTable covers a DELETE save: an empty replacement removes
// the table and leaves every other byte alone.
func TestTomlSectionDeleteTable(t *testing.T) {
	const original = `# providers
[providers.openai]
kind = "openai_compat"
base_url = "https://api.openai.test/v1"

# keep this one
[providers.anthropic]
kind = "anthropic"
base_url = "https://api.anthropic.test"
`
	want := `# providers

# keep this one
[providers.anthropic]
kind = "anthropic"
base_url = "https://api.anthropic.test"
`
	candidate, _, err := RewriteTable(context.Background(), acceptCandidate, []byte(original), "[providers.openai]", "")
	if err != nil {
		t.Fatalf("RewriteTable: %v", err)
	}
	if string(candidate) != want {
		t.Fatalf("candidate:\n got %q\nwant %q", candidate, want)
	}
}
