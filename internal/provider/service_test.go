package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceSelectionDoesNotSubstituteAnotherTreatment(t *testing.T) {
	menu := []Service{{ID: "beard", Name: "Beard Trim"}, {ID: "combo", Name: "Haircut + Beard"}, {ID: "cut", Name: "Haircut"}}
	s, err := ResolveService(menu, "")
	if err != nil || s.ID != "cut" {
		t.Fatalf("default = %+v, %v", s, err)
	}
	if _, err := ResolveService(menu[:1], ""); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("beard-only menu: %v", err)
	}
	menu = append(menu, Service{ID: "cut2", Name: "Haircut"})
	if _, err := ResolveService(menu, "Haircut"); err == nil {
		t.Fatal("ambiguous name silently selected a variant")
	}
	if s, err := ResolveService(menu, "cut2"); err != nil || s.ID != "cut2" {
		t.Fatalf("ID choice: %+v, %v", s, err)
	}
}

func TestBooksySelectedServiceAndDateChangesReuseMenu(t *testing.T) {
	b, stub := stubBooksy()
	ctx := context.Background()
	menu, err := b.Services(ctx, booksyShop)
	if err != nil {
		t.Fatal(err)
	}
	service, err := ResolveService(menu, "Beard Trim")
	if err != nil {
		t.Fatal(err)
	}
	for _, day := range []int{8, 9} {
		slots, err := b.AvailabilityFor(ctx, booksyShop, ny(2026, time.September, day), service)
		if err != nil || len(slots) == 0 {
			t.Fatalf("%d: %v %v", day, slots, err)
		}
		if slots[0].Service != "Beard Trim" || slots[0].Price != 25 || slots[0].Duration != 20*time.Minute {
			t.Fatalf("wrong service: %+v", slots[0])
		}
	}
	if len(stub.requests) != 3 {
		t.Fatalf("menu should be fetched once, requests: %v", stub.requests)
	}
	for _, req := range stub.requests[1:] {
		if !strings.Contains(req, `"service_variant_id":901`) {
			t.Fatalf("wrong variant: %s", req)
		}
	}
}

func TestBooksyMalformedScheduleIsNotAnEmptyCalendar(t *testing.T) {
	for _, payload := range []string{`{}`, `{"time_slots":null}`, `{"time_slots":[{"date":"2026-09-08","slots":[{"t":"nonsense"}]}]}`} {
		b, stub := stubBooksy()
		stub.slotsBody = payload
		_, err := b.Availability(context.Background(), booksyShop, ny(2026, time.September, 8))
		if !errors.Is(err, ErrBooksyChanged) {
			t.Errorf("%s: %v", payload, err)
		}
	}
}

func TestFreshaSelectedServiceDoesNotSelectOrReserveATime(t *testing.T) {
	f, stub := stubFresha()
	ctx := context.Background()
	menu, err := f.Services(ctx, freshaShop)
	if err != nil {
		t.Fatal(err)
	}
	service, err := ResolveService(menu, "Beard Trim")
	if err != nil {
		t.Fatal(err)
	}
	slots, err := f.AvailabilityFor(ctx, freshaShop, ny(2026, time.September, 5), service)
	if err != nil || len(slots) != 2 {
		t.Fatalf("%v %v", slots, err)
	}
	if slots[0].Service != "Beard Trim" || slots[0].Price != 35 || slots[0].Duration != 20*time.Minute {
		t.Fatalf("%+v", slots[0])
	}
	if !strings.Contains(stub.pressed[0], `"catalogId":"s:1"`) {
		t.Fatalf("wrong service: %v", stub.pressed)
	}
	for _, action := range stub.pressed {
		if strings.Contains(action, "onScreenTimeSet") || strings.Contains(action, "Confirm") {
			t.Fatalf("availability selected/submitted a time: %s", action)
		}
	}
}

func TestFreshaClockAcceptsLocalizedDisplayAndValidatesTokenDate(t *testing.T) {
	for _, display := range []string{"2:30\u202fPM", "2:30 pm", "14:30"} {
		h, m, ok := splitClock(display)
		if !ok || h != 14 || m != 30 {
			t.Fatalf("%q -> %d:%d %v", display, h, m, ok)
		}
	}
	var slot map[string]any
	_ = json.Unmarshal([]byte(`{"time":"localized display","action":`+act("onScreenTimeSet", `"date":"2026-09-08","time":52200`)+`}`), &slot)
	if h, m, ok := freshaSlotClock(slot, "2026-09-08"); !ok || h != 14 || m != 30 {
		t.Fatalf("token -> %d:%d %v", h, m, ok)
	}
	if _, _, ok := freshaSlotClock(slot, "2026-09-09"); ok {
		t.Fatal("previous day's slots relabelled as requested day")
	}
}

func TestFreshaPreservesPriceAndDurationLabels(t *testing.T) {
	var screen map[string]any
	_ = json.Unmarshal([]byte(`{"screenServices":{"categories":[{"items":[{"name":"Long cut","caption":"1 hr 15 min","price":{"formatted":"from $55.50"},"primaryAction":`+act("onScreenServicesModalServiceOpen", `"catalogId":"s:3"`)+`}]}]}}`), &screen)
	items := freshaServiceItems(screen)
	if len(items) != 1 || items[0].Duration != 75*time.Minute || items[0].PriceLabel != "from $55.50" {
		t.Fatalf("%+v", items)
	}
}

func TestFreshaRejectsMissingTimeslotsAndDisabledActions(t *testing.T) {
	f := &freshaFlow{}
	var screen map[string]any
	_ = json.Unmarshal([]byte(`{`+stubTime()+`}`), &screen)
	screen["screenTime"].(map[string]any)["day"] = map[string]any{}
	_, err := f.slotsOn(screen, ny(2026, time.September, 5), &freshaService{}, freshaShop)
	if !errors.Is(err, ErrFreshaFlowChanged) {
		t.Fatalf("missing day silently returned empty: %v", err)
	}
	var action map[string]any
	_ = json.Unmarshal([]byte(act("ModalService.onAdd", "")), &action)
	action["isDisabled"] = true
	if _, ok := findAction(action, "ModalService.onAdd", ""); ok {
		t.Fatal("disabled action accepted")
	}
}
