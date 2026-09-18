package receipt

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cpurev/go-ocr/internal/model"
)

type Parser struct {
	DayFirst bool
}

func New(dayFirst bool) *Parser {
	return &Parser{DayFirst: dayFirst}
}

const currencyCodes = `USD|EUR|GBP|JPY|CNY|KRW|INR|MNT|AUD|CAD|CHF|SGD|THB|RUB|` +
	`SEK|NOK|DKK|ISK|PLN|CZK|HUF|RON|BGN|TRY|UAH|ZAR|BRL|MXN|CLP|COP|ARS|` +
	`NZD|HKD|TWD|IDR|MYR|PHP|VND|AED|SAR|QAR|ILS|EGP|NGN|KES|PKR|BDT|LKR`

var (
	amountRe = regexp.MustCompile(`[-+]?[$€£¥₮₩₹]?\s?(?:\d{1,3}(?:[ ,.]\d{3})+|\d+)(?:[.,]\d{1,2})?`)

	isoDateRe = regexp.MustCompile(`\b(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})\b`)

	numericDateRe = regexp.MustCompile(`\b(\d{1,2})[-/.](\d{1,2})[-/.](\d{2,4})\b`)

	// [ \t]+, not \s+: a date is printed on one line. \s+ let "3,05" and
	// "Marabou 29,90" on the next line read as 5 March 2029.
	textDateDMYRe = regexp.MustCompile(`(?i)\b(\d{1,2})[ \t]+([a-z]{3,9})\.?,?[ \t]+(\d{2,4})\b`)
	textDateMDYRe = regexp.MustCompile(`(?i)\b([a-z]{3,9})\.?[ \t]+(\d{1,2}),?[ \t]+(\d{2,4})\b`)

	amountWithCodeRe = regexp.MustCompile(
		`([-+]?(?:\d{1,3}(?:[ ,.]\d{3})+|\d+)(?:[.,]\d{1,2})?)\s*(?:` + currencyCodes + `)\b`)

	maskedCardRe = regexp.MustCompile(`\*{2,}\s*\d|[xX]{4,}|[kK]{5,}`)

	qtyPrefixRe = regexp.MustCompile(`^(\d{1,3})\s*[xX*×]?\s+(.*)$`)

	qtySuffixRe = regexp.MustCompile(`^(.*?)\s+[xX×]\s*(\d{1,3})$`)

	qtyColumnRe = regexp.MustCompile(`^(\d{1,3}) (\d{3}(?:[.,]\d{1,2})?)$`)
)

var currencySymbols = []string{"$", "€", "£", "¥", "₮", "₩", "₹"}

var (
	subtotalWords = []string{"subtotal", "sub total", "sub-total", "net total", "net amount", "delsumma"}
	taxWords      = []string{"tax", "vat", "gst", "hst", "sales tax", "moms"}
	totalWords    = []string{"grand total", "total due", "amount due", "total amount", "balance due", "total",

		"att betala", "totalt", "summa", "belopp"}

	noiseWords = []string{
		"change", "cash", "card", "visa", "mastercard", "amex", "tender", "tendered",
		"payment", "auth", "approval", "terminal", "merchant id", "invoice", "receipt",
		"thank", "welcome", "customer copy", "tel", "phone", "www", "http", "order",
		"table", "server", "cashier", "ref", "trans", "date", "time", "qty", "item",

		"orgnr", "org nr", "aid:", "tsi:", "term:", "kvitto", "öppet", "oppet",
		"kontakt", "butik", "moms reg",
	}

	// paymentWords mark how the receipt was paid, not what was bought: "Kort
	// 35,00" repeats the total and "Tillbaka 15,00" is change. They are
	// matched as whole words because "tid" and "kort" are also parts of item
	// names ("Tidning", "Kortlek").
	paymentWords = []string{
		"kort", "bankkort", "kreditkort", "betalkort", "kortbetalning",
		"kontant", "kontanter", "kontantbetalning", "växel", "vaxel", "tillbaka",
		"betalt", "betalning", "erhållet", "swish", "kassa", "kassör",
		"datum", "tid", "telefon", "tel",
	}

	// registrationWords mark a line that carries an org.nr or VAT number. Its
	// digits are an identifier; read as money, "Momsreg.nr SE556036079301"
	// became a tax of 556 036 079 301 kr.
	registrationWords = []string{
		"org.nr", "org nr", "orgnr", "organisationsnummer", "momsreg", "moms reg",
		"reg.nr", "reg nr", "regnr", "vat no", "vat nr", "vat reg", "vat number",
	}

	// payableWords name the amount actually charged. They beat a bare
	// "Totalt", which some receipts also print before a deposit or discount.
	payableWords = []string{"att betala", "total due", "amount due", "balance due", "grand total"}

	// savingsWords mark a line that totals a discount ("Totalt sparat 5,00"),
	// not the receipt.
	savingsWords = []string{"spar", "rabatt", "avdrag", "discount", "you saved"}

	// "Totalt inkl. moms" is the total and "Summa exkl. moms" the net: the word
	// moms there qualifies the amount, it does not make the line the tax.
	inclVATWords = []string{"inkl", "incl"}
	exclVATWords = []string{"exkl", "excl", "ex moms", "ex. moms"}
)

type lineKind int

const (
	kindOther lineKind = iota
	kindSubtotal
	kindTax
	kindTotal
	kindPayable
)

func (p *Parser) Parse(raw string) model.ReceiptFields {
	fields := model.ReceiptFields{RawText: raw}

	lines := splitLines(raw)
	if len(lines) == 0 {
		return fields.Normalize()
	}

	fields.Merchant = p.findMerchant(lines)
	fields.Date = p.findDate(raw)
	fields.Subtotal, fields.Tax, fields.Total = p.findTotals(lines)
	fields.LineItems = p.findLineItems(lines)

	p.reconcile(&fields)

	return fields.Normalize()
}

func (p *Parser) findMerchant(lines []string) string {
	for i, line := range lines {
		if i > 6 {
			break
		}
		if !hasLetters(line) || containsAny(strings.ToLower(line), noiseWords) {
			continue
		}

		if digitRatio(line) > 0.3 {
			continue
		}

		if len(strings.Fields(line)) > 4 {
			continue
		}
		return strings.Trim(line, " \t*-=_.,:;")
	}
	return ""
}

func (p *Parser) findDate(raw string) string {
	if m := isoDateRe.FindStringSubmatch(raw); m != nil {
		if d, ok := buildDate(atoi(m[1]), atoi(m[2]), atoi(m[3])); ok {
			return d
		}
	}

	// A numeric date is unambiguous about which digits are the date; a text
	// pattern can still catch a price next to a word, so it only gets a turn
	// when no numeric date exists.
	if d := p.numericDate(raw); d != "" {
		return d
	}

	if m := textDateDMYRe.FindStringSubmatch(raw); m != nil {
		if month, ok := monthFromName(m[2]); ok {
			if d, ok := buildDate(expandYear(atoi(m[3])), month, atoi(m[1])); ok {
				return d
			}
		}
	}
	if m := textDateMDYRe.FindStringSubmatch(raw); m != nil {
		if month, ok := monthFromName(m[1]); ok {
			if d, ok := buildDate(expandYear(atoi(m[3])), month, atoi(m[2])); ok {
				return d
			}
		}
	}

	return ""
}

func (p *Parser) numericDate(raw string) string {
	if m := numericDateRe.FindStringSubmatch(raw); m != nil {
		first, second, year := atoi(m[1]), atoi(m[2]), expandYear(atoi(m[3]))

		day, month := first, second
		switch {
		case first > 12 && second <= 12:
			day, month = first, second
		case second > 12 && first <= 12:
			day, month = second, first
		case !p.DayFirst:
			day, month = second, first
		}

		if d, ok := buildDate(year, month, day); ok {
			return d
		}
	}

	return ""
}

func (p *Parser) findTotals(lines []string) (subtotal, tax, total float64) {
	var payable float64
	for i := len(lines) - 1; i >= 0; i-- {
		kind := classifyLine(strings.ToLower(lines[i]))
		if kind == kindOther {
			continue
		}

		if kind == kindTax {
			if amount, ok := taxAmount(lines[i]); ok && tax == 0 {
				tax = amount
			}
			continue
		}

		amount, ok := lastAmount(lines[i])
		if !ok {
			continue
		}
		switch {
		case kind == kindSubtotal && subtotal == 0:
			subtotal = amount
		case kind == kindPayable && payable == 0:
			payable = amount
		case kind == kindTotal && total == 0:
			total = amount
		}
	}
	if payable > 0 {
		total = payable
	}

	if total == 0 {
		total = amountWithCurrency(lines)
	}

	return subtotal, tax, total
}

// classifyLine says which of the receipt's sums a line states, if any.
// Registration and savings lines come first because they borrow the words of
// the others ("Momsreg.nr", "Totalt sparat"), and inkl/exkl moms come before
// the tax words because on those lines moms qualifies a total.
func classifyLine(lower string) lineKind {
	switch {
	case containsAny(lower, registrationWords), containsAny(lower, savingsWords):
		return kindOther
	case containsAny(lower, payableWords):
		return kindPayable
	case containsAny(lower, subtotalWords), containsAny(lower, exclVATWords):
		return kindSubtotal
	case containsAny(lower, inclVATWords):
		return kindTotal
	case containsAny(lower, taxWords):
		return kindTax
	case containsAny(lower, totalWords):
		return kindTotal
	}
	return kindOther
}

// taxAmount reads the tax from a VAT line. A VAT table row prints rate, tax,
// net and gross ("Moms 25% 25,60 102,40 128,00"), so the last amount is the
// gross. The tax is the smallest amount on the row: at every Swedish rate
// (6, 12, 25 %) it is below both net and gross, whatever the column order.
func taxAmount(line string) (float64, bool) {
	var best float64
	for _, text := range amountTexts(line) {
		v, ok := parseAmount(text)
		if !ok || v <= 0 {
			continue
		}
		if best == 0 || v < best {
			best = v
		}
	}
	return best, best > 0
}

// amountWithCurrency is the fallback total: the bottom-most "… SEK" amount
// not on a payment line. Tendered cash and change carry SEK too, and the
// change ("Tillbaka 15,00 SEK") is usually the last one printed.
func amountWithCurrency(lines []string) float64 {
	for i := len(lines) - 1; i >= 0; i-- {
		if containsWord(strings.ToLower(lines[i]), paymentWords) {
			continue
		}
		m := amountWithCodeRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		if amount, ok := parseAmount(m[1]); ok && amount > 0 {
			return amount
		}
	}
	return 0
}

func (p *Parser) findLineItems(lines []string) []model.LineItem {
	items := make([]model.LineItem, 0, len(lines))

	for _, line := range lines {
		lower := strings.ToLower(line)

		if containsAny(lower, subtotalWords) || containsAny(lower, taxWords) ||
			containsAny(lower, totalWords) || containsAny(lower, noiseWords) ||
			containsAny(lower, registrationWords) || containsWord(lower, paymentWords) {
			continue
		}

		if amountWithCodeRe.MatchString(line) {
			continue
		}

		if maskedCardRe.MatchString(line) {
			continue
		}

		amountText := lastAmountText(line)
		if amountText == "" {
			continue
		}
		columnQty, priceText := splitQuantityColumn(amountText)
		price, ok := parseAmount(priceText)
		if !ok || price == 0 {
			continue
		}

		name := line
		if idx := strings.LastIndex(line, amountText); idx > 0 {
			name = line[:idx]
		}
		name = strings.Trim(name, " \t.,:;-*")

		if !hasLetters(name) {
			continue
		}

		qty := columnQty
		if m := qtyPrefixRe.FindStringSubmatch(name); m != nil && hasLetters(m[2]) && columnQty == 1 {
			qty = atoi(m[1])
			name = strings.TrimSpace(m[2])
		} else if m := qtySuffixRe.FindStringSubmatch(name); m != nil && hasLetters(m[1]) && columnQty == 1 {
			qty = atoi(m[2])
			name = strings.TrimSpace(m[1])
		}
		if qty < 1 {
			qty = 1
		}

		items = append(items, model.LineItem{Name: name, Qty: qty, Price: price})
	}

	return items
}

// splitQuantityColumn separates "2 135,00" into quantity 2 and price 135,00.
// The text is ambiguous: a space is also the Swedish thousands separator, so
// it could be 2 135,00 kr. On an item line we read it as qty + price, because
// grocery receipts print those as neighbouring columns and a single item over
// 1 000 kr is rare, while the misread multiplies a bun's price by ten. Totals
// never go through here, so "Totalt 1 234,50" stays 1234.50.
func splitQuantityColumn(amountText string) (qty int, price string) {
	m := qtyColumnRe.FindStringSubmatch(strings.TrimSpace(amountText))
	if m == nil {
		return 1, amountText
	}
	return max(atoi(m[1]), 1), m[2]
}

func (p *Parser) reconcile(f *model.ReceiptFields) {
	// Nothing on a receipt is negative except a discount line, so a negative
	// sum is a misread; 0 says "unknown" and lets the user fill it in.
	f.Subtotal, f.Tax, f.Total = max(f.Subtotal, 0), max(f.Tax, 0), max(f.Total, 0)

	if f.Total == 0 && f.Subtotal > 0 {
		f.Total = f.Subtotal + f.Tax
	}

	if f.Total == 0 && len(f.LineItems) > 0 {
		var sum float64
		for _, item := range f.LineItems {
			sum += item.Price * float64(max(item.Qty, 1))
		}
		f.Total = max(sum, 0)
		if f.Subtotal == 0 {
			f.Subtotal = f.Total
		}
	}

	// Tax is a share of the total, so one at or above it came from some
	// other number on a moms line.
	if f.Tax >= f.Total && f.Total > 0 {
		f.Tax = 0
	}

	if f.Subtotal == 0 && f.Total > 0 {
		f.Subtotal = f.Total - f.Tax
	}
}

func splitLines(raw string) []string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")

	out := make([]string, 0, 32)
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func lastAmount(line string) (float64, bool) {
	text := lastAmountText(line)
	if text == "" {
		return 0, false
	}
	return parseAmount(text)
}

func lastAmountText(line string) string {
	texts := amountTexts(line)
	if len(texts) == 0 {
		return ""
	}
	return texts[len(texts)-1]
}

// amountTexts lists the numbers on a line that can be money. A percentage is
// a rate, not money. A sign glued to a digit is a hyphen inside an identifier
// ("556036-0793", "08-123 45"), so the line's last number is no price at all:
// taking it gave an item of -793 kr.
func amountTexts(line string) []string {
	var out []string
	for _, loc := range amountRe.FindAllStringIndex(line, -1) {
		text := line[loc[0]:loc[1]]
		if !strings.ContainsAny(text, "0123456789") {
			continue
		}
		if (text[0] == '-' || text[0] == '+') && loc[0] > 0 && isDigit(line[loc[0]-1]) {
			return nil
		}
		if strings.HasPrefix(strings.TrimLeft(line[loc[1]:], " \t"), "%") {
			continue
		}
		out = append(out, text)
	}
	return out
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func parseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	for _, symbol := range currencySymbols {
		s = strings.ReplaceAll(s, symbol, "")
	}
	s = strings.ReplaceAll(s, " ", "")
	if s == "" {
		return 0, false
	}

	lastComma := strings.LastIndex(s, ",")
	lastDot := strings.LastIndex(s, ".")

	switch {
	case lastComma > lastDot:

		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	default:

		s = strings.ReplaceAll(s, ",", "")
	}

	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

func buildDate(year, month, day int) (string, bool) {
	if year < 1970 || year > 2200 || month < 1 || month > 12 || day < 1 || day > 31 {
		return "", false
	}
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)

	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return "", false
	}
	return t.Format(model.DateLayout), true
}

func expandYear(y int) int {
	switch {
	case y >= 100:
		return y
	case y <= 69:
		return 2000 + y
	default:
		return 1900 + y
	}
}

// monthNames holds whole month names and printed abbreviations, Swedish and
// English. Only these count: matching any word on its first three letters
// read "Marabou" as March.
var monthNames = map[string]int{
	"jan": 1, "januari": 1, "january": 1,
	"feb": 2, "februari": 2, "february": 2,
	"mar": 3, "mars": 3, "march": 3,
	"apr": 4, "april": 4,
	"maj": 5, "may": 5,
	"jun": 6, "juni": 6, "june": 6,
	"jul": 7, "juli": 7, "july": 7,
	"aug": 8, "augusti": 8, "august": 8,
	"sep": 9, "sept": 9, "september": 9,
	"okt": 10, "oct": 10, "oktober": 10, "october": 10,
	"nov": 11, "november": 11,
	"dec": 12, "december": 12,
}

func monthFromName(name string) (int, bool) {
	month, ok := monthNames[strings.ToLower(strings.TrimSuffix(name, "."))]
	return month, ok
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

// containsWord is containsAny for short words that also occur inside other
// words: the match must not touch a letter on either side.
func containsWord(haystack string, words []string) bool {
	for _, w := range words {
		for from := 0; ; {
			i := strings.Index(haystack[from:], w)
			if i < 0 {
				break
			}
			start, end := from+i, from+i+len(w)
			before, _ := utf8.DecodeLastRuneInString(haystack[:start])
			after, _ := utf8.DecodeRuneInString(haystack[end:])
			if !unicode.IsLetter(before) && !unicode.IsLetter(after) {
				return true
			}
			from = end
		}
	}
	return false
}

func hasLetters(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

func digitRatio(s string) float64 {
	if s == "" {
		return 0
	}
	var digits int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return float64(digits) / float64(len([]rune(s)))
}

func atoi(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}
