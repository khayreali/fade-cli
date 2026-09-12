package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"fadecli/internal/catalog"
)

// Service is a choice from the provider's current menu. Labels preserve
// qualifications such as "from" and duration ranges instead of guessing a quote.
type Service struct {
	ID            string
	Name          string
	Price         int
	Duration      time.Duration
	PriceLabel    string
	DurationLabel string
}

// ServiceProvider supports choosing a service before requesting its times.
// The base Provider remains small for phone and website-only shops.
type ServiceProvider interface {
	Services(context.Context, catalog.Shop) ([]Service, error)
	AvailabilityFor(context.Context, catalog.Shop, time.Time, Service) ([]Slot, error)
}

var ErrServiceUnavailable = errors.New("choose a service from the shop's menu")

// ResolveService prefers an exact ID/name. Ambiguous input never silently
// selects another service with a different price or duration.
func ResolveService(services []Service, query string) (Service, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		for _, s := range services {
			if strings.EqualFold(s.Name, "haircut") {
				return s, nil
			}
		}
		for _, s := range services {
			if haircutLike.MatchString(s.Name) {
				return s, nil
			}
		}
		return Service{}, ErrServiceUnavailable
	}
	for _, s := range services {
		if s.ID == query {
			return s, nil
		}
	}
	var exact, partial []Service
	for _, s := range services {
		if strings.EqualFold(s.Name, query) {
			exact = append(exact, s)
		} else if strings.Contains(strings.ToLower(s.Name), strings.ToLower(query)) {
			partial = append(partial, s)
		}
	}
	if len(exact) > 0 {
		partial = exact
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	if len(partial) > 1 {
		return Service{}, fmt.Errorf("%q matches several services; choose a service ID", query)
	}
	return Service{}, fmt.Errorf("service %q is not on this shop's menu", query)
}

func defaultAvailability(ctx context.Context, p ServiceProvider, shop catalog.Shop, day time.Time) ([]Slot, error) {
	services, err := p.Services(ctx, shop)
	if err != nil {
		return nil, err
	}
	service, err := ResolveService(services, "")
	if err != nil {
		return nil, err
	}
	return p.AvailabilityFor(ctx, shop, day, service)
}

func (s Service) slot(start time.Time, url string) Slot {
	return Slot{Start: start, Service: s.Name, Price: s.Price, Duration: s.Duration,
		PriceLabel: s.PriceLabel, DurationLabel: s.DurationLabel, Barber: "any", BookURL: url}
}
