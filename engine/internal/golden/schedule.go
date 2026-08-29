// Package golden decides when a masked copy is refreshed and which old ones
// may be removed.
//
// The two questions are separate and both are about time. A schedule says when
// to make a new one; a maximum age says when the newest one has drifted far
// enough from production that branching it is testing last quarter's data; a
// retention count says how many to keep once there are more than anybody needs.
//
// None of the three may ever remove a version an environment came from. That
// refusal is the provider's, not this package's, and deliberately so: the
// provider is the only thing that knows whether a branch is still running, and
// a count kept alongside it would be a second answer that can disagree with
// the first. This package decides what to ask for and reports what was refused.
package golden

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Schedule is a cron expression and the zone it is read in.
//
// The zone is part of the expression rather than a separate setting, in the
// prefix form the manifest already accepts: CRON_TZ=Europe/London 0 3 * * *.
// A refresh at three in the morning means three in the morning where the team
// is, and a schedule kept in UTC drifts an hour twice a year against the one
// thing it was chosen to avoid, which is being awake for it.
type Schedule struct {
	expr    string
	loc     *time.Location
	minutes []int
	hours   []int
	days    []int
	months  []int
	weekly  []int
	// domRestricted and dowRestricted record whether the day of month and the
	// day of week fields were narrowed. When both are, a day matches if EITHER
	// does. That is cron's rule and it is surprising enough to be worth
	// naming: "0 0 13 * 5" is the thirteenth of every month AND every Friday,
	// not Friday the thirteenth.
	domRestricted bool
	dowRestricted bool
}

// String returns the expression as it was written.
func (s Schedule) String() string { return s.expr }

// Location is the zone the expression is read in.
func (s Schedule) Location() *time.Location { return s.loc }

// Zero reports whether this is the empty schedule, which is what a manifest
// with no schedule at all produces and means refresh on demand only.
func (s Schedule) Zero() bool { return s.loc == nil }

// ParseSchedule reads a cron expression, with an optional zone prefix.
func ParseSchedule(expr string) (Schedule, error) {
	s := Schedule{expr: expr, loc: time.UTC}
	body := strings.TrimSpace(expr)
	if body == "" {
		return Schedule{}, nil
	}
	if rest, ok := strings.CutPrefix(body, "CRON_TZ="); ok {
		i := strings.IndexAny(rest, " \t")
		if i < 0 {
			return Schedule{}, fmt.Errorf(
				"golden: the time zone prefix in %q is not followed by an expression", expr)
		}
		name := rest[:i]
		loc, err := time.LoadLocation(name)
		if err != nil {
			return Schedule{}, fmt.Errorf(
				"golden: %q is not a time zone this machine knows. "+
					"Zone names come from the IANA database, so it is Europe/London rather than BST", name)
		}
		s.loc = loc
		body = strings.TrimSpace(rest[i+1:])
	}

	fields := strings.Fields(body)
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf(
			"golden: %q has %d fields, and a cron expression has 5: "+
				"minute, hour, day of month, month, day of week", body, len(fields))
	}
	var err error
	if s.minutes, err = parseField(fields[0], 0, 59, "minute"); err != nil {
		return Schedule{}, err
	}
	if s.hours, err = parseField(fields[1], 0, 23, "hour"); err != nil {
		return Schedule{}, err
	}
	if s.days, err = parseField(fields[2], 1, 31, "day of month"); err != nil {
		return Schedule{}, err
	}
	if s.months, err = parseField(fields[3], 1, 12, "month"); err != nil {
		return Schedule{}, err
	}
	if s.weekly, err = parseField(fields[4], 0, 7, "day of week"); err != nil {
		return Schedule{}, err
	}
	// Sunday is both 0 and 7 in cron, and Go's Weekday only knows 0.
	for i, d := range s.weekly {
		if d == 7 {
			s.weekly[i] = 0
		}
	}
	s.weekly = unique(s.weekly)
	s.domRestricted = fields[2] != "*"
	s.dowRestricted = fields[4] != "*"
	return s, nil
}

// searchYears bounds the search. Four years is past any February the
// twenty-ninth, so a schedule that can fire at all fires inside it, and one
// that cannot, such as the thirty-first of February, terminates rather than
// searching forever.
const searchYears = 4

// Next returns the first firing strictly after the given time, or the zero
// time when the expression can never fire.
//
// Strictly after, so that feeding a firing back in returns the following one
// and a loop over the schedule terminates. That also decides what happens when
// the clocks go back and an hour repeats: 01:30 exists twice, Go resolves the
// wall time to the first of them, and the second is not after the first, so
// the refresh runs once rather than twice. That is what somebody who wrote "at
// half past one" meant.
//
// When the clocks go forward the chosen time may not exist at all, and this is
// the case worth being deliberate about. Go's own resolution of a wall time
// inside the gap is documented as not guaranteed, and it is not even
// consistent between zones: asking America/New_York for 02:30 on the day it
// jumps from 02:00 to 03:00 gives 01:30, an hour BEFORE the gap, while asking
// Europe/London for 01:30 on the day it jumps from 01:00 to 02:00 gives 02:30,
// an hour after it. Inheriting that would mean a nightly refresh silently
// running an hour early once a year in one zone and an hour late in another.
// So a wall time that does not exist is resolved here instead, forward, to the
// first wall time on that day that does: the instant the clock jumped. A
// refresh scheduled for 02:30 in New York happens at 03:00 on that one day,
// which is the first moment it could have. If the gap runs past midnight the
// firing is skipped, because a schedule that named a day should not fire on a
// different one.
func (s Schedule) Next(after time.Time) time.Time {
	if s.loc == nil {
		return time.Time{}
	}
	t := after.In(s.loc)
	y, mo, d := t.Date()
	day := time.Date(y, mo, d, 0, 0, 0, 0, s.loc)
	limit := after.Add(searchYears * 366 * 24 * time.Hour)

	for !day.After(limit) {
		if s.matchesDay(day) {
			yy, mm, dd := day.Date()
			for _, hour := range s.hours {
				for _, minute := range s.minutes {
					cand, ok := resolve(yy, mm, dd, hour, minute, s.loc)
					if !ok {
						cand, ok = afterGap(yy, mm, dd, hour, minute, s.loc)
					}
					if !ok || !cand.After(after) {
						continue
					}
					return cand
				}
			}
		}
		yy, mm, dd := day.Date()
		day = time.Date(yy, mm, dd+1, 0, 0, 0, 0, s.loc)
	}
	return time.Time{}
}

// resolve returns the instant for a wall time, and whether that wall time
// exists at all.
//
// It exists when the instant reads back as the same wall time. When the clocks
// go forward, the hours inside the gap read back as something else, which is
// how a gap is detected without asking the zone database about transitions.
func resolve(y int, mo time.Month, d, h, mi int, loc *time.Location) (time.Time, bool) {
	t := time.Date(y, mo, d, h, mi, 0, 0, loc)
	ry, rmo, rd := t.Date()
	rh, rmi, _ := t.Clock()
	return t, ry == y && rmo == mo && rd == d && rh == h && rmi == mi
}

// afterGap finds the first wall time on the same day, at or after the one
// asked for, that exists.
//
// Minute by minute rather than by asking the zone database for the transition,
// because the transition is exactly what a gap is and this finds it without a
// second source of truth about when it happens. A gap is at most a few hours,
// so the scan is bounded by the day.
func afterGap(y int, mo time.Month, d, h, mi int, loc *time.Location) (time.Time, bool) {
	for m := h*60 + mi; m < 24*60; m++ {
		if t, ok := resolve(y, mo, d, m/60, m%60, loc); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// Due reports whether a refresh should have happened by now, given when the
// last one did.
//
// A zero last time means one is due immediately: a project with a schedule and
// no golden at all wants one now rather than at three tomorrow morning.
func (s Schedule) Due(last, now time.Time) bool {
	if s.loc == nil {
		return false
	}
	if last.IsZero() {
		return true
	}
	next := s.Next(last)
	return !next.IsZero() && !next.After(now)
}

func (s Schedule) matchesDay(day time.Time) bool {
	if !contains(s.months, int(day.Month())) {
		return false
	}
	dom := contains(s.days, day.Day())
	dow := contains(s.weekly, int(day.Weekday()))
	switch {
	case s.domRestricted && s.dowRestricted:
		return dom || dow
	case s.domRestricted:
		return dom
	case s.dowRestricted:
		return dow
	default:
		return true
	}
}

// parseField reads one cron field into every value it allows.
//
// It accepts *, a number, a-b, and either of those with /step, in a comma
// separated list. It deliberately does not accept the extensions some cron
// implementations have, such as names for months and weekdays or @daily,
// because the manifest's validator does not accept them either and a schedule
// that validates and then fails to parse is worse than one that is refused.
func parseField(f string, lo, hi int, name string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(f, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("golden: the %s field %q has an empty entry", name, f)
		}
		step := 1
		spec := part
		if i := strings.IndexByte(part, '/'); i >= 0 {
			spec = part[:i]
			n, ok := atoi(part[i+1:])
			if !ok || n <= 0 {
				return nil, fmt.Errorf(
					"golden: the step in the %s field %q must be a positive number", name, part)
			}
			step = n
		}

		from, to := lo, hi
		switch {
		case spec == "*":
		case strings.Contains(spec, "-"):
			a, b, _ := strings.Cut(spec, "-")
			x, okA := atoi(a)
			y, okB := atoi(b)
			if !okA || !okB {
				return nil, fmt.Errorf(
					"golden: the range in the %s field %q must be two numbers", name, part)
			}
			if x < lo || y > hi || x > y {
				return nil, fmt.Errorf(
					"golden: the range in the %s field %q must run upward and lie within %d to %d",
					name, part, lo, hi)
			}
			from, to = x, y
		default:
			n, ok := atoi(spec)
			if !ok {
				return nil, fmt.Errorf(
					"golden: the %s field %q must be a number, a range, or *", name, part)
			}
			if n < lo || n > hi {
				return nil, fmt.Errorf(
					"golden: the %s value %d must lie within %d to %d", name, n, lo, hi)
			}
			// A bare number with a step means "from here to the end of the
			// field", which is what every cron does with 5/10.
			if step == 1 {
				from, to = n, n
			} else {
				from, to = n, hi
			}
		}
		for v := from; v <= to; v += step {
			out = append(out, v)
		}
	}
	out = unique(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("golden: the %s field %q allows no values at all", name, f)
	}
	return out, nil
}

func contains(items []int, v int) bool {
	for _, i := range items {
		if i == v {
			return true
		}
	}
	return false
}

func unique(items []int) []int {
	sort.Ints(items)
	out := items[:0]
	for i, v := range items {
		if i == 0 || items[i-1] != v {
			out = append(out, v)
		}
	}
	return out
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}
