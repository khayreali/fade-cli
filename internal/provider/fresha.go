package provider

import (
	"context"
	"net/http"
	"time"

	"fadecli/internal/catalog"
)

// Fresha covers Fresha-hosted shops.
//
// Fresha publishes no third-party availability API -- their integrations are
// merchant-side (POS, payments, calendar sync), with nothing that exposes a
// venue's open slots to an outside client. So this provider is handoff-only by
// design rather than by omission, and deep-links straight to the venue's
// booking page. If that changes, only Availability below needs to grow.
type Fresha struct {
	Client *http.Client
}

func (*Fresha) Kind() catalog.BookingKind { return catalog.KindFresha }

func (*Fresha) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, ErrNoLiveAvailability
}

func (*Fresha) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open on Fresha"}
	}
	if shop.Booking.ID != "" {
		return Action{
			Type:   ActionOpen,
			Target: "https://www.fresha.com/a/" + shop.Booking.ID,
			Label:  "open on Fresha",
		}
	}
	return Action{Type: ActionNone, Label: "no Fresha link on file"}
}
