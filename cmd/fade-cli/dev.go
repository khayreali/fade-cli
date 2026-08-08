package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/geo"
	"fadecli/internal/ui"
)

const devUsage = `fade-cli dev <task>

TASKS
  geocode   fill in missing coordinates from street addresses
  check     report gaps in the catalog

Run 'fade-cli dev <task> -h' for that task's flags.
`

func (a *app) dev(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, devUsage)
		return errors.New("name a task")
	}
	switch args[0] {
	// Asking for help is not an error, and every other command answers -h.
	case "-h", "--help", "help":
		fmt.Print(devUsage)
		return nil
	case "geocode":
		return a.devGeocode(args[1:])
	case "check":
		return a.devCheck(args[1:])
	default:
		return fmt.Errorf("unknown dev task %q", args[0])
	}
}

// nominatimRate is OSM's published usage limit: one request per second, with a
// real User-Agent. Expansion runs are one-off, so being polite costs nothing.
const nominatimRate = 1100 * time.Millisecond

// driftThreshold is how far a re-geocode may move a shop before it's worth
// flagging. A block or so of jitter between Nominatim runs is normal; a
// quarter mile means the stored point or the address is wrong.
const driftThreshold = 0.15

func (a *app) devGeocode(args []string) error {
	fs := flag.NewFlagSet("dev geocode", flag.ContinueOnError)
	var (
		file  = fs.String("file", "data/shops.json", "catalog file to update in place")
		force = fs.Bool("force", false, "re-geocode shops that already have coordinates")
		dry   = fs.Bool("dry-run", false, "show what would change without writing")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}

	raw, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", *file, err)
	}
	var cat catalog.Catalog
	if err := json.Unmarshal(raw, &cat); err != nil {
		return fmt.Errorf("parsing %s: %w", *file, err)
	}

	client := &http.Client{Timeout: 12 * time.Second}
	var resolved, failed, drifted int

	for i := range cat.Shops {
		s := &cat.Shops[i]
		if s.Located() && !*force {
			continue
		}
		if s.Address == "" {
			fmt.Printf("%s %s %s\n", ui.Yellow("skip"), s.ID, ui.Dim("no address"))
			continue
		}

		old, hadPoint := s.Point, s.Located()
		pt, err := geocode(client, s.Address)
		if err != nil {
			failed++
			fmt.Printf("%s %s %s\n", ui.Red("fail"), s.ID, ui.Dim(err.Error()))
			time.Sleep(nominatimRate)
			continue
		}

		stop := geo.NearestStop(pt)
		// With --force the shop already had coordinates, so the useful output
		// is what would change, not what the geocoder said. Reporting drift
		// turns this into a way to audit the catalog against its source.
		switch {
		case !hadPoint:
			fmt.Printf("%s %-38s %s\n", ui.Green(" ok "), s.ID,
				ui.Dim(fmt.Sprintf("%.5f,%.5f  near %s", pt.Lat, pt.Lon, stop.Name)))
		default:
			moved := geo.MilesBetween(old, pt)
			oldStop := geo.NearestStop(old)
			switch {
			case oldStop.ID != stop.ID:
				drifted++
				fmt.Printf("%s %-38s %s\n", ui.Red("MOVE"), s.ID,
					ui.Yellow(fmt.Sprintf("%.2f mi, %s -> %s", moved, oldStop.Name, stop.Name)))
			case moved > driftThreshold:
				drifted++
				fmt.Printf("%s %-38s %s\n", ui.Yellow("drift"), s.ID,
					ui.Dim(fmt.Sprintf("%.2f mi, still near %s", moved, stop.Name)))
			default:
				fmt.Printf("%s %-38s %s\n", ui.Green(" ok "), s.ID,
					ui.Dim(fmt.Sprintf("%.3f mi from stored", moved)))
			}
		}
		if !*dry {
			s.Point = pt
		}
		resolved++
		time.Sleep(nominatimRate)
	}

	fmt.Printf("\n%d resolved, %d failed", resolved, failed)
	if drifted > 0 {
		fmt.Printf(", %s", ui.Yellow(fmt.Sprintf("%d drifted", drifted)))
	}
	fmt.Println()
	if *dry || resolved == 0 {
		return nil
	}

	// Encode rather than Marshal so we can turn off HTML escaping: the default
	// mangles arrows and ampersands in shop and area names.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cat); err != nil {
		return err
	}
	if err := os.WriteFile(*file, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", *file, err)
	}
	fmt.Printf("wrote %s -- rebuild to embed the new coordinates\n", *file)
	return nil
}

func geocode(client *http.Client, address string) (geo.Point, error) {
	q := url.Values{}
	q.Set("q", address)
	q.Set("format", "jsonv2")
	q.Set("limit", "1")
	q.Set("countrycodes", "us")

	req, err := http.NewRequest(http.MethodGet, "https://nominatim.openstreetmap.org/search?"+q.Encode(), nil)
	if err != nil {
		return geo.Point{}, err
	}
	// Nominatim rejects requests without an identifying User-Agent.
	req.Header.Set("User-Agent", "fade-cli/"+version+" (haircut booking CLI)")

	resp, err := client.Do(req)
	if err != nil {
		return geo.Point{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return geo.Point{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var hits []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
		return geo.Point{}, err
	}
	if len(hits) == 0 {
		return geo.Point{}, errors.New("no match")
	}

	lat, err := strconv.ParseFloat(hits[0].Lat, 64)
	if err != nil {
		return geo.Point{}, err
	}
	lon, err := strconv.ParseFloat(hits[0].Lon, 64)
	if err != nil {
		return geo.Point{}, err
	}
	return geo.Point{Lat: lat, Lon: lon}, nil
}

// devCheck reports what the catalog is missing, which is the practical guide
// to what to research next when expanding to a new neighborhood.
func (a *app) devCheck(args []string) error {
	var (
		noCoords  []string
		noPrice   []string
		noContact []string
		byKind    = map[catalog.BookingKind]int{}
	)

	for _, s := range a.cat.Shops {
		byKind[s.Booking.Kind]++
		if !s.Located() {
			noCoords = append(noCoords, s.ID)
		}
		if s.PriceMin == 0 && s.PriceMax == 0 {
			noPrice = append(noPrice, s.ID)
		}
		if s.Phone == "" && s.Booking.URL == "" {
			noContact = append(noContact, s.ID)
		}
	}

	fmt.Println()
	fmt.Printf("%s  %s\n\n", ui.Bold(geo.AreaName()), ui.Dim(fmt.Sprintf("%d shops", len(a.cat.Shops))))

	t := ui.NewTable("booking kind", "shops")
	t.RightAlign(1)
	// Iterate what's actually in the data, not a hardcoded list: a new booking
	// kind should show up here without anyone remembering to add it.
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		t.Row(k, fmt.Sprint(byKind[catalog.BookingKind(k)]))
	}
	t.Render(os.Stdout)
	fmt.Println()

	report := func(label string, ids []string) {
		if len(ids) == 0 {
			fmt.Printf("  %s %s\n", ui.Green("ok  "), label)
			return
		}
		fmt.Printf("  %s %s %s\n", ui.Yellow("gap "), label, ui.Dim(fmt.Sprintf("(%d)", len(ids))))
		for _, id := range ids {
			fmt.Printf("       %s\n", ui.Dim(id))
		}
	}
	report("coordinates", noCoords)
	report("price range", noPrice)
	report("phone or link", noContact)
	fmt.Println()
	return nil
}
