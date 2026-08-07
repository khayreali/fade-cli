package ui

import (
	"bufio"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// KeyType distinguishes navigation keys from typed characters.
type KeyType int

const (
	KeyRune KeyType = iota
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyEnter
	KeyEsc
	KeyBackspace
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyInterrupt // ctrl-C or ctrl-D
)

// Key is one keypress. Rune is meaningful only when Type is KeyRune.
type Key struct {
	Type KeyType
	Rune rune
}

// Raw puts the terminal in raw mode so arrow keys arrive as keypresses rather
// than as literal "^[[B" text on a line-buffered read. Callers must call the
// returned restore func -- a program that exits without it leaves the user's
// shell with echo disabled.
type Raw struct {
	fd    int
	state *term.State
	r     *bufio.Reader
	sigs  chan os.Signal
	once  sync.Once
}

// EnterRaw switches to raw mode. It returns ok=false when stdin isn't a
// terminal or the mode switch fails, in which case callers fall back to the
// line-based prompts.
func EnterRaw() (*Raw, bool) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, false
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false
	}
	t := &Raw{fd: fd, state: state, r: bufio.NewReader(os.Stdin)}

	// A deferred Restore covers normal exits and panics, but not a signal --
	// and a process killed in raw mode leaves the user's shell with no echo
	// and no line editing, which looks like a broken terminal.
	t.sigs = make(chan os.Signal, 1)
	signal.Notify(t.sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		if _, ok := <-t.sigs; !ok {
			return
		}
		t.Restore()
		os.Exit(130)
	}()

	return t, true
}

// Restore returns the terminal to its previous mode. Safe to call repeatedly
// and from the signal goroutine concurrently with the deferred call.
func (t *Raw) Restore() {
	if t == nil || t.state == nil {
		return
	}
	t.once.Do(func() {
		signal.Stop(t.sigs)
		close(t.sigs)
		showCursor()
		_ = term.Restore(t.fd, t.state)
	})
}

// Suspend drops back to cooked mode for the duration of fn, so line-based
// prompts (which need echo and line editing) can run inside a raw-mode
// session. The original terminal state is kept so this can nest with Restore.
func (t *Raw) Suspend(fn func()) {
	if t == nil || t.state == nil {
		fn()
		return
	}
	_ = term.Restore(t.fd, t.state)
	showCursor()
	fn()
	if _, err := term.MakeRaw(t.fd); err != nil {
		return
	}
	hideCursor()
}

// ReadKey blocks for a single keypress, decoding the ANSI escape sequences
// that terminals use for arrows and navigation keys.
func (t *Raw) ReadKey() Key {
	b, err := t.r.ReadByte()
	if err != nil {
		return Key{Type: KeyInterrupt}
	}

	switch b {
	case 3, 4: // ctrl-C, ctrl-D
		return Key{Type: KeyInterrupt}
	case 13, 10: // CR, LF
		return Key{Type: KeyEnter}
	case 127, 8:
		return Key{Type: KeyBackspace}
	case 27:
		return t.readEscape()
	}
	if b < 32 {
		return Key{Type: KeyRune, Rune: rune(b)}
	}
	return Key{Type: KeyRune, Rune: rune(b)}
}

// readEscape decodes a CSI sequence. Terminals deliver the whole sequence in
// one write, so an escape with nothing buffered behind it is a bare Esc press
// rather than the start of a sequence -- checking Buffered avoids blocking
// forever waiting for a continuation that will never come.
func (t *Raw) readEscape() Key {
	if t.r.Buffered() == 0 {
		return Key{Type: KeyEsc}
	}
	b, err := t.r.ReadByte()
	if err != nil {
		return Key{Type: KeyEsc}
	}
	if b != '[' && b != 'O' {
		return Key{Type: KeyEsc}
	}

	b, err = t.r.ReadByte()
	if err != nil {
		return Key{Type: KeyEsc}
	}
	switch b {
	case 'A':
		return Key{Type: KeyUp}
	case 'B':
		return Key{Type: KeyDown}
	case 'C':
		return Key{Type: KeyRight}
	case 'D':
		return Key{Type: KeyLeft}
	case 'H':
		return Key{Type: KeyHome}
	case 'F':
		return Key{Type: KeyEnd}
	}

	// Numeric sequences like ESC[5~ (page up) carry digits then a tilde.
	if b >= '0' && b <= '9' {
		digits := []byte{b}
		for {
			n, err := t.r.ReadByte()
			if err != nil {
				return Key{Type: KeyEsc}
			}
			if n == '~' {
				break
			}
			if n < '0' || n > '9' {
				return Key{Type: KeyEsc}
			}
			digits = append(digits, n)
		}
		switch string(digits) {
		case "1", "7":
			return Key{Type: KeyHome}
		case "4", "8":
			return Key{Type: KeyEnd}
		case "5":
			return Key{Type: KeyPageUp}
		case "6":
			return Key{Type: KeyPageDown}
		}
	}
	return Key{Type: KeyEsc}
}

// Width returns the terminal width, defaulting to 80 when it can't be read.
func Width() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
