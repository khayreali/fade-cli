package ui

import (
	"bufio"
	"strings"
	"testing"
)

func rawOver(input string) *Raw {
	return &Raw{r: bufio.NewReader(strings.NewReader(input))}
}

func TestReadKeyDecodesArrowsAndPaging(t *testing.T) {
	cases := map[string]KeyType{
		"\x1b[A": KeyUp, "\x1b[B": KeyDown, "\x1b[C": KeyRight, "\x1b[D": KeyLeft,
		"\x1bOA": KeyUp, "\x1b[H": KeyHome, "\x1b[F": KeyEnd,
		"\x1b[5~": KeyPageUp, "\x1b[6~": KeyPageDown, "\x1b[1~": KeyHome,
		"\x1b[1;5C": KeyRight, // ctrl-right carries a modifier parameter
		"\r":        KeyEnter, "\x7f": KeyBackspace, "\x03": KeyInterrupt,
	}
	for in, want := range cases {
		if got := rawOver(in).readKey(); got.Type != want {
			t.Errorf("%q decoded as %v, want %v", in, got.Type, want)
		}
	}
}

func TestBareEscapeIsEsc(t *testing.T) {
	if got := rawOver("\x1b").readKey(); got.Type != KeyEsc {
		t.Errorf("bare ESC decoded as %v", got.Type)
	}
}

// A mouse report or an unknown control sequence must vanish whole. Before,
// its tail leaked into a prompt as typed letters.
func TestUnknownSequencesAreSwallowed(t *testing.T) {
	for _, in := range []string{
		"\x1b[<0;10;20M", // SGR mouse press
		"\x1b[<64;10;20m",
		"\x1b[?1;2c",                       // device attributes reply
		"\x1b]11;rgb:0000/0000/0000\x1b\\", // OSC background reply
		"\x1b]11;rgb:0000/0000/0000\a",
	} {
		r := rawOver(in + "x")
		if got := r.readKey(); got.Type != KeyRune || got.Rune != 'x' {
			t.Errorf("after %q got %+v, want the x that follows", in, got)
		}
	}
}

func TestReadKeyDecodesUTF8(t *testing.T) {
	r := rawOver("é")
	if got := r.readKey(); got.Type != KeyRune || got.Rune != 'é' {
		t.Errorf("got %+v, want the rune é", got)
	}
}

func TestEditLineIgnoresNavigationKeys(t *testing.T) {
	var out strings.Builder
	seq := []Key{
		{Type: KeyRune, Rune: '4'}, {Type: KeyUp}, {Type: KeyDown}, {Type: KeyLeft},
		{Type: KeyRune, Rune: '5'}, {Type: KeyPageDown}, {Type: KeyEnter},
	}
	got, ok := editLine(&keys{seq: seq}, &out, nil)
	if !ok || got != "45" {
		t.Errorf("got (%q, %v), want (\"45\", true)", got, ok)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Errorf("navigation keys were echoed: %q", out.String())
	}
}

func TestEditLineBackspaceAndFilter(t *testing.T) {
	var out strings.Builder
	digits := func(r rune) bool { return r >= '0' && r <= '9' }
	seq := []Key{
		{Type: KeyRune, Rune: 'a'}, // filtered out
		{Type: KeyRune, Rune: '7'}, {Type: KeyRune, Rune: '8'}, {Type: KeyBackspace},
		{Type: KeyRune, Rune: '9'}, {Type: KeyEnter},
	}
	got, ok := editLine(&keys{seq: seq}, &out, digits)
	if !ok || got != "79" {
		t.Errorf("got (%q, %v), want (\"79\", true)", got, ok)
	}
	if !strings.Contains(out.String(), "\b \b") {
		t.Error("backspace did not erase on screen")
	}
}

func TestEditLineEscAbandons(t *testing.T) {
	var out strings.Builder
	seq := []Key{{Type: KeyRune, Rune: 'y'}, {Type: KeyEsc}}
	if got, ok := editLine(&keys{seq: seq}, &out, nil); ok || got != "" {
		t.Errorf("esc gave (%q, %v), want abandoned", got, ok)
	}
}

func TestParseIntRetriesGarbageAndAcceptsDollar(t *testing.T) {
	if n, ok, done := parseInt("abc", 0); done || ok || n != 0 {
		t.Errorf("garbage gave (%d, %v, done=%v), want a retry", n, ok, done)
	}
	if n, ok, done := parseInt("$42", 0); !done || !ok || n != 42 {
		t.Errorf("$42 gave (%d, %v, done=%v)", n, ok, done)
	}
	if n, ok, done := parseInt("", 55); !done || !ok || n != 55 {
		t.Errorf("bare enter gave (%d, %v, done=%v), want the default", n, ok, done)
	}
}
