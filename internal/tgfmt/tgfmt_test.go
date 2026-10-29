package tgfmt

import (
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
	}
	for _, c := range cases {
		if got := ToHTML(c.in); got != c.want {
			t.Errorf("ToHTML(%q) = %q, want %q", c.in, got, c.want)
		}
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
