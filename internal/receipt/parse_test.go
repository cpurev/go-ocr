package receipt

import (
	"reflect"
	"testing"

	"github.com/cpurev/go-ocr/internal/model"
)

func TestParseTotals(t *testing.T) {
	tests := []struct {
		name                 string
		text                 string
		subtotal, tax, total float64
	}{
		{
			name:  "plain totalt",
			text:  "Kaffe 35,00\nBulle 93,00\nTotalt 128,00",
			total: 128, subtotal: 128,
		},
		{
			name:  "totalt and moms",
			text:  "Kaffe 35,00\nBulle 93,00\nTotalt 128,00\nMoms 25,60",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "summa moms is tax",
			text:  "Totalt 128,00\nSumma moms 25,60",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "space thousands on a total",
			text:  "Soffa 1 234,50\nTotalt 1 234,50",
			total: 1234.5, subtotal: 1234.5,
		},
		{
			name:  "momsreg is not tax",
			text:  "ICA Nara\nMomsreg.nr SE556036079301\nKaffe 35,00\nTotalt 35,00",
			total: 35, subtotal: 35,
		},
		{
			name:  "tax larger than total is dropped",
			text:  "Totalt 35,00\nMoms 350,00",
			total: 35, subtotal: 35,
		},
		{
			name:  "totalt inkl moms is total",
			text:  "Kaffe 35,00\nBulle 93,00\nTotalt inkl. moms 128,00",
			total: 128, subtotal: 128,
		},
		{
			name:  "inkl moms with separate tax",
			text:  "Totalt inkl moms 128,00\nVarav moms 25,60",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "exkl moms is subtotal",
			text:  "Summa exkl moms 102,40\nMoms 25,60\nAtt betala 128,00",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "vat table row",
			text:  "Totalt 128,00\nMoms% Moms Netto Brutto\nMoms 25% 25,60 102,40 128,00",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "vat table row with decimal rate",
			text:  "Totalt 128,00\n25,00% 25,60 102,40 128,00 moms",
			total: 128, tax: 25.6, subtotal: 102.4,
		},
		{
			name:  "card payment line is not an item",
			text:  "Kaffe 35,00\nKort 35,00",
			total: 35, subtotal: 35,
		},
		{
			name:  "cash and change",
			text:  "Kaffe 35,00\nKONTANT 50,00 SEK\nTillbaka 15,00 SEK",
			total: 35, subtotal: 35,
		},
		{
			name:  "org.nr line",
			text:  "Org.nr 556036-0793\nKaffe 35,00",
			total: 35, subtotal: 35,
		},
		{
			name:  "savings line after total",
			text:  "Mjolk 20,00\nTotalt 20,00\nTotalt sparat 5,00",
			total: 20, subtotal: 20,
		},
		{
			name:  "att betala beats totalt",
			text:  "Mjolk 25,00\nTotalt 25,00\nRabatt -5,00\nAtt betala 20,00\nTotalt antal 1",
			total: 20, subtotal: 20,
		},
		{
			name: "negative total is unknown",
			text: "Totalt -12,00",
		},
		{
			name:  "total with currency code",
			text:  "Kaffe 35,00\nBulle 20,00\n55,00 SEK",
			total: 55, subtotal: 55,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(true).Parse(tt.text)
			if got.Subtotal != tt.subtotal || got.Tax != tt.tax || got.Total != tt.total {
				t.Errorf("subtotal, tax, total = %v, %v, %v; want %v, %v, %v",
					got.Subtotal, got.Tax, got.Total, tt.subtotal, tt.tax, tt.total)
			}
		})
	}
}

func TestParseLineItems(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []model.LineItem
	}{
		{
			name: "ica receipt",
			text: "ICA Nara\nOrg.nr 556036-0793\n2026-09-18 14:02\nMjolk 3% 1,5L 18,90\nBrod 32,50\n" +
				"Totalt 51,40\nMoms 5,51\nKort 51,40\nTel 08-123 45 67",
			want: []model.LineItem{{Name: "Mjolk 3% 1,5L", Qty: 1, Price: 18.9}, {Name: "Brod", Qty: 1, Price: 32.5}},
		},
		{
			name: "quantity column before price",
			text: "Kanelbulle 2 135,00",
			want: []model.LineItem{{Name: "Kanelbulle", Qty: 2, Price: 135}},
		},
		{
			name: "quantity prefix",
			text: "2 x Kaffe 35,00",
			want: []model.LineItem{{Name: "Kaffe", Qty: 2, Price: 35}},
		},
		{
			name: "payment and change lines",
			text: "Kaffe 35,00\nKONTANT 50,00 SEK\nVäxel 15,00\nSwish 35,00\nBankkort 35,00",
			want: []model.LineItem{{Name: "Kaffe", Qty: 1, Price: 35}},
		},
		{
			name: "payment words inside item names",
			text: "Tidning 49,00\nKortlek 29,00",
			want: []model.LineItem{{Name: "Tidning", Qty: 1, Price: 49}, {Name: "Kortlek", Qty: 1, Price: 29}},
		},
		{
			name: "hyphenated identifier is no price",
			text: "Kund 850101-1236\nKaffe 35,00",
			want: []model.LineItem{{Name: "Kaffe", Qty: 1, Price: 35}},
		},
		{
			name: "discount keeps its sign",
			text: "Kaffe 35,00\nErbjudande -5,00",
			want: []model.LineItem{{Name: "Kaffe", Qty: 1, Price: 35}, {Name: "Erbjudande", Qty: 1, Price: -5}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := New(true).Parse(tt.text).LineItems
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("line items = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"iso", "ICA Nara\n2026-09-18 14:02", "2026-09-18"},
		{"day first numeric", "ICA Nara\n18.09.2026", "2026-09-18"},
		{"numeric beats item words", "ICA Nara\n18.09.2026\nJuice 3,05\nMarabou 29,90", "2026-09-18"},
		{"item words are no date", "ICA Nara\nJuice 3,05\nMarabou 29,90", ""},
		{"swedish abbreviation", "18 sep 2026", "2026-09-18"},
		{"swedish maj", "3 maj 2026", "2026-05-03"},
		{"swedish okt with dot", "3 okt. 2026", "2026-10-03"},
		{"swedish full name", "3 augusti 2026", "2026-08-03"},
		{"english month first", "Sep 18, 2026", "2026-09-18"},
		{"no month prefix guessing", "18 marabou 2026", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(true).Parse(tt.text).Date; got != tt.want {
				t.Errorf("date = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseICAReceipt(t *testing.T) {
	text := `ICA Nara Hornstull
Org.nr 556036-0793
Tel 08-123 45 67
18.09.2026 14:02  Kvitto nr 4711
Mjolk 18,90
2 Kanelbulle 35,00
Kaffe 35,00
Totalt 123,90
Moms% Moms Netto Brutto
Moms 12,00% 13,28 110,62 123,90
Kort 123,90
Momsreg.nr SE556036079301
Totalt sparat 5,00`

	got := New(true).Parse(text)
	if got.Merchant != "ICA Nara Hornstull" {
		t.Errorf("merchant = %q", got.Merchant)
	}
	if got.Date != "2026-09-18" {
		t.Errorf("date = %q", got.Date)
	}
	if got.Total != 123.9 || got.Tax != 13.28 || got.Subtotal != 110.62 {
		t.Errorf("subtotal, tax, total = %v, %v, %v", got.Subtotal, got.Tax, got.Total)
	}
	want := []model.LineItem{
		{Name: "Mjolk", Qty: 1, Price: 18.9},
		{Name: "Kanelbulle", Qty: 2, Price: 35},
		{Name: "Kaffe", Qty: 1, Price: 35},
	}
	if !reflect.DeepEqual(got.LineItems, want) {
		t.Errorf("line items = %+v, want %+v", got.LineItems, want)
	}
	if nr := FindOrgNr(text); nr != "556036-0793" {
		t.Errorf("org.nr = %q", nr)
	}
}
