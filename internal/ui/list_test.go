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

func TestHighlightSurvivesInnerResets(t *testing.T) {
	SetColor(true)
	t.Cleanup(func() { SetColor(false) })

	// A colored cell ends with a reset; without re-arming, the highlight bar
	// would stop partway across the row.
	got := Highlight("plain" + Green("green") + "tail")
	if strings.Count(got, reverse) < 2 {
		t.Errorf("highlight not re-armed after inner reset: %q", got)
	}
}
