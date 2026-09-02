package ui

import (
	"bufio"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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

	// Set up lazily by Keys/Resized for screens that select on both.
	watch   sync.Once
	keys    chan Key
	resized chan struct{}
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
// that terminals use for arrows and navigation keys. Once a reader goroutine
// owns stdin (see Keys), it is the only thing allowed to read, so ReadKey
// takes from its channel instead.
func (t *Raw) ReadKey() Key {
	if t.keys != nil {
		return <-t.keys
	}
	return t.readKey()
}

func (t *Raw) readKey() Key {
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

	if b == ']' {
		// An OSC reply (a late answer to the background query, say) is not
		// keys. Swallow it through its BEL or ST terminator.
		t.skipOSC()
		return t.ReadKey()
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

// skipOSC consumes the rest of an OSC sequence: everything up to BEL or
// ESC-backslash, with a size cap so a malformed stream can't hang input.
func (t *Raw) skipOSC() {
	for i := 0; i < 256; i++ {
		b, err := t.r.ReadByte()
		if err != nil || b == '\a' {
			return
		}
		if b == 27 {
			if n, err := t.r.ReadByte(); err != nil || n == '\\' {
				return
			}
		}
	}
}

// QueryBackground asks the terminal for its background color (OSC 11) and
// waits up to timeout for the answer. Most terminals reply in a millisecond;
// one that never replies costs the timeout once, which is why callers keep
// it short. Both ends must be a terminal.
func QueryBackground(timeout time.Duration) (Color, bool) {
	if !enabled || !IsInteractive() {
		return Color{}, false
	}
	fd := int(os.Stdin.Fd())
	state, err := term.MakeRaw(fd)
	if err != nil {
		return Color{}, false
	}
	defer term.Restore(fd, state)

	// os.Stdin is opened blocking, which rules out read deadlines; a second
	// File over the same descriptor in non-blocking mode gets them. It is
	// never closed -- that would close fd 0 -- and the descriptor goes back
	// to blocking afterwards so the bufio key reader behaves as before.
	if err := syscall.SetNonblock(fd, true); err != nil {
		return Color{}, false
	}
	defer syscall.SetNonblock(fd, false)
	f := os.NewFile(uintptr(fd), "stdin")
	if err := f.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return Color{}, false
	}

	os.Stdout.WriteString("\033]11;?\033\\")
	var buf []byte
	one := make([]byte, 1)
	for len(buf) < 64 {
		n, err := f.Read(one)
		if err != nil || n == 0 {
			break
		}
		buf = append(buf, one[0])
		if one[0] == '\a' || (len(buf) >= 2 && buf[len(buf)-2] == 27 && one[0] == '\\') {
			break
		}
	}
	return parseOSC11(string(buf))
}

// parseOSC11 reads "\e]11;rgb:RRRR/GGGG/BBBB\e\\" (or BEL-terminated, or
// two-digit channels) into a Color.
func parseOSC11(s string) (Color, bool) {
	i := strings.Index(s, "rgb:")
	if i < 0 {
		return Color{}, false
	}
	s = strings.TrimRight(s[i+4:], "\a\\\033")
	parts := strings.Split(s, "/")
	if len(parts) != 3 {
		return Color{}, false
	}
	var out [3]uint8
	for k, p := range parts {
		if len(p) == 0 || len(p) > 4 {
			return Color{}, false
		}
		v, err := strconv.ParseUint(p, 16, 16)
		if err != nil {
			return Color{}, false
		}
		// Channels come as 1-4 hex digits; the leading byte is the value.
		switch len(p) {
		case 1:
			out[k] = uint8(v * 17)
		case 2:
			out[k] = uint8(v)
		default:
			out[k] = uint8(v >> (4 * uint(len(p)-2)))
		}
	}
	return Color{out[0], out[1], out[2]}, true
}

// backgroundFromEnv reads the COLORFGBG hint ("15;0" is white on black)
// that rxvt-style terminals export, for when OSC 11 goes unanswered.
func backgroundFromEnv() (Color, bool) {
	v := os.Getenv("COLORFGBG")
	if v == "" {
		return Color{}, false
	}
	parts := strings.Split(v, ";")
	idx, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil || idx < 0 || idx > 15 {
		return Color{}, false
	}
	basics := []Color{
		{0, 0, 0}, {205, 49, 49}, {13, 188, 121}, {229, 229, 16},
		{36, 114, 200}, {188, 63, 188}, {17, 168, 205}, {229, 229, 229},
		{102, 102, 102}, {241, 76, 76}, {35, 209, 139}, {245, 245, 67},
		{59, 142, 234}, {214, 112, 214}, {41, 184, 219}, {255, 255, 255},
	}
	return basics[idx], true
}

// AdaptToTerminal finds the terminal's background and re-tunes the active
// theme to stay legible on it. Nothing happens when color is off or the
// terminal keeps its background to itself, in which case the theme is
// rendered as designed, for a dark ground.
func AdaptToTerminal() {
	bg, ok := QueryBackground(80 * time.Millisecond)
	if !ok {
		bg, ok = backgroundFromEnv()
	}
	if ok {
		SetBackground(bg)
	}
}

// Width returns the terminal width, defaulting to 80 when it can't be read.
func Width() int {
	w, _ := Size()
	return w
}

// Size returns the terminal's columns and rows, with an 80x24 fallback so
// layout code never divides by zero on a pipe.
func Size() (cols, rows int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// Resized reports terminal size changes. A screen that selects on this
// alongside its key channel can redraw at the new size instead of waiting
// for the next keypress to notice it.
func (t *Raw) Resized() <-chan struct{} {
	t.startWatching()
	return t.resized
}

// Keys delivers keypresses on a channel, for screens that need to select
// between input and resize events. Once started, the reader goroutine owns
// stdin for the life of the Raw session; ReadKey keeps working through it.
func (t *Raw) Keys() <-chan Key {
	t.startWatching()
	return t.keys
}

func (t *Raw) startWatching() {
	t.watch.Do(func() {
		t.keys = make(chan Key, 8)
		t.resized = make(chan struct{}, 1)

		winch := make(chan os.Signal, 1)
		signal.Notify(winch, syscall.SIGWINCH)
		go func() {
			for range winch {
				// Coalesce a burst of resize signals into one pending redraw.
				select {
				case t.resized <- struct{}{}:
				default:
				}
			}
		}()
		go func() {
			for {
				k := t.readKey()
				t.keys <- k
				if k.Type == KeyInterrupt {
					return
				}
			}
		}()
	})
}
