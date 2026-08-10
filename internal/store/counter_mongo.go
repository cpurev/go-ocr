package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type counterDocument struct {
	ID  string `bson:"_id"`
	Seq int    `bson:"seq"`
}

type MongoCounters struct {
	coll *mongo.Collection
}

func NewMongoCounters(coll *mongo.Collection) *MongoCounters {
	return &MongoCounters{coll: coll}
}

func (m *MongoCounters) Next(ctx context.Context, name string) (int, error) {
	opts := options.FindOneAndUpdate().
		SetUpsert(true).
		SetReturnDocument(options.After)

	var doc counterDocument
	err := m.coll.FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: name}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "seq", Value: 1}}}},
		opts,
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("mongo: allocating %s sequence: %w", name, err)
	}

	return doc.Seq, nil
}
