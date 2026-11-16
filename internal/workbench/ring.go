package workbench

import "sync"

// ring is the last max bytes written to it, for the replay an attach gets.
type ring struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func newRing(max int) *ring { return &ring{max: max} }

// write appends p and drops what falls off the front.
func (r *ring) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		n := copy(r.buf, r.buf[len(r.buf)-r.max:])
		r.buf = r.buf[:n]
	}
}

// snapshot returns a copy of the current buffer.
func (r *ring) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...)
}
