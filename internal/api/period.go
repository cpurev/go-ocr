package api

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Period is the span a total covers, half-open as [From, To). A zero bound is
// unbounded on that side.
type Period struct {
	From  time.Time
	To    time.Time
	Label string
}

// Scope is whose receipts a total counts.
type Scope uint8

const (
	scopeSender Scope = iota
	scopeEveryone
)

// ErrNotAPeriod reports text that does not name a period at all, as opposed to
// text shaped like one whose value is wrong. A command relays the first as
// ordinary chat and answers the second with the error.
var ErrNotAPeriod = errors.New("not a period")

var monthRe = regexp.MustCompile(`^\d{4}-\d{1,2}$`)

func ParsePeriod(args string, now time.Time) (Period, error) {
	text := strings.TrimSpace(args)

	switch strings.ToLower(text) {
	case "", "this month":
		return monthPeriod(now), nil

	case "last month":
		// Stepping back from the 1st rather than now.AddDate(0, -1, 0), which
		// on the 29th to the 31st lands in the month after last.
		return monthPeriod(monthStart(now).AddDate(0, 0, -1)), nil

	case "ever":
		return Period{Label: "all time"}, nil
	}

	if !monthRe.MatchString(text) {
		return Period{}, fmt.Errorf("%q: %w", text, ErrNotAPeriod)
	}

	month, err := time.ParseInLocation("2006-01", text, now.Location())
	if err != nil {
		return Period{}, fmt.Errorf(
			`I don't know the period %q; try "last month" or "2026-08"`, text)
	}

	return monthPeriod(month), nil
}

func monthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}

func monthPeriod(t time.Time) Period {
	from := monthStart(t)

	return Period{From: from, To: from.AddDate(0, 1, 0), Label: from.Format("January 2006")}
}
