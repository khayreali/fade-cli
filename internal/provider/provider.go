// Package provider abstracts the booking systems behind the shops. Every shop
// is bookable by *some* route on day one -- worst case a phone number -- and
// individual shops upgrade to live availability as real API access lands,
// without the CLI surface changing.
package provider

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"fadecli/internal/catalog"
)

var (
	// ErrNoLiveAvailability means the provider can only hand off. This is the
	// normal, expected state for most shops -- it is not a failure.
	ErrNoLiveAvailability = errors.New("no live availability for this shop")
	// ErrNeedsCredentials means live availability exists but this install has
	// not been granted access to it.
	ErrNeedsCredentials = errors.New("provider needs credentials")
)

// Slot is one open appointment.
type Slot struct {
	Start    time.Time
	Duration time.Duration
	Service  string
	Barber   string
	Price    int
	// BookURL deep-links to this specific slot where the provider supports it.
	BookURL string
}

// ActionType is how a user completes a booking outside the terminal.
type ActionType string

const (
	ActionOpen ActionType = "open" // open a URL in the browser
	ActionCall ActionType = "call" // place a phone call
	ActionNone ActionType = "none"
)

// Action is the handoff instruction for a shop.
type Action struct {
	Type   ActionType
	Target string // URL or tel: number
	Label  string // human-readable description of what will happen
}

// Provider talks to one booking backend.
type Provider interface {
	Kind() catalog.BookingKind
	// Availability returns open slots for the given day, or
	// ErrNoLiveAvailability / ErrNeedsCredentials.
	Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error)
	// Handoff is always available and never fails.
	Handoff(shop catalog.Shop) Action
}

// Registry maps booking kinds to providers.
type Registry struct {
	providers map[catalog.BookingKind]Provider
}

// NewRegistry wires up the providers this build supports. Credentials come
// from the environment; a provider with no credentials still hands off.
func NewRegistry(env func(string) string) *Registry {
	r := &Registry{providers: map[catalog.BookingKind]Provider{}}
	for _, p := range []Provider{
		Link{},
		Phone{},
		&Booksy{APIKey: env("FADE_BOOKSY_API_KEY"), Client: defaultClient()},
		&Fresha{Client: defaultClient()},
		&Vagaro{Client: defaultClient()},
		&Squire{Client: defaultClient()},
		&Square{Token: env("FADE_SQUARE_TOKEN"), Client: defaultClient()},
	} {
		r.providers[p.Kind()] = p
	}
	return r
}

// For returns the provider for a shop, falling back to Link so an unknown or
// misspelled kind degrades to "open the website" instead of erroring out.
func (r *Registry) For(shop catalog.Shop) Provider {
	if p, ok := r.providers[shop.Booking.Kind]; ok {
		return p
	}
	if shop.Booking.URL != "" {
		return Link{}
	}
	return Phone{}
}

// ShopSlots is one shop's availability outcome.
type ShopSlots struct {
	Shop  catalog.Shop
	Slots []Slot
	Err   error
}

// Live reports whether this shop returned real availability.
func (s ShopSlots) Live() bool { return s.Err == nil && len(s.Slots) > 0 }

// AvailabilityAcross queries every shop concurrently and returns results in the
// input order. This is the whole point of the design: checking twenty shops
// across four booking platforms should cost one round trip, not twenty.
func (r *Registry) AvailabilityAcross(ctx context.Context, shops []catalog.Shop, day time.Time) []ShopSlots {
	out := make([]ShopSlots, len(shops))
	// Bound concurrency so a wide search doesn't open 200 sockets at once and
	// trip a provider's rate limiter.
	sem := make(chan struct{}, 8)

	var wg sync.WaitGroup
	for i, shop := range shops {
		wg.Add(1)
		go func(i int, shop catalog.Shop) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			slots, err := r.For(shop).Availability(ctx, shop, day)
			sort.Slice(slots, func(a, b int) bool { return slots[a].Start.Before(slots[b].Start) })
			out[i] = ShopSlots{Shop: shop, Slots: slots, Err: err}
		}(i, shop)
	}
	wg.Wait()
	return out
}
