package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"fadecli/data"
	"fadecli/internal/catalog"
	"fadecli/internal/geo"
	"fadecli/internal/ui"
)

const devUsage = `fade-cli dev <task>

TASKS
  geocode   fill in missing coordinates from street addresses
  check     report gaps in the catalog
  links     check that every booking URL still resolves

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
	case "links":
		return a.devLinks(args[1:])
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
	var resolved, failed, drifted, approx int

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
		query := s.Address
		if s.GeocodeAs != "" {
			query = s.GeocodeAs
		}
		pt, exact, err := geocode(client, query)
		if err != nil {
			failed++
			fmt.Printf("%s %s %s\n", ui.Red("fail"), s.ID, ui.Dim(err.Error()))
			time.Sleep(nominatimRate)
			continue
		}

		if !exact {
			approx++
		}
		stop := geo.NearestStop(pt)
		// With --force the shop already had coordinates, so the useful output
		// is what would change, not what the geocoder said. Reporting drift
		// turns this into a way to audit the catalog against its source.
		switch {
		case !exact:
			fmt.Printf("%s %-38s %s\n", ui.Yellow("approx"), s.ID,
				ui.Yellow("no house number matched -- street-level only"))
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
	if approx > 0 {
		fmt.Printf(", %s", ui.Yellow(fmt.Sprintf("%d street-level only", approx)))
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

// geocode resolves an address. exact reports whether Nominatim matched the
// house number; when it can't, it silently returns a point on the street
// instead, which is both imprecise and unstable between runs -- two shops five
// blocks apart came back 15 m apart that way.
func geocode(client *http.Client, address string) (pt geo.Point, exact bool, err error) {
	q := url.Values{}
	q.Set("q", address)
	q.Set("format", "jsonv2")
	q.Set("limit", "1")
	q.Set("countrycodes", "us")
	q.Set("addressdetails", "1")

	req, err := http.NewRequest(http.MethodGet, "https://nominatim.openstreetmap.org/search?"+q.Encode(), nil)
	if err != nil {
		return geo.Point{}, false, err
	}
	// Nominatim rejects requests without an identifying User-Agent.
	req.Header.Set("User-Agent", "fade-cli/"+version+" (haircut booking CLI)")

	resp, err := client.Do(req)
	if err != nil {
		return geo.Point{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return geo.Point{}, false, fmt.Errorf("status %d", resp.StatusCode)
	}

	var hits []struct {
		Lat     string `json:"lat"`
		Lon     string `json:"lon"`
		Address struct {
			HouseNumber string `json:"house_number"`
		} `json:"address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
		return geo.Point{}, false, err
	}
	if len(hits) == 0 {
		return geo.Point{}, false, errors.New("no match")
	}

	lat, err := strconv.ParseFloat(hits[0].Lat, 64)
	if err != nil {
		return geo.Point{}, false, err
	}
	lon, err := strconv.ParseFloat(hits[0].Lon, 64)
	if err != nil {
		return geo.Point{}, false, err
	}
	return geo.Point{Lat: lat, Lon: lon}, hits[0].Address.HouseNumber != "", nil
}

// devLinks verifies every booking URL still resolves. A dead link is the worst
// kind of catalog rot: the shop looks bookable right up until the browser
// opens on nothing.
func (a *app) devLinks(args []string) error {
	fs := flag.NewFlagSet("dev links", flag.ContinueOnError)
	workers := fs.Int("workers", 8, "how many URLs to check at once")
	if err := parse(fs, args); err != nil {
		return nil
	}

	type target struct{ id, url string }
	var targets []target
	for _, s := range a.cat.Shops {
		if u := s.Booking.URL; u != "" {
			targets = append(targets, target{s.ID, u})
		}
	}

	fmt.Printf("\nchecking %s\n\n", plural(len(targets), "booking link"))

	client := &http.Client{Timeout: 15 * time.Second}
	sem := make(chan struct{}, max(1, *workers))
	var mu sync.Mutex
	var broken, blocked int

	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Add(1)
		go func(t target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// HEAD first; some booking hosts refuse it, so fall back to GET
			// rather than reporting a live shop as dead.
			status, err := probe(client, http.MethodHead, t.url)
			if err != nil || status >= 400 {
				status, err = probe(client, http.MethodGet, t.url)
			}

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				broken++
				fmt.Printf("%s   %-34s %s\n", ui.Red("dead"), t.id, ui.Dim(err.Error()))
			case isBotBlock(status):
				// A booking host refusing scripted traffic tells us nothing
				// about whether a person can book there. Report it, don't
				// count it as broken.
				blocked++
				fmt.Printf("%s %-34s %s\n", ui.Yellow("blocked"), t.id,
					ui.Dim(fmt.Sprintf("HTTP %d -- check by hand", status)))
			case status >= 400:
				broken++
				fmt.Printf("%s     %-34s %s\n", ui.Red(fmt.Sprint(status)), t.id, ui.Dim(t.url))
			}
		}(t)
	}
	wg.Wait()

	fmt.Println()
	switch {
	case broken > 0:
		fmt.Printf("%s\n", ui.Red(fmt.Sprintf("%d of %d unreachable", broken, len(targets))))
	default:
		fmt.Printf("%s %d links resolve", ui.Green("ok"), len(targets)-blocked)
		if blocked > 0 {
			fmt.Printf(", %s", ui.Yellow(fmt.Sprintf("%d blocked to scripts", blocked)))
		}
		fmt.Println()
	}
	fmt.Println()
	return nil
}

// isBotBlock reports statuses that mean "not to a script", not "not there".
func isBotBlock(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden,
		http.StatusMethodNotAllowed, http.StatusTooManyRequests:
		return true
	}
	return false
}

func probe(client *http.Client, method, url string) (int, error) {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "fade-cli/"+version+" (link check)")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// devCheck reports what the catalog is missing, which is the practical guide
// to what to research next when expanding to a new neighborhood.
func (a *app) devCheck(args []string) error {
	var (
		noCoords  []string
		noPrice   []string
		noHours   []string
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
		if !s.Hours.Known() {
			noHours = append(noHours, s.ID)
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

	total := len(a.cat.Shops)
	report := func(label string, ids []string) {
		if len(ids) == 0 {
			fmt.Printf("  %s %s\n", ui.Green("ok  "), label)
			return
		}
		fmt.Printf("  %s %-14s %s\n", ui.Yellow("gap "), label,
			ui.Dim(fmt.Sprintf("%d of %d missing", len(ids), total)))
		// Listing sixty ids buries the summary; a sample is enough to start on.
		for i, id := range ids {
			if i == 6 {
				fmt.Printf("       %s\n", ui.Dim(fmt.Sprintf("... and %d more", len(ids)-i)))
				break
			}
			fmt.Printf("       %s\n", ui.Dim(id))
		}
	}
	report("coordinates", noCoords)
	report("price range", noPrice)
	report("opening hours", noHours)
	report("phone or link", noContact)

	a.reportStaleSeed()
	fmt.Println()
	return nil
}

// reportStaleSeed warns when data/shops.json has been edited since this binary
// was built. The catalog is embedded at compile time, so every command reads
// the seed as it was at `make build` -- editing the JSON and then running
// `dev check` otherwise reports on data that is silently out of date.
func (a *app) reportStaleSeed() {
	onDisk, err := os.ReadFile("data/shops.json")
	if err != nil {
		return // not in the repo root; nothing to compare against
	}
	if sha256.Sum256(onDisk) == sha256.Sum256(data.SeedJSON) {
		return
	}
	fmt.Printf("\n  %s data/shops.json differs from the embedded catalog\n",
		ui.Yellow("stale"))
	fmt.Printf("        %s\n", ui.Dim("run `make build` to embed your edits"))
}
