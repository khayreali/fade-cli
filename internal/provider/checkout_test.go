package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"fadecli/internal/catalog"
)

type checkoutTransport func(*http.Request) (*http.Response, error)

func (f checkoutTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestFreshaTimeSelectedRejectsUnselectedOrChangedDates(t *testing.T) {
	start := time.Date(2026, 9, 15, 11, 0, 0, 0, catalog.ShopLocation())
	for _, tc := range []struct {
		date, label    string
		selected, want bool
	}{
		{"2026-09-15", "11:00 AM", true, true},
		{"2026-09-15", "11:00 AM", false, false},
		{"2026-09-16", "11:00 AM", true, false},
		{"2026-09-15", "12:00 PM", true, false},
		{"2026-09-15", "unknown", true, false},
	} {
		screen := map[string]any{"screen": map[string]any{"__typename": "BookingFlowScreenTime"}, "screenTime": map[string]any{
			"dates": []any{map[string]any{"date": map[string]any{"iso": tc.date}, "isSelected": true}},
			"day":   map[string]any{"timeslots": []any{map[string]any{"time": tc.label, "isSelected": tc.selected}}},
		}}
		if got := freshaTimeSelected(screen, start); got != tc.want {
			t.Fatalf("%+v: %v", tc, got)
		}
	}
}

func TestFreshaCheckoutUsesOnlyTheReviewedCartAndTime(t *testing.T) {
	start := catalog.InShopTime(time.Now()).AddDate(0, 0, 1).Truncate(time.Minute)
	action, _ := json.Marshal([]any{map[string]any{"type": "onScreenTimeSet", "date": start.Format("2006-01-02"), "time": start.Hour()*3600 + start.Minute()*60}, "L"})
	slot := Slot{Start: start, freshaCartID: "private-cart", freshaAction: string(action)}
	count := 0
	f := Fresha{Client: &http.Client{Transport: checkoutTransport(func(r *http.Request) (*http.Response, error) {
		count++
		var request struct {
			Variables struct {
				ID   string `json:"id"`
				Cart string `json:"cartId"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Variables.ID != string(action) || request.Variables.Cart != "private-cart" {
			t.Fatalf("unexpected action: %+v", request)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":{"bookingFlowActionButtonPressed":{"screen":{"__typename":"BookingFlowScreenTime"},"screenTime":{"dates":[{"isSelected":true,"date":{"iso":"` + start.Format("2006-01-02") + `"}}],"day":{"timeslots":[{"isSelected":true,"time":"` + start.Format("15:04") + `"}]}}}}}`))}, nil
	})}}
	target, err := f.PrepareCheckout(context.Background(), freshaShop, slot)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(target)
	if u.Host != "www.fresha.com" || u.Path != "/a/otis-slug/booking" || u.Query().Get("cartId") != "private-cart" || count != 1 {
		t.Fatal(target, count)
	}
	b, _ := json.Marshal(slot)
	if strings.Contains(string(b), "private-cart") || strings.Contains(string(b), "onScreenTimeSet") {
		t.Fatal("cart tokens leaked into serialized slots")
	}
	for _, bad := range []Slot{{Start: start}, {Start: time.Now().Add(-time.Hour), freshaCartID: slot.freshaCartID, freshaAction: slot.freshaAction}, {Start: start.Add(time.Hour), freshaCartID: slot.freshaCartID, freshaAction: slot.freshaAction}} {
		if _, err := f.PrepareCheckout(context.Background(), freshaShop, bad); err == nil {
			t.Fatal("invalid checkout accepted")
		}
	}
	if count != 1 {
		t.Fatal("invalid checkout touched network")
	}
}

// Explicit opt-in only: prepares an anonymous cart, never submits a booking
// or payment. Prints its short-lived resume URL for manual browser verification.
func TestFreshaCheckoutLive(t *testing.T) {
	if os.Getenv("FADE_TEST_FRESHA_CHECKOUT") != "1" {
		t.Skip("opt-in live cart handoff")
	}
	day, err := time.ParseInLocation("2006-01-02", os.Getenv("FADE_TEST_FRESHA_DAY"), catalog.ShopLocation())
	if err != nil {
		t.Fatal("set FADE_TEST_FRESHA_DAY to a future open date")
	}
	shop := catalog.Shop{Name: "Otis & Finn Williamsburg", Booking: catalog.Booking{Kind: catalog.KindFresha, ID: "otis-finn-williamsburg-new-york-154-grand-street-hwi5s0hz"}}
	f := &Fresha{}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	menu, err := f.Services(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	service, err := ResolveService(menu, "Haircut")
	if err != nil {
		t.Fatal(err)
	}
	slots, err := f.AvailabilityFor(ctx, shop, day, service)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("no openings for test date")
	}
	target, err := f.PrepareCheckout(ctx, shop, slots[0])
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Prepared %s at %s, %s (NOT BOOKED). Resume: %s", service.Name, slots[0].Start, slots[0].PriceLabel, target)
}
