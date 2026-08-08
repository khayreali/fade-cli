// Package catalog is the shop directory: the embedded seed plus any local
// additions the user has made, with the query surface `fade-cli find` sits on.
package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"fadecli/data"
	"fadecli/internal/geo"
)

// BookingKind identifies which provider handles a shop. Adding a kind here is
// the only coupling between the catalog and the provider package; providers
// register themselves against these strings.
type BookingKind string

const (
	KindLink   BookingKind = "link"   // hand off to a website
	KindPhone  BookingKind = "phone"  // hand off to a phone call
	KindBooksy BookingKind = "booksy" // Booksy-hosted
	KindFresha BookingKind = "fresha" // Fresha-hosted
	KindSquare BookingKind = "square" // Square Appointments
	KindVagaro BookingKind = "vagaro" // Vagaro-hosted
)

// Booking describes how to reach a shop's scheduling system.
type Booking struct {
	Kind BookingKind `json:"kind"`
	// ID is the provider-native identifier: a Booksy numeric business id, a
	// Fresha venue slug, a Square location id.
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
}

// Shop is one bookable entity. Note that on Booksy an individual barber is
// often their own bookable business at a shared address, so several Shops can
// share a Venue. `fade-cli find` collapses them unless asked not to.
type Shop struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Venue   string `json:"venue,omitempty"`
	Address string `json:"address"`
	// GeocodeAs overrides Address when resolving coordinates. Queens house
	// numbers are hyphenated ("66-24 Forest Ave") but OSM is inconsistent about
	// which form it indexes, so a handful of shops only match one spelling.
	// The displayed address stays canonical.
	GeocodeAs string    `json:"geocode_as,omitempty"`
	Point     geo.Point `json:"point"`
	Phone     string    `json:"phone,omitempty"`
	Rating    float64   `json:"rating,omitempty"`
	Reviews   int       `json:"reviews,omitempty"`
	// RatingSrc names where Rating came from. Booksy, Fresha and Google score
	// different populations on different scales, so a bare number across
	// sources invites a comparison it can't support.
	RatingSrc string   `json:"rating_src,omitempty"`
	PriceMin  int      `json:"price_min,omitempty"`
	PriceMax  int      `json:"price_max,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	// Hours is nil when nobody has looked them up, which is distinct from
	// closed -- see the Hours doc comment.
	Hours   Hours   `json:"hours,omitempty"`
	Booking Booking `json:"booking"`
}

// Located reports whether the shop has usable coordinates. Unresolved shops
// still show up in listings; they just can't be distance-sorted.
func (s Shop) Located() bool { return s.Point.Lat != 0 || s.Point.Lon != 0 }

// NearestStop is the corridor stop this shop hangs off of.
func (s Shop) NearestStop() (geo.Stop, bool) {
	if !s.Located() {
		return geo.Stop{}, false
	}
	return geo.NearestStop(s.Point), true
}

// PriceLabel renders the price range compactly, or "?" when unknown. We never
// invent a price: an unknown price is worth showing as unknown.
func (s Shop) PriceLabel() string {
	switch {
	case s.PriceMin == 0 && s.PriceMax == 0:
		return "—"
	case s.PriceMin == s.PriceMax:
		return fmt.Sprintf("$%d", s.PriceMin)
	case s.PriceMax == 0:
		return fmt.Sprintf("$%d+", s.PriceMin)
	default:
		return fmt.Sprintf("$%d-%d", s.PriceMin, s.PriceMax)
	}
}

// Catalog is a loaded shop directory.
type Catalog struct {
	Version  int    `json:"version"`
	Area     string `json:"area"`
	AreaName string `json:"area_name"`
	Shops    []Shop `json:"shops"`
}

// Load returns the embedded seed merged with the user's local catalog, if one
// exists. Local entries win on id collision, so a user can correct a price or
// a phone number without waiting on an upstream release.
func Load(localPath string) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(data.SeedJSON, &c); err != nil {
		return nil, fmt.Errorf("parsing embedded catalog: %w", err)
	}

	if localPath != "" {
		local, err := loadFile(localPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if local != nil {
			c.merge(local.Shops)
		}
	}
	return &c, nil
}

func loadFile(path string) (*Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

func (c *Catalog) merge(shops []Shop) {
	idx := make(map[string]int, len(c.Shops))
	for i, s := range c.Shops {
		idx[s.ID] = i
	}
	for _, s := range shops {
		if i, ok := idx[s.ID]; ok {
			c.Shops[i] = s
			continue
		}
		idx[s.ID] = len(c.Shops)
		c.Shops = append(c.Shops, s)
	}
}

// Get finds a shop by exact id.
func (c *Catalog) Get(id string) (Shop, bool) {
	for _, s := range c.Shops {
		if s.ID == id {
			return s, true
		}
	}
	return Shop{}, false
}

// Resolve finds a shop by id, then by unambiguous name prefix or substring, so
// `fade-cli book cabello` works without anyone memorising ids. It reports every
// candidate on ambiguity rather than guessing, since the next step spends money.
func (c *Catalog) Resolve(q string) (Shop, []Shop, error) {
	if s, ok := c.Get(q); ok {
		return s, nil, nil
	}
	needle := strings.ToLower(strings.TrimSpace(q))
	if needle == "" {
		return Shop{}, nil, fmt.Errorf("no shop given")
	}

	var prefix, contains []Shop
	for _, s := range c.Shops {
		name := strings.ToLower(s.Name)
		switch {
		case strings.HasPrefix(name, needle) || strings.HasPrefix(s.ID, needle):
			prefix = append(prefix, s)
		case strings.Contains(name, needle) || strings.Contains(s.ID, needle):
			contains = append(contains, s)
		}
	}

	hits := prefix
	if len(hits) == 0 {
		hits = contains
	}
	switch len(hits) {
	case 0:
		return Shop{}, nil, fmt.Errorf("no shop matching %q", q)
	case 1:
		return hits[0], nil, nil
	default:
		return Shop{}, hits, fmt.Errorf("%q matches %d shops", q, len(hits))
	}
}

// Query is the filter set behind `fade-cli find`.
type Query struct {
	Origin    *geo.Point // nil means "don't distance-filter"
	MaxMiles  float64
	MaxPrice  int
	MinRating float64
	Kinds     []BookingKind
	Corridor  string // limit to one corridor by id
	// OpenAt keeps only shops open at this instant. Zero disables it. Shops
	// with unknown hours are excluded rather than assumed open.
	OpenAt     time.Time
	FromStop   string // inclusive corridor range, by stop id
	ToStop     string
	Text       string
	Sort       SortBy
	CollapseBy string // "venue" to show one row per address
}

// Result pairs a shop with the distance context for the current query.
type Result struct {
	Shop      Shop
	Miles     float64
	WalkMin   int
	Stop      geo.Stop
	HasOrigin bool
	// Alongside holds other bookable barbers at the same venue when collapsed.
	Alongside []Shop
}

// Find applies the query and returns results sorted by distance when an origin
// is known, otherwise by rating then review count.
func (c *Catalog) Find(q Query) []Result {
	var out []Result

	corridorID, lo, hi := c.stopRange(q)

	for _, s := range c.Shops {
		if !matchesText(s, q.Text) {
			continue
		}
		if q.MinRating > 0 && s.Rating < q.MinRating {
			continue
		}
		// Filter on the low end of the range: a shop whose cheapest cut is
		// under the cap is still worth showing to a price-sensitive user.
		if q.MaxPrice > 0 && s.PriceMin > 0 && s.PriceMin > q.MaxPrice {
			continue
		}
		if len(q.Kinds) > 0 && !containsKind(q.Kinds, s.Booking.Kind) {
			continue
		}
		if !q.OpenAt.IsZero() {
			if st, _ := s.Hours.OpenAt(q.OpenAt); st != StatusOpen {
				continue
			}
		}

		r := Result{Shop: s}
		stop, placed := s.NearestStop()
		if placed {
			r.Stop = stop
		}
		// A shop with no coordinates can't be placed on a corridor, so any
		// corridor or stop-range filter has to exclude it.
		if q.Corridor != "" && (!placed || stop.Corridor != q.Corridor) {
			continue
		}
		if corridorID != "" {
			if !placed || stop.Corridor != corridorID || stop.Order < lo || stop.Order > hi {
				continue
			}
		}

		if q.Origin != nil && s.Located() {
			r.HasOrigin = true
			r.Miles = geo.MilesBetween(*q.Origin, s.Point)
			r.WalkMin = geo.WalkMinutes(*q.Origin, s.Point)
			if q.MaxMiles > 0 && r.Miles > q.MaxMiles {
				continue
			}
		} else if q.MaxMiles > 0 {
			continue
		}

		out = append(out, r)
	}

	if q.CollapseBy == "venue" {
		out = collapseByVenue(out)
	}
	sortResults(out, q.Sort, q.Origin != nil)
	return out
}

// stopRange turns --from/--to stop ids into an inclusive Order window within a
// single corridor. Order only compares within one corridor, so the from-stop
// (or the to-stop, if that's the only one given) picks which. A missing bound
// extends to that corridor's end; both missing disables the filter.
func (c *Catalog) stopRange(q Query) (corridorID string, lo, hi int) {
	from, hasFrom := geo.StopByID(q.FromStop)
	to, hasTo := geo.StopByID(q.ToStop)
	if !hasFrom && !hasTo {
		return "", 0, 0
	}

	anchor := from
	if !hasFrom {
		anchor = to
	}
	cor, ok := geo.CorridorByID(anchor.Corridor)
	if !ok {
		return "", 0, 0
	}

	lo, hi = 1, len(cor.Stops)
	if hasFrom && from.Corridor == cor.ID {
		lo = from.Order
	}
	if hasTo && to.Corridor == cor.ID {
		hi = to.Order
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	return cor.ID, lo, hi
}

// collapseByVenue keeps the best-rated bookable entity per address and files
// the rest under Alongside, so five Cutmaster barbers read as one shop.
func collapseByVenue(in []Result) []Result {
	byVenue := map[string]int{}
	var out []Result
	for _, r := range in {
		key := r.Shop.Venue
		if key == "" {
			key = r.Shop.Address
		}
		i, seen := byVenue[key]
		if !seen {
			byVenue[key] = len(out)
			out = append(out, r)
			continue
		}
		if better(r.Shop, out[i].Shop) {
			out[i].Alongside = append(out[i].Alongside, out[i].Shop)
			out[i].Shop = r.Shop
		} else {
			out[i].Alongside = append(out[i].Alongside, r.Shop)
		}
	}
	return out
}

func better(a, b Shop) bool {
	if a.Rating != b.Rating {
		return a.Rating > b.Rating
	}
	return a.Reviews > b.Reviews
}

// SortBy names an ordering for results.
type SortBy string

const (
	SortNearest   SortBy = ""         // default: closest first
	SortCheapest  SortBy = "cheapest" // lowest starting price first
	SortBestRated SortBy = "rated"    // highest rating first
)

// Label is the human phrase for a sort, for headers.
func (s SortBy) Label() string {
	switch s {
	case SortCheapest:
		return "cheapest first"
	case SortBestRated:
		return "best rated first"
	default:
		return "sorted by walk time"
	}
}

// Next cycles through the sorts, for a UI that toggles with one key.
func (s SortBy) Next() SortBy {
	switch s {
	case SortNearest:
		return SortCheapest
	case SortCheapest:
		return SortBestRated
	default:
		return SortNearest
	}
}

func sortResults(rs []Result, by SortBy, byDistance bool) {
	// A shop with no price or no rating has nothing to rank on, and must not
	// win a sort by looking like a zero. Unknowns go last in every ordering.
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]

		switch by {
		case SortCheapest:
			ap, bp := a.Shop.PriceMin, b.Shop.PriceMin
			if (ap == 0) != (bp == 0) {
				return bp == 0
			}
			if ap != bp && ap != 0 {
				return ap < bp
			}
		case SortBestRated:
			ar, br := a.Shop.Rating, b.Shop.Rating
			if (ar == 0) != (br == 0) {
				return br == 0
			}
			if ar != br {
				return ar > br
			}
			if a.Shop.Reviews != b.Shop.Reviews {
				return a.Shop.Reviews > b.Shop.Reviews
			}
		}

		// Distance is the tie-breaker for every sort: given two equally cheap
		// or equally rated shops, you want the nearer one.
		if byDistance && a.HasOrigin && b.HasOrigin && a.Miles != b.Miles {
			return a.Miles < b.Miles
		}
		if byDistance && a.HasOrigin != b.HasOrigin {
			return a.HasOrigin
		}
		if a.Shop.Rating != b.Shop.Rating {
			return a.Shop.Rating > b.Shop.Rating
		}
		return a.Shop.Reviews > b.Shop.Reviews
	})
}

func matchesText(s Shop, text string) bool {
	if text == "" {
		return true
	}
	t := strings.ToLower(text)
	return strings.Contains(strings.ToLower(s.Name), t) ||
		strings.Contains(strings.ToLower(s.Address), t) ||
		strings.Contains(strings.ToLower(strings.Join(s.Tags, " ")), t)
}

func containsKind(kinds []BookingKind, k BookingKind) bool {
	for _, want := range kinds {
		if want == k {
			return true
		}
	}
	return false
}
