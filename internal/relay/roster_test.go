package relay

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain digits", "97688001234", "97688001234"},
		{"leading plus", "+97688001234", "97688001234"},
		{"spaces and dashes", "+976 8800-1234", "97688001234"},
		{"parens", "+976 (88) 001234", "97688001234"},
		{"empty", "", ""},
		{"no digits", "not-a-number", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in); got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewDropsJunkAndDuplicates(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want int
	}{
		{"two distinct", []string{"+97611111111", "97622222222"}, 2},
		{"same number written differently", []string{"+976 1111-1111", "97611111111"}, 1},
		{"junk dropped", []string{"+97611111111", "", "nonsense"}, 1},
		{"empty roster", nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.in).Size(); got != tt.want {
				t.Errorf("New(%v).Size() = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestActive(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want bool
	}{
		{"two members fan out", []string{"+97611111111", "+97622222222"}, true},
		{"single member is inert", []string{"+97611111111"}, false},
		{"empty is inert", nil, false},
		{"duplicates collapse to inert", []string{"+97611111111", "97611111111"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.in).Active(); got != tt.want {
				t.Errorf("New(%v).Active() = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestHasMatchesAcrossFormats(t *testing.T) {
	r := New([]string{"+976 8800-1234", "97699887766"})

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"configured with plus, delivered without", "97688001234", true},
		{"exact match", "97699887766", true},
		{"stranger", "97600000000", false},
		{"empty", "", false},
		{"junk", "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Has(tt.in); got != tt.want {
				t.Errorf("Has(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestOthers(t *testing.T) {
	r := New([]string{"+97611111111", "+97622222222", "+97633333333"})

	tests := []struct {
		name   string
		sender string
		want   []string
	}{
		{
			name:   "excludes the sender",
			sender: "97622222222",
			want:   []string{"97611111111", "97633333333"},
		},
		{
			name:   "matches sender written with plus",
			sender: "+97611111111",
			want:   []string{"97622222222", "97633333333"},
		},
		{
			name:   "stranger fans out to nobody",
			sender: "97600000000",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Others(tt.sender)
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Others(%q) = %v, want %v", tt.sender, got, tt.want)
			}
		})
	}
}

func TestNilRosterIsSafe(t *testing.T) {
	var r *Roster

	if r.Size() != 0 {
		t.Errorf("nil Size() = %d, want 0", r.Size())
	}
	if r.Active() {
		t.Error("nil Active() = true, want false")
	}
	if r.Has("97611111111") {
		t.Error("nil Has() = true, want false")
	}
	if got := r.Others("97611111111"); got != nil {
		t.Errorf("nil Others() = %v, want nil", got)
	}
}
