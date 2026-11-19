package workbench

import "sync"

// ringMark is a pty size that took effect at off bytes into the ring buffer.
type ringMark struct {
	off        int
	cols, rows int
}

// ring is the last max bytes written to it, for the replay an attach gets. It
// also records the pty size changes among those bytes, so a client can replay
// each byte at the size it was produced at: baseCols and baseRows are the size
// in effect at the first retained byte (baseCols is 0 while that is unknown),
// and marks lists the size changes that took effect after it.
type ring struct {
	mu       sync.Mutex
	buf      []byte
	max      int
	baseCols int
	baseRows int
	marks    []ringMark
}

func newRing(max int) *ring { return &ring{max: max} }

// write appends p and drops what falls off the front, along with the marks the
// dropped bytes carried.
func (r *ring) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) <= r.max {
		return
	}
	over := len(r.buf) - r.max
	// Drop the marks before the new start; the last one dropped is the size in
	// effect at the first retained byte, so it becomes the base.
	drop := 0
	for drop < len(r.marks) && r.marks[drop].off < over {
		drop++
	}
	if drop > 0 {
		last := r.marks[drop-1]
		r.baseCols, r.baseRows = last.cols, last.rows
		r.marks = append(r.marks[:0], r.marks[drop:]...)
	}
	for i := range r.marks {
		r.marks[i].off -= over
	}
	n := copy(r.buf, r.buf[over:])
	r.buf = r.buf[:n]
}

// mark records that the size is cols×rows from the current end of the buffer
// on. A mark equal to the size already in effect adds nothing, and a mark at the
// same offset as the last one replaces it.
func (r *ring) mark(cols, rows int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, w, ok := r.effectiveLocked(); ok && c == cols && w == rows {
		return
	}
	off := len(r.buf)
	if n := len(r.marks); n > 0 && r.marks[n-1].off == off {
		r.marks[n-1].cols, r.marks[n-1].rows = cols, rows
		return
	}
	r.marks = append(r.marks, ringMark{off: off, cols: cols, rows: rows})
}

// effectiveLocked is the size in effect at the end of the buffer, and whether it
// is known. The last mark wins; with no marks the base does.
func (r *ring) effectiveLocked() (cols, rows int, ok bool) {
	if n := len(r.marks); n > 0 {
		return r.marks[n-1].cols, r.marks[n-1].rows, true
	}
	if r.baseCols > 0 {
		return r.baseCols, r.baseRows, true
	}
	return 0, 0, false
}

// snapshot returns the replay: the base size marker when the size is known, the
// bytes with each mark's size marker inserted at its offset, and the replay-end
// marker as the very last bytes.
func (r *ring) snapshot() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, 0, len(r.buf)+len(r.marks)*16+len(replayEndMarker))
	if r.baseCols > 0 {
		out = append(out, sizeMarker(r.baseCols, r.baseRows)...)
	}
	prev := 0
	for _, m := range r.marks {
		out = append(out, r.buf[prev:m.off]...)
		out = append(out, sizeMarker(m.cols, m.rows)...)
		prev = m.off
	}
	out = append(out, r.buf[prev:]...)
	return append(out, replayEndMarker...)
}
