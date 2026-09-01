package ui

import (
	"strings"
)

// Box draws a titled panel the way btop does: the title sits in a notch cut
// into the top border rather than on a line of its own, so a stack of panels
// reads as one surface. lines are pre-colored; width is the outer width and
// every returned line is exactly that wide, which is what lets Beside lay
// panels next to each other.
func Box(title string, lines []string, width int) []string {
	g := Sym()
	width = max(width, 8)
	inner := width - 4 // border, space, text, space, border

	var out []string
	if title != "" {
		t := " " + truncate(title, max(0, inner-4)) + " "
		top := Border(g.TopLeft+g.H+g.TitleLeft) + Title(t) + Border(g.TitleRight)
		// Border so far: corner, one dash, notch, title, notch; then dashes
		// out to the far corner.
		fill := width - 4 - visibleWidth(t) - 1
		out = append(out, top+Border(strings.Repeat(g.H, max(0, fill))+g.TopRight))
	} else {
		out = append(out, Border(g.TopLeft+strings.Repeat(g.H, width-2)+g.TopRight))
	}
	for _, l := range lines {
		l = truncate(l, inner)
		l += strings.Repeat(" ", inner-visibleWidth(l))
		out = append(out, Border(g.V)+" "+l+" "+Border(g.V))
	}
	out = append(out, Border(g.BottomLeft+strings.Repeat(g.H, width-2)+g.BottomRight))
	return out
}

// Beside places panels side by side when the terminal is wide enough for
// all of them, otherwise stacks them. Each panel is a slice of equal-width
// lines, as Box returns. Shorter panels are padded with blank lines so the
// row has a flat bottom.
func Beside(termWidth, gap int, panels ...[]string) []string {
	panels = nonEmpty(panels)
	if len(panels) == 0 {
		return nil
	}
	total := gap * (len(panels) - 1)
	for _, p := range panels {
		total += visibleWidth(p[0])
	}
	if total > termWidth || len(panels) == 1 {
		var out []string
		for i, p := range panels {
			if i > 0 {
				out = append(out, "")
			}
			out = append(out, p...)
		}
		return out
	}

	height := 0
	for _, p := range panels {
		height = max(height, len(p))
	}
	out := make([]string, height)
	for row := range out {
		var b strings.Builder
		for i, p := range panels {
			if i > 0 {
				b.WriteString(strings.Repeat(" ", gap))
			}
			if row < len(p) {
				b.WriteString(p[row])
			} else {
				b.WriteString(strings.Repeat(" ", visibleWidth(p[0])))
			}
		}
		out[row] = b.String()
	}
	return out
}

func nonEmpty(panels [][]string) [][]string {
	out := panels[:0:0]
	for _, p := range panels {
		if len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// Meter renders a value as a bar of width cells, each filled cell colored by
// where it sits on the theme's low-to-high gradient -- btop's usage meters.
// pct is 0-100.
func Meter(pct, width int) string {
	g := Sym()
	pct = clamp(pct, 0, 100)
	width = max(width, 1)
	filled := (pct*width + 50) / 100
	if pct > 0 && filled == 0 {
		filled = 1
	}
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			b.WriteString(tint(current.MeterAt(i*100/width), g.Meter))
		} else {
			b.WriteString(Border(g.MeterEmpty))
		}
	}
	return b.String()
}

// truncate cuts s to width visible cells, keeping escape sequences intact
// and ending with an ellipsis when something was dropped.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if visibleWidth(s) <= width {
		return s
	}
	g := Sym()
	ell := g.Ellipsis
	keep := width - visibleWidth(ell)

	var b strings.Builder
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case inEscape:
			b.WriteRune(r)
			if r == 'm' {
				inEscape = false
			}
		case r == '\033':
			inEscape = true
			b.WriteRune(r)
		default:
			if n >= keep {
				continue
			}
			b.WriteRune(r)
			n++
		}
	}
	out := b.String()
	if enabled && !strings.HasSuffix(out, reset) {
		out += reset
	}
	return out + Subtle(ell)
}

// stripANSI removes escape sequences, for matching and measuring.
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
