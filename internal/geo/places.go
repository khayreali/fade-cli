package geo

// City is the level above neighborhoods. There is one, so screens show it as
// a breadcrumb rather than a list of one; a second city is what would turn
// it into a screen of its own.
const City = "New York"

// Neighborhood is a run of stops. Stops are listed outbound from Manhattan
// -- the order the train reaches them from Union Square -- which is the
// order a list of shops is read in: Bedford first, Halsey last, and on the G
// south to north.
type Neighborhood struct {
	ID    string
	Name  string
	Stops []string // stop ids, outbound
}

// Neighborhoods is the whole service area as people name it, corridor by
// corridor and outbound within each.
var Neighborhoods = []Neighborhood{
	{ID: "williamsburg", Name: "Williamsburg", Stops: []string{"bedford", "lorimer", "graham", "grand"}},
	{ID: "east-williamsburg", Name: "East Williamsburg", Stops: []string{"montrose", "morgan"}},
	{ID: "bushwick", Name: "Bushwick", Stops: []string{"jefferson", "dekalb", "myrtle-wyckoff", "halsey"}},
	{ID: "greenpoint", Name: "Greenpoint", Stops: []string{"nassau", "greenpoint-av"}},
	{ID: "ridgewood", Name: "Ridgewood", Stops: []string{"seneca", "forest", "fresh-pond"}},
}

// NeighborhoodByID finds a neighborhood.
func NeighborhoodByID(id string) (Neighborhood, bool) {
	for _, n := range Neighborhoods {
		if n.ID == id {
			return n, true
		}
	}
	return Neighborhood{}, false
}

// NeighborhoodOf finds the neighborhood a stop belongs to.
func NeighborhoodOf(stopID string) (Neighborhood, bool) {
	for _, n := range Neighborhoods {
		if n.Has(stopID) {
			return n, true
		}
	}
	return Neighborhood{}, false
}

// Has reports whether the stop is in this neighborhood.
func (n Neighborhood) Has(stopID string) bool {
	for _, id := range n.Stops {
		if id == stopID {
			return true
		}
	}
	return false
}

// StopList resolves the stop ids, in outbound order.
func (n Neighborhood) StopList() []Stop {
	out := make([]Stop, 0, len(n.Stops))
	for _, id := range n.Stops {
		if s, ok := StopByID(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// Line is the subway line serving the neighborhood -- each is on one.
func (n Neighborhood) Line() string {
	if stops := n.StopList(); len(stops) > 0 {
		return stops[0].Line
	}
	return ""
}

// Span names the first and last stop, for a subtitle.
func (n Neighborhood) Span() string {
	stops := n.StopList()
	switch len(stops) {
	case 0:
		return ""
	case 1:
		return stops[0].Name
	}
	return stops[0].Name + " → " + stops[len(stops)-1].Name
}
