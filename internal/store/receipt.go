package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/model"
)

var ErrDuplicate = errors.New("store: record already exists")

type ReceiptFilter struct {
	Merchant string
	UserID   string
	GroupID  string
	Currency string
	DateFrom string
	DateTo   string
	MinTotal *float64
	MaxTotal *float64

	Limit  int
	Offset int
}

const (
	DefaultReceiptLimit = 50
	MaxReceiptLimit     = 200
)

func (f ReceiptFilter) Validate() (ReceiptFilter, model.ValidationErrors) {
	problems := make(model.ValidationErrors)

	f.Merchant = strings.TrimSpace(f.Merchant)
	f.UserID = strings.TrimSpace(f.UserID)
	f.GroupID = strings.TrimSpace(f.GroupID)
	f.Currency = strings.ToUpper(strings.TrimSpace(f.Currency))

	for field, value := range map[string]string{"date_from": f.DateFrom, "date_to": f.DateTo} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(model.DateLayout, value); err != nil {
			problems[field] = "must be a date in YYYY-MM-DD format"
		}
	}

	if problems["date_from"] == "" && problems["date_to"] == "" &&
		f.DateFrom != "" && f.DateTo != "" && f.DateFrom > f.DateTo {
		problems["date_from"] = "must not be after date_to"
	}

	if f.MinTotal != nil && f.MaxTotal != nil && *f.MinTotal > *f.MaxTotal {
		problems["min_total"] = "must not be greater than max_total"
	}

	switch {
	case f.Limit < 0:
		problems["limit"] = "must not be negative"
	case f.Limit == 0:
		f.Limit = DefaultReceiptLimit
	case f.Limit > MaxReceiptLimit:
		f.Limit = MaxReceiptLimit
	}
	if f.Offset < 0 {
		problems["offset"] = "must not be negative"
	}

	if len(problems) == 0 {
		return f, nil
	}
	return f, problems
}

type ReceiptStore interface {
	CreateReceipt(ctx context.Context, in model.ReceiptInput, fields model.ReceiptFields) (model.Receipt, error)

	GetReceipt(ctx context.Context, id string) (model.Receipt, error)

	ListReceipts(ctx context.Context, filter ReceiptFilter) ([]model.Receipt, int64, error)

	GetReceiptByNumber(ctx context.Context, number int) (model.Receipt, error)

	UpdateReceipt(ctx context.Context, id string, update model.ReceiptUpdate) (model.Receipt, error)
}

type StoreDirectory interface {
	LookupStore(ctx context.Context, orgNr string) (model.Store, error)

	SaveStore(ctx context.Context, orgNr, merchant string) (model.Store, error)

	ListStores(ctx context.Context) ([]model.Store, error)
}

type Sequencer interface {
	Next(ctx context.Context, name string) (int, error)
}

const ReceiptSequence = "receipts"

func describeFilter(f ReceiptFilter) string {
	parts := make([]string, 0, 8)
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("merchant", f.Merchant)
	add("user", f.UserID)
	add("group", f.GroupID)
	add("currency", f.Currency)
	add("from", f.DateFrom)
	add("to", f.DateTo)
	if f.MinTotal != nil {
		add("min", fmt.Sprintf("%.2f", *f.MinTotal))
	}
	if f.MaxTotal != nil {
		add("max", fmt.Sprintf("%.2f", *f.MaxTotal))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}
