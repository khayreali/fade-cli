package main

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	// Friday afternoon, so weekday math has a fixed anchor.
	now := time.Date(2026, time.August, 7, 14, 30, 0, 0, time.UTC)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"3pm", time.Date(2026, time.August, 7, 15, 0, 0, 0, time.UTC)},
		{"sat 10am", time.Date(2026, time.August, 8, 10, 0, 0, 0, time.UTC)},
		{"tomorrow 10:30am", time.Date(2026, time.August, 8, 10, 30, 0, 0, time.UTC)},
		{"mon 15:00", time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)},
		{"2026-08-14 3:30pm", time.Date(2026, time.August, 14, 15, 30, 0, 0, time.UTC)},
		{"12pm", time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)},    // noon
		{"sat 12am", time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)}, // midnight
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, now)
		if err != nil {
			t.Errorf("parseWhen(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseWhen(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseWhenRejects(t *testing.T) {
	now := time.Date(2026, time.August, 7, 14, 30, 0, 0, time.UTC)
	for _, in := range []string{
		"",
		"fri",        // a day needs a time
		"fri 8",      // bare hour with no am/pm is ambiguous
		"fri 25:00",  // no such hour
		"fri 3:75pm", // no such minute
		"someday 3pm",
		"fri 3pm sharp",
	} {
		if _, err := parseWhen(in, now); err == nil {
			t.Errorf("parseWhen(%q) accepted, want an error", in)
		}
	}
}

func TestParseClockMeridiem(t *testing.T) {
	cases := map[string][2]int{
		"12am": {0, 0}, "12pm": {12, 0}, "1pm": {13, 0}, "11:59pm": {23, 59}, "9:05": {9, 5},
	}
	for in, want := range cases {
		hh, mm, ok := parseClock(in)
		if !ok || hh != want[0] || mm != want[1] {
			t.Errorf("parseClock(%q) = (%d,%d,%v), want (%d,%d,true)", in, hh, mm, ok, want[0], want[1])
		}
	}
	for _, in := range []string{"8", "13pm", "0pm", "+3pm", "8:5pm"} {
		if _, _, ok := parseClock(in); ok {
			t.Errorf("parseClock(%q) accepted, want rejection", in)
		}
	}
}
