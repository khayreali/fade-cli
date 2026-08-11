// Package ui renders terminal output. It deliberately has no dependencies:
// the binary stays small, startup stays instant, and `go build` works offline.
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

// color codes are emitted only when the output is an interactive terminal and
// the user hasn't opted out, so piping to grep or jq stays clean.
var enabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// SetColor forces color on or off, for tests and for --no-color.
func SetColor(on bool) { enabled = on }

const (
	reset      = "\033[0m"
	bold       = "\033[1m"
	dim        = "\033[2m"
	reverse    = "\033[7m"
	red        = "\033[31m"
	green      = "\033[32m"
	yellow     = "\033[33m"
	blue       = "\033[34m"
	cyan       = "\033[36m"
	brightCyan = "\033[96m"
)

func paint(code, s string) string {
	if !enabled {
		return s
	}
	return code + s + reset
}

func Accent(s string) string { return paint(brightCyan, s) }
func Bold(s string) string   { return paint(bold, s) }
func Dim(s string) string    { return paint(dim, s) }
func Red(s string) string    { return paint(red, s) }
func Green(s string) string  { return paint(green, s) }
func Yellow(s string) string { return paint(yellow, s) }
func Blue(s string) string   { return paint(blue, s) }
func Cyan(s string) string   { return paint(cyan, s) }

// Highlight reverses foreground and background to mark the focused row. Inner
// resets are re-armed with the reverse code, otherwise the first colored cell
// in a row would end the highlight halfway across.
func Highlight(s string) string {
	if !enabled {
		return "[" + s + "]"
	}
	return reverse + strings.ReplaceAll(s, reset, reset+reverse) + reset
}

// Table renders aligned columns. Widths are measured on the visible text, not
// the byte length, so color codes and non-ASCII names don't break alignment.
type Table struct {
	headers []string
	rows    [][]string
	// right marks columns that should be right-aligned, by index.
	right  map[int]bool
	indent string
}

func NewTable(headers ...string) *Table {
	return &Table{headers: headers, right: map[int]bool{}}
}

// Indent prefixes every line, header included, so a table nested under a
// section heading stays visually attached to it.
func (t *Table) Indent(prefix string) *Table {
	t.indent = prefix
	return t
}

// RightAlign marks columns as right-aligned. Numbers read better that way.
func (t *Table) RightAlign(cols ...int) *Table {
	for _, c := range cols {
		t.right[c] = true
	}
	return t
}

func (t *Table) Row(cells ...string) *Table {
	t.rows = append(t.rows, cells)
	return t
}

func (t *Table) Len() int { return len(t.rows) }

func (t *Table) Render(w io.Writer) {
	if len(t.rows) == 0 {
		return
	}
	cols := len(t.headers)
	for _, r := range t.rows {
		if len(r) > cols {
			cols = len(r)
		}
	}

	widths := make([]int, cols)
	measure := func(cells []string) {
		for i, c := range cells {
			if n := visibleWidth(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	if len(t.headers) > 0 {
		measure(t.headers)
	}
	for _, r := range t.rows {
		measure(r)
	}

	if len(t.headers) > 0 {
		line := make([]string, cols)
		for i := range line {
			h := ""
			if i < len(t.headers) {
				h = strings.ToUpper(t.headers[i])
			}
			line[i] = Dim(t.pad(h, widths[i], i))
		}
		fmt.Fprintln(w, t.indent+strings.TrimRight(strings.Join(line, "  "), " "))
	}

	for _, r := range t.rows {
		line := make([]string, cols)
		for i := range line {
			cell := ""
			if i < len(r) {
				cell = r[i]
			}
			line[i] = t.pad(cell, widths[i], i)
		}
		fmt.Fprintln(w, t.indent+strings.TrimRight(strings.Join(line, "  "), " "))
	}
}

func (t *Table) pad(s string, width, col int) string {
	gap := width - visibleWidth(s)
	if gap < 0 {
		gap = 0
	}
	if t.right[col] {
		return strings.Repeat(" ", gap) + s
	}
	return s + strings.Repeat(" ", gap)
}

// visibleWidth counts printable runes, skipping ANSI escape sequences.
func visibleWidth(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
		default:
			n++
		}
	}
	if n == 0 {
		return utf8.RuneCountInString(s)
	}
	return n
}

// Stars renders a rating as a compact numeric badge. A glyph row is harder to
// scan at a glance than the number itself, and misaligns in many fonts.
func Stars(rating float64, reviews int) string {
	if rating == 0 {
		return Dim("—")
	}
	s := fmt.Sprintf("%.1f", rating)
	if reviews > 0 {
		s += Dim(fmt.Sprintf(" (%d)", reviews))
	}
	return s
}

// RelDay renders a date the way a person would say it.
func RelDay(t, now time.Time) string {
	d := daysBetween(now, t)
	switch {
	case d == 0:
		return "today"
	case d == 1:
		return "tomorrow"
	case d == -1:
		return "yesterday"
	case d > 1 && d < 7:
		return t.Format("Mon")
	case d < -1 && d > -30:
		return fmt.Sprintf("%dd ago", -d)
	default:
		return t.Format("Jan 2")
	}
}

// Duration renders a span in the largest unit that stays readable.
func Duration(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 14:
		return fmt.Sprintf("%d weeks", days/7)
	case days >= 1:
		return fmt.Sprintf("%d days", days)
	case d >= time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return "under an hour"
	}
}

func daysBetween(from, to time.Time) int {
	f := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
	t := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
	// Round, don't truncate: DST days are 23 or 25 hours, and truncation
	// shifts every label ("tomorrow", "6d ago") by one across the transition.
	return int(t.Sub(f).Round(24*time.Hour) / (24 * time.Hour))
}

// Errf prints a user-facing error to stderr.
func Errf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, Red("error:")+" "+fmt.Sprintf(format, args...))
}

// Hint prints a dimmed follow-up suggestion.
func Hint(format string, args ...any) {
	fmt.Fprintln(os.Stdout, Dim("  "+fmt.Sprintf(format, args...)))
}
