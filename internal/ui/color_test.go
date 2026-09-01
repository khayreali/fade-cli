package ui

import (
	"strings"
	"testing"
)

func TestHexParsesAndTolerates(t *testing.T) {
	if got := Hex("#1a2b3c"); got != (Color{0x1a, 0x2b, 0x3c}) {
		t.Errorf("Hex(#1a2b3c) = %v", got)
	}
	if got := Hex("ffffff"); got != (Color{255, 255, 255}) {
		t.Errorf("Hex(ffffff) = %v", got)
	}
	// A theme typo must degrade, never panic.
	if got := Hex("#zz"); got != (Color{128, 128, 128}) {
		t.Errorf("bad hex gave %v, want mid-gray", got)
	}
}

func TestEscapesMatchDepth(t *testing.T) {
	c := Hex("#67e8f9")
	cases := []struct {
		d    Depth
		want string
	}{
		{DepthTrue, "\033[38;2;103;232;249m"},
		{Depth256, "\033[38;5;"},
		{Depth16, "\033[9"}, // bright cyan is 96; any bright basic starts with 9
		{DepthNone, ""},
	}
	for _, tc := range cases {
		SetDepth(tc.d)
		if got := c.Fg(); !strings.HasPrefix(got, tc.want) {
			t.Errorf("depth %d: Fg = %q, want prefix %q", tc.d, got, tc.want)
		}
	}
	SetDepth(DepthNone)
}

func TestIndex256PrefersGrayRampForGrays(t *testing.T) {
	// Pure grays should land on the 232-255 ramp, not a cube corner.
	if idx := Hex("#808080").index256(); idx < 232 {
		t.Errorf("mid gray mapped to cube index %d", idx)
	}
	// A saturated color must stay in the cube.
	if idx := Hex("#ff6319").index256(); idx < 16 || idx > 231 {
		t.Errorf("orange mapped to %d, want a cube index", idx)
	}
}

func TestIndex16NearestBasic(t *testing.T) {
	n, bright := Hex("#4ade80").index16()
	if n != 2 || !bright {
		t.Errorf("mint green mapped to basic %d bright=%v, want bright green", n, bright)
	}
	n, _ = Hex("#000000").index16()
	if n != 0 {
		t.Errorf("black mapped to %d", n)
	}
}

func TestGradientEndsOnItsStops(t *testing.T) {
	a, b, c := Hex("#000000"), Hex("#808080"), Hex("#ffffff")
	g := Gradient(11, a, b, c)
	if len(g) != 11 {
		t.Fatalf("len = %d", len(g))
	}
	if g[0] != a || g[10] != c {
		t.Errorf("ends = %v .. %v, want the stops", g[0], g[10])
	}
	if g[5] != b {
		t.Errorf("middle = %v, want the mid stop %v", g[5], b)
	}
	for i := 1; i < len(g); i++ {
		if g[i].R < g[i-1].R {
			t.Errorf("gradient not monotonic at %d: %v after %v", i, g[i], g[i-1])
		}
	}
}

func TestGradientDegenerateInputs(t *testing.T) {
	if g := Gradient(0, Hex("#fff")); g != nil {
		t.Errorf("n=0 gave %v", g)
	}
	if g := Gradient(3); g != nil {
		t.Errorf("no stops gave %v", g)
	}
	one := Gradient(4, Hex("#ff0000"))
	for _, c := range one {
		if c != (Color{255, 0, 0}) {
			t.Errorf("single stop should repeat, got %v", c)
		}
	}
}
