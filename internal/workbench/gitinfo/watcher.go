package gitinfo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// The watcher schedule of REQ-WM-080, REQ-WM-081 and REQ-WM-086.
const (
	watcherTick      = time.Second      // how often the background loop calls Tick
	watcherHot       = 3 * time.Second  // hot repos, and the HEAD/index stat check
	watcherSweepBusy = 10 * time.Second // sweep while the root is busy
	watcherSweepIdle = 120 * time.Second
	watcherOutput    = 30 * time.Second // after this much silence the root is idle
	watcherSubTTL    = 45 * time.Second // a subscription not renewed for this long ends
	watcherStopAfter = 30 * time.Second // a watcher with no subscription for this long stops
	watcherCacheTTL  = 10 * time.Second // how long a request summary is reused
)

// watcherSlowRuns is how many timeouts in a row mark a repo slow (REQ-WM-082).
const watcherSlowRuns = 2

// errWatchersClosed is returned by a request against a closed manager.
var errWatchersClosed = errors.New("gitinfo: root watchers closed")

// OtherEntry is a direct child of a root that is not a repository, as the root
// summary lists it (REQ-WM-003): its name and "dir" or "file".
type OtherEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// RootSummary is the answer of REQ-WM-003: the root, its direct child repos
// sorted case-insensitively, and its direct child folders and files that are
// not repos. ChangedRepos counts the repos with uncommitted changes and
// ChangedFiles the sum of their changed file counts. ETag is a short hash of
// the body (repo names, their etags and slow flags, and the other entries) and
// is not serialized; the HTTP route puts it in the ETag header.
type RootSummary struct {
	Root         string        `json:"root"`
	Name         string        `json:"name"`
	Repos        []RepoSummary `json:"repos"`
	Other        []OtherEntry  `json:"other"`
	ChangedRepos int           `json:"changed_repos"`
	ChangedFiles int           `json:"changed_files"`
	ETag         string        `json:"-"`
}

// Activity is what the caller knows about a root on each tick (REQ-WM-081):
// whether any session on it is working, the latest session output time, the
// process directories of its panes, and the spec 69 reported worktrees. The
// watcher uses it to pick the hot repos and the sweep interval; it never runs
// anything else.
type Activity struct {
	Working    bool
	LastOutput time.Time
	Dirs       []string
	Reported   []string
}

// ActivityFunc supplies the Activity of a root. The watcher calls it on each
// tick and on subscribe.
type ActivityFunc func(root string) Activity

// ScanFunc is the per-repo summary scan the watcher runs. Its default is
// Runner.ScanRepo; tests inject a fake.
type ScanFunc func(ctx context.Context, rootName, worktree string) RepoSummary

// cachedSummary is a request summary and the time it was stored (REQ-WM-080).
type cachedSummary struct {
	summary RootSummary
	at      time.Time
}

// RootWatchers keeps one activity-driven watcher per subscribed root
// (REQ-WM-080). Requests read its cache and the watchers push a summary when it
// changes; concurrent requests for the same root share one scan (singleflight).
// All methods are safe for concurrent use.
type RootWatchers struct {
	runner   *Runner
	activity ActivityFunc
	notify   func(root, etag string, repos map[string]string)

	// scan and now are test hooks: the per-repo scan (default Runner.ScanRepo)
	// and the time source (default time.Now).
	scan ScanFunc
	now  func() time.Time

	mu       sync.Mutex
	watchers map[string]*rootWatcher
	cached   map[string]cachedSummary
	group    singleflight.Group
	closed   bool

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewRootWatchers builds a manager and starts its one-second background loop.
// activity supplies the per-root session state and notify is called with
// (root, etag, repos) after a scan that changed a root's summary, where repos
// maps each repo with changes to its etag (REQ-WM-083).
func NewRootWatchers(r *Runner, activity ActivityFunc, notify func(root, etag string, repos map[string]string)) *RootWatchers {
	m := newRootWatchers(r, activity, notify)
	m.wg.Add(1)
	go m.loop()
	return m
}

// newRootWatchers builds a manager without its background loop, so a test can
// drive it through Tick. NewRootWatchers uses it and adds the loop.
func newRootWatchers(r *Runner, activity ActivityFunc, notify func(root, etag string, repos map[string]string)) *RootWatchers {
	if activity == nil {
		activity = func(string) Activity { return Activity{} }
	}
	if notify == nil {
		notify = func(string, string, map[string]string) {}
	}
	return &RootWatchers{
		runner:   r,
		activity: activity,
		notify:   notify,
		scan:     r.ScanRepo,
		now:      time.Now,
		watchers: map[string]*rootWatcher{},
		cached:   map[string]cachedSummary{},
		stop:     make(chan struct{}),
	}
}

// loop calls Tick every second until Close (REQ-WM-080).
func (m *RootWatchers) loop() {
	defer m.wg.Done()
	ticker := time.NewTicker(watcherTick)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.Tick(now)
		}
	}
}

// Close stops the background loop and waits for it (REQ-WM-080). It is safe to
// call more than once.
func (m *RootWatchers) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	close(m.stop)
	m.mu.Unlock()
	m.wg.Wait()
}

// Tick runs the schedule of every root once at now (REQ-WM-081). The background
// loop calls it every second; tests call it with a fake clock.
func (m *RootWatchers) Tick(now time.Time) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	roots := make([]string, 0, len(m.watchers))
	for root := range m.watchers {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	watchers := make([]*rootWatcher, 0, len(roots))
	for _, root := range roots {
		watchers = append(watchers, m.watchers[root])
	}
	m.mu.Unlock()

	for _, w := range watchers {
		w.tick(now)
	}
}

// Subscribe records or renews a client on root, validates it with DetectRoot
// (returning ErrNotARoot or ErrForbiddenPath) and starts the root's watcher if
// none, running the first full scan immediately (REQ-WM-080). A subscription
// that is not renewed for 45 s ends.
func (m *RootWatchers) Subscribe(ctx context.Context, root, client string) error {
	info, err := m.runner.DetectRoot(ctx, root)
	if err != nil {
		return err
	}
	key := info.Path
	now := m.now()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errWatchersClosed
	}
	if w := m.watchers[key]; w != nil {
		m.mu.Unlock()
		w.renew(client, now)
		return nil
	}
	w := &rootWatcher{
		m:         m,
		root:      key,
		name:      info.Name,
		clients:   map[string]time.Time{client: now},
		lastSeen:  now,
		lastSweep: now,
		lastHot:   now,
		repos:     map[string]*repoState{},
		ready:     make(chan struct{}),
	}
	m.watchers[key] = w
	m.mu.Unlock()
	w.scanFirst(ctx)
	return nil
}

// Unsubscribe drops a client from root. The watcher keeps running for 30 s in
// case the client comes back, then stops and drops its cache (REQ-WM-080).
func (m *RootWatchers) Unsubscribe(root, client string) {
	key := filepath.Clean(root)
	m.mu.Lock()
	w := m.watchers[key]
	m.mu.Unlock()
	if w != nil {
		w.drop(client, m.now())
	}
}

// Summary answers the root summary of REQ-WM-003. It returns the watcher's cache
// when a watcher has one; otherwise it runs one full scan shared with any
// concurrent caller (singleflight per root) and caches it for 10 s, so a root
// never has more than one scan in flight (REQ-WM-080).
func (m *RootWatchers) Summary(ctx context.Context, root string) (RootSummary, error) {
	key := filepath.Clean(root)
	now := m.now()

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return RootSummary{}, errWatchersClosed
	}
	w := m.watchers[key]
	cached, ok := m.cached[key]
	m.mu.Unlock()

	if w != nil {
		if s, ready := w.snapshot(ctx); ready {
			return s, nil
		}
	}
	if ok && now.Sub(cached.at) < watcherCacheTTL {
		return cached.summary, nil
	}

	v, err, _ := m.group.Do(key, func() (any, error) {
		m.mu.Lock()
		if c, ok := m.cached[key]; ok && m.now().Sub(c.at) < watcherCacheTTL {
			m.mu.Unlock()
			return c.summary, nil
		}
		m.mu.Unlock()
		return m.coldScan(ctx, key)
	})
	if err != nil {
		return RootSummary{}, err
	}
	return v.(RootSummary), nil
}

// Refresh runs a full scan of root now (the refresh button and working to idle,
// REQ-WM-081). With no watcher it scans into the request cache.
func (m *RootWatchers) Refresh(root string) {
	key := filepath.Clean(root)
	m.mu.Lock()
	w := m.watchers[key]
	m.mu.Unlock()
	if w != nil {
		w.refresh()
		return
	}
	_, _, _ = m.group.Do(key, func() (any, error) { return m.coldScan(context.Background(), key) })
}

// coldScan is the full scan of a root with no watcher: it discovers the repos,
// scans them all, builds the summary and caches it for 10 s (REQ-WM-080).
func (m *RootWatchers) coldScan(ctx context.Context, root string) (RootSummary, error) {
	info, other, err := m.discover(ctx, root)
	if err != nil {
		return RootSummary{}, err
	}
	repos := m.scanNames(ctx, info.Path, info.Repos)
	summary := buildSummary(info.Path, info.Name, repos, other)
	m.mu.Lock()
	m.cached[root] = cachedSummary{summary: summary, at: m.now()}
	m.mu.Unlock()
	return summary, nil
}

// remove forgets a stopped watcher and its cache (REQ-WM-080).
func (m *RootWatchers) remove(root string) {
	m.mu.Lock()
	delete(m.watchers, root)
	delete(m.cached, root)
	m.mu.Unlock()
}

// scanNames runs the per-repo scan of every name in parallel and returns the
// results in the same order. Parallelism is bounded by the shared git limit of
// REQ-WM-082 inside the runner, not here.
func (m *RootWatchers) scanNames(ctx context.Context, root string, names []string) []RepoSummary {
	repos := make([]RepoSummary, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			repos[i] = m.scan(ctx, root, filepath.Join(root, name))
		}(i, name)
	}
	wg.Wait()
	return repos
}

// discover reads a root fresh: it checks the path policy, refuses a folder
// inside a worktree, reads the direct children and reports the child repos and
// the other children. It is DetectRoot without the 5 s cache, so a sweep picks
// up a repo added or removed (REQ-WM-081).
func (m *RootWatchers) discover(ctx context.Context, root string) (RootInfo, []OtherEntry, error) {
	if err := m.runner.CheckPath(root); err != nil {
		return RootInfo{}, nil, err
	}
	if _, err := m.runner.Resolve(ctx, root); err == nil {
		return RootInfo{}, nil, ErrNotARoot
	} else if !errors.Is(err, ErrNotARepo) {
		return RootInfo{}, nil, err
	}
	dir := filepath.Clean(root)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return RootInfo{}, nil, ErrNotARoot
	}
	repos := repoChildren(dir, entries)
	if len(repos) == 0 {
		return RootInfo{}, nil, ErrNotARoot
	}
	return RootInfo{Path: dir, Name: filepath.Base(dir), Repos: repos}, otherChildren(entries, repos), nil
}

// otherChildren lists the direct children of a root that are not repos
// (REQ-WM-003): hidden children skipped, directories first then files, each
// case-insensitively sorted. It is never nil.
func otherChildren(entries []os.DirEntry, repos []string) []OtherEntry {
	isRepo := make(map[string]bool, len(repos))
	for _, name := range repos {
		isRepo[name] = true
	}
	dirs, files := []OtherEntry{}, []OtherEntry{}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || isRepo[name] {
			continue
		}
		row := OtherEntry{Name: name, Kind: treeFileKind}
		if entry.IsDir() {
			row.Kind = treeDirKind
			dirs = append(dirs, row)
		} else {
			files = append(files, row)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return lessName(dirs[i].Name, dirs[j].Name) })
	sort.Slice(files, func(i, j int) bool { return lessName(files[i].Name, files[j].Name) })
	return append(dirs, files...)
}

// buildSummary assembles a root summary from its repos and other children and
// stamps its ETag.
func buildSummary(root, name string, repos []RepoSummary, other []OtherEntry) RootSummary {
	if repos == nil {
		repos = []RepoSummary{}
	}
	if other == nil {
		other = []OtherEntry{}
	}
	summary := RootSummary{
		Root:  root,
		Name:  name,
		Repos: repos,
		Other: other,
	}
	for _, repo := range repos {
		if repo.Changed > 0 {
			summary.ChangedRepos++
		}
		summary.ChangedFiles += repo.Changed
	}
	summary.ETag = rootEtag(repos, other)
	return summary
}

// rootEtag is the summary ETag of REQ-WM-003: "r-" plus a short hash of the repo
// names, their etags and slow flags, and the other entries, so a client reloads
// only when something it shows changed.
func rootEtag(repos []RepoSummary, other []OtherEntry) string {
	h := sha256.New()
	for _, repo := range repos {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%t\x00", repo.Name, repo.Etag, repo.Slow)
	}
	for _, entry := range other {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00", entry.Name, entry.Kind)
	}
	return "r-" + hex.EncodeToString(h.Sum(nil))[:etagLen]
}

// rootWatcher is the per-root watcher of REQ-WM-080. Repo states and the
// assembled summary live behind mu; the manager removes it once it stops.
type rootWatcher struct {
	m    *RootWatchers
	root string

	mu       sync.Mutex
	name     string
	clients  map[string]time.Time
	lastSeen time.Time
	repos    map[string]*repoState
	order    []string
	other    []OtherEntry
	summary  RootSummary
	haveSum  bool
	etag     string

	started    bool
	wasWorking bool
	lastSweep  time.Time
	lastHot    time.Time
	stopped    bool

	// ready is closed after the first full scan, so a Summary that races a
	// fresh subscribe waits for it instead of starting a second scan.
	ready chan struct{}
}

// repoState is the last scan of one repo plus its slow streak and the
// HEAD/index modified times the stat trigger compares (REQ-WM-081, REQ-WM-082).
type repoState struct {
	summary  RepoSummary
	timeouts int
	headMod  time.Time
	indexMod time.Time
	haveStat bool
}

// rootEvent is a pending git_root push (REQ-WM-083).
type rootEvent struct {
	root  string
	etag  string
	repos map[string]string
}

// scanFirst records the subscribing client and runs the first full scan
// (REQ-WM-080). It holds the watcher lock, so a concurrent Tick or Summary
// waits, then closes ready.
func (w *rootWatcher) scanFirst(ctx context.Context) {
	w.mu.Lock()
	events := w.fullScan(ctx)
	w.wasWorking = w.m.activity(w.root).Working
	w.mu.Unlock()
	close(w.ready)
	w.push(events)
}

// renew records or refreshes a client.
func (w *rootWatcher) renew(client string, now time.Time) {
	w.mu.Lock()
	w.clients[client] = now
	w.lastSeen = now
	w.mu.Unlock()
}

// drop removes a client; when the last one leaves the stop countdown starts.
func (w *rootWatcher) drop(client string, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.clients[client]; !ok {
		return
	}
	delete(w.clients, client)
	if len(w.clients) == 0 {
		w.lastSeen = now
	}
}

// snapshot returns the current summary and whether the watcher has one,
// waiting for the first scan when it is still running.
func (w *rootWatcher) snapshot(ctx context.Context) (RootSummary, bool) {
	select {
	case <-w.ready:
	case <-ctx.Done():
		return RootSummary{}, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.haveSum {
		return RootSummary{}, false
	}
	return w.summary, true
}

// refresh runs a full scan now (REQ-WM-081).
func (w *rootWatcher) refresh() {
	w.mu.Lock()
	events := w.fullScan(context.Background())
	now := w.m.now()
	w.lastSweep, w.lastHot = now, now
	w.mu.Unlock()
	w.push(events)
}

// tick runs one schedule step of the watcher (REQ-WM-081).
func (w *rootWatcher) tick(now time.Time) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	wasEmpty := len(w.clients) == 0
	for client, at := range w.clients {
		if now.Sub(at) >= watcherSubTTL {
			delete(w.clients, client)
		}
	}
	if len(w.clients) == 0 {
		if !wasEmpty {
			// The last subscription just ended; start the stop countdown.
			w.lastSeen = now
		}
		if !now.Before(w.lastSeen.Add(watcherStopAfter)) {
			w.stopped = true
			w.mu.Unlock()
			w.m.remove(w.root)
			return
		}
		w.mu.Unlock()
		return
	}
	w.lastSeen = now

	act := w.m.activity(w.root)
	var events []rootEvent
	switch {
	case w.started && w.wasWorking && !act.Working:
		events = w.fullScan(context.Background())
		w.lastSweep, w.lastHot = now, now
	case w.started && !now.Before(w.lastSweep.Add(w.sweepEvery(act, now))):
		events = w.fullScan(context.Background())
		w.lastSweep, w.lastHot = now, now
	case w.started && !now.Before(w.lastHot.Add(watcherHot)):
		events = w.hotScan(context.Background(), act)
		w.lastHot = now
	}
	w.wasWorking = act.Working
	w.mu.Unlock()
	w.push(events)
}

// sweepEvery is the sweep interval of REQ-WM-081: 10 s while a session on the
// root is working or had output in the last 30 s, 120 s otherwise.
func (w *rootWatcher) sweepEvery(act Activity, now time.Time) time.Duration {
	if act.Working || now.Sub(act.LastOutput) < watcherOutput {
		return watcherSweepBusy
	}
	return watcherSweepIdle
}

// fullScan re-discovers the repos, scans them all and rebuilds the summary. It
// must be called with mu held.
func (w *rootWatcher) fullScan(ctx context.Context) []rootEvent {
	info, other, err := w.m.discover(ctx, w.root)
	if err != nil {
		return nil
	}
	w.started = true
	w.name = info.Name
	w.other = other
	keep := make(map[string]bool, len(info.Repos))
	for _, name := range info.Repos {
		keep[name] = true
	}
	for name := range w.repos {
		if !keep[name] {
			delete(w.repos, name)
		}
	}
	w.order = info.Repos
	for _, name := range info.Repos {
		if w.repos[name] == nil {
			w.repos[name] = &repoState{}
		}
	}
	results := w.m.scanNames(ctx, w.root, info.Repos)
	for i, name := range info.Repos {
		w.apply(name, results[i])
	}
	w.captureStats()
	return w.finish()
}

// hotScan scans the repos that may have changed (REQ-WM-081): the hot set and
// any repo whose .git/HEAD or .git/index modified time moved. Slow repos are
// left to the sweep. It must be called with mu held.
func (w *rootWatcher) hotScan(ctx context.Context, act Activity) []rootEvent {
	names := []string{}
	for _, name := range w.order {
		st := w.repos[name]
		if st == nil || st.summary.Slow {
			continue
		}
		if hotRepo(st, act) || w.statMoved(name, st) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	results := w.m.scanNames(ctx, w.root, names)
	for i, name := range names {
		w.apply(name, results[i])
	}
	return w.finish()
}

// hotRepo reports whether a repo is in the hot set (REQ-WM-081): it has
// uncommitted changes, or it holds a pane process directory or a reported
// worktree of the root.
func hotRepo(st *repoState, act Activity) bool {
	if st.summary.Changed > 0 {
		return true
	}
	for _, dir := range act.Dirs {
		if pathWithin(st.summary.Worktree, dir) {
			return true
		}
	}
	for _, dir := range act.Reported {
		if pathWithin(st.summary.Worktree, dir) {
			return true
		}
	}
	return false
}

// statMoved checks the repo's .git/HEAD and .git/index modified times and
// reports whether either moved since the last check (REQ-WM-081).
func (w *rootWatcher) statMoved(name string, st *repoState) bool {
	head, index := gitStatMtimes(filepath.Join(w.root, name))
	if !st.haveStat {
		st.headMod, st.indexMod, st.haveStat = head, index, true
		return false
	}
	moved := !head.Equal(st.headMod) || !index.Equal(st.indexMod)
	st.headMod, st.indexMod = head, index
	return moved
}

// captureStats records the HEAD/index modified times of every repo after a full
// scan, so the first hot tick does not rescan everything.
func (w *rootWatcher) captureStats() {
	for name, st := range w.repos {
		st.headMod, st.indexMod = gitStatMtimes(filepath.Join(w.root, name))
		st.haveStat = true
	}
}

// apply stores a repo scan, marking a repo slow after two timeouts in a row and
// clearing the mark on any scan that is not a timeout (REQ-WM-082).
func (w *rootWatcher) apply(name string, summary RepoSummary) {
	st := w.repos[name]
	if st == nil {
		st = &repoState{}
		w.repos[name] = st
	}
	if summary.Error == "git_timeout" {
		st.timeouts++
		if st.timeouts >= watcherSlowRuns {
			summary.Slow = true
		}
		if st.summary.Slow {
			summary.Slow = true
		}
	} else {
		st.timeouts = 0
		summary.Slow = false
	}
	st.summary = summary
}

// finish rebuilds the summary and returns a git_root push when its ETag changed
// since the last scan, with the repos that have changes (REQ-WM-083). The first
// scan only seeds the ETag. It must be called with mu held.
func (w *rootWatcher) finish() []rootEvent {
	repos := make([]RepoSummary, 0, len(w.order))
	for _, name := range w.order {
		if st := w.repos[name]; st != nil {
			repos = append(repos, st.summary)
		}
	}
	summary := buildSummary(w.root, w.name, repos, w.other)
	w.summary = summary
	w.haveSum = true
	previous := w.etag
	w.etag = summary.ETag
	if previous == "" || previous == summary.ETag {
		return nil
	}
	changed := make(map[string]string)
	for _, repo := range summary.Repos {
		if repo.Changed > 0 {
			changed[repo.Name] = repo.Etag
		}
	}
	return []rootEvent{{root: w.root, etag: summary.ETag, repos: changed}}
}

// push calls notify for each pending event, outside the watcher lock.
func (w *rootWatcher) push(events []rootEvent) {
	for _, event := range events {
		w.m.notify(event.root, event.etag, event.repos)
	}
}

// pathWithin reports whether path is dir or below it.
func pathWithin(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// gitStatMtimes returns the modified times of a repo's HEAD and index, which is
// what the stat trigger checks (REQ-WM-081). A missing file leaves a zero time,
// so the first check just records it.
func gitStatMtimes(repo string) (head, index time.Time) {
	dir := gitDirOf(repo)
	if info, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		head = info.ModTime()
	}
	if info, err := os.Stat(filepath.Join(dir, "index")); err == nil {
		index = info.ModTime()
	}
	return head, index
}

// gitDirOf is the git directory of a repo: its ".git" folder, or the folder a
// ".git" file points at with "gitdir: <path>" for a linked worktree
// (REQ-WM-081).
func gitDirOf(repo string) string {
	dotGit := filepath.Join(repo, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return dotGit
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit
	}
	line := strings.TrimSpace(string(raw))
	rest, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return dotGit
	}
	dir := strings.TrimSpace(rest)
	if dir == "" {
		return dotGit
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repo, dir)
	}
	return filepath.Clean(dir)
}
