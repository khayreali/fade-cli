package ui

import (
	"io"
	"strings"
	"testing"
)

// keys replays a scripted sequence, then behaves like a closed terminal so a
// test can never hang on an unconsumed list.
type keys struct {
	seq []Key
	i   int
}

func (k *keys) ReadKey() Key {
	if k.i >= len(k.seq) {
		return Key{Type: KeyInterrupt}
	}
	got := k.seq[k.i]
	k.i++
	return got
}

func down(n int) []Key {
	out := make([]Key, n)
	for i := range out {
		out[i] = Key{Type: KeyDown}
	}
	return out
}

func rows(n int) [][]string {
	out := make([][]string, n)
	for i := range out {
		out[i] = []string{string(rune('a' + i))}
	}
	return out
}

func newList(n int) *List {
	return &List{Title: "t", Rows: rows(n), Height: 4, Out: io.Discard}
}

func run(l *List, seq ...Key) Selection { return l.Run(&keys{seq: seq}, 0) }

func TestArrowsMoveAndEnterSelects(t *testing.T) {
	got := run(newList(5), Key{Type: KeyDown}, Key{Type: KeyDown}, Key{Type: KeyEnter})
	if !got.OK || got.Cmd != "" {
		t.Fatalf("got %+v, want a selection", got)
	}
	if got.Index != 2 {
		t.Errorf("Index = %d, want 2", got.Index)
	}
}

func TestUpFromTopStaysPut(t *testing.T) {
	// Clamping rather than wrapping: an accidental extra press shouldn't
	// teleport you to the far end of a long list.
	got := run(newList(5), Key{Type: KeyUp}, Key{Type: KeyUp}, Key{Type: KeyEnter})
	if got.Index != 0 {
		t.Errorf("Index = %d, want 0", got.Index)
	}
}

func TestDownPastEndStaysPut(t *testing.T) {
	seq := append(down(20), Key{Type: KeyEnter})
	if got := run(newList(5), seq...); got.Index != 4 {
		t.Errorf("Index = %d, want 4", got.Index)
	}
}

func TestVimKeysMove(t *testing.T) {
	got := run(newList(5),
		Key{Type: KeyRune, Rune: 'j'}, Key{Type: KeyRune, Rune: 'j'},
		Key{Type: KeyRune, Rune: 'k'}, Key{Type: KeyEnter})
	if got.Index != 1 {
		t.Errorf("Index = %d, want 1", got.Index)
	}
}

func TestHomeAndEnd(t *testing.T) {
	if got := run(newList(9), Key{Type: KeyEnd}, Key{Type: KeyEnter}); got.Index != 8 {
		t.Errorf("End gave %d, want 8", got.Index)
	}
	if got := run(newList(9), Key{Type: KeyEnd}, Key{Type: KeyHome}, Key{Type: KeyEnter}); got.Index != 0 {
		t.Errorf("Home gave %d, want 0", got.Index)
	}
}

func TestDigitsJumpButDoNotSelect(t *testing.T) {
	// Typing "1" on the way to "10" must not fire row 1.
	l := newList(9)
	got := run(l, Key{Type: KeyRune, Rune: '3'}, Key{Type: KeyEnter})
	if !got.OK || got.Index != 2 {
		t.Errorf("got %+v, want index 2 selected only after enter", got)
	}
}

func TestDigitBeyondListIsIgnored(t *testing.T) {
	got := run(newList(3), Key{Type: KeyRune, Rune: '9'}, Key{Type: KeyEnter})
	if got.Index != 0 {
		t.Errorf("Index = %d, want 0 -- out-of-range digit should not move", got.Index)
	}
}

func TestHintKeyReturnsCommand(t *testing.T) {
	l := newList(5)
	l.Hints = []KeyHint{{Key: "q", Label: "quit"}}
	got := run(l, Key{Type: KeyRune, Rune: 'q'})
	if !got.OK || got.Cmd != "q" {
		t.Errorf("got %+v, want Cmd q", got)
	}
}

func TestUnregisteredKeyIsIgnored(t *testing.T) {
	l := newList(5)
	l.Hints = []KeyHint{{Key: "q", Label: "quit"}}
	got := run(l, Key{Type: KeyRune, Rune: 'z'}, Key{Type: KeyDown}, Key{Type: KeyEnter})
	if got.Cmd != "" || got.Index != 1 {
		t.Errorf("got %+v, want a plain selection of index 1", got)
	}
}

func TestEscOnlyReturnsWhenRegistered(t *testing.T) {
	plain := run(newList(5), Key{Type: KeyEsc}, Key{Type: KeyEnter})
	if plain.Cmd != "" || plain.Index != 0 {
		t.Errorf("unregistered esc gave %+v, want it ignored", plain)
	}

	l := newList(5)
	l.Hints = []KeyHint{{Key: EscKey, Label: "back"}}
	if got := run(l, Key{Type: KeyEsc}); got.Cmd != EscKey {
		t.Errorf("registered esc gave %+v, want the back command", got)
	}
}

func TestInterruptReportsNotOK(t *testing.T) {
	if got := run(newList(5), Key{Type: KeyInterrupt}); got.OK {
		t.Errorf("ctrl-C gave %+v, want OK=false", got)
	}
}

func TestEmptyListDoesNotHang(t *testing.T) {
	l := &List{Title: "t", Out: io.Discard}
	if got := l.Run(&keys{}, 0); got.OK {
		t.Errorf("empty list gave %+v, want OK=false", got)
	}
}

func TestScrollFollowsSelectionPastTheWindow(t *testing.T) {
	l := newList(20) // Height 4
	l.Run(&keys{seq: append(down(9), Key{Type: KeyEnter})}, 0)

	top, bottom := l.scroll()
	if l.sel != 9 {
		t.Fatalf("sel = %d, want 9", l.sel)
	}
	if l.sel < top || l.sel >= bottom {
		t.Errorf("selection %d outside visible window [%d,%d)", l.sel, top, bottom)
	}
}

func TestStartIndexIsClamped(t *testing.T) {
	l := newList(3)
	if got := l.Run(&keys{seq: []Key{{Type: KeyEnter}}}, 99); got.Index != 2 {
		t.Errorf("Index = %d, want the last row", got.Index)
	}
}

// Raw mode disables the terminal's newline translation, so a bare \n would
// stair-step every row diagonally across the screen.
func TestDrawUsesCarriageReturns(t *testing.T) {
	var b strings.Builder
	l := &List{Title: "t", Rows: rows(3), Height: 3, Out: &b}
	l.Run(&keys{seq: []Key{{Type: KeyEnter}}}, 0)

	out := b.String()
	if strings.Count(out, "\n") == 0 {
		t.Fatal("nothing drawn")
	}
	if n := strings.Count(out, "\n") - strings.Count(out, "\r\n"); n != 0 {
		t.Errorf("%d newlines are missing their carriage return", n)
	}
}

func TestSelectedSurvivesInnerResets(t *testing.T) {
	SetDepth(DepthTrue)
	t.Cleanup(func() { SetDepth(DepthNone) })

	// A colored cell ends with a reset; without re-arming, the selection
	// wash would stop partway across the row.
	bg := Current().Selected.Bg.Bg()
	got := Selected("plain" + Green("green") + "tail")
	if strings.Count(got, bg) < 2 {
		t.Errorf("selection not re-armed after inner reset: %q", got)
	}
}

func TestSlashFiltersAndEnterOpensLoneMatch(t *testing.T) {
	l := &List{Title: "t", Filterable: true, Out: io.Discard, Rows: [][]string{
		{"Cabello Brooklyn"}, {"Power Of Barbers"}, {"SHEAR 483"},
	}}
	seq := []Key{{Type: KeyRune, Rune: '/'}}
	for _, r := range "pwr" { // letters in order, not a substring
		seq = append(seq, Key{Type: KeyRune, Rune: r})
	}
	seq = append(seq, Key{Type: KeyEnter})
	got := l.Run(&keys{seq: seq}, 0)
	if !got.OK || got.Index != 1 {
		t.Errorf("got %+v, want Power Of Barbers (index 1) opened directly", got)
	}
}

func TestFilterKeepsOriginalIndexes(t *testing.T) {
	l := &List{Title: "t", Filterable: true, Out: io.Discard, Rows: [][]string{
		{"alpha"}, {"beta"}, {"gamma"}, {"delta"},
	}}
	// "a" matches every row; confirm the filter, then move to the third
	// survivor and select it. Index must be the row's position in Rows.
	seq := []Key{{Type: KeyRune, Rune: '/'}, {Type: KeyRune, Rune: 't'}, {Type: KeyEnter}}
	seq = append(seq, Key{Type: KeyDown}, Key{Type: KeyEnter})
	got := l.Run(&keys{seq: seq}, 0)
	// "t": beta, delta survive; down once lands on delta = Rows[3].
	if !got.OK || got.Index != 3 {
		t.Errorf("got %+v, want index 3 (delta)", got)
	}
}

func TestHintKeysAreLettersWhileFiltering(t *testing.T) {
	l := &List{Title: "t", Filterable: true, Out: io.Discard, Rows: rows(5),
		Hints: []KeyHint{{Key: "q", Label: "quit"}}}
	// Typing q into the search must not quit.
	seq := []Key{{Type: KeyRune, Rune: '/'}, {Type: KeyRune, Rune: 'q'}, {Type: KeyEsc},
		{Type: KeyEnter}}
	got := l.Run(&keys{seq: seq}, 0)
	if !got.OK || got.Cmd != "" || got.Index != 0 {
		t.Errorf("got %+v, want a plain selection after clearing the filter", got)
	}
}

func TestEscClearsFilterBeforeGoingBack(t *testing.T) {
	l := &List{Title: "t", Filterable: true, Out: io.Discard,
		Rows:  [][]string{{"alpha"}, {"beta"}, {"gamma"}},
		Hints: []KeyHint{{Key: EscKey, Label: "back"}}}
	// "a" keeps all three rows, so enter confirms the filter rather than
	// opening a lone match.
	seq := []Key{{Type: KeyRune, Rune: '/'}, {Type: KeyRune, Rune: 'a'}, {Type: KeyEnter},
		{Type: KeyEsc}, {Type: KeyEsc}}
	got := l.Run(&keys{seq: seq}, 0)
	if !got.OK || got.Cmd != EscKey {
		t.Errorf("got %+v, want back only on the second esc", got)
	}
	if l.query != "" {
		t.Errorf("query still %q after esc", l.query)
	}
}

func TestHelpOverlayIsDismissedByAnyKey(t *testing.T) {
	l := newList(5)
	seq := []Key{{Type: KeyRune, Rune: '?'}, {Type: KeyDown}, {Type: KeyEnter}}
	// The down press only closes help; enter then selects row 0.
	if got := l.Run(&keys{seq: seq}, 0); !got.OK || got.Index != 0 {
		t.Errorf("got %+v, want index 0", got)
	}
}

func TestVimTopAndBottom(t *testing.T) {
	got := run(newList(9), Key{Type: KeyRune, Rune: 'G'}, Key{Type: KeyEnter})
	if got.Index != 8 {
		t.Errorf("G gave %d, want 8", got.Index)
	}
	got = run(newList(9), Key{Type: KeyRune, Rune: 'G'}, Key{Type: KeyRune, Rune: 'g'}, Key{Type: KeyEnter})
	if got.Index != 0 {
		t.Errorf("g gave %d, want 0", got.Index)
	}
}

func TestHeightFitsTheTerminal(t *testing.T) {
	l := newList(40)
	l.Height = 0
	l.Subtitle = "s"
	l.sizeFn = func() (int, int) { return 80, 20 }
	// 20 rows minus 7 lines of chrome (with a subtitle) leaves 13.
	if h := l.height(); h != 13 {
		t.Errorf("height = %d, want 13", h)
	}
	l.Height = 30
	if h := l.height(); h != 13 {
		t.Errorf("explicit height should still be capped to fit, got %d", h)
	}
}

func TestMatchPositions(t *testing.T) {
	if p := matchPositions("power of barbers", "pwr"); len(p) != 3 || p[0] != 0 || p[1] != 2 || p[2] != 4 {
		t.Errorf("subsequence positions = %v", p)
	}
	if p := matchPositions("cabello", "bell"); len(p) != 4 || p[0] != 2 {
		t.Errorf("substring positions = %v", p)
	}
	if p := matchPositions("cabello", "xyz"); p != nil {
		t.Errorf("miss gave %v", p)
	}
}
