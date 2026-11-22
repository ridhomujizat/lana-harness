package figcraft

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The stable-file wait of REQ-FV-007. They are variables so a test can shorten
// them.
var (
	stableWindow = 400 * time.Millisecond
	stablePoll   = 200 * time.Millisecond
	stableMax    = 3 * time.Second
)

// fileSource reads a design from the .figcraft file on disk (REQ-FV-004..006).
type fileSource struct {
	svc *Service
}

// infoData is the cached page list of one fingerprint.
type infoData struct {
	Pages []Page
}

// step is one command in a figcraft-cli run script (REQ-FV-003).
type step struct {
	Command string         `json:"command"`
	Params  map[string]any `json:"params"`
}

// cliStep is one JSON line of a run's output (REQ-FV-003).
type cliStep struct {
	Command string          `json:"command"`
	OK      bool            `json:"ok"`
	Error   string          `json:"error"`
	Result  json.RawMessage `json:"result"`
}

// pageSummary is one element of a page.list result (REQ-FV-004).
type pageSummary struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Divider  bool   `json:"divider"`
	Children int    `json:"children"`
}

// rawBounds is a node's absoluteBounds in page units (REQ-FV-004).
type rawBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// rawNode is one node of a node.get result (REQ-FV-004, REQ-FV-005). The CLI
// omits keys it does not have, so the optional ones are pointers.
type rawNode struct {
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"`
	Background     string     `json:"background"`
	ChildCount     *int       `json:"childCount"`
	Visible        *bool      `json:"visible"`
	AbsoluteBounds *rawBounds `json:"absoluteBounds"`
	Children       []rawNode  `json:"children"`
}

// Info answers a page list with each page's top-level layers (REQ-FV-004).
func (f *fileSource) Info(ctx context.Context, abs string) (Info, error) {
	if _, _, err := f.svc.cli(ctx); err != nil {
		return Info{}, err
	}
	fp, err := f.svc.fileFingerprint(ctx, abs)
	if err != nil {
		return Info{}, err
	}
	out, err := f.pages(ctx, abs, fp)
	if err != nil {
		return Info{}, err
	}
	return f.svc.buildInfo(ctx, abs, fp, out.data, out.stale), nil
}

// pagesOut is the page list of one request plus whether it is the last good
// result of a failing run (REQ-FV-007).
type pagesOut struct {
	data  infoData
	stale bool
}

// pages returns the page list of abs at fp, from the cache, a shared run or the
// last good value (REQ-FV-007, REQ-FV-008).
func (f *fileSource) pages(ctx context.Context, abs, fp string) (pagesOut, error) {
	const req = "pages"
	v, _, stale, err := f.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return f.rawPages(ctx, abs)
	})
	if err != nil {
		return pagesOut{}, err
	}
	return pagesOut{data: v.(infoData), stale: stale}, nil
}

// rawPages runs page.list and then node.get for every non-divider page, in two
// runs (REQ-FV-004).
func (f *fileSource) rawPages(ctx context.Context, abs string) (infoData, error) {
	dir, cleanup, err := f.svc.newRunDir()
	if err != nil {
		return infoData{}, err
	}
	defer cleanup()

	listSteps, _, err := f.runScript(ctx, abs, dir, []step{{Command: "page.list", Params: map[string]any{}}})
	if err != nil {
		return infoData{}, err
	}
	summaries, err := pageList(listSteps)
	if err != nil {
		return infoData{}, err
	}

	ids := make([]int64, 0, len(summaries))
	steps := make([]step, 0, len(summaries))
	for _, ps := range summaries {
		if ps.Divider {
			continue
		}
		ids = append(ids, ps.ID)
		steps = append(steps, step{Command: "node.get", Params: map[string]any{"id": ps.ID}})
	}

	nodes := make(map[int64]rawNode, len(ids))
	if len(steps) > 0 {
		nodeSteps, _, err := f.runScript(ctx, abs, dir, steps)
		if err != nil {
			return infoData{}, err
		}
		for i, st := range nodeSteps {
			if i >= len(ids) {
				break
			}
			if !st.OK {
				continue
			}
			var n rawNode
			if err := json.Unmarshal(st.Result, &n); err == nil {
				nodes[ids[i]] = n
			}
		}
	}
	return infoData{Pages: assemblePages(summaries, nodes)}, nil
}

// runScript writes steps to a script in the run temp folder and runs the CLI
// with "run <script> --in <file>" (REQ-FV-003).
func (f *fileSource) runScript(ctx context.Context, abs, dir string, steps []step) ([]cliStep, time.Duration, error) {
	script, err := json.Marshal(steps)
	if err != nil {
		return nil, 0, err
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, script, 0o600); err != nil {
		return nil, 0, err
	}
	out, took, err := f.svc.run(ctx, abs, dir, []string{"run", path, "--in", abs})
	if err != nil {
		return nil, took, err
	}
	parsed, err := parseSteps(out)
	if err != nil {
		return nil, took, err
	}
	return parsed, took, nil
}

// parseSteps reads the one-JSON-per-line output of a run (REQ-FV-003).
func parseSteps(out []byte) ([]cliStep, error) {
	var steps []cliStep
	scanner := bufio.NewScanner(bytes.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var st cliStep
		if err := json.Unmarshal(line, &st); err != nil {
			return nil, fmt.Errorf("figcraft: run output: %w", err)
		}
		steps = append(steps, st)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return steps, nil
}

// pageList returns the page summaries of a run's steps (REQ-FV-004).
func pageList(steps []cliStep) ([]pageSummary, error) {
	for _, st := range steps {
		if st.Command != "page.list" {
			continue
		}
		if !st.OK {
			return nil, stepError(st)
		}
		var out []pageSummary
		if err := json.Unmarshal(st.Result, &out); err != nil {
			return nil, fmt.Errorf("figcraft: page.list: %w", err)
		}
		return out, nil
	}
	return nil, &FailedError{Code: "figcraft_failed", Message: "no page.list result"}
}

// assemblePages builds the pages with their top-level layers, origin and size
// (REQ-FV-004). A divider keeps its name and verifies nothing else.
func assemblePages(summaries []pageSummary, nodes map[int64]rawNode) []Page {
	pages := make([]Page, 0, len(summaries))
	for _, ps := range summaries {
		page := Page{ID: ps.ID, Name: ps.Name, Divider: ps.Divider, Layers: []Layer{}}
		if node, ok := nodes[ps.ID]; ok {
			page.Background = node.Background
			for _, ch := range node.Children {
				page.Layers = append(page.Layers, layerFromRaw(ch))
			}
			page.Origin, page.Size = unionVisible(page.Layers)
		}
		pages = append(pages, page)
	}
	return pages
}

// layerFromRaw maps one CLI node to a Layer (REQ-FV-004). A missing visible is
// true; a missing childCount falls back to the inlined children, else 0.
func layerFromRaw(n rawNode) Layer {
	l := Layer{ID: n.ID, Name: n.Name, Type: n.Type, Visible: true}
	if n.AbsoluteBounds != nil {
		l.Bounds = Rect{
			X:      n.AbsoluteBounds.X,
			Y:      n.AbsoluteBounds.Y,
			Width:  n.AbsoluteBounds.Width,
			Height: n.AbsoluteBounds.Height,
		}
	}
	switch {
	case n.ChildCount != nil:
		l.ChildCount = *n.ChildCount
	case n.Children != nil:
		l.ChildCount = len(n.Children)
	}
	if n.Visible != nil {
		l.Visible = *n.Visible
	}
	return l
}

// unionVisible is the union of the bounds of the visible layers (REQ-FV-004):
// the origin is its top-left corner, the size its width and height. No visible
// layer gives a zero origin and size.
func unionVisible(layers []Layer) (Point, Size) {
	var minX, minY, maxX, maxY float64
	found := false
	for _, l := range layers {
		if !l.Visible {
			continue
		}
		x, y, w, h := l.Bounds.X, l.Bounds.Y, l.Bounds.Width, l.Bounds.Height
		if !found {
			minX, minY, maxX, maxY = x, y, x+w, y+h
			found = true
			continue
		}
		minX = min(minX, x)
		minY = min(minY, y)
		maxX = max(maxX, x+w)
		maxY = max(maxY, y+h)
	}
	if !found {
		return Point{}, Size{}
	}
	return Point{X: minX, Y: minY}, Size{Width: maxX - minX, Height: maxY - minY}
}

// Layers lists the children of one node with the same fields (REQ-FV-005). A
// node the CLI does not know is ErrNotFound.
func (f *fileSource) Layers(ctx context.Context, abs string, node int64) (Children, error) {
	if _, _, err := f.svc.cli(ctx); err != nil {
		return Children{}, err
	}
	fp, err := f.svc.fileFingerprint(ctx, abs)
	if err != nil {
		return Children{}, err
	}
	req := "children:" + strconv.FormatInt(node, 10)
	v, _, _, err := f.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return f.rawChildren(ctx, abs, node, fp)
	})
	if err != nil {
		return Children{}, err
	}
	return v.(Children), nil
}

// rawChildren runs node.get for one node and returns its children (REQ-FV-005).
func (f *fileSource) rawChildren(ctx context.Context, abs string, node int64, fp string) (Children, error) {
	dir, cleanup, err := f.svc.newRunDir()
	if err != nil {
		return Children{}, err
	}
	defer cleanup()

	steps, _, err := f.runScript(ctx, abs, dir, []step{{Command: "node.get", Params: map[string]any{"id": node}}})
	if err != nil {
		return Children{}, err
	}
	if len(steps) == 0 {
		return Children{}, &FailedError{Code: "figcraft_failed", Message: "no node.get result"}
	}
	if !steps[0].OK {
		return Children{}, stepError(steps[0])
	}
	var raw rawNode
	if err := json.Unmarshal(steps[0].Result, &raw); err != nil {
		return Children{}, fmt.Errorf("figcraft: node.get: %w", err)
	}
	ch := Children{Node: node, Fingerprint: fp, Children: []Layer{}}
	for _, c := range raw.Children {
		ch.Children = append(ch.Children, layerFromRaw(c))
	}
	return ch, nil
}

// stepError turns a failed step into ErrNotFound when the CLI says the node is
// missing, else a *FailedError (REQ-FV-005).
func stepError(st cliStep) error {
	msg := strings.ToLower(st.Error)
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no layer") {
		return ErrNotFound
	}
	return &FailedError{Code: "figcraft_failed", Message: st.Error}
}

// RenderPage renders one page as PNG at the clamped scale (REQ-FV-006).
func (f *fileSource) RenderPage(ctx context.Context, abs string, page int64, scale float64) (Render, error) {
	if _, _, err := f.svc.cli(ctx); err != nil {
		return Render{}, err
	}
	fp, err := f.svc.fileFingerprint(ctx, abs)
	if err != nil {
		return Render{}, err
	}
	out, err := f.pages(ctx, abs, fp)
	if err != nil {
		return Render{}, err
	}
	pg, ok := findPage(out.data.Pages, page)
	if !ok {
		return Render{}, ErrNotFound
	}
	s := clampScale(scale, math.Max(pg.Size.Width, pg.Size.Height))
	req := "render:page:" + strconv.FormatInt(page, 10) + ":" + formatScale(s)
	v, hit, stale, err := f.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return f.rawRenderPage(ctx, abs, pg, s, fp)
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

// rawRenderPage runs "render <file> -o <tmp>.png --page <name> --scale <s>"
// (REQ-FV-006).
func (f *fileSource) rawRenderPage(ctx context.Context, abs string, pg Page, s float64, fp string) (Render, error) {
	dir, cleanup, err := f.svc.newRunDir()
	if err != nil {
		return Render{}, err
	}
	defer cleanup()

	outPath := filepath.Join(dir, "out.png")
	_, took, err := f.svc.run(ctx, abs, dir, []string{"render", abs, "-o", outPath, "--page", pg.Name, "--scale", formatScale(s)})
	if err != nil {
		return Render{}, err
	}
	png, err := os.ReadFile(outPath)
	if err != nil {
		return Render{}, err
	}
	return Render{
		PNG:         png,
		OriginX:     pg.Origin.X,
		OriginY:     pg.Origin.Y,
		Scale:       s,
		Fingerprint: fp,
		Took:        took,
	}, nil
}

// RenderNode renders one node as PNG (REQ-FV-006). It first reads the node's own
// bounds with node.get, then runs "export <file> -o <tmp>.png --format png
// --node <id> --scale <s>".
func (f *fileSource) RenderNode(ctx context.Context, abs string, node int64, scale float64) (Render, error) {
	if _, _, err := f.svc.cli(ctx); err != nil {
		return Render{}, err
	}
	fp, err := f.svc.fileFingerprint(ctx, abs)
	if err != nil {
		return Render{}, err
	}
	bounds, err := f.nodeBounds(ctx, abs, node)
	if err != nil {
		return Render{}, err
	}
	s := clampScale(scale, math.Max(bounds.Width, bounds.Height))
	req := "render:node:" + strconv.FormatInt(node, 10) + ":" + formatScale(s)
	v, hit, stale, err := f.cached(cacheKey(abs, fp, req), staleKey(abs, req), func() (any, error) {
		return f.rawRenderNode(ctx, abs, node, bounds, s, fp)
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

// nodeBounds runs node.get for one node and returns its own bounds (REQ-FV-006).
func (f *fileSource) nodeBounds(ctx context.Context, abs string, node int64) (Rect, error) {
	dir, cleanup, err := f.svc.newRunDir()
	if err != nil {
		return Rect{}, err
	}
	defer cleanup()

	steps, _, err := f.runScript(ctx, abs, dir, []step{{Command: "node.get", Params: map[string]any{"id": node}}})
	if err != nil {
		return Rect{}, err
	}
	if len(steps) == 0 {
		return Rect{}, &FailedError{Code: "figcraft_failed", Message: "no node.get result"}
	}
	if !steps[0].OK {
		return Rect{}, stepError(steps[0])
	}
	var raw rawNode
	if err := json.Unmarshal(steps[0].Result, &raw); err != nil {
		return Rect{}, fmt.Errorf("figcraft: node.get: %w", err)
	}
	if raw.AbsoluteBounds == nil {
		return Rect{}, &FailedError{Code: "figcraft_failed", Message: "node has no bounds"}
	}
	b := raw.AbsoluteBounds
	return Rect{X: b.X, Y: b.Y, Width: b.Width, Height: b.Height}, nil
}

// rawRenderNode runs "export <file> -o <tmp>.png --format png --node <id>
// --scale <s>" (REQ-FV-006).
func (f *fileSource) rawRenderNode(ctx context.Context, abs string, node int64, bounds Rect, s float64, fp string) (Render, error) {
	dir, cleanup, err := f.svc.newRunDir()
	if err != nil {
		return Render{}, err
	}
	defer cleanup()

	outPath := filepath.Join(dir, "out.png")
	_, took, err := f.svc.run(ctx, abs, dir, []string{"export", abs, "-o", outPath, "--format", "png", "--node", strconv.FormatInt(node, 10), "--scale", formatScale(s)})
	if err != nil {
		return Render{}, err
	}
	png, err := os.ReadFile(outPath)
	if err != nil {
		return Render{}, err
	}
	return Render{
		PNG:         png,
		OriginX:     bounds.X,
		OriginY:     bounds.Y,
		Scale:       s,
		Fingerprint: fp,
		Took:        took,
	}, nil
}

// findPage returns the page with the given id (REQ-FV-006).
func findPage(pages []Page, id int64) (Page, bool) {
	for _, p := range pages {
		if p.ID == id {
			return p, true
		}
	}
	return Page{}, false
}

// clampScale applies the REQ-FV-006 scale rules: 0 or less is 1, then the
// 0.1..4 window, then a cap so longest*scale is at most 8192 px. longest is the
// longer side of the render in page units (0 leaves the scale alone).
func clampScale(scale, longest float64) float64 {
	if scale <= 0 {
		scale = 1
	}
	scale = min(max(scale, 0.1), 4)
	if longest > 0 {
		if limit := 8192 / longest; scale > limit {
			scale = limit
		}
	}
	return scale
}

// formatScale renders a scale for a CLI flag without a trailing ".0".
func formatScale(s float64) string {
	return strconv.FormatFloat(s, 'f', -1, 64)
}

// fileFingerprint is "f-<mtimeUnixNano>-<size>" of a stable file (REQ-FV-007).
// A missing file is ErrNotFound.
func (s *Service) fileFingerprint(ctx context.Context, abs string) (string, error) {
	st, err := s.stableStat(ctx, abs)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("f-%d-%d", st.mod.UnixNano(), st.size), nil
}

// fileStat is the part of a file's state the fingerprint uses.
type fileStat struct {
	mod  time.Time
	size int64
}

// statFile stats abs; a missing file or a non-regular file is ErrNotFound.
func statFile(abs string) (fileStat, error) {
	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fileStat{}, ErrNotFound
		}
		return fileStat{}, err
	}
	if !info.Mode().IsRegular() {
		return fileStat{}, ErrNotFound
	}
	return fileStat{mod: info.ModTime(), size: info.Size()}, nil
}

// stableStat waits for a file that changed less than stableWindow ago to settle
// (REQ-FV-007): it polls every stablePoll until two stats agree and the file is
// at least stableWindow old, for at most stableMax.
func (s *Service) stableStat(ctx context.Context, abs string) (fileStat, error) {
	cur, err := statFile(abs)
	if err != nil {
		return fileStat{}, err
	}
	if s.clock().Sub(cur.mod) >= stableWindow {
		return cur, nil
	}
	polls := int(stableMax / stablePoll)
	if polls < 1 {
		polls = 1
	}
	for i := 0; i < polls; i++ {
		if err := sleepCtx(ctx, stablePoll); err != nil {
			return fileStat{}, err
		}
		next, err := statFile(abs)
		if err != nil {
			return fileStat{}, err
		}
		if next == cur && s.clock().Sub(next.mod) >= stableWindow {
			return next, nil
		}
		cur = next
	}
	return cur, nil
}

// sleepCtx waits for d or until ctx is done.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// staleValue wraps the last good value a failing run falls back to
// (REQ-FV-007).
type staleValue struct {
	val any
}

// cached returns the value of a request from the cache, one shared run or the
// last good value (REQ-FV-007, REQ-FV-008). hit is true only on a direct cache
// hit; stale is true when the run failed and an older value answers.
func (f *fileSource) cached(ckey, skey string, fn func() (any, error)) (any, bool, bool, error) {
	if v, ok := f.svc.cache.get(ckey); ok {
		return v, true, false, nil
	}
	v, sfErr, _ := f.svc.runs.Do(ckey, func() (any, error) {
		if v, ok := f.svc.cache.get(ckey); ok {
			return v, nil
		}
		value, runErr := fn()
		if runErr != nil {
			if last, ok := f.svc.stale.get(skey); ok {
				return staleValue{val: last}, nil
			}
			return nil, runErr
		}
		f.svc.cache.put(ckey, value, measure(value))
		f.svc.stale.put(skey, value)
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

// cacheKey is the cache key of a request: the absolute path, the fingerprint and
// the request (REQ-FV-008).
func cacheKey(abs, fp, req string) string {
	return abs + "\x00" + fp + "\x00" + req
}

// staleKey is the last-good key of a request: the absolute path and the request
// only, so a new fingerprint still finds the last good value (REQ-FV-007).
func staleKey(abs, req string) string {
	return abs + "\x00" + req
}
