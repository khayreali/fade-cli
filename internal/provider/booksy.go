package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"fadecli/internal/catalog"
)

func defaultClient() *http.Client {
	return &http.Client{Timeout: 8 * time.Second}
}

// Booksy reads live availability from Booksy-hosted shops.
//
// Booksy runs no open partner program, but its own website is a client of a
// public customer API: the booking widget fetches a business's services and
// then posts for open time slots, authenticating with a web client key that
// ships in the site's JavaScript. This provider speaks that same API -- two
// requests, no login. If the key or the endpoints rotate, every failure
// degrades to the handoff the CLI always had.
type Booksy struct {
	// APIKey overrides the built-in web client key (FADE_BOOKSY_API_KEY).
	APIKey string
	Base   string
	Client *http.Client
}

const (
	booksyBase = "https://us.booksy.com/core/v2/customer_api"
	// booksyWebKey is the public client key Booksy's marketplace site sends
	// on every request. It is not a secret -- it is in the page source -- and
	// it is what makes the site's own availability calls work anonymously.
	booksyWebKey = "web-e3d812bf-d7a2-445d-ab38-55589ae6a121"
	booksyUA     = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
)

// ErrBooksyChanged is returned when Booksy's customer API no longer accepts
// the web key or answers in the shape this code expects.
var ErrBooksyChanged = errors.New("booksy's customer API changed; live times unavailable until this is updated")

func (*Booksy) Kind() catalog.BookingKind { return catalog.KindBooksy }

func (b *Booksy) base() string {
	if b.Base != "" {
		return b.Base
	}
	return booksyBase
}

func (b *Booksy) key() string {
	if b.APIKey != "" {
		return b.APIKey
	}
	return booksyWebKey
}

func (b *Booksy) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return defaultClient()
}

func (b *Booksy) Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error) {
	if shop.Booking.ID == "" {
		return nil, fmt.Errorf("booksy: shop %s has no business id", shop.ID)
	}
	svc, err := b.haircutVariant(ctx, shop.Booking.ID)
	if err != nil {
		return nil, err
	}

	loc := catalog.ShopLocation()
	day = day.In(loc)
	date := day.Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{
		"subbookings": []map[string]any{{"service_variant_id": svc.variantID, "staffer_id": -1}},
		"start_date":  date,
		"end_date":    date,
	})
	var payload struct {
		TimeSlots []struct {
			Date  string `json:"date"`
			Slots []struct {
				T string `json:"t"`
			} `json:"slots"`
		} `json:"time_slots"`
	}
	if err := b.do(ctx, http.MethodPost, fmt.Sprintf("/me/businesses/%s/appointments/time_slots", shop.Booking.ID), body, &payload); err != nil {
		return nil, err
	}

	slots := []Slot{}
	for _, d := range payload.TimeSlots {
		if d.Date != date {
			continue
		}
		for _, s := range d.Slots {
			hh, mm, ok := splitClock(s.T)
			if !ok {
				continue
			}
			slots = append(slots, Slot{
				Start:    time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc),
				Duration: svc.duration,
				Service:  svc.name,
				Barber:   "any",
				Price:    svc.price,
				BookURL:  shop.Booking.URL,
			})
		}
	}
	return slots, nil
}

type booksyVariant struct {
	variantID int
	name      string
	price     int
	duration  time.Duration
}

// haircutVariant fetches the business and picks the service to price
// availability against: the first whose name looks like a haircut, else the
// first bookable service. Availability depends on the service's duration, so
// the choice matters.
func (b *Booksy) haircutVariant(ctx context.Context, businessID string) (*booksyVariant, error) {
	var raw map[string]any
	if err := b.do(ctx, http.MethodGet, "/businesses/"+businessID+"/?with_combos=1&with_markdown=1", nil, &raw); err != nil {
		return nil, err
	}
	// The business may arrive bare or wrapped under "business".
	biz := raw
	if inner, ok := raw["business"].(map[string]any); ok {
		biz = inner
	}
	var first, pick *booksyVariant
	cats, _ := biz["service_categories"].([]any)
	for _, c := range cats {
		cm, _ := c.(map[string]any)
		services, _ := cm["services"].([]any)
		for _, s := range services {
			sm, _ := s.(map[string]any)
			name, _ := sm["name"].(string)
			variants, _ := sm["variants"].([]any)
			if len(variants) == 0 {
				continue
			}
			vm, _ := variants[0].(map[string]any)
			id, _ := vm["id"].(float64)
			if id == 0 {
				continue
			}
			v := &booksyVariant{variantID: int(id), name: strings.TrimSpace(name)}
			if p, _ := vm["price"].(float64); p > 0 {
				v.price = int(p)
			}
			if d, _ := vm["duration"].(float64); d > 0 {
				v.duration = time.Duration(d) * time.Minute
			}
			if first == nil {
				first = v
			}
			if pick == nil && haircutLike.MatchString(name) && v.price > 0 {
				pick = v
			}
		}
	}
	if pick == nil {
		pick = first
	}
	if pick == nil {
		return nil, ErrBooksyChanged
	}
	return pick, nil
}

// do performs one customer-API call and decodes the JSON into out.
func (b *Booksy) do(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, b.base()+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", b.key())
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", booksyUA)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return fmt.Errorf("booksy: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
	case http.StatusUnauthorized, http.StatusForbidden:
		// The built-in key is Booksy's own web client key; if it stops
		// working, Booksy rotated it -- that is not the user's credentials.
		if b.APIKey != "" {
			return ErrNeedsCredentials
		}
		return ErrBooksyChanged
	case http.StatusNotFound:
		return ErrBooksyChanged
	default:
		return fmt.Errorf("booksy: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("booksy: %w", err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("booksy: decoding response: %w", err)
	}
	return nil
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
