package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/cpurev/go-ocr/internal/model"
)

func TestBuildTotalQuery(t *testing.T) {
	from := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

	september := bson.E{Key: "$or", Value: bson.A{
		bson.D{{Key: "date", Value: bson.D{
			{Key: "$gte", Value: "2026-09-01"},
			{Key: "$lt", Value: "2026-10-01"},
		}}},
		bson.D{
			{Key: "date", Value: ""},
			{Key: "createdAt", Value: bson.D{
				{Key: "$gte", Value: from},
				{Key: "$lt", Value: to},
			}},
		},
	}}

	tests := []struct {
		name string
		q    TotalQuery
		want bson.D
	}{
		{
			name: "a month for one sender",
			q:    TotalQuery{UserID: "46700000001", From: from, To: to},
			want: bson.D{{Key: "userId", Value: "46700000001"}, september},
		},
		{
			name: "a month for everyone",
			q:    TotalQuery{From: from, To: to},
			want: bson.D{september},
		},
		{
			name: "all time for one sender",
			q:    TotalQuery{UserID: "46700000001"},
			want: bson.D{{Key: "userId", Value: "46700000001"}},
		},
		{
			name: "all time for everyone matches every receipt",
			q:    TotalQuery{},
			want: bson.D{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildTotalQuery(tt.q)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("buildTotalQuery(%+v) built %+v, want %+v", tt.q, got, tt.want)
			}
		})
	}
}

func TestSumReceipts(t *testing.T) {
	arrived := func(day int) bson.ObjectID {
		return bson.NewObjectIDFromTimestamp(time.Date(2026, time.September, day, 0, 0, 0, 0, time.UTC))
	}
	ica := receiptDocument{ID: arrived(2), Number: 45, Merchant: "ICA",
		Currency: "SEK", Total: 100.10, Date: "2026-09-02"}
	coop := receiptDocument{ID: arrived(3), Number: 46, Merchant: "Coop",
		Currency: "SEK", Total: 54.43}
	willys := receiptDocument{ID: arrived(5), Number: 47, Merchant: "Willys",
		Currency: "USD", Total: 89, Date: "2026-09-04"}
	ikea := receiptDocument{ID: arrived(6), Number: 48, Merchant: "Ikea",
		Currency: "SEK", Total: 1085.97}

	tests := []struct {
		name   string
		docs   []receiptDocument
		latest int
		want   ReceiptTotal
	}{
		{
			name:   "nothing to add up",
			latest: 5,
			want:   ReceiptTotal{},
		},
		{
			name:   "one sum whatever the stored label, with the newest by arrival capped",
			docs:   []receiptDocument{coop, ikea, ica, willys},
			latest: 2,
			want: ReceiptTotal{Total: 1329.50, Count: 4, Undated: 2,
				Latest: []model.Receipt{ikea.toModel(), willys.toModel()}},
		},
		{
			name:   "a cap past the count returns every receipt",
			docs:   []receiptDocument{ica, willys},
			latest: 5,
			want: ReceiptTotal{Total: 189.10, Count: 2,
				Latest: []model.Receipt{willys.toModel(), ica.toModel()}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sumReceipts(tt.docs, tt.latest)
			if err != nil {
				t.Fatalf("sumReceipts failed with %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sumReceipts gave %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSumReceiptsRefusesMoreThanItCanHold(t *testing.T) {
	docs := make([]receiptDocument, maxTotalReceipts+1)

	if _, err := sumReceipts(docs, 5); !errors.Is(err, ErrTooManyReceipts) {
		t.Errorf("summing %d receipts gave %v, want ErrTooManyReceipts", len(docs), err)
	}
	if _, err := sumReceipts(docs[:maxTotalReceipts], 5); err != nil {
		t.Errorf("summing %d receipts failed with %v, want the limit to be inclusive",
			maxTotalReceipts, err)
	}
}
