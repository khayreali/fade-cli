package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// stdin is shared across every prompt and the raw-mode key reader. A fresh
// bufio.Reader per call would discard anything already buffered, which
// silently eats fast typing.
var stdin = bufio.NewReader(os.Stdin)

// IsInteractive reports whether we can run a menu-driven flow. Both ends must
// be a terminal: piped input means the caller wants the flag interface.
func IsInteractive() bool {
	for _, f := range []*os.File{os.Stdin, os.Stdout} {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
			return false
		}
	}
	return true
}

// Line prompts and reads one trimmed line. ok is false on ctrl-C, ctrl-D or
// esc, which is how any prompt is abandoned.
//
// At a terminal the prompt reads keys itself in raw mode and echoes only
// what belongs in the answer: an arrow key, a scroll gesture or a mouse
// report is consumed silently instead of being echoed back as ^[[A the way
// the terminal's own line editor would. On a pipe it reads a plain line.
func Line(prompt string) (text string, ok bool) { return ask(prompt, nil) }

// ask is Line with an optional filter on which characters are accepted.
func ask(prompt string, accept func(rune) bool) (string, bool) {
	if !IsInteractive() {
		return lineFrom(stdin, prompt)
	}
	fmt.Print(Dim("  › ") + prompt)
	raw, ok := EnterRaw()
	if !ok {
		return readCooked(stdin)
	}
	defer raw.Restore()
	return editLine(raw, os.Stdout, accept)
}

// editLine is the raw-mode line editor: printable runes are kept and echoed,
// backspace erases, enter submits, esc and ctrl-C abandon. Everything else
// -- arrows, paging, mouse -- is ignored, because there is nothing for it to
// do on one line and echoing it would be noise.
func editLine(keys KeyReader, out io.Writer, accept func(rune) bool) (string, bool) {
	var buf []rune
	for {
		k := keys.ReadKey()
		switch k.Type {
		case KeyInterrupt, KeyEsc:
			io.WriteString(out, "\r\n")
			return "", false
		case KeyEnter:
			io.WriteString(out, "\r\n")
			return strings.TrimSpace(string(buf)), true
		case KeyBackspace:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				io.WriteString(out, "\b \b")
			}
		case KeyRune:
			if !unicode.IsPrint(k.Rune) || (accept != nil && !accept(k.Rune)) {
				continue
			}
			buf = append(buf, k.Rune)
			io.WriteString(out, string(k.Rune))
		}
	}
}

// lineFrom prompts and reads one line from a cooked-mode reader -- the
// path for pipes and for tests.
func lineFrom(r *bufio.Reader, prompt string) (text string, ok bool) {
	fmt.Print(Dim("  › ") + prompt)
	return readCooked(r)
}

func readCooked(r *bufio.Reader) (text string, ok bool) {
	s, err := r.ReadString('\n')
	if err != nil && s == "" {
		if err != io.EOF {
			Errf("reading input: %v", err)
		}
		fmt.Println()
		return "", false
	}
	return strings.TrimSpace(s), true
}

// Int reads an optional whole number, returning def on a bare enter. At a
// terminal only digits (and a leading $) can be typed at all.
func Int(prompt string, def int) (int, bool) {
	if !IsInteractive() {
		return intFrom(stdin, prompt, def)
	}
	for {
		in, ok := ask(prompt, func(r rune) bool { return r == '$' || (r >= '0' && r <= '9') })
		if !ok {
			return 0, false
		}
		n, ok, done := parseInt(in, def)
		if done {
			return n, ok
		}
	}
}

func intFrom(r *bufio.Reader, prompt string, def int) (int, bool) {
	for {
		in, ok := lineFrom(r, prompt)
		if !ok {
			return 0, false
		}
		n, ok, done := parseInt(in, def)
		if done {
			return n, ok
		}
	}
}

// parseInt reads the typed answer; done is false when it should be asked
// again.
func parseInt(in string, def int) (n int, ok, done bool) {
	if in == "" {
		return def, true, true
	}
	n, err := strconv.Atoi(strings.TrimPrefix(in, "$"))
	if err != nil || n < 0 {
		Warn("enter a number, or press enter to skip")
		return 0, false, false
	}
	return n, true, true
}

// Yes asks a yes/no question defaulting to yes.
func Yes(prompt string) bool {
	if !IsInteractive() {
		return yesFrom(stdin, prompt)
	}
	in, ok := ask(prompt, nil)
	return ok && isYes(in)
}

func yesFrom(r *bufio.Reader, prompt string) bool {
	in, ok := lineFrom(r, prompt)
	return ok && isYes(in)
}

func isYes(in string) bool {
	switch strings.ToLower(in) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

// Warn prints a recoverable complaint.
func Warn(format string, args ...any) {
	fmt.Printf("  %s %s\n", Yellow("!"), fmt.Sprintf(format, args...))
}
