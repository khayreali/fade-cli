package catalog

import (
	"testing"

	"fadecli/internal/geo"
)

// The tests run against the real embedded seed, so a bad data edit fails the
// build rather than shipping.
func load(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load("")
	if err != nil {
		t.Fatalf("loading embedded catalog: %v", err)
	}
	return c
}

func TestSeedIsWellFormed(t *testing.T) {
	c := load(t)
	if len(c.Shops) < 20 {
		t.Fatalf("only %d shops in seed, expected the full corridor", len(c.Shops))
	}

	seen := map[string]bool{}
	for _, s := range c.Shops {
		if s.ID == "" || s.Name == "" || s.Address == "" {
			t.Errorf("shop %+v missing a required field", s)
		}
		if seen[s.ID] {
			t.Errorf("duplicate shop id %q", s.ID)
		}
		seen[s.ID] = true

		if !s.Located() {
			t.Errorf("shop %q has no coordinates -- run `fade-cli dev geocode`", s.ID)
		}
		// Every shop must be bookable somehow, or it has no business being
		// listed: a row you can't act on is noise.
		if s.Booking.Kind != KindPhone && s.Booking.URL == "" && s.Booking.ID == "" {
			t.Errorf("shop %q has no way to book", s.ID)
		}
		if s.PriceMin > 0 && s.PriceMax > 0 && s.PriceMin > s.PriceMax {
			t.Errorf("shop %q has price_min above price_max", s.ID)
		}
	}
}

func TestSeedShopsSitInTheServiceArea(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		stop, ok := s.NearestStop()
		if !ok {
			continue
		}
		// Nothing in the catalog should be more than a mile from the corridor;
		// if it is, either the geocode is wrong or the shop doesn't belong.
		if d := geo.MilesBetween(s.Point, stop.Point); d > 1.0 {
			t.Errorf("shop %q is %.2f mi from its nearest stop (%s)", s.ID, d, stop.Name)
		}
	}
}

func TestFindFiltersByPriceOnTheLowEnd(t *testing.T) {
	c := load(t)
	got := c.Find(Query{MaxPrice: 30})
	if len(got) == 0 {
		t.Fatal("no shops under $30, expected several")
	}
	for _, r := range got {
		if r.Shop.PriceMin > 30 {
			t.Errorf("%s has price_min $%d, above the $30 cap", r.Shop.ID, r.Shop.PriceMin)
		}
	}
}

func TestFindStopRangeIsInclusive(t *testing.T) {
	c := load(t)
	got := c.Find(Query{FromStop: "bedford", ToStop: "graham"})
	if len(got) == 0 {
		t.Fatal("no shops between Bedford and Graham")
	}
	for _, r := range got {
		if r.Stop.Order < 1 || r.Stop.Order > 3 {
			t.Errorf("%s at %s (order %d) is outside bedford..graham", r.Shop.ID, r.Stop.Name, r.Stop.Order)
		}
	}
}

func TestFindReversedStopRangeStillWorks(t *testing.T) {
	c := load(t)
	fwd := c.Find(Query{FromStop: "bedford", ToStop: "graham"})
	rev := c.Find(Query{FromStop: "graham", ToStop: "bedford"})
	if len(fwd) != len(rev) {
		t.Errorf("reversed range gave %d results, forward gave %d", len(rev), len(fwd))
	}
}

func TestFindSortsByDistanceWhenOriginKnown(t *testing.T) {
	c := load(t)
	graham, _ := geo.StopByID("graham")
	got := c.Find(Query{Origin: &graham.Point, MaxMiles: 1.0})
	if len(got) < 2 {
		t.Fatalf("expected several shops within a mile of Graham, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Miles > got[i].Miles {
			t.Errorf("results out of order: %.3f before %.3f", got[i-1].Miles, got[i].Miles)
		}
	}
}

func TestCollapseByVenueKeepsTheBestRated(t *testing.T) {
	c := load(t)
	full := c.Find(Query{Text: "cutmaster"})
	collapsed := c.Find(Query{Text: "cutmaster", CollapseBy: "venue"})

	if len(full) < 2 {
		t.Skip("seed no longer has multiple barbers at one venue")
	}
	if len(collapsed) != 1 {
		t.Fatalf("collapsed to %d rows, want 1", len(collapsed))
	}
	if n := len(collapsed[0].Alongside); n != len(full)-1 {
		t.Errorf("Alongside has %d shops, want %d", n, len(full)-1)
	}
	for _, other := range collapsed[0].Alongside {
		if better(other, collapsed[0].Shop) {
			t.Errorf("%s should have won over %s", other.ID, collapsed[0].Shop.ID)
		}
	}
}

func TestResolveExactBeatsSubstring(t *testing.T) {
	c := load(t)
	// "cutmaster-maiki" is an exact id and also a substring of nothing else.
	s, _, err := c.Resolve("cutmaster-maiki")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if s.ID != "cutmaster-maiki" {
		t.Errorf("resolved to %s", s.ID)
	}
}

func TestResolveReportsAmbiguity(t *testing.T) {
	c := load(t)
	_, candidates, err := c.Resolve("cutmaster")
	if err == nil {
		t.Fatal("expected ambiguity error for \"cutmaster\"")
	}
	if len(candidates) < 2 {
		t.Errorf("got %d candidates, want several", len(candidates))
	}
}

func TestResolveMiss(t *testing.T) {
	c := load(t)
	if _, cands, err := c.Resolve("supercuts"); err == nil || len(cands) != 0 {
		t.Error("expected a clean miss for an absent shop")
	}
}

func TestLocalCatalogOverridesSeed(t *testing.T) {
	c := load(t)
	before, ok := c.Get("cabello-brooklyn")
	if !ok {
		t.Fatal("seed is missing cabello-brooklyn")
	}
	c.merge([]Shop{
		{ID: "cabello-brooklyn", Name: before.Name, Address: before.Address, PriceMin: 999},
		{ID: "my-guy", Name: "My Guy", Address: "somewhere"},
	})

	after, _ := c.Get("cabello-brooklyn")
	if after.PriceMin != 999 {
		t.Errorf("local override did not win: price_min = %d", after.PriceMin)
	}
	if _, ok := c.Get("my-guy"); !ok {
		t.Error("local-only shop was not added")
	}
}

func TestPriceLabel(t *testing.T) {
	cases := []struct {
		shop Shop
		want string
	}{
		{Shop{}, "?"},
		{Shop{PriceMin: 40, PriceMax: 65}, "$40-65"},
		{Shop{PriceMin: 55, PriceMax: 55}, "$55"},
		{Shop{PriceMin: 30}, "$30+"},
	}
	for _, c := range cases {
		if got := c.shop.PriceLabel(); got != c.want {
			t.Errorf("PriceLabel(%+v) = %q, want %q", c.shop, got, c.want)
		}
	}
}

func TestFindCorridorFilter(t *testing.T) {
	c := load(t)
	got := c.Find(Query{Corridor: "g-greenpoint"})
	if len(got) == 0 {
		t.Fatal("no Greenpoint shops found")
	}
	for _, r := range got {
		if r.Stop.Corridor != "g-greenpoint" {
			t.Errorf("%s is on %s, not the Greenpoint corridor", r.Shop.ID, r.Stop.Corridor)
		}
	}
	if len(got) >= len(c.Shops) {
		t.Error("corridor filter returned everything")
	}
}

// A stop range is scoped to one corridor, so bedford..graham must not sweep in
// Greenpoint shops that happen to sit at a low Order on the G.
func TestFindStopRangeIsScopedToOneCorridor(t *testing.T) {
	c := load(t)
	for _, r := range c.Find(Query{FromStop: "bedford", ToStop: "graham"}) {
		if r.Stop.Corridor != "l-bk" {
			t.Errorf("%s (%s) leaked into an L-corridor range", r.Shop.ID, r.Stop.Corridor)
		}
	}
}

func TestFindStopRangeOnTheGCorridor(t *testing.T) {
	c := load(t)
	got := c.Find(Query{FromStop: "greenpoint-av", ToStop: "nassau"})
	if len(got) == 0 {
		t.Fatal("no shops in the Greenpoint range")
	}
	for _, r := range got {
		if r.Stop.Corridor != "g-greenpoint" {
			t.Errorf("%s is on %s", r.Shop.ID, r.Stop.Corridor)
		}
	}
}

func TestSingleBoundPicksItsOwnCorridor(t *testing.T) {
	c := load(t)
	// Only --to given: the corridor should come from that stop, not default
	// to the first corridor in the list.
	for _, r := range c.Find(Query{ToStop: "nassau"}) {
		if r.Stop.Corridor != "g-greenpoint" {
			t.Errorf("%s (%s) should not appear for --to nassau", r.Shop.ID, r.Stop.Corridor)
		}
	}
}

func TestEveryCorridorHasShops(t *testing.T) {
	c := load(t)
	for _, cor := range geo.Corridors {
		if n := len(c.Find(Query{Corridor: cor.ID})); n == 0 {
			t.Errorf("corridor %s has no shops -- it shouldn't be in the service area", cor.ID)
		}
	}
}
