package id

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	mu   sync.Mutex
	last int64
)

// New returns a 26-character, time-ordered ID (similar to a ULID).
func New() string {
	mu.Lock()
	now := time.Now().UnixMilli()
	if now <= last {
		now = last + 1
	}
	last = now
	mu.Unlock()
	var raw [16]byte
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(now))
	copy(raw[:6], ts[2:])
	if _, err := rand.Read(raw[6:]); err != nil {
		for i := 6; i < 16; i++ {
			raw[i] = byte(now >> (i * 3))
		}
	}
	// 128 bits -> 26 base32 characters (130 bits, the first two being zero).
	out := make([]byte, 26)
	var acc uint32
	bits := 0
	pos := 0
	for _, b := range raw {
		acc = (acc << 8) | uint32(b)
		bits += 8
		for bits >= 5 {
			bits -= 5
			out[pos] = alphabet[(acc>>uint(bits))&31]
			pos++
		}
	}
	if bits > 0 {
		out[pos] = alphabet[(acc<<uint(5-bits))&31]
	}
	return string(out)
}
