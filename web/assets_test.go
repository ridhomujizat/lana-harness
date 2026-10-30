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

// assetURL maps a file in the embedded dist tree to the URL the server
// exposes for it. The dashboard is served at "/" and its files under the
// "/assets/" prefix, as before the React build.
func assetURL(name string) string {
	rel := strings.TrimPrefix(name, "dist/")
	if rel == "index.html" {
		return "/"
	}
	return "/assets/" + rel
}

// indexAssetRefs returns every /assets/ URL referenced by an index.html src
// or href attribute.
func indexAssetRefs(index []byte) []string {
	re := regexp.MustCompile(`(?i)(?:src|href)\s*=\s*"(/assets/[^"]*)"`)
	var refs []string
	for _, match := range re.FindAllStringSubmatch(string(index), -1) {
		refs = append(refs, match[1])
	}
	return refs
}

// indexAssetRef returns the first /assets/ URL in index whose path ends with
// ext, or the empty string.
func indexAssetRef(index []byte, ext string) string {
	for _, ref := range indexAssetRefs(index) {
		if strings.HasSuffix(strings.ToLower(ref), ext) {
			return ref
		}
	}
	return ""
}

func TestEmbeddedAssetsLoad(t *testing.T) {
	s, err := New(Config{Addr: "127.0.0.1:0", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	var files []string
	err = fs.WalkDir(distFS, "dist", func(name string, entry fs.DirEntry, walkErr error) error {
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
		t.Fatal("no embedded dist files found")
	}
	hasIndex := false
	jsCount := 0
	cssCount := 0
	for _, name := range files {
		if name == "dist/index.html" {
			hasIndex = true
		}
		if strings.HasPrefix(name, "dist/assets/") {
			switch strings.ToLower(path.Ext(name)) {
			case ".js":
				jsCount++
			case ".css":
				cssCount++
			}
		}
	}
	if !hasIndex {
		t.Fatal("dist/index.html is not embedded")
	}
	if jsCount == 0 {
		t.Fatal("dist/assets has no .js file")
	}
	if cssCount == 0 {
		t.Fatal("dist/assets has no .css file")
	}

	for _, name := range files {
		wantTypes := acceptedAssetContentTypes(name)
		if len(wantTypes) == 0 {
			t.Fatalf("%s has no expected Content-Type mapping", name)
		}
		url := assetURL(name)
		resp, err := http.Get(ts.URL + url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		body := readBody(t, resp)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", url, resp.StatusCode)
		}
		if body == "" {
			t.Fatalf("GET %s returned an empty body", url)
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
			t.Fatalf("GET %s Content-Type = %q, want one of %v", url, got, wantTypes)
		}
	}

	indexAsset, err := fs.ReadFile(distFS, "dist/index.html")
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
		t.Fatal("GET / does not serve dist/index.html")
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
			t.Fatalf("Content-Security-Policy %s does not permit same-origin shell resources: %q", name, directive)
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

// TestIndexHTMLReferencesOnlyAssets checks the built index.html: every
// resource it loads is under /assets/ and no script carries an inline body
// (the CSP is default-src 'self').
func TestIndexHTMLReferencesOnlyAssets(t *testing.T) {
	data, err := fs.ReadFile(distFS, "dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)

	scriptRE := regexp.MustCompile(`(?is)<script\b([^>]*)>(.*?)</script>`)
	scripts := scriptRE.FindAllStringSubmatch(body, -1)
	if len(scripts) == 0 {
		t.Fatal("index has no scripts")
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

	// Every element that loads a resource (stylesheet, image, media, frame)
	// must point at the /assets/ prefix.
	resourceRE := regexp.MustCompile(`(?is)<(?:link|img|source|video|audio|iframe|object|embed)\b([^>]*)>`)
	for _, element := range resourceRE.FindAllStringSubmatch(body, -1) {
		attrs := element[1]
		found := false
		for _, name := range []string{"href", "src", "poster", "data"} {
			value := attrValue(attrs, name)
			if value == "" {
				continue
			}
			found = true
			if !strings.HasPrefix(value, "/assets/") {
				t.Fatalf("index %s = %q, want an /assets/ path", name, value)
			}
		}
		if !found {
			t.Fatalf("index element loads no resource: %q", attrs)
		}
	}

	if strings.Contains(strings.ToLower(body), `style="`) {
		t.Fatal("index contains an inline style attribute")
	}
}

func assertAssetsHaveNoMatches(t *testing.T, dir, ext string, patterns map[string]*regexp.Regexp) {
	t.Helper()
	seen := 0
	err := fs.WalkDir(distFS, dir, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.EqualFold(path.Ext(name), ext) {
			return nil
		}
		seen++
		data, err := fs.ReadFile(distFS, name)
		if err != nil {
			return err
		}
		text := string(data)
		for form, re := range patterns {
			if loc := re.FindString(text); loc != "" {
				t.Fatalf("%s contains an external %s target: %q", name, form, loc)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen == 0 {
		t.Fatalf("%s has no %s files to check", dir, ext)
	}
}

// TestCSSAssetsHaveNoExternalURLs checks that the extracted CSS loads no
// external resource through url() or @import.
func TestCSSAssetsHaveNoExternalURLs(t *testing.T) {
	assertAssetsHaveNoMatches(t, "dist/assets", ".css", map[string]*regexp.Regexp{
		"url()":   regexp.MustCompile(`(?i)url\(\s*["']?\s*(?:https?:)?//`),
		"@import": regexp.MustCompile(`(?i)@import\s+(?:url\(\s*)?["']?\s*(?:https?:)?//`),
	})
}

// TestJSAssetsHaveNoExternalTargets checks the three call forms that load a
// resource at runtime: dynamic import(), src= assignment, and fetch().
// Plain string literals such as the React error-doc links are allowed.
func TestJSAssetsHaveNoExternalTargets(t *testing.T) {
	assertAssetsHaveNoMatches(t, "dist/assets", ".js", map[string]*regexp.Regexp{
		"import()": regexp.MustCompile("(?i)\\bimport\\(\\s*[\"'`]\\s*(?:https?:)?//"),
		"src=":     regexp.MustCompile("(?i)\\bsrc\\s*=\\s*[\"'`]\\s*(?:https?:)?//"),
		"fetch()":  regexp.MustCompile("(?i)\\bfetch\\(\\s*[\"'`]\\s*(?:https?:)?//"),
	})
}
