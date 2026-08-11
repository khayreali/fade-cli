package catalog

import (
	"strings"
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
		{Shop{}, "—"},
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

// A rating with no source can't be compared against the others -- Booksy,
// Fresha and Google score different populations.
func TestEveryRatingDeclaresItsSource(t *testing.T) {
	c := load(t)
	valid := map[string]bool{"booksy": true, "fresha": true, "google": true, "web": true}
	for _, s := range c.Shops {
		if s.Rating == 0 {
			if s.RatingSrc != "" {
				t.Errorf("%s has a rating source but no rating", s.ID)
			}
			continue
		}
		if s.RatingSrc == "" {
			t.Errorf("%s has rating %.1f with no source", s.ID, s.Rating)
		} else if !valid[s.RatingSrc] {
			t.Errorf("%s has unknown rating source %q", s.ID, s.RatingSrc)
		}
	}
}

func TestRatingsAreInRange(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		if s.Rating < 0 || s.Rating > 5 {
			t.Errorf("%s has out-of-range rating %.1f", s.ID, s.Rating)
		}
	}
}

// Greenpoint shipped with no prices or ratings at all; this guards the pass
// that filled them in from being quietly reverted.
func TestGreenpointHasPricesAndRatings(t *testing.T) {
	c := load(t)
	got := c.Find(Query{Corridor: "g-greenpoint"})
	var priced, rated int
	for _, r := range got {
		if r.Shop.PriceMin > 0 {
			priced++
		}
		if r.Shop.Rating > 0 {
			rated++
		}
	}
	if priced < 3 {
		t.Errorf("only %d of %d Greenpoint shops have a price", priced, len(got))
	}
	if rated < 3 {
		t.Errorf("only %d of %d Greenpoint shops have a rating", rated, len(got))
	}
}

func TestSortCheapestPutsUnknownPricesLast(t *testing.T) {
	c := load(t)
	got := c.Find(Query{Sort: SortCheapest})
	if len(got) < 10 {
		t.Fatalf("only %d results", len(got))
	}

	var seenUnknown bool
	last := 0
	for _, r := range got {
		p := r.Shop.PriceMin
		if p == 0 {
			seenUnknown = true
			continue
		}
		// A priced shop after an unpriced one means zero sorted as "cheapest".
		if seenUnknown {
			t.Fatalf("%s ($%d) ranked below a shop with no price", r.Shop.ID, p)
		}
		if p < last {
			t.Errorf("%s ($%d) out of order after $%d", r.Shop.ID, p, last)
		}
		last = p
	}
	if !seenUnknown {
		t.Skip("every shop has a price; nothing to check")
	}
}

func TestSortBestRatedPutsUnratedLast(t *testing.T) {
	c := load(t)
	got := c.Find(Query{Sort: SortBestRated})

	var seenUnrated bool
	last := 5.1
	for _, r := range got {
		v := r.Shop.Rating
		if v == 0 {
			seenUnrated = true
			continue
		}
		if seenUnrated {
			t.Fatalf("%s (%.1f) ranked below an unrated shop", r.Shop.ID, v)
		}
		if v > last {
			t.Errorf("%s (%.1f) out of order after %.1f", r.Shop.ID, v, last)
		}
		last = v
	}
}

func TestSortDefaultStaysNearest(t *testing.T) {
	c := load(t)
	graham, _ := geo.StopByID("graham")
	got := c.Find(Query{Origin: &graham.Point, MaxMiles: 1.0})
	for i := 1; i < len(got); i++ {
		if got[i-1].Miles > got[i].Miles {
			t.Fatalf("default sort is no longer by distance: %.3f before %.3f", got[i-1].Miles, got[i].Miles)
		}
	}
}

func TestSortCycles(t *testing.T) {
	seen := map[SortBy]bool{}
	s := SortNearest
	for i := 0; i < 3; i++ {
		seen[s] = true
		if s.Label() == "" {
			t.Errorf("%q has no label", s)
		}
		s = s.Next()
	}
	if s != SortNearest {
		t.Errorf("cycle did not return to the default, ended at %q", s)
	}
	if len(seen) != 3 {
		t.Errorf("cycle covered %d sorts, want 3", len(seen))
	}
}

// Ties break on distance: two equally cheap shops should list the nearer first.
func TestSortCheapestBreaksTiesByDistance(t *testing.T) {
	c := load(t)
	graham, _ := geo.StopByID("graham")
	got := c.Find(Query{Origin: &graham.Point, Sort: SortCheapest})
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if a.Shop.PriceMin == b.Shop.PriceMin && a.Shop.PriceMin != 0 {
			if a.Miles > b.Miles {
				t.Errorf("equal prices not tie-broken by distance: %s (%.2f) before %s (%.2f)",
					a.Shop.ID, a.Miles, b.Shop.ID, b.Miles)
			}
		}
	}
}

// A row you cannot act on is worse than no row: every shop must offer some
// route to booking, whether a link or a phone number.
func TestEveryShopIsReachable(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		if s.Phone == "" && s.Booking.URL == "" && s.Booking.ID == "" {
			t.Errorf("%s has no phone, link or provider id -- it cannot be booked", s.ID)
		}
	}
}

// Two distinct venues sharing a point is the signature of a geocode that fell
// back to a street centroid: Clippers and Mark's are five blocks apart on
// Fresh Pond Rd and once sat 15 m apart because neither house number resolved.
// The real minimum here is ~21 m (adjacent buildings on one block), so this
// only catches genuine stacking, not close neighbours.
func TestDistinctVenuesAreNotStacked(t *testing.T) {
	const stackedMiles = 0.006 // ~10 m

	c := load(t)
	for i := range c.Shops {
		for j := i + 1; j < len(c.Shops); j++ {
			a, b := c.Shops[i], c.Shops[j]
			if a.Venue == b.Venue || !a.Located() || !b.Located() {
				continue
			}
			if d := geo.MilesBetween(a.Point, b.Point); d < stackedMiles {
				t.Errorf("%s and %s are %.0f m apart at different venues -- likely a street-level geocode",
					a.ID, b.ID, d*1609)
			}
		}
	}
}

// Venue is the collapse key, so it has to identify a building. One shop had
// "Bushwick" as its venue -- a neighbourhood, which would have merged it with
// every future shop tagged the same way. Requiring the venue to be the leading
// part of the address keeps the key tied to a street address.
func TestVenueIdentifiesABuilding(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		if s.Venue == "" {
			t.Errorf("%s has no venue; collapse would fall back to the full address", s.ID)
			continue
		}
		if !strings.HasPrefix(strings.ToLower(s.Address), strings.ToLower(s.Venue)) {
			t.Errorf("%s: venue %q is not the start of address %q", s.ID, s.Venue, s.Address)
		}
	}
}

// A venue must not span two addresses, or collapse merges unrelated shops.
func TestVenueMapsToOneAddress(t *testing.T) {
	c := load(t)
	seen := map[string]string{}
	for _, s := range c.Shops {
		key := strings.ToLower(s.Venue)
		if prev, ok := seen[key]; ok && prev != strings.ToLower(s.Address) {
			t.Errorf("venue %q covers two addresses: %q and %q", s.Venue, prev, s.Address)
		}
		seen[key] = strings.ToLower(s.Address)
	}
}

func TestWalkInLabels(t *testing.T) {
	if WalkInUnknown.Label() != "" {
		t.Error("an unchecked walk-in policy must render as nothing, not a claim")
	}
	seen := map[string]bool{}
	for _, w := range []WalkIn{WalkInWelcome, WalkInOnly, WalkInNo} {
		l := w.Label()
		if l == "" || seen[l] {
			t.Errorf("%q has a blank or duplicate label", w)
		}
		seen[l] = true
	}
}

// Only values the UI understands may appear in the seed.
func TestSeededWalkInValuesAreValid(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		switch s.WalkIn {
		case WalkInUnknown, WalkInWelcome, WalkInOnly, WalkInNo:
		default:
			t.Errorf("%s has unknown walk_in %q", s.ID, s.WalkIn)
		}
	}
}

// Fresha publishes two kinds of page: /a/ is a partner venue you can book, and
// /lvp/ is an SEO directory listing for a shop that is NOT a Fresha partner --
// it carries no booking flow, only "Call to book". Twenty-two shops were once
// linked to /lvp/ pages and counted as bookable online, which was false.
func TestNoFreshaDirectoryPagesAreTreatedAsBooking(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		if strings.Contains(s.Booking.URL, "/lvp/") {
			t.Errorf("%s links a Fresha directory page as if it were bookable: %s", s.ID, s.Booking.URL)
		}
	}
}

// InfoURL exists precisely so a directory page is never mistaken for booking.
func TestInfoURLIsNeverABookingLink(t *testing.T) {
	c := load(t)
	for _, s := range c.Shops {
		if s.InfoURL != "" && s.InfoURL == s.Booking.URL {
			t.Errorf("%s uses its directory page as a booking link", s.ID)
		}
	}
}

// A salon cut and a barbershop fade are both haircuts but not the same
// product, so the type must be filterable and must never be guessed.
func TestShopTypeFilter(t *testing.T) {
	c := load(t)

	salons := c.Find(Query{Type: TypeSalon, TypeSet: true})
	if len(salons) == 0 {
		t.Fatal("no salons in the catalog")
	}
	for _, r := range salons {
		if r.Shop.Type != TypeSalon {
			t.Errorf("%s is not a salon", r.Shop.ID)
		}
	}

	shops := c.Find(Query{Type: TypeBarbershop, TypeSet: true})
	for _, r := range shops {
		if r.Shop.Type != TypeBarbershop {
			t.Errorf("%s leaked into a barbershop-only query", r.Shop.ID)
		}
	}
	if len(salons)+len(shops) != len(c.Shops) {
		t.Errorf("%d + %d != %d: the two types should partition the catalog",
			len(salons), len(shops), len(c.Shops))
	}

	// No filter must mean no filtering, not "barbershops only".
	if got := len(c.Find(Query{})); got != len(c.Shops) {
		t.Errorf("unfiltered query returned %d of %d", got, len(c.Shops))
	}
}

func TestShopTypeLabels(t *testing.T) {
	if TypeBarbershop.Label() != "" {
		t.Error("the default type must render as nothing, not a tag on every row")
	}
	if TypeSalon.Label() == "" {
		t.Error("a salon must be labelled")
	}
}

// The collapsed row must carry the winner's own distance fields: swapping only
// the Shop left the loser's walk time on the winner's row, which both sorted
// and displayed wrongly.
func TestCollapseCarriesTheWinnersDistance(t *testing.T) {
	c := load(t)
	graham, _ := geo.StopByID("graham")
	collapsed := c.Find(Query{Origin: &graham.Point, Text: "cutmaster", CollapseBy: "venue"})
	if len(collapsed) != 1 {
		t.Skip("cutmaster venue no longer collapses to one row")
	}
	r := collapsed[0]
	wantMiles := geo.MilesBetween(graham.Point, r.Shop.Point)
	if r.Miles != wantMiles {
		t.Errorf("row Miles %.4f but winner's own distance is %.4f", r.Miles, wantMiles)
	}
}

func TestResolveExactNameBeatsPrefixAmbiguity(t *testing.T) {
	c := load(t)
	c.merge([]Shop{
		{ID: "zz-fade", Name: "Fade", Address: "1 Test St, Brooklyn, NY", Booking: Booking{Kind: KindPhone}, Phone: "+15550000001"},
		{ID: "zz-fade-factory", Name: "Fade Factory", Address: "2 Test St, Brooklyn, NY", Booking: Booking{Kind: KindPhone}, Phone: "+15550000002"},
	})
	got, _, err := c.Resolve("fade")
	if err != nil {
		t.Fatalf("exact name drowned in prefix ambiguity: %v", err)
	}
	if got.ID != "zz-fade" {
		t.Errorf("resolved %s, want the exact-name match", got.ID)
	}
}
