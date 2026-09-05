package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"fadecli/internal/catalog"
)

// booksyStub answers Booksy's two customer-API calls from canned JSON, and
// records what was asked, so the provider is tested without the network.
type booksyStub struct {
	status    int // when non-zero, every call returns this status
	requests  []string
	slotsBody string
}

const booksyBusiness = `{"id":1130879,"name":"SHEAR 483","service_categories":[
 {"services":[
   {"id":1,"name":"Black Card Member","variants":[{"id":900,"price":null,"duration":30}]},
   {"id":2,"name":"Beard Trim","variants":[{"id":901,"price":25.0,"duration":20}]},
   {"id":3,"name":"Haircut","variants":[{"id":902,"price":45.0,"duration":30}]}
 ]}
]}`

func (s *booksyStub) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	s.requests = append(s.requests, req.Method+" "+req.URL.Path+" "+string(body))
	if s.status != 0 {
		return &http.Response{StatusCode: s.status, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	if req.Header.Get("x-api-key") == "" {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/businesses/1130879/"):
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(booksyBusiness))}, nil
	case req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/appointments/time_slots"):
		out := s.slotsBody
		if out == "" {
			out = `{"time_slots":[{"date":"2026-09-08","slots":[{"t":"10:00","p":""},{"t":"14:30","p":""}]},
			                       {"date":"2026-09-09","slots":[{"t":"09:00","p":""}]}]}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(out))}, nil
	}
	return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

func stubBooksy() (*Booksy, *booksyStub) {
	s := &booksyStub{}
	return &Booksy{Client: &http.Client{Transport: s}}, s
}

var booksyShop = catalog.Shop{ID: "shear-483", Booking: catalog.Booking{Kind: catalog.KindBooksy, ID: "1130879", URL: "https://booksy.com/x"}}

func TestBooksyReadsSlotsForTheDay(t *testing.T) {
	b, s := stubBooksy()
	slots, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2 (only the 8th's, not the 9th's)", len(slots))
	}
	if slots[0].Start.Hour() != 10 || slots[1].Start.Hour() != 14 || slots[1].Start.Minute() != 30 {
		t.Errorf("slot times = %v, %v", slots[0].Start, slots[1].Start)
	}
	if slots[0].Start.Location() != catalog.ShopLocation() {
		t.Error("slots must be in the shop's timezone")
	}
	if slots[0].Service != "Haircut" || slots[0].Price != 45 || slots[0].Duration != 30*time.Minute {
		t.Errorf("slot labelled %q $%d %v, want Haircut $45 30m", slots[0].Service, slots[0].Price, slots[0].Duration)
	}
	// The request must ask for the haircut's variant and "any" staffer.
	var posted string
	for _, r := range s.requests {
		if strings.HasPrefix(r, "POST") {
			posted = r
		}
	}
	var req struct {
		Subbookings []struct {
			ServiceVariantID int `json:"service_variant_id"`
			StafferID        int `json:"staffer_id"`
		} `json:"subbookings"`
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	_ = json.Unmarshal([]byte(posted[strings.Index(posted, "{"):]), &req)
	if len(req.Subbookings) != 1 || req.Subbookings[0].ServiceVariantID != 902 || req.Subbookings[0].StafferID != -1 {
		t.Errorf("posted %+v, want variant 902 with staffer -1", req.Subbookings)
	}
	if req.StartDate != "2026-09-08" || req.EndDate != "2026-09-08" {
		t.Errorf("date window %s..%s, want the single day", req.StartDate, req.EndDate)
	}
}

// A membership row with no price is listed first; a $25 beard trim second.
// The haircut must still win.
func TestBooksyPrefersThePricedHaircut(t *testing.T) {
	b, s := stubBooksy()
	if _, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(s.requests, "\n"), `"service_variant_id":902`) {
		t.Errorf("did not choose the Haircut variant: %v", s.requests)
	}
}

func TestBooksyDayWithNoSlotsIsEmptyNotAnError(t *testing.T) {
	b, _ := stubBooksy()
	slots, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 10))
	if err != nil {
		t.Fatal(err)
	}
	if slots == nil || len(slots) != 0 {
		t.Errorf("got %v, want an empty non-nil slice", slots)
	}
}

func TestBooksyUsesTheWebKeyByDefault(t *testing.T) {
	b, s := stubBooksy()
	if _, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8)); err != nil {
		t.Fatalf("no key configured should still work via the web key: %v", err)
	}
	if len(s.requests) < 2 {
		t.Errorf("expected business + slots calls, got %v", s.requests)
	}
}

func TestBooksyRotatedKeyIsReportedAsChanged(t *testing.T) {
	b, s := stubBooksy()
	s.status = 403
	_, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8))
	if !errors.Is(err, ErrBooksyChanged) {
		t.Errorf("got %v, want ErrBooksyChanged", err)
	}
	// With a user-supplied key, a 403 is about their credentials instead.
	b.APIKey = "user-key"
	_, err = b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8))
	if !errors.Is(err, ErrNeedsCredentials) {
		t.Errorf("got %v, want ErrNeedsCredentials for a user key", err)
	}
}

func TestBooksyWithoutBusinessIDErrors(t *testing.T) {
	b, _ := stubBooksy()
	_, err := b.Availability(context.Background(), catalog.Shop{ID: "x", Booking: catalog.Booking{Kind: catalog.KindBooksy}}, ny(2026, time.September, 8))
	if err == nil {
		t.Error("expected an error for a shop with no business id")
	}
}
