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

var ErrTooManyReceipts = errors.New("store: too many receipts to total")

// If the volume assumption behind summing in Go is ever wrong, the answer is a
// refusal rather than a quietly truncated sum.
const maxTotalReceipts = 10_000

type ReceiptTotal struct {
	Total float64
	Count int

	// Undated counts receipts included by createdAt because OCR found no date
	// on them, so a surprising total can be explained.
	Undated int

	// Latest is newest first by insertion order, at most TotalQuery.Latest.
	Latest []model.Receipt
}

// TotalQuery selects the receipts a total covers. The range is half-open,
// [From, To), and a zero time means unbounded. An empty UserID means everyone.
// Latest is how many of the newest selected receipts come back with the sum,
// and does not narrow what is summed.
type TotalQuery struct {
	UserID string
	From   time.Time
	To     time.Time
	Latest int
}

type ReceiptFilter struct {
	Merchant string
	UserID   string
	GroupID  string
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

	// ListRecentReceipts returns the newest receipts by insertion order, not by
	// the date printed on them.
	ListRecentReceipts(ctx context.Context, limit int) ([]model.Receipt, error)

	UpdateReceipt(ctx context.Context, id string, update model.ReceiptUpdate) (model.Receipt, error)

	DeleteReceipt(ctx context.Context, id string) error

	// SumReceipts is one sum because every receipt is SEK, so a receipt still
	// labelled with another currency counts too. A receipt OCR could not date is
	// dated by createdAt so it cannot fall out of a month.
	SumReceipts(ctx context.Context, q TotalQuery) (ReceiptTotal, error)
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
