package main

import (
	"fmt"
	"strings"
	"time"
)

// parseWhen reads a day-plus-time the way people type them: "fri 3pm",
// "tomorrow 10:30", "2026-08-14 15:00", or a bare "3pm" meaning today. The day
// part reuses parseDay's forward-looking rules, so "fri" on a Friday means
// next Friday.
func parseWhen(s string, now time.Time) (time.Time, error) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(s)))
	switch len(fields) {
	case 0:
		return time.Time{}, fmt.Errorf("give a day and time, like \"fri 3pm\"")
	case 1:
		// Either a bare time ("3pm" -> today) or a bare day (an error: a
		// booking request needs a time of day).
		if hh, mm, ok := parseClock(fields[0]); ok {
			day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			return day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute), nil
		}
		if _, err := parseDay(fields[0], now); err == nil {
			return time.Time{}, fmt.Errorf("%q needs a time of day too, like %q", s, s+" 3pm")
		}
		return time.Time{}, fmt.Errorf("can't read %q -- try \"fri 3pm\" or \"2026-08-14 15:00\"", s)
	case 2:
		day, err := parseDay(fields[0], now)
		if err != nil {
			return time.Time{}, err
		}
		hh, mm, ok := parseClock(fields[1])
		if !ok {
			return time.Time{}, fmt.Errorf("can't read %q as a time -- try 3pm, 3:30pm or 15:00", fields[1])
		}
		return day.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute), nil
	default:
		return time.Time{}, fmt.Errorf("can't read %q -- try \"fri 3pm\" or \"2026-08-14 15:00\"", s)
	}
}

// parseClock accepts 3pm, 3:30pm, 10am, 15:00, 9:05. A bare number with no
// am/pm is rejected rather than guessed: "book at 8" is genuinely ambiguous
// for a barbershop that is open at both.
func parseClock(s string) (hh, mm int, ok bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	mer := ""
	for _, suffix := range []string{"am", "pm"} {
		if strings.HasSuffix(s, suffix) {
			mer = suffix
			s = strings.TrimSuffix(s, suffix)
			break
		}
	}

	hpart, mpart, hasMinutes := strings.Cut(s, ":")
	if hpart == "" {
		return 0, 0, false
	}
	h, err := atoiStrict(hpart)
	if err != nil {
		return 0, 0, false
	}
	m := 0
	if hasMinutes {
		if len(mpart) != 2 {
			return 0, 0, false // minutes are two digits on every clock
		}
		if m, err = atoiStrict(mpart); err != nil || m > 59 {
			return 0, 0, false
		}
	}

	switch mer {
	case "":
		// No am/pm: only unambiguous 24h forms with minutes ("15:00", "9:05").
		if !hasMinutes || h > 23 {
			return 0, 0, false
		}
		return h, m, true
	case "am":
		if h < 1 || h > 12 {
			return 0, 0, false
		}
		if h == 12 {
			h = 0
		}
		return h, m, true
	default: // pm
		if h < 1 || h > 12 {
			return 0, 0, false
		}
		if h != 12 {
			h += 12
		}
		return h, m, true
	}
}

// atoiStrict parses a small positive decimal with no signs, spaces or
// surprises -- strconv.Atoi accepts "+3", which is not a clock digit.
func atoiStrict(s string) (int, error) {
	if s == "" || len(s) > 2 {
		return 0, fmt.Errorf("not a number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}
