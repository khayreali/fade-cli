package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"fadecli/internal/catalog"
)

func defaultClient() *http.Client {
	return &http.Client{Timeout: 8 * time.Second}
}

// booksyBase is the documented public-api host. Booksy issues X-API-Key
// credentials per business rather than running an open developer program, so
// this path only works for shops that have granted you access. Override with
// FADE_BOOKSY_BASE if your key is scoped to a different region host.
const booksyBase = "https://us.booksy.com/public-api/us"

// Booksy reads availability from Booksy-hosted shops.
//
// Scope note: there is no open third-party Booksy API. Without a per-business
// key this provider reports ErrNeedsCredentials and the shop falls back to a
// deep link, which is the correct behaviour for the ~20 Booksy shops in the
// seed catalog today.
type Booksy struct {
	APIKey string
	Base   string
	Client *http.Client
}

func (*Booksy) Kind() catalog.BookingKind { return catalog.KindBooksy }

func (b *Booksy) base() string {
	if b.Base != "" {
		return b.Base
	}
	return booksyBase
}

func (b *Booksy) Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error) {
	if b.APIKey == "" {
		return nil, ErrNeedsCredentials
	}
	if shop.Booking.ID == "" {
		return nil, fmt.Errorf("booksy: shop %s has no business id", shop.ID)
	}

	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	till := from.AddDate(0, 0, 1)

	q := url.Values{}
	q.Set("booked_from", from.Format(time.RFC3339))
	q.Set("booked_till", till.Format(time.RFC3339))

	endpoint := fmt.Sprintf("%s/business/%s/appointments/?%s", b.base(), shop.Booking.ID, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", b.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := b.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("booksy: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrNeedsCredentials
	default:
		return nil, fmt.Errorf("booksy: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		TimeSlots []struct {
			Time     string  `json:"time"`
			Service  string  `json:"service_name"`
			Staff    string  `json:"staffer_name"`
			Price    float64 `json:"price"`
			Duration int     `json:"duration"`
		} `json:"time_slots"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("booksy: decoding response: %w", err)
	}

	slots := make([]Slot, 0, len(payload.TimeSlots))
	for _, ts := range payload.TimeSlots {
		start, err := parseSlotTime(ts.Time, day)
		if err != nil {
			continue // a slot we can't place in time is worse than no slot
		}
		slots = append(slots, Slot{
			Start:    start,
			Duration: time.Duration(ts.Duration) * time.Minute,
			Service:  ts.Service,
			Barber:   ts.Staff,
			Price:    int(ts.Price),
			BookURL:  shop.Booking.URL,
		})
	}
	return slots, nil
}

func (*Booksy) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open on Booksy"}
	}
	return Action{Type: ActionNone, Label: "no Booksy link on file"}
}

// parseSlotTime accepts both a full timestamp and a bare "HH:MM", which is
// what the slot endpoints return when the date is implied by the request.
func parseSlotTime(s string, day time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("15:04", s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), 0, 0, day.Location()), nil
}
