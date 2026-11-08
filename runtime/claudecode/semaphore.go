package claudecode

import (
	"container/list"
	"context"
	"sync"
)

// fifoSem is a counting semaphore whose waiters are served in arrival order.
// A waiter whose context ends leaves the queue.
type fifoSem struct {
	mu      sync.Mutex
	max     int
	held    int
	waiters list.List // of chan struct{}
}

func newFIFOSem(max int) *fifoSem {
	if max < 1 {
		max = 1
	}
	return &fifoSem{max: max}
}

// Acquire takes a slot, waiting in FIFO order. It returns ctx.Err() when ctx
// ends first; the slot is then not held.
func (s *fifoSem) Acquire(ctx context.Context) error {
	s.mu.Lock()
	if s.held < s.max && s.waiters.Len() == 0 {
		s.held++
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	el := s.waiters.PushBack(ch)
	s.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		select {
		case <-ch:
			// The slot was handed over while ctx ended: give it back.
			s.mu.Unlock()
			s.Release()
		default:
			s.waiters.Remove(el)
			s.mu.Unlock()
		}
		return ctx.Err()
	}
}

// Release frees a slot, handing it to the oldest waiter if there is one.
func (s *fifoSem) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if front := s.waiters.Front(); front != nil {
		s.waiters.Remove(front)
		close(front.Value.(chan struct{}))
		return
	}
	if s.held > 0 {
		s.held--
	}
}

// Counts returns the held slots and the waiting callers.
func (s *fifoSem) Counts() (running, queued int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held, s.waiters.Len()
}
