package store

import (
	"context"
	"sync"
	"time"
)

// maxMemoryClaims is the size at which Claim sweeps expired entries, so a
// long-lived process does not hold every message id it has ever seen.
const maxMemoryClaims = 4096

// MemoryClaims is the claim store used when Mongo is not configured. It is
// per-process, so several Cloud Run instances can still let one retry through.
// That is a smaller hole than no claim at all, which is the alternative.
type MemoryClaims struct {
	mu         sync.Mutex
	staleAfter time.Duration
	held       map[string]memoryClaim

	// now is a seam: the lease and the TTL are both time based, and a test
	// cannot sleep through 48 hours.
	now func() time.Time
}

type memoryClaim struct {
	status    string
	claimedAt time.Time
}

var _ MessageClaims = (*MemoryClaims)(nil)

func NewMemoryClaims(staleAfter time.Duration) *MemoryClaims {
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}
	return &MemoryClaims{
		staleAfter: staleAfter,
		held:       make(map[string]memoryClaim),
		now:        time.Now,
	}
}

func (m *MemoryClaims) Claim(_ context.Context, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if len(m.held) >= maxMemoryClaims {
		m.sweep(now)
	}

	if held, ok := m.held[messageID]; ok && !m.takeable(held, now) {
		return ErrClaimed
	}

	m.held[messageID] = memoryClaim{status: statusWorking, claimedAt: now}
	return nil
}

func (m *MemoryClaims) Finish(_ context.Context, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.held[messageID]; !ok {
		return nil
	}
	m.held[messageID] = memoryClaim{status: statusReplied, claimedAt: m.now()}
	return nil
}

func (m *MemoryClaims) Release(_ context.Context, messageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.held, messageID)
	return nil
}

// takeable reports a claim a new delivery may take: expired, or a working
// lease whose owner has gone quiet for longer than staleAfter.
func (m *MemoryClaims) takeable(c memoryClaim, now time.Time) bool {
	if now.Sub(c.claimedAt) >= ClaimTTL {
		return true
	}
	return c.status == statusWorking && now.Sub(c.claimedAt) >= m.staleAfter
}

// sweep drops expired entries. Mongo's TTL index does this for MongoClaims.
func (m *MemoryClaims) sweep(now time.Time) {
	for id, c := range m.held {
		if now.Sub(c.claimedAt) >= ClaimTTL {
			delete(m.held, id)
		}
	}
}
