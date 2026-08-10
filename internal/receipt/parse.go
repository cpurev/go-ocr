package receipt

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cpurev/go-ocr/internal/model"
)

type Parser struct {
	DayFirst bool

	DefaultCurrency string
}

func New(dayFirst bool, defaultCurrency string) *Parser {
	return &Parser{DayFirst: dayFirst, DefaultCurrency: strings.ToUpper(defaultCurrency)}
}

const currencyCodes = `USD|EUR|GBP|JPY|CNY|KRW|INR|MNT|AUD|CAD|CHF|SGD|THB|RUB|` +
	`SEK|NOK|DKK|ISK|PLN|CZK|HUF|RON|BGN|TRY|UAH|ZAR|BRL|MXN|CLP|COP|ARS|` +
	`NZD|HKD|TWD|IDR|MYR|PHP|VND|AED|SAR|QAR|ILS|EGP|NGN|KES|PKR|BDT|LKR`

var (
	amountRe = regexp.MustCompile(`[-+]?[$€£¥₮₩₹]?\s?(?:\d{1,3}(?:[ ,.]\d{3})+|\d+)(?:[.,]\d{1,2})?`)

	isoDateRe = regexp.MustCompile(`\b(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})\b`)

	numericDateRe = regexp.MustCompile(`\b(\d{1,2})[-/.](\d{1,2})[-/.](\d{2,4})\b`)

	textDateDMYRe = regexp.MustCompile(`(?i)\b(\d{1,2})\s+([a-z]{3,9})\.?,?\s+(\d{2,4})\b`)
	textDateMDYRe = regexp.MustCompile(`(?i)\b([a-z]{3,9})\.?\s+(\d{1,2}),?\s+(\d{2,4})\b`)

	currencyCodeRe = regexp.MustCompile(`\b(` + currencyCodes + `)\b`)

	amountWithCodeRe = regexp.MustCompile(
		`([-+]?(?:\d{1,3}(?:[ ,.]\d{3})+|\d+)(?:[.,]\d{1,2})?)\s*(?:` + currencyCodes + `)\b`)

	maskedCardRe = regexp.MustCompile(`\*{2,}\s*\d|[xX]{4,}|[kK]{5,}`)

	qtyPrefixRe = regexp.MustCompile(`^(\d{1,3})\s*[xX*×]?\s+(.*)$`)

	qtySuffixRe = regexp.MustCompile(`^(.*?)\s+[xX×]\s*(\d{1,3})$`)
)

var symbolCurrency = map[string]string{
	"$": "USD", "€": "EUR", "£": "GBP", "¥": "JPY", "₮": "MNT", "₩": "KRW", "₹": "INR",
}

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
)

func (p *Parser) Parse(raw string) model.ReceiptFields {
	fields := model.ReceiptFields{RawText: raw}

	lines := splitLines(raw)
	if len(lines) == 0 {
		return fields.Normalize()
	}

	fields.Merchant = p.findMerchant(lines)
	fields.Date = p.findDate(raw)
	fields.Currency = p.findCurrency(raw)
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

func (p *Parser) findCurrency(raw string) string {
	if m := currencyCodeRe.FindString(strings.ToUpper(raw)); m != "" {
		return m
	}
	for symbol, code := range symbolCurrency {
		if strings.Contains(raw, symbol) {
			return code
		}
	}
	return p.DefaultCurrency
}

func (p *Parser) findTotals(lines []string) (subtotal, tax, total float64) {
	for i := len(lines) - 1; i >= 0; i-- {
		lower := strings.ToLower(lines[i])

		amount, ok := lastAmount(lines[i])
		if !ok {
			continue
		}

		switch {
		case subtotal == 0 && containsAny(lower, subtotalWords):
			subtotal = amount
		case tax == 0 && containsAny(lower, taxWords) && !containsAny(lower, subtotalWords):
			tax = amount
		case total == 0 && containsAny(lower, totalWords) && !containsAny(lower, subtotalWords):
			total = amount
		}
	}

	if total == 0 {
		for i := len(lines) - 1; i >= 0; i-- {
			m := amountWithCodeRe.FindStringSubmatch(lines[i])
			if m == nil {
				continue
			}
			if amount, ok := parseAmount(m[1]); ok && amount > 0 {
				total = amount
				break
			}
		}
	}

	return subtotal, tax, total
}

func (p *Parser) findLineItems(lines []string) []model.LineItem {
	items := make([]model.LineItem, 0, len(lines))

	for _, line := range lines {
		lower := strings.ToLower(line)

		if containsAny(lower, subtotalWords) || containsAny(lower, taxWords) ||
			containsAny(lower, totalWords) || containsAny(lower, noiseWords) {
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
		price, ok := parseAmount(amountText)
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

		qty := 1
		if m := qtyPrefixRe.FindStringSubmatch(name); m != nil && hasLetters(m[2]) {
			qty = atoi(m[1])
			name = strings.TrimSpace(m[2])
		} else if m := qtySuffixRe.FindStringSubmatch(name); m != nil && hasLetters(m[1]) {
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

func (p *Parser) reconcile(f *model.ReceiptFields) {
	switch {
	case f.Total == 0 && f.Subtotal > 0:
		f.Total = f.Subtotal + f.Tax
	case f.Subtotal == 0 && f.Total > 0:
		f.Subtotal = f.Total - f.Tax
	}

	if f.Total == 0 && len(f.LineItems) > 0 {
		var sum float64
		for _, item := range f.LineItems {
			sum += item.Price * float64(max(item.Qty, 1))
		}
		f.Total = sum
		if f.Subtotal == 0 {
			f.Subtotal = sum
		}
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
	matches := amountRe.FindAllString(line, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if strings.ContainsAny(matches[i], "0123456789") {
			return matches[i]
		}
	}
	return ""
}

func parseAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	for symbol := range symbolCurrency {
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

func monthFromName(name string) (int, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	if len(name) < 3 {
		return 0, false
	}
	months := []string{
		"january", "february", "march", "april", "may", "june",
		"july", "august", "september", "october", "november", "december",
	}
	for i, m := range months {
		if strings.HasPrefix(m, name[:3]) && strings.HasPrefix(m, name) {
			return i + 1, true
		}
	}

	for i, m := range months {
		if m[:3] == name[:3] {
			return i + 1, true
		}
	}
	return 0, false
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
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
