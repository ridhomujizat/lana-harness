package tailscale

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// call records one runner invocation.
type call struct {
	bin  string
	args []string
}

// fakeRunner is a Runner that returns canned output. It never runs the real
// CLI (REQ-RA-054).
type fakeRunner struct {
	calls   []call
	handler func(bin string, args []string) ([]byte, []byte, error)
}

func (f *fakeRunner) Run(_ context.Context, bin string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, call{bin: bin, args: append([]string(nil), args...)})
	if f.handler == nil {
		return nil, nil, nil
	}
	return f.handler(bin, args)
}

func (f *fakeRunner) lastArgs() []string {
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1].args
}

// finder returns a fixed binary lookup result.
func finder(path string, ok bool) func() (string, bool) {
	return func() (string, bool) { return path, ok }
}

// newTestClient returns a Client backed by r, found at a fixed path.
func newTestClient(r *fakeRunner) *Client {
	return NewWithRunner(r, finder("/usr/local/bin/tailscale", true))
}

// testdata reads a canned CLI response.
func testdata(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata %s: %v", name, err)
	}
	return data
}

func TestNewWiresDefaults(t *testing.T) {
	c := New()
	if c.runner == nil || c.lookPath == nil || c.now == nil || c.cache == nil {
		t.Fatalf("New client is not wired: %+v", c)
	}
}

func TestStatusNotInstalled(t *testing.T) {
	r := &fakeRunner{}
	c := NewWithRunner(r, finder("", false))
	st, err := c.Status(context.Background(), "127.0.0.1:18300")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if st.Installed || st.Path != "" {
		t.Fatalf("Status = %+v, want not installed", st)
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner called %d times for a missing binary", len(r.calls))
	}
}

func TestStatusStoppedBackend(t *testing.T) {
	r := &fakeRunner{handler: func(_ string, args []string) ([]byte, []byte, error) {
		switch args[0] {
		case "version":
			return []byte("tailscale v1.2.3\n"), nil, nil
		case "status":
			return nil, []byte("failed to connect to local tailscaled"), errors.New("exit status 1")
		}
		return nil, nil, nil
	}}
	st, err := newTestClient(r).Status(context.Background(), "127.0.0.1:18300")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if !st.Installed || st.Running {
		t.Fatalf("Status = %+v, want installed and not running", st)
	}
	if st.Version != "tailscale v1.2.3" {
		t.Fatalf("Version = %q", st.Version)
	}
}

func TestStatusRunningServing(t *testing.T) {
	r := &fakeRunner{handler: func(_ string, args []string) ([]byte, []byte, error) {
		switch args[0] {
		case "version":
			return []byte("1.60.0\n"), nil, nil
		case "status":
			return testdata(t, "status_running.json"), nil, nil
		case "serve":
			return testdata(t, "serve_ours.json"), nil, nil
		}
		return nil, nil, nil
	}}
	st, err := newTestClient(r).Status(context.Background(), "127.0.0.1:18300")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	want := Status{
		Installed:  true,
		Path:       "/usr/local/bin/tailscale",
		Version:    "1.60.0",
		Running:    true,
		DNSName:    "ridho-mbp.tail3f2a1.ts.net",
		Login:      "ridho@github",
		Tailnet:    "tail3f2a1.ts.net",
		MagicDNS:   true,
		HTTPSCerts: true,
		Serving:    true,
		ServeOther: false,
	}
	if st != want {
		t.Fatalf("Status = %+v, want %+v", st, want)
	}
}

func TestStatusServeOther(t *testing.T) {
	r := &fakeRunner{handler: func(_ string, args []string) ([]byte, []byte, error) {
		switch args[0] {
		case "status":
			return testdata(t, "status_running.json"), nil, nil
		case "serve":
			return testdata(t, "serve_other.json"), nil, nil
		}
		return nil, nil, nil
	}}
	st, err := newTestClient(r).Status(context.Background(), "127.0.0.1:18300")
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if st.Serving || !st.ServeOther {
		t.Fatalf("Status serving/other = %v/%v, want false/true", st.Serving, st.ServeOther)
	}
}

// TestStatusNoServeConfig covers empty and "{}" serve output: neither serving
// nor serve_other.
func TestStatusNoServeConfig(t *testing.T) {
	cases := []struct {
		name string
		out  string
	}{
		{"empty", ""},
		{"empty object", "{}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{handler: func(_ string, args []string) ([]byte, []byte, error) {
				switch args[0] {
				case "status":
					return testdata(t, "status_running.json"), nil, nil
				case "serve":
					return []byte(tc.out), nil, nil
				}
				return nil, nil, nil
			}}
			st, err := newTestClient(r).Status(context.Background(), "127.0.0.1:18300")
			if err != nil {
				t.Fatalf("Status error: %v", err)
			}
			if st.Serving || st.ServeOther {
				t.Fatalf("Status serving/other = %v/%v, want false/false", st.Serving, st.ServeOther)
			}
		})
	}
}

func TestServeState(t *testing.T) {
	cases := []struct {
		name           string
		out            string
		serving, other bool
	}{
		{"empty", "", false, false},
		{"empty object", "{}", false, false},
		{"ours", `{"TCP":{"443":{"HTTPS":true}},"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:18300"}}}}}`,
			true, false},
		{"ours with trailing slash", `{"TCP":{"443":{"HTTPS":true}},"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:18300/"}}}}}`,
			true, false},
		{"other target", `{"TCP":{"443":{"HTTPS":true}},"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9000"}}}}}`,
			false, true},
		{"tcp 443 without web", `{"TCP":{"443":{"HTTPS":true}}}`, false, true},
		{"web 443 without tcp https", `{"Web":{"h.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:18300"}}}}}`,
			false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{handler: func(_ string, _ []string) ([]byte, []byte, error) {
				return []byte(tc.out), nil, nil
			}}
			serving, other := newTestClient(r).serveState(context.Background(), "/usr/local/bin/tailscale", "127.0.0.1:18300")
			if serving != tc.serving || other != tc.other {
				t.Fatalf("serveState = %v/%v, want %v/%v", serving, other, tc.serving, tc.other)
			}
		})
	}
}

func TestServeArgs(t *testing.T) {
	r := &fakeRunner{}
	if err := newTestClient(r).Serve(context.Background(), "127.0.0.1:18300"); err != nil {
		t.Fatalf("Serve error: %v", err)
	}
	want := []string{"serve", "--bg", "--https=443", "http://127.0.0.1:18300"}
	if got := r.lastArgs(); !slices.Equal(got, want) {
		t.Fatalf("Serve args = %v, want %v", got, want)
	}
}

func TestUnserveArgs(t *testing.T) {
	r := &fakeRunner{}
	if err := newTestClient(r).Unserve(context.Background()); err != nil {
		t.Fatalf("Unserve error: %v", err)
	}
	want := []string{"serve", "--https=443", "off"}
	if got := r.lastArgs(); !slices.Equal(got, want) {
		t.Fatalf("Unserve args = %v, want %v", got, want)
	}
}

func TestServeErrorSurfacedAndTruncated(t *testing.T) {
	long := strings.Repeat("x", errorCut+200)
	r := &fakeRunner{handler: func(_ string, _ []string) ([]byte, []byte, error) {
		return nil, []byte(long + "\n"), errors.New("exit status 1")
	}}
	err := newTestClient(r).Serve(context.Background(), "127.0.0.1:18300")
	if err == nil {
		t.Fatal("Serve returned no error")
	}
	msg := strings.TrimPrefix(err.Error(), "tailscale serve: ")
	if len(msg) != errorCut || strings.Trim(msg, "x") != "" {
		t.Fatalf("stderr not surfaced and cut to %d: %q (%d chars)", errorCut, msg, len(msg))
	}
}

func TestWhoisParseAndCache(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	r := &fakeRunner{handler: func(_ string, _ []string) ([]byte, []byte, error) {
		return testdata(t, "whois.json"), nil, nil
	}}
	c := NewWithRunner(r, finder("/usr/local/bin/tailscale", true))
	c.now = func() time.Time { return now }

	peer, err := c.Whois(context.Background(), "100.64.0.7")
	if err != nil {
		t.Fatalf("Whois error: %v", err)
	}
	if peer != (Peer{Device: "ridho-mbp", Login: "ridho@github"}) {
		t.Fatalf("Peer = %+v", peer)
	}
	if _, err := c.Whois(context.Background(), "100.64.0.7"); err != nil {
		t.Fatalf("cached Whois error: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("runner called %d times, want 1 (cache hit)", len(r.calls))
	}

	now = now.Add(whoisTTL + time.Second)
	if _, err := c.Whois(context.Background(), "100.64.0.7"); err != nil {
		t.Fatalf("expired Whois error: %v", err)
	}
	if len(r.calls) != 2 {
		t.Fatalf("runner called %d times, want 2 (cache expired)", len(r.calls))
	}
}

func TestWhoisDeviceFromNodeName(t *testing.T) {
	r := &fakeRunner{handler: func(_ string, _ []string) ([]byte, []byte, error) {
		return testdata(t, "whois_no_computed_name.json"), nil, nil
	}}
	peer, err := newTestClient(r).Whois(context.Background(), "100.64.0.7")
	if err != nil {
		t.Fatalf("Whois error: %v", err)
	}
	if peer.Device != "ridho-mbp" {
		t.Fatalf("Device = %q, want the first label of Node.Name", peer.Device)
	}
}

func TestWhoisInvalidIP(t *testing.T) {
	r := &fakeRunner{}
	if _, err := newTestClient(r).Whois(context.Background(), "not-an-ip"); err == nil {
		t.Fatal("Whois accepted an invalid IP")
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner called %d times for an invalid IP", len(r.calls))
	}
}

func TestWhoisErrorSurfacesStderr(t *testing.T) {
	r := &fakeRunner{handler: func(_ string, _ []string) ([]byte, []byte, error) {
		return nil, []byte("no match for IP\n"), errors.New("exit status 1")
	}}
	_, err := newTestClient(r).Whois(context.Background(), "100.64.0.7")
	if err == nil || !strings.Contains(err.Error(), "no match for IP") {
		t.Fatalf("Whois error = %v", err)
	}
}

func TestNotInstalledErrors(t *testing.T) {
	c := NewWithRunner(&fakeRunner{}, finder("", false))
	ctx := context.Background()
	if err := c.Serve(ctx, "127.0.0.1:18300"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Serve error = %v, want ErrNotInstalled", err)
	}
	if err := c.Unserve(ctx); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Unserve error = %v, want ErrNotInstalled", err)
	}
	if _, err := c.Whois(ctx, "100.64.0.7"); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("Whois error = %v, want ErrNotInstalled", err)
	}
}
