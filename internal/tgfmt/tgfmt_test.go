package tgfmt

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToHTML(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**ok** and `x`", "<b>ok</b> and <code>x</code>"},
		{"__b__ *i* _j_ snake_case_name", "<b>b</b> <i>i</i> <i>j</i> snake_case_name"},
		{"a < b && c > d", "a &lt; b &amp;&amp; c &gt; d"},
		{"see [docs](https://go.dev/doc) now", `see <a href="https://go.dev/doc">docs</a> now`},
		{"[bad](javascript:alert(1))", "[bad](javascript:alert(1))"},
		{"## Title", "<b>Title</b>"},
		{"- one\n* two", "• one\n• two"},
		{"`<b>`", "<code>&lt;b&gt;</code>"},
		{"```go\nif a < b {\n}\n```", "<pre><code class=\"language-go\">if a &lt; b {\n}</code></pre>"},
		{"```\nopen", "<pre><code>open</code></pre>"},
		{"2 * 3 * 4", "2 * 3 * 4"},
		{"a | b", "a | b"},
		{"a | b\nnot a table", "a | b\nnot a table"},
		{"| id | ok |\n|---|---|\n| 1 | yes |", "<pre>id │ ok\n───┼────\n1  │ yes</pre>"},
		{"| a | b | c |\n|---|---|---|\n| 1 |", "<pre>a │ b │ c\n──┼───┼──\n1 │   │</pre>"},
		{"| a | b | c |\n|---|---|---|\n| 1 | 2 | 3 | 4 |", "<pre>a │ b │ c\n──┼───┼──\n1 │ 2 │ 3</pre>"},
		{"| a | b |\n|---|---|\n| **bold** | `code` |", "<pre>a    │ b\n─────┼─────\nbold │ code</pre>"},
		{
			"intro\n\n| id | ok |\n|---|---|\n| 1 | yes |\n\noutro",
			"intro\n\n<pre>id │ ok\n───┼────\n1  │ yes</pre>\n\noutro",
		},
	}
	for _, c := range cases {
		if got := ToHTML(c.in); got != c.want {
			t.Errorf("ToHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToHTMLWideTable(t *testing.T) {
	const in = `| username | email | fullname | is_active |
|---|---|---|---|
| agents | agentdemo01@yopmail.com | Agent Bro | 1 |
| idamzero30 | iam.zer3012@gmail.com | idamzero | 0 |
| spv@infomedia.co.id | spvyola@gmail.com | Supervisor | 1 |`
	want := strings.Join([]string{
		"<b>agents</b>",
		"email: agentdemo01@yopmail.com",
		"fullname: Agent Bro",
		"is_active: 1",
		"",
		"<b>idamzero30</b>",
		"email: iam.zer3012@gmail.com",
		"fullname: idamzero",
		"is_active: 0",
		"",
		"<b>spv@infomedia.co.id</b>",
		"email: spvyola@gmail.com",
		"fullname: Supervisor",
		"is_active: 1",
	}, "\n")
	// The fixture is 70 runes wide, so the wide per-row form is required.
	if total := tableWidth([]int{19, 23, 10, 9}); total <= 40 {
		t.Fatalf("fixture width = %d, want > 40", total)
	}
	got := ToHTML(in)
	if got != want {
		t.Errorf("ToHTML(wide table) = %q, want %q", got, want)
	}
	if strings.Contains(got, "|") || strings.Contains(got, "<pre>") {
		t.Errorf("wide table still contains table syntax or a pre block: %q", got)
	}
}

func TestToHTMLTableInFence(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"```\n| id | ok |\n|---|---|\n| 1 | yes |\n```",
			"<pre><code>| id | ok |\n|---|---|\n| 1 | yes |</code></pre>",
		},
		{
			"```md\n| id | ok |\n|---|---|\n| 1 | yes |\n```",
			"<pre><code class=\"language-md\">| id | ok |\n|---|---|\n| 1 | yes |</code></pre>",
		},
	}
	for _, c := range cases {
		if got := ToHTML(c.in); got != c.want {
			t.Errorf("ToHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToHTMLTableEscaping(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"| x | y |\n|---|---|\n| a<b | c&d |",
			"<pre>x   │ y\n────┼────\na&lt;b │ c&amp;d</pre>",
		},
		{
			"| name | description |\n|---|---|\n| a<b | x&y this description is long enough to be wide |",
			"<b>a&lt;b</b>\ndescription: x&amp;y this description is long enough to be wide",
		},
	}
	for _, c := range cases {
		if got := ToHTML(c.in); got != c.want {
			t.Errorf("ToHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitReopensNarrowTable(t *testing.T) {
	const limit = 256
	var rows strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&rows, "| row%d | ok |\n", i)
	}
	html := ToHTML("| id | ok |\n|---|---|\n" + rows.String())
	if !strings.HasPrefix(html, "<pre>") {
		t.Fatalf("table not narrow: %q", html)
	}
	parts := Split(html, limit)
	if len(parts) < 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	strip := func(s string) []string {
		s = strings.NewReplacer("<pre>", " ", "</pre>", " ").Replace(s)
		return strings.Fields(s)
	}
	want := strip(html)
	for i, part := range parts {
		if n := utf8.RuneCountInString(part); n > limit {
			t.Fatalf("part %d too long: %d", i, n)
		}
		if strings.Count(part, "<pre>") != strings.Count(part, "</pre>") {
			t.Fatalf("part %d has unbalanced pre: %q", i, part)
		}
	}
	if got := strip(strings.Join(parts, "\n")); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatal("content lost")
	}
}

func TestPlain(t *testing.T) {
	if got := Plain("<b>a</b> &lt;x&gt; &amp;"); got != "a <x> &" {
		t.Fatalf("plain = %q", got)
	}
}

func TestSplitParagraphs(t *testing.T) {
	para := strings.Repeat("x", 3000)
	text := strings.Join([]string{para, para, para}, "\n\n")
	parts := Split(text, Limit)
	if len(parts) != 3 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, part := range parts {
		if utf8.RuneCountInString(part) > Limit {
			t.Fatalf("part too long: %d", utf8.RuneCountInString(part))
		}
	}
	if strings.Join(parts, "") != strings.Repeat("x", 9000) {
		t.Fatal("content lost")
	}
}

func TestSplitReopensCodeBlock(t *testing.T) {
	code := strings.Repeat("line of code\n", 600)
	html := ToHTML("intro\n\n```go\n" + code + "```")
	parts := Split(html, Limit)
	if len(parts) < 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for i, part := range parts {
		if utf8.RuneCountInString(part) > Limit {
			t.Fatalf("part %d too long", i)
		}
		if strings.Count(part, "<pre>") != strings.Count(part, "</pre>") {
			t.Fatalf("part %d has unbalanced pre:\n%s", i, part[:80])
		}
	}
	if !strings.HasPrefix(parts[len(parts)-1], `<pre><code class="language-go">`) {
		t.Fatalf("last part not reopened: %q", parts[len(parts)-1][:60])
	}
}

func TestSplitHardCutAvoidsEntity(t *testing.T) {
	text := strings.Repeat("a", 4085) + "&amp;" + strings.Repeat("b", 100)
	parts := Split(text, Limit)
	if strings.Contains(parts[0], "&am") && !strings.Contains(parts[0], "&amp;") {
		t.Fatal("entity cut")
	}
	if strings.Join(parts, "") != text {
		t.Fatal("content lost")
	}
}
