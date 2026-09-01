package store

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type claimDocument struct {
	ID        string    `bson:"_id"`
	Status    string    `bson:"status"`
	ClaimedAt time.Time `bson:"claimedAt"`
}

type MongoClaims struct {
	coll       *mongo.Collection
	staleAfter time.Duration
}

var _ MessageClaims = (*MongoClaims)(nil)

// NewMongoClaims builds the claim store. staleAfter is the lease length and
// must exceed the inline work budget, or a slow but healthy OCR has its lease
// taken over mid-flight and the human gets two replies.
func NewMongoClaims(coll *mongo.Collection, staleAfter time.Duration) *MongoClaims {
	if staleAfter <= 0 {
		staleAfter = defaultStaleAfter
	}
	return &MongoClaims{coll: coll, staleAfter: staleAfter}
}

func (m *MongoClaims) EnsureIndexes(ctx context.Context) error {
	index := mongo.IndexModel{
		Keys: bson.D{{Key: "claimedAt", Value: 1}},
		Options: options.Index().SetName("claimedAt_ttl").
			SetExpireAfterSeconds(int32(ClaimTTL.Seconds())),
	}
	if _, err := m.coll.Indexes().CreateOne(ctx, index); err != nil {
		return fmt.Errorf("mongo: creating claim indexes: %w", err)
	}
	return nil
}

// Claim inserts, then falls back to a stale takeover. These stay two calls: an
// upsert whose filter carries status as an equality while $setOnInsert also
// sets it is the one shape whose document-construction semantics are ambiguous.
func (m *MongoClaims) Claim(ctx context.Context, messageID string) error {
	now := time.Now().UTC().Truncate(time.Millisecond)

	_, err := m.coll.InsertOne(ctx, claimDocument{
		ID:        messageID,
		Status:    statusWorking,
		ClaimedAt: now,
	})
	switch {
	case err == nil:
		return nil
	case !mongo.IsDuplicateKeyError(err):
		return fmt.Errorf("mongo: claiming %s: %w", messageID, err)
	}

	res, err := m.coll.UpdateOne(ctx,
		bson.D{
			{Key: "_id", Value: messageID},
			{Key: "status", Value: statusWorking},
			{Key: "claimedAt", Value: bson.D{{Key: "$lt", Value: now.Add(-m.staleAfter)}}},
		},
		bson.D{{Key: "$set", Value: bson.D{{Key: "claimedAt", Value: now}}}},
	)
	if err != nil {
		return fmt.Errorf("mongo: taking over stale claim %s: %w", messageID, err)
	}
	if res.ModifiedCount == 1 {
		return nil
	}

	return ErrClaimed
}

func (m *MongoClaims) Finish(ctx context.Context, messageID string) error {
	// Refreshing claimedAt restarts the TTL from the reply, not from the claim.
	update := bson.D{{Key: "$set", Value: bson.D{
		{Key: "status", Value: statusReplied},
		{Key: "claimedAt", Value: time.Now().UTC().Truncate(time.Millisecond)},
	}}}

	if _, err := m.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: messageID}}, update); err != nil {
		return fmt.Errorf("mongo: finishing claim %s: %w", messageID, err)
	}
	return nil
}

func (m *MongoClaims) Release(ctx context.Context, messageID string) error {
	if _, err := m.coll.DeleteOne(ctx, bson.D{{Key: "_id", Value: messageID}}); err != nil {
		return fmt.Errorf("mongo: releasing claim %s: %w", messageID, err)
	}
	return nil
}
