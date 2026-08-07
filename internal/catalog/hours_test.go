package catalog

import (
	"testing"
	"time"
)

// nyc builds a time in shop-local terms, which is what hours are expressed in.
func nyc(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, shopTZ)
}

// 2026-08-07 is a Friday.
var (
	fri = nyc(2026, time.August, 7, 0, 0)
	sat = nyc(2026, time.August, 8, 0, 0)
)

func at(base time.Time, hh, mm int) time.Time {
	return base.Add(time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute)
}

func TestUnknownHoursAreNotClosed(t *testing.T) {
	var h Hours
	if h.Known() {
		t.Error("nil Hours reported as known")
	}
	// The distinction matters: a shop nobody researched must not be rendered
	// as shut, and must not be filtered out as open either.
	if st, _ := h.OpenAt(at(fri, 12, 0)); st != StatusUnknown {
		t.Errorf("status = %v, want StatusUnknown", st)
	}
	if got := h.StatusLabel(at(fri, 12, 0)); got != "" {
		t.Errorf("StatusLabel = %q, want empty", got)
	}
	if got := h.TodayLabel(at(fri, 12, 0)); got != "" {
		t.Errorf("TodayLabel = %q, want empty", got)
	}
}

func TestOpenWithinHours(t *testing.T) {
	h := Hours{"fri": "10:00-20:00"}
	st, until := h.OpenAt(at(fri, 12, 30))
	if st != StatusOpen {
		t.Fatalf("status = %v, want open", st)
	}
	if want := at(fri, 20, 0); !until.Equal(want) {
		t.Errorf("closes at %v, want %v", until, want)
	}
}

func TestBoundariesAreHalfOpen(t *testing.T) {
	h := Hours{"fri": "10:00-20:00"}
	// Open exactly at opening time...
	if st, _ := h.OpenAt(at(fri, 10, 0)); st != StatusOpen {
		t.Error("should be open at exactly 10:00")
	}
	// ...and shut exactly at closing time. Arriving as they lock up is not open.
	if st, _ := h.OpenAt(at(fri, 20, 0)); st != StatusClosed {
		t.Error("should be closed at exactly 20:00")
	}
	if st, _ := h.OpenAt(at(fri, 9, 59)); st != StatusClosed {
		t.Error("should be closed at 09:59")
	}
}

func TestClosedDayIsClosed(t *testing.T) {
	h := Hours{"mon": "10:00-20:00"} // every other day absent
	if st, _ := h.OpenAt(at(fri, 12, 0)); st != StatusClosed {
		t.Error("a day with no entry should be closed")
	}
}

func TestOvernightHoursCoverTheSmallHours(t *testing.T) {
	// Closing "before" opening means trading past midnight.
	h := Hours{"fri": "18:00-02:00"}
	if st, _ := h.OpenAt(at(fri, 23, 0)); st != StatusOpen {
		t.Error("should be open at 23:00 Friday")
	}
	// 01:00 Saturday is still Friday's session.
	st, until := h.OpenAt(at(sat, 1, 0))
	if st != StatusOpen {
		t.Fatal("should still be open at 01:00 Saturday")
	}
	if want := at(sat, 2, 0); !until.Equal(want) {
		t.Errorf("closes at %v, want %v", until, want)
	}
	if st, _ := h.OpenAt(at(sat, 3, 0)); st != StatusClosed {
		t.Error("should be closed at 03:00 Saturday")
	}
}

func TestTwentyFourHours(t *testing.T) {
	h := Hours{"fri": "00:00-24:00"}
	for _, hh := range []int{0, 6, 12, 23} {
		if st, _ := h.OpenAt(at(fri, hh, 0)); st != StatusOpen {
			t.Errorf("should be open at %02d:00", hh)
		}
	}
}

func TestSplitShift(t *testing.T) {
	h := Hours{"fri": "09:00-13:00,15:00-20:00"}
	if st, _ := h.OpenAt(at(fri, 10, 0)); st != StatusOpen {
		t.Error("should be open in the morning shift")
	}
	if st, _ := h.OpenAt(at(fri, 14, 0)); st != StatusClosed {
		t.Error("should be closed between shifts")
	}
	if st, _ := h.OpenAt(at(fri, 16, 0)); st != StatusOpen {
		t.Error("should be open in the afternoon shift")
	}
}

func TestNextOpenSameDay(t *testing.T) {
	h := Hours{"fri": "10:00-20:00"}
	st, when := h.OpenAt(at(fri, 8, 0))
	if st != StatusClosed {
		t.Fatal("should be closed at 08:00")
	}
	if want := at(fri, 10, 0); !when.Equal(want) {
		t.Errorf("next open %v, want %v", when, want)
	}
}

func TestNextOpenSkipsClosedDays(t *testing.T) {
	// Closed Saturday; asking late Friday should point at Sunday.
	h := Hours{"fri": "10:00-20:00", "sun": "11:00-18:00"}
	_, when := h.OpenAt(at(fri, 21, 0))
	if want := nyc(2026, time.August, 9, 11, 0); !when.Equal(want) {
		t.Errorf("next open %v, want Sunday 11:00 (%v)", when, want)
	}
}

func TestNextOpenWrapsTheWeek(t *testing.T) {
	h := Hours{"mon": "10:00-20:00"}
	_, when := h.OpenAt(at(fri, 12, 0))
	if want := nyc(2026, time.August, 10, 10, 0); !when.Equal(want) {
		t.Errorf("next open %v, want Monday 10:00 (%v)", when, want)
	}
}

func TestNeverOpenHasNoNextOpen(t *testing.T) {
	h := Hours{"mon": ""} // present but empty: known schedule, never open
	if !h.Known() {
		t.Fatal("an explicit empty day is still a known schedule")
	}
	st, when := h.OpenAt(at(fri, 12, 0))
	if st != StatusClosed || !when.IsZero() {
		t.Errorf("got (%v, %v), want closed with no next opening", st, when)
	}
}

// Hours belong to the shop, not the machine asking. A laptop in UTC must still
// get Brooklyn's answer.
func TestHoursAreEvaluatedInShopTime(t *testing.T) {
	h := Hours{"fri": "10:00-20:00"}
	// 23:00 UTC Friday is 19:00 in New York -- still open.
	utc := time.Date(2026, time.August, 7, 23, 0, 0, 0, time.UTC)
	if st, _ := h.OpenAt(utc); st != StatusOpen {
		t.Error("should be open: 23:00 UTC is 19:00 in New York")
	}
	// 02:00 UTC Saturday is 22:00 Friday in New York -- shut.
	utc2 := time.Date(2026, time.August, 8, 2, 0, 0, 0, time.UTC)
	if st, _ := h.OpenAt(utc2); st != StatusClosed {
		t.Error("should be closed: 02:00 UTC is 22:00 Friday in New York")
	}
}

func TestMalformedRangesAreDroppedNotGuessed(t *testing.T) {
	for _, bad := range []string{"nonsense", "25:00-26:00", "10:00", "10-20", "aa:bb-cc:dd"} {
		h := Hours{"fri": bad}
		if st, _ := h.OpenAt(at(fri, 12, 0)); st != StatusClosed {
			t.Errorf("%q should yield no usable span, got %v", bad, st)
		}
	}
}

func TestLabels(t *testing.T) {
	h := Hours{"fri": "10:00-19:30", "sat": ""}

	if got, want := h.TodayLabel(at(fri, 12, 0)), "10am–7:30pm"; got != want {
		t.Errorf("TodayLabel = %q, want %q", got, want)
	}
	if got, want := h.TodayLabel(at(sat, 12, 0)), "closed today"; got != want {
		t.Errorf("TodayLabel Saturday = %q, want %q", got, want)
	}
	if got, want := h.StatusLabel(at(fri, 12, 0)), "open until 7:30pm"; got != want {
		t.Errorf("StatusLabel = %q, want %q", got, want)
	}
	if got, want := h.StatusLabel(at(fri, 8, 0)), "opens 10am"; got != want {
		t.Errorf("StatusLabel before opening = %q, want %q", got, want)
	}
}

func TestClockLabel(t *testing.T) {
	cases := map[int]string{
		0: "12am", 9 * 60: "9am", 12 * 60: "12pm",
		13*60 + 30: "1:30pm", 19*60 + 30: "7:30pm", 23 * 60: "11pm",
	}
	for mins, want := range cases {
		if got := clockLabel(mins); got != want {
			t.Errorf("clockLabel(%d) = %q, want %q", mins, got, want)
		}
	}
}

func TestSeededHoursParse(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	var withHours int
	for _, s := range c.Shops {
		if !s.Hours.Known() {
			continue
		}
		withHours++
		for day, raw := range s.Hours {
			var valid bool
			for _, k := range dayKeys {
				if day == k {
					valid = true
				}
			}
			if !valid {
				t.Errorf("%s has unknown day key %q", s.ID, day)
			}
			if raw == "" {
				continue
			}
			if _, ok := parseSpan(raw); !ok {
				t.Errorf("%s has unparseable hours for %s: %q", s.ID, day, raw)
			}
		}
	}
	if withHours == 0 {
		t.Error("no seeded shop has hours")
	}
}
