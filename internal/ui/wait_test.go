package ui

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Inject just the event channels, avoiding real terminal reads in unit tests.
func waitingRaw() *Raw {
	r := &Raw{reqs: make(chan struct{}, 8), keys: make(chan Key, 8), resized: make(chan struct{}, 1)}
	r.watch.Do(func() {})
	return r
}

func TestWaitEscapeCancelsTheRequest(t *testing.T) {
	r := waitingRaw()
	r.keys <- Key{Type: KeyEsc}
	stopped := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := r.Wait(ctx, "test", func(ctx context.Context) error { defer close(stopped); <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("escape: %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("request wasn't canceled")
	}
	if r.pending {
		t.Fatal("canceled key is still pending")
	}
}

func TestWaitLeavesPendingReadForNextScreen(t *testing.T) {
	r := waitingRaw()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Wait(ctx, "test", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !r.pending {
		t.Fatal("outstanding read was forgotten")
	}
	r.keys <- Key{Type: KeyEnter}
	k, resized := r.NextEvent()
	if resized || k.Type != KeyEnter || r.pending {
		t.Fatalf("next key lost: %+v", k)
	}
	if len(r.reqs) != 1 {
		t.Fatalf("two readers requested: %d", len(r.reqs))
	}
}

func TestWaitInterruptIsDistinctFromGoingBack(t *testing.T) {
	r := waitingRaw()
	r.keys <- Key{Type: KeyInterrupt}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := r.Wait(ctx, "test", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, ErrInterrupted) {
		t.Fatalf("ctrl-C: %v", err)
	}
}
