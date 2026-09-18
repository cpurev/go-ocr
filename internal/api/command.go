package api

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/model"
)

type Command struct {
	Number int

	Limit int

	Update model.ReceiptUpdate

	// Total, Merchant, and Date are add's fields, for a receipt with no photo.
	Total    float64
	Merchant string
	Date     string

	Period Period
	Scope  Scope

	Err error
}

var (
	editNumberRe = regexp.MustCompile(`^\s*#?\s*(\d+)\s*`)

	// leadingAmountRe requires the amount to open the message, so "add" without
	// one relays as chat instead of misreading whatever comes after it. A
	// currency word on either side of the amount is consumed with it: every
	// receipt is SEK, and left in place it became the front of the merchant.
	// The word must end at whitespace, not \b, which is ASCII-only in RE2 and
	// would read "Krögers" as "kr" plus "ögers".
	leadingAmountRe = regexp.MustCompile(
		`(?i)^(?:(?:sek|kr)\.?\s*)?(\d+(?:[.,]\d+)?)\s*(?:(?:sek|kronor|kr)\.?(?:\s+|$)|:-)?\s*`)

	fieldRe = regexp.MustCompile(
		`(?i)\b(merchant|shop|store|total|sum|subtotal|tax|vat|moms|date)\b\s*[:=]?\s*`)

	// fieldAssignRe decides whether edit was meant at all, where fieldRe only
	// reads the fields once that is settled. The separator is what separates
	// them: without it "edit my store list" would set merchant to "list" on the
	// newest receipt, tell both phones, and teach the store directory that name.
	fieldAssignRe = regexp.MustCompile(
		`(?i)\b(merchant|shop|store|total|sum|subtotal|tax|vat|moms|date)\b\s*[:=]`)
)

// parseCommand matches the leading word against the verb table. The word alone
// is not enough, because most of the table is also ordinary English. The rest of
// the message has to parse as that verb's arguments before it is a command.
func parseCommand(text string, now time.Time) (*verb, Command, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, Command{}, false
	}

	word := verbWord(trimmed)
	args := strings.TrimSpace(trimmed[len(word):])

	v, ok := verbIndex[strings.ToLower(word)]
	if !ok {
		return nil, Command{}, false
	}

	cmd, matched := v.Parse(args, now)
	if !matched {
		return nil, Command{}, false
	}

	return v, cmd, true
}

// verbWord is the leading run of letters. A text with no leading letter is its
// own word, which is what lets "?" be an alias and what stops "?!" being one.
func verbWord(trimmed string) string {
	end := 0
	for end < len(trimmed) && isLetter(trimmed[end]) {
		end++
	}
	if end == 0 {
		return trimmed
	}

	return trimmed[:end]
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func noArgs(args string, _ time.Time) (Command, bool) {
	return Command{}, strings.TrimSpace(args) == ""
}

const (
	defaultRecent = 5
	maxRecent     = 20
)

func parseLast(args string, _ time.Time) (Command, bool) {
	args = strings.TrimSpace(args)
	if args == "" {
		return Command{Limit: defaultRecent}, true
	}

	howMany, err := strconv.Atoi(args)
	if err != nil {
		return Command{}, false
	}
	// A number too small to count out is still unmistakably a count, not chat.
	if howMany < 1 {
		return Command{Err: errors.New("how many? try: last 5")}, true
	}
	if howMany > maxRecent {
		howMany = maxRecent
	}

	return Command{Limit: howMany}, true
}

func parseEdit(args string, _ time.Time) (Command, bool) {
	m := editNumberRe.FindStringSubmatchIndex(args)
	if m == nil && !fieldAssignRe.MatchString(args) {
		return Command{}, false
	}

	number, tail := 0, args
	if m != nil {
		n, err := strconv.Atoi(args[m[2]:m[3]])
		if err != nil || n <= 0 {
			return Command{Err: errors.New("receipt number must be a positive number")}, true
		}
		number, tail = n, args[m[1]:]
	}

	update, err := parseFields(tail)
	if err != nil {
		return Command{Number: number, Err: err}, true
	}
	if update.IsEmpty() {
		return Command{Number: number,
			Err: errors.New("name at least one field to change, e.g. merchant: ICA")}, true
	}

	return Command{Number: number, Update: update}, true
}

// A wrong edit is repairable and a wrong delete is not, so the destructive verb
// is the one that pays for aim.
func parseDelete(args string, _ time.Time) (Command, bool) {
	m := editNumberRe.FindStringSubmatchIndex(args)
	if m == nil {
		return Command{}, false
	}
	if strings.TrimSpace(args[m[1]:]) != "" {
		return Command{Err: errors.New("which receipt? name its number")}, true
	}

	number, err := strconv.Atoi(args[m[2]:m[3]])
	if err != nil || number <= 0 {
		return Command{Err: errors.New("receipt number must be a positive number")}, true
	}

	return Command{Number: number}, true
}

// parseAdd reads "150 ICA" as a receipt with no photo: the leading number is
// the total, the rest of the line is the merchant. A message that does not
// open with a number is not this verb's arguments at all, since "add" alone
// is ordinary English too.
func parseAdd(args string, now time.Time) (Command, bool) {
	args = strings.TrimSpace(args)

	m := leadingAmountRe.FindStringSubmatchIndex(args)
	if m == nil {
		return Command{}, false
	}

	amount, err := parseMoney(args[m[2]:m[3]])
	if err != nil || amount <= 0 {
		return Command{Err: errors.New("amount must be a positive number")}, true
	}

	merchant := strings.TrimSpace(args[m[1]:])
	if merchant == "" {
		return Command{Err: errors.New("name the shop, e.g. add 150 ICA")}, true
	}
	if looksLikeShopping(merchant) {
		return Command{}, false
	}

	return Command{Total: amount, Merchant: merchant, Date: now.Format(model.DateLayout)}, true
}

// unitWords open a quantity rather than a shop: "add 2 liters of milk".
var unitWords = map[string]bool{
	"l": true, "liter": true, "liters": true, "litre": true, "litres": true,
	"kg": true, "kilo": true, "g": true, "gram": true, "st": true, "pcs": true,
	"pieces": true, "pack": true, "packs": true, "bottle": true, "bottles": true,
	"can": true, "cans": true, "cups": true, "dozen": true, "more": true, "x": true,
}

// looksLikeShopping reports an add that is a shopping-list note, not a receipt.
// "add 150 ICA" has to stay a receipt with no currency word, so the test is
// on what follows the number: a unit, or the list it is being added to.
func looksLikeShopping(merchant string) bool {
	words := strings.Fields(strings.ToLower(merchant))
	if len(words) > 0 && unitWords[strings.Trim(words[0], ".,")] {
		return true
	}
	lower := " " + strings.Join(words, " ") + " "
	return strings.Contains(lower, " to the list ") || strings.Contains(lower, " to list ") ||
		strings.Contains(lower, " to the shopping ")
}

func parseTotal(args string, now time.Time) (Command, bool) {
	// The rejoined remainder is used either way, so "total last  month" parses
	// the same as "total all last  month" rather than only the latter.
	rest, everyone := stripToken(args, "all")

	period, err := ParsePeriod(rest, now)
	if errors.Is(err, ErrNotAPeriod) {
		return Command{}, false
	}
	if err != nil {
		return Command{Err: err}, true
	}

	scope := scopeSender
	if everyone {
		scope = scopeEveryone
	}

	return Command{Period: period, Scope: scope}, true
}

// stripToken removes a whole word, so the "all" inside "fallout" is left where
// it is and reaches ParsePeriod as part of the period.
func stripToken(args, token string) (string, bool) {
	fields := strings.Fields(args)
	kept, found := make([]string, 0, len(fields)), false

	for _, f := range fields {
		if strings.EqualFold(f, token) {
			found = true
			continue
		}
		kept = append(kept, f)
	}

	return strings.Join(kept, " "), found
}

func parseFields(tail string) (model.ReceiptUpdate, error) {
	var update model.ReceiptUpdate

	locs := fieldStarts(tail)
	if len(locs) == 0 {
		return update, nil
	}

	for i, loc := range locs {
		name := strings.ToLower(tail[loc[2]:loc[3]])

		end := len(tail)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		value := strings.Trim(tail[loc[1]:end], " \t,;.")

		if value == "" {
			return update, fmt.Errorf("%s is missing a value", name)
		}

		if err := assignField(&update, name, value); err != nil {
			return update, err
		}
	}

	return update, nil
}

// fieldStarts keeps the field words that begin a field, dropping the ones that
// are part of a value: "Pizza Total", "Apple Store Täby", "The Body Shop". A
// word begins a field when it carries a separator, opens the text or follows a
// comma, or is a number field immediately followed by a digit ("ICA total 150").
func fieldStarts(tail string) [][]int {
	var starts [][]int
	for _, loc := range fieldRe.FindAllStringSubmatchIndex(tail, -1) {
		name := strings.ToLower(tail[loc[2]:loc[3]])
		keyword := tail[loc[0]:loc[1]]
		before := strings.TrimRight(tail[:loc[0]], " \t")
		rest := tail[loc[1]:]

		switch {
		case strings.ContainsAny(keyword, ":="),
			before == "",
			strings.HasSuffix(before, ",") || strings.HasSuffix(before, ";"),
			numericField(name) && rest != "" && rest[0] >= '0' && rest[0] <= '9':
			starts = append(starts, loc)
		}
	}
	return starts
}

func numericField(name string) bool {
	switch name {
	case "total", "sum", "subtotal", "tax", "vat", "moms", "date":
		return true
	}
	return false
}

func assignField(update *model.ReceiptUpdate, name, value string) error {
	switch name {
	case "merchant", "shop", "store":
		update.Merchant = &value

	case "date":
		update.Date = &value

	case "total", "sum":
		amount, err := parseMoney(value)
		if err != nil {
			return fmt.Errorf("total %q is not a number", value)
		}
		update.Total = &amount

	case "subtotal":
		amount, err := parseMoney(value)
		if err != nil {
			return fmt.Errorf("subtotal %q is not a number", value)
		}
		update.Subtotal = &amount

	case "tax", "vat", "moms":
		amount, err := parseMoney(value)
		if err != nil {
			return fmt.Errorf("tax %q is not a number", value)
		}
		update.Tax = &amount
	}

	return nil
}

func parseMoney(s string) (float64, error) {
	s = strings.TrimSpace(s)

	s = strings.ReplaceAll(s, ",", ".")
	var b strings.Builder
	for _, r := range s {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			b.WriteRune(r)
		}
	}

	cleaned := b.String()
	if cleaned == "" {
		return 0, fmt.Errorf("no number found")
	}

	return strconv.ParseFloat(cleaned, 64)
}
