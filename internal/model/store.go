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

// NormalizeOrgNr reduces an org.nr to its 10 digits. The VAT form
// "SE556036079301" is the org.nr wrapped in "SE" and "01", so its 12 digits
// lose the trailing "01", not the leading "55" that a plain last-10 cut would
// keep. Any other longer run (a "16" century prefix) keeps its last 10 digits.
func NormalizeOrgNr(s string) string {
	digits := orgNrDigitsRe.ReplaceAllString(s, "")
	if len(digits) == 12 && strings.HasSuffix(digits, "01") {
		return digits[:10]
	}
	if len(digits) > 10 {
		digits = digits[len(digits)-10:]
	}
	return digits
}

// ValidOrgNr reports whether a normalized org.nr is one we may key the store
// directory on. A wrong number gets learned and then renames other people's
// receipts, so it must pass the Luhn check digit, and its third digit must be
// at least 2: org numbers put 20+ in the month position, which a personnummer
// (a customer's, printed on the same receipt) never does.
func ValidOrgNr(normalized string) bool {
	if len(normalized) != 10 || orgNrDigitsRe.MatchString(normalized) {
		return false
	}
	return normalized[2] >= '2' && luhnValid(normalized)
}

// luhnValid checks the Luhn check digit that closes every Swedish org.nr.
func luhnValid(digits string) bool {
	sum := 0
	double := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
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
	Subtotal *float64
	Tax      *float64
	Total    *float64
}

func (u ReceiptUpdate) IsEmpty() bool {
	return u.Merchant == nil && u.Date == nil &&
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
