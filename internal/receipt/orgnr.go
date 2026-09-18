package receipt

import (
	"regexp"

	"github.com/cpurev/go-ocr/internal/model"
)

var (
	// [ \t] rather than \s: a label must sit on the same line as its number,
	// or "Org.nr" at the end of one line would claim the next line's digits.
	// Some receipts print the VAT form ("SE556036079301") under this label.
	orgNrLabelledRe = regexp.MustCompile(
		`(?i)org(?:anisations)?[ \t.]*n(?:umme)?r[ \t.:]*(?:SE[ \t]?)?(\d{6}[ \t]?-?[ \t]?\d{4})`)

	// The VAT number is the org.nr wrapped as SE + 10 digits + 01. Receipts
	// that print only this form still name the store.
	vatNrLabelledRe = regexp.MustCompile(
		`(?i)(?:moms[ \t.]*reg\w*|vat)[^\n\d]{0,20}?SE[ \t]?(\d{6}[ \t]?-?[ \t]?\d{4})[ \t]?01\b`)

	orgNrBareRe = regexp.MustCompile(`\b(\d{6}[ \t]?-[ \t]?\d{4})\b`)
)

// FindOrgNr returns the store's org.nr as "dddddd-dddd", or "" when the text
// has none we trust. The number keys the store directory, so a wrong one is
// learned and renames later receipts: labelled numbers win over bare ones,
// and every candidate must pass model.ValidOrgNr. That check is what keeps a
// customer's personnummer ("Kund 850101-1236") from being read as the shop.
func FindOrgNr(raw string) string {
	for _, re := range []*regexp.Regexp{orgNrLabelledRe, vatNrLabelledRe, orgNrBareRe} {
		for _, m := range re.FindAllStringSubmatch(raw, -1) {
			if nr := model.NormalizeOrgNr(m[1]); model.ValidOrgNr(nr) {
				return nr[:6] + "-" + nr[6:]
			}
		}
	}
	return ""
}
