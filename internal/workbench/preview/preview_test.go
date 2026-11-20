package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	home := "/home/u"
	cases := []struct {
		name      string
		link, cwd string
		want      string
		line, col int
		ok        bool
	}{
		{"absolute", "/etc/hosts", "/tmp", "/etc/hosts", 0, 0, true},
		{"home", "~/proj/main.go", "", "/home/u/proj/main.go", 0, 0, true},
		{"tilde alone", "~", "", "/home/u", 0, 0, true},
		{"dot", "./a.go", "/srv/app", "/srv/app/a.go", 0, 0, true},
		{"dotdot", "../a.go", "/srv/app/sub", "/srv/app/a.go", 0, 0, true},
		{"relative", "a.go", "/srv/app", "/srv/app/a.go", 0, 0, true},
		{"relative no cwd", "a.go", "", "a.go", 0, 0, true},
		{"relative with line", "a.go:10", "/srv", "/srv/a.go", 10, 0, true},
		{"file url escape", "file:///tmp/a%20b.go", "", "/tmp/a b.go", 0, 0, true},
		{"file url host", "file://localhost/tmp/a.go", "", "/tmp/a.go", 0, 0, true},
		{"line", "/a.go:12", "", "/a.go", 12, 0, true},
		{"line col", "/a.go:12:5", "", "/a.go", 12, 5, true},
		{"colon kept", "/a:b.go", "", "/a:b.go", 0, 0, true},
		{"http", "http://x/y", "", "", 0, 0, false},
		{"https", "https://x/y", "", "", 0, 0, false},
		{"empty", "", "", "", 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, line, col, ok := ParseLink(tc.link, tc.cwd, home)
			if ok != tc.ok || path != tc.want || line != tc.line || col != tc.col {
				t.Fatalf("ParseLink(%q, %q) = (%q,%d,%d,%v), want (%q,%d,%d,%v)",
					tc.link, tc.cwd, path, line, col, ok, tc.want, tc.line, tc.col, tc.ok)
			}
		})
	}
}

func TestRelativePath(t *testing.T) {
	cases := []struct {
		link, want string
		ok         bool
	}{
		{"a.go", "a.go", true},
		{"./a.go", "./a.go", true},
		{"a.go:12", "a.go", true},
		{"/abs/a.go", "", false},
		{"~/a.go", "", false},
		{"~", "", false},
		{"file:///tmp/a.go", "", false},
		{"http://x/y", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := RelativePath(tc.link)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("RelativePath(%q) = (%q,%v), want (%q,%v)", tc.link, got, ok, tc.want, tc.ok)
		}
	}
}

func TestIsLocalLink(t *testing.T) {
	cases := []struct {
		link string
		want bool
	}{
		{"/abs/a.go", true},
		{"~/a.go", true},
		{"a.go", true},
		{"file:///tmp/a.go", true},
		{"http://x/y", false},
		{"https://x/y", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsLocalLink(tc.link); got != tc.want {
			t.Fatalf("IsLocalLink(%q) = %v, want %v", tc.link, got, tc.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	text := write("a.go", "package main\n")
	html := write("a.html", "<p>x</p>")
	markdown := write("a.md", "# x\n")
	image := write("a.png", "\x89PNG")
	upperImage := write("a.SVG", "<svg/>")
	pdf := write("a.pdf", "%PDF")
	known := write("Makefile", "all:\n")
	unknownText := write("a.weird", "hello world")
	unknownBinary := write("b.weird", "a\x00b")
	missing := filepath.Join(dir, "gone.zzz")

	cases := []struct {
		name string
		path string
		size int64
		want Kind
	}{
		{"text ext", text, 13, KindText},
		{"html", html, 8, KindHTML},
		{"markdown", markdown, 4, KindMarkdown},
		{"image", image, 4, KindImage},
		{"image upper", upperImage, 6, KindImage},
		{"pdf", pdf, 4, KindPDF},
		{"text base name", known, 5, KindText},
		{"unknown sniff text", unknownText, 11, KindText},
		{"unknown sniff binary", unknownBinary, 3, KindBinary},
		{"unreadable", missing, 10, KindBinary},
		{"large text", text, MaxSize + 1, KindBinary},
		{"large image stays", image, MaxSize + 1, KindImage},
		{"large pdf stays", pdf, MaxSize + 1, KindPDF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := KindOf(tc.path, tc.size); got != tc.want {
				t.Fatalf("KindOf(%q, %d) = %q, want %q", tc.path, tc.size, got, tc.want)
			}
		})
	}
}

func TestEscapeName(t *testing.T) {
	if got, want := EscapeName("a b/c.go"), "a%20b/c.go"; got != want {
		t.Fatalf("EscapeName = %q, want %q", got, want)
	}
}

func TestPolicy(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	tmp := filepath.Join(root, "tmp")
	for _, dir := range []string{filepath.Join(home, ".ssh"), filepath.Join(home, "proj"), tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	policy := NewPolicy(home, []string{tmp})

	allowed := filepath.Join(home, "proj", "main.go")
	if !policy.Allowed(allowed) {
		t.Fatalf("Allowed(%q) = false", allowed)
	}
	if !policy.Allowed(filepath.Join(tmp, "x.txt")) {
		t.Fatal("temp folder should be allowed")
	}
	if policy.Allowed(filepath.Join(root, "elsewhere", "x")) {
		t.Fatal("a path outside home and temp should not be allowed")
	}

	for _, refused := range []string{
		filepath.Join(home, ".ssh", "id_rsa"),
		filepath.Join(home, "secrets.env"),
		filepath.Join(home, "proj", ".env"),
		filepath.Join(home, "proj", ".env.local"),
	} {
		if !policy.Refused(refused) {
			t.Fatalf("Refused(%q) = false", refused)
		}
	}
	if policy.Refused(allowed) {
		t.Fatalf("Refused(%q) = true", allowed)
	}

	// A symlink inside an allowed folder pointing at a refused file is refused
	// because the check runs on the resolved path (REQ-WP-003).
	secret := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.WriteFile(secret, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "link")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	if !policy.Refused(Resolve(link)) {
		t.Fatalf("Refused(Resolve(%q)) = false", link)
	}
}

func TestRenderMarkdownPage(t *testing.T) {
	src := "# Title\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- [x] done\n"
	page, err := RenderMarkdownPage([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	got := string(page)
	for _, want := range []string{"<h1", "<table>", "checkbox", "#1D1C1B", "#ECEAE5", "#262523", "Geist"} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown page missing %q:\n%s", want, got)
		}
	}

	raw, err := RenderMarkdownPage([]byte("<script>alert(1)</script>\n"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "<script") {
		t.Fatalf("raw HTML not dropped:\n%s", raw)
	}
	if !strings.Contains(string(raw), "raw HTML omitted") {
		t.Fatalf("raw HTML marker missing:\n%s", raw)
	}
}

func TestRenderTextPage(t *testing.T) {
	page := string(RenderTextPage([]byte("a < b\nsecond\n"), 2))
	for _, want := range []string{
		"<pre", `id="L1"`, `id="L2"`, `id="L3"`, "row hl", "a &lt; b", ":target", "monospace",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("text page missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "a < b") {
		t.Fatalf("text page did not escape the source:\n%s", page)
	}
}

func TestRenderImagePage(t *testing.T) {
	page := string(RenderImagePage("/v1/workbench/preview/tok/pic.png"))
	for _, want := range []string{
		`<img src="/v1/workbench/preview/tok/pic.png"`, "display: flex",
		"justify-content: center", "max-width: 100%", "max-height: 100vh", "#1D1C1B",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("image page missing %q:\n%s", want, page)
		}
	}
}
