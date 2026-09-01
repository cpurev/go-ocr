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

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// testNow is the clock every parser test reads. UTC keeps it independent of the
// machine's zoneinfo, which the api package does not embed.
var testNow = time.Date(2026, time.September, 15, 12, 0, 0, 0, time.UTC)

// parseCommandCase pins one message in both directions. An empty verb means the
// text must be relayed to the other phone untouched.
type parseCommandCase struct {
	text    string
	verb    string
	number  int
	limit   int
	update  model.ReceiptUpdate
	wantErr string
}

var parseCommandTests = []parseCommandCase{
	{text: "help", verb: "help"},
	{text: "HELP", verb: "help"},
	{text: "help me carry the bags"},
	{text: "?", verb: "help"},
	{text: "?! what was that"},
	{text: "commands", verb: "help"},
	{text: "commands are hard to remember"},

	{text: "who", verb: "who"},
	{text: "who is coming tonight"},
	{text: "relay", verb: "who"},
	{text: "relay that to him for me"},

	{text: "  stores  ", verb: "stores"},
	{text: "stores are closed today"},
	{text: "shops", verb: "stores"},
	{text: "shops close at six"},
	{text: "merchants", verb: "stores"},
	{text: "merchants of venice on friday"},

	{text: "last", verb: "last", limit: defaultRecent},
	{text: "last 7", verb: "last", limit: 7},
	{text: "last night was fun"},
	{text: "last 0", verb: "last", wantErr: "how many? try: last 5"},
	{text: "recent", verb: "last", limit: defaultRecent},
	{text: "recent photos are all gone"},

	{text: "total", verb: "total"},
	{text: "total all", verb: "total"},
	{text: "total last month", verb: "total"},
	{text: "total 2026-08", verb: "total"},
	{text: "total all last month", verb: "total"},
	{text: "total nonsense"},
	{text: "total 2026-99", verb: "total",
		wantErr: `I don't know the period "2026-99"; try "last month" or "2026-08"`},
	{text: "total 2026-8", verb: "total",
		wantErr: `I don't know the period "2026-8"; try "last month" or "2026-08"`},
	{text: "sum", verb: "total"},
	{text: "sum of the parts is bigger"},
	{text: "spent", verb: "total"},
	{text: "spent too much today"},

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
	{text: "edit 0 merchant: ICA", verb: "edit",
		wantErr: "receipt number must be a positive number"},
	{text: "edit 7", verb: "edit", number: 7,
		wantErr: "name at least one field to change, e.g. merchant: ICA"},
	{text: "edit 7 total: abc", verb: "edit", number: 7,
		wantErr: `total "abc" is not a number`},
	{text: "edit"},
	{text: "edit the shopping list"},
	// fieldRe reads a bare field noun anywhere in the args, so these two are
	// commands today. They are the widest the matcher still opens.
	{text: "edit my store list", verb: "edit",
		update: model.ReceiptUpdate{Merchant: ptr("list")}},
	{text: "delete 3 messages", verb: "delete",
		wantErr: "which receipt? name its number"},

	{text: "delete 7", verb: "delete", number: 7},
	{text: "delete#7", verb: "delete", number: 7},
	{text: "delete 7 and also 8", verb: "delete",
		wantErr: "which receipt? name its number"},
	{text: "delete"},
	{text: "delete that photo"},
	{text: "remove 7", verb: "delete", number: 7},
	{text: "remove your shoes please"},
	{text: "rm 7", verb: "delete", number: 7},
	{text: "rm the stains later"},

	{text: ""},
	{text: "   "},
	{text: "picking up milk"},
}

func TestParseCommand(t *testing.T) {
	for _, tt := range parseCommandTests {
		t.Run(fmt.Sprintf("%q", tt.text), func(t *testing.T) {
			v, cmd, ok := parseCommand(tt.text, testNow)

			if tt.verb == "" {
				if ok {
					t.Fatalf("%q ran the %q command, want it relayed to the other phone as chat",
						tt.text, v.Name)
				}
				return
			}

			if !ok {
				t.Fatalf("%q was relayed as chat, want the %q verb", tt.text, tt.verb)
			}
			if v.Name != tt.verb {
				t.Fatalf("%q matched the %q verb, want %q", tt.text, v.Name, tt.verb)
			}
			if got := errText(cmd.Err); got != tt.wantErr {
				t.Fatalf("%q answered %q, want %q", tt.text, got, tt.wantErr)
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

// Verb words are ordinary English too, so an alias that only ever appears in
// the table as a command is one production relay away from eating a sentence
// that starts with it.
func TestEveryVerbWordIsPinnedAsBothCommandAndChat(t *testing.T) {
	type coverage struct{ command, chat bool }

	seen := make(map[string]*coverage)
	for _, tt := range parseCommandTests {
		trimmed := strings.TrimSpace(tt.text)
		word := verbWord(trimmed)

		key := strings.ToLower(word)
		if _, known := verbIndex[key]; !known {
			continue
		}

		if seen[key] == nil {
			seen[key] = &coverage{}
		}
		if tt.verb != "" {
			seen[key].command = true
			continue
		}
		// A bare verb word relayed as chat says nothing about the gate, so only
		// a sentence that carries on past the word counts here.
		if strings.TrimSpace(trimmed[len(word):]) != "" {
			seen[key].chat = true
		}
	}

	for _, v := range verbs {
		for _, word := range append([]string{v.Name}, v.Aliases...) {
			c := seen[word]
			if c == nil || !c.command {
				t.Errorf("no case in parseCommandTests runs %q as a command", word)
				continue
			}

			// A word with no letters can only ever match alone, because any
			// text after it becomes part of the word and misses the index.
			if !allLetters(word) {
				continue
			}
			if !c.chat {
				t.Errorf("no case in parseCommandTests sends a sentence starting with %q "+
					"to the other phone, so nothing stops %q swallowing ordinary chat",
					word, word)
			}
		}
	}
}

func allLetters(word string) bool {
	for i := 0; i < len(word); i++ {
		if !isLetter(word[i]) {
			return false
		}
	}
	return word != ""
}

func TestParseEditFindsTheReceiptNumber(t *testing.T) {
	for _, args := range []string{"7 merchant: ICA", "#7 merchant: ICA", "  #  7 merchant: ICA"} {
		cmd, matched := parseEdit(args, testNow)

		if !matched {
			t.Errorf("parseEdit(%q) read the args as chat", args)
			continue
		}
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
		args    string
		number  int
		matched bool
		wantErr string
	}{
		{args: "7", number: 7, matched: true},
		{args: "#7", number: 7, matched: true},
		{args: "  #7  ", number: 7, matched: true},
		{args: "0", matched: true, wantErr: "receipt number must be a positive number"},
		{args: "7 now", matched: true, wantErr: "which receipt? name its number"},
		{args: ""},
		{args: "now"},
		{args: "abc"},
		{args: "that photo"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd, matched := parseDelete(tt.args, testNow)

			if matched != tt.matched {
				t.Fatalf("parseDelete(%q) matched: %v, want %v", tt.args, matched, tt.matched)
			}
			if got := errText(cmd.Err); got != tt.wantErr {
				t.Fatalf("parseDelete(%q) answered %q, want %q", tt.args, got, tt.wantErr)
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
		args    string
		limit   int
		matched bool
		wantErr string
	}{
		{args: "", limit: defaultRecent, matched: true},
		{args: "5", limit: 5, matched: true},
		{args: "  5  ", limit: 5, matched: true},
		{args: "1", limit: 1, matched: true},
		{args: "20", limit: maxRecent, matched: true},
		{args: "21", limit: maxRecent, matched: true},
		{args: "999", limit: maxRecent, matched: true},
		{args: "0", matched: true, wantErr: "how many? try: last 5"},
		{args: "-1", matched: true, wantErr: "how many? try: last 5"},
		{args: "five"},
		{args: "5 receipts"},
		{args: "night was fun"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd, matched := parseLast(tt.args, testNow)

			if matched != tt.matched {
				t.Fatalf("parseLast(%q) matched: %v, want %v", tt.args, matched, tt.matched)
			}
			if got := errText(cmd.Err); got != tt.wantErr {
				t.Fatalf("parseLast(%q) answered %q, want %q", tt.args, got, tt.wantErr)
			}
			if cmd.Limit != tt.limit {
				t.Errorf("parseLast(%q) asks for %d receipts, want %d", tt.args, cmd.Limit, tt.limit)
			}
		})
	}
}

func TestParseTotal(t *testing.T) {
	tests := []struct {
		args    string
		label   string
		scope   Scope
		matched bool
		wantErr string
	}{
		{args: "", label: "September 2026", scope: scopeSender, matched: true},
		{args: "this month", label: "September 2026", scope: scopeSender, matched: true},
		{args: "last month", label: "August 2026", scope: scopeSender, matched: true},
		{args: "2026-08", label: "August 2026", scope: scopeSender, matched: true},
		{args: "ever", label: "all time", scope: scopeSender, matched: true},
		{args: "all", label: "September 2026", scope: scopeEveryone, matched: true},
		{args: "all last month", label: "August 2026", scope: scopeEveryone, matched: true},
		{args: "last month all", label: "August 2026", scope: scopeEveryone, matched: true},
		{args: "last  month", label: "August 2026", scope: scopeSender, matched: true},
		{args: "ALL ever", label: "all time", scope: scopeEveryone, matched: true},
		{args: "2026-99", matched: true,
			wantErr: `I don't know the period "2026-99"; try "last month" or "2026-08"`},
		{args: "all 2026-99", matched: true,
			wantErr: `I don't know the period "2026-99"; try "last month" or "2026-08"`},
		{args: "fallout"},
		{args: "too much on coffee"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.args), func(t *testing.T) {
			cmd, matched := parseTotal(tt.args, testNow)

			if matched != tt.matched {
				t.Fatalf("parseTotal(%q) matched: %v, want %v", tt.args, matched, tt.matched)
			}
			if got := errText(cmd.Err); got != tt.wantErr {
				t.Fatalf("parseTotal(%q) answered %q, want %q", tt.args, got, tt.wantErr)
			}
			if tt.wantErr != "" || !matched {
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
