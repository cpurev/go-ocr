package api

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cpurev/go-ocr/internal/model"
)

type CommandKind int

const (
	CommandNone CommandKind = iota

	CommandEdit

	CommandHelp

	CommandStores
)

type Command struct {
	Kind CommandKind

	Number int

	Update model.ReceiptUpdate

	Err error
}

var (
	editPrefixRe = regexp.MustCompile(`(?i)^\s*edit\s*#?\s*(\d+)\s*(.*)$`)

	fieldRe = regexp.MustCompile(
		`(?i)\b(merchant|shop|store|total|sum|subtotal|tax|vat|moms|currency|date)\b\s*[:=]?\s*`)
)

func ParseCommand(text string) Command {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return Command{Kind: CommandNone}
	}

	switch strings.ToLower(trimmed) {
	case "help", "?", "commands":
		return Command{Kind: CommandHelp}
	case "stores", "shops", "merchants":
		return Command{Kind: CommandStores}
	}

	m := editPrefixRe.FindStringSubmatch(trimmed)
	if m == nil {
		return Command{Kind: CommandNone}
	}

	number, err := strconv.Atoi(m[1])
	if err != nil || number <= 0 {
		return Command{Kind: CommandEdit, Err: fmt.Errorf("receipt number must be a positive number")}
	}

	update, err := parseFields(m[2])
	if err != nil {
		return Command{Kind: CommandEdit, Number: number, Err: err}
	}
	if update.IsEmpty() {
		return Command{Kind: CommandEdit, Number: number,
			Err: fmt.Errorf("name at least one field to change, e.g. merchant: ICA")}
	}

	return Command{Kind: CommandEdit, Number: number, Update: update}
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

const HelpText = `*Receipt bot*

Send a photo of a receipt and I'll read it.

Correct one I got wrong:
edit 7 merchant: ICA
edit 7 total: 154.53, date: 2026-08-04
edit 7 merchant: Willys, currency: SEK

Fields: merchant, total, subtotal, tax, currency, date

Correcting a merchant teaches me that shop, so the next receipt from the same
company gets the name automatically.

Type *stores* to see what I've learned.`
