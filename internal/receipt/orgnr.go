package receipt

import "regexp"

var (
	orgNrLabelledRe = regexp.MustCompile(
		`(?i)org(?:anisations)?[\s.]*n(?:umme)?r[\s.:]*(\d{6}\s?-?\s?\d{4})`)

	orgNrBareRe = regexp.MustCompile(`\b(\d{6}\s?-\s?\d{4})\b`)
)

func FindOrgNr(raw string) string {
	if m := orgNrLabelledRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	if m := orgNrBareRe.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return ""
}
