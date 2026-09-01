package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

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

// testNow is the clock every parser test reads. UTC keeps it independent of the
// machine's zoneinfo, which the api package does not embed.
var testNow = time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)

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

		{text: "edit merchant: ICA", verb: "edit",
			update: model.ReceiptUpdate{Merchant: ptr("ICA")}},
		{text: "edit total: 154.53", verb: "edit",
			update: model.ReceiptUpdate{Total: ptr(154.53)}},

		{text: "edit 0 merchant: ICA", verb: "edit", errSet: true},
		{text: "edit 7", verb: "edit", number: 7, errSet: true},

		{text: "delete 7", verb: "delete", number: 7},
		{text: "delete#7", verb: "delete", number: 7},
		{text: "remove 7", verb: "delete", number: 7},
		{text: "rm 7", verb: "delete", number: 7},
		{text: "delete", verb: "delete", errSet: true},

		{text: "total", verb: "total"},
		{text: "sum", verb: "total"},
		{text: "spent", verb: "total"},
		{text: "total last month", verb: "total"},
		{text: "total xyzzy", verb: "total", errSet: true},

		{text: ""},
		{text: "   "},
		{text: "picking up milk"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.text), func(t *testing.T) {
			v, cmd, ok := parseCommand(tt.text, testNow)

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
		cmd := parseEdit(args, testNow)

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

func TestParseDelete(t *testing.T) {
	tests := []struct {
		args   string
		number int
		errSet bool
	}{
		{args: "7", number: 7},
		{args: "#7", number: 7},
		{args: "  #7  ", number: 7},
		{args: "", errSet: true},
		{args: "now", errSet: true},
		{args: "0", errSet: true},
		{args: "abc", errSet: true},
		{args: "7 now", errSet: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd := parseDelete(tt.args, testNow)

			if (cmd.Err != nil) != tt.errSet {
				t.Fatalf("parseDelete(%q) gave err %v, want an error: %v", tt.args, cmd.Err, tt.errSet)
			}
			if cmd.Number != tt.number {
				t.Errorf("parseDelete(%q) targets receipt #%d, want #%d", tt.args, cmd.Number, tt.number)
			}
		})
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

// A capital in a Name or Alias would never match, because parseCommand
// lowercases the word before the index lookup.
func TestEveryVerbWordIsLowercase(t *testing.T) {
	for _, v := range verbs {
		for _, word := range append([]string{v.Name}, v.Aliases...) {
			if word != strings.ToLower(word) {
				t.Errorf("the %q verb registers %q, which no message can ever match: "+
					"parseCommand lowercases the word before looking it up", v.Name, word)
			}
		}
	}
}

func TestEveryVerbAnswersTheRightAudience(t *testing.T) {
	names := map[Audience]string{
		audienceSender: "the sender", audienceEveryone: "everyone", audienceOthers: "the others",
	}
	want := map[string]Audience{
		"help":   audienceSender,
		"who":    audienceSender,
		"stores": audienceSender,
		"last":   audienceSender,
		"total":  audienceSender,
		"edit":   audienceEveryone,
		"delete": audienceEveryone,
	}

	if len(verbs) != len(want) {
		t.Fatalf("the table has %d verbs and this test names %d; a new verb has to say "+
			"who sees its answer", len(verbs), len(want))
	}

	for _, v := range verbs {
		expected, named := want[v.Name]
		if !named {
			t.Errorf("the %q verb is not named here, so nothing pins who sees its answer", v.Name)
			continue
		}
		if v.Audience != expected {
			t.Errorf("the %q verb answers %s, want %s: a query must not buzz the other "+
				"phone, and anything that changed shared state must",
				v.Name, names[v.Audience], names[expected])
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
			cmd := parseLast(tt.args, testNow)

			if (cmd.Err != nil) != tt.errSet {
				t.Fatalf("parseLast(%q) gave err %v, want an error: %v", tt.args, cmd.Err, tt.errSet)
			}
			if cmd.Limit != tt.limit {
				t.Errorf("parseLast(%q) asks for %d receipts, want %d", tt.args, cmd.Limit, tt.limit)
			}
		})
	}
}

func TestParseTotal(t *testing.T) {
	tests := []struct {
		args   string
		label  string
		scope  Scope
		errSet bool
	}{
		{args: "", label: "September 2026", scope: scopeSender},
		{args: "this month", label: "September 2026", scope: scopeSender},
		{args: "last month", label: "August 2026", scope: scopeSender},
		{args: "2026-08", label: "August 2026", scope: scopeSender},
		{args: "ever", label: "all time", scope: scopeSender},
		{args: "all", label: "September 2026", scope: scopeEveryone},
		{args: "all last month", label: "August 2026", scope: scopeEveryone},
		{args: "last month all", label: "August 2026", scope: scopeEveryone},
		{args: "last  month", label: "August 2026", scope: scopeSender},
		{args: "ALL ever", label: "all time", scope: scopeEveryone},
		{args: "fallout", errSet: true},
		{args: "2026-99", errSet: true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd := parseTotal(tt.args, testNow)

			if (cmd.Err != nil) != tt.errSet {
				t.Fatalf("parseTotal(%q) gave err %v, want an error: %v", tt.args, cmd.Err, tt.errSet)
			}
			if tt.errSet {
				return
			}
			if cmd.Period.Label != tt.label {
				t.Errorf("parseTotal(%q) covers %q, want %q", tt.args, cmd.Period.Label, tt.label)
			}
			if cmd.Scope != tt.scope {
				t.Errorf("parseTotal(%q) has scope %d, want %d", tt.args, cmd.Scope, tt.scope)
			}
		})
	}
}
