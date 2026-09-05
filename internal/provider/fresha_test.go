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

// freshaStub plays Fresha's booking state machine from canned screens, keyed
// by the operation and the action type being pressed. It records every action
// echoed back, so tests can assert the flow chose the right buttons. No test
// here touches the network.
type freshaStub struct {
	pressed  []string
	failWith string // when set, every call returns this GraphQL error message
}

const loc = `"L"` // the location id that terminates every action token

func act(typ, extra string) string {
	if extra != "" {
		extra = "," + extra
	}
	return `{"id":"[{\"type\":\"` + typ + `\"` + strings.ReplaceAll(extra, `"`, `\"`) + `},` + strings.ReplaceAll(loc, `"`, `\"`) + `]"}`
}

// Beard Trim is listed FIRST so a naive "take the first service" would pick
// it; the flow must find the haircut.
var stubServices = `"screenServices":{"categories":[{"items":[
  {"name":"Beard Trim ","caption":"20 mins","price":{"formatted":"US$35"},"primaryAction":` + act("onScreenServicesServiceVariantAdd", `"catalogId":"s:1"`) + `},
  {"name":"Haircut ","caption":"30 mins","price":{"formatted":"US$55"},"primaryAction":` + act("onScreenServicesModalServiceOpen", `"catalogId":"s:2"`) + `}
]}],"continueButton":{"action":` + act("onScreenServicesContinue", "") + `}}`

var stubEmployee = `"screen":{"__typename":"BookingFlowScreenEmployee"},"screenEmployee":{"employees":[{"action":` +
	act("onScreenEmployeeSetAny", "") + `}],"continueButton":{"action":` + act("onScreenEmployeeContinue", "") + `}}`

func stubTime(slots ...string) string {
	quoted := make([]string, len(slots))
	for i, s := range slots {
		quoted[i] = `{"time":"` + s + `"}`
	}
	return `"screen":{"__typename":"BookingFlowScreenTime"},"screenTime":{"dates":[
  {"date":{"iso":"2026-09-05T00:00:00.000Z"},"isSelected":true,"isAvailableToBeBooked":true},
  {"date":{"iso":"2026-09-06T00:00:00.000Z"},"isSelected":false,"isAvailableToBeBooked":false,"action":` + act("onScreenTimeDaySelectorDateSet", `"date":"2026-09-06"`) + `},
  {"date":{"iso":"2026-09-08T00:00:00.000Z"},"isSelected":false,"isAvailableToBeBooked":true,"action":` + act("onScreenTimeDaySelectorDateSet", `"date":"2026-09-08"`) + `}
],"day":{"timeslots":[` + strings.Join(quoted, ",") + `]}}`
}

func (s *freshaStub) RoundTrip(req *http.Request) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	var call struct {
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	_ = json.Unmarshal(body, &call)

	reply := func(field, inner string) *http.Response {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{"data":{"` + field + `":{` + inner + `}}}`))}
	}
	if s.failWith != "" {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"` + s.failWith + `"}]}`))}, nil
	}

	switch call.OperationName {
	case "BookingFlow_Initialize_Mutation":
		return reply("bookingFlowInitialize", `"cartId":"cart-1","screen":{"__typename":"BookingFlowScreenServices"},`+stubServices), nil
	case "BookingFlow_ActionButtonPressed_Mutation":
		id, _ := call.Variables["id"].(string)
		s.pressed = append(s.pressed, id)
		if cart, _ := call.Variables["cartId"].(string); cart != "cart-1" {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"unknown cart"}]}`))}, nil
		}
		f := "bookingFlowActionButtonPressed"
		switch {
		case strings.Contains(id, "ModalServiceOpen"):
			return reply(f, `"screen":{"__typename":"BookingFlowScreenServices"},"modal":{"addAction":`+act("ModalService.onAdd", "")+`},`+stubServices), nil
		case strings.Contains(id, "ModalService.onAdd"), strings.Contains(id, "ServiceVariantAdd"):
			return reply(f, `"screen":{"__typename":"BookingFlowScreenServices"},`+stubServices), nil
		case strings.Contains(id, "onScreenServicesContinue"), strings.Contains(id, "onScreenEmployeeSetAny"):
			return reply(f, stubEmployee), nil
		case strings.Contains(id, "onScreenEmployeeContinue"):
			return reply(f, stubTime("11:00", "14:30")), nil
		case strings.Contains(id, "DaySelectorDateSet") && strings.Contains(id, "2026-09-08"):
			return reply(f, stubTime("09:00")), nil
		}
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errors":[{"message":"stub: unexpected ` + call.OperationName + `"}]}`))}, nil
}

func stubFresha() (*Fresha, *freshaStub) {
	s := &freshaStub{}
	return &Fresha{Client: &http.Client{Transport: s}}, s
}

var freshaShop = catalog.Shop{ID: "otis", Name: "Otis & Finn", Booking: catalog.Booking{Kind: catalog.KindFresha, ID: "otis-slug"}}

func ny(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 10, 0, 0, 0, catalog.ShopLocation())
}

func TestFreshaReadsSlotsForTheSelectedDay(t *testing.T) {
	f, _ := stubFresha()
	slots, err := f.Availability(context.Background(), freshaShop, ny(2026, time.September, 5))
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d slots, want 2", len(slots))
	}
	got := slots[0]
	if got.Start.Hour() != 11 || got.Start.Minute() != 0 || got.Start.Location() != catalog.ShopLocation() {
		t.Errorf("first slot = %v, want 11:00 in the shop's zone", got.Start)
	}
	if got.Service != "Haircut" || got.Price != 55 || got.Duration != 30*time.Minute {
		t.Errorf("slot labelled %q $%d %v, want Haircut $55 30m", got.Service, got.Price, got.Duration)
	}
	if got.Barber != "any" || !strings.Contains(got.BookURL, "otis-slug") {
		t.Errorf("slot barber/url = %q %q", got.Barber, got.BookURL)
	}
}

// Availability depends on the service's duration, so the flow must add the
// haircut even when another service is listed first.
func TestFreshaPrefersTheHaircut(t *testing.T) {
	f, s := stubFresha()
	if _, err := f.Availability(context.Background(), freshaShop, ny(2026, time.September, 5)); err != nil {
		t.Fatal(err)
	}
	// The id travels as a JSON string; by the time the stub sees it the
	// escaping is gone, so match the plain form.
	if len(s.pressed) == 0 || !strings.Contains(s.pressed[0], `"catalogId":"s:2"`) {
		t.Errorf("first action pressed was %q, want the Haircut's modal-open (s:2)", s.pressed)
	}
	if len(s.pressed) < 2 || !strings.Contains(s.pressed[1], "ModalService.onAdd") {
		t.Errorf("second action was %q, want the modal's Add", s.pressed)
	}
}

// A day the shop doesn't trade is an answer -- no slots -- not a failure.
func TestFreshaClosedDayIsEmptyNotAnError(t *testing.T) {
	f, s := stubFresha()
	slots, err := f.Availability(context.Background(), freshaShop, ny(2026, time.September, 6))
	if err != nil {
		t.Fatalf("closed day returned error: %v", err)
	}
	if slots == nil || len(slots) != 0 {
		t.Errorf("closed day gave %v, want an empty (non-nil) slice", slots)
	}
	for _, p := range s.pressed {
		if strings.Contains(p, "DaySelectorDateSet") {
			t.Error("should not select a day the venue marks unbookable")
		}
	}
}

func TestFreshaSelectsAnotherDay(t *testing.T) {
	f, s := stubFresha()
	slots, err := f.Availability(context.Background(), freshaShop, ny(2026, time.September, 8))
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || slots[0].Start.Hour() != 9 || slots[0].Start.Day() != 8 {
		t.Errorf("got %v, want one 09:00 slot on the 8th", slots)
	}
	found := false
	for _, p := range s.pressed {
		if strings.Contains(p, "DaySelectorDateSet") && strings.Contains(p, "2026-09-08") {
			found = true
		}
	}
	if !found {
		t.Error("the 8th was never selected on the time screen")
	}
}

func TestFreshaOutsideBookingWindowErrors(t *testing.T) {
	f, _ := stubFresha()
	_, err := f.Availability(context.Background(), freshaShop, ny(2026, time.October, 30))
	if err == nil || !strings.Contains(err.Error(), "booking window") {
		t.Errorf("got %v, want a booking-window error", err)
	}
}

// A rotated persisted-query hash must degrade to the handoff, loudly typed.
func TestFreshaFlowChangedIsReported(t *testing.T) {
	f, s := stubFresha()
	s.failWith = "PersistedQueryKeyNotFound"
	_, err := f.Availability(context.Background(), freshaShop, ny(2026, time.September, 5))
	if !errors.Is(err, ErrFreshaFlowChanged) {
		t.Errorf("got %v, want ErrFreshaFlowChanged", err)
	}
}

func TestFreshaShopWithoutSlugHasNoLiveTimes(t *testing.T) {
	f, _ := stubFresha()
	_, err := f.Availability(context.Background(), catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindFresha}}, ny(2026, time.September, 5))
	if !errors.Is(err, ErrNoLiveAvailability) {
		t.Errorf("got %v, want ErrNoLiveAvailability", err)
	}
}

func TestFreshaSlugFromURL(t *testing.T) {
	s := catalog.Shop{Booking: catalog.Booking{URL: "https://www.fresha.com/a/some-venue-abc123?x=1"}}
	if got := freshaSlug(s); got != "some-venue-abc123" {
		t.Errorf("freshaSlug = %q", got)
	}
}

func TestFindActionMatchesTypeUnderAnyKey(t *testing.T) {
	var node any
	_ = json.Unmarshal([]byte(`{"a":{"primaryAction":`+act("Foo", `"x":"1"`)+`},"b":[{"addAction":`+act("Bar", "")+`}]}`), &node)
	if id, ok := findAction(node, "Bar", ""); !ok || !strings.Contains(id, "Bar") {
		t.Errorf("Bar not found: %q %v", id, ok)
	}
	if _, ok := findAction(node, "Foo", `"x":"2"`); ok {
		t.Error("must-constraint should have excluded Foo")
	}
	if _, ok := findAction(node, "Nope", ""); ok {
		t.Error("found an action that does not exist")
	}
}

func TestSplitClock(t *testing.T) {
	if h, m, ok := splitClock("14:30"); !ok || h != 14 || m != 30 {
		t.Errorf("14:30 -> %d %d %v", h, m, ok)
	}
	for _, bad := range []string{"", "9", "25:00", "12:60", "x:y"} {
		if _, _, ok := splitClock(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}
