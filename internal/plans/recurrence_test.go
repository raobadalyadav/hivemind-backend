package plans

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	l, err := time.LoadLocation(name)
	if err != nil {
		t.Skipf("tzdata unavailable for %s: %v", name, err)
	}
	return l
}

func TestParseRule(t *testing.T) {
	good := []string{"FREQ=WEEKLY", "freq=daily;interval=2", "FREQ=MONTHLY;COUNT=6", "FREQ=WEEKLY;UNTIL=20261231"}
	for _, g := range good {
		if _, err := ParseRule(g); err != nil {
			t.Errorf("%q should parse: %v", g, err)
		}
	}
	bad := []string{"", "FREQ=YEARLY", "FREQ=WEEKLY;COUNT=5;UNTIL=20261231", "FREQ=WEEKLY;COUNT=999", "FREQ=WEEKLY;BYDAY=SA", "INTERVAL=2", "FREQ=WEEKLY;INTERVAL=0", "nonsense"}
	for _, b := range bad {
		if _, err := ParseRule(b); err == nil {
			t.Errorf("%q must be rejected", b)
		}
	}
}

func TestOccurrences_WeeklyKolkataCount(t *testing.T) {
	loc := mustLoc(t, "Asia/Kolkata")
	start := time.Date(2026, 10, 3, 7, 0, 0, 0, loc) // Saturday 7 AM IST
	r, _ := ParseRule("FREQ=WEEKLY;COUNT=4")
	got := r.Occurrences(start, loc, start.AddDate(1, 0, 0))
	if len(got) != 4 {
		t.Fatalf("COUNT=4 → 4 occurrences, got %d", len(got))
	}
	for i, o := range got {
		want := start.AddDate(0, 0, 7*i)
		if !o.Equal(want) || o.In(loc).Weekday() != time.Saturday || o.In(loc).Hour() != 7 {
			t.Errorf("occurrence %d = %v, want %v (Saturday 7AM)", i, o.In(loc), want)
		}
	}
}

// 7 AM must stay 7 AM local across a DST change (America/New_York falls
// back on 2026-11-01), which a naive "+7*24h" would break.
func TestOccurrences_WeeklyKeepsLocalHourAcrossDST(t *testing.T) {
	loc := mustLoc(t, "America/New_York")
	start := time.Date(2026, 10, 25, 7, 0, 0, 0, loc)
	r, _ := ParseRule("FREQ=WEEKLY;COUNT=3")
	for i, o := range r.Occurrences(start, loc, start.AddDate(1, 0, 0)) {
		if h := o.In(loc).Hour(); h != 7 {
			t.Errorf("occurrence %d at local hour %d, want 7", i, h)
		}
	}
}

func TestOccurrences_MonthlyOn31stSkipsShortMonths(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 1, 31, 10, 0, 0, 0, loc)
	r, _ := ParseRule("FREQ=MONTHLY;COUNT=4")
	got := r.Occurrences(start, loc, start.AddDate(2, 0, 0))
	want := []time.Month{time.January, time.March, time.May, time.July} // Feb/Apr/Jun have no 31st
	if len(got) != 4 {
		t.Fatalf("expected 4 occurrences, got %d", len(got))
	}
	for i, o := range got {
		if o.Month() != want[i] || o.Day() != 31 {
			t.Errorf("occurrence %d = %v, want day 31 of %v", i, o, want[i])
		}
	}
}

func TestOccurrences_UntilAndHorizon(t *testing.T) {
	loc := time.UTC
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, loc)
	r, _ := ParseRule("FREQ=DAILY;UNTIL=20261005")
	if got := r.Occurrences(start, loc, start.AddDate(1, 0, 0)); len(got) != 5 {
		t.Errorf("UNTIL is inclusive by date: want 5, got %d", len(got))
	}
	open, _ := ParseRule("FREQ=DAILY")
	if got := open.Occurrences(start, loc, start.Add(72*time.Hour)); len(got) != 4 { // day 0..3
		t.Errorf("open-ended series is bounded by the horizon: want 4, got %d", len(got))
	}
}
