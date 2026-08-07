package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fadecli/internal/catalog"
)

// slowProvider records concurrency so we can prove the fan-out is actually
// parallel rather than a loop that happens to finish.
type slowProvider struct {
	delay    time.Duration
	inFlight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
}

func (*slowProvider) Kind() catalog.BookingKind { return catalog.KindBooksy }

func (p *slowProvider) Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error) {
	n := p.inFlight.Add(1)
	defer p.inFlight.Add(-1)
	for {
		peak := p.peak.Load()
		if n <= peak || p.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	p.calls.Add(1)

	select {
	case <-time.After(p.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []Slot{{Start: day.Add(10 * time.Hour), Service: shop.ID}}, nil
}

func (*slowProvider) Handoff(catalog.Shop) Action { return Action{Type: ActionNone} }

func shops(n int) []catalog.Shop {
	out := make([]catalog.Shop, n)
	for i := range out {
		out[i] = catalog.Shop{
			ID:      fmt.Sprintf("shop-%02d", i),
			Booking: catalog.Booking{Kind: catalog.KindBooksy},
		}
	}
	return out
}

func TestAvailabilityAcrossRunsConcurrently(t *testing.T) {
	p := &slowProvider{delay: 40 * time.Millisecond}
	r := &Registry{providers: map[catalog.BookingKind]Provider{catalog.KindBooksy: p}}

	const n = 8
	start := time.Now()
	got := r.AvailabilityAcross(context.Background(), shops(n), time.Now())
	elapsed := time.Since(start)

	if len(got) != n {
		t.Fatalf("got %d results, want %d", len(got), n)
	}
	// Serial execution would take 8*40ms = 320ms. Allow generous slack for a
	// loaded CI box while still failing a serial implementation.
	if elapsed > 200*time.Millisecond {
		t.Errorf("fan-out took %v, looks serial", elapsed)
	}
	if peak := p.peak.Load(); peak < 2 {
		t.Errorf("peak concurrency %d, expected parallel execution", peak)
	}
}

func TestAvailabilityAcrossBoundsConcurrency(t *testing.T) {
	p := &slowProvider{delay: 10 * time.Millisecond}
	r := &Registry{providers: map[catalog.BookingKind]Provider{catalog.KindBooksy: p}}

	r.AvailabilityAcross(context.Background(), shops(40), time.Now())

	// The cap exists so a wide sweep doesn't trip provider rate limits.
	if peak := p.peak.Load(); peak > 8 {
		t.Errorf("peak concurrency %d exceeds the cap of 8", peak)
	}
}

func TestAvailabilityAcrossPreservesInputOrder(t *testing.T) {
	p := &slowProvider{delay: time.Millisecond}
	r := &Registry{providers: map[catalog.BookingKind]Provider{catalog.KindBooksy: p}}

	in := shops(12)
	got := r.AvailabilityAcross(context.Background(), in, time.Now())
	for i := range in {
		if got[i].Shop.ID != in[i].ID {
			t.Errorf("position %d = %s, want %s", i, got[i].Shop.ID, in[i].ID)
		}
	}
}

func TestAvailabilityAcrossHonoursContextCancel(t *testing.T) {
	p := &slowProvider{delay: 5 * time.Second}
	r := &Registry{providers: map[catalog.BookingKind]Provider{catalog.KindBooksy: p}}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := r.AvailabilityAcross(ctx, shops(4), time.Now())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("cancel took %v to take effect", elapsed)
	}
	for _, g := range got {
		if g.Err == nil {
			t.Errorf("%s returned no error despite cancellation", g.Shop.ID)
		}
	}
}

// errProvider always fails, to check one bad shop doesn't sink the sweep.
type errProvider struct{}

func (errProvider) Kind() catalog.BookingKind { return catalog.KindSquare }
func (errProvider) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, errors.New("boom")
}
func (errProvider) Handoff(catalog.Shop) Action { return Action{Type: ActionNone} }

func TestOneFailingShopDoesNotSinkTheSweep(t *testing.T) {
	r := &Registry{providers: map[catalog.BookingKind]Provider{
		catalog.KindBooksy: &slowProvider{delay: time.Millisecond},
		catalog.KindSquare: errProvider{},
	}}

	in := []catalog.Shop{
		{ID: "good", Booking: catalog.Booking{Kind: catalog.KindBooksy}},
		{ID: "bad", Booking: catalog.Booking{Kind: catalog.KindSquare}},
	}
	got := r.AvailabilityAcross(context.Background(), in, time.Now())

	if !got[0].Live() {
		t.Error("healthy shop returned no slots")
	}
	if got[1].Err == nil {
		t.Error("failing shop reported success")
	}
}

func TestRegistryFallsBackForUnknownKind(t *testing.T) {
	r := NewRegistry(func(string) string { return "" })

	withURL := catalog.Shop{Booking: catalog.Booking{Kind: "yelp", URL: "https://example.com"}}
	if got := r.For(withURL).Handoff(withURL); got.Type != ActionOpen {
		t.Errorf("unknown kind with a URL gave %v, want an open action", got.Type)
	}

	withPhone := catalog.Shop{Phone: "+13475991874", Booking: catalog.Booking{Kind: "yelp"}}
	if got := r.For(withPhone).Handoff(withPhone); got.Type != ActionCall {
		t.Errorf("unknown kind with a phone gave %v, want a call action", got.Type)
	}
}

func TestProvidersWithoutCredentialsReportSo(t *testing.T) {
	r := NewRegistry(func(string) string { return "" })
	day := time.Now()

	for _, tc := range []struct {
		shop catalog.Shop
		want error
	}{
		{catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindBooksy, ID: "215669"}}, ErrNeedsCredentials},
		{catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindSquare, ID: "loc:var"}}, ErrNeedsCredentials},
		{catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindFresha, ID: "slug"}}, ErrNoLiveAvailability},
		{catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindLink, URL: "https://x.test"}}, ErrNoLiveAvailability},
		{catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindPhone}}, ErrNoLiveAvailability},
	} {
		_, err := r.For(tc.shop).Availability(context.Background(), tc.shop, day)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.shop.Booking.Kind, err, tc.want)
		}
	}
}

func TestHandoffNeverPanicsOnEmptyShop(t *testing.T) {
	r := NewRegistry(func(string) string { return "" })
	var wg sync.WaitGroup
	for _, k := range []catalog.BookingKind{
		catalog.KindLink, catalog.KindPhone, catalog.KindBooksy,
		catalog.KindFresha, catalog.KindSquare,
	} {
		wg.Add(1)
		go func(k catalog.BookingKind) {
			defer wg.Done()
			shop := catalog.Shop{Booking: catalog.Booking{Kind: k}}
			if got := r.For(shop).Handoff(shop); got.Type != ActionNone {
				t.Errorf("%s with no contact info gave %v, want none", k, got.Type)
			}
		}(k)
	}
	wg.Wait()
}

func TestPrettyPhone(t *testing.T) {
	cases := map[string]string{
		"+13475991874": "(347) 599-1874",
		"3475991874":   "(347) 599-1874",
		"":             "",
		"555":          "555",
	}
	for in, want := range cases {
		if got := PrettyPhone(in); got != want {
			t.Errorf("PrettyPhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitSquareID(t *testing.T) {
	loc, variation, err := splitSquareID("L123:SV456")
	if err != nil || loc != "L123" || variation != "SV456" {
		t.Errorf("got (%q, %q, %v)", loc, variation, err)
	}
	if _, _, err := splitSquareID("L123"); err == nil {
		t.Error("expected an error for an id without a colon")
	}
}

func TestParseSlotTime(t *testing.T) {
	day := time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)

	got, err := parseSlotTime("14:30", day)
	if err != nil {
		t.Fatalf("parseSlotTime: %v", err)
	}
	if want := time.Date(2026, time.August, 8, 14, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("bare time = %v, want %v", got, want)
	}

	if _, err := parseSlotTime("not-a-time", day); err == nil {
		t.Error("expected an error for unparseable slot time")
	}
}
