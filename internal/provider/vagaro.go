package provider

import (
	"context"
	"net/http"
	"time"

	"fadecli/internal/catalog"
)

// Vagaro covers Vagaro-hosted shops.
//
// Like Fresha, Vagaro's API program is merchant-facing -- it exists so a shop
// can sync its own calendar, not so a third party can read open slots across
// venues. Handoff-only by design, not by omission.
type Vagaro struct {
	Client *http.Client
}

func (*Vagaro) Kind() catalog.BookingKind { return catalog.KindVagaro }

func (*Vagaro) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, ErrNoLiveAvailability
}

func (*Vagaro) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open booking page"}
	}
	return Action{Type: ActionNone, Label: "no booking link on file"}
}
