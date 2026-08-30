package engine

import (
	"testing"
	"time"
)

func TestParseScheduleMatches(t *testing.T) {
	tests := []struct {
		expr  string
		when  string
		match bool
	}{
		{"0 3 * * *", "2026-03-01T03:00:00Z", true},
		{"0 3 * * *", "2026-03-01T03:01:00Z", false},
		{"*/15 * * * *", "2026-03-01T10:30:00Z", true},
		{"*/15 * * * *", "2026-03-01T10:31:00Z", false},
		{"30 2 * * 1-5", "2026-03-02T02:30:00Z", true},  // Monday
		{"30 2 * * 1-5", "2026-03-07T02:30:00Z", false}, // Saturday
		{"0 0 1 * *", "2026-04-01T00:00:00Z", true},
		{"0 0 1 * *", "2026-04-02T00:00:00Z", false},
		{"@daily", "2026-04-02T00:00:00Z", true},
		{"@hourly", "2026-04-02T13:00:00Z", true},
		{"0 12 * * SUN", "2026-03-01T12:00:00Z", true}, // Sunday by name
		{"0 12 * * 7", "2026-03-01T12:00:00Z", true},   // Sunday as 7
		{"0 0 * jan *", "2026-01-15T00:00:00Z", true},
		{"0 0 * jan *", "2026-02-15T00:00:00Z", false},
		{"0,30 * * * *", "2026-01-15T04:30:00Z", true},
		{"0,30 * * * *", "2026-01-15T04:15:00Z", false},
	}
	for _, tc := range tests {
		s, err := ParseSchedule(tc.expr)
		if err != nil {
			t.Fatalf("ParseSchedule(%q): %v", tc.expr, err)
		}
		when, err := time.Parse(time.RFC3339, tc.when)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.Matches(when.UTC()); got != tc.match {
			t.Errorf("%q.Matches(%s) = %v, want %v", tc.expr, tc.when, got, tc.match)
		}
	}
}

// A schedule restricting BOTH day-of-month and day-of-week fires when
// either matches. This is cron's oldest and least intuitive rule, and
// getting it backwards would silently skip or double-run jobs.
func TestScheduleDayFieldUnion(t *testing.T) {
	s, err := ParseSchedule("0 0 13 * 5") // the 13th, OR any Friday
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"2026-02-13T00:00:00Z": true,  // Friday the 13th: both
		"2026-01-13T00:00:00Z": true,  // the 13th, a Tuesday
		"2026-01-16T00:00:00Z": true,  // a Friday, not the 13th
		"2026-01-14T00:00:00Z": false, // neither
	}
	for ts, want := range cases {
		when, _ := time.Parse(time.RFC3339, ts)
		if got := s.Matches(when.UTC()); got != want {
			t.Errorf("Matches(%s) = %v, want %v", ts, got, want)
		}
	}
}

func TestScheduleNext(t *testing.T) {
	s, err := ParseSchedule("0 3 * * *")
	if err != nil {
		t.Fatal(err)
	}
	from, _ := time.Parse(time.RFC3339, "2026-03-01T04:00:00Z")
	next := s.Next(from.UTC())
	want, _ := time.Parse(time.RFC3339, "2026-03-02T03:00:00Z")
	if !next.Equal(want.UTC()) {
		t.Errorf("Next = %s, want %s", next, want)
	}
}

// A schedule that can never fire must terminate rather than loop forever.
func TestScheduleNextImpossible(t *testing.T) {
	s, err := ParseSchedule("0 0 30 2 *") // 30 February
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan time.Time, 1)
	go func() { done <- s.Next(time.Now()) }()
	select {
	case got := <-done:
		if !got.IsZero() {
			t.Errorf("Next = %s, want the zero time for an impossible schedule", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Next did not terminate for an impossible schedule")
	}
}

func TestParseScheduleErrors(t *testing.T) {
	for _, expr := range []string{
		"", "0 3 * *", "0 3 * * * *", "60 * * * *", "* 25 * * *",
		"0 0 32 * *", "0 0 * 13 *", "0 0 * * 8", "*/0 * * * *", "a * * * *",
		"5-1 * * * *",
	} {
		if _, err := ParseSchedule(expr); err == nil {
			t.Errorf("ParseSchedule(%q) should have failed", expr)
		}
	}
}
