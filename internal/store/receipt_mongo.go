package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/cpurev/go-ocr/internal/model"
)

type MongoReceipts struct {
	coll   *mongo.Collection
	seq    Sequencer
	logger *slog.Logger
}

var _ ReceiptStore = (*MongoReceipts)(nil)

func NewMongoReceipts(coll *mongo.Collection, seq Sequencer, logger *slog.Logger) *MongoReceipts {
	return &MongoReceipts{coll: coll, seq: seq, logger: logger}
}

type receiptDocument struct {
	ID bson.ObjectID `bson:"_id,omitempty"`

	Number int `bson:"number"`

	WhatsAppMediaID string             `bson:"whatsappMediaId"`
	UserID          string             `bson:"userId"`
	GroupID         string             `bson:"groupId,omitempty"`
	Merchant        string             `bson:"merchant"`
	Date            string             `bson:"date"`
	Currency        string             `bson:"currency"`
	Subtotal        float64            `bson:"subtotal"`
	Tax             float64            `bson:"tax"`
	Total           float64            `bson:"total"`
	LineItems       []lineItemDocument `bson:"lineItems"`
	RawText         string             `bson:"rawText,omitempty"`
	CreatedAt       time.Time          `bson:"createdAt"`
	UpdatedAt       time.Time          `bson:"updatedAt"`
}

type lineItemDocument struct {
	Name  string  `bson:"name"`
	Qty   int     `bson:"qty"`
	Price float64 `bson:"price"`
}

func (d receiptDocument) toModel() model.Receipt {
	items := make([]model.LineItem, 0, len(d.LineItems))
	for _, item := range d.LineItems {
		items = append(items, model.LineItem{Name: item.Name, Qty: item.Qty, Price: item.Price})
	}

	return model.Receipt{
		ID:              d.ID.Hex(),
		Number:          d.Number,
		WhatsAppMediaID: d.WhatsAppMediaID,
		UserID:          d.UserID,
		GroupID:         d.GroupID,
		Merchant:        d.Merchant,
		Date:            d.Date,
		Currency:        d.Currency,
		Subtotal:        d.Subtotal,
		Tax:             d.Tax,
		Total:           d.Total,
		LineItems:       items,
		RawText:         d.RawText,
		CreatedAt:       d.CreatedAt,
		UpdatedAt:       d.UpdatedAt,
	}
}

func newReceiptDocument(r model.Receipt, id bson.ObjectID) receiptDocument {
	items := make([]lineItemDocument, 0, len(r.LineItems))
	for _, item := range r.LineItems {
		items = append(items, lineItemDocument{Name: item.Name, Qty: item.Qty, Price: item.Price})
	}

	return receiptDocument{
		ID:              id,
		Number:          r.Number,
		WhatsAppMediaID: r.WhatsAppMediaID,
		UserID:          r.UserID,
		GroupID:         r.GroupID,
		Merchant:        r.Merchant,
		Date:            r.Date,
		Currency:        r.Currency,
		Subtotal:        r.Subtotal,
		Tax:             r.Tax,
		Total:           r.Total,
		LineItems:       items,
		RawText:         r.RawText,
		CreatedAt:       r.CreatedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func (m *MongoReceipts) EnsureIndexes(ctx context.Context) error {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "whatsappMediaId", Value: 1}},
			Options: options.Index().SetName("whatsappMediaId_unique").SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "userId", Value: 1}, {Key: "date", Value: -1}},
			Options: options.Index().SetName("userId_date_desc"),
		},
		{
			Keys:    bson.D{{Key: "date", Value: -1}},
			Options: options.Index().SetName("date_desc"),
		},
		{
			Keys:    bson.D{{Key: "merchant", Value: 1}},
			Options: options.Index().SetName("merchant_asc"),
		},
		{
			Keys: bson.D{{Key: "number", Value: 1}},
			Options: options.Index().SetName("number_unique").
				SetUnique(true).SetSparse(true),
		},
	}

	if _, err := m.coll.Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("mongo: creating receipt indexes: %w", err)
	}
	return nil
}

func (m *MongoReceipts) Ping(ctx context.Context) error {
	if err := m.coll.Database().RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
		return fmt.Errorf("mongo: ping: %w", err)
	}
	return nil
}

func (m *MongoReceipts) CreateReceipt(ctx context.Context, in model.ReceiptInput, fields model.ReceiptFields) (model.Receipt, error) {
	objID := bson.NewObjectID()
	receipt := in.NewReceipt(objID.Hex(), fields, time.Now().UTC().Truncate(time.Millisecond))

	if m.seq != nil {
		number, err := m.seq.Next(ctx, ReceiptSequence)
		if err != nil {
			return model.Receipt{}, err
		}
		receipt.Number = number
	}

	if _, err := m.coll.InsertOne(ctx, newReceiptDocument(receipt, objID)); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return model.Receipt{}, fmt.Errorf("%w: media %s already ingested",
				ErrDuplicate, receipt.WhatsAppMediaID)
		}
		return model.Receipt{}, fmt.Errorf("mongo: inserting receipt: %w", err)
	}

	return receipt, nil
}

func (m *MongoReceipts) GetReceiptByNumber(ctx context.Context, number int) (model.Receipt, error) {
	if number <= 0 {
		return model.Receipt{}, ErrNotFound
	}

	var doc receiptDocument
	err := m.coll.FindOne(ctx, bson.D{{Key: "number", Value: number}}).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return model.Receipt{}, ErrNotFound
	case err != nil:
		return model.Receipt{}, fmt.Errorf("mongo: finding receipt #%d: %w", number, err)
	}

	return doc.toModel(), nil
}

func (m *MongoReceipts) UpdateReceipt(ctx context.Context, id string, update model.ReceiptUpdate) (model.Receipt, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return model.Receipt{}, ErrNotFound
	}

	clean := update.Normalized()

	set := bson.D{{Key: "updatedAt", Value: time.Now().UTC().Truncate(time.Millisecond)}}
	if clean.Merchant != nil {
		set = append(set, bson.E{Key: "merchant", Value: *clean.Merchant})
	}
	if clean.Date != nil {
		set = append(set, bson.E{Key: "date", Value: *clean.Date})
	}
	if clean.Currency != nil {
		set = append(set, bson.E{Key: "currency", Value: *clean.Currency})
	}
	if clean.Subtotal != nil {
		set = append(set, bson.E{Key: "subtotal", Value: *clean.Subtotal})
	}
	if clean.Tax != nil {
		set = append(set, bson.E{Key: "tax", Value: *clean.Tax})
	}
	if clean.Total != nil {
		set = append(set, bson.E{Key: "total", Value: *clean.Total})
	}

	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

	var doc receiptDocument
	err = m.coll.FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: objID}},
		bson.D{{Key: "$set", Value: set}},
		opts,
	).Decode(&doc)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return model.Receipt{}, ErrNotFound
	case err != nil:
		return model.Receipt{}, fmt.Errorf("mongo: updating receipt %s: %w", id, err)
	}

	return doc.toModel(), nil
}

func (m *MongoReceipts) GetReceipt(ctx context.Context, id string) (model.Receipt, error) {
	objID, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return model.Receipt{}, ErrNotFound
	}

	var doc receiptDocument
	if err := m.coll.FindOne(ctx, bson.D{{Key: "_id", Value: objID}}).Decode(&doc); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return model.Receipt{}, ErrNotFound
		}
		return model.Receipt{}, fmt.Errorf("mongo: fetching receipt %s: %w", id, err)
	}

	return doc.toModel(), nil
}

func (m *MongoReceipts) ListReceipts(ctx context.Context, f ReceiptFilter) ([]model.Receipt, int64, error) {
	query := buildReceiptQuery(f)

	if m.logger != nil {
		m.logger.DebugContext(ctx, "listing receipts",
			"filter", describeFilter(f), "limit", f.Limit, "offset", f.Offset)
	}

	total, err := m.coll.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("mongo: counting receipts: %w", err)
	}

	cursor, err := m.coll.Find(ctx, query, options.Find().
		SetSort(bson.D{{Key: "date", Value: -1}, {Key: "_id", Value: -1}}).
		SetSkip(int64(f.Offset)).
		SetLimit(int64(f.Limit)))
	if err != nil {
		return nil, 0, fmt.Errorf("mongo: listing receipts: %w", err)
	}
	defer cursor.Close(ctx)

	var docs []receiptDocument
	if err := cursor.All(ctx, &docs); err != nil {
		return nil, 0, fmt.Errorf("mongo: decoding receipts: %w", err)
	}

	receipts := make([]model.Receipt, 0, len(docs))
	for _, doc := range docs {
		receipts = append(receipts, doc.toModel())
	}

	return receipts, total, nil
}

func buildReceiptQuery(f ReceiptFilter) bson.D {
	query := bson.D{}

	if f.UserID != "" {
		query = append(query, bson.E{Key: "userId", Value: f.UserID})
	}
	if f.GroupID != "" {
		query = append(query, bson.E{Key: "groupId", Value: f.GroupID})
	}
	if f.Currency != "" {
		query = append(query, bson.E{Key: "currency", Value: f.Currency})
	}

	if f.Merchant != "" {
		query = append(query, bson.E{Key: "merchant", Value: bson.D{
			{Key: "$regex", Value: regexp.QuoteMeta(f.Merchant)},
			{Key: "$options", Value: "i"},
		}})
	}

	if dateRange := rangeDoc(f.DateFrom, f.DateTo); len(dateRange) > 0 {
		query = append(query, bson.E{Key: "date", Value: dateRange})
	}

	totalRange := bson.D{}
	if f.MinTotal != nil {
		totalRange = append(totalRange, bson.E{Key: "$gte", Value: *f.MinTotal})
	}
	if f.MaxTotal != nil {
		totalRange = append(totalRange, bson.E{Key: "$lte", Value: *f.MaxTotal})
	}
	if len(totalRange) > 0 {
		query = append(query, bson.E{Key: "total", Value: totalRange})
	}

	return query
}

func rangeDoc(from, to string) bson.D {
	out := bson.D{}
	if from != "" {
		out = append(out, bson.E{Key: "$gte", Value: from})
	}
	if to != "" {
		out = append(out, bson.E{Key: "$lte", Value: to})
	}
	return out
}
