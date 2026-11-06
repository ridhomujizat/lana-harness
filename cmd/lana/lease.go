package main

import (
	"context"
	"sync"

	"github.com/ridhomujizat/lana-harness"
)

// leaseSet holds the session leases taken by this process (spec #1 session lock).
type leaseSet struct {
	rt   *lana.Runtime
	mu   sync.Mutex
	held map[string]func() error
}

func newLeaseSet(rt *lana.Runtime) *leaseSet {
	return &leaseSet{rt: rt, held: map[string]func() error{}}
}

// ensure takes the lease of sessionID unless this process already holds it.
func (l *leaseSet) ensure(ctx context.Context, sessionID string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.held[sessionID]; ok {
		return nil
	}
	release, err := l.rt.LeaseSession(ctx, sessionID)
	if err != nil {
		return err
	}
	l.held[sessionID] = release
	return nil
}

// holds reports whether this process holds the session lease.
func (l *leaseSet) holds(sessionID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.held[sessionID]
	return ok
}

// releaseAll releases every held lease.
func (l *leaseSet) releaseAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, release := range l.held {
		_ = release()
		delete(l.held, id)
	}
}
