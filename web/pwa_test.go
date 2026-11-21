package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ridhomujizat/lana-harness/core"
)

// manifestLink is the line spec 68 inserts before </head>.
const manifestLink = "    <link rel=\"manifest\" href=\"/assets/manifest.json\">\n"

// TestWithManifestLink covers the pure helper of spec 68: the manifest link is
// inserted once, right before the first </head>, and an index without </head>
// is returned unchanged.
func TestWithManifestLink(t *testing.T) {
	t.Run("inserts the link before the first </head>", func(t *testing.T) {
		in := []byte("<head>\n  <title>x</title>\n  </head>\n<body></body>\n")
		got := withManifestLink(in)
		if n := bytes.Count(got, []byte(manifestLink)); n != 1 {
			t.Fatalf("link count = %d, want 1; got %q", n, got)
		}
		i := bytes.Index(got, []byte(manifestLink))
		if !bytes.HasPrefix(got[i+len(manifestLink):], []byte("</head>")) {
			t.Fatalf("the link is not directly before </head>: %q", got)
		}
		want := "<head>\n  <title>x</title>\n  " + manifestLink + "</head>\n<body></body>\n"
		if string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("only the first </head> is used", func(t *testing.T) {
		in := []byte("<head></head>\n</head>\n")
		got := withManifestLink(in)
		if n := bytes.Count(got, []byte(manifestLink)); n != 1 {
			t.Fatalf("link count = %d, want 1; got %q", n, got)
		}
		if want := "<head>" + manifestLink + "</head>\n</head>\n"; string(got) != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	})

	t.Run("no </head> leaves the index unchanged", func(t *testing.T) {
		in := []byte("<html><body>no head</body></html>")
		if got := withManifestLink(in); !bytes.Equal(got, in) {
			t.Fatalf("got %q, want the input unchanged", got)
		}
	})

	t.Run("does not modify the input slice", func(t *testing.T) {
		in := []byte("<head></head>")
		before := append([]byte(nil), in...)
		_ = withManifestLink(in)
		if !bytes.Equal(in, before) {
			t.Fatalf("the input changed: %q", in)
		}
	})
}

// TestLocalIndexHasNoManifestLink covers spec 68: the local Mac page never
// offers the manifest, so Lana cannot be installed as an app there.
func TestLocalIndexHasNoManifestLink(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, core.DefaultWebRemoteConfig())

	rr := serveAuth(s, localHostRequest(http.MethodGet, "/", ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `rel="manifest"`) {
		t.Fatalf("the local index carries a manifest link: %q", rr.Body.String())
	}
}

// TestRemoteIndexHasManifestLink covers spec 68: an allowed remote request gets
// the index with the manifest link.
func TestRemoteIndexHasManifestLink(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())

	rr := serveAuth(s, remoteHostRequest(http.MethodGet, "/", "", remoteTestLogin))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `<link rel="manifest" href="/assets/manifest.json">`) {
		t.Fatalf("the remote index has no manifest link: %q", rr.Body.String())
	}
}

// TestRemoteManifestIsServed covers spec 68: the manifest is served without a
// session and stands alone as an installable app.
func TestRemoteManifestIsServed(t *testing.T) {
	s, _, _ := newRemoteTestServer(t, remoteEnabledConfig())

	rr := serveAuth(s, remoteHostRequest(http.MethodGet, "/assets/manifest.json", "", remoteTestLogin))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /assets/manifest.json status = %d, body = %q", rr.Code, rr.Body.String())
	}
	var manifest map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("manifest body %q: %v", rr.Body.String(), err)
	}
	if got := manifest["display"]; got != "standalone" {
		t.Fatalf("display = %v, want standalone", got)
	}
}
