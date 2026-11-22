package gitinfo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeClock is the watcher's injectable time source (REQ-WM-081).
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

// fakeScanner counts the per-repo scans and answers a fixed summary, so a test
// can see the schedule without running git.
type fakeScanner struct {
	mu   sync.Mutex
	runs map[string]int
	fn   func(name string) RepoSummary
}

func newFakeScanner() *fakeScanner {
	return &fakeScanner{runs: map[string]int{}}
}

func (f *fakeScanner) scan(_ context.Context, _, worktree string) RepoSummary {
	name := filepath.Base(worktree)
	f.mu.Lock()
	f.runs[name]++
	fn := f.fn
	f.mu.Unlock()
	if fn != nil {
		return fn(name)
	}
	return cleanSummary("root", name, worktree)
}

func (f *fakeScanner) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[name]
}

func (f *fakeScanner) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, count := range f.runs {
		total += count
	}
	return total
}

func (f *fakeScanner) set(fn func(name string) RepoSummary) {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
}

// activityBox is the injectable ActivityFunc (REQ-WM-081).
type activityBox struct {
	mu sync.Mutex
	a  Activity
}

func (b *activityBox) set(a Activity) {
	b.mu.Lock()
	b.a = a
	b.mu.Unlock()
}

func (b *activityBox) get(string) Activity {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.a
}

// notifyRecorder captures the git_root pushes (REQ-WM-083).
type notifyRecorder struct {
	mu     sync.Mutex
	events []notifyEvent
}

type notifyEvent struct {
	root  string
	etag  string
	repos map[string]string
}

func (n *notifyRecorder) record(root, etag string, repos map[string]string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, notifyEvent{root: root, etag: etag, repos: repos})
}

func (n *notifyRecorder) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.events)
}

func (n *notifyRecorder) last() notifyEvent {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.events) == 0 {
		return notifyEvent{}
	}
	return n.events[len(n.events)-1]
}

// countWatchers reports how many root watchers the manager holds.
func (m *RootWatchers) countWatchers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.watchers)
}

// cleanSummary is a healthy one-repo scan with a deterministic etag.
func cleanSummary(rootName, name, worktree string) RepoSummary {
	return RepoSummary{
		Name:     name,
		Display:  RepoDisplayName(rootName, name),
		Worktree: filepath.Clean(worktree),
		Branch:   "main",
		Short:    "main",
		Color:    Color(filepath.Clean(worktree)),
		Etag:     "e-" + name,
	}
}

// harness drives a manager with a fake clock, a fake scan and a fake activity.
type harness struct {
	t      *testing.T
	root   string
	runner *Runner
	m      *RootWatchers
	clk    *fakeClock
	scan   *fakeScanner
	act    *activityBox
	notes  *notifyRecorder
}

func newHarness(t *testing.T, repos ...string) *harness {
	t.Helper()
	root := t.TempDir()
	if len(repos) == 0 {
		repos = []string{"repo"}
	}
	for _, name := range repos {
		initRepo(t, filepath.Join(root, name))
	}
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	scan := newFakeScanner()
	act := &activityBox{}
	notes := &notifyRecorder{}
	runner := NewRunner(allowPolicy(root))
	m := newRootWatchers(runner, act.get, notes.record)
	m.now = clk.now
	m.scan = scan.scan
	t.Cleanup(m.Close)
	return &harness{t: t, root: root, runner: runner, m: m, clk: clk, scan: scan, act: act, notes: notes}
}

func (h *harness) subscribe(client string) {
	h.t.Helper()
	if err := h.m.Subscribe(context.Background(), h.root, client); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) tick(d time.Duration) {
	h.m.Tick(h.clk.add(d))
}

func (h *harness) summary() RootSummary {
	h.t.Helper()
	s, err := h.m.Summary(context.Background(), h.root)
	if err != nil {
		h.t.Fatal(err)
	}
	return s
}

func repoOf(t *testing.T, s RootSummary, name string) RepoSummary {
	t.Helper()
	for _, repo := range s.Repos {
		if repo.Name == name {
			return repo
		}
	}
	t.Fatalf("repo %q not in summary", name)
	return RepoSummary{}
}

func TestRootWatcherFirstScan(t *testing.T) {
	h := newHarness(t, "alpha", "beta")
	h.subscribe("c1")
	for _, name := range []string{"alpha", "beta"} {
		if got := h.scan.count(name); got != 1 {
			t.Errorf("%s scanned %d times, want 1", name, got)
		}
	}
	s := h.summary()
	if len(s.Repos) != 2 || s.Repos[0].Name != "alpha" || s.Repos[1].Name != "beta" {
		t.Fatalf("repos = %v, want alpha and beta", s.Repos)
	}
	if s.ETag == "" {
		t.Errorf("ETag is empty")
	}
	if s.ChangedRepos != 0 || s.ChangedFiles != 0 {
		t.Errorf("changed = %d/%d, want 0/0", s.ChangedRepos, s.ChangedFiles)
	}
}

func TestRootWatcherHotRepo(t *testing.T) {
	h := newHarness(t, "hot", "clean")
	rootName := filepath.Base(h.root)
	h.scan.set(func(name string) RepoSummary {
		s := cleanSummary(rootName, name, filepath.Join(h.root, name))
		if name == "hot" {
			s.Changed = 2
		}
		return s
	})
	h.subscribe("c1")

	h.tick(3 * time.Second)
	if got := h.scan.count("hot"); got != 2 {
		t.Errorf("hot scanned %d times, want 2", got)
	}
	if got := h.scan.count("clean"); got != 1 {
		t.Errorf("clean scanned %d times, want 1", got)
	}
}

func TestRootWatcherDirsAndReportedHot(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.subscribe("c1")

	h.act.set(Activity{Dirs: []string{filepath.Join(h.root, "a", "sub")}})
	h.tick(3 * time.Second)
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("dir repo a scanned %d times, want 2", got)
	}
	if got := h.scan.count("b"); got != 1 {
		t.Errorf("clean repo b scanned %d times, want 1", got)
	}

	h.act.set(Activity{Reported: []string{filepath.Join(h.root, "b", "worktree")}})
	h.tick(3 * time.Second)
	if got := h.scan.count("b"); got != 2 {
		t.Errorf("reported repo b scanned %d times, want 2", got)
	}
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("repo a scanned %d times, want 2 (no longer hot)", got)
	}
}

func TestRootWatcherSweepWorking(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.act.set(Activity{Working: true})
	h.subscribe("c1")

	h.tick(10 * time.Second)
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("a scanned %d times, want 2 after the 10 s sweep", got)
	}
	if got := h.scan.count("b"); got != 2 {
		t.Errorf("b scanned %d times, want 2 after the 10 s sweep", got)
	}
}

func TestRootWatcherSweepIdle(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.subscribe("c1")

	// The client renews every 30 s, so the subscription stays live to 120 s.
	h.tick(44 * time.Second)
	h.subscribe("c1")
	h.tick(44 * time.Second)
	h.subscribe("c1")
	if got := h.scan.count("a"); got != 1 {
		t.Errorf("a scanned %d times before the idle sweep, want 1", got)
	}

	h.tick(32 * time.Second) // t0 + 120 s
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("a scanned %d times, want 2 after the 120 s sweep", got)
	}
	if got := h.scan.count("b"); got != 2 {
		t.Errorf("b scanned %d times, want 2 after the 120 s sweep", got)
	}
}

func TestRootWatcherWorkingToIdle(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.act.set(Activity{Working: true})
	h.subscribe("c1")

	h.act.set(Activity{Working: false})
	h.tick(1 * time.Second)
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("a scanned %d times, want 2 after working to idle", got)
	}
	if got := h.scan.count("b"); got != 2 {
		t.Errorf("b scanned %d times, want 2 after working to idle", got)
	}
}

func TestRootWatcherRefresh(t *testing.T) {
	h := newHarness(t, "a", "b")
	h.subscribe("c1")

	h.m.Refresh(h.root)
	if got := h.scan.count("a"); got != 2 {
		t.Errorf("a scanned %d times, want 2 after Refresh", got)
	}
	if got := h.scan.count("b"); got != 2 {
		t.Errorf("b scanned %d times, want 2 after Refresh", got)
	}
}

func TestRootWatcherStatTrigger(t *testing.T) {
	h := newHarness(t, "only")
	h.subscribe("c1")

	index := filepath.Join(h.root, "only", ".git", "index")
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(index, future, future); err != nil {
		t.Fatal(err)
	}

	h.tick(3 * time.Second)
	if got := h.scan.count("only"); got != 2 {
		t.Errorf("only scanned %d times, want 2 after the index mtime moved", got)
	}
}

func TestRootWatcherSlowMark(t *testing.T) {
	h := newHarness(t, "slow", "fast")
	rootName := filepath.Base(h.root)
	h.act.set(Activity{Working: true})
	h.scan.set(func(name string) RepoSummary {
		s := cleanSummary(rootName, name, filepath.Join(h.root, name))
		if name == "slow" {
			s.Error = "git_timeout"
			s.Etag = ""
		}
		return s
	})
	h.subscribe("c1")

	h.tick(10 * time.Second) // second timeout in a row
	if repo := repoOf(t, h.summary(), "slow"); !repo.Slow {
		t.Errorf("slow repo = %+v, want Slow", repo)
	}
	if repo := repoOf(t, h.summary(), "fast"); repo.Slow {
		t.Errorf("fast repo = %+v, want not Slow", repo)
	}

	h.scan.set(func(name string) RepoSummary {
		return cleanSummary(rootName, name, filepath.Join(h.root, name))
	})
	h.tick(10 * time.Second) // a successful sweep clears the mark
	if repo := repoOf(t, h.summary(), "slow"); repo.Slow {
		t.Errorf("slow repo after success = %+v, want not Slow", repo)
	}
}

func TestRootWatcherNotifyOnlyOnChange(t *testing.T) {
	h := newHarness(t, "a")
	rootName := filepath.Base(h.root)
	h.scan.set(func(name string) RepoSummary {
		s := cleanSummary(rootName, name, filepath.Join(h.root, name))
		s.Changed = 1
		s.Etag = "e-one"
		return s
	})
	h.subscribe("c1")
	if got := h.notes.count(); got != 0 {
		t.Fatalf("notify count on first scan = %d, want 0", got)
	}

	h.scan.set(func(name string) RepoSummary {
		s := cleanSummary(rootName, name, filepath.Join(h.root, name))
		s.Changed = 2
		s.Etag = "e-two"
		return s
	})
	h.tick(3 * time.Second)
	if got := h.notes.count(); got != 1 {
		t.Fatalf("notify count = %d, want 1", got)
	}
	event := h.notes.last()
	if event.root != filepath.Clean(h.root) {
		t.Errorf("event root = %q, want %q", event.root, h.root)
	}
	if event.repos["a"] != "e-two" {
		t.Errorf("event repos = %v, want a: e-two", event.repos)
	}
	if event.etag == "" {
		t.Errorf("event etag is empty")
	}

	h.tick(3 * time.Second)
	if got := h.notes.count(); got != 1 {
		t.Errorf("notify count = %d, want still 1 when nothing changed", got)
	}
}

func TestRootWatcherSingleflight(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 3; i++ {
		initRepo(t, filepath.Join(root, fmt.Sprintf("r%d", i)))
	}
	rootName := filepath.Base(root)
	scan := newFakeScanner()
	started := make(chan struct{}, 1)
	gate := make(chan struct{})
	scan.set(func(name string) RepoSummary {
		select {
		case started <- struct{}{}:
		default:
		}
		<-gate
		return cleanSummary(rootName, name, filepath.Join(root, name))
	})
	m := newRootWatchers(NewRunner(allowPolicy(root)), func(string) Activity { return Activity{} }, func(string, string, map[string]string) {})
	m.scan = scan.scan
	t.Cleanup(m.Close)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.Summary(context.Background(), root); err != nil {
				t.Error(err)
			}
		}()
	}
	<-started
	close(gate)
	wg.Wait()

	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("r%d", i)
		if got := scan.count(name); got != 1 {
			t.Errorf("%s scanned %d times, want 1 (singleflight)", name, got)
		}
	}
}

func TestRootWatcherSubscriptionExpiry(t *testing.T) {
	h := newHarness(t, "a")
	h.subscribe("c1")
	if got := h.m.countWatchers(); got != 1 {
		t.Fatalf("watchers = %d, want 1", got)
	}

	h.tick(45 * time.Second)
	if got := h.m.countWatchers(); got != 1 {
		t.Errorf("watchers = %d after the subscription expired, want 1", got)
	}
	before := h.scan.total()
	h.tick(29 * time.Second)
	if got := h.m.countWatchers(); got != 1 {
		t.Errorf("watchers = %d, want 1 before the stop grace ends", got)
	}
	if got := h.scan.total(); got != before {
		t.Errorf("scans = %d, want %d with no subscription", got, before)
	}

	h.tick(1 * time.Second)
	if got := h.m.countWatchers(); got != 0 {
		t.Errorf("watchers = %d after the stop grace, want 0", got)
	}
}

func TestRootWatcherNoScansWithoutSubscribers(t *testing.T) {
	h := newHarness(t, "a")
	for i := 0; i < 5; i++ {
		h.tick(10 * time.Second)
	}
	if got := h.scan.total(); got != 0 {
		t.Fatalf("scans = %d with no subscriber, want 0", got)
	}

	h.subscribe("c1")
	h.m.Unsubscribe(h.root, "c1")
	before := h.scan.total()
	for i := 0; i < 5; i++ {
		h.tick(5 * time.Second)
	}
	if got := h.scan.total(); got != before {
		t.Errorf("scans = %d after unsubscribe, want %d", got, before)
	}
}

func TestRootWatcherBudget(t *testing.T) {
	root := t.TempDir()
	const repos = 30
	names := make([]string, 0, repos)
	for i := 0; i < repos; i++ {
		name := fmt.Sprintf("repo-%02d", i)
		names = append(names, name)
		initRepo(t, filepath.Join(root, name))
	}
	for i := 0; i < 5; i++ {
		writeFile(t, filepath.Join(root, names[i]), "dirty.txt", "x\n")
	}

	m := newRootWatchers(NewRunner(allowPolicy(root)), func(string) Activity { return Activity{} }, func(string, string, map[string]string) {})
	t.Cleanup(m.Close)

	start := time.Now()
	s, err := m.Summary(context.Background(), root)
	cold := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("cold summary of %d repos: %v", len(s.Repos), cold)
	if len(s.Repos) != repos {
		t.Errorf("repos = %d, want %d", len(s.Repos), repos)
	}
	if s.ChangedFiles != 5 {
		t.Errorf("changed files = %d, want 5", s.ChangedFiles)
	}
	if cold > time.Second {
		t.Errorf("cold summary = %v, want under 1 s", cold)
	}

	start = time.Now()
	if _, err := m.Summary(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	warm := time.Since(start)
	t.Logf("cached summary: %v", warm)
	if warm > 5*time.Millisecond {
		t.Errorf("cached summary = %v, want under 5 ms", warm)
	}
}

func TestRootWatchersLifecycle(t *testing.T) {
	root := t.TempDir()
	initRepo(t, filepath.Join(root, "a"))
	m := NewRootWatchers(NewRunner(allowPolicy(root)), nil, nil)
	if err := m.Subscribe(context.Background(), root, "c1"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m.Close()
}

func TestRunnerGitLimit(t *testing.T) {
	var zero Runner
	if got := cap(zero.gitSlots()); got != gitLimit {
		t.Errorf("zero Runner limit = %d, want %d", got, gitLimit)
	}
	r := NewRunner(allowPolicy(t.TempDir()))
	if got := cap(r.gitSlots()); got != gitLimit {
		t.Errorf("Runner limit = %d, want %d", got, gitLimit)
	}
}

func TestRunnerGitLimitRespectsCancel(t *testing.T) {
	dir := t.TempDir()
	r := NewRunner(allowPolicy(dir))
	slots := r.gitSlots()
	for i := 0; i < gitLimit; i++ {
		slots <- struct{}{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Git(ctx, dir, "status"); !errors.Is(err, context.Canceled) {
		t.Errorf("Git with a full limit and a cancelled ctx = %v, want context.Canceled", err)
	}
}
