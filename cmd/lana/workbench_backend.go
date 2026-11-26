package main

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/web"
)

// workbenchProbeTimeout bounds the liveness probe that decides whether a failed
// call means the core connection broke.
const workbenchProbeTimeout = 2 * time.Second

// workbenchBackend satisfies web.WorkbenchBackend against the Workbench core.
// The core is started lazily on the first API call (REQ-WB-002) and is never
// stopped here, so its sessions survive a daemon stop or upgrade (REQ-WB-003).
type workbenchBackend struct {
	home string

	mu     sync.Mutex
	client *workbench.Client
	// onLive, when set, is called with the client each time the backend
	// installs a live one. The Workbench notification watcher uses it to
	// subscribe to a core the browser started (spec 55 A5).
	onLive func(*workbench.Client)

	// daemonVersion and daemonCommit are the daemon's build, which a core is
	// compared with (spec 84 REQ-WBR-002).
	daemonVersion, daemonCommit string
	// restarting is set while RestartCore runs (spec 84 REQ-WBR-003).
	restarting atomic.Bool
	// restartCore replaces the core; tests swap it.
	restartCore func(ctx context.Context, home string) (*workbench.Client, error)
}

var (
	_ web.WorkbenchBackend     = (*workbenchBackend)(nil)
	_ web.WorkbenchCoreControl = (*workbenchBackend)(nil)
)

func newWorkbenchBackend(home string) *workbenchBackend {
	return &workbenchBackend{
		home:          home,
		daemonVersion: version,
		daemonCommit:  commit,
		restartCore:   workbench.RestartCore,
	}
}

// CoreInfo reports the running core and whether it is older than the daemon
// (spec 84 REQ-WBR-002). It dials a connection of its own and never starts a
// core: with none answering, running is false.
func (b *workbenchBackend) CoreInfo(ctx context.Context) (web.WorkbenchCore, error) {
	client, err := workbench.Dial(ctx, b.home)
	if err != nil {
		return web.NewWorkbenchCore(false, "", "", b.daemonVersion, b.daemonCommit, 0), nil
	}
	defer func() { _ = client.Close() }()
	return b.describe(ctx, client)
}

// describe reads a core's Hello and its pane count.
func (b *workbenchBackend) describe(ctx context.Context, client *workbench.Client) (web.WorkbenchCore, error) {
	h, err := client.Hello(ctx)
	if err != nil {
		return web.WorkbenchCore{}, err
	}
	list, err := client.List(ctx)
	if err != nil {
		return web.WorkbenchCore{}, err
	}
	return web.NewWorkbenchCore(true, h.AppVersion, h.Commit, b.daemonVersion, b.daemonCommit, len(list)), nil
}

// RestartCore stops the core and starts a new one from the daemon's binary
// (spec 84 REQ-WBR-003). It holds the client lock throughout, so the other
// Workbench calls wait for the new core instead of starting one of their own;
// a second restart meanwhile is refused.
func (b *workbenchBackend) RestartCore(ctx context.Context) (web.WorkbenchCore, error) {
	if !b.restarting.CompareAndSwap(false, true) {
		return web.WorkbenchCore{}, web.ErrWorkbenchRestartRunning
	}
	defer b.restarting.Store(false)

	b.mu.Lock()
	old := b.client
	b.client = nil
	client, err := b.restartCore(ctx, b.home)
	if err == nil {
		b.client = client
	}
	hook := b.onLive
	b.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	if err != nil {
		return web.WorkbenchCore{}, err
	}
	if hook != nil {
		hook(client)
	}
	return b.describe(ctx, client)
}

// setOnLive installs the hook the backend calls with a live client (spec 55
// A5).
func (b *workbenchBackend) setOnLive(hook func(*workbench.Client)) {
	b.mu.Lock()
	b.onLive = hook
	b.mu.Unlock()
}

// conn returns a connected client, starting the core on first use.
func (b *workbenchBackend) conn(ctx context.Context) (*workbench.Client, error) {
	b.mu.Lock()
	if b.client != nil {
		client := b.client
		b.mu.Unlock()
		return client, nil
	}
	client, err := workbench.Ensure(ctx, b.home)
	if err != nil {
		b.mu.Unlock()
		return nil, err
	}
	b.client = client
	hook := b.onLive
	b.mu.Unlock()
	if hook != nil {
		hook(client)
	}
	return client, nil
}

// replace drops old and Ensures a fresh client; it keeps a client another
// goroutine installed meanwhile.
func (b *workbenchBackend) replace(ctx context.Context, old *workbench.Client) (*workbench.Client, error) {
	b.mu.Lock()
	if b.client == old {
		b.client = nil
	}
	b.mu.Unlock()
	return b.conn(ctx)
}

// stale reports whether a failed call was caused by a broken connection rather
// than a core-level error: a Hello on the same client fails only when the core
// is gone (it restarted). It never retries a plain core error.
func (b *workbenchBackend) stale(ctx context.Context, client *workbench.Client) bool {
	if ctx.Err() != nil {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, workbenchProbeTimeout)
	defer cancel()
	_, err := client.Hello(probeCtx)
	return err != nil
}

// do runs op against the current client. When the call fails because the
// connection broke, it Ensures a fresh client once (the core restarted) and
// retries the operation.
func workbenchDo[T any](b *workbenchBackend, ctx context.Context, op func(*workbench.Client) (T, error)) (T, error) {
	var zero T
	client, err := b.conn(ctx)
	if err != nil {
		return zero, err
	}
	out, err := op(client)
	if err == nil {
		return out, nil
	}
	if !b.stale(ctx, client) {
		return out, err
	}
	client, err = b.replace(ctx, client)
	if err != nil {
		return zero, err
	}
	return op(client)
}

func (b *workbenchBackend) List(ctx context.Context) ([]workbench.Session, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) ([]workbench.Session, error) {
		return c.List(ctx)
	})
}

func (b *workbenchBackend) Get(ctx context.Context, id string) (workbench.Session, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (workbench.Session, error) {
		return c.Get(ctx, id)
	})
}

func (b *workbenchBackend) Create(ctx context.Context, opts workbench.CreateOptions) (workbench.Session, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (workbench.Session, error) {
		return c.Create(ctx, opts)
	})
}

func (b *workbenchBackend) Kill(ctx context.Context, id string) error {
	_, err := workbenchDo(b, ctx, func(c *workbench.Client) (struct{}, error) {
		return struct{}{}, c.Kill(ctx, id)
	})
	return err
}

func (b *workbenchBackend) Restart(ctx context.Context, id string) (workbench.Session, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (workbench.Session, error) {
		return c.Restart(ctx, id)
	})
}

func (b *workbenchBackend) SwitchAccount(ctx context.Context, id, agent, configDir string) (workbench.Session, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (workbench.Session, error) {
		return c.SwitchAccount(ctx, id, agent, configDir)
	})
}

func (b *workbenchBackend) Resize(ctx context.Context, id string, cols, rows int) error {
	_, err := workbenchDo(b, ctx, func(c *workbench.Client) (struct{}, error) {
		return struct{}{}, c.Resize(ctx, id, cols, rows)
	})
	return err
}

func (b *workbenchBackend) Layout(ctx context.Context) (json.RawMessage, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (json.RawMessage, error) {
		return c.Layout(ctx)
	})
}

func (b *workbenchBackend) SetLayout(ctx context.Context, layout json.RawMessage) error {
	_, err := workbenchDo(b, ctx, func(c *workbench.Client) (struct{}, error) {
		return struct{}{}, c.SetLayout(ctx, layout)
	})
	return err
}

func (b *workbenchBackend) Attach(ctx context.Context, id, mode string) (io.ReadWriteCloser, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (io.ReadWriteCloser, error) {
		return c.Attach(ctx, id, mode)
	})
}

func (b *workbenchBackend) Subscribe(ctx context.Context) (<-chan workbench.Event, error) {
	return workbenchDo(b, ctx, func(c *workbench.Client) (<-chan workbench.Event, error) {
		return c.Subscribe(ctx)
	})
}
