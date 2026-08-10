package model

import (
	"math"
	"strings"
	"time"
)

const DateLayout = "2006-01-02"

const (
	maxMerchantLen  = 200
	maxRawTextRunes = 50_000
	maxLineItems    = 500
	maxIDLen        = 200
)

type LineItem struct {
	Name  string  `json:"name"`
	Qty   int     `json:"qty"`
	Price float64 `json:"price"`
}

type Receipt struct {
	ID string `json:"id"`

	Number int `json:"number"`

	WhatsAppMediaID string `json:"whatsapp_media_id"`
	UserID          string `json:"user_id"`
	GroupID         string `json:"group_id,omitempty"`

	Merchant  string     `json:"merchant"`
	Date      string     `json:"date"`
	Currency  string     `json:"currency"`
	Subtotal  float64    `json:"subtotal"`
	Tax       float64    `json:"tax"`
	Total     float64    `json:"total"`
	LineItems []LineItem `json:"line_items"`
	RawText   string     `json:"raw_text,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ReceiptInput struct {
	WhatsAppMediaID string `json:"whatsapp_media_id"`
	UserID          string `json:"user_id"`
	GroupID         string `json:"group_id"`
}

func (in ReceiptInput) Validate() ValidationErrors {
	problems := make(ValidationErrors)

	mediaID := strings.TrimSpace(in.WhatsAppMediaID)
	switch {
	case mediaID == "":
		problems["whatsapp_media_id"] = "must not be empty"
	case len(mediaID) > maxIDLen:
		problems["whatsapp_media_id"] = "must be at most 200 characters"
	}

	userID := strings.TrimSpace(in.UserID)
	switch {
	case userID == "":
		problems["user_id"] = "must not be empty"
	case len(userID) > maxIDLen:
		problems["user_id"] = "must be at most 200 characters"
	}

	if len(strings.TrimSpace(in.GroupID)) > maxIDLen {
		problems["group_id"] = "must be at most 200 characters"
	}

	if len(problems) == 0 {
		return nil
	}
	return problems
}

type ReceiptFields struct {
	Merchant  string
	Date      string
	Currency  string
	Subtotal  float64
	Tax       float64
	Total     float64
	LineItems []LineItem
	RawText   string
}

func RoundMoney(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}

func (f ReceiptFields) Normalize() ReceiptFields {
	f.Merchant = truncateRunes(strings.TrimSpace(f.Merchant), maxMerchantLen)
	f.Currency = strings.ToUpper(strings.TrimSpace(f.Currency))
	f.RawText = truncateRunes(f.RawText, maxRawTextRunes)

	f.Date = strings.TrimSpace(f.Date)
	if f.Date != "" {
		if _, err := time.Parse(DateLayout, f.Date); err != nil {
			f.Date = ""
		}
	}

	f.Subtotal = RoundMoney(f.Subtotal)
	f.Tax = RoundMoney(f.Tax)
	f.Total = RoundMoney(f.Total)

	if len(f.LineItems) > maxLineItems {
		f.LineItems = f.LineItems[:maxLineItems]
	}

	items := make([]LineItem, 0, len(f.LineItems))
	for _, item := range f.LineItems {
		items = append(items, LineItem{
			Name:  truncateRunes(strings.TrimSpace(item.Name), maxMerchantLen),
			Qty:   item.Qty,
			Price: RoundMoney(item.Price),
		})
	}
	f.LineItems = items

	return f
}

func (in ReceiptInput) NewReceipt(id string, fields ReceiptFields, now time.Time) Receipt {
	clean := fields.Normalize()

	return Receipt{
		ID:              id,
		WhatsAppMediaID: strings.TrimSpace(in.WhatsAppMediaID),
		UserID:          strings.TrimSpace(in.UserID),
		GroupID:         strings.TrimSpace(in.GroupID),
		Merchant:        clean.Merchant,
		Date:            clean.Date,
		Currency:        clean.Currency,
		Subtotal:        clean.Subtotal,
		Tax:             clean.Tax,
		Total:           clean.Total,
		LineItems:       clean.LineItems,
		RawText:         clean.RawText,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}
