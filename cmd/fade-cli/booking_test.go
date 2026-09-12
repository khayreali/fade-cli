package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/provider"
)

type bookingStub struct {
	slots []provider.Slot
	err   error
}

func (b bookingStub) Services(context.Context, catalog.Shop) ([]provider.Service, error) {
	return []provider.Service{{ID: "cut", Name: "Haircut"}}, b.err
}
func (b bookingStub) AvailabilityFor(context.Context, catalog.Shop, time.Time, provider.Service) ([]provider.Slot, error) {
	return b.slots, b.err
}

func TestRecheckBookingRejectsGoneOrPastTimesAndSurfacesUpdates(t *testing.T) {
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, catalog.ShopLocation())
	wanted := provider.Slot{Start: now.Add(time.Hour), Service: "Haircut", Price: 45}
	choice := bookingChoice{slot: wanted, service: provider.Service{ID: "cut"}}
	for _, b := range []bookingStub{{}, {slots: []provider.Slot{{Start: now}}}, {err: errors.New("offline")}} {
		choice.provider = b
		if _, err := recheckBooking(context.Background(), catalog.Shop{}, choice, now); err == nil {
			t.Fatal("unavailable time accepted")
		}
	}
	wanted.Price = 55
	choice.provider = bookingStub{slots: []provider.Slot{wanted}}
	if got, err := recheckBooking(context.Background(), catalog.Shop{}, choice, now); err != nil || got.Price != 55 {
		t.Fatalf("updated quote: %+v, %v", got, err)
	}
}

func TestBookingSummaryUsesShopTimeAndDoesNotClaimConfirmation(t *testing.T) {
	start := time.Date(2026, 9, 15, 18, 0, 0, 0, time.UTC)
	choice := bookingChoice{service: provider.Service{Name: "Haircut", PriceLabel: "from $55.50", Duration: 45 * time.Minute}, slot: provider.Slot{Start: start}}
	summary := bookingSummary(catalog.Shop{Name: "Test shop", Address: "Test address"}, choice, "https://example.com/book")
	for _, want := range []string{"2:00pm EDT", "from $55.50", "45 min", "Not booked yet", "https://example.com/book"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("missing %q: %s", want, summary)
		}
	}
}

func TestFutureSlotsSortsWithoutMutatingProviderResponse(t *testing.T) {
	now := time.Now()
	slots := []provider.Slot{{Start: now.Add(2 * time.Hour)}, {Start: now.Add(-time.Hour)}, {Start: now.Add(time.Hour)}}
	got := futureSlots(slots, now)
	if len(got) != 2 || !got[0].Start.Equal(now.Add(time.Hour)) || !slots[0].Start.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("%v", got)
	}
}
