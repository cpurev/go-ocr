package store

import (
	"context"
	"testing"
	"time"
)

func TestMemoryOutboxSeenKeepsTheNewestTime(t *testing.T) {
	ctx := context.Background()
	o := NewMemoryOutbox()
	newer := time.Now()

	_ = o.Seen(ctx, "46", newer)
	_ = o.Seen(ctx, "46", newer.Add(-30*time.Hour))

	if got, _ := o.LastSeen(ctx, "46"); !got.Equal(newer) {
		t.Fatalf("LastSeen = %v, want %v: a late retry must not shrink the window", got, newer)
	}
	if got, _ := o.LastSeen(ctx, "39"); !got.IsZero() {
		t.Fatalf("LastSeen for a number never seen = %v, want zero", got)
	}
}

func TestMemoryOutboxTakesOldestFirstUpToLimit(t *testing.T) {
	ctx := context.Background()
	o := NewMemoryOutbox()
	base := time.Now()

	for i, body := range []string{"one", "two", "three"} {
		pending, _ := o.Hold(ctx, HeldMessage{To: "46", Body: body, HeldAt: base.Add(time.Duration(i) * time.Second)})
		if pending != i+1 {
			t.Fatalf("Hold %q pending = %d, want %d", body, pending, i+1)
		}
	}
	_, _ = o.Hold(ctx, HeldMessage{To: "39", Body: "other"})

	first, _ := o.Take(ctx, "46", 2)
	if len(first) != 2 || first[0].Body != "one" || first[1].Body != "two" {
		t.Fatalf("first Take = %+v, want one and two", first)
	}
	rest, _ := o.Take(ctx, "46", 10)
	if len(rest) != 1 || rest[0].Body != "three" {
		t.Fatalf("second Take = %+v, want three", rest)
	}
	if again, _ := o.Take(ctx, "46", 10); len(again) != 0 {
		t.Fatalf("third Take = %+v, want nothing left", again)
	}
	if other, _ := o.Take(ctx, "39", 10); len(other) != 1 {
		t.Fatalf("Take for another number = %+v, want its own message untouched", other)
	}
}
