package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// KeyHint is one command shown in a list's footer.
type KeyHint struct {
	Key   string // the character that triggers it, "" for display-only hints
	Label string
}

// List is a keyboard-navigable menu. Arrows move, enter selects, and any
// registered hint key returns as a command.
//
// Everything it prints uses \r\n: raw mode turns off the terminal's
// newline-to-carriage-return translation, so a bare \n would stair-step the
// output down and to the right.
type List struct {
	Title    string
	Subtitle string
	Hint     string       // one line of guidance above the rows
	Rows     [][]string   // pre-colored cells
	Right    map[int]bool // column indexes to right-align
	Hints    []KeyHint
	Height   int       // visible rows; 0 means a sensible default
	Out      io.Writer // defaults to stdout; tests point it at io.Discard

	sel, top int
}

// KeyReader supplies keypresses. *Raw is the real one; tests script a slice.
type KeyReader interface{ ReadKey() Key }

// EscKey is the hint key for "go back", registered by screens that have a
// parent. It is matched by Esc, Left, and Backspace.
const EscKey = "\x1b"

// Selection is what a List returns.
type Selection struct {
	Index int    // chosen row, valid when Cmd is empty and OK is true
	Cmd   string // a hint key, when the user pressed one
	OK    bool   // false if the user quit or the terminal went away
}

// Run draws the list and handles keys until the user selects or quits. It
// assumes the terminal is already in raw mode.
func (l *List) Run(t KeyReader, start int) Selection {
	if len(l.Rows) == 0 {
		return Selection{OK: false}
	}
	l.sel = clamp(start, 0, len(l.Rows)-1)

	hideCursor()
	defer showCursor()

	for {
		l.draw()

		k := t.ReadKey()
		switch k.Type {
		case KeyInterrupt:
			return Selection{OK: false}
		case KeyEnter, KeyRight:
			return Selection{Index: l.sel, OK: true}
		case KeyUp:
			l.move(-1)
		case KeyDown:
			l.move(1)
		case KeyPageUp:
			l.move(-l.height())
		case KeyPageDown:
			l.move(l.height())
		case KeyHome:
			l.sel = 0
		case KeyEnd:
			l.sel = len(l.Rows) - 1
		case KeyEsc, KeyLeft, KeyBackspace:
			if cmd, ok := l.hintFor(EscKey); ok {
				return Selection{Cmd: cmd, OK: true}
			}
		case KeyRune:
			switch {
			case k.Rune == 'k':
				l.move(-1)
			case k.Rune == 'j':
				l.move(1)
			case k.Rune >= '1' && k.Rune <= '9':
				// Digits jump the cursor without selecting, so typing "1"
				// on the way to "10" doesn't fire the wrong row.
				if n := int(k.Rune - '1'); n < len(l.Rows) {
					l.sel = n
				}
			default:
				if cmd, ok := l.hintFor(string(k.Rune)); ok {
					return Selection{Cmd: cmd, OK: true}
				}
			}
		}
	}
}

func (l *List) hintFor(k string) (string, bool) {
	for _, h := range l.Hints {
		if h.Key != "" && h.Key == strings.ToLower(k) {
			return h.Key, true
		}
	}
	return "", false
}

func (l *List) move(d int) {
	l.sel = clamp(l.sel+d, 0, len(l.Rows)-1)
}

// height is how many rows fit, leaving room for the header and footer.
func (l *List) height() int {
	if l.Height > 0 {
		return l.Height
	}
	return 12
}

// scroll keeps the selection inside the visible window.
func (l *List) scroll() (top, bottom int) {
	h := min(l.height(), len(l.Rows))
	if l.sel < l.top {
		l.top = l.sel
	}
	if l.sel >= l.top+h {
		l.top = l.sel - h + 1
	}
	l.top = clamp(l.top, 0, max(0, len(l.Rows)-h))
	return l.top, l.top + h
}

func (l *List) draw() {
	var b strings.Builder
	b.WriteString("\033[H\033[2J") // home + clear

	b.WriteString("\r\n")
	b.WriteString("  " + Accent("▌") + " " + Bold(l.Title) + "\r\n")
	if l.Subtitle != "" {
		b.WriteString("    " + Dim(l.Subtitle) + "\r\n")
	}
	b.WriteString("\r\n")
	if l.Hint != "" {
		b.WriteString("  " + l.Hint + "\r\n\r\n")
	}

	widths := colWidths(l.Rows)
	top, bottom := l.scroll()

	// Every row renders to the same width so the highlight is a clean bar
	// rather than a ragged edge that tracks each row's content length.
	full := 0
	for i := top; i < bottom; i++ {
		full = max(full, visibleWidth(l.renderRow(l.Rows[i], widths)))
	}
	for i := top; i < bottom; i++ {
		line := l.renderRow(l.Rows[i], widths)
		line += strings.Repeat(" ", max(0, full-visibleWidth(line)))
		if i == l.sel {
			b.WriteString("  " + Accent("▸") + " " + Highlight(" "+line+" ") + "\r\n")
		} else {
			b.WriteString("     " + line + "\r\n")
		}
	}

	if len(l.Rows) > bottom-top {
		b.WriteString("\r\n    " + Dim(fmt.Sprintf("%d–%d of %d", top+1, bottom, len(l.Rows))) + "\r\n")
	}

	b.WriteString("\r\n  " + l.footer() + "\r\n")
	io.WriteString(l.out(), b.String())
}

func (l *List) out() io.Writer {
	if l.Out != nil {
		return l.Out
	}
	return os.Stdout
}

func (l *List) renderRow(cells []string, widths []int) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		pad := widths[i] - visibleWidth(c)
		if pad < 0 {
			pad = 0
		}
		if l.Right[i] {
			parts[i] = strings.Repeat(" ", pad) + c
		} else {
			parts[i] = c + strings.Repeat(" ", pad)
		}
	}
	return strings.Join(parts, "  ")
}

func (l *List) footer() string {
	parts := []string{Accent("↑↓") + Dim(" move"), Accent("⏎") + Dim(" select")}
	for _, h := range l.Hints {
		if h.Key == "" {
			parts = append(parts, Dim(h.Label))
			continue
		}
		parts = append(parts, Accent(keyLabel(h.Key))+Dim(" "+h.Label))
	}
	return strings.Join(parts, Dim("   "))
}

// keyLabel spells out keys that have no printable form.
func keyLabel(k string) string {
	if k == EscKey {
		return "esc"
	}
	return k
}

func colWidths(rows [][]string) []int {
	n := 0
	for _, r := range rows {
		n = max(n, len(r))
	}
	w := make([]int, n)
	for _, r := range rows {
		for i, c := range r {
			w[i] = max(w[i], visibleWidth(c))
		}
	}
	return w
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

func hideCursor() {
	if enabled {
		os.Stdout.WriteString("\033[?25l")
	}
}

func showCursor() {
	if enabled {
		os.Stdout.WriteString("\033[?25h")
	}
}
