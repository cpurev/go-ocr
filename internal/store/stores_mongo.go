package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/cpurev/go-ocr/internal/model"
)

type storeDocument struct {
	ID        bson.ObjectID `bson:"_id,omitempty"`
	OrgNr     string        `bson:"orgNr"`
	Merchant  string        `bson:"merchant"`
	CreatedAt time.Time     `bson:"createdAt"`
	UpdatedAt time.Time     `bson:"updatedAt"`
}

func (d storeDocument) toModel() model.Store {
	return model.Store{
		ID:        d.ID.Hex(),
		OrgNr:     d.OrgNr,
		Merchant:  d.Merchant,
		CreatedAt: d.CreatedAt,
		UpdatedAt: d.UpdatedAt,
	}
}

type MongoStores struct {
	coll *mongo.Collection
}

var _ StoreDirectory = (*MongoStores)(nil)

func NewMongoStores(coll *mongo.Collection) *MongoStores {
	return &MongoStores{coll: coll}
}

func (m *MongoStores) EnsureIndexes(ctx context.Context) error {
	model := mongo.IndexModel{
		Keys:    bson.D{{Key: "orgNr", Value: 1}},
		Options: options.Index().SetName("orgNr_unique").SetUnique(true),
	}
	if _, err := m.coll.Indexes().CreateOne(ctx, model); err != nil {
		return fmt.Errorf("mongo: creating store indexes: %w", err)
	}
	return nil
}

func (m *MongoStores) LookupStore(ctx context.Context, orgNr string) (model.Store, error) {
	normalized := model.NormalizeOrgNr(orgNr)
	if !model.ValidOrgNr(normalized) {
		return model.Store{}, ErrNotFound
	}

	var doc storeDocument
	err := m.coll.FindOne(ctx, bson.D{{Key: "orgNr", Value: normalized}}).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return model.Store{}, ErrNotFound
	case err != nil:
		return model.Store{}, fmt.Errorf("mongo: looking up store %s: %w", normalized, err)
	}

	return doc.toModel(), nil
}

func (m *MongoStores) SaveStore(ctx context.Context, orgNr, merchant string) (model.Store, error) {
	normalized := model.NormalizeOrgNr(orgNr)
	if !model.ValidOrgNr(normalized) {
		return model.Store{}, fmt.Errorf("store: %q is not a usable registration number", orgNr)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)

	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)

	update := bson.D{
		{Key: "$set", Value: bson.D{
			{Key: "merchant", Value: merchant},
			{Key: "updatedAt", Value: now},
		}},

		{Key: "$setOnInsert", Value: bson.D{
			{Key: "orgNr", Value: normalized},
			{Key: "createdAt", Value: now},
		}},
	}

	var doc storeDocument
	err := m.coll.FindOneAndUpdate(ctx,
		bson.D{{Key: "orgNr", Value: normalized}}, update, opts).Decode(&doc)
	if err != nil {
		return model.Store{}, fmt.Errorf("mongo: saving store %s: %w", normalized, err)
	}

	return doc.toModel(), nil
}

func (m *MongoStores) ListStores(ctx context.Context) ([]model.Store, error) {
	opts := options.Find().SetSort(bson.D{{Key: "updatedAt", Value: -1}}).SetLimit(500)

	cursor, err := m.coll.Find(ctx, bson.D{}, opts)
	if err != nil {
		return nil, fmt.Errorf("mongo: listing stores: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []storeDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("mongo: decoding stores: %w", err)
	}

	out := make([]model.Store, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.toModel())
	}
	return out, nil
}
