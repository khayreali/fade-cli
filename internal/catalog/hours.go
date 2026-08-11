package catalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// shopTZ is where every shop in the catalog is. Opening hours are a property
// of the shop, not of the laptop asking -- checking "is it open" from a
// machine set to UTC must still answer in Brooklyn time.
var shopTZ = mustLoadTZ("America/New_York")

func mustLoadTZ(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// dayKeys are the JSON keys for a week, indexed by time.Weekday.
var dayKeys = [7]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Hours is a weekly opening schedule keyed by lowercase three-letter day.
// A value is one or more "HH:MM-HH:MM" ranges separated by commas; a day that
// is absent or empty is closed.
//
// A nil Hours means *unknown*, which is not the same as closed and must never
// be rendered as either -- the catalog has hours for only some shops, and
// claiming a shop is shut when nobody looked is worse than saying nothing.
type Hours map[string]string

// Known reports whether this shop has any hours on file.
func (h Hours) Known() bool { return len(h) > 0 }

// span is a half-open range of minutes from midnight. end may exceed 1440,
// meaning the range runs past midnight into the following day.
type span struct{ start, end int }

func (h Hours) spansOn(d time.Weekday) []span {
	raw := strings.TrimSpace(h[dayKeys[d]])
	if raw == "" {
		return nil
	}
	var out []span
	for _, part := range strings.Split(raw, ",") {
		s, ok := parseSpan(part)
		if !ok {
			continue // a malformed range is dropped, not guessed at
		}
		out = append(out, s)
	}
	return out
}

func parseSpan(s string) (span, bool) {
	lo, hi, found := strings.Cut(strings.TrimSpace(s), "-")
	if !found {
		return span{}, false
	}
	start, ok := parseClock(lo)
	if !ok {
		return span{}, false
	}
	end, ok := parseClock(hi)
	if !ok {
		return span{}, false
	}
	// An end at or before the start means the shop trades past midnight.
	if end <= start {
		end += 24 * 60
	}
	return span{start, end}, true
}

func parseClock(s string) (int, bool) {
	hh, mm, found := strings.Cut(strings.TrimSpace(s), ":")
	if !found {
		return 0, false
	}
	h, err := strconv.Atoi(hh)
	if err != nil || h < 0 || h > 24 {
		return 0, false
	}
	m, err := strconv.Atoi(mm)
	if err != nil || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// Status is what the UI needs to know about a shop right now.
type Status int

const (
	StatusUnknown Status = iota // no hours on file
	StatusOpen
	StatusClosed
)

// OpenAt reports whether the shop is open at t, and when that changes.
// The second return is the closing time when open, or the next opening time
// when closed; it is zero when that can't be determined within a week.
func (h Hours) OpenAt(t time.Time) (Status, time.Time) {
	if !h.Known() {
		return StatusUnknown, time.Time{}
	}
	local := t.In(shopTZ)
	// Wall-clock minutes, NOT elapsed-since-midnight: on a DST transition day
	// the two diverge by an hour, and a shop's "10:00-19:00" means the clock
	// on its wall, which springs forward with everyone else's.
	mins := local.Hour()*60 + local.Minute()

	// Yesterday's overnight trading can still cover this morning.
	for _, s := range h.spansOn(prevDay(local.Weekday())) {
		if s.end > 24*60 && mins < s.end-24*60 {
			return StatusOpen, clockOn(local, 0, s.end-24*60)
		}
	}
	for _, s := range h.spansOn(local.Weekday()) {
		if mins >= s.start && mins < s.end {
			return StatusOpen, clockOn(local, 0, s.end)
		}
	}
	return StatusClosed, h.nextOpen(local, mins)
}

// clockOn builds the instant reading m minutes on the wall clock, dayOffset
// days after t's day, in shop time. m may exceed 24h (overnight close);
// time.Date normalises the overflow, and building from components rather than
// midnight.Add keeps DST days honest.
func clockOn(t time.Time, dayOffset, m int) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day()+dayOffset, m/60, m%60, 0, 0, shopTZ)
}

// nextOpen scans forward a week for the next opening time.
func (h Hours) nextOpen(local time.Time, mins int) time.Time {
	today := local.Weekday()
	for offset := 0; offset < 8; offset++ {
		day := (int(today) + offset) % 7
		spans := h.spansOn(time.Weekday(day))
		sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
		for _, s := range spans {
			if offset == 0 && s.start <= mins {
				continue // already passed today
			}
			return clockOn(local, offset, s.start)
		}
	}
	return time.Time{}
}

func prevDay(d time.Weekday) time.Weekday { return time.Weekday((int(d) + 6) % 7) }

// OnDay returns the opening ranges for the weekday of t, and whether the shop
// trades at all that day. Unlike OpenAt it says nothing about the time of day,
// which is what you want when asking about a date rather than about right now.
func (h Hours) OnDay(t time.Time) (label string, trades bool) {
	if !h.Known() {
		return "", false
	}
	spans := h.spansOn(t.In(shopTZ).Weekday())
	if len(spans) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(spans))
	for _, s := range spans {
		parts = append(parts, clockLabel(s.start)+"–"+clockLabel(s.end%(24*60)))
	}
	return strings.Join(parts, ", "), true
}

// TodayLabel renders the day's hours the way a shop window would, or "closed".
func (h Hours) TodayLabel(t time.Time) string {
	if !h.Known() {
		return ""
	}
	if label, trades := h.OnDay(t); trades {
		return label
	}
	return "closed today"
}

// SameShopDay reports whether two instants fall on the same calendar day where
// the shops are, which is the only comparison that makes sense for hours.
func SameShopDay(a, b time.Time) bool {
	x, y := a.In(shopTZ), b.In(shopTZ)
	return x.Year() == y.Year() && x.YearDay() == y.YearDay()
}

// InShopTime converts an instant to the shops' wall clock, for anything that
// renders a time a shop will read -- a texted "around 3pm" must mean the
// shop's 3pm no matter what the sender's laptop is set to.
func InShopTime(t time.Time) time.Time { return t.In(shopTZ) }

// clockLabel renders minutes-from-midnight as a compact 12-hour time.
func clockLabel(m int) string {
	m %= 24 * 60
	h, min := m/60, m%60
	suffix := "am"
	switch {
	case h == 0:
		h = 12
	case h == 12:
		suffix = "pm"
	case h > 12:
		h, suffix = h-12, "pm"
	}
	if min == 0 {
		return fmt.Sprintf("%d%s", h, suffix)
	}
	return fmt.Sprintf("%d:%02d%s", h, min, suffix)
}

// StatusLabel is the one-line "open until 7:30pm" / "opens 10am Tue" summary.
func (h Hours) StatusLabel(t time.Time) string {
	status, when := h.OpenAt(t)
	switch status {
	case StatusUnknown:
		return ""
	case StatusOpen:
		if when.IsZero() {
			return "open"
		}
		return "open until " + clockLabel(when.Hour()*60+when.Minute())
	default:
		if when.IsZero() {
			return "closed"
		}
		local := t.In(shopTZ)
		at := clockLabel(when.Hour()*60 + when.Minute())
		switch days := daysApart(local, when); days {
		case 0:
			return "opens " + at
		case 1:
			return "opens " + at + " tomorrow"
		default:
			return "opens " + at + " " + when.Format("Mon")
		}
	}
}

func daysApart(from, to time.Time) int {
	f := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, shopTZ)
	t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, shopTZ)
	// Round, don't truncate: across a DST transition adjacent midnights are 23
	// or 25 hours apart, and truncation turns "tomorrow" into "today".
	return int(t.Sub(f).Round(24*time.Hour) / (24 * time.Hour))
}
