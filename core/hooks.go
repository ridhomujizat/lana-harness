package core

import (
	"context"
	"sync"
)

// Next meneruskan ke listener berikutnya, atau ke handler default di ujung rantai.
type Next[I, O any] func(ctx context.Context, in I) (O, error)

// WaterfallListener menerima input dan next.
type WaterfallListener[I, O any] func(ctx context.Context, in I, next Next[I, O]) (O, error)

// SerialListener menerima input dan mengembalikan satu hasil.
type SerialListener[I, O any] func(ctx context.Context, in I) (O, error)

// UseOption mengatur registrasi listener.
type UseOption func(*useOptions)

type useOptions struct{ prepend bool }

// WithPrepend mendaftarkan listener sebelum listener biasa.
func WithPrepend() UseOption { return func(o *useOptions) { o.prepend = true } }

type waterfallEntry[I, O any] struct {
	id       uint64
	listener WaterfallListener[I, O]
}

// Waterfall adalah middleware berurutan dengan handler default.
type Waterfall[I, O any] struct {
	mu        sync.RWMutex
	listeners []waterfallEntry[I, O]
	def       func(ctx context.Context, in I) (O, error)
	nextID    uint64
}

func NewWaterfall[I, O any]() *Waterfall[I, O] { return &Waterfall[I, O]{} }

// SetDefault mengganti handler default.
func (w *Waterfall[I, O]) SetDefault(h func(ctx context.Context, in I) (O, error)) {
	w.mu.Lock()
	w.def = h
	w.mu.Unlock()
}

// Use mendaftarkan listener. Release menghapus listener itu.
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

// Call menjalankan rantai. Hasil listener pertama yang tidak memanggil next dikembalikan.
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
			return def(ctx, input)
		}
		next := func(ctx context.Context, nextInput I) (O, error) {
			return run(i+1, nextInput)
		}
		return listeners[i].listener(ctx, input, next)
	}
	return run(0, in)
}

type serialEntry[I, O any] struct {
	id       uint64
	listener SerialListener[I, O]
}

// Serial memanggil semua listener berurutan dan mengumpulkan hasilnya.
type Serial[I, O any] struct {
	mu        sync.RWMutex
	listeners []serialEntry[I, O]
	nextID    uint64
}

func NewSerial[I, O any]() *Serial[I, O] { return &Serial[I, O]{} }

// Use mendaftarkan listener. Release menghapus listener itu.
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

// Call mengembalikan hasil per listener sesuai urutan. Error pertama menghentikan rantai.
func (s *Serial[I, O]) Call(ctx context.Context, in I) ([]O, error) {
	s.mu.RLock()
	listeners := s.listeners
	s.mu.RUnlock()
	out := make([]O, 0, len(listeners))
	for _, entry := range listeners {
		v, err := entry.listener(ctx, in)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// ObserveBuffer adalah jumlah event per subscriber.
const ObserveBuffer = 1024

// Received adalah hasil Recv. Lagged > 0 berarti Value belum terisi; panggil Recv lagi.
type Received[T any] struct {
	Lagged int
	Value  T
}

// Observe mengirim event ke semua subscriber tanpa menunggu.
type Observe[T any] struct {
	mu     sync.Mutex
	subs   map[*Subscription[T]]struct{}
	closed bool
}

func NewObserve[T any]() *Observe[T] {
	return &Observe[T]{subs: make(map[*Subscription[T]]struct{})}
}

// Publish mengirim v ke setiap subscriber. Tidak pernah memblokir.
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

// Subscribe membuat subscriber dengan buffer ObserveBuffer.
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

// Close menutup semua subscriber.
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

// Subscription adalah pembaca untuk satu subscriber.
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

// Recv menunggu event berikutnya. Error ErrClosed setelah Close, atau ctx.Err().
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

// Close melepas subscriber.
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

// Hooks adalah kumpulan hook bertipe. Dibuat oleh Boot.
type Hooks struct {
	AgentPreStep      *Waterfall[PreStepInput, PreStepDecision]
	AgentRequest      *Waterfall[RequestControl, RequestControl]
	LLMStream         *Waterfall[LLMStreamInput, ChatStream]
	ToolsPreExecute   *Waterfall[ToolsPreExecuteInput, ToolDecision]
	ToolsExecute      *Waterfall[ToolsExecuteInput, ToolOutput]
	ToolsPostExecute  *Waterfall[ToolsPostExecuteInput, ToolOutput]
	ApprovalRequest   *Waterfall[ApprovalRequest, ApprovalOutcome]
	AgentTurnStopping *Serial[TurnStopInput, StopVote]
	SessionEvent      *Observe[SessionEvent]
}

func newHooks() *Hooks {
	return &Hooks{
		AgentPreStep:      NewWaterfall[PreStepInput, PreStepDecision](),
		AgentRequest:      NewWaterfall[RequestControl, RequestControl](),
		LLMStream:         NewWaterfall[LLMStreamInput, ChatStream](),
		ToolsPreExecute:   NewWaterfall[ToolsPreExecuteInput, ToolDecision](),
		ToolsExecute:      NewWaterfall[ToolsExecuteInput, ToolOutput](),
		ToolsPostExecute:  NewWaterfall[ToolsPostExecuteInput, ToolOutput](),
		ApprovalRequest:   NewWaterfall[ApprovalRequest, ApprovalOutcome](),
		AgentTurnStopping: NewSerial[TurnStopInput, StopVote](),
		SessionEvent:      NewObserve[SessionEvent](),
	}
}
