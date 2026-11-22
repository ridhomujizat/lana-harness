package figcraft

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Live mode (REQ-FV-010..013) reads an open document from the FigCraft app's
// control port instead of the file on disk, so unsaved edits show too. The
// source seam of figcraft.go is filled by a chooser: it answers a request from
// the app when the file is open there and the live setting is on, and from the
// file otherwise.

// The live protocol timings of REQ-FV-011. They are variables so a test can
// shorten them.
var (
	liveDialTimeout    = 3 * time.Second
	liveRequestTimeout = 10 * time.Second
	liveRenderTimeout  = 60 * time.Second
)

const (
	// liveRenderTimeoutMs extends the app's own wait for a render (REQ-FV-011).
	liveRenderTimeoutMs = 60000
	// liveDiscoverTTL is how long the discovered port list is reused
	// (REQ-FV-010).
	liveDiscoverTTL = 5 * time.Second
	// liveDocsTTL is how long one port's doc.list is reused (REQ-FV-010).
	liveDocsTTL = 3 * time.Second
	// liveMaxReply caps one reply line (REQ-FV-011).
	liveMaxReply = 256 << 20
)

// The node.get field lists of REQ-FV-012.
var (
	pageLayerFields  = []string{"name", "type", "absoluteBounds", "visible"}
	nodeBoundsFields = []string{"absoluteBounds"}
)

// liveTarget is where a file is open: the control port and the document path
// the app knows it by (REQ-FV-010).
type liveTarget struct {
	port int
	doc  string
}

// liveDoc is one element of a doc.list result (REQ-FV-010).
type liveDoc struct {
	Active bool   `json:"active"`
	Dirty  bool   `json:"dirty"`
	ID     int64  `json:"id"`
	Index  int    `json:"index"`
	Name   string `json:"name"`
	Path   string `json:"path"`
}

// chooser is the source a Service uses (REQ-FV-013): the live app when the file
// is open there and the live setting is on, else the file on disk. On any live
// error it falls back to file mode and, for Info, records the reason in
// LiveError.
type chooser struct {
	svc *Service

	discoverMu  sync.Mutex
	discoverAt  time.Time
	discoverSet []int

	docsMu    sync.Mutex
	docsAt    map[int]time.Time
	docsCache map[int][]liveDoc

	poolMu sync.Mutex
	pool   map[int]*control
}

// newChooser wires the live/file chooser the Service's source seam uses.
func newChooser(svc *Service) *chooser {
	return &chooser{
		svc:       svc,
		docsAt:    make(map[int]time.Time),
		docsCache: make(map[int][]liveDoc),
	}
}

// Info answers the page list from the live document, or the file on any live
// error (REQ-FV-012, REQ-FV-013).
func (c *chooser) Info(ctx context.Context, abs string) (Info, error) {
	t, ok := c.target(ctx, abs)
	if !ok {
		return c.svc.file.Info(ctx, abs)
	}
	info, err := c.live(t).Info(ctx, abs)
	if err == nil {
		return info, nil
	}
	c.svc.log.Warn("figcraft live failed, falling back to file", "file", abs, "port", t.port, "error", err)
	fallback, ferr := c.svc.file.Info(ctx, abs)
	if ferr != nil {
		return fallback, ferr
	}
	fallback.LiveError = liveShort(err)
	return fallback, nil
}

// Layers answers one node's children from the live document, or the file on any
// live error (REQ-FV-012, REQ-FV-013).
func (c *chooser) Layers(ctx context.Context, abs string, node int64) (Children, error) {
	t, ok := c.target(ctx, abs)
	if !ok {
		return c.svc.file.Layers(ctx, abs, node)
	}
	children, err := c.live(t).Layers(ctx, abs, node)
	if err == nil {
		return children, nil
	}
	c.svc.log.Warn("figcraft live failed, falling back to file", "file", abs, "port", t.port, "error", err)
	return c.svc.file.Layers(ctx, abs, node)
}

// RenderPage renders one page from the live document, or the file on any live
// error (REQ-FV-012, REQ-FV-013).
func (c *chooser) RenderPage(ctx context.Context, abs string, page int64, scale float64) (Render, error) {
	t, ok := c.target(ctx, abs)
	if !ok {
		return c.svc.file.RenderPage(ctx, abs, page, scale)
	}
	render, err := c.live(t).RenderPage(ctx, abs, page, scale)
	if err == nil {
		return render, nil
	}
	c.svc.log.Warn("figcraft live failed, falling back to file", "file", abs, "port", t.port, "error", err)
	return c.svc.file.RenderPage(ctx, abs, page, scale)
}

// RenderNode renders one node from the live document, or the file on any live
// error (REQ-FV-012, REQ-FV-013).
func (c *chooser) RenderNode(ctx context.Context, abs string, node int64, scale float64) (Render, error) {
	t, ok := c.target(ctx, abs)
	if !ok {
		return c.svc.file.RenderNode(ctx, abs, node, scale)
	}
	render, err := c.live(t).RenderNode(ctx, abs, node, scale)
	if err == nil {
		return render, nil
	}
	c.svc.log.Warn("figcraft live failed, falling back to file", "file", abs, "port", t.port, "error", err)
	return c.svc.file.RenderNode(ctx, abs, node, scale)
}

// target is the live port and document for a file, when live is on and the file
// is open in a discovered app (REQ-FV-010, REQ-FV-013).
func (c *chooser) target(ctx context.Context, abs string) (liveTarget, bool) {
	if !c.svc.settings().Live {
		return liveTarget{}, false
	}
	return c.liveTarget(ctx, abs)
}

// live builds the live source for one target.
func (c *chooser) live(t liveTarget) *liveSource {
	return &liveSource{svc: c.svc, ctl: c.control(t.port), port: t.port, doc: t.doc}
}

// liveTarget looks the file up among the open documents of the discovered
// ports (REQ-FV-010). Paths compare after EvalSymlinks and Clean on both sides,
// and the first matching port in ascending order wins.
func (c *chooser) liveTarget(ctx context.Context, abs string) (liveTarget, bool) {
	want := resolvePath(abs)
	for _, port := range c.ports(ctx) {
		docs, err := c.docs(ctx, port)
		if err != nil {
			c.svc.log.Debug("figcraft live discovery: doc.list failed", "port", port, "error", err)
			continue
		}
		for _, d := range docs {
			if d.Path != "" && resolvePath(d.Path) == want {
				return liveTarget{port: port, doc: d.Path}, true
			}
		}
	}
	return liveTarget{}, false
}

// ports is the live_ports setting unioned with the auto-detected ports, cached
// for liveDiscoverTTL and sorted ascending (REQ-FV-010).
func (c *chooser) ports(ctx context.Context) []int {
	c.discoverMu.Lock()
	if !c.discoverAt.IsZero() && c.svc.clock().Sub(c.discoverAt) < liveDiscoverTTL {
		p := c.discoverSet
		c.discoverMu.Unlock()
		return p
	}
	c.discoverMu.Unlock()

	set := make(map[int]struct{})
	add := func(p int) {
		if p >= 1 && p <= 65535 {
			set[p] = struct{}{}
		}
	}
	for _, p := range c.svc.settings().LivePorts {
		add(p)
	}
	if fn := c.svc.opts.DiscoverPorts; fn != nil {
		found, err := fn(ctx)
		if err != nil {
			c.svc.log.Warn("figcraft live discovery failed", "error", err)
		}
		for _, p := range found {
			add(p)
		}
	}

	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)

	c.discoverMu.Lock()
	c.discoverAt = c.svc.clock()
	c.discoverSet = out
	c.discoverMu.Unlock()
	return out
}

// docs is one port's doc.list, cached for liveDocsTTL (REQ-FV-010).
func (c *chooser) docs(ctx context.Context, port int) ([]liveDoc, error) {
	c.docsMu.Lock()
	if at, ok := c.docsAt[port]; ok && c.svc.clock().Sub(at) < liveDocsTTL {
		d := c.docsCache[port]
		c.docsMu.Unlock()
		return d, nil
	}
	c.docsMu.Unlock()

	res, err := c.control(port).call(ctx, "engine.execute", map[string]any{"command": "doc.list"})
	if err != nil {
		return nil, err
	}
	var docs []liveDoc
	if err := json.Unmarshal(res, &docs); err != nil {
		return nil, fmt.Errorf("figcraft: doc.list: %w", err)
	}

	c.docsMu.Lock()
	c.docsCache[port] = docs
	c.docsAt[port] = c.svc.clock()
	c.docsMu.Unlock()
	return docs, nil
}

// control is the one reused connection to a port (REQ-FV-011).
func (c *chooser) control(port int) *control {
	c.poolMu.Lock()
	defer c.poolMu.Unlock()
	if c.pool == nil {
		c.pool = make(map[int]*control)
	}
	ct := c.pool[port]
	if ct == nil {
		ct = &control{svc: c.svc, port: port}
		c.pool[port] = ct
	}
	return ct
}

// resolvePath cleans a path and resolves its symlinks, so two spellings of the
// same file compare equal (REQ-FV-010).
func resolvePath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

// liveSource reads a design from an open document over one control connection
// (REQ-FV-012). It implements the source interface, so the chooser can use it
// in place of the file source.
type liveSource struct {
	svc  *Service
	ctl  *control
	port int
	doc  string
}

// Info answers the page list of the open document (REQ-FV-012).
func (l *liveSource) Info(ctx context.Context, abs string) (Info, error) {
	ins, err := l.inspect(ctx)
	if err != nil {
		return Info{}, err
	}
	fp := liveFingerprint(ins)
	data, stale, err := l.pages(ctx, abs, fp)
	if err != nil {
		return Info{}, err
	}
	info := l.svc.buildInfo(ctx, abs, fp, data, stale)
	info.Source = "live"
	info.LivePort = l.port
	info.Unsaved = ins.Dirty
	return info, nil
}

// Layers answers one node's children of the open document (REQ-FV-012).
func (l *liveSource) Layers(ctx context.Context, abs string, node int64) (Children, error) {
	fp, err := l.fingerprint(ctx)
	if err != nil {
		return Children{}, err
	}
	req := "live:children:" + strconv.FormatInt(node, 10)
	v, _, _, err := l.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return l.rawChildren(ctx, node, fp)
	})
	if err != nil {
		return Children{}, err
	}
	return v.(Children), nil
}

// RenderPage renders one page of the open document at the clamped scale
// (REQ-FV-012).
func (l *liveSource) RenderPage(ctx context.Context, abs string, page int64, scale float64) (Render, error) {
	fp, err := l.fingerprint(ctx)
	if err != nil {
		return Render{}, err
	}
	data, _, err := l.pages(ctx, abs, fp)
	if err != nil {
		return Render{}, err
	}
	pg, ok := findPage(data.Pages, page)
	if !ok {
		return Render{}, ErrNotFound
	}
	s := clampScale(scale, math.Max(pg.Size.Width, pg.Size.Height))
	req := "live:render:page:" + strconv.FormatInt(page, 10) + ":" + formatScale(s)
	v, hit, stale, err := l.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return l.render(ctx, visibleTopIDs(pg), pg.Origin, s, fp)
	})
	if err != nil {
		return Render{}, err
	}
	r := v.(Render)
	if hit {
		r.Took = 0
	}
	r.Stale = stale
	return r, nil
}

// RenderNode renders one node of the open document at the clamped scale
// (REQ-FV-012).
func (l *liveSource) RenderNode(ctx context.Context, abs string, node int64, scale float64) (Render, error) {
	fp, err := l.fingerprint(ctx)
	if err != nil {
		return Render{}, err
	}
	raw, err := l.node(ctx, node, nodeBoundsFields)
	if err != nil {
		return Render{}, err
	}
	if raw.AbsoluteBounds == nil {
		return Render{}, &FailedError{Code: "figcraft_failed", Message: "node has no bounds"}
	}
	b := raw.AbsoluteBounds
	bounds := Rect{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}
	s := clampScale(scale, math.Max(bounds.Width, bounds.Height))
	req := "live:render:node:" + strconv.FormatInt(node, 10) + ":" + formatScale(s)
	v, hit, stale, err := l.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return l.render(ctx, []int64{node}, Point{X: bounds.X, Y: bounds.Y}, s, fp)
	})
	if err != nil {
		return Render{}, err
	}
	r := v.(Render)
	if hit {
		r.Took = 0
	}
	r.Stale = stale
	return r, nil
}

// pages returns the live page list at fp, from the cache or a fresh run
// (REQ-FV-008, REQ-FV-012).
func (l *liveSource) pages(ctx context.Context, abs, fp string) (infoData, bool, error) {
	v, _, stale, err := l.cached(cacheKey(abs, fp, "live:pages"), staleKey(abs, "live:pages"), func() (any, error) {
		return l.rawPages(ctx)
	})
	if err != nil {
		return infoData{}, false, err
	}
	return v.(infoData), stale, nil
}

// rawPages runs page.list and then node.get for every non-divider page
// (REQ-FV-012). It reuses the file mode's Layer mapping and visible-union.
func (l *liveSource) rawPages(ctx context.Context) (infoData, error) {
	summaries, err := l.pageList(ctx)
	if err != nil {
		return infoData{}, err
	}
	nodes := make(map[int64]rawNode, len(summaries))
	for _, ps := range summaries {
		if ps.Divider {
			continue
		}
		n, err := l.node(ctx, ps.ID, pageLayerFields)
		if err != nil {
			return infoData{}, err
		}
		nodes[ps.ID] = n
	}
	return infoData{Pages: assemblePages(summaries, nodes)}, nil
}

// rawChildren runs node.get for one node and returns its children (REQ-FV-012).
func (l *liveSource) rawChildren(ctx context.Context, node int64, fp string) (Children, error) {
	raw, err := l.node(ctx, node, pageLayerFields)
	if err != nil {
		return Children{}, err
	}
	ch := Children{Node: node, Fingerprint: fp, Children: []Layer{}}
	for _, c := range raw.Children {
		ch.Children = append(ch.Children, layerFromRaw(c))
	}
	return ch, nil
}

// render asks the app to render the given ids and decodes the PNG (REQ-FV-012).
func (l *liveSource) render(ctx context.Context, ids []int64, origin Point, scale float64, fp string) (Render, error) {
	start := l.svc.clock()
	res, err := l.ctl.render(ctx, ids, scale, l.doc)
	if err != nil {
		return Render{}, err
	}
	took := l.svc.clock().Sub(start)

	var out struct {
		Format string `json:"format"`
		Data   string `json:"data"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return Render{}, fmt.Errorf("figcraft: ui.render: %w", err)
	}
	png, err := base64.StdEncoding.DecodeString(out.Data)
	if err != nil {
		return Render{}, fmt.Errorf("figcraft: ui.render: %w", err)
	}
	return Render{
		PNG:         png,
		OriginX:     origin.X,
		OriginY:     origin.Y,
		Scale:       scale,
		Fingerprint: fp,
		Took:        took,
	}, nil
}

// pageList runs page.list for the open document (REQ-FV-012).
func (l *liveSource) pageList(ctx context.Context) ([]pageSummary, error) {
	res, err := l.ctl.call(ctx, "engine.execute", map[string]any{"command": "page.list", "document": l.doc})
	if err != nil {
		return nil, err
	}
	var out []pageSummary
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("figcraft: page.list: %w", err)
	}
	return out, nil
}

// node runs node.get for one node (REQ-FV-012).
func (l *liveSource) node(ctx context.Context, id int64, fields []string) (rawNode, error) {
	res, err := l.ctl.call(ctx, "node.get", map[string]any{
		"id":       id,
		"depth":    1,
		"fields":   fields,
		"document": l.doc,
	})
	if err != nil {
		return rawNode{}, err
	}
	var raw rawNode
	if err := json.Unmarshal(res, &raw); err != nil {
		return rawNode{}, fmt.Errorf("figcraft: node.get: %w", err)
	}
	return raw, nil
}

// inspect runs document.inspect for the open document (REQ-FV-012).
func (l *liveSource) inspect(ctx context.Context) (inspectReply, error) {
	res, err := l.ctl.call(ctx, "document.inspect", map[string]any{
		"depth":    0,
		"fields":   []string{},
		"document": l.doc,
	})
	if err != nil {
		return inspectReply{}, err
	}
	var out inspectReply
	if err := json.Unmarshal(res, &out); err != nil {
		return inspectReply{}, fmt.Errorf("figcraft: document.inspect: %w", err)
	}
	return out, nil
}

// fingerprint is the live fingerprint of the open document, from one
// document.inspect call (REQ-FV-012).
func (l *liveSource) fingerprint(ctx context.Context) (string, error) {
	ins, err := l.inspect(ctx)
	if err != nil {
		return "", err
	}
	return liveFingerprint(ins), nil
}

// cached returns the value of a live request from the cache, one shared run or
// the last good value, keyed by the live fingerprint (REQ-FV-008, REQ-FV-007).
func (l *liveSource) cached(ckey, skey string, fn func() (any, error)) (any, bool, bool, error) {
	if v, ok := l.svc.cache.get(ckey); ok {
		return v, true, false, nil
	}
	v, sfErr, _ := l.svc.runs.Do(ckey, func() (any, error) {
		if v, ok := l.svc.cache.get(ckey); ok {
			return v, nil
		}
		value, runErr := fn()
		if runErr != nil {
			if last, ok := l.svc.stale.get(skey); ok {
				return staleValue{val: last}, nil
			}
			return nil, runErr
		}
		l.svc.cache.put(ckey, value, measure(value))
		l.svc.stale.put(skey, value)
		return value, nil
	})
	if sfErr != nil {
		return nil, false, false, sfErr
	}
	if sv, ok := v.(staleValue); ok {
		return sv.val, false, true, nil
	}
	return v, false, false, nil
}

// inspectReply is the part of a document.inspect result the viewer uses
// (REQ-FV-012).
type inspectReply struct {
	Dirty   bool          `json:"dirty"`
	History historyInfo   `json:"history"`
	Pages   []pageSummary `json:"pages"`
}

// historyInfo is the undo/redo state of a live document (REQ-FV-012).
type historyInfo struct {
	Redo int      `json:"redo"`
	Undo []string `json:"undo"`
}

// liveFingerprint is "l-" plus the first 16 hex chars of the sha256 of the
// document's dirty flag, history and page summaries (REQ-FV-012).
func liveFingerprint(ins inspectReply) string {
	input := struct {
		Dirty   bool          `json:"dirty"`
		History historyInfo   `json:"history"`
		Pages   []pageSummary `json:"pages"`
	}{ins.Dirty, ins.History, ins.Pages}
	data, err := json.Marshal(input)
	if err != nil {
		data = nil
	}
	sum := sha256.Sum256(data)
	return "l-" + hex.EncodeToString(sum[:])[:16]
}

// visibleTopIDs is the ids of a page's visible top-level layers, the render ids
// of a live page (REQ-FV-012).
func visibleTopIDs(pg Page) []int64 {
	ids := make([]int64, 0, len(pg.Layers))
	for _, l := range pg.Layers {
		if l.Visible {
			ids = append(ids, l.ID)
		}
	}
	return ids
}

// liveShort is a short label for a live failure, put in Info.LiveError
// (REQ-FV-013).
func liveShort(err error) string {
	var failed *FailedError
	if errors.As(err, &failed) {
		if failed.Code == "figcraft_timeout" {
			return "timeout"
		}
		if msg := firstLine(failed.Message); msg != "" {
			return msg
		}
		return failed.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return "connection refused"
	case strings.Contains(msg, "no route to host"):
		return "unreachable"
	case strings.Contains(msg, "no such host"):
		return "unreachable"
	}
	if line := firstLine(msg); line != "" {
		return line
	}
	return "live unavailable"
}

// control is one TCP connection to a live app's control port, reused across
// requests (REQ-FV-011). Requests on one connection are serialized, and the
// connection is redialled after any error.
type control struct {
	svc  *Service
	port int

	mu   sync.Mutex
	conn net.Conn
	br   *bufio.Reader
	id   int
}

// liveRequest is one protocol request line (REQ-FV-011).
type liveRequest struct {
	ID        int            `json:"id"`
	Method    string         `json:"method"`
	Params    map[string]any `json:"params,omitempty"`
	TimeoutMs int            `json:"timeoutMs,omitempty"`
}

// liveReply is one protocol reply line (REQ-FV-011).
type liveReply struct {
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Result json.RawMessage `json:"result"`
}

// call runs one request with the default timeout (REQ-FV-011).
func (c *control) call(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	return c.do(ctx, method, params, liveRequestTimeout, 0)
}

// render runs ui.render with the longer timeout and the timeoutMs extension
// (REQ-FV-011).
func (c *control) render(ctx context.Context, ids []int64, scale float64, doc string) (json.RawMessage, error) {
	params := map[string]any{"ids": ids, "scale": scale, "document": doc}
	return c.do(ctx, "ui.render", params, liveRenderTimeout, liveRenderTimeoutMs)
}

// do sends one request and reads one reply, refusing anything outside the
// read-only allowlist before it reaches the socket (REQ-FV-011). An error reply
// is a *FailedError, and a client deadline is a *FailedError with
// "figcraft_timeout".
func (c *control) do(ctx context.Context, method string, params map[string]any, timeout time.Duration, timeoutMs int) (json.RawMessage, error) {
	if !allowed(method, params) {
		return nil, fmt.Errorf("figcraft: live: %q is not allowed", method)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connect(ctx); err != nil {
		return nil, err
	}

	c.id++
	data, err := json.Marshal(liveRequest{ID: c.id, Method: method, Params: params, TimeoutMs: timeoutMs})
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	deadline := time.Now().Add(timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := c.conn.SetDeadline(deadline); err != nil {
		c.reset()
		return nil, err
	}

	start := time.Now()
	if _, err := c.conn.Write(data); err != nil {
		c.reset()
		return nil, err
	}
	line, err := c.br.ReadBytes('\n')
	if int64(len(line)) > liveMaxReply {
		c.reset()
		return nil, fmt.Errorf("figcraft: live reply is larger than %d bytes", liveMaxReply)
	}
	if err != nil {
		c.reset()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, &FailedError{Code: "figcraft_timeout", Message: "live request timed out"}
		}
		return nil, err
	}

	var reply liveReply
	if err := json.Unmarshal(line, &reply); err != nil {
		c.reset()
		return nil, fmt.Errorf("figcraft: live reply: %w", err)
	}
	if !reply.OK {
		c.reset()
		return nil, &FailedError{Code: "figcraft_failed", Message: reply.Error}
	}
	c.svc.log.Debug("figcraft live request", "port", c.port, "method", method, "duration", time.Since(start))
	return reply.Result, nil
}

// connect dials the port when there is no live connection (REQ-FV-011).
func (c *control) connect(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: liveDialTimeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port)))
	if err != nil {
		return err
	}
	c.conn = conn
	c.br = bufio.NewReaderSize(conn, 64*1024)
	return nil
}

// reset drops the connection, so the next request redials (REQ-FV-011).
func (c *control) reset() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.br = nil
	}
}

// allowed is the read-only allowlist of REQ-FV-011, checked before any write to
// the socket. Only doc.list, page.list, document.inspect, node.get and
// ui.render pass.
func allowed(method string, params map[string]any) bool {
	switch method {
	case "document.inspect", "node.get", "ui.render":
		return true
	case "engine.execute":
		command, _ := params["command"].(string)
		return command == "doc.list" || command == "page.list"
	}
	return false
}
