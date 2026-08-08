package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"fadecli/internal/catalog"
	"fadecli/internal/geo"
	"fadecli/internal/ui"
)

func (a *app) find(args []string) error {
	fs := flag.NewFlagSet("find", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli find [text] [flags]

Search the shop directory. With no flags, uses your saved home stop.

FLAGS
`)
		fs.PrintDefaults()
	}

	var (
		near      = fs.String("near", "", "measure from this stop id (see `fade-cli stops`)")
		within    = fs.Float64("within", 0, "max distance in miles")
		under     = fs.Int("under", 0, "max price for the cheapest cut, in dollars")
		minRating = fs.Float64("min-rating", 0, "minimum star rating")
		kind      = fs.String("kind", "", "booking kind: booksy, fresha, square, vagaro, link, phone")
		from      = fs.String("from", "", "only shops from this stop onward")
		to        = fs.String("to", "", "only shops up to this stop")
		sortBy    = fs.String("sort", "", "order: nearest (default), cheapest, rated")
		collapse  = fs.Bool("collapse", false, "one row per address, not per bookable barber")
		limit     = fs.Int("limit", 20, "max rows to show (0 for all)")
		asJSON    = fs.Bool("json", false, "emit JSON instead of a table")
		noColor   = fs.Bool("no-color", false, "disable color")
	)
	if err := parse(fs, args); err != nil {
		return nil // flag package already printed the problem
	}
	if *noColor {
		ui.SetColor(false)
	}

	q := catalog.Query{
		Text:      strings.Join(fs.Args(), " "),
		MaxMiles:  *within,
		MaxPrice:  *under,
		MinRating: *minRating,
		FromStop:  *from,
		ToStop:    *to,
	}
	if *collapse {
		q.CollapseBy = "venue"
	}
	switch *sortBy {
	case "", "nearest":
	case "cheapest":
		q.Sort = catalog.SortCheapest
	case "rated":
		q.Sort = catalog.SortBestRated
	default:
		return fmt.Errorf("unknown --sort %q -- use nearest, cheapest or rated", *sortBy)
	}
	if *kind != "" {
		q.Kinds = []catalog.BookingKind{catalog.BookingKind(*kind)}
	}
	if q.MaxPrice == 0 {
		q.MaxPrice = a.state.Profile.MaxPrice
	}

	// Order only compares within a corridor, so a range spanning two of them
	// is meaningless -- say so rather than silently returning one corridor.
	if *from != "" && *to != "" {
		f, fok := geo.StopByID(*from)
		t, tok := geo.StopByID(*to)
		if fok && tok && f.Corridor != t.Corridor {
			return fmt.Errorf("%s and %s are on different corridors -- a range needs both on one", f.Name, t.Name)
		}
	}

	origin, originLabel, err := a.resolveOrigin(*near)
	if err != nil {
		return err
	}
	q.Origin = origin

	results := a.cat.Find(q)

	// JSON is machine output, so the default row cap must not silently
	// truncate it -- but an explicitly typed --limit is an instruction.
	typed := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { typed[f.Name] = true })
	if *asJSON {
		if typed["limit"] && *limit > 0 && len(results) > *limit {
			results = results[:*limit]
		}
		return json.NewEncoder(os.Stdout).Encode(results)
	}

	if len(results) == 0 {
		fmt.Println(ui.Dim("No shops match. Try widening --within or raising --under."))
		return nil
	}

	shown := results
	if *limit > 0 && len(shown) > *limit {
		shown = shown[:*limit]
	}

	a.printHeader(len(results), len(shown), originLabel)
	printResults(shown, origin != nil)

	if len(shown) < len(results) {
		ui.Hint("%d more -- pass --limit 0 to see them all", len(results)-len(shown))
	}
	if origin == nil {
		ui.Hint("set a home stop for walk times: fade-cli me --set-home graham")
	}
	return nil
}

// resolveOrigin picks the point to measure from: an explicit --near, else the
// saved profile. Returns nil when neither is set, which disables distance.
func (a *app) resolveOrigin(near string) (*geo.Point, string, error) {
	if near != "" {
		s, ok := geo.StopByID(near)
		if !ok {
			return nil, "", fmt.Errorf("unknown stop %q -- run `fade-cli stops` to see them", near)
		}
		return &s.Point, s.Name, nil
	}
	if p, ok := a.state.Profile.Origin(); ok {
		label := "your home"
		if s, ok := geo.StopByID(a.state.Profile.HomeStop); ok {
			label = s.Name
		}
		return &p, label, nil
	}
	return nil, "", nil
}

func (a *app) printHeader(total, shown int, originLabel string) {
	head := fmt.Sprintf("%s  %s", ui.Bold(geo.AreaName()), ui.Dim(fmt.Sprintf("%d shops", total)))
	if originLabel != "" {
		head += ui.Dim(" from " + originLabel)
	}
	fmt.Println()
	fmt.Println(head)
	fmt.Println()
}

func printResults(results []catalog.Result, withDistance bool) {
	t := ui.NewTable("shop", "walk", "price", "rating", "book", "at")
	t.RightAlign(1, 2)

	for _, r := range results {
		walk := ui.Dim("—")
		if r.HasOrigin {
			walk = fmt.Sprintf("%d min", r.WalkMin)
		}

		name := r.Shop.Name
		if n := len(r.Alongside); n > 0 {
			name += ui.Dim(fmt.Sprintf(" +%d", n))
		}

		stop := ""
		if r.Stop.Name != "" {
			stop = ui.Dim(r.Stop.Name)
		}

		t.Row(
			name,
			walk,
			priceCell(r.Shop),
			ui.Stars(r.Shop.Rating, r.Shop.Reviews),
			kindCell(r.Shop.Booking.Kind),
			stop,
		)
	}
	t.Render(os.Stdout)
	fmt.Println()
}

// priceCell highlights genuinely cheap cuts, since price is the filter people
// reach for most and the eye should land on it.
func priceCell(s catalog.Shop) string {
	label := s.PriceLabel()
	switch {
	case s.PriceMin == 0:
		return ui.Dim(label)
	case s.PriceMin <= 30:
		return ui.Green(label)
	case s.PriceMin >= 80:
		return ui.Yellow(label)
	default:
		return label
	}
}

// kindCell colors by whether the kind can ever give live times, so the user
// can see at a glance which rows `fade-cli slots` will be useful on.
func kindCell(k catalog.BookingKind) string {
	switch k {
	case catalog.KindBooksy, catalog.KindSquare:
		return ui.Cyan(string(k))
	case catalog.KindPhone:
		return ui.Dim("call")
	case catalog.KindFresha, catalog.KindLink:
		return ui.Dim(string(k))
	default:
		return string(k)
	}
}
