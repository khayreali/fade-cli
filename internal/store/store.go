// Package store holds the per-user state: who you are, where you're walking
// from, and every cut you've logged. All of it is plain JSON under a config
// dir so it stays greppable and hand-editable.
package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"fadecli/internal/geo"
)

// DefaultCutInterval is how often a fade grows out for most people. Used only
// until there's enough history to measure the user's actual cadence.
const DefaultCutInterval = 21 * 24 * time.Hour

// Profile is the user's saved preferences.
type Profile struct {
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
	Email string `json:"email,omitempty"`
	// HomeStop anchors distance queries to a corridor stop, which is easier to
	// type than coordinates and is how people actually think about Brooklyn.
	HomeStop  string     `json:"home_stop,omitempty"`
	HomePoint *geo.Point `json:"home_point,omitempty"`
	MaxPrice  int        `json:"max_price,omitempty"`
	// IntervalDays overrides the learned cut cadence.
	IntervalDays int `json:"interval_days,omitempty"`
}

// Origin resolves the profile to a coordinate to measure from, preferring an
// explicit point over the stop anchor.
func (p Profile) Origin() (geo.Point, bool) {
	if p.HomePoint != nil {
		return *p.HomePoint, true
	}
	if s, ok := geo.StopByID(p.HomeStop); ok {
		return s.Point, true
	}
	return geo.Point{}, false
}

// Cut is one logged haircut.
type Cut struct {
	Date     time.Time `json:"date"`
	ShopID   string    `json:"shop_id"`
	ShopName string    `json:"shop_name"`
	Barber   string    `json:"barber,omitempty"`
	Service  string    `json:"service,omitempty"`
	Price    int       `json:"price,omitempty"`
	Tip      int       `json:"tip,omitempty"`
	Rating   int       `json:"rating,omitempty"` // 1-5, 0 = unrated
	Notes    string    `json:"notes,omitempty"`
}

// Total is what the visit actually cost.
func (c Cut) Total() int { return c.Price + c.Tip }

// State is everything persisted for a user.
type State struct {
	Profile Profile `json:"profile"`
	Cuts    []Cut   `json:"cuts"`

	dir string
}

// Dir returns the config directory, honouring FADE_HOME for testing and for
// users who keep dotfiles somewhere unusual.
func Dir() (string, error) {
	if d := os.Getenv("FADE_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "fade"), nil
}

// Load reads state, returning an empty state on first run rather than an error.
func Load() (*State, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	s := &State{dir: dir}

	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("parsing state.json: %w", err)
	}
	s.dir = dir
	// LastCut, Interval and DueIn all assume newest-first. This file is
	// documented as hand-editable, and a person listing cuts oldest-first --
	// the natural way to type them -- would otherwise get the wrong "last cut",
	// the wrong due date, and `again` rebooking the wrong shop.
	s.sortCuts()
	return s, nil
}

// sortCuts puts history newest-first, which every reader here relies on.
func (s *State) sortCuts() {
	sort.SliceStable(s.Cuts, func(i, j int) bool { return s.Cuts[i].Date.After(s.Cuts[j].Date) })
}

// LocalCatalogPath is where user-added or user-corrected shops live.
func (s *State) LocalCatalogPath() string { return filepath.Join(s.dir, "shops.local.json") }

// Save writes state atomically, so an interrupted write can't leave a user
// with a truncated cut history.
func (s *State) Save() error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(s.dir, "state.json")
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

// AddCut records a haircut and keeps history newest-first.
func (s *State) AddCut(c Cut) {
	s.Cuts = append(s.Cuts, c)
	s.sortCuts()
}

// LastCut returns the most recent logged cut.
func (s *State) LastCut() (Cut, bool) {
	if len(s.Cuts) == 0 {
		return Cut{}, false
	}
	return s.Cuts[0], true
}

// IntervalSource says where a cadence came from. A learned interval and a
// generic default render as the same number of weeks, so without this the tool
// claims to know your habits when it is really just guessing.
type IntervalSource int

const (
	IntervalDefault    IntervalSource = iota // not enough history; generic guess
	IntervalLearned                          // median of your own gaps
	IntervalConfigured                       // you set it explicitly
)

// Label describes the source in words, for screens that show the cadence.
func (s IntervalSource) Label() string {
	switch s {
	case IntervalConfigured:
		return "you set this"
	case IntervalLearned:
		return "learned from your cuts"
	default:
		return "default, not enough history yet"
	}
}

// IntervalWithSource returns the cadence and where it came from.
func (s *State) IntervalWithSource() (time.Duration, IntervalSource) {
	if s.Profile.IntervalDays > 0 {
		return time.Duration(s.Profile.IntervalDays) * 24 * time.Hour, IntervalConfigured
	}
	if d, ok := s.learnedInterval(); ok {
		return d, IntervalLearned
	}
	return DefaultCutInterval, IntervalDefault
}

// Interval is how often this user actually gets cut: the median gap between
// logged cuts once there are enough of them, otherwise the configured or
// default interval. Median rather than mean because one six-month lapse
// shouldn't convince the tool you're a twice-a-year person.
func (s *State) Interval() time.Duration {
	d, _ := s.IntervalWithSource()
	return d
}

// learnedInterval is the median gap between logged cuts, when there are enough
// of them to mean anything.
func (s *State) learnedInterval() (time.Duration, bool) {
	if len(s.Cuts) < 3 {
		return 0, false
	}
	gaps := make([]time.Duration, 0, len(s.Cuts)-1)
	for i := 0; i+1 < len(s.Cuts); i++ {
		if g := s.Cuts[i].Date.Sub(s.Cuts[i+1].Date); g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) == 0 {
		return 0, false
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	return gaps[len(gaps)/2], true
}

// DueIn reports time until the next cut is due; negative means overdue.
// The bool is false when there's no history to reason from.
func (s *State) DueIn(now time.Time) (time.Duration, bool) {
	last, ok := s.LastCut()
	if !ok {
		return 0, false
	}
	return last.Date.Add(s.Interval()).Sub(now), true
}

// Regulars ranks the shops the user returns to, most visits first.
func (s *State) Regulars() []ShopCount {
	counts := map[string]*ShopCount{}
	for _, c := range s.Cuts {
		key := c.ShopID
		if key == "" {
			key = c.ShopName
		}
		if _, ok := counts[key]; !ok {
			counts[key] = &ShopCount{ShopID: c.ShopID, ShopName: c.ShopName}
		}
		e := counts[key]
		e.Visits++
		e.Spent += c.Total()
		if c.Date.After(e.Last) {
			e.Last = c.Date
		}
	}
	out := make([]ShopCount, 0, len(counts))
	for _, e := range counts {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Visits != out[j].Visits {
			return out[i].Visits > out[j].Visits
		}
		return out[i].Last.After(out[j].Last)
	})
	return out
}

// ShopCount aggregates a user's history at one shop.
type ShopCount struct {
	ShopID   string
	ShopName string
	Visits   int
	Spent    int
	Last     time.Time
}
