package ui

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Depth is how many colors the terminal can show. Themes are written in hex
// and rendered down to whatever the terminal supports, so one palette serves
// a truecolor iTerm and a 16-color Linux console alike.
type Depth int

const (
	DepthNone Depth = iota // NO_COLOR, a pipe, or TERM=dumb
	Depth16
	Depth256
	DepthTrue
)

var depth = detectDepth()

func detectDepth() Depth {
	if !enabled {
		return DepthNone
	}
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return DepthTrue
	}
	term := os.Getenv("TERM")
	switch {
	case strings.Contains(term, "256color"), strings.Contains(term, "kitty"),
		strings.Contains(term, "alacritty"), strings.Contains(term, "wezterm"):
		return Depth256
	case term == "":
		return Depth16
	}
	return Depth16
}

// SetDepth forces a color depth, for tests and for --color=256 style flags.
func SetDepth(d Depth) {
	depth = d
	enabled = d != DepthNone
}

// Color is an sRGB color. The zero value is black; use Hex to build one.
type Color struct{ R, G, B uint8 }

// Hex parses "#rrggbb" or "rrggbb". A malformed string yields mid-gray rather
// than a panic, so a typo in a theme file degrades instead of crashing.
func Hex(s string) Color {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return Color{128, 128, 128}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return Color{128, 128, 128}
	}
	return Color{uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

func (c Color) String() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// Fg returns the escape sequence that sets this as the foreground, at the
// terminal's depth. Empty when color is off.
func (c Color) Fg() string { return c.escape(38, 30) }

// Bg is Fg for the background.
func (c Color) Bg() string { return c.escape(48, 40) }

func (c Color) escape(trueBase, basicBase int) string {
	switch depth {
	case DepthTrue:
		return fmt.Sprintf("\033[%d;2;%d;%d;%dm", trueBase, c.R, c.G, c.B)
	case Depth256:
		return fmt.Sprintf("\033[%d;5;%dm", trueBase, c.index256())
	case Depth16:
		n, bright := c.index16()
		if bright {
			return fmt.Sprintf("\033[%dm", basicBase+60+n)
		}
		return fmt.Sprintf("\033[%dm", basicBase+n)
	}
	return ""
}

// index256 maps to the xterm palette: the 6x6x6 cube for colored values, the
// 24-step gray ramp when all three channels are close, whichever is nearer.
func (c Color) index256() int {
	cube := func(v uint8) int {
		// Cube levels are 0,95,135,175,215,255.
		if v < 48 {
			return 0
		}
		if v < 115 {
			return 1
		}
		return int((int(v) - 35) / 40)
	}
	levels := [6]int{0, 95, 135, 175, 215, 255}
	ri, gi, bi := cube(c.R), cube(c.G), cube(c.B)
	cubeColor := Color{uint8(levels[ri]), uint8(levels[gi]), uint8(levels[bi])}
	cubeIdx := 16 + 36*ri + 6*gi + bi

	avg := (int(c.R) + int(c.G) + int(c.B)) / 3
	gi2 := (avg - 8) / 10
	if gi2 < 0 {
		gi2 = 0
	}
	if gi2 > 23 {
		gi2 = 23
	}
	g := uint8(8 + gi2*10)
	grayColor := Color{g, g, g}
	grayIdx := 232 + gi2

	if dist(c, grayColor) < dist(c, cubeColor) {
		return grayIdx
	}
	return cubeIdx
}

// index16 picks the nearest of the classic eight colors and whether the
// bright variant is closer.
func (c Color) index16() (n int, bright bool) {
	basics := []Color{
		{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16},
		{36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229},
	}
	brights := []Color{
		{102, 102, 102}, {241, 76, 76}, {35, 209, 139}, {245, 245, 67},
		{59, 142, 234}, {214, 112, 214}, {41, 184, 219}, {255, 255, 255},
	}
	best, bestD := 0, math.MaxFloat64
	for i := range basics {
		if d := dist(c, basics[i]); d < bestD {
			best, bestD, bright = i, d, false
		}
		if d := dist(c, brights[i]); d < bestD {
			best, bestD, bright = i, d, true
		}
	}
	return best, bright
}

func dist(a, b Color) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	// Weighted for perception: green carries most of what the eye sees.
	return 2*dr*dr + 4*dg*dg + 3*db*db
}

// Blend interpolates between two colors; t runs 0 (a) to 1 (b).
func Blend(a, b Color, t float64) Color {
	t = math.Max(0, math.Min(1, t))
	lerp := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*t)) }
	return Color{lerp(a.R, b.R), lerp(a.G, b.G), lerp(a.B, b.B)}
}

// Gradient spreads n colors evenly across the given stops -- a meter's start,
// mid and end, the way btop builds its usage bars -- so the caller can index
// by percentage instead of blending on every draw.
func Gradient(n int, stops ...Color) []Color {
	if n <= 0 || len(stops) == 0 {
		return nil
	}
	out := make([]Color, n)
	if n == 1 || len(stops) == 1 {
		for i := range out {
			out[i] = stops[0]
		}
		return out
	}
	segs := len(stops) - 1
	for i := range out {
		pos := float64(i) / float64(n-1) * float64(segs)
		seg := int(pos)
		if seg >= segs {
			seg = segs - 1
		}
		out[i] = Blend(stops[seg], stops[seg+1], pos-float64(seg))
	}
	return out
}
