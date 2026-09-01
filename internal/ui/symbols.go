package ui

// Glyphs are compiled constants rather than theme values, so every screen
// draws the same box the same way. The rounded set is the default; Plain is
// swapped in for 16-color terminals, which tend to be the ones whose fonts
// lack the rounded corners.
type glyphs struct {
	H, V                     string
	TopLeft, TopRight        string
	BottomLeft, BottomRight  string
	TitleLeft, TitleRight    string // the notch the title sits in
	TeeLeft, TeeRight        string
	Meter, MeterEmpty        string
	Gutter                   string // marks the selected row
	Dot, Ring                string // open / closed
	Pin                      string // a saved shop
	Up, Down, Left, Right    string
	Enter, Ellipsis, Divider string
}

var rounded = glyphs{
	H: "─", V: "│",
	TopLeft: "╭", TopRight: "╮", BottomLeft: "╰", BottomRight: "╯",
	TitleLeft: "┐", TitleRight: "┌",
	TeeLeft: "├", TeeRight: "┤",
	Meter: "■", MeterEmpty: "■",
	Gutter: "│",
	Dot:    "●", Ring: "○",
	Pin: "◆",
	Up:  "↑", Down: "↓", Left: "←", Right: "→",
	Enter: "↵", Ellipsis: "…", Divider: "·",
}

var plain = glyphs{
	H: "-", V: "|",
	TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
	TitleLeft: "[", TitleRight: "]",
	TeeLeft: "+", TeeRight: "+",
	Meter: "#", MeterEmpty: "-",
	Gutter: ">",
	Dot:    "*", Ring: "o",
	Pin: "+",
	Up:  "^", Down: "v", Left: "<", Right: ">",
	Enter: "enter", Ellipsis: "...", Divider: "-",
}

// square is the middle tier: Unicode lines for terminals that surely have
// them, square corners for the fonts that draw rounded ones as tofu.
var square = func() glyphs {
	g := rounded
	g.TopLeft, g.TopRight, g.BottomLeft, g.BottomRight = "┌", "┐", "└", "┘"
	return g
}()

// Sym returns the glyph set for the current terminal.
func Sym() glyphs {
	switch depth {
	case DepthNone:
		return plain
	case Depth16:
		return square
	}
	return rounded
}
