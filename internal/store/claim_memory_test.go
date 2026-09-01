package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

const (
	opClaim   = "claim"
	opFinish  = "finish"
	opRelease = "release"
)

type claimStep struct {
	op string

	// advance moves the fake clock forward before op runs.
	advance time.Duration
}

func TestMemoryClaimsLease(t *testing.T) {
	const leaseLength = time.Minute

	tests := []struct {
		name  string
		steps []claimStep
		want  error
	}{
		{
			name:  "an unseen message id is claimed",
			steps: []claimStep{{op: opClaim}},
			want:  nil,
		},
		{
			name:  "a redelivery inside the lease is refused",
			steps: []claimStep{{op: opClaim}, {op: opClaim}},
			want:  ErrClaimed,
		},
		{
			name:  "a released claim lets a retry redo the work",
			steps: []claimStep{{op: opClaim}, {op: opRelease}, {op: opClaim}},
			want:  nil,
		},
		{
			name:  "a finished message is refused",
			steps: []claimStep{{op: opClaim}, {op: opFinish}, {op: opClaim}},
			want:  ErrClaimed,
		},
		{
			name:  "a lease whose owner died is taken over",
			steps: []claimStep{{op: opClaim}, {op: opClaim, advance: leaseLength}},
			want:  nil,
		},
		{
			name:  "a finished message is claimable again once the ttl expires",
			steps: []claimStep{{op: opClaim}, {op: opFinish}, {op: opClaim, advance: ClaimTTL}},
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
			claims := NewMemoryClaims(leaseLength)
			claims.now = func() time.Time { return clock }

			var err error
			for i, step := range tt.steps {
				clock = clock.Add(step.advance)

				switch step.op {
				case opClaim:
					err = claims.Claim(context.Background(), "wamid.1")
				case opFinish:
					err = claims.Finish(context.Background(), "wamid.1")
				case opRelease:
					err = claims.Release(context.Background(), "wamid.1")
				}

				if i < len(tt.steps)-1 && err != nil {
					t.Fatalf("step %d, %s: %v", i, step.op, err)
				}
			}

			if !errors.Is(err, tt.want) {
				t.Errorf("last step = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestMemoryClaimsSweepsExpiredEntriesAtTheBound(t *testing.T) {
	clock := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	claims := NewMemoryClaims(time.Minute)
	claims.now = func() time.Time { return clock }

	for i := range maxMemoryClaims {
		if err := claims.Claim(context.Background(), fmt.Sprintf("wamid.%d", i)); err != nil {
			t.Fatalf("claiming wamid.%d: %v", i, err)
		}
	}
	if got := len(claims.held); got != maxMemoryClaims {
		t.Fatalf("held %d ids before the bound, want %d", got, maxMemoryClaims)
	}

	clock = clock.Add(ClaimTTL)
	if err := claims.Claim(context.Background(), "wamid.new"); err != nil {
		t.Fatalf("claiming at the bound: %v", err)
	}

	if got := len(claims.held); got != 1 {
		t.Errorf("held %d ids after the sweep, want 1", got)
	}
}
