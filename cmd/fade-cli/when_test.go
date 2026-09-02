package main

import (
	"testing"
	"time"

	"fadecli/internal/catalog"
)

func TestParseWhen(t *testing.T) {
	ny := catalog.ShopLocation()
	// A typed time means the shop's wall clock, so results are New York
	// times regardless of the machine's timezone -- pass `now` in UTC and
	// expect the answer in New York.
	now := time.Date(2026, time.August, 7, 18, 30, 0, 0, time.UTC) // Fri 2:30pm ET
	cases := []struct {
		in   string
		want time.Time
	}{
		{"3pm", time.Date(2026, time.August, 7, 15, 0, 0, 0, ny)},
		{"sat 10am", time.Date(2026, time.August, 8, 10, 0, 0, 0, ny)},
		{"tomorrow 10:30am", time.Date(2026, time.August, 8, 10, 30, 0, 0, ny)},
		{"mon 15:00", time.Date(2026, time.August, 10, 15, 0, 0, 0, ny)},
		{"2026-08-14 3:30pm", time.Date(2026, time.August, 14, 15, 30, 0, 0, ny)},
		{"12pm", time.Date(2026, time.August, 7, 12, 0, 0, 0, ny)},    // noon
		{"sat 12am", time.Date(2026, time.August, 8, 0, 0, 0, 0, ny)}, // midnight
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

// Spring-forward: 2026-03-08, when 2am jumps to 3am. "3pm" must stay 3pm,
// not slide to 4pm the way midnight+15h would.
func TestParseWhenIsDSTSafe(t *testing.T) {
	ny := catalog.ShopLocation()
	now := time.Date(2026, time.March, 8, 8, 0, 0, 0, ny)
	got, err := parseWhen("3pm", now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.March, 8, 15, 0, 0, 0, ny)
	if !got.Equal(want) {
		t.Errorf("parseWhen(\"3pm\") on spring-forward day = %v, want %v", got, want)
	}
}

// A machine on Pacific time still books New York hours: "fri 3pm" is 3pm in
// Brooklyn, which is what InShopTime will render and what OpenAt will check.
func TestParseWhenAnchorsToNewYork(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database")
	}
	now := time.Date(2026, time.August, 7, 9, 0, 0, 0, la) // Fri 9am PT = noon ET
	got, err := parseWhen("3pm", now)
	if err != nil {
		t.Fatal(err)
	}
	if h := catalog.InShopTime(got).Hour(); h != 15 {
		t.Errorf("InShopTime hour = %d, want 15 (3pm in Brooklyn)", h)
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
