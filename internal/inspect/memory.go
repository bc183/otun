package inspect

import (
	"context"
	"fmt"
	"sync"
)

// MemoryStore is an in-memory ring buffer Store. Oldest records are dropped
// when capacity is reached.
type MemoryStore struct {
	cap int

	mu      sync.RWMutex
	records []*Record // newest appended at end; length ≤ cap
	byID    map[string]*Record

	subsMu sync.Mutex
	subs   map[int]chan *Record
	nextID int
}

// NewMemoryStore returns a MemoryStore with the given capacity. Capacities
// below 1 are clamped to 1.
func NewMemoryStore(capacity int) *MemoryStore {
	if capacity < 1 {
		capacity = 1
	}
	return &MemoryStore{
		cap:     capacity,
		records: make([]*Record, 0, capacity),
		byID:    make(map[string]*Record, capacity),
		subs:    make(map[int]chan *Record),
	}
}

// Add inserts a record and evicts the oldest if at capacity.
func (m *MemoryStore) Add(_ context.Context, r *Record) error {
	m.mu.Lock()
	if len(m.records) == m.cap {
		old := m.records[0]
		m.records = m.records[1:]
		delete(m.byID, old.ID)
	}
	m.records = append(m.records, r)
	m.byID[r.ID] = r
	m.mu.Unlock()

	m.broadcast(r)
	return nil
}

// List returns records newest-first, respecting the filter and cursor.
func (m *MemoryStore) List(_ context.Context, f Filter) ([]*Record, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*Record, 0, limit)
	for i := len(m.records) - 1; i >= 0; i-- {
		r := m.records[i]
		if f.Cursor != "" && r.ID >= f.Cursor {
			continue
		}
		if !f.matches(r) {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Get returns a record by ID or an error if not found.
func (m *MemoryStore) Get(_ context.Context, id string) (*Record, error) {
	m.mu.RLock()
	r, ok := m.byID[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("record %q not found", id)
	}
	return r, nil
}

// Subscribe registers a listener. The returned channel has a small buffer;
// if it fills, records are dropped for that subscriber.
func (m *MemoryStore) Subscribe() (<-chan *Record, func()) {
	ch := make(chan *Record, 16)
	m.subsMu.Lock()
	id := m.nextID
	m.nextID++
	m.subs[id] = ch
	m.subsMu.Unlock()

	unsub := func() {
		m.subsMu.Lock()
		if c, ok := m.subs[id]; ok {
			delete(m.subs, id)
			close(c)
		}
		m.subsMu.Unlock()
	}
	return ch, unsub
}

func (m *MemoryStore) broadcast(r *Record) {
	m.subsMu.Lock()
	defer m.subsMu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- r:
		default:
			// Slow consumer — drop.
		}
	}
}

// Close closes all subscriber channels.
func (m *MemoryStore) Close() error {
	m.subsMu.Lock()
	for id, ch := range m.subs {
		delete(m.subs, id)
		close(ch)
	}
	m.subsMu.Unlock()
	return nil
}
