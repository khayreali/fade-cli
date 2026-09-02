package ui

import (
	"strings"
	"testing"
)

func TestBoxLinesAreUniformWidth(t *testing.T) {
	SetDepth(DepthTrue)
	t.Cleanup(func() { SetDepth(DepthNone) })

	lines := Box("Hours", []string{"short", Green("colored cell"), "a line that is far too long for the box"}, 24)
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want top + 3 + bottom", len(lines))
	}
	for i, l := range lines {
		if w := visibleWidth(l); w != 24 {
			t.Errorf("line %d is %d wide, want 24: %q", i, w, stripANSI(l))
		}
	}
	top := stripANSI(lines[0])
	if !strings.Contains(top, "Hours") {
		t.Errorf("title missing from top border: %q", top)
	}
	if !strings.HasSuffix(stripANSI(lines[3]), "… │") {
		t.Errorf("overlong line not ellipsized: %q", stripANSI(lines[3]))
	}
}

func TestBesideStacksWhenNarrow(t *testing.T) {
	a := Box("A", []string{"x"}, 20)
	b := Box("B", []string{"y", "z"}, 20)

	wide := Beside(60, 2, a, b)
	if len(wide) != 4 {
		t.Errorf("side by side should be as tall as the taller panel (4), got %d", len(wide))
	}
	if w := visibleWidth(wide[0]); w != 42 {
		t.Errorf("row width = %d, want 20+2+20", w)
	}

	narrow := Beside(30, 2, a, b)
	if len(narrow) != 3+1+4 {
		t.Errorf("stacked should be a + blank + b = 8 lines, got %d", len(narrow))
	}
}

func TestMeterFillsProportionally(t *testing.T) {
	SetDepth(DepthTrue)
	t.Cleanup(func() { SetDepth(DepthNone) })

	g := Sym()
	full := stripANSI(Meter(100, 10))
	if strings.Count(full, g.Meter) != 10 {
		t.Errorf("100%% should fill every cell: %q", full)
	}
	half := Meter(50, 10)
	if n := strings.Count(half, current.MeterAt(0).Fg()); n == 0 {
		t.Error("filled cells should carry the gradient's first color")
	}
	// A tiny non-zero value still shows one cell, or the bar reads as empty.
	if strings.Count(stripANSI(Meter(1, 10)), g.Meter) < 1 {
		t.Error("1%% rendered as an empty bar")
	}
}

func TestTruncateNeverExceedsWidth(t *testing.T) {
	SetDepth(DepthNone) // plain glyphs: the ellipsis is the 3-cell "..."
	for _, w := range []int{1, 2, 3, 4, 8} {
		got := truncate("abcdefghij", w)
		if visibleWidth(got) > w {
			t.Errorf("truncate(_, %d) = %q, width %d exceeds %d", w, got, visibleWidth(got), w)
		}
	}
}

func TestTruncateKeepsEscapesBalanced(t *testing.T) {
	SetDepth(DepthTrue)
	t.Cleanup(func() { SetDepth(DepthNone) })

	s := Green("abcdefghij") + " tail"
	got := truncate(s, 6)
	if w := visibleWidth(got); w != 6 {
		t.Errorf("width = %d, want 6", w)
	}
	if !strings.HasSuffix(got, reset) {
		t.Errorf("truncated string does not end in a reset: %q", got)
	}
}

func TestThemeFallsBackOnUnknownName(t *testing.T) {
	before := Current().Name
	if UseTheme("no-such-theme") {
		t.Error("unknown theme reported success")
	}
	if Current().Name != before {
		t.Errorf("theme changed to %q on a bad name", Current().Name)
	}
	if !UseTheme("nord") || Current().Name != "nord" {
		t.Error("nord should be selectable")
	}
	UseTheme(before)
}

func TestEveryThemeHasAFullGradient(t *testing.T) {
	for _, th := range Themes() {
		if th.MeterAt(0) != th.Meter[0] || th.MeterAt(100) != th.Meter[2] {
			t.Errorf("%s: gradient ends %v..%v do not match stops", th.Name, th.MeterAt(0), th.MeterAt(100))
		}
		if th.Selected.Bg == (Color{}) {
			t.Errorf("%s: no selected background derived", th.Name)
		}
	}
}
