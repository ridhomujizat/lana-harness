package core

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// callHook invokes a hook listener and turns a panic into an error, so one bad
// listener cannot take down the agent turn goroutine that called it.
func callHook[O any](fn func() (O, error)) (out O, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero O
			out = zero
			err = fmt.Errorf("hook listener panicked: %v", r)
			slog.Error("hook listener panicked", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	return fn()
}

// Next forwards to the next listener, or to the default handler at the end of the chain.
type Next[I, O any] func(ctx context.Context, in I) (O, error)

// WaterfallListener receives the input and next.
type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

// SerialListener receives the input and returns one result.
type SerialListener[I, O any] func(ctx context.Context, in I) (O, error)

// UseOption configures listener registration.
type UseOption func(*useOptions)

type useOptions struct{ prepend bool }

// WithPrepend registers the listener before regular listeners.
func WithPrepend() UseOption { return func(o *useOptions) { o.prepend = true } }

type waterfallEntry[I, O any] struct {
	id       uint64
	listener WaterfallListener[I, O]
}

// Waterfall is sequential middleware with a default handler.
type Waterfall[I, O any] struct {
	mu        sync.RWMutex
	listeners []waterfallEntry[I, O]
	def       func(ctx context.Context, in I) (O, error)
	nextID    uint64
}

func NewWaterfall[I, O any]() *Waterfall[I, O] { return &Waterfall[I, O]{} }

// SetDefault replaces the default handler.
func (w *Waterfall[I, O]) SetDefault(h func(ctx context.Context, in I) (O, error)) {
	w.mu.Lock()
	w.def = h
	w.mu.Unlock()
}

// Use registers a listener. Release removes that listener.
func (w *Waterfall[I, O]) Use(l WaterfallListener[I, O], opts ...UseOption) (release func()) {
	var o useOptions
	for _, opt := range opts {
		opt(&o)
	}
	w.mu.Lock()
	w.nextID++
	id := w.nextID
	entry := waterfallEntry[I, O]{id: id, listener: l}
	if o.prepend {
		w.listeners = append([]waterfallEntry[I, O]{entry}, w.listeners...)
	} else {
		w.listeners = append(w.listeners, entry)
	}
	w.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			w.mu.Lock()
			next := make([]waterfallEntry[I, O], 0, len(w.listeners))
			for _, e := range w.listeners {
				if e.id != id {
					next = append(next, e)
				}
			}
			w.listeners = next
			w.mu.Unlock()
		})
	}
}

// Call runs the chain. The result of the first listener that does not call next is returned.
func (w *Waterfall[I, O]) Call(ctx context.Context, in I) (O, error) {
	w.mu.RLock()
	listeners := w.listeners
	def := w.def
	w.mu.RUnlock()

	var run func(i int, input I) (O, error)
	run = func(i int, input I) (O, error) {
		if i >= len(listeners) {
			if def == nil {
				var zero O
				return zero, ErrNoDefault
			}
			return callHook(func() (O, error) { return def(ctx, input) })
		}
		next := func(ctx context.Context, nextInput I) (O, error) {
			return run(i+1, nextInput)
		}
		return callHook(func() (O, error) { return listeners[i].listener(ctx, input, next) })
	}
	return run(0, in)
}

type serialEntry[I, O any] struct {
	id       uint64
	listener SerialListener[I, O]
}

// Serial calls all listeners in order and collects their results.
type Serial[I, O any] struct {
	mu        sync.RWMutex
	listeners []serialEntry[I, O]
	nextID    uint64
}

func NewSerial[I, O any]() *Serial[I, O] { return &Serial[I, O]{} }

// Use registers a listener. Release removes that listener.
func (s *Serial[I, O]) Use(l SerialListener[I, O]) (release func()) {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.listeners = append(s.listeners, serialEntry[I, O]{id: id, listener: l})
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			next := make([]serialEntry[I, O], 0, len(s.listeners))
			for _, e := range s.listeners {
				if e.id != id {
					next = append(next, e)
				}
			}
			s.listeners = next
			s.mu.Unlock()
		})
	}
}

// Call returns the result of each listener in order. The first error stops the chain.
func (s *Serial[I, O]) Call(ctx context.Context, in I) ([]O, error) {
	s.mu.RLock()
	listeners := s.listeners
	s.mu.RUnlock()
	out := make([]O, 0, len(listeners))
	for _, entry := range listeners {
		v, err := callHook(func() (O, error) { return entry.listener(ctx, in) })
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ObserveBuffer is the number of events per subscriber.
const ObserveBuffer = 1024

// Received is the result of Recv. Lagged > 0 means Value is not set; call Recv again.
type Received[T any] struct {
	Lagged int
	Value  T
}

// Observe sends events to all subscribers without waiting.
type Observe[T any] struct {
	mu     sync.Mutex
	subs   map[*Subscription[T]]struct{}
	closed bool
}

func NewObserve[T any]() *Observe[T] {
	return &Observe[T]{subs: make(map[*Subscription[T]]struct{})}
}

// Publish sends v to every subscriber. It never blocks.
func (o *Observe[T]) Publish(v T) {
	o.mu.Lock()
	subs := make([]*Subscription[T], 0, len(o.subs))
	closed := o.closed
	for sub := range o.subs {
		subs = append(subs, sub)
	}
	o.mu.Unlock()
	if closed {
		return
	}
	for _, sub := range subs {
		sub.push(v)
	}
}

// Subscribe creates a subscriber with a buffer of ObserveBuffer events.
func (o *Observe[T]) Subscribe() *Subscription[T] {
	sub := &Subscription[T]{
		observe:  o,
		notify:   make(chan struct{}, 1),
		closedCh: make(chan struct{}),
	}
	o.mu.Lock()
	if o.closed {
		sub.closed = true
		close(sub.closedCh)
	} else {
		o.subs[sub] = struct{}{}
	}
	o.mu.Unlock()
	return sub
}

// Close closes all subscribers.
func (o *Observe[T]) Close() {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return
	}
	o.closed = true
	subs := make([]*Subscription[T], 0, len(o.subs))
	for sub := range o.subs {
		subs = append(subs, sub)
	}
	o.subs = map[*Subscription[T]]struct{}{}
	o.mu.Unlock()
	for _, sub := range subs {
		sub.close()
	}
}

// Subscription is the reader for one subscriber.
type Subscription[T any] struct {
	mu       sync.Mutex
	observe  *Observe[T]
	buf      [ObserveBuffer]T
	head     int
	size     int
	dropped  int
	notify   chan struct{}
	closedCh chan struct{}
	closed   bool
}

func (s *Subscription[T]) push(v T) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.size == ObserveBuffer {
		s.buf[s.head] = v
		s.head = (s.head + 1) % ObserveBuffer
		s.dropped++
	} else {
		idx := (s.head + s.size) % ObserveBuffer
		s.buf[idx] = v
		s.size++
	}
	s.mu.Unlock()
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Recv waits for the next event. It returns ErrClosed after Close, or ctx.Err().
func (s *Subscription[T]) Recv(ctx context.Context) (Received[T], error) {
	for {
		s.mu.Lock()
		if s.dropped > 0 {
			n := s.dropped
			s.dropped = 0
			s.mu.Unlock()
			return Received[T]{Lagged: n}, nil
		}
		if s.size > 0 {
			v := s.buf[s.head]
			s.head = (s.head + 1) % ObserveBuffer
			s.size--
			s.mu.Unlock()
			return Received[T]{Value: v}, nil
		}
		closed := s.closed
		s.mu.Unlock()
		if closed {
			var zero T
			return Received[T]{Value: zero}, ErrClosed
		}
		select {
		case <-ctx.Done():
			var zero T
			return Received[T]{Value: zero}, ctx.Err()
		case <-s.notify:
		case <-s.closedCh:
		}
	}
}

// Len returns the number of unread events.
func (s *Subscription[T]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.size
}

// Close releases the subscriber.
func (s *Subscription[T]) Close() { s.close() }

func (s *Subscription[T]) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.closedCh)
	if s.observe != nil {
		s.observe.mu.Lock()
		delete(s.observe.subs, s)
		s.observe.mu.Unlock()
	}
}

// Hooks is the set of typed hooks. It is created by Boot.
type Hooks struct {
	AgentPreStep      *Waterfall[PreStepInput, PreStepDecision]
	AgentRequest      *Waterfall[RequestControl, RequestControl]
	LLMStream         *Waterfall[LLMStreamInput, ChatStream]
	ToolsPreExecute   *Waterfall[ToolsPreExecuteInput, ToolDecision]
	ToolsExecute      *Waterfall[ToolsExecuteInput, ToolOutput]
	ToolsPostExecute  *Waterfall[ToolsPostExecuteInput, ToolOutput]
	ApprovalRequest   *Waterfall[ApprovalRequest, ApprovalOutcome]
	QuestionAsk       *Waterfall[QuestionRequest, QuestionOutcome]
	AgentTurnStopping *Serial[TurnStopInput, StopVote]
	SessionEvent      *Observe[SessionEvent]
}

// NewHooks creates an empty set of hooks, ready to use.
func NewHooks() *Hooks { return newHooks() }

func newHooks() *Hooks {
	return &Hooks{
		AgentPreStep:      NewWaterfall[PreStepInput, PreStepDecision](),
		AgentRequest:      NewWaterfall[RequestControl, RequestControl](),
		LLMStream:         NewWaterfall[LLMStreamInput, ChatStream](),
		ToolsPreExecute:   NewWaterfall[ToolsPreExecuteInput, ToolDecision](),
		ToolsExecute:      NewWaterfall[ToolsExecuteInput, ToolOutput](),
		ToolsPostExecute:  NewWaterfall[ToolsPostExecuteInput, ToolOutput](),
		ApprovalRequest:   NewWaterfall[ApprovalRequest, ApprovalOutcome](),
		QuestionAsk:       NewWaterfall[QuestionRequest, QuestionOutcome](),
		AgentTurnStopping: NewSerial[TurnStopInput, StopVote](),
		SessionEvent:      NewObserve[SessionEvent](),
	}
}
