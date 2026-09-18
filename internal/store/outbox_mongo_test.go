package store

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestMongoOutbox runs against a real server, since $max upserts and
// FindOneAndDelete ordering are the parts a fake would get right by accident.
// Set MONGO_TEST_URI (e.g. mongodb://localhost:27017) to run it.
func TestMongoOutbox(t *testing.T) {
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("MONGO_TEST_URI not set")
	}
	ctx := context.Background()

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	t.Cleanup(func() { _ = client.Disconnect(ctx) })

	db := client.Database("go_ocr_test_" + time.Now().Format("150405.000000"))
	t.Cleanup(func() { _ = db.Drop(ctx) })

	o := NewMongoOutbox(db.Collection("seen"), db.Collection("outbox"))
	if err := o.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	newer := time.Now().UTC().Truncate(time.Millisecond)
	if err := o.Seen(ctx, "46", newer); err != nil {
		t.Fatalf("Seen: %v", err)
	}
	if err := o.Seen(ctx, "46", newer.Add(-30*time.Hour)); err != nil {
		t.Fatalf("Seen older: %v", err)
	}
	if got, err := o.LastSeen(ctx, "46"); err != nil || !got.Equal(newer) {
		t.Fatalf("LastSeen = %v, %v; want %v", got, err, newer)
	}
	if got, err := o.LastSeen(ctx, "39"); err != nil || !got.IsZero() {
		t.Fatalf("LastSeen unseen = %v, %v; want zero", got, err)
	}

	for i, body := range []string{"one", "two", "three"} {
		pending, err := o.Hold(ctx, HeldMessage{To: "46", From: "39", Body: body, HeldAt: newer.Add(time.Duration(i) * time.Second)})
		if err != nil || pending != i+1 {
			t.Fatalf("Hold %q = %d, %v; want %d", body, pending, err, i+1)
		}
	}

	first, err := o.Take(ctx, "46", 2)
	if err != nil || len(first) != 2 || first[0].Body != "one" || first[1].Body != "two" || first[0].From != "39" {
		t.Fatalf("Take = %+v, %v; want one and two from 39", first, err)
	}
	rest, err := o.Take(ctx, "46", 10)
	if err != nil || len(rest) != 1 || rest[0].Body != "three" {
		t.Fatalf("Take rest = %+v, %v; want three", rest, err)
	}
}
