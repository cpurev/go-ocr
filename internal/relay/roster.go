// Package relay fans messages out between a fixed set of phone numbers so
// separate 1:1 chats behave like a shared group.
package relay

import "strings"

// Roster is the set of phone numbers taking part in the relay.
type Roster struct {
	numbers []string
}

// New builds a roster, dropping entries with no digits and collapsing duplicates.
func New(numbers []string) *Roster {
	seen := make(map[string]struct{}, len(numbers))
	out := make([]string, 0, len(numbers))

	for _, raw := range numbers {
		n := Normalize(raw)
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}

	return &Roster{numbers: out}
}

// Normalize strips a phone number to its digits. WhatsApp delivers senders as
// E.164 without a leading plus, so comparing raw strings would miss.
func Normalize(number string) string {
	var b strings.Builder
	b.Grow(len(number))

	for _, r := range number {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// Size reports how many numbers are on the roster.
func (r *Roster) Size() int {
	if r == nil {
		return 0
	}
	return len(r.numbers)
}

// Active reports whether there is anyone to fan out to.
func (r *Roster) Active() bool {
	return r.Size() > 1
}

// Has reports whether number is on the roster.
func (r *Roster) Has(number string) bool {
	if r == nil {
		return false
	}

	target := Normalize(number)
	if target == "" {
		return false
	}

	for _, n := range r.numbers {
		if n == target {
			return true
		}
	}

	return false
}

// Others returns every roster member except sender. A sender that is not on the
// roster gets nobody, so an unknown number cannot fan messages out.
func (r *Roster) Others(sender string) []string {
	if !r.Has(sender) {
		return nil
	}

	from := Normalize(sender)
	out := make([]string, 0, len(r.numbers)-1)
	for _, n := range r.numbers {
		if n != from {
			out = append(out, n)
		}
	}

	return out
}
