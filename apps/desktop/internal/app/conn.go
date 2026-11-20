// Package app is the state and the views of the native Workbench window
// (spec 58). Its view draws the panel, the tab strip and the panes of the
// shared layout document (REQ-DW-020) over a connection to the daemon
// (REQ-DW-010).
package app

import (
	"context"
	"encoding/json"

	"github.com/ridhomujizat/lana-harness/apps/desktop/internal/lana"
)

// Conn is the part of a daemon connection the app uses (REQ-DW-010,
// REQ-DW-014). *lana.Local is the implementation of the local daemon; a
// remote one can be added later without changing the views. Tests use a
// fake, so they need no daemon.
type Conn interface {
	// BaseURL is the daemon address the connection talks to.
	BaseURL() string

	ListSessions(ctx context.Context) ([]lana.Session, error)
	CreateSession(ctx context.Context, opts lana.CreateOptions) (lana.Session, error)
	KillSession(ctx context.Context, id string) error
	RestartSession(ctx context.Context, id string) (lana.Session, error)
	Resize(ctx context.Context, id string, cols, rows int) error
	GetLayout(ctx context.Context) (json.RawMessage, error)
	PutLayout(ctx context.Context, layout json.RawMessage) error
	// Preview resolves and classifies a local file link (REQ-WP-040).
	Preview(ctx context.Context, session, link, dir string) (lana.PreviewFile, error)
	// Attach opens the control-mode attachment of one session
	// (REQ-DW-040).
	Attach(ctx context.Context, id string) (*lana.Attachment, error)
	// Subscribe reads the event stream, calling onEvent for each event
	// until ctx ends or the stream fails.
	Subscribe(ctx context.Context, onEvent func(lana.Event)) error
}

var _ Conn = (*lana.Local)(nil)

// Dialer opens a connection to a Lana home (REQ-DW-011). It is a seam so
// tests can drive the app without a daemon.
type Dialer func(ctx context.Context, home string) (Conn, error)

// DialLocal connects to the daemon on this machine: it reads
// <home>/daemon.secret and signs in, the way Lana.app does.
func DialLocal(ctx context.Context, home string) (Conn, error) {
	c := lana.NewLocal(home)
	if err := c.Connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
