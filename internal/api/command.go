package api

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cpurev/go-ocr/internal/model"
)

type Command struct {
	Number int

	Limit int

	Update model.ReceiptUpdate

	Err error
}

var (
	editNumberRe = regexp.MustCompile(`^\s*#?\s*(\d+)\s*`)

	fieldRe = regexp.MustCompile(
		`(?i)\b(merchant|shop|store|total|sum|subtotal|tax|vat|moms|currency|date)\b\s*[:=]?\s*`)
)

// parseCommand takes the leading run of letters as the verb word. A text with
// no leading letter is its own word, which is what lets "?" be an alias.
func parseCommand(text string) (*verb, Command, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, Command{}, false
	}

	end := 0
	for end < len(trimmed) && isLetter(trimmed[end]) {
		end++
	}

	word, args := trimmed, ""
	if end > 0 {
		word, args = trimmed[:end], strings.TrimSpace(trimmed[end:])
	}

	v, ok := verbIndex[strings.ToLower(word)]
	if !ok {
		return nil, Command{}, false
	}

	return v, v.Parse(args), true
}

func isLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func noArgs(string) Command { return Command{} }

const (
	defaultRecent = 5
	maxRecent     = 20
)

func parseLast(args string) Command {
	args = strings.TrimSpace(args)
	if args == "" {
		return Command{Limit: defaultRecent}
	}

	howMany, err := strconv.Atoi(args)
	if err != nil || howMany < 1 {
		return Command{Err: errors.New("how many? try: last 5")}
	}
	if howMany > maxRecent {
		howMany = maxRecent
	}

	return Command{Limit: howMany}
}

func parseEdit(args string) Command {
	number, tail := 0, args

	if m := editNumberRe.FindStringSubmatchIndex(args); m != nil {
		n, err := strconv.Atoi(args[m[2]:m[3]])
		if err != nil || n <= 0 {
			return Command{Err: errors.New("receipt number must be a positive number")}
		}
		number, tail = n, args[m[1]:]
	}

	update, err := parseFields(tail)
	if err != nil {
		return Command{Number: number, Err: err}
	}
	if update.IsEmpty() {
		return Command{Number: number,
			Err: errors.New("name at least one field to change, e.g. merchant: ICA")}
	}

	return Command{Number: number, Update: update}
}

// A wrong edit is repairable and a wrong delete is not, so the destructive verb
// is the one that pays for aim.
func parseDelete(args string) Command {
	m := editNumberRe.FindStringSubmatchIndex(args)
	if m == nil || strings.TrimSpace(args[m[1]:]) != "" {
		return Command{Err: errors.New("which receipt? name its number")}
	}

	number, err := strconv.Atoi(args[m[2]:m[3]])
	if err != nil || number <= 0 {
		return Command{Err: errors.New("receipt number must be a positive number")}
	}

	return Command{Number: number}
}

func parseFields(tail string) (model.ReceiptUpdate, error) {
	var update model.ReceiptUpdate

	locs := fieldRe.FindAllStringSubmatchIndex(tail, -1)
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

func assignField(update *model.ReceiptUpdate, name, value string) error {
	switch name {
	case "merchant", "shop", "store":
		update.Merchant = &value

	case "date":
		update.Date = &value

	case "currency":
		upper := strings.ToUpper(value)
		update.Currency = &upper

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
