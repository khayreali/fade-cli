package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/store"
	"fadecli/internal/ui"
)

func (a *app) log(args []string) error {
	if len(args) > 0 && args[0] == "add" {
		return a.logAdd(args[1:])
	}
	return a.logList(args)
}

func (a *app) logAdd(args []string) error {
	fs := flag.NewFlagSet("log add", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli log add --shop <shop> [flags]

Record a haircut.

FLAGS
`)
		fs.PrintDefaults()
	}
	var (
		shopArg = fs.String("shop", "", "shop name or id")
		barber  = fs.String("barber", "", "who cut it")
		service = fs.String("service", "", "what you got")
		price   = fs.Int("price", 0, "price in dollars")
		tip     = fs.Int("tip", 0, "tip in dollars")
		rating  = fs.Int("rating", 0, "how it came out, 1-5")
		notes   = fs.String("notes", "", "what to ask for next time")
		date    = fs.String("date", "today", "today, yesterday, or YYYY-MM-DD")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}
	if *shopArg == "" {
		fs.Usage()
		return errors.New("--shop is required")
	}
	if *rating < 0 || *rating > 5 {
		return errors.New("--rating must be between 1 and 5")
	}

	when, err := parseLogDate(*date, time.Now())
	if err != nil {
		return err
	}

	cut := store.Cut{
		Date:    when,
		Barber:  *barber,
		Service: *service,
		Price:   *price,
		Tip:     *tip,
		Rating:  *rating,
		Notes:   *notes,
	}

	// A shop that isn't in the catalog is still worth logging -- you got the
	// haircut either way. Record the raw name and move on.
	if shop, _, err := a.cat.Resolve(*shopArg); err == nil {
		cut.ShopID, cut.ShopName = shop.ID, shop.Name
	} else {
		cut.ShopName = *shopArg
		ui.Hint("%q isn't in the directory -- logged by name", *shopArg)
	}

	a.state.AddCut(cut)
	if err := a.state.Save(); err != nil {
		return fmt.Errorf("saving: %w", err)
	}

	fmt.Printf("%s %s at %s", ui.Green("logged"), ui.RelDay(when, time.Now()), ui.Bold(cut.ShopName))
	if cut.Total() > 0 {
		fmt.Printf(" %s", ui.Dim(fmt.Sprintf("$%d", cut.Total())))
	}
	fmt.Println()

	if d, ok := a.state.DueIn(time.Now()); ok && d > 0 {
		ui.Hint("next one due in %s", ui.Duration(d))
	}
	return nil
}

func (a *app) logList(args []string) error {
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	limit := fs.Int("n", 10, "how many to show")
	stats := fs.Bool("stats", false, "show totals and your regular shops")
	if err := parse(fs, args); err != nil {
		return nil
	}

	if len(a.state.Cuts) == 0 {
		fmt.Println(ui.Dim("No cuts logged yet."))
		ui.Hint("fade-cli log add --shop <shop> --price 45")
		return nil
	}

	cuts := a.state.Cuts
	if *limit > 0 && len(cuts) > *limit {
		cuts = cuts[:*limit]
	}

	now := time.Now()
	fmt.Println()
	t := ui.NewTable("when", "shop", "barber", "paid", "rated")
	t.RightAlign(3)
	for _, c := range cuts {
		paid := ui.Dim("—")
		if c.Total() > 0 {
			paid = fmt.Sprintf("$%d", c.Total())
		}
		rated := ui.Dim("—")
		if c.Rating > 0 {
			rated = ratingCell(c.Rating)
		}
		t.Row(ui.RelDay(c.Date, now), c.ShopName, dimIfEmpty(c.Barber), paid, rated)
	}
	t.Render(os.Stdout)
	fmt.Println()

	if *stats {
		a.printStats()
	}
	return nil
}

func (a *app) printStats() {
	var spent int
	for _, c := range a.state.Cuts {
		spent += c.Total()
	}
	interval, src := a.state.IntervalWithSource()
	fmt.Printf("  %d cuts, $%d total, every %s %s\n\n",
		len(a.state.Cuts), spent, ui.Duration(interval), ui.Dim("("+src.Label()+")"))

	regulars := a.state.Regulars()
	if len(regulars) == 0 {
		return
	}
	fmt.Println(ui.Bold("  Where you go"))
	t := ui.NewTable("shop", "visits", "spent", "last").Indent("  ")
	t.RightAlign(1, 2)
	now := time.Now()
	for i, r := range regulars {
		if i >= 5 {
			break
		}
		t.Row(r.ShopName, fmt.Sprint(r.Visits), fmt.Sprintf("$%d", r.Spent), ui.RelDay(r.Last, now))
	}
	t.Render(os.Stdout)
	fmt.Println()
}

func (a *app) due(args []string) error {
	fs := flag.NewFlagSet("due", flag.ContinueOnError)
	if err := parse(fs, args); err != nil {
		return nil
	}

	now := time.Now()
	showAppt := func() {
		if ap, ok := a.state.NextAppointment(now); ok {
			verb := "requested"
			if ap.Status == store.ApptConfirmed {
				verb = "booked"
			}
			fmt.Printf("  %s %s at %s %s\n", ui.Cyan(verb),
				catalog.InShopTime(ap.When).Format("Mon Jan 2 3:04pm"),
				ap.ShopName, ui.Dim("("+ap.ID+")"))
		}
	}

	d, ok := a.state.DueIn(now)
	if !ok {
		fmt.Println()
		showAppt()
		fmt.Println(ui.Dim("  No cut history yet, so there's nothing to predict."))
		ui.Hint("fade-cli log add --shop <shop> --price 45")
		return nil
	}

	last, _ := a.state.LastCut()
	fmt.Println()
	switch {
	case d < 0:
		fmt.Printf("  %s  %s\n", ui.Red(fmt.Sprintf("Overdue by %s", ui.Duration(-d))),
			ui.Dim(fmt.Sprintf("last cut %s at %s", ui.RelDay(last.Date, now), last.ShopName)))
	case d < 3*24*time.Hour:
		fmt.Printf("  %s  %s\n", ui.Yellow(fmt.Sprintf("Due in %s", ui.Duration(d))),
			ui.Dim("worth booking now"))
	default:
		fmt.Printf("  %s  %s\n", ui.Green(fmt.Sprintf("Due in %s", ui.Duration(d))),
			ui.Dim(fmt.Sprintf("last cut %s", ui.RelDay(last.Date, now))))
	}
	fmt.Println()

	showAppt()

	// A prediction built on the generic default is a guess, not a measurement.
	if _, src := a.state.IntervalWithSource(); src == store.IntervalDefault {
		ui.Hint("based on a %d-day default -- log a few cuts and it learns your cadence",
			int(store.DefaultCutInterval.Hours()/24))
	}
	if last.ShopID != "" {
		ui.Hint("rebook: fade-cli book %s", last.ShopID)
	}
	return nil
}

func ratingCell(r int) string {
	s := fmt.Sprintf("%d/5", r)
	switch {
	case r >= 5:
		return ui.Green(s)
	case r <= 2:
		return ui.Red(s)
	default:
		return s
	}
}

func dimIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ui.Dim("—")
	}
	return s
}

// parseLogDate is the past-facing sibling of parseDay: a bare weekday here
// means the most recent one, because you're recording something that happened.
func parseLogDate(s string, now time.Time) (time.Time, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	midnight := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	}
	switch s {
	case "", "today":
		return midnight(now), nil
	case "yesterday":
		return midnight(now.AddDate(0, 0, -1)), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("can't read %q as a date -- try today, yesterday, or 2026-08-01", s)
}
