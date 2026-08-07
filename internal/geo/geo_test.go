package geo

import (
	"math"
	"testing"
)

func TestMilesBetweenKnownStops(t *testing.T) {
	bedford, _ := StopByID("bedford")
	halsey, _ := StopByID("halsey")

	// Bedford Av to Halsey St is a little over 3 miles down the L.
	got := MilesBetween(bedford.Point, halsey.Point)
	if got < 3.0 || got > 3.4 {
		t.Errorf("Bedford->Halsey = %.2f mi, want roughly 3.0-3.4", got)
	}
}

func TestMilesBetweenIsSymmetricAndZero(t *testing.T) {
	a := Point{40.7143, -73.9441}
	b := Point{40.6956, -73.9041}

	if d := MilesBetween(a, a); d != 0 {
		t.Errorf("distance to self = %v, want 0", d)
	}
	if f, r := MilesBetween(a, b), MilesBetween(b, a); math.Abs(f-r) > 1e-9 {
		t.Errorf("asymmetric: %v vs %v", f, r)
	}
}

func TestWalkMinutesAccountsForTheGrid(t *testing.T) {
	graham, _ := StopByID("graham")
	grand, _ := StopByID("grand")

	crow := MilesBetween(graham.Point, grand.Point)
	got := WalkMinutes(graham.Point, grand.Point)

	// Should exceed the crow-flies time, since you can't walk through blocks.
	crowMin := int(math.Round(crow / 3.0 * 60))
	if got <= crowMin {
		t.Errorf("walk %d min not greater than crow-flies %d min", got, crowMin)
	}
	if got < 5 || got > 15 {
		t.Errorf("Graham->Grand = %d min, want a plausible 5-15", got)
	}
}

func TestNearestStop(t *testing.T) {
	// 476 Humboldt St, which the geocoder places just north of Graham Av.
	cabello := Point{40.71857, -73.94309}
	if s := NearestStop(cabello); s.ID != "graham" {
		t.Errorf("nearest to Cabello = %s, want graham", s.ID)
	}
}

func TestCorridorsAreOrderedAndGloballyUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Corridors {
		if len(c.Stops) == 0 {
			t.Errorf("corridor %s has no stops", c.ID)
		}
		for i, s := range c.Stops {
			if s.Order != i+1 {
				t.Errorf("%s: stop %s has Order %d at index %d", c.ID, s.ID, s.Order, i)
			}
			if s.Corridor != c.ID {
				t.Errorf("stop %s claims corridor %q, is listed under %q", s.ID, s.Corridor, c.ID)
			}
			// Ids must be unique across corridors, not just within one:
			// StopByID and the --near flag look them up globally.
			if seen[s.ID] {
				t.Errorf("duplicate stop id %s", s.ID)
			}
			seen[s.ID] = true
		}
	}
}

// Two stops at the same street corner would split one walkable cluster of
// shops between corridors depending on which centroid won by a few feet.
func TestNoTwoStopsShareACorner(t *testing.T) {
	all := AllStops()
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if d := MilesBetween(all[i].Point, all[j].Point); d < 0.1 {
				t.Errorf("%s and %s are %.3f mi apart -- too close to disambiguate",
					all[i].ID, all[j].ID, d)
			}
		}
	}
}

func TestAllStopsCoversEveryCorridor(t *testing.T) {
	want := 0
	for _, c := range Corridors {
		want += len(c.Stops)
	}
	if got := len(AllStops()); got != want {
		t.Errorf("AllStops returned %d, want %d", got, want)
	}
}

func TestNearestStopSpansCorridors(t *testing.T) {
	// 197 Franklin St, Greenpoint -- must land on the G, not the nearest L stop.
	otisFinn := Point{40.73358, -73.95843}
	got := NearestStop(otisFinn)
	if got.Corridor != "g-greenpoint" {
		t.Errorf("Greenpoint shop landed on %s (%s), want the G corridor", got.ID, got.Corridor)
	}
}

func TestCorridorByID(t *testing.T) {
	if c, ok := CorridorByID("l-bk"); !ok || c.Line != "L" {
		t.Errorf("CorridorByID(l-bk) = %+v, %v", c, ok)
	}
	if _, ok := CorridorByID("nope"); ok {
		t.Error("CorridorByID matched a non-existent corridor")
	}
}

func TestStopByIDMiss(t *testing.T) {
	if _, ok := StopByID("bedford-ave"); ok {
		t.Error("StopByID matched a non-existent id")
	}
}
