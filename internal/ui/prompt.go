package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// stdin is shared across every prompt. A fresh bufio.Reader per call would
// discard anything already buffered, which silently eats fast typing.
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

// Line prompts and reads one trimmed line. ok is false at EOF, which is how
// ctrl-D exits any prompt.
//
// These prompts are line-based and need cooked mode, so inside a raw-mode
// session they must run within Raw.Suspend.
func Line(prompt string) (text string, ok bool) { return lineFrom(stdin, prompt) }

func lineFrom(r *bufio.Reader, prompt string) (text string, ok bool) {
	fmt.Print(Dim("  › ") + prompt)
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

// Int reads an optional whole number, returning def on a bare enter.
func Int(prompt string, def int) (int, bool) { return intFrom(stdin, prompt, def) }

func intFrom(r *bufio.Reader, prompt string, def int) (int, bool) {
	for {
		in, ok := lineFrom(r, prompt)
		if !ok {
			return 0, false
		}
		if in == "" {
			return def, true
		}
		n, err := strconv.Atoi(strings.TrimPrefix(in, "$"))
		if err != nil || n < 0 {
			Warn("enter a number, or press enter to skip")
			continue
		}
		return n, true
	}
}

// Yes asks a yes/no question defaulting to yes.
func Yes(prompt string) bool { return yesFrom(stdin, prompt) }

func yesFrom(r *bufio.Reader, prompt string) bool {
	in, ok := lineFrom(r, prompt)
	if !ok {
		return false
	}
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
