package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type seenDocument struct {
	ID       string    `bson:"_id"`
	LastSeen time.Time `bson:"lastSeen"`
}

type heldDocument struct {
	ID     bson.ObjectID `bson:"_id,omitempty"`
	To     string        `bson:"to"`
	From   string        `bson:"from"`
	Body   string        `bson:"body"`
	HeldAt time.Time     `bson:"heldAt"`
}

type MongoOutbox struct {
	seen *mongo.Collection
	held *mongo.Collection
}

var _ Outbox = (*MongoOutbox)(nil)

func NewMongoOutbox(seen, held *mongo.Collection) *MongoOutbox {
	return &MongoOutbox{seen: seen, held: held}
}

func (m *MongoOutbox) EnsureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "to", Value: 1}, {Key: "heldAt", Value: 1}},
			Options: options.Index().SetName("to_heldAt"),
		},
		{
			Keys: bson.D{{Key: "heldAt", Value: 1}},
			Options: options.Index().SetName("heldAt_ttl").
				SetExpireAfterSeconds(int32(HeldTTL.Seconds())),
		},
	}
	if _, err := m.held.Indexes().CreateMany(ctx, indexes); err != nil {
		return fmt.Errorf("mongo: creating outbox indexes: %w", err)
	}
	return nil
}

func (m *MongoOutbox) Seen(ctx context.Context, number string, at time.Time) error {
	// $max keeps the newest time, so a webhook retried hours late is harmless.
	_, err := m.seen.UpdateOne(ctx,
		bson.D{{Key: "_id", Value: number}},
		bson.D{{Key: "$max", Value: bson.D{
			{Key: "lastSeen", Value: at.UTC().Truncate(time.Millisecond)},
		}}},
		options.UpdateOne().SetUpsert(true),
	)
	if err != nil {
		return fmt.Errorf("mongo: recording %s as seen: %w", number, err)
	}
	return nil
}

func (m *MongoOutbox) LastSeen(ctx context.Context, number string) (time.Time, error) {
	var doc seenDocument
	err := m.seen.FindOne(ctx, bson.D{{Key: "_id", Value: number}}).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return time.Time{}, nil
	case err != nil:
		return time.Time{}, fmt.Errorf("mongo: reading when %s was last seen: %w", number, err)
	}
	return doc.LastSeen, nil
}

func (m *MongoOutbox) Hold(ctx context.Context, msg HeldMessage) (int, error) {
	_, err := m.held.InsertOne(ctx, heldDocument{
		To:     msg.To,
		From:   msg.From,
		Body:   msg.Body,
		HeldAt: msg.HeldAt.UTC().Truncate(time.Millisecond),
	})
	if err != nil {
		return 0, fmt.Errorf("mongo: holding message for %s: %w", msg.To, err)
	}

	return m.Pending(ctx, msg.To)
}

func (m *MongoOutbox) Pending(ctx context.Context, to string) (int, error) {
	pending, err := m.held.CountDocuments(ctx, bson.D{{Key: "to", Value: to}})
	if err != nil {
		return 0, fmt.Errorf("mongo: counting messages held for %s: %w", to, err)
	}
	return int(pending), nil
}

// Take deletes one document per call rather than find-then-delete, so two
// instances draining the same recipient each get different messages.
func (m *MongoOutbox) Take(ctx context.Context, to string, limit int) ([]HeldMessage, error) {
	opts := options.FindOneAndDelete().SetSort(bson.D{{Key: "heldAt", Value: 1}})

	var taken []HeldMessage
	for len(taken) < limit {
		var doc heldDocument
		err := m.held.FindOneAndDelete(ctx, bson.D{{Key: "to", Value: to}}, opts).Decode(&doc)
		switch {
		case errors.Is(err, mongo.ErrNoDocuments):
			return taken, nil
		case err != nil:
			return taken, fmt.Errorf("mongo: taking messages held for %s: %w", to, err)
		}
		taken = append(taken, HeldMessage{To: doc.To, From: doc.From, Body: doc.Body, HeldAt: doc.HeldAt})
	}
	return taken, nil
}
