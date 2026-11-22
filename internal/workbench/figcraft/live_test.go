package figcraft

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The tests drive live mode against a fake control server: a TCP listener on
// 127.0.0.1 that answers the read-only protocol with canned JSON and records
// the methods it received (REQ-FV-100). The file source stays the fake CLI of
// figcraft_test.go, so fallback is tested without a real app.

// liveSeen is one request the fake server received.
type liveSeen struct {
	method  string
	command string
	params  json.RawMessage
}

// liveServer is a fake FigCraft control server.
type liveServer struct {
	t  *testing.T
	ln net.Listener

	mu     sync.Mutex
	seen   []liveSeen
	conns  int
	docs   string
	pages  string
	ins    string
	nodes  map[int64]string
	render string

	failMethod string
	failMode   string // "hangup" or "error"
}

// newLiveServer starts the fake server and stops it when the test ends.
func newLiveServer(t *testing.T) *liveServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &liveServer{
		t:      t,
		ln:     ln,
		docs:   "[]",
		pages:  "[]",
		ins:    `{"dirty":false,"history":{"redo":0,"undo":[]},"pages":[]}`,
		nodes:  make(map[int64]string),
		render: tinyPNGBase64(t),
	}
	go s.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

// port is the port the fake server listens on.
func (s *liveServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// serve accepts connections until the listener closes.
func (s *liveServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns++
		s.mu.Unlock()
		go s.handle(conn)
	}
}

// handle answers the newline-delimited requests of one connection.
func (s *liveServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		var params struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(req.Params, &params)

		s.mu.Lock()
		s.seen = append(s.seen, liveSeen{method: req.Method, command: params.Command, params: req.Params})
		fail := s.failMethod != "" && req.Method == s.failMethod
		mode := s.failMode
		s.mu.Unlock()

		if fail && mode == "hangup" {
			return
		}
		if fail && mode == "error" {
			_, _ = fmt.Fprintf(conn, "{\"id\":%d,\"ok\":false,\"error\":\"boom\"}\n", req.ID)
			continue
		}
		result, ok := s.result(req.Method, params.Command, req.Params)
		if !ok {
			_, _ = fmt.Fprintf(conn, "{\"id\":%d,\"ok\":false,\"error\":\"unexpected %s\"}\n", req.ID, req.Method)
			continue
		}
		_, _ = fmt.Fprintf(conn, "{\"id\":%d,\"ok\":true,\"result\":%s}\n", req.ID, result)
	}
}

// result is the canned reply of one request, or false for an unknown method.
func (s *liveServer) result(method, command string, params json.RawMessage) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "engine.execute":
		switch command {
		case "doc.list":
			return s.docs, true
		case "page.list":
			return s.pages, true
		}
	case "document.inspect":
		return s.ins, true
	case "node.get":
		var p struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(params, &p)
		if n, ok := s.nodes[p.ID]; ok {
			return n, true
		}
		return "", false
	case "ui.render":
		return `{"format":"png","data":"` + s.render + `"}`, true
	}
	return "", false
}

// setDocs makes doc.list report the given open files.
func (s *liveServer) setDocs(paths ...string) {
	docs := make([]liveDoc, 0, len(paths))
	for i, p := range paths {
		docs = append(docs, liveDoc{Active: true, ID: int64(i + 1), Index: i, Name: filepath.Base(p), Path: p})
	}
	data, _ := json.Marshal(docs)
	s.mu.Lock()
	s.docs = string(data)
	s.mu.Unlock()
}

// setPages sets the page.list result.
func (s *liveServer) setPages(pages string) {
	s.mu.Lock()
	s.pages = pages
	s.mu.Unlock()
}

// setInspect sets the document.inspect result.
func (s *liveServer) setInspect(ins string) {
	s.mu.Lock()
	s.ins = ins
	s.mu.Unlock()
}

// setNode sets one node.get result.
func (s *liveServer) setNode(id int64, node string) {
	s.mu.Lock()
	s.nodes[id] = node
	s.mu.Unlock()
}

// failOn makes the server fail the named method with the given mode.
func (s *liveServer) failOn(method, mode string) {
	s.mu.Lock()
	s.failMethod = method
	s.failMode = mode
	s.mu.Unlock()
}

// seenCommand reports whether the server received an engine.execute command.
func (s *liveServer) seenCommand(command string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.seen {
		if e.command == command {
			return true
		}
	}
	return false
}

// connections is how many connections the server accepted.
func (s *liveServer) connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

// lastParams is the params of the most recent request with the given method.
func (s *liveServer) lastParams(method string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.seen) - 1; i >= 0; i-- {
		if s.seen[i].method == method {
			var m map[string]any
			_ = json.Unmarshal(s.seen[i].params, &m)
			return m
		}
	}
	return nil
}

// tinyPNGBase64 is a base64 2x2 PNG, the ui.render payload.
func tinyPNGBase64(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

// liveService builds a Service that uses the fake CLI and one live port, with a
// discovery that finds nothing else.
func liveService(f *fakeCLI, port int, live bool) *Service {
	return f.service(func(o *Options) {
		o.Settings = func() Settings {
			return Settings{CLIPath: f.cli, Live: live, LivePorts: []int{port}}
		}
		o.DiscoverPorts = func(context.Context) ([]int, error) { return nil, nil }
	})
}

const (
	livePages    = `[{"children":1,"current":false,"divider":false,"id":10,"name":"Home"}]`
	liveHomeNode = `{"id":10,"name":"Home","type":"PAGE","children":[` +
		`{"id":101,"name":"A","type":"FRAME","absoluteBounds":{"x":100,"y":50,"width":200,"height":100},"childCount":0,"visible":true}]}`
)

func TestLiveDiscoveryMatchesByPath(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	real := f.design("a.figcraft", "zip")
	link := filepath.Join(f.work, "link.figcraft")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	srv.setDocs(real)
	svc := liveService(f, srv.port(), true)
	ctx := context.Background()

	got, err := svc.Info(ctx, real)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "live" || got.LivePort != srv.port() {
		t.Fatalf("Info(real) = %s/%d, want live/%d", got.Source, got.LivePort, srv.port())
	}

	viaLink, err := svc.Info(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	if viaLink.Source != "live" {
		t.Errorf("Info(symlink) source = %q, want live", viaLink.Source)
	}
}

func TestLiveOffNeverDials(t *testing.T) {
	f := newFakeCLI(t)
	f.homeFixtures()
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)

	discovered := false
	svc := f.service(func(o *Options) {
		o.Settings = func() Settings {
			return Settings{CLIPath: f.cli, Live: false, LivePorts: []int{srv.port()}}
		}
		o.DiscoverPorts = func(context.Context) ([]int, error) { discovered = true; return nil, nil }
	})

	info, err := svc.Info(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "file" {
		t.Errorf("source = %q, want file", info.Source)
	}
	if n := srv.connections(); n != 0 {
		t.Errorf("connections = %d, want 0", n)
	}
	if discovered {
		t.Error("discovery ran, want none while live is off")
	}
}

func TestLiveAllowlistRefusesWrites(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	svc := liveService(f, srv.port(), true)
	ctx := context.Background()

	if _, err := svc.Info(ctx, abs); err != nil {
		t.Fatal(err)
	}
	c, ok := svc.src.(*chooser)
	if !ok {
		t.Fatalf("src is %T, want *chooser", svc.src)
	}
	ctl := c.control(srv.port())
	for _, command := range []string{"node.set", "app.quit"} {
		if _, err := ctl.call(ctx, "engine.execute", map[string]any{"command": command}); err == nil {
			t.Errorf("%s was allowed, want a refusal", command)
		}
		if srv.seenCommand(command) {
			t.Errorf("%s reached the server, want it refused before the socket", command)
		}
	}
}

func TestLiveFingerprintFollowsHistory(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	svc := liveService(f, srv.port(), true)
	ctx := context.Background()

	srv.setInspect(`{"dirty":false,"history":{"redo":0,"undo":["a"]},"pages":[]}`)
	first, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.Fingerprint, "l-") {
		t.Fatalf("fingerprint = %q, want an l- prefix", first.Fingerprint)
	}

	srv.setInspect(`{"dirty":false,"history":{"redo":0,"undo":["a","b"]},"pages":[]}`)
	changed, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint == first.Fingerprint {
		t.Errorf("fingerprint %q unchanged after a history change", changed.Fingerprint)
	}

	srv.setInspect(`{"dirty":false,"history":{"redo":0,"undo":["a"]},"pages":[]}`)
	same, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if same.Fingerprint != first.Fingerprint {
		t.Errorf("fingerprint %q, want the same as %q", same.Fingerprint, first.Fingerprint)
	}
}

func TestLiveUnsavedMirrorsDirty(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	svc := liveService(f, srv.port(), true)
	ctx := context.Background()

	srv.setInspect(`{"dirty":true,"history":{"redo":0,"undo":[]},"pages":[]}`)
	dirty, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty.Unsaved {
		t.Error("unsaved = false, want true")
	}

	srv.setInspect(`{"dirty":false,"history":{"redo":0,"undo":[]},"pages":[]}`)
	clean, err := svc.Info(ctx, abs)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Unsaved {
		t.Error("unsaved = true, want false")
	}
}

func TestLiveRenderPage(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	srv.setPages(livePages)
	srv.setNode(10, liveHomeNode)
	svc := liveService(f, srv.port(), true)

	r, err := svc.RenderPage(context.Background(), abs, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(r.PNG)); err != nil {
		t.Fatalf("render is not a valid PNG: %v", err)
	}
	if r.Scale != 4 {
		t.Errorf("scale = %v, want the clamp to 4", r.Scale)
	}
	if r.OriginX != 100 || r.OriginY != 50 {
		t.Errorf("origin = %v/%v, want 100/50", r.OriginX, r.OriginY)
	}
	if !strings.HasPrefix(r.Fingerprint, "l-") {
		t.Errorf("fingerprint = %q, want an l- prefix", r.Fingerprint)
	}

	params := srv.lastParams("ui.render")
	ids, _ := params["ids"].([]any)
	if len(ids) != 1 || ids[0].(float64) != 101 {
		t.Errorf("ui.render ids = %v, want [101]", params["ids"])
	}
	if scale, _ := params["scale"].(float64); scale != 4 {
		t.Errorf("ui.render scale = %v, want 4", params["scale"])
	}
}

func TestLiveLayers(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	srv.setNode(101, `{"id":101,"name":"A","type":"FRAME","children":[`+
		`{"id":111,"name":"Child","type":"TEXT","absoluteBounds":{"x":1,"y":1,"width":2,"height":2},"visible":true}]}`)
	svc := liveService(f, srv.port(), true)

	ch, err := svc.Layers(context.Background(), abs, 101)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Node != 101 || !strings.HasPrefix(ch.Fingerprint, "l-") {
		t.Errorf("node/fingerprint = %d/%q, want 101 and an l- prefix", ch.Node, ch.Fingerprint)
	}
	if len(ch.Children) != 1 || ch.Children[0].ID != 111 || ch.Children[0].Type != "TEXT" {
		t.Fatalf("children = %+v, want one TEXT node 111", ch.Children)
	}
}

func TestLiveRenderNode(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	srv.setNode(101, `{"id":101,"name":"A","type":"FRAME","absoluteBounds":{"x":100,"y":50,"width":200,"height":100},"visible":true}`)
	svc := liveService(f, srv.port(), true)

	r, err := svc.RenderNode(context.Background(), abs, 101, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(r.PNG)); err != nil {
		t.Fatalf("render is not a valid PNG: %v", err)
	}
	if r.Scale != 2 || r.OriginX != 100 || r.OriginY != 50 {
		t.Errorf("scale/origin = %v/%v/%v, want 2/100/50", r.Scale, r.OriginX, r.OriginY)
	}

	params := srv.lastParams("ui.render")
	ids, _ := params["ids"].([]any)
	if len(ids) != 1 || ids[0].(float64) != 101 {
		t.Errorf("ui.render ids = %v, want [101]", params["ids"])
	}
}

func TestLiveWithoutCLI(t *testing.T) {
	f := newFakeCLI(t)
	srv := newLiveServer(t)
	abs := f.design("a.figcraft", "zip")
	srv.setDocs(abs)
	svc := f.service(func(o *Options) {
		o.HomeDir = t.TempDir()
		o.Settings = func() Settings { return Settings{Live: true, LivePorts: []int{srv.port()}} }
		o.LookPath = func(string) (string, error) { return "", errors.New("not found") }
		o.DiscoverPorts = func(context.Context) ([]int, error) { return nil, nil }
	})

	info, err := svc.Info(context.Background(), abs)
	if err != nil {
		t.Fatal(err)
	}
	if info.Source != "live" {
		t.Errorf("source = %q, want live", info.Source)
	}
	if info.CLI != nil {
		t.Errorf("cli = %+v, want nil without a figcraft-cli", info.CLI)
	}
}

func TestLiveFallbackToFile(t *testing.T) {
	for _, mode := range []string{"hangup", "error"} {
		t.Run(mode, func(t *testing.T) {
			f := newFakeCLI(t)
			f.homeFixtures()
			srv := newLiveServer(t)
			abs := f.design("a.figcraft", "zip")
			srv.setDocs(abs)
			srv.failOn("document.inspect", mode)
			svc := liveService(f, srv.port(), true)

			info, err := svc.Info(context.Background(), abs)
			if err != nil {
				t.Fatal(err)
			}
			if info.Source != "file" {
				t.Errorf("source = %q, want file", info.Source)
			}
			if info.LiveError == "" {
				t.Error("live_error empty, want a short message")
			}
			if len(info.Pages) == 0 {
				t.Error("pages empty, want the file mode result")
			}
		})
	}
}

func TestParseLsofPorts(t *testing.T) {
	out := strings.Join([]string{
		"p4242",
		"cFigCraft",
		"n127.0.0.1:47810",
		"n*:47800",
		"n192.168.1.2:9999",
		"n[::1]:47811",
		"nlocalhost:47812",
		"n127.0.0.1:47810",
	}, "\n")

	want := []int{47800, 47810, 47811, 47812}
	if got := parseLsofPorts(out); !slices.Equal(got, want) {
		t.Errorf("parseLsofPorts = %v, want %v", got, want)
	}
	if got := parseLsofPorts(""); len(got) != 0 {
		t.Errorf("parseLsofPorts(empty) = %v, want none", got)
	}
}
