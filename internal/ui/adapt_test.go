package ui

import "testing"

func TestContrastExtremes(t *testing.T) {
	white, black := Color{255, 255, 255}, Color{}
	if c := Contrast(white, black); c < 20.9 || c > 21.1 {
		t.Errorf("white/black contrast = %.2f, want 21", c)
	}
	if c := Contrast(white, white); c != 1 {
		t.Errorf("same color contrast = %.2f, want 1", c)
	}
}

func TestLegibleLeavesPassingColorsAlone(t *testing.T) {
	accent := Hex("#67e8f9")
	if got := Legible(accent, darkGround, contrastText); got != accent {
		t.Errorf("accent on the designed ground changed to %v", got)
	}
}

func TestLegibleReachesTheTarget(t *testing.T) {
	grounds := []Color{Hex("#4466cc"), Hex("#ffffff"), Hex("#000000"), Hex("#808080"), Hex("#2e7d32")}
	roles := []Color{Hex("#6b7a8a"), Hex("#67e8f9"), Hex("#2a3644"), Hex("#eab308")}
	for _, bg := range grounds {
		for _, c := range roles {
			for _, min := range []float64{contrastLine, contrastSubtle, contrastText} {
				got := Legible(c, bg, min)
				if Contrast(got, bg) < min-0.01 {
					t.Errorf("Legible(%v on %v, %.1f) = %v with contrast %.2f", c, bg, min, got, Contrast(got, bg))
				}
			}
		}
	}
}

// The user's terminal was blue and the subtle text vanished. Every role of
// every theme must clear its target on that ground and on a white one.
func TestEveryThemeIsLegibleOnAnyGround(t *testing.T) {
	for _, bg := range []Color{Hex("#4466cc"), Hex("#ffffff"), Hex("#fdf6e3"), darkGround, Hex("#1e1e2e")} {
		for _, base := range builtinThemes {
			th := base.Adapt(bg)
			check := func(role string, c Color, min float64) {
				if got := Contrast(c, bg); got < min-0.01 {
					t.Errorf("%s on %v: %s contrast %.2f < %.1f", th.Name, bg, role, got, min)
				}
			}
			check("title", th.Title, contrastText)
			check("accent", th.Accent, contrastText)
			check("subtle", th.Subtle, contrastSubtle)
			check("good", th.Good, contrastText)
			check("warn", th.Warn, contrastText)
			check("bad", th.Bad, contrastText)
			check("line", th.Line, contrastLine)
			check("selected bg", th.Selected.Bg, contrastWash)
			if got := Contrast(th.Selected.Fg, th.Selected.Bg); got < contrastText-0.01 {
				t.Errorf("%s on %v: selected text contrast %.2f", th.Name, bg, got)
			}
			if got := Contrast(th.Logo.Fg, th.Logo.Bg); got < contrastSubtle {
				t.Errorf("%s on %v: pill text contrast %.2f", th.Name, bg, got)
			}
		}
	}
}

// On the ground it was designed for, the default theme must not move: the
// README screenshots are the contract.
func TestDefaultThemeUnchangedOnDarkGround(t *testing.T) {
	base, _ := lookupTheme("fade")
	th := base.Adapt(darkGround)
	if th.Accent != base.Accent || th.Title != base.Title || th.Subtle != base.Subtle || th.Good != base.Good {
		t.Errorf("fade drifted on its own ground: %+v", th)
	}
}

func TestSetBackgroundReadaptsCurrentTheme(t *testing.T) {
	before, _ := Background()
	t.Cleanup(func() { SetBackground(before); groundKnown = false })

	UseTheme("fade")
	dim := Current().Subtle
	SetBackground(Hex("#4466cc"))
	if Current().Subtle == dim {
		t.Error("subtle did not change for a blue background")
	}
	if bg, ok := Background(); !ok || bg != Hex("#4466cc") {
		t.Errorf("Background() = %v, %v", bg, ok)
	}
	// Switching themes keeps the ground.
	UseTheme("nord")
	if Contrast(Current().Subtle, Hex("#4466cc")) < contrastSubtle-0.01 {
		t.Error("nord was not adapted to the blue ground on switch")
	}
	UseTheme("fade")
}

func TestParseOSC11(t *testing.T) {
	cases := []struct {
		in   string
		want Color
		ok   bool
	}{
		{"\033]11;rgb:4444/6666/cccc\033\\", Color{0x44, 0x66, 0xcc}, true},
		{"\033]11;rgb:0b0b/0f0f/1414\a", Color{0x0b, 0x0f, 0x14}, true},
		{"\033]11;rgb:44/66/cc\a", Color{0x44, 0x66, 0xcc}, true},
		{"\033]11;rgb:4/6/c\a", Color{0x44, 0x66, 0xcc}, true},
		{"", Color{}, false},
		{"\033]11;rgb:zz/00/00\a", Color{}, false},
	}
	for _, c := range cases {
		got, ok := parseOSC11(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseOSC11(%q) = %v, %v; want %v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestBackgroundFromEnv(t *testing.T) {
	t.Setenv("COLORFGBG", "15;0")
	if bg, ok := backgroundFromEnv(); !ok || bg != (Color{}) {
		t.Errorf("15;0 gave %v, %v; want black", bg, ok)
	}
	t.Setenv("COLORFGBG", "0;15")
	if bg, ok := backgroundFromEnv(); !ok || bg != (Color{255, 255, 255}) {
		t.Errorf("0;15 gave %v, %v; want white", bg, ok)
	}
	t.Setenv("COLORFGBG", "default;default")
	if _, ok := backgroundFromEnv(); ok {
		t.Error("non-numeric hint should be ignored")
	}
}
