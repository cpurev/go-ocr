package api

import (
	"errors"
	"testing"
	"time"
)

func TestParsePeriod(t *testing.T) {
	day := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}

	midJanuary := time.Date(2026, time.January, 15, 9, 30, 0, 0, time.UTC)
	lastOfMarch := time.Date(2026, time.March, 31, 23, 59, 0, 0, time.UTC)

	tests := []struct {
		name   string
		args   string
		now    time.Time
		from   time.Time
		to     time.Time
		label  string
		errSet bool

		// notAPeriod separates text that names no period from text shaped like
		// one that holds a wrong value. Only the second is worth a reply.
		notAPeriod bool
	}{
		{
			name:  "no argument is the month we are in",
			now:   midJanuary,
			from:  day(2026, time.January, 1),
			to:    day(2026, time.February, 1),
			label: "January 2026",
		},
		{
			name:  "this month",
			args:  "This Month",
			now:   midJanuary,
			from:  day(2026, time.January, 1),
			to:    day(2026, time.February, 1),
			label: "January 2026",
		},
		{
			name:  "last month crosses the year boundary",
			args:  "last month",
			now:   midJanuary,
			from:  day(2025, time.December, 1),
			to:    day(2026, time.January, 1),
			label: "December 2025",
		},
		{
			name:  "last month from the 31st does not skip February",
			args:  "LAST MONTH",
			now:   lastOfMarch,
			from:  day(2026, time.February, 1),
			to:    day(2026, time.March, 1),
			label: "February 2026",
		},
		{
			name:  "a named month",
			args:  " 2026-08 ",
			now:   midJanuary,
			from:  day(2026, time.August, 1),
			to:    day(2026, time.September, 1),
			label: "August 2026",
		},
		{
			name:  "ever is unbounded on both sides",
			args:  "ever",
			now:   midJanuary,
			label: "all time",
		},
		{
			name:       "a period that is not a period",
			args:       "xyzzy",
			now:        midJanuary,
			errSet:     true,
			notAPeriod: true,
		},
		{
			name:   "a month that does not exist",
			args:   "2026-13",
			now:    midJanuary,
			errSet: true,
		},
		{
			name:   "a month written with one digit",
			args:   "2026-8",
			now:    midJanuary,
			errSet: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePeriod(tt.args, tt.now)

			if (err != nil) != tt.errSet {
				t.Fatalf("ParsePeriod(%q) gave err %v, want an error: %v", tt.args, err, tt.errSet)
			}
			if got := errors.Is(err, ErrNotAPeriod); got != tt.notAPeriod {
				t.Fatalf("ParsePeriod(%q) names no period: %v, want %v", tt.args, got, tt.notAPeriod)
			}
			if tt.errSet {
				return
			}
			if !got.From.Equal(tt.from) {
				t.Errorf("ParsePeriod(%q) starts at %s, want %s", tt.args, got.From, tt.from)
			}
			if !got.To.Equal(tt.to) {
				t.Errorf("ParsePeriod(%q) ends before %s, want %s", tt.args, got.To, tt.to)
			}
			if got.Label != tt.label {
				t.Errorf("ParsePeriod(%q) is labelled %q, want %q", tt.args, got.Label, tt.label)
			}
		})
	}
}
