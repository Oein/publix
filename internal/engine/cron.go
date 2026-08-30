package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Oein/publix/internal/store"
)

// Schedule is a parsed five-field cron expression.
//
// publix parses cron itself rather than taking a dependency, because the
// subset it needs is small and completely specified: minute, hour, day of
// month, month, day of week, with lists, ranges and steps.
type Schedule struct {
	Minute  []bool // 60
	Hour    []bool // 24
	Dom     []bool // 32, index 1-31
	Month   []bool // 13, index 1-12
	Dow     []bool // 7, index 0-6 with 0 = Sunday
	domStar bool
	dowStar bool
	expr    string
}

// Predefined schedules accepted in place of a five-field expression.
var predefined = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// ParseSchedule reads a five-field cron expression.
func ParseSchedule(expr string) (*Schedule, error) {
	raw := strings.TrimSpace(expr)
	if sub, ok := predefined[strings.ToLower(raw)]; ok {
		raw = sub
	}

	fields := strings.Fields(raw)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron schedule %q must have 5 fields (minute hour day-of-month month day-of-week), got %d", expr, len(fields))
	}

	s := &Schedule{expr: expr}
	var err error
	if s.Minute, err = parseField(fields[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}
	if s.Hour, err = parseField(fields[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}
	if s.Dom, err = parseField(fields[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}
	if s.Month, err = parseField(fields[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}
	if s.Dow, err = parseField(fields[4], 0, 6, dowNames); err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}
	s.domStar = fields[2] == "*"
	s.dowStar = fields[4] == "*"
	return s, nil
}

var monthNames = map[string]int{
	"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
}

var dowNames = map[string]int{
	"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
}

// parseField expands one cron field into a set over [minVal, maxVal].
func parseField(field string, minVal, maxVal int, names map[string]int) ([]bool, error) {
	set := make([]bool, maxVal+1)
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty element in %q", field)
		}

		step := 1
		if base, stepStr, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 {
				return nil, fmt.Errorf("step %q must be a positive integer", stepStr)
			}
			step = n
			part = base
		}

		lo, hi := minVal, maxVal
		if part != "*" {
			loStr, hiStr, isRange := strings.Cut(part, "-")
			var err error
			if lo, err = parseValue(loStr, names); err != nil {
				return nil, err
			}
			if isRange {
				if hi, err = parseValue(hiStr, names); err != nil {
					return nil, err
				}
			} else {
				hi = lo
			}
		}

		// Sunday is both 0 and 7 in every cron implementation people expect.
		if maxVal == 6 {
			if lo == 7 {
				lo = 0
			}
			if hi == 7 {
				hi = 0
			}
		}
		if lo < minVal || hi > maxVal || lo > hi {
			return nil, fmt.Errorf("%q is outside the valid range %d-%d", part, minVal, maxVal)
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return set, nil
}

func parseValue(s string, names map[string]int) (int, error) {
	s = strings.TrimSpace(s)
	if names != nil {
		if v, ok := names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	return n, nil
}

// Matches reports whether t falls on this schedule.
func (s *Schedule) Matches(t time.Time) bool {
	if !s.Minute[t.Minute()] || !s.Hour[t.Hour()] || !s.Month[int(t.Month())] {
		return false
	}
	dom := s.Dom[t.Day()]
	dow := s.Dow[int(t.Weekday())]

	// Cron's oldest quirk: when both day fields are restricted, a match on
	// either one fires. When only one is restricted, only that one counts.
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dow
	case s.dowStar:
		return dom
	default:
		return dom || dow
	}
}

// Next returns the first minute strictly after t that matches.
func (s *Schedule) Next(t time.Time) time.Time {
	t = t.Truncate(time.Minute).Add(time.Minute)
	// Four years covers every schedule that can ever fire, including
	// "29 February" — and terminates for one that cannot, such as 30 Feb.
	limit := t.AddDate(4, 0, 0)
	for ; t.Before(limit); t = t.Add(time.Minute) {
		if s.Matches(t) {
			return t
		}
	}
	return time.Time{}
}

// String returns the original expression.
func (s *Schedule) String() string { return s.expr }

// RunCron executes one scheduled job against the project's live image.
func (e *Engine) RunCron(ctx context.Context, projectID, jobName string) error {
	p, ok := e.store.Project(projectID)
	if !ok {
		return &store.NotFoundError{Kind: "project", ID: projectID}
	}
	live := p.LiveDeployment()
	if live == nil {
		return fmt.Errorf("%s has no live deployment, so scheduled jobs cannot run", p.Name)
	}
	return e.runCronJob(ctx, p.ID, live.ID, jobName)
}
