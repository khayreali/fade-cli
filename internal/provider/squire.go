package provider

import (
	"context"
	"net/http"
	"time"

	"fadecli/internal/catalog"
)

// Squire covers shops on SQUIRE, a booking platform built specifically for
// barbershops and common among the higher-end ones in Brooklyn.
//
// Like Fresha and Vagaro its API is merchant-facing -- a shop uses it to run
// its own chair, not to expose open slots to third parties -- so this is a
// handoff to the shop's Squire page.
type Squire struct {
	Client *http.Client
}

func (*Squire) Kind() catalog.BookingKind { return catalog.KindSquire }

func (*Squire) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, ErrNoLiveAvailability
}

func (*Squire) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open on Squire"}
	}
	return Action{Type: ActionNone, Label: "no Squire link on file"}
}
