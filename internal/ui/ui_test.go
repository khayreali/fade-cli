package ui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestVisibleWidthIgnoresANSI(t *testing.T) {
	SetColor(true)
	t.Cleanup(func() { SetColor(false) })

	plain := "Cabello"
	colored := Green(plain)

	if len(colored) == len(plain) {
		t.Fatal("color was not applied, test is meaningless")
	}
	if got := visibleWidth(colored); got != len(plain) {
		t.Errorf("visibleWidth(colored) = %d, want %d", got, len(plain))
	}
}

func TestTableAlignsColoredCells(t *testing.T) {
	SetColor(true)
	t.Cleanup(func() { SetColor(false) })

	var buf bytes.Buffer
	NewTable("shop", "price").
		Row(Green("A"), "$30").
		Row("BBBBBB", Red("$100")).
		Render(&buf)

	// Strip color, then check every column starts at the same offset.
	var starts []int
	for _, line := range strings.Split(strings.TrimSpace(stripANSI(buf.String())), "\n") {
		starts = append(starts, strings.Index(line, strings.Fields(line)[1]))
	}
	for i := 1; i < len(starts); i++ {
		if starts[i] != starts[0] {
			t.Errorf("column 2 starts at %d on line %d, want %d", starts[i], i, starts[0])
		}
	}
}

func TestTableRightAlign(t *testing.T) {
	SetColor(false)
	var buf bytes.Buffer
	NewTable("n").RightAlign(0).Row("1").Row("1000").Render(&buf)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if got := lines[len(lines)-2]; got != "   1" {
		t.Errorf("short cell = %q, want %q", got, "   1")
	}
}

func TestTableIndentAppliesToHeader(t *testing.T) {
	SetColor(false)
	var buf bytes.Buffer
	NewTable("shop").Indent("  ").Row("A").Render(&buf)

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("line %q is not indented", line)
		}
	}
}

func TestEmptyTableRendersNothing(t *testing.T) {
	var buf bytes.Buffer
	NewTable("a", "b").Render(&buf)
	if buf.Len() != 0 {
		t.Errorf("empty table wrote %q", buf.String())
	}
}

func TestRelDay(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	cases := map[string]time.Time{
		"today":     now,
		"tomorrow":  now.AddDate(0, 0, 1),
		"yesterday": now.AddDate(0, 0, -1),
		"5d ago":    now.AddDate(0, 0, -5),
	}
	for want, in := range cases {
		if got := RelDay(in, now); got != want {
			t.Errorf("RelDay(%v) = %q, want %q", in.Format("Jan 2"), got, want)
		}
	}
}

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		21 * 24 * time.Hour: "3 weeks",
		5 * 24 * time.Hour:  "5 days",
		6 * time.Hour:       "6 hours",
		30 * time.Minute:    "under an hour",
	}
	for in, want := range cases {
		if got := Duration(in); got != want {
			t.Errorf("Duration(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestStarsHandlesUnrated(t *testing.T) {
	SetColor(false)
	if got := Stars(0, 0); got != "--" {
		t.Errorf("Stars(0,0) = %q", got)
	}
	if got := Stars(4.9, 757); got != "4.9 (757)" {
		t.Errorf("Stars(4.9,757) = %q", got)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
