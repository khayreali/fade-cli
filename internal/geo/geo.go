// Package geo handles coordinates, distance, and the subway corridors that
// define the service area. Neighborhoods are fuzzy; train stops are not, so
// the catalog anchors every shop to its nearest stop and the tool expands by
// adding corridors.
package geo

import "math"

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Stop is a subway station used as a service-area anchor.
type Stop struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Line string `json:"line"`
	// Corridor is the id of the corridor this stop belongs to. Order is only
	// comparable within one corridor.
	Corridor string `json:"corridor"`
	Order    int    `json:"order"`
	Point    Point  `json:"point"`
}

// Corridor is a named run of stops along one line. Adding a neighborhood means
// appending a corridor here and seeding shops against its stops.
//
// Corridors deliberately do not include stations that share a street corner
// with a stop on another corridor -- Metropolitan Av (G) is the same complex
// as Lorimer St (L), and listing both would split one walkable cluster of
// shops across two service areas depending on which centroid won by a few feet.
type Corridor struct {
	ID    string
	Name  string
	Line  string
	Stops []Stop
}

// Corridors is the full service area, in the order they were added.
var Corridors = []Corridor{lCorridor, gCorridor}

var lCorridor = Corridor{
	ID:   "l-bk",
	Name: "Williamsburg → Halsey St",
	Line: "L",
	Stops: []Stop{
		stop("bedford", "Bedford Av", "L", "l-bk", 1, 40.717304, -73.956872),
		stop("lorimer", "Lorimer St", "L", "l-bk", 2, 40.714063, -73.950275),
		stop("graham", "Graham Av", "L", "l-bk", 3, 40.714565, -73.944053),
		stop("grand", "Grand St", "L", "l-bk", 4, 40.711926, -73.940770),
		stop("montrose", "Montrose Av", "L", "l-bk", 5, 40.707739, -73.939748),
		stop("morgan", "Morgan Av", "L", "l-bk", 6, 40.706152, -73.933147),
		stop("jefferson", "Jefferson St", "L", "l-bk", 7, 40.706607, -73.922913),
		stop("dekalb", "DeKalb Av", "L", "l-bk", 8, 40.703811, -73.918425),
		stop("myrtle-wyckoff", "Myrtle-Wyckoff Avs", "L", "l-bk", 9, 40.699814, -73.911586),
		stop("halsey", "Halsey St", "L", "l-bk", 10, 40.695602, -73.904084),
	},
}

var gCorridor = Corridor{
	ID:   "g-greenpoint",
	Name: "Greenpoint",
	Line: "G",
	Stops: []Stop{
		stop("greenpoint-av", "Greenpoint Av", "G", "g-greenpoint", 1, 40.731352, -73.954449),
		stop("nassau", "Nassau Av", "G", "g-greenpoint", 2, 40.724635, -73.951277),
	},
}

func stop(id, name, line, corridor string, order int, lat, lon float64) Stop {
	return Stop{ID: id, Name: name, Line: line, Corridor: corridor, Order: order, Point: Point{lat, lon}}
}

// AllStops returns every stop across every corridor.
func AllStops() []Stop {
	var out []Stop
	for _, c := range Corridors {
		out = append(out, c.Stops...)
	}
	return out
}

// StopByID looks up a stop by its short id ("bedford"), which is what users
// type. Ids are unique across corridors.
func StopByID(id string) (Stop, bool) {
	for _, s := range AllStops() {
		if s.ID == id {
			return s, true
		}
	}
	return Stop{}, false
}

// CorridorByID finds a corridor.
func CorridorByID(id string) (Corridor, bool) {
	for _, c := range Corridors {
		if c.ID == id {
			return c, true
		}
	}
	return Corridor{}, false
}

// AreaName summarises the whole service area for headers.
func AreaName() string {
	switch len(Corridors) {
	case 0:
		return "no service area"
	case 1:
		return Corridors[0].Name
	default:
		return Corridors[0].Name + " + " + Corridors[len(Corridors)-1].Name
	}
}

const earthRadiusMi = 3958.8

// MilesBetween returns great-circle distance in miles. At neighborhood scale
// the haversine error versus real walking distance is dwarfed by the fact that
// Brooklyn's street grid is not a straight line, so WalkMinutes corrects for it.
func MilesBetween(a, b Point) float64 {
	lat1, lat2 := rad(a.Lat), rad(b.Lat)
	dLat := rad(b.Lat - a.Lat)
	dLon := rad(b.Lon - a.Lon)

	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusMi * math.Asin(math.Sqrt(h))
}

// gridFactor converts straight-line distance into approximate street distance.
// 1.27 is the standard circuity factor for a dense orthogonal grid; Brooklyn's
// street plan is close enough to orthogonal that this beats reporting raw
// crow-flies distance to someone who has to walk it.
const gridFactor = 1.27

// walkMph is a brisk city walking pace.
const walkMph = 3.0

// WalkMinutes estimates walking time between two points, rounded to a minute.
func WalkMinutes(a, b Point) int {
	mi := MilesBetween(a, b) * gridFactor
	return int(math.Round(mi / walkMph * 60))
}

// MilesForWalkMinutes inverts WalkMinutes: the crow-flies radius that
// corresponds to a given walking time, for turning "within 20 minutes" into
// the distance filter the catalog understands.
func MilesForWalkMinutes(minutes int) float64 {
	return float64(minutes) / 60 * walkMph / gridFactor
}

// NearestStop returns the closest stop in any corridor.
func NearestStop(p Point) Stop {
	all := AllStops()
	best, bestD := all[0], math.Inf(1)
	for _, s := range all {
		if d := MilesBetween(p, s.Point); d < bestD {
			best, bestD = s, d
		}
	}
	return best
}

func rad(deg float64) float64 { return deg * math.Pi / 180 }
