// Package cron reads the five-field schedules of cron(8) and says when one
// next comes due. It is what gnosh's scheduled prompts (CronCreate, /loop
// with an interval) run on, as Claude Code's do: minute, hour, day of the
// month, month and day of the week, in the local time of the time asked
// about.
//
// Each field is a list of items parted by commas. An item is * (every
// value), a value, a range a-b, or either of those with a step: */15,
// 10-50/20, 5/15 (from 5 to the field's end). The day of the week runs
// from 0 to 7, both Sunday. As in Vixie cron, when both day fields are
// limited (neither begins with *), a date matches when either does;
// otherwise it must match both. Names (MON, JAN) and the extensions L, W,
// ? and # are not read.
//
// Around the clocks changing, as cron(8) does: a time the clocks skip runs
// at the first minute after the jump, and a time they pass twice runs
// once, for a schedule of fixed hours (one that runs every hour runs by
// the clock, twice in the hour that comes twice).
package cron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a five-field cron schedule, read.
type Schedule struct {
	minute, hour, dom, month, dow uint64 // one bit per value that matches
	// domStar and dowStar say the day field begins with *: then a date
	// must match both day fields, not either; hourStar says the hour
	// field does (the schedule runs by the clock through a repeated hour).
	domStar, dowStar, hourStar bool
	text                       string
}

// field is the range of one of the five fields.
type field struct {
	name     string
	min, max int
}

var fields = [5]field{
	{"minute", 0, 59},
	{"hour", 0, 23},
	{"day of the month", 1, 31},
	{"month", 1, 12},
	{"day of the week", 0, 7},
}

// Parse reads a schedule of five fields parted by blanks. The error says
// what is wrong with it, in words for a person.
func Parse(expr string) (*Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("a schedule is five fields (minute, hour, day of the month, month, day of the week), and %q has %d", expr, len(parts))
	}
	s := &Schedule{text: strings.Join(parts, " ")}
	sets := [5]*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, p := range parts {
		bits, err := parseField(p, fields[i])
		if err != nil {
			return nil, err
		}
		*sets[i] = bits
	}
	s.domStar, s.dowStar, s.hourStar = strings.HasPrefix(parts[2], "*"), strings.HasPrefix(parts[4], "*"), strings.HasPrefix(parts[1], "*")
	if s.dow&(1<<7) != 0 {
		s.dow |= 1 // 7 is Sunday, as 0 is
	}
	return s, nil
}

// parseField reads one field of a schedule into its bits.
func parseField(text string, f field) (uint64, error) {
	var bits uint64
	for _, item := range strings.Split(text, ",") {
		if item == "" {
			return 0, fmt.Errorf("the %s field %q has an empty item", f.name, text)
		}
		rng, stepText, stepped := strings.Cut(item, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 || n > f.max-f.min+1 {
				return 0, fmt.Errorf("the %s field %q has the step %q, which is no number from 1 to %d", f.name, text, stepText, f.max-f.min+1)
			}
			step = n
		}
		lo, hi := f.min, f.max
		switch {
		case rng == "*":
		default:
			a, b, isRange := strings.Cut(rng, "-")
			var err error
			if lo, err = value(a, f, text); err != nil {
				return 0, err
			}
			switch {
			case isRange:
				if hi, err = value(b, f, text); err != nil {
					return 0, err
				}
				if hi < lo {
					return 0, fmt.Errorf("the %s field %q has the range %s, which runs backwards", f.name, text, rng)
				}
			case !stepped:
				hi = lo
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

// value reads one number of a field.
func value(text string, f field, whole string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("the %s field %q has %q, which is no number from %d to %d", f.name, whole, text, f.min, f.max)
	}
	return n, nil
}

// String is the schedule as read, its fields parted by single blanks.
func (s *Schedule) String() string { return s.text }

// matchesDay reports whether t's date is one of the schedule's.
func (s *Schedule) matchesDay(t time.Time) bool {
	if s.month&(1<<uint(t.Month())) == 0 {
		return false
	}
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	if s.domStar || s.dowStar {
		return dom && dow
	}
	return dom || dow
}

// ErrNever says a schedule never comes due: "0 0 30 2 *" asks for the
// thirtieth of February.
var ErrNever = errors.New("the schedule never comes due")

// horizon is how far Next looks: past the next leap day, whatever the
// date asked about.
const horizon = 5 * 366 * 24 * time.Hour

// Next is the first minute after t, in t's time zone, that the schedule
// matches: whole minutes, so a time just past one is not that minute
// again. It returns ErrNever for a schedule no date within five years
// matches.
func (s *Schedule) Next(t time.Time) (time.Time, error) {
	loc := t.Location()
	end := t.Add(horizon)
	c := t.Truncate(time.Minute).Add(time.Minute)
	for c.Before(end) {
		if !s.matchesDay(c) {
			c = nextDay(c, loc)
			continue
		}
		if s.hour&(1<<uint(c.Hour())) == 0 {
			next := nextHour(c, loc)
			// The clocks went forward past an hour the schedule wanted: it
			// runs at the first minute after the jump.
			if y, m, d := next.Date(); y == c.Year() && m == c.Month() && d == c.Day() {
				for h := c.Hour() + 1; h < next.Hour(); h++ {
					if s.hour&(1<<uint(h)) != 0 {
						return next, nil
					}
				}
			}
			c = next
			continue
		}
		if s.minute&(1<<uint(c.Minute())) == 0 {
			c = c.Add(time.Minute)
			continue
		}
		if !s.hourStar && !wallAfter(c, t) {
			// The clocks went back, and this minute of the clock has been
			// passed already: a schedule of fixed hours runs it once.
			c = c.Add(time.Minute)
			continue
		}
		return c, nil
	}
	return time.Time{}, ErrNever
}

// nextDay is the first minute of the day after c's, in loc. Where the
// clocks skip that midnight, it is the first minute after the jump: time.Date
// may give a time of the day before for a midnight that is not there.
func nextDay(c time.Time, loc *time.Location) time.Time {
	y, m, d := c.Date()
	n := time.Date(y, m, d+1, 0, 0, 0, 0, loc)
	for !n.After(c) {
		n = n.Add(time.Hour)
	}
	return n
}

// nextHour is the first minute of the hour after c's, by the clock: past
// a jump forward, the first minute after it; in an hour the clocks pass
// twice, the next hour by the clock in real time. c is a whole minute.
func nextHour(c time.Time, loc *time.Location) time.Time {
	y, m, d := c.Date()
	n := time.Date(y, m, d, c.Hour()+1, 0, 0, 0, loc)
	if !n.After(c) {
		n = c.Add(time.Duration(60-c.Minute()) * time.Minute)
	}
	return n
}

// wallAfter reports whether c reads later on the clock than t, in c's
// zone: a later date, or a later hour and minute that day.
func wallAfter(c, t time.Time) bool {
	t = t.In(c.Location())
	cy, cm, cd := c.Date()
	ty, tm, td := t.Date()
	if cy != ty || cm != tm || cd != td {
		return c.After(t)
	}
	return c.Hour()*60+c.Minute() > t.Hour()*60+t.Minute()
}

// Every is the schedule that comes due every d, d rounded to a step cron
// can take, and that step in words, with whether d had to be rounded:
// minutes that divide an hour (*/5), hours that divide a day (0 */2), or
// whole days (0 0 */3); seconds are rounded up to a minute. It is /loop's
// interval, as Claude Code's /loop reads it.
func Every(d time.Duration) (expr string, step time.Duration, rounded bool) {
	odd := d%time.Minute != 0 // seconds, rounded up to a minute
	if d < time.Minute {
		d = time.Minute
	}
	mins := int((d + time.Minute - 1) / time.Minute)
	n := nearest(mins, everySteps)
	switch {
	case n < 60:
		expr = "*/" + strconv.Itoa(n) + " * * * *"
		if n == 1 {
			expr = "* * * * *"
		}
	case n < 24*60:
		expr = "0 */" + strconv.Itoa(n/60) + " * * *"
		if n == 60 {
			expr = "0 * * * *"
		}
	default:
		expr = "0 0 */" + strconv.Itoa(n/(24*60)) + " * *"
		if n == 24*60 {
			expr = "0 0 * * *"
		}
	}
	return expr, time.Duration(n) * time.Minute, n != mins || odd
}

// everySteps are the intervals, in minutes, a cron schedule keeps evenly:
// minutes that divide an hour, hours that divide a day, and days.
var everySteps = func() []int {
	steps := []int{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30, 60, 120, 180, 240, 360, 480, 720}
	for d := 1; d <= 31; d++ {
		steps = append(steps, d*24*60)
	}
	return steps
}()

// nearest is the value of steps closest to n, the larger one on a tie
// (the less often a prompt runs, the less it costs).
func nearest(n int, steps []int) int {
	best := steps[0]
	for _, s := range steps {
		if d, bd := abs(s-n), abs(best-n); d < bd || d == bd && s > best {
			best = s
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
