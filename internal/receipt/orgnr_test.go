package receipt

import "testing"

func TestFindOrgNr(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"labelled", "ICA Nara\nOrg.nr 556036-0793\nKaffe 35,00", "556036-0793"},
		{"labelled without hyphen", "Organisationsnummer: 5560360793", "556036-0793"},
		{"orgnr with space", "Orgnr 556547 5489", "556547-5489"},
		{"vat form", "Momsreg.nr SE556036079301", "556036-0793"},
		{"vat form under the org label", "Kund 850101-1236\nOrg.nr SE556036079301", "556036-0793"},
		{"vat label", "VAT no: SE 556547548901", "556547-5489"},
		{"momsregistreringsnummer", "Momsregistreringsnummer SE556036079301", "556036-0793"},
		{"bare org.nr", "ICA Nara\n556036-0793\nKaffe 35,00", "556036-0793"},
		{"labelled beats bare", "Kund 202100-5489\nOrg.nr 556036-0793", "556036-0793"},
		// 850101-1236 passes Luhn but is a personnummer: third digit 0.
		{"personnummer not taken over vat form", "Kund 850101-1236\nMomsreg.nr SE556036079301", "556036-0793"},
		{"personnummer alone", "Kund 850101-1236", ""},
		{"bad check digit", "Org.nr 556036-0794", ""},
		{"bad labelled falls back to valid bare", "Org.nr 556036-0794\n556547-5489", "556547-5489"},
		{"label on previous line", "Org.nr\n850101-1236", ""},
		{"none", "Kaffe 35,00", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FindOrgNr(tt.text); got != tt.want {
				t.Errorf("FindOrgNr(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
