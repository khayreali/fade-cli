package geo

import "testing"

// Every stop belongs to exactly one neighborhood: a stop in none would hide
// its shops from the index, a stop in two would list them twice.
func TestEveryStopHasOneNeighborhood(t *testing.T) {
	seen := map[string]string{}
	for _, n := range Neighborhoods {
		for _, id := range n.Stops {
			if _, ok := StopByID(id); !ok {
				t.Errorf("%s names unknown stop %q", n.ID, id)
			}
			if prev, dup := seen[id]; dup {
				t.Errorf("stop %q is in both %s and %s", id, prev, n.ID)
			}
			seen[id] = n.ID
		}
	}
	for _, s := range AllStops() {
		if _, ok := seen[s.ID]; !ok {
			t.Errorf("stop %q is in no neighborhood", s.ID)
		}
	}
}

// Neighborhood stops run outbound from Manhattan, which is the corridor's
// own Order. If someone lists them the other way round the shop list would
// read against the direction of travel.
func TestNeighborhoodStopsRunOutbound(t *testing.T) {
	for _, n := range Neighborhoods {
		stops := n.StopList()
		for i := 1; i < len(stops); i++ {
			if stops[i].Corridor != stops[i-1].Corridor {
				t.Errorf("%s spans corridors %s and %s", n.ID, stops[i-1].Corridor, stops[i].Corridor)
			}
			if stops[i].Order <= stops[i-1].Order {
				t.Errorf("%s: %s (order %d) listed after %s (order %d)", n.ID,
					stops[i].ID, stops[i].Order, stops[i-1].ID, stops[i-1].Order)
			}
		}
	}
}

// The G runs south to north through Greenpoint: Nassau Av is the stop
// nearer Manhattan and must come first.
func TestGreenpointRunsSouthToNorth(t *testing.T) {
	n, ok := NeighborhoodByID("greenpoint")
	if !ok {
		t.Fatal("no greenpoint neighborhood")
	}
	if len(n.Stops) < 2 || n.Stops[0] != "nassau" || n.Stops[1] != "greenpoint-av" {
		t.Errorf("greenpoint stops = %v, want nassau then greenpoint-av", n.Stops)
	}
	nassau, _ := StopByID("nassau")
	gp, _ := StopByID("greenpoint-av")
	if nassau.Order >= gp.Order {
		t.Errorf("corridor order has Nassau (%d) after Greenpoint Av (%d)", nassau.Order, gp.Order)
	}
}

func TestSpanAndLine(t *testing.T) {
	n, _ := NeighborhoodByID("williamsburg")
	if got := n.Span(); got != "Bedford Av → Grand St" {
		t.Errorf("Span = %q", got)
	}
	if got := n.Line(); got != "L" {
		t.Errorf("Line = %q", got)
	}
	if _, ok := NeighborhoodOf("fresh-pond"); !ok {
		t.Error("fresh-pond should map to a neighborhood")
	}
}
