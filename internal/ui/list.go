package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
)

// KeyHint is one command shown in a list's footer.
type KeyHint struct {
	Key   string // the character that triggers it, "" for display-only hints
	Label string
}

// listState gates which keys mean what, the way glow crosses a view state
// with a filter state: while typing a search, "q" is a letter, not quit.
type listState int

const (
	listReady listState = iota
	listFiltering
	listHelp
)

// List is a keyboard-navigable menu. Arrows move, enter selects, any
// registered hint key returns as a command, "/" filters and "?" explains.
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
	Height   int       // visible rows; 0 fits the terminal
	Out      io.Writer // defaults to stdout; tests point it at io.Discard
	// Note is a one-shot message for the status bar -- "saved", "nothing
	// open right now" -- cleared by the next keypress.
	Note string
	// Filterable turns on "/" search across the visible text of each row.
	Filterable bool
	// Sections marks rows that are headings, by index into Rows: drawn as a
	// ruled label, skipped by the cursor, never returned, and dropped while
	// a search is open so the matches read as one flat list. A heading row
	// is {name, meta}.
	Sections map[int]bool

	state  listState
	query  string
	view   []int // indexes into Rows that survive the filter
	sel    int   // position within view
	top    int
	sizeFn func() (int, int)
}

// KeyReader supplies keypresses. *Raw is the real one; tests script a slice.
type KeyReader interface{ ReadKey() Key }

// eventReader is what *Raw also satisfies: a key or a resize, whichever
// comes first, so the list can redraw when the window changes rather than
// when the user next touches a key.
type eventReader interface {
	NextEvent() (k Key, resized bool)
}

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
	l.state, l.query = listReady, ""
	l.refilter()
	l.sel = l.settle(clamp(start, 0, len(l.view)-1), 1)

	hideCursor()
	defer showCursor()

	src, live := t.(eventReader)
	for {
		l.draw()

		var k Key
		if live {
			var resized bool
			if k, resized = src.NextEvent(); resized {
				continue
			}
		} else {
			k = t.ReadKey()
		}
		l.Note = ""

		if k.Type == KeyInterrupt {
			return Selection{OK: false}
		}
		var sel Selection
		var done bool
		switch l.state {
		case listHelp:
			l.state = listReady
		case listFiltering:
			sel, done = l.keyFiltering(k)
		default:
			sel, done = l.keyReady(k)
		}
		if done {
			return sel
		}
	}
}

func (l *List) keyReady(k Key) (Selection, bool) {
	switch k.Type {
	case KeyEnter, KeyRight:
		return l.choose()
	case KeyUp:
		l.move(-1)
	case KeyDown:
		l.move(1)
	case KeyPageUp:
		l.move(-l.height())
	case KeyPageDown:
		l.move(l.height())
	case KeyHome:
		l.sel = l.settle(0, 1)
	case KeyEnd:
		l.sel = l.settle(len(l.view)-1, -1)
	case KeyEsc, KeyLeft, KeyBackspace:
		if l.query != "" {
			// A confirmed filter is the first thing esc peels away.
			l.query = ""
			l.refilter()
			return Selection{}, false
		}
		if cmd, ok := l.hintFor(EscKey); ok {
			return Selection{Cmd: cmd, OK: true}, true
		}
	case KeyRune:
		switch {
		case k.Rune == 'k':
			l.move(-1)
		case k.Rune == 'j':
			l.move(1)
		case k.Rune == 'g':
			l.sel = l.settle(0, 1)
		case k.Rune == 'G':
			l.sel = l.settle(len(l.view)-1, -1)
		case k.Rune == '/' && l.Filterable:
			l.state = listFiltering
		case k.Rune == '?':
			l.state = listHelp
		case k.Rune >= '1' && k.Rune <= '9':
			// Digits jump the cursor without selecting, so typing "1" on
			// the way to "10" doesn't fire the wrong row. They count
			// selectable rows, so headings don't shift the numbering.
			if i, ok := l.nthSelectable(int(k.Rune - '1')); ok {
				l.sel = i
			}
		default:
			if cmd, ok := l.hintFor(string(k.Rune)); ok {
				return Selection{Cmd: cmd, OK: true}, true
			}
		}
	}
	return Selection{}, false
}

// keyFiltering: letters build the query, arrows still move, enter confirms
// (and opens a lone match outright, as glow does), esc abandons.
func (l *List) keyFiltering(k Key) (Selection, bool) {
	switch k.Type {
	case KeyEsc:
		l.query = ""
		l.state = listReady
		l.refilter()
	case KeyEnter:
		l.state = listReady
		if len(l.view) == 1 {
			return l.choose()
		}
	case KeyBackspace:
		if l.query == "" {
			l.state = listReady
			return Selection{}, false
		}
		r := []rune(l.query)
		l.query = string(r[:len(r)-1])
		l.refilter()
	case KeyUp:
		l.move(-1)
	case KeyDown:
		l.move(1)
	case KeyRune:
		if unicode.IsPrint(k.Rune) {
			l.query += string(k.Rune)
			l.refilter()
		}
	}
	return Selection{}, false
}

// Cursor is the row under the highlight, as an index into Rows, for commands
// that act on it without selecting it -- saving a shop, for one. It returns
// -1 when there is no selectable row under the cursor (an empty or
// filtered-to-nothing view, or a section heading), so a caller never mistakes
// "nothing here" for row 0.
func (l *List) Cursor() int {
	if l.sel < 0 || l.sel >= len(l.view) || l.isSection(l.sel) {
		return -1
	}
	return l.view[l.sel]
}

func (l *List) choose() (Selection, bool) {
	if len(l.view) == 0 || l.isSection(l.sel) {
		return Selection{}, false
	}
	return Selection{Index: l.view[l.sel], OK: true}, true
}

// isSection reports whether the view row is a heading.
func (l *List) isSection(viewIdx int) bool {
	return viewIdx >= 0 && viewIdx < len(l.view) && l.Sections[l.view[viewIdx]]
}

// settle walks from i in direction dir to the nearest selectable row,
// turning back if it hits the edge, so the cursor never rests on a heading.
func (l *List) settle(i, dir int) int {
	n := len(l.view)
	if n == 0 {
		return 0
	}
	i = clamp(i, 0, n-1)
	for j := i; j >= 0 && j < n; j += dir {
		if !l.isSection(j) {
			return j
		}
	}
	for j := i; j >= 0 && j < n; j -= dir {
		if !l.isSection(j) {
			return j
		}
	}
	return i
}

// nthSelectable maps a digit key to the n-th non-heading row.
func (l *List) nthSelectable(n int) (int, bool) {
	for i := range l.view {
		if l.isSection(i) {
			continue
		}
		if n == 0 {
			return i, true
		}
		n--
	}
	return 0, false
}

// selectableBefore counts non-heading rows above view index i, for the
// position readout: "3 of 22" should count shops, not labels.
func (l *List) selectableBefore(i int) int {
	n := 0
	for j := 0; j < i && j < len(l.view); j++ {
		if !l.isSection(j) {
			n++
		}
	}
	return n
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
	if len(l.view) == 0 {
		return
	}
	target := clamp(l.sel+d, 0, len(l.view)-1)
	dir := 1
	if d < 0 {
		dir = -1
	}
	// Landing on a heading, step past it to the nearest selectable row in
	// the direction of travel (settle turns back at an edge). A single step
	// that would only reach a heading naturally resolves to where the cursor
	// already is, so this needs no special case for arrows vs paging -- the
	// earlier version's edge guard made PageUp inert near a top heading.
	if l.isSection(target) {
		next := l.settle(target, dir)
		if l.isSection(next) {
			return // nothing selectable to land on
		}
		target = next
	}
	l.sel = target
}

// refilter rebuilds the visible set for the current query, keeping the
// cursor on the same row when it survives. Headings are shown only when no
// search is open.
func (l *List) refilter() {
	keep := -1
	if l.sel < len(l.view) {
		keep = l.view[l.sel]
	}
	l.view = l.view[:0]
	for i, r := range l.Rows {
		if l.Sections[i] {
			if l.query == "" {
				l.view = append(l.view, i)
			}
			continue
		}
		if l.query == "" || matches(rowText(r), l.query) {
			l.view = append(l.view, i)
		}
	}
	l.sel = 0
	for i, idx := range l.view {
		if idx == keep {
			l.sel = i
		}
	}
	l.sel = l.settle(l.sel, 1)
	l.top = 0
}

func rowText(cells []string) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = stripANSI(c)
	}
	return strings.Join(parts, " ")
}

// matches is a forgiving search: a substring first, else the query's letters
// in order ("pwr" finds Power Of Barbers). Case-insensitive.
func matches(text, query string) bool {
	text, query = strings.ToLower(text), strings.ToLower(query)
	if strings.Contains(text, query) {
		return true
	}
	return matchPositions(text, query) != nil
}

// matchPositions returns the visible-rune indexes the query lands on, or nil.
func matchPositions(text, query string) []int {
	text, query = strings.ToLower(text), strings.ToLower(query)
	if query == "" {
		return nil
	}
	if i := strings.Index(text, query); i >= 0 {
		start := len([]rune(text[:i]))
		out := make([]int, 0, len([]rune(query)))
		for j := range []rune(query) {
			out = append(out, start+j)
		}
		return out
	}
	var out []int
	q := []rune(query)
	qi := 0
	for ti, r := range []rune(text) {
		if qi < len(q) && r == q[qi] {
			out = append(out, ti)
			qi++
		}
	}
	if qi < len(q) {
		return nil
	}
	return out
}

// height is how many rows fit, leaving room for the header and footer.
func (l *List) height() int {
	cols, rows := l.size()
	chrome := 5 // top blank, title, blank, blank, status
	chrome += len(l.footerLines(cols))
	if l.Subtitle != "" {
		chrome++
	}
	if l.Hint != "" {
		chrome += 2
	}
	if l.Hint != "" {
		// A multi-line hint (the shop screen's panels) costs its line count.
		chrome += strings.Count(l.Hint, "\n")
	}
	fit := max(3, rows-chrome)
	if l.Height > 0 {
		return min(l.Height, fit)
	}
	return min(fit, 14)
}

func (l *List) size() (int, int) {
	if l.sizeFn != nil {
		return l.sizeFn()
	}
	return Size()
}

// scroll keeps the selection inside the visible window.
func (l *List) scroll() (top, bottom int) {
	h := min(l.height(), len(l.view))
	if l.sel < l.top {
		l.top = l.sel
	}
	if l.sel >= l.top+h {
		l.top = l.sel - h + 1
	}
	l.top = clamp(l.top, 0, max(0, len(l.view)-h))
	return l.top, l.top + h
}

func (l *List) draw() {
	g := Sym()
	cols, _ := l.size()
	var b strings.Builder
	// Synchronized output: the terminal holds the frame until the closing
	// sequence, so a redraw never shows half a screen.
	b.WriteString("\033[?2026h\033[H\033[2J")

	b.WriteString("\r\n")
	b.WriteString("  " + Title(l.Title) + "\r\n")
	if l.Subtitle != "" {
		b.WriteString("  " + Subtle(l.Subtitle) + "\r\n")
	}
	b.WriteString("\r\n")
	if l.Hint != "" {
		for _, line := range strings.Split(l.Hint, "\n") {
			b.WriteString("  " + line + "\r\n")
		}
		b.WriteString("\r\n")
	}

	if l.state == listHelp {
		for _, line := range l.helpLines(cols) {
			b.WriteString("  " + line + "\r\n")
		}
	} else if len(l.view) == 0 {
		b.WriteString("    " + Subtle("Nothing found.") + "\r\n")
		for i := 1; i < l.height(); i++ {
			b.WriteString("\r\n")
		}
	} else {
		widths := l.colWidths()
		top, bottom := l.scroll()
		// Every row renders to the same width so the selection is a clean
		// bar rather than a ragged edge that tracks each row's content.
		full := 0
		for i := top; i < bottom; i++ {
			if !l.isSection(i) {
				full = max(full, visibleWidth(l.renderRow(l.Rows[l.view[i]], widths)))
			}
		}
		full = min(full, cols-6)
		for i := top; i < bottom; i++ {
			if l.isSection(i) {
				b.WriteString("  " + l.renderSection(l.Rows[l.view[i]], full+2) + "\r\n")
				continue
			}
			line := truncate(l.renderRow(l.Rows[l.view[i]], widths), full)
			// Match positions are measured on the truncated line that is
			// actually drawn -- padded like the render, so columns after the
			// first aren't shifted, and clipped at the cut so a match past the
			// ellipsis isn't underlined onto the ellipsis glyph.
			if l.query != "" {
				if p := matchPositions(stripANSI(line), l.query); len(p) > 0 {
					line = underlineAt(line, p)
				}
			}
			line += strings.Repeat(" ", max(0, full-visibleWidth(line)))
			if i == l.sel {
				b.WriteString("  " + Accent(g.Gutter) + Selected(" "+line+" ") + "\r\n")
			} else {
				b.WriteString("    " + line + "\r\n")
			}
		}
		for i := bottom - top; i < l.height(); i++ {
			b.WriteString("\r\n")
		}
	}

	b.WriteString("\r\n  " + l.statusBar(cols) + "\r\n")
	for _, line := range l.footerLines(cols) {
		b.WriteString("  " + line + "\r\n")
	}
	b.WriteString("\033[?2026l")
	io.WriteString(l.out(), b.String())
}

// statusBar is glow's pager bar: wordmark pill, then what the screen is
// doing, then where you are in it.
func (l *List) statusBar(cols int) string {
	left := LogoPill("fade")
	switch {
	case l.state == listFiltering:
		left += " " + Accent("Find: ") + l.query + Accent("_")
	case l.state == listHelp:
		left += " " + Subtle("keys")
	case l.Note != "":
		left += " " + Warning(l.Note)
	case l.query != "":
		left += " " + Subtle(fmt.Sprintf("%d matching %q", len(l.view), l.query))
	}

	right := ""
	if len(l.view) > 0 && l.state != listHelp {
		top, bottom := l.scroll()
		first, last := l.selectableBefore(top)+1, l.selectableBefore(bottom)
		total := l.selectableBefore(len(l.view))
		if total > 0 {
			right = Subtle(fmt.Sprintf("%d–%d of %d", first, last, total))
			if len(l.view) > bottom-top {
				right += Subtle(fmt.Sprintf("  %3d%%", last*100/total))
			}
		}
	}
	gap := cols - 4 - visibleWidth(left) - visibleWidth(right)
	return left + strings.Repeat(" ", max(1, gap)) + right
}

// footerLines is the key strip, wrapped onto as many lines as the width
// needs. The arrow hints are the first to go when space is tight: a narrow
// terminal should lose "move" before it loses a real command.
func (l *List) footerLines(cols int) []string {
	g := Sym()
	var parts []string
	if l.state == listFiltering {
		parts = []string{
			Subtle("type to narrow"), Accent(g.Enter) + Subtle(" keep"), Accent("esc") + Subtle(" clear"),
		}
	} else {
		if l.Filterable {
			parts = append(parts, Accent("/")+Subtle(" find"))
		}
		for _, h := range l.Hints {
			if h.Key == "" {
				parts = append(parts, Subtle(h.Label))
				continue
			}
			parts = append(parts, Accent(keyLabel(h.Key))+Subtle(" "+h.Label))
		}
		parts = append(parts, Accent("?")+Subtle(" keys"))
		basics := []string{Accent(g.Up+g.Down) + Subtle(" move"), Accent(g.Enter) + Subtle(" select")}
		if visibleWidth(strings.Join(append(basics, parts...), "   ")) <= cols-4 {
			parts = append(basics, parts...)
		}
	}

	sep := Subtle("   ")
	var lines []string
	var cur []string
	width := 0
	for _, p := range parts {
		w := visibleWidth(p)
		if len(cur) > 0 && width+3+w > cols-4 {
			lines = append(lines, strings.Join(cur, sep))
			cur, width = nil, 0
		}
		if len(cur) > 0 {
			width += 3
		}
		cur = append(cur, p)
		width += w
	}
	if len(cur) > 0 {
		lines = append(lines, strings.Join(cur, sep))
	}
	return lines
}

// helpLines is the full key reference, laid out btop-style in a titled box.
func (l *List) helpLines(cols int) []string {
	g := Sym()
	type entry struct{ key, desc string }
	rows := []entry{
		{g.Up + g.Down + ", j/k", "move"},
		{"g / G", "top / bottom"},
		{"pgup / pgdn", "page"},
		{"1-9", "jump to a row"},
		{g.Enter + " / " + g.Right, "select"},
	}
	if l.Filterable {
		rows = append(rows, entry{"/", "find (letters in order match)"})
	}
	for _, h := range l.Hints {
		if h.Key != "" {
			rows = append(rows, entry{keyLabel(h.Key), h.Label})
		}
	}
	rows = append(rows, entry{"?", "this list"}, entry{"ctrl-c", "leave"})

	lines := []string{Subtle(fmt.Sprintf("%-16s%s", "Key", "Description"))}
	for _, r := range rows {
		lines = append(lines, Accent(fmt.Sprintf("%-16s", r.key))+r.desc)
	}
	return Box("Keys", lines, min(cols-4, 60))
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

// underlineAt underlines the visible runes at the given indexes, stepping
// over escape sequences so the row's own colors survive.
func underlineAt(s string, positions []int) string {
	if !enabled {
		return s
	}
	want := map[int]bool{}
	for _, p := range positions {
		want[p] = true
	}
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
			if want[n] {
				b.WriteString(underline + string(r) + "\033[24m")
			} else {
				b.WriteRune(r)
			}
			n++
		}
	}
	return b.String()
}

// keyLabel spells out keys that have no printable form.
func keyLabel(k string) string {
	if k == EscKey {
		return "esc"
	}
	return k
}

// renderSection draws a heading as a ruled label: two dashes, the name, its
// meta, and a rule out to the row width.
func (l *List) renderSection(cells []string, width int) string {
	g := Sym()
	name := ""
	if len(cells) > 0 {
		name = cells[0]
	}
	label := Border(g.H+g.H) + " " + Title(name)
	if len(cells) > 1 && cells[1] != "" {
		label += " " + Subtle(g.Divider+" "+cells[1])
	}
	label += " "
	fill := width - visibleWidth(label)
	if fill > 0 {
		label += Border(strings.Repeat(g.H, fill))
	}
	return label
}

// colWidths measures the selectable rows; headings have their own layout.
func (l *List) colWidths() []int {
	n := 0
	for i, r := range l.Rows {
		if !l.Sections[i] {
			n = max(n, len(r))
		}
	}
	w := make([]int, n)
	for i, r := range l.Rows {
		if l.Sections[i] {
			continue
		}
		for j, c := range r {
			w[j] = max(w[j], visibleWidth(c))
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
