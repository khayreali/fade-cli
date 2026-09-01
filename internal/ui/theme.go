package ui

import (
	"sort"
	"strings"
)

// Theme names every color a screen may use, by role rather than by hue, the
// way btop keys its theme files (main_fg, hi_fg, selected_bg, ...). Draw code
// asks for "the accent" or "a good thing"; only the theme knows what shade.
type Theme struct {
	Name string
	// Desc is one line for the theme picker.
	Desc string

	// Fg is the body text. Zero means the terminal's own default, which is
	// the right call for the built-in dark theme: it must read on whatever
	// background the user already has.
	Fg       Color
	FgSet    bool
	Title    Color // headings; rendered bold
	Accent   Color // keys, cursors, the wordmark
	Subtle   Color // secondary text, dividers
	Selected struct{ Fg, Bg Color }
	Good     Color // open, confirmed, saved
	Warn     Color // closed-but-known, requested, notices
	Bad      Color // errors, overdue
	Line     Color // box borders
	// Meter runs low -> high, for bars that grade a value: price against the
	// corridor, days since your last cut against your cadence.
	Meter [3]Color
	// Logo is the wordmark pill in the status bar.
	Logo struct{ Fg, Bg Color }

	meter []Color // Meter expanded to 101 steps
}

// fadeTheme is the default: the charcoal-and-teal palette from the README
// hero, with body text left to the terminal.
var fadeTheme = Theme{
	Name:  "fade",
	Desc:  "charcoal and teal, the default",
	Title: Hex("#e8eef4"), Accent: Hex("#67e8f9"), Subtle: Hex("#6b7a8a"),
	Good: Hex("#4ade80"), Warn: Hex("#eab308"), Bad: Hex("#f87171"),
	Line:  Hex("#2a3644"),
	Meter: [3]Color{Hex("#4ade80"), Hex("#eab308"), Hex("#f87171")},
}

var builtinThemes = []Theme{
	fadeTheme,
	{
		Name: "nord", Desc: "arctic blues",
		Fg: Hex("#d8dee9"), FgSet: true,
		Title: Hex("#eceff4"), Accent: Hex("#88c0d0"), Subtle: Hex("#616e88"),
		Good: Hex("#a3be8c"), Warn: Hex("#ebcb8b"), Bad: Hex("#bf616a"),
		Line:  Hex("#434c5e"),
		Meter: [3]Color{Hex("#a3be8c"), Hex("#ebcb8b"), Hex("#bf616a")},
	},
	{
		Name: "dracula", Desc: "purple on charcoal",
		Fg: Hex("#f8f8f2"), FgSet: true,
		Title: Hex("#f8f8f2"), Accent: Hex("#bd93f9"), Subtle: Hex("#6272a4"),
		Good: Hex("#50fa7b"), Warn: Hex("#f1fa8c"), Bad: Hex("#ff5555"),
		Line:  Hex("#44475a"),
		Meter: [3]Color{Hex("#50fa7b"), Hex("#8be9fd"), Hex("#bd93f9")},
	},
	{
		Name: "gruvbox", Desc: "warm retro",
		Fg: Hex("#ebdbb2"), FgSet: true,
		Title: Hex("#fbf1c7"), Accent: Hex("#fabd2f"), Subtle: Hex("#7c6f64"),
		Good: Hex("#b8bb26"), Warn: Hex("#fe8019"), Bad: Hex("#fb4934"),
		Line:  Hex("#504945"),
		Meter: [3]Color{Hex("#b8bb26"), Hex("#fabd2f"), Hex("#fb4934")},
	},
	{
		Name: "paper", Desc: "for light terminals",
		Fg: Hex("#2b2f36"), FgSet: true,
		Title: Hex("#111418"), Accent: Hex("#0e7490"), Subtle: Hex("#7a848f"),
		Good: Hex("#15803d"), Warn: Hex("#b45309"), Bad: Hex("#b91c1c"),
		Line:  Hex("#c9d1d9"),
		Meter: [3]Color{Hex("#15803d"), Hex("#b45309"), Hex("#b91c1c")},
	},
	{
		Name: "mono", Desc: "no color, just weight",
		Title: Hex("#ffffff"), Accent: Hex("#ffffff"), Subtle: Hex("#7a7a7a"),
		Good: Hex("#e6e6e6"), Warn: Hex("#bdbdbd"), Bad: Hex("#ffffff"),
		Line:  Hex("#4a4a4a"),
		Meter: [3]Color{Hex("#8a8a8a"), Hex("#bdbdbd"), Hex("#ffffff")},
	},
}

func init() {
	for i := range builtinThemes {
		builtinThemes[i].finish()
	}
	current = builtinThemes[0]
}

// finish derives the parts a theme author shouldn't have to spell out.
func (t *Theme) finish() {
	if t.Selected.Bg == (Color{}) {
		// A selected row sits on a dim wash of the accent: visible on a dark
		// ground, not garish, and it survives being tinted by cell colors.
		t.Selected.Bg = Blend(Hex("#0b0f14"), t.Accent, 0.22)
		if t.Name == "paper" {
			t.Selected.Bg = Blend(Hex("#ffffff"), t.Accent, 0.16)
		}
	}
	if t.Selected.Fg == (Color{}) {
		t.Selected.Fg = t.Title
	}
	if t.Logo.Bg == (Color{}) {
		t.Logo.Bg = t.Accent
		t.Logo.Fg = Hex("#0b0f14")
		if t.Name == "paper" {
			t.Logo.Fg = Hex("#ffffff")
		}
	}
	t.meter = Gradient(101, t.Meter[0], t.Meter[1], t.Meter[2])
}

// MeterAt returns the gradient color for a 0-100 position.
func (t Theme) MeterAt(pct int) Color {
	pct = clamp(pct, 0, 100)
	if len(t.meter) != 101 {
		return t.Meter[1]
	}
	return t.meter[pct]
}

var current Theme

// Current is the active theme.
func Current() Theme { return current }

// UseTheme activates a built-in theme by name. Unknown names keep the
// current theme and report false, so a stale profile never blanks the UI.
func UseTheme(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, t := range builtinThemes {
		if t.Name == name {
			current = t
			return true
		}
	}
	return false
}

// Themes lists the built-in themes, default first then alphabetical.
func Themes() []Theme {
	out := make([]Theme, len(builtinThemes))
	copy(out, builtinThemes)
	sort.SliceStable(out[1:], func(i, j int) bool { return out[i+1].Name < out[j+1].Name })
	return out
}

// tint wraps s in a foreground color, reset afterwards.
func tint(c Color, s string) string {
	if !enabled {
		return s
	}
	return c.Fg() + s + reset
}

// Themed styling. These are the only color entry points draw code should
// use; the plain ANSI names below them remain as aliases so existing callers
// pick up the theme without an edit.

func Title(s string) string   { return paint(bold, tint(current.Title, s)) }
func Accent(s string) string  { return tint(current.Accent, s) }
func Subtle(s string) string  { return tint(current.Subtle, s) }
func Good(s string) string    { return tint(current.Good, s) }
func Warning(s string) string { return tint(current.Warn, s) }
func Bad(s string) string     { return tint(current.Bad, s) }
func Border(s string) string  { return tint(current.Line, s) }

// Selected paints a whole row as the focused one: theme background, theme
// foreground, re-armed after every inner reset so a colored cell can't end
// the wash halfway across.
func Selected(s string) string {
	if !enabled {
		return "[" + s + "]"
	}
	on := current.Selected.Bg.Bg() + current.Selected.Fg.Fg()
	return on + strings.ReplaceAll(s, reset, reset+on) + reset
}

// LogoPill renders the wordmark the way glow badges its name in the status
// bar: a padded, bold block in the accent.
func LogoPill(s string) string {
	if !enabled {
		return "[" + s + "]"
	}
	return current.Logo.Bg.Bg() + current.Logo.Fg.Fg() + bold + " " + s + " " + reset
}

// Wordmark renders "fade" fading out, as on the README hero: each letter a
// step further along the accent -> subtle blend.
func Wordmark() string {
	if !enabled {
		return "fade"
	}
	steps := Gradient(4, current.Title, current.Subtle)
	var b strings.Builder
	for i, r := range "fade" {
		b.WriteString(steps[i].Fg() + string(r))
	}
	b.WriteString(reset)
	return bold + b.String()
}
