package plans

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var ErrBadRecurrence = errors.New("plans: invalid recurrence rule")

const maxRecurrenceCount = 104

// Rule is the supported RRULE subset (flow.md §23):
//
//	FREQ=DAILY|WEEKLY|MONTHLY[;INTERVAL=n][;COUNT=n | ;UNTIL=YYYYMMDD]
//
// No BYDAY: "every Saturday" is implied by the first plan's weekday.
// ponytail: full RFC 5545 (BYDAY/BYMONTHDAY/EXDATE) if a real need appears.
type Rule struct {
	Freq     string
	Interval int
	Count    int       // 0 = unbounded
	Until    time.Time // zero = unbounded; compared by local calendar date
}

func ParseRule(s string) (*Rule, error) {
	r := &Rule{Interval: 1}
	for _, part := range strings.Split(strings.TrimSpace(s), ";") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, ErrBadRecurrence
		}
		key, val := strings.ToUpper(kv[0]), strings.ToUpper(kv[1])
		switch key {
		case "FREQ":
			if val != "DAILY" && val != "WEEKLY" && val != "MONTHLY" {
				return nil, ErrBadRecurrence
			}
			r.Freq = val
		case "INTERVAL":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 || n > 12 {
				return nil, ErrBadRecurrence
			}
			r.Interval = n
		case "COUNT":
			n, err := strconv.Atoi(val)
			if err != nil || n < 1 || n > maxRecurrenceCount {
				return nil, ErrBadRecurrence
			}
			r.Count = n
		case "UNTIL":
			t, err := time.Parse("20060102", val)
			if err != nil {
				return nil, ErrBadRecurrence
			}
			r.Until = t
		default:
			return nil, ErrBadRecurrence
		}
	}
	if r.Freq == "" || (r.Count > 0 && !r.Until.IsZero()) {
		return nil, ErrBadRecurrence
	}
	return r, nil
}

// Occurrences returns every start time (including the template's own, i=0)
// at or before horizon. Times are computed from the ORIGINAL start in loc, so
// 7 AM stays 7 AM across DST changes and monthly recurrences never drift.
// A monthly occurrence whose day doesn't exist in the target month (e.g. the
// 31st in April) is skipped, as RFC 5545 specifies.
func (r *Rule) Occurrences(start time.Time, loc *time.Location, horizon time.Time) []time.Time {
	local := start.In(loc)
	y, m, d := local.Date()
	h, mi, sec := local.Clock()
	ns := local.Nanosecond() // keep sub-second so occurrence 0 equals the template exactly

	var out []time.Time
	for i := 0; i < 2000; i++ {
		var t time.Time
		n := i * r.Interval
		switch r.Freq {
		case "DAILY":
			t = time.Date(y, m, d+n, h, mi, sec, ns, loc)
		case "WEEKLY":
			t = time.Date(y, m, d+7*n, h, mi, sec, ns, loc)
		case "MONTHLY":
			t = time.Date(y, m+time.Month(n), d, h, mi, sec, ns, loc)
			if t.Day() != d { // overflowed into the next month → skip
				continue
			}
		}
		if t.After(horizon) {
			break
		}
		if !r.Until.IsZero() {
			ty, tm, td := t.Date()
			uy, um, ud := r.Until.Date()
			if time.Date(ty, tm, td, 0, 0, 0, 0, time.UTC).After(time.Date(uy, um, ud, 0, 0, 0, 0, time.UTC)) {
				break
			}
		}
		out = append(out, t)
		if r.Count > 0 && len(out) >= r.Count {
			break
		}
	}
	return out
}
