package cron

import (
	"strings"
	"testing"
	"time"
)

// at is a time in UTC, to the minute.
func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	for _, c := range []struct {
		expr, from, want string
	}{
		{"*/5 * * * *", "2026-11-17 10:02", "2026-11-17 10:05"},
		{"*/5 * * * *", "2026-11-17 10:05", "2026-11-17 10:10"}, // never the minute asked about
		{"* * * * *", "2026-11-17 23:59", "2026-11-18 00:00"},
		{"0 * * * *", "2026-11-17 10:00", "2026-11-17 11:00"},
		{"7 * * * *", "2026-11-17 10:30", "2026-11-17 11:07"},
		{"0 9 * * *", "2026-11-17 10:00", "2026-11-18 09:00"},
		{"0 9 * * 1-5", "2026-11-21 08:00", "2026-11-23 09:00"}, // Saturday to Monday
		{"30 14 15 3 *", "2026-11-17 10:00", "2027-03-15 14:30"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"}, // the next leap day
		{"0 0 * * 7", "2026-11-17 10:00", "2026-11-22 00:00"},  // 7 is Sunday
		{"0 0 * * 0", "2026-11-17 10:00", "2026-11-22 00:00"},
		// Both days limited: either one matches (Vixie cron).
		{"0 12 1 * 1", "2026-11-17 13:00", "2026-11-23 12:00"},
		{"0 12 18 * 1", "2026-11-17 13:00", "2026-11-18 12:00"},
		// A day field that begins with *: both must match (Vixie cron).
		{"0 0 */2 * 1", "2026-11-01 00:00", "2026-11-09 00:00"},
		{"10-50/20 * * * *", "2026-11-17 10:15", "2026-11-17 10:30"},
		{"5/15 * * * *", "2026-11-17 10:21", "2026-11-17 10:35"},
		{"1,15,30 * * * *", "2026-11-17 10:16", "2026-11-17 10:30"},
		{"0 0 */3 * *", "2026-11-30 01:00", "2026-12-01 00:00"},
		{"0 */2 * * *", "2026-11-17 10:59", "2026-11-17 12:00"},
	} {
		s, err := Parse(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		got, err := s.Next(at(c.from))
		if err != nil || !got.Equal(at(c.want)) {
			t.Errorf("%q after %s: %v, %v; want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), err, c.want)
		}
	}
}

// Seconds past a minute do not make that minute come again.
func TestNextSkipsTheMinuteItIsIn(t *testing.T) {
	s, _ := Parse("* * * * *")
	from := at("2026-11-17 10:02").Add(42 * time.Second)
	if got, _ := s.Next(from); !got.Equal(at("2026-11-17 10:03")) {
		t.Errorf("got %v", got)
	}
}

// The local time of the time asked about is the schedule's.
func TestNextInLocalTime(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database here")
	}
	s, _ := Parse("0 9 * * *")
	from := time.Date(2026, 11, 17, 10, 0, 0, 0, ny)
	got, err := s.Next(from)
	if want := time.Date(2026, 11, 18, 9, 0, 0, 0, ny); err != nil || !got.Equal(want) {
		t.Errorf("got %v, %v; want %v", got, err, want)
	}
	// Across the night the clocks go forward: 2:30 does not happen on
	// 2026-03-08 in New York, and the hour after the gap comes instead.
	s, _ = Parse("30 2 * * *")
	got, err = s.Next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny))
	if err != nil || got.Day() != 9 && !(got.Day() == 8 && got.Hour() == 3) {
		t.Errorf("across the gap: %v, %v", got, err)
	}
}

func TestNever(t *testing.T) {
	s, err := Parse("0 0 30 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Next(at("2026-01-01 00:00")); err != ErrNever {
		t.Errorf("the thirtieth of February: %v", err)
	}
}

func TestParseRefuses(t *testing.T) {
	for _, c := range []struct{ expr, says string }{
		{"* * * *", "five fields"},
		{"* * * * * *", "five fields"},
		{"60 * * * *", "no number from 0 to 59"},
		{"* 24 * * *", "no number from 0 to 23"},
		{"* * 0 * *", "no number from 1 to 31"},
		{"* * * 13 *", "no number from 1 to 12"},
		{"* * * * 8", "no number from 0 to 7"},
		{"*/0 * * * *", "step"},
		{"*/61 * * * *", "step"},
		{"30-10 * * * *", "runs backwards"},
		{"1,,2 * * * *", "empty item"},
		{"MON * * * *", "no number"},
		{"* * * JAN *", "no number"},
		{"L * * * *", "no number"},
		{"? * * * *", "no number"},
		{"-1 * * * *", "no number"},
	} {
		_, err := Parse(c.expr)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: %v, want it to say %q", c.expr, err, c.says)
		}
	}
}

func TestEvery(t *testing.T) {
	for _, c := range []struct {
		d       time.Duration
		expr    string
		step    time.Duration
		rounded bool
	}{
		{5 * time.Minute, "*/5 * * * *", 5 * time.Minute, false},
		{time.Minute, "* * * * *", time.Minute, false},
		{30 * time.Second, "* * * * *", time.Minute, true},
		{90 * time.Second, "*/2 * * * *", 2 * time.Minute, true},
		{7 * time.Minute, "*/6 * * * *", 6 * time.Minute, true},
		{20 * time.Minute, "*/20 * * * *", 20 * time.Minute, false},
		{45 * time.Minute, "0 * * * *", time.Hour, true}, // a tie goes to the longer
		{50 * time.Minute, "0 * * * *", time.Hour, true},
		{time.Hour, "0 * * * *", time.Hour, false},
		{90 * time.Minute, "0 */2 * * *", 2 * time.Hour, true},
		{2 * time.Hour, "0 */2 * * *", 2 * time.Hour, false},
		{5 * time.Hour, "0 */6 * * *", 6 * time.Hour, true},
		{20 * time.Hour, "0 0 * * *", 24 * time.Hour, true},
		{23*time.Hour + 50*time.Minute, "0 0 * * *", 24 * time.Hour, true},
		{24 * time.Hour, "0 0 * * *", 24 * time.Hour, false},
		{3 * 24 * time.Hour, "0 0 */3 * *", 3 * 24 * time.Hour, false},
	} {
		expr, step, rounded := Every(c.d)
		if expr != c.expr || step != c.step || rounded != c.rounded {
			t.Errorf("Every(%v) = %q, %v, %v; want %q, %v, %v", c.d, expr, step, rounded, c.expr, c.step, c.rounded)
		}
		if _, err := Parse(expr); err != nil {
			t.Errorf("Every(%v) made %q, which does not parse: %v", c.d, expr, err)
		}
	}
}

// No schedule makes Parse or Next panic, and Next always comes after the
// time asked about and matches the schedule.
func FuzzSchedule(f *testing.F) {
	for _, s := range []string{"*/5 * * * *", "0 9 * * 1-5", "30 14 15 3 *", "5/15 1-3 */2 * 0,7", "0 0 29 2 *"} {
		f.Add(s, int64(1794950000))
	}
	f.Fuzz(func(t *testing.T, expr string, unix int64) {
		s, err := Parse(expr)
		if err != nil {
			return
		}
		from := time.Unix(unix%(1<<40), 0).UTC()
		next, err := s.Next(from)
		if err != nil {
			return
		}
		if !next.After(from) || next.Second() != 0 || next.Nanosecond() != 0 {
			t.Errorf("%q after %v: %v", expr, from, next)
		}
		if !s.matchesDay(next) || s.hour&(1<<uint(next.Hour())) == 0 || s.minute&(1<<uint(next.Minute())) == 0 {
			t.Errorf("%q after %v: %v does not match", expr, from, next)
		}
	})
}

// Where the clocks skip a midnight, the next day still comes: Next used to
// go round for ever in Havana, Santiago and the Azores.
func TestNextPastAMissingMidnight(t *testing.T) {
	for _, zone := range []string{"America/Havana", "America/Santiago", "Atlantic/Azores"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Skip("no time zone database here")
		}
		s, _ := Parse("0 9 * * 1")
		done := make(chan time.Time, 1)
		go func() {
			next, _ := s.Next(time.Date(2027, 3, 1, 7, 0, 0, 0, loc))
			done <- next
		}()
		select {
		case next := <-done:
			if next.IsZero() || next.Hour() != 9 || next.Weekday() != time.Monday {
				t.Errorf("%s: %v", zone, next)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: Next did not return", zone)
		}
		// And every day of a year there, without a stall.
		s, _ = Parse("30 0 * * *")
		at := time.Date(2026, 12, 31, 12, 0, 0, 0, loc)
		for range 400 {
			next, err := s.Next(at)
			if err != nil || !next.After(at) || next.Sub(at) > 49*time.Hour {
				t.Fatalf("%s after %v: %v, %v", zone, at, next, err)
			}
			at = next
		}
	}
}

// Around the clocks changing in New York: a fixed time the clocks pass
// twice runs once, a time they skip runs right after the jump, and an
// hourly schedule keeps to the clock.
func TestNextAroundClockChanges(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no time zone database here")
	}
	// 2026-11-01: 01:00 to 02:00 comes twice.
	s, _ := Parse("30 1 * * *")
	first, _ := s.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, ny))
	second, _ := s.Next(first.Add(5 * time.Second))
	if first.Day() != 1 || first.Hour() != 1 || second.Day() != 2 {
		t.Errorf("a fixed 1:30 on the day the clocks go back: %v, then %v", first, second)
	}
	h, _ := Parse("15 * * * *")
	a, _ := h.Next(time.Date(2026, 11, 1, 1, 0, 0, 0, ny))
	b, _ := h.Next(a.Add(5 * time.Second))
	if b.Sub(a) != time.Hour {
		t.Errorf("an hourly schedule in the hour that comes twice: %v, then %v", a, b)
	}
	// 2027-03-14: 02:00 to 03:00 does not come.
	s, _ = Parse("30 2 * * *")
	next, _ := s.Next(time.Date(2027, 3, 14, 0, 0, 0, 0, ny))
	if next.Day() != 14 || next.Hour() != 3 {
		t.Errorf("a 2:30 the clocks skip: %v, want right after the jump on the 14th", next)
	}
}

// zones are time zones whose clocks do odd things: skipped and repeated
// midnights, half hours, quarter-hour offsets.
var zones = []string{"UTC", "America/New_York", "America/Havana", "America/Santiago", "Atlantic/Azores",
	"Australia/Lord_Howe", "Asia/Kathmandu", "Pacific/Chatham", "Asia/Beirut", "Africa/Cairo", "Europe/London",
	"Pacific/Apia", "America/Asuncion", "Asia/Tehran"}

// In any of those zones Next returns, a whole minute after the time asked
// about, and well within the time a match takes to come round.
func FuzzScheduleInZones(f *testing.F) {
	for i, s := range []string{"*/5 * * * *", "0 9 * * 1-5", "30 2 * * *", "30 0 * * *", "0 0 * * 0", "15 1 1 * *", "0 0 29 2 *"} {
		f.Add(s, int64(1794950000+i*86400*41), uint8(i))
	}
	f.Fuzz(func(t *testing.T, expr string, unix int64, zone uint8) {
		s, err := Parse(expr)
		if err != nil {
			return
		}
		loc, err := time.LoadLocation(zones[int(zone)%len(zones)])
		if err != nil {
			t.Skip("no time zone database here")
		}
		from := time.Unix(unix%(1<<36), 0).In(loc)
		done := make(chan struct{})
		var next time.Time
		go func() {
			defer close(done)
			next, err = s.Next(from)
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("%q in %s after %v: Next did not return", expr, loc, from)
		}
		if err != nil {
			return
		}
		if !next.After(from) || next.Second() != 0 || next.Nanosecond() != 0 {
			t.Errorf("%q in %s after %v: %v", expr, loc, from, next)
		}
	})
}
