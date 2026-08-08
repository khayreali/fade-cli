package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/provider"
	"fadecli/internal/ui"
)

func (a *app) slots(args []string) error {
	fs := flag.NewFlagSet("slots", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli slots [shop] [flags]

Check open appointment times. Name a shop, or use --near to sweep every shop
in range at once.

FLAGS
`)
		fs.PrintDefaults()
	}

	var (
		day    = fs.String("day", "today", "today, tomorrow, a weekday, or YYYY-MM-DD")
		near   = fs.String("near", "", "sweep shops around this stop instead of naming one")
		within = fs.Float64("within", 0.75, "with --near, max distance in miles")
		under  = fs.Int("under", 0, "with --near, max price")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}

	when, err := parseDay(*day, time.Now())
	if err != nil {
		return err
	}

	shops, label, err := a.slotTargets(fs.Args(), *near, *within, *under)
	if err != nil {
		return err
	}
	if len(shops) == 0 {
		return errors.New("no shops to check")
	}

	// One context for the whole sweep: if the user ctrl-Cs or a provider
	// stalls, every in-flight request gives up together.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	fmt.Println()
	fmt.Printf("%s  %s\n\n",
		ui.Bold(fmt.Sprintf("Open times %s", ui.RelDay(when, time.Now()))),
		ui.Dim(label))

	results := a.reg.AvailabilityAcross(ctx, shops, when)
	printSlots(results, when)
	return nil
}

// slotTargets resolves either a named shop or a --near sweep into the shop set
// to query.
func (a *app) slotTargets(nameArgs []string, near string, within float64, under int) ([]catalog.Shop, string, error) {
	if name := strings.Join(nameArgs, " "); name != "" {
		shop, err := a.mustResolve(name)
		if err != nil {
			return nil, "", err
		}
		return []catalog.Shop{shop}, shop.Address, nil
	}

	origin, originLabel, err := a.resolveOrigin(near)
	if err != nil {
		return nil, "", err
	}
	if origin == nil {
		return nil, "", errors.New("name a shop, or pass --near <stop> (see `fade-cli stops`)")
	}

	results := a.cat.Find(catalog.Query{Origin: origin, MaxMiles: within, MaxPrice: under})
	shops := make([]catalog.Shop, 0, len(results))
	for _, r := range results {
		shops = append(shops, r.Shop)
	}
	return shops, fmt.Sprintf("%d shops within %.2g mi of %s", len(shops), within, originLabel), nil
}

func printSlots(results []provider.ShopSlots, when time.Time) {
	var (
		live      int
		handoffs  []provider.ShopSlots
		failed    []provider.ShopSlots
		needsCred bool
	)

	for _, r := range results {
		if r.Live() {
			live++
			printShopSlots(r)
			continue
		}
		switch {
		case errors.Is(r.Err, provider.ErrNeedsCredentials):
			needsCred = true
		case r.Err != nil && !errors.Is(r.Err, provider.ErrNoLiveAvailability):
			// A bad token, a dead network or a changed API used to render
			// identically to "this shop has no live availability", so a broken
			// setup looked exactly like a working one.
			failed = append(failed, r)
		}
		handoffs = append(handoffs, r)
	}

	if live == 0 {
		fmt.Println(ui.Dim("  No live availability for these shops yet."))
		fmt.Println()
	}

	if len(handoffs) > 0 {
		fmt.Println(ui.Bold("  Book these directly"))
		t := ui.NewTable("shop", "price", "rating", "when", "how").Indent("  ")
		t.RightAlign(1)
		now := time.Now()
		for _, r := range handoffs {
			t.Row(
				r.Shop.Name,
				priceCell(r.Shop),
				ui.Stars(r.Shop.Rating, r.Shop.Reviews),
				openCellFor(r.Shop, when, now),
				ui.Dim(handoffLabel(r.Shop)),
			)
		}
		t.Render(os.Stdout)
		fmt.Println()
		ui.Hint("fade-cli book <shop> opens the booking page or dials the shop")
	}

	if needsCred {
		ui.Hint("some shops support live times but need API access -- see README 'Live availability'")
	}
	if len(failed) > 0 {
		fmt.Println()
		ui.Warn("%s could not be checked:", plural(len(failed), "shop"))
		for _, r := range failed {
			fmt.Printf("    %s %s\n", r.Shop.Name, ui.Dim(r.Err.Error()))
		}
	}
}

func printShopSlots(r provider.ShopSlots) {
	fmt.Printf("  %s  %s\n", ui.Bold(r.Shop.Name), ui.Dim(r.Shop.Address))

	var times []string
	for _, s := range r.Slots {
		times = append(times, s.Start.Format("3:04pm"))
	}
	// Wrap the time list rather than one-per-line: a day of slots is a shape
	// you scan, not a list you read.
	for _, line := range wrap(times, 8) {
		fmt.Println("    " + ui.Green(line))
	}
	fmt.Println()
}

func wrap(items []string, per int) []string {
	var out []string
	for i := 0; i < len(items); i += per {
		end := i + per
		if end > len(items) {
			end = len(items)
		}
		out = append(out, strings.Join(items[i:end], "  "))
	}
	return out
}

func handoffLabel(s catalog.Shop) string {
	switch s.Booking.Kind {
	case catalog.KindPhone:
		if s.Phone != "" {
			return provider.PrettyPhone(s.Phone)
		}
		return "call"
	case catalog.KindBooksy, catalog.KindFresha, catalog.KindSquare, catalog.KindVagaro:
		return string(s.Booking.Kind)
	default:
		return "website"
	}
}

// openCellFor answers for the day the user asked about. For today that's the
// live status; for any other date it's that day's opening hours, since "open
// until 8pm" is meaningless about next Wednesday.
func openCellFor(s catalog.Shop, day, now time.Time) string {
	if !s.Hours.Known() {
		return ui.Dim("—")
	}
	if catalog.SameShopDay(day, now) {
		return openCell(s, now)
	}
	label, trades := s.Hours.OnDay(day)
	if !trades {
		return ui.Yellow("closed " + day.Format("Mon"))
	}
	return ui.Green(label)
}

// openCell says whether a shop is open right now. `slots` exists to answer
// "when can I get in", so listing a shop that shut three hours ago without
// saying so is the one thing this screen must not do.
func openCell(s catalog.Shop, now time.Time) string {
	label := s.Hours.StatusLabel(now)
	switch st, _ := s.Hours.OpenAt(now); st {
	case catalog.StatusOpen:
		return ui.Green(label)
	case catalog.StatusClosed:
		return ui.Yellow(label)
	default:
		return ui.Dim("—")
	}
}

// parseDay accepts the words people actually type plus ISO dates.
func parseDay(s string, now time.Time) (time.Time, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	midnight := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}

	switch s {
	case "", "today", "tod":
		return midnight(now), nil
	case "tomorrow", "tmr", "tom":
		return midnight(now.AddDate(0, 0, 1)), nil
	}

	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "sun": time.Sunday,
		"monday": time.Monday, "mon": time.Monday,
		"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
		"wednesday": time.Wednesday, "wed": time.Wednesday,
		"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday,
		"friday": time.Friday, "fri": time.Friday,
		"saturday": time.Saturday, "sat": time.Saturday,
	}
	if wd, ok := weekdays[s]; ok {
		// Always look forward: "friday" on a Friday means next Friday, because
		// if you meant today you'd have said today.
		delta := (int(wd) - int(now.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7
		}
		return midnight(now.AddDate(0, 0, delta)), nil
	}

	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("can't read %q as a day -- try today, tomorrow, fri, or 2026-08-14", s)
}

// mustResolve turns user input into one shop, printing the candidates when the
// input is ambiguous rather than silently picking one.
func (a *app) mustResolve(q string) (catalog.Shop, error) {
	shop, candidates, err := a.cat.Resolve(q)
	if err == nil {
		return shop, nil
	}
	if len(candidates) > 0 {
		fmt.Fprintln(os.Stderr, ui.Yellow(err.Error())+":")
		for _, c := range candidates {
			fmt.Fprintf(os.Stderr, "  %-34s %s\n", c.ID, ui.Dim(c.Address))
		}
		return catalog.Shop{}, errors.New("be more specific, or use the id")
	}
	return catalog.Shop{}, err
}
