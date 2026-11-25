package antigravity

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// maxLineBytes caps one ACP stdout line. Longer lines are dropped.
const maxLineBytes = 10 << 20

// errProcessExited is returned to a caller waiting for a response when the
// child exited first.
var errProcessExited = errors.New("antigravity process exited")

// rpcEnvelope is the part every ACP line shares. ID is kept raw so an agent
// request's string id ("1") is echoed back unchanged.
type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	if len(e.Data) > 0 {
		return fmt.Sprintf("%s (%s)", e.Message, string(e.Data))
	}
	return e.Message
}

// rpcResponse is what the reader hands a waiting call.
type rpcResponse struct {
	result json.RawMessage
	err    error
}

// nextID returns the next integer request id of the process.
func (p *process) nextID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	return strconv.Itoa(p.seq)
}

// call sends a JSON-RPC request and waits for its response. A JSON-RPC error
// becomes an *rpcError.
func (p *process) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := p.nextID()
	ch := make(chan rpcResponse, 1)
	p.mu.Lock()
	p.pending[id] = ch
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
	}()
	if err := p.write(rpcRequestLine(json.RawMessage(id), method, params)); err != nil {
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.result, nil
	case <-p.done:
		return nil, errProcessExited
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification (no id, no response).
func (p *process) notify(method string, params any) error {
	return p.write(rpcNotificationLine(method, params))
}

// readLoop reads one JSON-RPC object per stdout line and dispatches it.
func (p *process) readLoop(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := readLine(r)
		if len(bytes.TrimSpace(line)) > 0 {
			p.dispatch(line)
		}
		if err != nil {
			return
		}
	}
}

// dispatch routes a stdout line: a response to its waiting call; a notification
// or an agent request to the running turn (dropped while no session is ready,
// so a load's replayed history is not emitted).
func (p *process) dispatch(line []byte) {
	var env rpcEnvelope
	if json.Unmarshal(line, &env) != nil {
		return
	}
	if env.Method == "" && len(env.ID) > 0 {
		key := string(env.ID)
		p.mu.Lock()
		ch := p.pending[key]
		delete(p.pending, key)
		p.mu.Unlock()
		if ch != nil {
			ch <- rpcResponse{result: env.Result, err: envError(env)}
		}
		return
	}
	p.mu.Lock()
	t := p.turn
	ready := p.ready
	p.mu.Unlock()
	if t == nil || !ready {
		return
	}
	select {
	case t.lines <- line:
	case <-t.done:
	}
}

// envError returns a response's error, or nil.
func envError(env rpcEnvelope) error {
	if env.Error != nil {
		return env.Error
	}
	return nil
}

// readLine reads one line without its newline. A line over maxLineBytes is
// dropped and an empty line returned.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	tooLong := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(buf)+len(chunk) > maxLineBytes {
				tooLong, buf = true, nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if tooLong {
			return nil, err
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// --- lines lana writes ---

func rpcRequestLine(id json.RawMessage, method string, params any) []byte {
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	return mustLine(msg)
}

func rpcNotificationLine(method string, params any) []byte {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return mustLine(msg)
}

func rpcResultLine(id json.RawMessage, result any) []byte {
	return mustLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func rpcErrorLine(id json.RawMessage, code int, message string) []byte {
	return mustLine(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func mustLine(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		// Only maps of strings and raw JSON reach here.
		panic("antigravity: marshal: " + err.Error())
	}
	return append(b, '\n')
}

// --- stderr ---

// tailBuffer keeps the last lines of a stream for error messages.
type tailBuffer struct {
	mu    sync.Mutex
	max   int
	lines []string
}

func newTailBuffer(max int) *tailBuffer { return &tailBuffer{max: max} }

func (b *tailBuffer) readFrom(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if len(line) > 2048 {
			line = line[:2048]
		}
		b.mu.Lock()
		b.lines = append(b.lines, line)
		if len(b.lines) > b.max {
			b.lines = b.lines[len(b.lines)-b.max:]
		}
		b.mu.Unlock()
	}
	_, _ = io.Copy(io.Discard, r)
}

func (b *tailBuffer) lastLine() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.lines) == 0 {
		return ""
	}
	return b.lines[len(b.lines)-1]
}
