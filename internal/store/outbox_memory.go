package store

import (
	"context"
	"sync"
	"time"
)

// MemoryOutbox is the outbox used when Mongo is not configured. It is
// per-process, so a held message is lost if the instance that held it is
// stopped before the recipient writes again.
type MemoryOutbox struct {
	mu   sync.Mutex
	seen map[string]time.Time
	held []HeldMessage
}

var _ Outbox = (*MemoryOutbox)(nil)

func NewMemoryOutbox() *MemoryOutbox {
	return &MemoryOutbox{seen: make(map[string]time.Time)}
}

func (m *MemoryOutbox) Seen(_ context.Context, number string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if at.After(m.seen[number]) {
		m.seen[number] = at
	}
	return nil
}

func (m *MemoryOutbox) LastSeen(_ context.Context, number string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.seen[number], nil
}

func (m *MemoryOutbox) Hold(_ context.Context, msg HeldMessage) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.held = append(m.held, msg)
	return m.pending(msg.To), nil
}

func (m *MemoryOutbox) Pending(_ context.Context, to string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.pending(to), nil
}

func (m *MemoryOutbox) pending(to string) int {
	n := 0
	for _, h := range m.held {
		if h.To == to {
			n++
		}
	}
	return n
}

func (m *MemoryOutbox) Take(_ context.Context, to string, limit int) ([]HeldMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var taken []HeldMessage
	kept := m.held[:0]
	for _, h := range m.held {
		if h.To == to && len(taken) < limit {
			taken = append(taken, h)
			continue
		}
		kept = append(kept, h)
	}
	m.held = kept

	return taken, nil
}
