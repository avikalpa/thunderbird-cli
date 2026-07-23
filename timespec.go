package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Relative time windows.
//
// Every "what came in today" question used to start with the caller computing
// a date and formatting it as YYYY-MM-DD. That is a step the tool can do, and
// getting it wrong silently narrows the search — the single most common reason
// a correct query returns nothing.

// parseTimeSpec accepts an absolute date (YYYY-MM-DD), a named day (today,
// yesterday), or a relative offset (30m, 24h, 7d, 2w, 3mo, 1y).
//
// now is passed in rather than read from the clock so the behaviour is testable.
func parseTimeSpec(spec string, now time.Time) (time.Time, error) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return time.Time{}, nil
	}
	switch spec {
	case "today":
		return startOfDay(now), nil
	case "yesterday":
		return startOfDay(now).AddDate(0, 0, -1), nil
	case "week":
		return startOfDay(now).AddDate(0, 0, -7), nil
	case "month":
		return startOfDay(now).AddDate(0, -1, 0), nil
	case "year":
		return startOfDay(now).AddDate(-1, 0, 0), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", spec, now.Location()); err == nil {
		return t, nil
	}
	// YYYY-MM means the whole month, which is how people remember old bills.
	if t, err := time.ParseInLocation("2006-01", spec, now.Location()); err == nil {
		return t, nil
	}
	if d, ok := parseRelativeOffset(spec); ok {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q (use YYYY-MM-DD, YYYY-MM, today, yesterday, or an offset like 7d / 24h / 3mo)", spec)
}

// parseRelativeOffset reads "7d", "24h", "2w", "3mo", "1y" into a duration.
func parseRelativeOffset(spec string) (time.Duration, bool) {
	unitStart := 0
	for unitStart < len(spec) && spec[unitStart] >= '0' && spec[unitStart] <= '9' {
		unitStart++
	}
	if unitStart == 0 || unitStart == len(spec) {
		return 0, false
	}
	n, err := strconv.Atoi(spec[:unitStart])
	if err != nil || n < 0 {
		return 0, false
	}
	switch spec[unitStart:] {
	case "m", "min", "mins":
		return time.Duration(n) * time.Minute, true
	case "h", "hr", "hrs":
		return time.Duration(n) * time.Hour, true
	case "d", "day", "days":
		return time.Duration(n) * 24 * time.Hour, true
	case "w", "wk", "weeks":
		return time.Duration(n) * 7 * 24 * time.Hour, true
	case "mo", "mon", "months":
		return time.Duration(n) * 30 * 24 * time.Hour, true
	case "y", "yr", "years":
		return time.Duration(n) * 365 * 24 * time.Hour, true
	}
	return 0, false
}

// endOfTimeSpec returns the exclusive upper bound for a --till value, so that
// `--till 2019-07` covers all of July and `--till today` includes today.
func endOfTimeSpec(spec string, now time.Time) (time.Time, error) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return time.Time{}, nil
	}
	switch spec {
	case "today":
		return startOfDay(now).AddDate(0, 0, 1), nil
	case "yesterday":
		return startOfDay(now), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", spec, now.Location()); err == nil {
		return t.AddDate(0, 0, 1), nil
	}
	if t, err := time.ParseInLocation("2006-01", spec, now.Location()); err == nil {
		return t.AddDate(0, 1, 0), nil
	}
	if d, ok := parseRelativeOffset(spec); ok {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("unrecognised time %q", spec)
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
