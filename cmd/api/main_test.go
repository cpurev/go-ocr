package main

import (
	"testing"
	"time"
)

func TestTheDefaultTimezoneLoads(t *testing.T) {
	if _, err := time.LoadLocation("Europe/Stockholm"); err != nil {
		t.Fatalf("loading the default APP_TIMEZONE: %v; this binary must import "+
			"time/tzdata because the runtime image ships no zoneinfo", err)
	}
}
