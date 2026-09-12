package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
	return defaultAvailability(ctx, b, shop, day)
}

func (b *Booksy) AvailabilityFor(ctx context.Context, shop catalog.Shop, day time.Time, svc Service) ([]Slot, error) {
	id, err := strconv.Atoi(svc.ID)
	if err != nil || id <= 0 || shop.Booking.ID == "" {
		return nil, ErrServiceUnavailable
	}

	loc := catalog.ShopLocation()
	day = day.In(loc)
	date := day.Format("2006-01-02")
	body, _ := json.Marshal(map[string]any{
		"subbookings": []map[string]any{{"service_variant_id": id, "staffer_id": -1}},
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
	if payload.TimeSlots == nil {
		return nil, ErrBooksyChanged
	}

	slots := []Slot{}
	for _, d := range payload.TimeSlots {
		if d.Date != date {
			continue
		}
		for _, s := range d.Slots {
			hh, mm, ok := splitClock(s.T)
			if !ok {
				return nil, ErrBooksyChanged
			}
			slots = append(slots, svc.slot(time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc), shop.Booking.URL))
		}
	}
	return slots, nil
}

// Services flattens every variant, including different durations/prices of
// the same service. Reading this once lets a date change cost just one request.
func (b *Booksy) Services(ctx context.Context, shop catalog.Shop) ([]Service, error) {
	if shop.Booking.ID == "" {
		return nil, fmt.Errorf("booksy: shop %s has no business id", shop.ID)
	}
	type business struct {
		Categories []struct {
			Services []struct {
				Name     string `json:"name"`
				Variants []struct {
					ID       int      `json:"id"`
					Name     string   `json:"name"`
					Price    *float64 `json:"price"`
					Duration int      `json:"duration"`
				} `json:"variants"`
			} `json:"services"`
		} `json:"service_categories"`
	}
	var raw struct {
		business
		Business *business `json:"business"`
	}
	if err := b.do(ctx, http.MethodGet, "/businesses/"+shop.Booking.ID+"/?with_combos=1&with_markdown=1", nil, &raw); err != nil {
		return nil, err
	}
	biz := raw.business
	if raw.Business != nil {
		biz = *raw.Business
	}
	var services []Service
	seen := map[int]bool{}
	for _, c := range biz.Categories {
		for _, s := range c.Services {
			for _, v := range s.Variants {
				if v.ID <= 0 || seen[v.ID] || strings.TrimSpace(s.Name) == "" {
					continue
				}
				seen[v.ID] = true
				svc := Service{ID: strconv.Itoa(v.ID), Name: strings.TrimSpace(s.Name), Duration: time.Duration(v.Duration) * time.Minute}
				if v.Name != "" && v.Name != s.Name {
					svc.Name += " / " + v.Name
				}
				if v.Price != nil {
					svc.Price = int(*v.Price)
					svc.PriceLabel = "$" + strconv.FormatFloat(*v.Price, 'f', -1, 64)
				}
				services = append(services, svc)
			}
		}
	}
	if len(services) == 0 {
		return nil, ErrBooksyChanged
	}
	return services, nil
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
