package ui

import (
	"sort"
	"strings"
)

// Theme names every color a screen may use, by role rather than by hue, the
// way btop keys its theme files (main_fg, hi_fg, selected_bg, ...). Draw code
// asks for "the accent" or "a good thing"; only the theme knows what shade.
//
// A theme is written for a dark ground and then adapted to the terminal it
// actually runs in: every role is pushed just far enough from the real
// background to stay legible (see Adapt). On the ground it was designed for
// nothing moves; on a blue or white one, everything that would have vanished
// is corrected while keeping its hue.
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

// Contrast targets per role, WCAG ratios. Headings and keys read as body
// text and get the AA figure; secondary text is allowed to recede; borders
// and meter backgrounds only need to exist.
const (
	contrastText   = 4.5
	contrastSubtle = 3.0
	contrastLine   = 1.5
	contrastWash   = 1.25
)

// darkGround is the background themes are designed against: the README's
// charcoal. It is what a terminal that won't say its color is assumed to be.
var darkGround = Hex("#0b0f14")

var builtinThemes = []Theme{
	{
		Name: "fade", Desc: "charcoal and teal, the default",
		Title: Hex("#e8eef4"), Accent: Hex("#67e8f9"), Subtle: Hex("#6b7a8a"),
		Good: Hex("#4ade80"), Warn: Hex("#eab308"), Bad: Hex("#f87171"),
		Line:  Hex("#2a3644"),
		Meter: [3]Color{Hex("#4ade80"), Hex("#eab308"), Hex("#f87171")},
	},
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
		Name: "paper", Desc: "ink on paper",
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

var (
	ground      = darkGround // the terminal's background, once known
	groundKnown bool
	current     Theme
	currentName string
)

func init() {
	activate(builtinThemes[0])
}

func activate(base Theme) {
	currentName = base.Name
	current = base.Adapt(ground)
}

// Adapt re-tunes the theme for a background. Each role is nudged toward
// legibility against bg and no further, so the designed look survives
// wherever it already worked; the selection wash and the wordmark pill are
// rebuilt from the adapted accent and the real ground rather than assumed.
func (t Theme) Adapt(bg Color) Theme {
	o := t
	if t.FgSet {
		o.Fg = Legible(t.Fg, bg, contrastText)
	}
	o.Title = Legible(t.Title, bg, contrastText)
	o.Accent = Legible(t.Accent, bg, contrastText)
	o.Subtle = Legible(t.Subtle, bg, contrastSubtle)
	o.Good = Legible(t.Good, bg, contrastText)
	o.Warn = Legible(t.Warn, bg, contrastText)
	o.Bad = Legible(t.Bad, bg, contrastText)
	o.Line = Legible(t.Line, bg, contrastLine)
	for i := range o.Meter {
		o.Meter[i] = Legible(t.Meter[i], bg, contrastSubtle)
	}

	// A selected row sits on a wash of the accent over the real ground:
	// close enough to belong, distinct enough to find.
	o.Selected.Bg = Legible(Blend(bg, o.Accent, 0.3), bg, contrastWash)
	o.Selected.Fg = Legible(t.Title, o.Selected.Bg, contrastText)

	o.Logo.Bg = o.Accent
	o.Logo.Fg = Color{0x0b, 0x0f, 0x14}
	if Contrast(Color{255, 255, 255}, o.Accent) > Contrast(o.Logo.Fg, o.Accent) {
		o.Logo.Fg = Color{255, 255, 255}
	}

	o.meter = Gradient(101, o.Meter[0], o.Meter[1], o.Meter[2])
	return o
}

// MeterAt returns the gradient color for a 0-100 position.
func (t Theme) MeterAt(pct int) Color {
	pct = clamp(pct, 0, 100)
	if len(t.meter) != 101 {
		return t.Meter[1]
	}
	return t.meter[pct]
}

// Current is the active theme, adapted to the terminal.
func Current() Theme { return current }

// SetBackground records the terminal's background and re-adapts the theme.
func SetBackground(bg Color) {
	ground, groundKnown = bg, true
	if base, ok := lookupTheme(currentName); ok {
		activate(base)
	}
}

// Background reports the terminal background the theme is adapted to, and
// whether the terminal actually said so.
func Background() (Color, bool) { return ground, groundKnown }

func lookupTheme(name string) (Theme, bool) {
	for _, t := range builtinThemes {
		if t.Name == name {
			return t, true
		}
	}
	return Theme{}, false
}

// UseTheme activates a built-in theme by name. Unknown names keep the
// current theme and report false, so a stale profile never blanks the UI.
func UseTheme(name string) bool {
	base, ok := lookupTheme(strings.ToLower(strings.TrimSpace(name)))
	if !ok {
		return false
	}
	activate(base)
	return true
}

// Themes lists the built-in themes, default first then alphabetical, each
// adapted to the current background so a preview shows what you'd get.
func Themes() []Theme {
	out := make([]Theme, len(builtinThemes))
	for i, t := range builtinThemes {
		out[i] = t.Adapt(ground)
	}
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
// use; the plain ANSI names in ui.go remain as aliases so existing callers
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
