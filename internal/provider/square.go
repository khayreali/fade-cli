package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"fadecli/internal/catalog"
)

const squareBase = "https://connect.squareup.com/v2"

// squareAPIVersion pins the Square API contract. Square dates its versions and
// keeps old ones working, so pinning here beats drifting with their default.
const squareAPIVersion = "2025-01-23"

// Square reads availability from Square Appointments.
//
// This is the one genuinely open path: Square documents a Bookings API, and a
// shop can authorize this app via OAuth. The catch is that availability search
// requires the shop's service-variation id, not just a location id -- Square
// will not quote you open time without knowing which service you want. Shops
// therefore carry `booking.id` as "<location_id>:<service_variation_id>".
type Square struct {
	Token  string
	Client *http.Client
}

func (*Square) Kind() catalog.BookingKind { return catalog.KindSquare }

func (s *Square) Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error) {
	// A missing token and a missing location/variation pair are both "this
	// shop isn't set up yet", not a failure -- the id is per-shop credential
	// data the merchant has to grant, so reporting it as an error would make
	// an unconfigured shop look broken.
	if s.Token == "" || shop.Booking.ID == "" {
		return nil, ErrNeedsCredentials
	}
	locationID, variationID, err := splitSquareID(shop.Booking.ID)
	if err != nil {
		return nil, err
	}

	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	// Square rejects a start time in the past, so clamp to now for today.
	if now := time.Now(); from.Before(now) {
		from = now.Add(time.Minute)
	}

	body, err := json.Marshal(map[string]any{
		"query": map[string]any{
			"filter": map[string]any{
				"start_at_range": map[string]string{
					"start_at": from.Format(time.RFC3339),
					"end_at":   from.AddDate(0, 0, 1).Format(time.RFC3339),
				},
				"location_id": locationID,
				"segment_filters": []map[string]any{
					{"service_variation_id": variationID},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, squareBase+"/bookings/availability/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.Token)
	req.Header.Set("Square-Version", squareAPIVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("square: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, ErrNeedsCredentials
	default:
		return nil, fmt.Errorf("square: unexpected status %d", resp.StatusCode)
	}

	var payload struct {
		Availabilities []struct {
			StartAt         string `json:"start_at"`
			LocationID      string `json:"location_id"`
			AppointmentSegs []struct {
				DurationMinutes  int    `json:"duration_minutes"`
				TeamMemberID     string `json:"team_member_id"`
				ServiceVariation string `json:"service_variation_id"`
			} `json:"appointment_segments"`
		} `json:"availabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("square: decoding response: %w", err)
	}

	slots := make([]Slot, 0, len(payload.Availabilities))
	for _, a := range payload.Availabilities {
		start, err := time.Parse(time.RFC3339, a.StartAt)
		if err != nil {
			continue
		}
		slot := Slot{Start: start, BookURL: shop.Booking.URL}
		if len(a.AppointmentSegs) > 0 {
			slot.Duration = time.Duration(a.AppointmentSegs[0].DurationMinutes) * time.Minute
			slot.Barber = a.AppointmentSegs[0].TeamMemberID
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func (*Square) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open Square booking page"}
	}
	return Action{Type: ActionNone, Label: "no Square link on file"}
}

func splitSquareID(id string) (location, variation string, err error) {
	for i := 0; i < len(id); i++ {
		if id[i] == ':' {
			return id[:i], id[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("square: booking.id must be \"<location_id>:<service_variation_id>\", got %q", id)
}
