package model

import (
	"regexp"
	"strings"
	"time"
)

type Store struct {
	ID        string    `json:"id"`
	OrgNr     string    `json:"org_nr"`
	Merchant  string    `json:"merchant"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var orgNrDigitsRe = regexp.MustCompile(`\D`)

func NormalizeOrgNr(s string) string {
	digits := orgNrDigitsRe.ReplaceAllString(s, "")
	if len(digits) > 10 {
		digits = digits[len(digits)-10:]
	}
	return digits
}

func ValidOrgNr(normalized string) bool {
	return len(normalized) == 10
}

func NewStore(id, orgNr, merchant string, now time.Time) Store {
	return Store{
		ID:        id,
		OrgNr:     NormalizeOrgNr(orgNr),
		Merchant:  strings.TrimSpace(merchant),
		CreatedAt: now,
		UpdatedAt: now,
	}
}

type ReceiptUpdate struct {
	Merchant *string
	Date     *string
	Currency *string
	Subtotal *float64
	Tax      *float64
	Total    *float64
}

func (u ReceiptUpdate) IsEmpty() bool {
	return u.Merchant == nil && u.Date == nil && u.Currency == nil &&
		u.Subtotal == nil && u.Tax == nil && u.Total == nil
}

func (u ReceiptUpdate) Validate() ValidationErrors {
	problems := make(ValidationErrors)

	if u.Merchant != nil && len(strings.TrimSpace(*u.Merchant)) > maxMerchantLen {
		problems["merchant"] = "is too long"
	}
	if u.Date != nil && *u.Date != "" {
		if _, err := time.Parse(DateLayout, strings.TrimSpace(*u.Date)); err != nil {
			problems["date"] = "must be YYYY-MM-DD"
		}
	}
	if u.Currency != nil && *u.Currency != "" && len(strings.TrimSpace(*u.Currency)) != 3 {
		problems["currency"] = "must be a 3-letter code, e.g. SEK"
	}
	for name, v := range map[string]*float64{
		"subtotal": u.Subtotal, "tax": u.Tax, "total": u.Total,
	} {
		if v != nil && *v < 0 {
			problems[name] = "must not be negative"
		}
	}

	return problems
}

func (u ReceiptUpdate) Normalized() ReceiptUpdate {
	out := u

	if u.Merchant != nil {
		v := strings.TrimSpace(*u.Merchant)
		out.Merchant = &v
	}
	if u.Date != nil {
		v := strings.TrimSpace(*u.Date)
		out.Date = &v
	}
	if u.Currency != nil {
		v := strings.ToUpper(strings.TrimSpace(*u.Currency))
		out.Currency = &v
	}
	for _, p := range []struct{ in, out **float64 }{
		{&u.Subtotal, &out.Subtotal}, {&u.Tax, &out.Tax}, {&u.Total, &out.Total},
	} {
		if *p.in != nil {
			v := RoundMoney(**p.in)
			*p.out = &v
		}
	}

	return out
}
