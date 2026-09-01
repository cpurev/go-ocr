package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cpurev/go-ocr/internal/model"
)

func ptr[T any](v T) *T { return &v }

func showUpdate(u model.ReceiptUpdate) string {
	var b strings.Builder

	text := func(name string, v *string) {
		if v != nil {
			fmt.Fprintf(&b, " %s=%q", name, *v)
		}
	}
	money := func(name string, v *float64) {
		if v != nil {
			fmt.Fprintf(&b, " %s=%g", name, *v)
		}
	}

	text("merchant", u.Merchant)
	text("date", u.Date)
	text("currency", u.Currency)
	money("subtotal", u.Subtotal)
	money("tax", u.Tax)
	money("total", u.Total)

	if b.Len() == 0 {
		return "(no fields)"
	}
	return strings.TrimSpace(b.String())
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		text   string
		verb   string
		number int
		limit  int
		update model.ReceiptUpdate
		errSet bool
	}{
		{text: "help", verb: "help"},
		{text: "?", verb: "help"},
		{text: "commands", verb: "help"},
		{text: "HELP", verb: "help"},

		{text: "who", verb: "who"},
		{text: "relay", verb: "who"},

		{text: "  stores  ", verb: "stores"},
		{text: "shops", verb: "stores"},
		{text: "merchants", verb: "stores"},

		{text: "last", verb: "last", limit: defaultRecent},
		{text: "recent", verb: "last", limit: defaultRecent},
		{text: "last 7", verb: "last", limit: 7},

		{text: "edit 7 merchant: ICA", verb: "edit", number: 7,
			update: model.ReceiptUpdate{Merchant: ptr("ICA")}},
		{text: "edit#7 total: 154,53", verb: "edit", number: 7,
			update: model.ReceiptUpdate{Total: ptr(154.53)}},
		{text: "edit 7 total: 154.53, date: 2026-08-04", verb: "edit", number: 7,
			update: model.ReceiptUpdate{Total: ptr(154.53), Date: ptr("2026-08-04")}},
		{text: "edit 7 merchant: Willys, currency: SEK", verb: "edit", number: 7,
			update: model.ReceiptUpdate{Merchant: ptr("Willys"), Currency: ptr("SEK")}},

		{text: "edit 0 merchant: ICA", verb: "edit", errSet: true},
		{text: "edit 7", verb: "edit", number: 7, errSet: true},

		{text: ""},
		{text: "   "},
		{text: "picking up milk"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.text), func(t *testing.T) {
			v, cmd, ok := parseCommand(tt.text)

			if tt.verb == "" {
				if ok {
					t.Fatalf("%q matched the %q verb, want it relayed as chat", tt.text, v.Name)
				}
				return
			}

			if !ok {
				t.Fatalf("%q was relayed as chat, want the %q verb", tt.text, tt.verb)
			}
			if v.Name != tt.verb {
				t.Fatalf("%q matched the %q verb, want %q", tt.text, v.Name, tt.verb)
			}
			if (cmd.Err != nil) != tt.errSet {
				t.Fatalf("%q parsed with err %v, want an error: %v", tt.text, cmd.Err, tt.errSet)
			}
			if cmd.Number != tt.number {
				t.Errorf("%q parsed receipt number %d, want %d", tt.text, cmd.Number, tt.number)
			}
			if cmd.Limit != tt.limit {
				t.Errorf("%q parsed limit %d, want %d", tt.text, cmd.Limit, tt.limit)
			}
			if got, want := showUpdate(cmd.Update), showUpdate(tt.update); got != want {
				t.Errorf("%q parsed update %s, want %s", tt.text, got, want)
			}
		})
	}
}

func TestParseEditFindsTheReceiptNumber(t *testing.T) {
	for _, args := range []string{"7 merchant: ICA", "#7 merchant: ICA", "  #  7 merchant: ICA"} {
		cmd := parseEdit(args)

		if cmd.Err != nil {
			t.Errorf("parseEdit(%q) failed with %v", args, cmd.Err)
			continue
		}
		if cmd.Number != 7 {
			t.Errorf("parseEdit(%q) found receipt %d, want 7", args, cmd.Number)
		}
		if got, want := showUpdate(cmd.Update), `merchant="ICA"`; got != want {
			t.Errorf("parseEdit(%q) parsed update %s, want %s", args, got, want)
		}
	}
}

func TestNoVerbWordIsRegisteredTwice(t *testing.T) {
	owner := make(map[string]string)

	for _, v := range verbs {
		for _, word := range append([]string{v.Name}, v.Aliases...) {
			if first, taken := owner[word]; taken {
				t.Errorf("%q is registered by both the %q verb and the %q verb", word, first, v.Name)
				continue
			}
			owner[word] = v.Name
		}
	}
}

func TestHelpTextDocumentsEveryVerb(t *testing.T) {
	for _, v := range verbs {
		if v.Usage == "" {
			t.Errorf("the %q verb has no Usage, so help cannot document it", v.Name)
			continue
		}
		if !strings.Contains(helpText, v.Usage) {
			t.Errorf("help text has no line for the %q verb, want it to contain %q", v.Name, v.Usage)
		}
	}
}

func TestParseLast(t *testing.T) {
	tests := []struct {
		args   string
		limit  int
		errSet bool
	}{
		{args: "", limit: defaultRecent},
		{args: "5", limit: 5},
		{args: "  5  ", limit: 5},
		{args: "1", limit: 1},
		{args: "20", limit: maxRecent},
		{args: "21", limit: maxRecent},
		{args: "999", limit: maxRecent},
		{args: "0", errSet: true},
		{args: "-1", errSet: true},
		{args: "five", errSet: true},
		{args: "5 receipts", errSet: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd := parseLast(tt.args)

			if (cmd.Err != nil) != tt.errSet {
				t.Fatalf("parseLast(%q) gave err %v, want an error: %v", tt.args, cmd.Err, tt.errSet)
			}
			if cmd.Limit != tt.limit {
				t.Errorf("parseLast(%q) asks for %d receipts, want %d", tt.args, cmd.Limit, tt.limit)
			}
		})
	}
}
