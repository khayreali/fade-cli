package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var ErrInterrupted = errors.New("interrupted")

// Wait keeps navigation responsive during a request. It shares NextEvent's
// reader, including any pending key, so finishing a request cannot steal the
// next screen's first keypress. Escape cancels; ctrl-C quits.
func (t *Raw) Wait(ctx context.Context, title string, work func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- work(ctx) }()
	t.startWatching()
	hideCursor()
	defer showCursor()
	draw := func() {
		cols, _ := Size()
		lines := Box("Please wait", []string{Subtle("Working... you can cancel at any time."), Accent("esc") + " cancel   " + Accent("ctrl-C") + " quit"}, min(cols-4, 62))
		fmt.Print("\033[H\033[2J\r\n  " + Title(truncate(title, cols-4)) + "\r\n\r\n  " + strings.Join(lines, "\r\n  ") + "\r\n")
	}
	draw()
	for {
		if !t.pending {
			t.pending = true
			t.reqs <- struct{}{}
		}
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-t.resized:
			draw()
		case k := <-t.keys:
			t.pending = false
			switch k.Type {
			case KeyInterrupt:
				return ErrInterrupted
			case KeyEsc, KeyLeft:
				return context.Canceled
			}
		}
	}
}
