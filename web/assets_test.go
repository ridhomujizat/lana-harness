package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func acceptedAssetContentTypes(name string) []string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html":
		return []string{"text/html"}
	case ".css":
		return []string{"text/css"}
	case ".js":
		// Go's mime table and the platform may pick either registered type.
		return []string{"text/javascript", "application/javascript"}
	case ".svg":
		return []string{"image/svg+xml"}
	case ".txt":
		return []string{"text/plain"}
	default:
		return nil
	}
}

func TestEmbeddedAssetsLoad(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var files []string
	err = fs.WalkDir(assetsFS, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		files = append(files, name)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("no embedded assets found")
	}
	hasTokens := false
	iconCount := 0
	for _, name := range files {
		if name == "assets/tokens.css" {
			hasTokens = true
		}
		if strings.HasPrefix(name, "assets/icons/") && strings.HasSuffix(name, ".svg") {
			iconCount++
		}
	}
	if !hasTokens {
		t.Fatal("assets/tokens.css is not embedded")
	}
	if iconCount != 27 {
		t.Fatalf("embedded icons = %d, want the 27 files of design/export/icons", iconCount)
	}

	for _, name := range files {
		wantTypes := acceptedAssetContentTypes(name)
		if len(wantTypes) == 0 {
			t.Fatalf("%s has no expected Content-Type mapping", name)
		}
		resp, err := http.Get(ts.URL + "/" + name)
		if err != nil {
			t.Fatalf("GET /%s: %v", name, err)
		}
		body := readBody(t, resp)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /%s status = %d", name, resp.StatusCode)
		}
		if body == "" {
			t.Fatalf("GET /%s returned an empty body", name)
		}
		got := resp.Header.Get("Content-Type")
		matched := false
		for _, want := range wantTypes {
			if strings.HasPrefix(strings.ToLower(got), want) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("GET /%s Content-Type = %q, want one of %v", name, got, wantTypes)
		}
	}

	indexAsset, err := fs.ReadFile(assetsFS, "assets/index.html")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}
	if body != string(indexAsset) {
		t.Fatal("GET / does not serve assets/index.html")
	}
	assertCSPAllowsShell(t, resp.Header.Get("Content-Security-Policy"))
}

func cspDirective(csp, name string) (string, bool) {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Fields(part)
		if len(fields) > 0 && fields[0] == name {
			return part, true
		}
	}
	return "", false
}

func assertCSPAllowsShell(t *testing.T, csp string) {
	t.Helper()
	if csp == "" {
		t.Fatal("the server sends no Content-Security-Policy on the shell")
	}
	defaultSrc, ok := cspDirective(csp, "default-src")
	if !ok || !strings.Contains(defaultSrc, "'self'") {
		t.Fatalf("Content-Security-Policy default-src does not permit same-origin shell assets: %q", csp)
	}
	frameAncestors, ok := cspDirective(csp, "frame-ancestors")
	if !ok || !strings.Contains(frameAncestors, "'none'") {
		t.Fatalf("Content-Security-Policy frame-ancestors = %q, want 'none'", frameAncestors)
	}
	// The shell is plain same-origin files: a script, stylesheets, SVG images,
	// and fetch/SSE calls. Any narrower directive would break one of them.
	for _, name := range []string{"script-src", "style-src", "img-src", "connect-src", "font-src"} {
		directive, present := cspDirective(csp, name)
		if present && !strings.Contains(directive, "'self'") {
			t.Fatalf("Content-Security-Policy %s does not permit same-origin shell resources: %q", name, csp)
		}
	}
}

func attrValue(attrs, name string) string {
	re := regexp.MustCompile(`(?i)(?:^|\s)` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]*)"`)
	match := re.FindStringSubmatch(attrs)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func TestIndexHTMLReferencesOnlyAssets(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d", resp.StatusCode)
	}

	scriptRE := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	scripts := scriptRE.FindAllStringSubmatch(body, -1)
	if len(scripts) != 1 {
		t.Fatalf("index has %d scripts, want 1", len(scripts))
	}
	for _, script := range scripts {
		attrs := script[1]
		src := attrValue(attrs, "src")
		if src == "" {
			t.Fatal("index script has no src attribute")
		}
		if !strings.HasPrefix(src, "/assets/") {
			t.Fatalf("index script src = %q, want an /assets/ path", src)
		}
		if strings.TrimSpace(script[2]) != "" {
			t.Fatal("index script has an inline body")
		}
		if strings.Contains(strings.ToLower(attrs), "style=") {
			t.Fatal("index script has an inline style attribute")
		}
	}

	linkRE := regexp.MustCompile(`(?is)<link\b([^>]*)>`)
	var hrefs []string
	for _, link := range linkRE.FindAllStringSubmatch(body, -1) {
		attrs := link[1]
		href := attrValue(attrs, "href")
		if href == "" {
			t.Fatalf("index link has no href: %q", attrs)
		}
		if !strings.HasPrefix(href, "/assets/") {
			t.Fatalf("index stylesheet href = %q, want an /assets/ path", href)
		}
		if strings.Contains(strings.ToLower(attrs), "style=") {
			t.Fatal("index link has an inline style attribute")
		}
		hrefs = append(hrefs, href)
	}
	if len(hrefs) == 0 || hrefs[0] != "/assets/tokens.css" {
		t.Fatalf("index stylesheets = %v, want /assets/tokens.css first", hrefs)
	}
	foundAppCSS := false
	for _, href := range hrefs {
		if href == "/assets/app.css" {
			foundAppCSS = true
			break
		}
	}
	if !foundAppCSS {
		t.Fatalf("index stylesheets = %v, want /assets/app.css", hrefs)
	}

	if strings.Contains(strings.ToLower(body), `style="`) {
		t.Fatal("index contains an inline style attribute")
	}
	if got := strings.Count(strings.ToLower(body), "aria-live"); got != 1 {
		t.Fatalf("index has %d aria-live regions, want exactly 1", got)
	}
}

func isShellAsset(name string) bool {
	if strings.HasPrefix(name, "assets/vendor/") || strings.HasPrefix(name, "assets/icons/") {
		return false
	}
	if name == "assets/index.html" {
		return true
	}
	return strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".css")
}

func TestOwnAssetsHaveNoExternalURLs(t *testing.T) {
	externalURL := regexp.MustCompile(`(?i)https?://`)
	protocolRelative := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*["']//|["']//[a-z0-9]`)
	err := fs.WalkDir(assetsFS, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !isShellAsset(name) {
			return nil
		}
		data, err := fs.ReadFile(assetsFS, name)
		if err != nil {
			return err
		}
		text := string(data)
		if loc := externalURL.FindString(text); loc != "" {
			t.Fatalf("%s contains an external URL: %q", name, loc)
		}
		if loc := protocolRelative.FindString(text); loc != "" {
			t.Fatalf("%s contains a protocol-relative URL: %q", name, loc)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestShellRoutesMatchRequirement(t *testing.T) {
	data, err := fs.ReadFile(assetsFS, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	routes := []string{
		"#/home",
		"#/sessions",
		"#/sessions/new",
		"#/sessions/{id}",
		"#/agents",
		"#/agents/{name}/{tab}",
		"#/skills",
		"#/skills/{scope}/{name}",
		"#/skills/new",
		"#/mcp",
		"#/mcp/{name}",
		"#/channels",
		"#/channels/telegram",
		"#/settings/{tab}",
		"#/setup/{step}",
	}
	for _, route := range routes {
		if !strings.Contains(source, `"`+route+`"`) {
			t.Fatalf("app.js route table is missing %s", route)
		}
	}
	segments := []string{
		"general", "persona", "tools", "skills", "mcp", "rules", "workspace",
		"providers", "secrets", "config", "audit",
		"unlock", "provider", "connect", "agent", "ready",
	}
	for _, segment := range segments {
		if !strings.Contains(source, `"`+segment+`"`) {
			t.Fatalf("app.js route table is missing the %q segment", segment)
		}
	}
	if !strings.Contains(source, "not-found") {
		t.Fatal("app.js has no not-found route state")
	}
}

func TestHomeViewIsTheEmptyState(t *testing.T) {
	data, err := fs.ReadFile(assetsFS, "assets/views/home.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, want := range []string{"Home is coming soon", "Go to sessions", "#/sessions"} {
		if !strings.Contains(source, want) {
			t.Fatalf("views/home.js does not contain %q", want)
		}
	}
}

func TestShellPersistsThemeAndSidebarState(t *testing.T) {
	app, err := fs.ReadFile(assetsFS, "assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	theme, err := fs.ReadFile(assetsFS, "assets/theme.js")
	if err != nil {
		t.Fatal(err)
	}
	appSource := string(app)
	themeSource := string(theme)
	for _, want := range []string{"localStorage", "lana.sidebar", "readCollapsed", "saveCollapsed"} {
		if !strings.Contains(appSource, want) {
			t.Fatalf("app.js does not persist the sidebar state with %q", want)
		}
	}
	for _, want := range []string{"localStorage", "lana.theme", "prefers-color-scheme", "data-theme"} {
		if !strings.Contains(themeSource, want) {
			t.Fatalf("theme.js does not apply the stored theme with %q", want)
		}
	}
}

func TestVendoredPreactOnlyHasTheSVGNamespaceURL(t *testing.T) {
	data, err := fs.ReadFile(assetsFS, "assets/vendor/preact-htm.js")
	if err != nil {
		t.Fatal(err)
	}
	const svgNamespace = "http://www.w3.org/2000/svg"
	text := string(data)
	if !strings.Contains(text, svgNamespace) {
		t.Fatal("vendored preact-htm.js does not contain the expected SVG namespace")
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		// The first line is the upstream attribution comment. It names the
		// npm package URL and is part of the unmodified vendored file.
		text = strings.Join(lines[1:], "\n")
	}
	text = strings.ReplaceAll(text, svgNamespace, "")
	if loc := regexp.MustCompile(`(?i)https?://`).FindString(text); loc != "" {
		t.Fatalf("vendored preact-htm.js has an unexpected external URL: %q", loc)
	}
}
