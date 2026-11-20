package lana

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/coder/websocket"
)

// closeCodeNotFound is the attach close code for a session that is gone: the
// WebSocket private range (web/workbench.go).
const closeCodeNotFound = websocket.StatusCode(4404)

// Attachment is one attached session (REQ-DW-040): the daemon sends terminal
// output as binary frames and reads input as binary frames, and a size change
// is a text frame.
type Attachment struct {
	conn *websocket.Conn
	ctx  context.Context
}

// Attach opens the control-mode WebSocket of one session (REQ-DW-040). The
// handshake carries the session cookie from the jar and Origin: http://<addr>,
// as REQ-WB-041 requires. A 401 handshake signs in again once.
func (c *Local) Attach(ctx context.Context, id string) (*Attachment, error) {
	target := c.base + sessionPath(id) + "/attach?mode=" + url.QueryEscape(AttachModeControl)
	conn, err := c.dialAttach(ctx, target, true)
	if err != nil {
		return nil, err
	}
	return &Attachment{conn: conn, ctx: ctx}, nil
}

// dialAttach performs the handshake, retrying once after a re-sign-in when the
// daemon answers 401.
func (c *Local) dialAttach(ctx context.Context, target string, retry bool) (*websocket.Conn, error) {
	conn, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{
		HTTPClient: c.client,
		HTTPHeader: http.Header{headerOrigin: {c.base}},
	})
	if err == nil {
		return conn, nil
	}
	if retry && resp != nil && resp.StatusCode == http.StatusUnauthorized {
		if err := c.signIn(ctx); err != nil {
			return nil, err
		}
		return c.dialAttach(ctx, target, false)
	}
	return nil, c.handshakeError(resp, err)
}

// handshakeError maps a failed handshake: the daemon's own answer when it
// refused before upgrading, otherwise a transport error.
func (c *Local) handshakeError(resp *http.Response, err error) error {
	if resp != nil && resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		return newAPIError(resp.StatusCode, body)
	}
	return c.transportError(err)
}

// Read returns the next chunk of terminal output. It reports
// ErrSessionNotFound when the daemon closed the attachment with code 4404.
func (a *Attachment) Read() ([]byte, error) {
	_, data, err := a.conn.Read(a.ctx)
	if err != nil {
		return nil, mapCloseError(err)
	}
	return data, nil
}

// Write sends input bytes to the session as a binary frame.
func (a *Attachment) Write(data []byte) error {
	return a.conn.Write(a.ctx, websocket.MessageBinary, data)
}

// Resize sends a size change as a text frame:
// {"type":"resize","cols":N,"rows":N}.
func (a *Attachment) Resize(cols, rows int) error {
	message, err := json.Marshal(struct {
		Type string `json:"type"`
		Cols int    `json:"cols"`
		Rows int    `json:"rows"`
	}{Type: "resize", Cols: cols, Rows: rows})
	if err != nil {
		return err
	}
	return a.conn.Write(a.ctx, websocket.MessageText, message)
}

// Close closes the attachment with a normal closure and unblocks Read and
// Write. It is safe to call more than once.
func (a *Attachment) Close() error {
	return a.conn.Close(websocket.StatusNormalClosure, "")
}

// mapCloseError turns the daemon's 4404 close into ErrSessionNotFound.
func mapCloseError(err error) error {
	if websocket.CloseStatus(err) == closeCodeNotFound {
		return fmt.Errorf("%w: the daemon closed the attachment", ErrSessionNotFound)
	}
	return err
}
