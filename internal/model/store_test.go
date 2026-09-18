package model

import "testing"

func TestNormalizeOrgNr(t *testing.T) {
	tests := map[string]string{
		"556036-0793":    "5560360793",
		"556036 0793":    "5560360793",
		"SE556036079301": "5560360793",
		"16556036-0793":  "5560360793",
		"":               "",
	}
	for in, want := range tests {
		if got := NormalizeOrgNr(in); got != want {
			t.Errorf("NormalizeOrgNr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidOrgNr(t *testing.T) {
	tests := []struct {
		nr   string
		want bool
	}{
		{"5560360793", true},
		{"5565475489", true},
		{"2021005489", true},
		{"5560360794", false}, // check digit
		{"8501011236", false}, // Luhn-valid personnummer
		{"556036079", false},
		{"556036079x", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidOrgNr(tt.nr); got != tt.want {
			t.Errorf("ValidOrgNr(%q) = %v, want %v", tt.nr, got, tt.want)
		}
	}
}
