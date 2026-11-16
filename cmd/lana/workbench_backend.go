package main

import (
	"context"
	"encoding/json"
	"io"
	"sync"
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
}

var _ web.WorkbenchBackend = (*workbenchBackend)(nil)

func newWorkbenchBackend(home string) *workbenchBackend {
	return &workbenchBackend{home: home}
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
