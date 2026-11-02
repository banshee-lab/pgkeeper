package events

import (
	"sync"
	"sync/atomic"
)

const subBuffer = 256

type subscriber[T any] struct {
	ch     chan T
	filter func(T) bool
}

// Hub is an in-memory pub/sub. Publish never blocks: slow subscribers lose events.
type Hub[T any] struct {
	mu      sync.RWMutex
	subs    map[uint64]*subscriber[T]
	nextID  uint64
	dropped atomic.Uint64
}

func NewHub[T any]() *Hub[T] { return &Hub[T]{subs: make(map[uint64]*subscriber[T])} }

// Subscribe returns a channel and an idempotent cancel func that closes it.
func (h *Hub[T]) Subscribe(filter func(T) bool) (<-chan T, func()) {
	s := &subscriber[T]{ch: make(chan T, subBuffer), filter: filter}
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	h.subs[id] = s
	h.mu.Unlock()

	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, id)
			h.mu.Unlock()
			close(s.ch) // safe: Publish holds RLock while sending
		})
	}
}

// Publish is nil-safe so callers/tests may pass a nil hub.
func (h *Hub[T]) Publish(v T) {
	if h == nil {
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.subs {
		if s.filter != nil && !s.filter(v) {
			continue
		}
		select {
		case s.ch <- v:
		default:
			h.dropped.Add(1)
		}
	}
}

func (h *Hub[T]) Dropped() uint64 { return h.dropped.Load() }