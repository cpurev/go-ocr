package store

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
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

func TestSumByCurrency(t *testing.T) {
	tests := []struct {
		name string
		docs []receiptDocument
		want []CurrencyTotal
	}{
		{
			name: "nothing to add up",
			want: []CurrencyTotal{},
		},
		{
			name: "currencies are kept apart and undated receipts are counted",
			docs: []receiptDocument{
				{Currency: "SEK", Total: 100.10, Date: "2026-09-02"},
				{Currency: "SEK", Total: 54.43},
				{Currency: "EUR", Total: 89, Date: "2026-09-04"},
				{Currency: "SEK", Total: 1085.97},
			},
			want: []CurrencyTotal{
				{Currency: "SEK", Total: 1240.50, Count: 3, Undated: 2},
				{Currency: "EUR", Total: 89, Count: 1},
			},
		},
		{
			name: "equal totals fall back to the currency name",
			docs: []receiptDocument{
				{Currency: "USD", Total: 10, Date: "2026-09-01"},
				{Currency: "EUR", Total: 10, Date: "2026-09-01"},
			},
			want: []CurrencyTotal{
				{Currency: "EUR", Total: 10, Count: 1},
				{Currency: "USD", Total: 10, Count: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sumByCurrency(tt.docs)
			if err != nil {
				t.Fatalf("sumByCurrency failed with %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("sumByCurrency gave %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSumByCurrencyRefusesMoreThanItCanHold(t *testing.T) {
	docs := make([]receiptDocument, maxTotalReceipts+1)

	if _, err := sumByCurrency(docs); !errors.Is(err, ErrTooManyReceipts) {
		t.Errorf("summing %d receipts gave %v, want ErrTooManyReceipts", len(docs), err)
	}
	if _, err := sumByCurrency(docs[:maxTotalReceipts]); err != nil {
		t.Errorf("summing %d receipts failed with %v, want the limit to be inclusive",
			maxTotalReceipts, err)
	}
}
