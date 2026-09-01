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
	"fadecli/internal/geo"
	"fadecli/internal/provider"
	"fadecli/internal/store"
	"fadecli/internal/ui"
)

// errAborted unwinds an interactive flow without printing an error, for when
// the user quits or hits ctrl-C.
var errAborted = errors.New("aborted")

// interactive is what you get by running `fade-cli` with no arguments: an
// arrow-key list of shops near you. The flag interface still exists underneath
// for anyone who wants it.
func (a *app) interactive() error {
	raw, ok := ui.EnterRaw()
	if !ok {
		return errors.New("this terminal can't run the browser -- try `fade-cli find`")
	}
	// Restore on every exit path, including a panic: leaving a user's shell in
	// raw mode with echo off is a genuinely bad way to fail.
	defer raw.Restore()

	if err := a.ensureHome(raw); err != nil {
		return err
	}
	return a.browse(raw)
}

// ensureHome runs first-time setup. Everything else needs somewhere to measure
// from, so this is the only question the tool ever insists on.
func (a *app) ensureHome(raw *ui.Raw) error {
	if _, ok := a.state.Profile.Origin(); ok {
		return nil
	}
	return a.pickHome(raw, true)
}

func (a *app) pickHome(raw *ui.Raw, firstRun bool) error {
	counts := map[string]int{}
	for _, s := range a.cat.Shops {
		if stop, ok := s.NearestStop(); ok {
			counts[stop.ID]++
		}
	}

	// Flatten every corridor into one list, tagging each row with its line so
	// the grouping reads without needing section headers.
	stops := geo.AllStops()
	rows := make([][]string, 0, len(stops))
	start := 0
	for i, s := range stops {
		shops := ui.Dim("none yet")
		if n := counts[s.ID]; n > 0 {
			shops = plural(n, "shop")
		}
		rows = append(rows, []string{lineBadge(s.Line), s.Name, shops})
		if s.ID == a.state.Profile.HomeStop {
			start = i
		}
	}

	subtitle := geo.AreaName()
	if !firstRun {
		subtitle = "pick a new home stop"
	}

	list := &ui.List{
		Title:      "Where do you walk from?",
		Subtitle:   subtitle,
		Rows:       rows,
		Right:      map[int]bool{2: true},
		Height:     len(stops),
		Filterable: true,
	}
	if !firstRun {
		list.Hints = []ui.KeyHint{{Key: ui.EscKey, Label: "back"}}
	}

	sel := list.Run(raw, start)
	if !sel.OK || sel.Cmd != "" {
		if firstRun {
			return errAborted
		}
		return nil
	}

	stop := stops[sel.Index]
	a.state.Profile.HomeStop = stop.ID
	a.state.Profile.HomePoint = nil
	if err := a.state.Save(); err != nil {
		return fmt.Errorf("saving your stop: %w", err)
	}
	return nil
}

// walkTiers are the radii browse will try, in minutes. Sparse corners of the
// map widen automatically rather than showing three shops and nothing else.
var walkTiers = []int{20, 30, 45}

// enoughShops is the point below which a radius is treated as too tight to be
// worth showing on its own.
const enoughShops = 6

func (a *app) browse(raw *ui.Raw) error {
	maxPrice, sel := a.state.Profile.MaxPrice, 0
	showAll, openNow := false, false
	sortBy := catalog.SortNearest
	notice := "" // explains a filter that had to be dropped to show anything
	focus := ""  // shop id to put the cursor on after the list is rebuilt

	for {
		origin, label, err := a.resolveOrigin("")
		if err != nil {
			return err
		}

		results, within := a.walkableShops(origin, maxPrice, showAll, openNow, sortBy)
		if len(results) == 0 {
			// Relax one filter and retry. This must be guaranteed to run out:
			// an earlier version just cleared the price cap on the assumption
			// that nothing else could empty the list, which stopped being true
			// when the open-now filter arrived and left the loop able to spin.
			note, relaxed := relax(&openNow, &maxPrice, &showAll)
			if !relaxed {
				return errors.New("no shops in the catalog to show")
			}
			notice = note
			continue
		}

		subtitle := "all " + plural(len(results), "shop")
		if within > 0 {
			subtitle = fmt.Sprintf("%s within a %d min walk", plural(len(results), "shop"), within)
		}
		subtitle += " · " + sortBy.Label()
		if maxPrice > 0 {
			// A price cap keeps shops whose price nobody has published -- we
			// can't claim they're over budget. Say so, or a list of unpriced
			// shops reads as a broken filter.
			subtitle += fmt.Sprintf(" under $%d", maxPrice)
			if unpriced := countUnpriced(results); unpriced > 0 {
				subtitle += fmt.Sprintf(" (+%d unpriced)", unpriced)
			}
		}
		if openNow {
			subtitle += ", open now"
		}
		if stop, ok := geo.StopByID(a.state.Profile.HomeStop); ok {
			if cor, ok := geo.CorridorByID(stop.Corridor); ok {
				subtitle = cor.Name + " · " + subtitle
			}
		}

		results = pinSaved(results, a.state.Profile)
		if focus != "" {
			// Pinning reorders the list; keep the cursor on the shop that
			// was just saved rather than on whatever slid into its slot.
			for i, r := range results {
				if r.Shop.ID == focus {
					sel = i
				}
			}
			focus = ""
		}
		rows, right := browseTable(results, time.Now(), a.state.Profile.IsSaved)
		allLabel := "show all"
		if showAll {
			allLabel = "nearby only"
		}

		list := &ui.List{
			Title:      "Near " + label,
			Subtitle:   subtitle,
			Note:       notice,
			Rows:       rows,
			Right:      right,
			Filterable: true,
			Hints: []ui.KeyHint{
				{Key: "f", Label: "price"},
				{Key: "a", Label: allLabel},
				{Key: "o", Label: openLabel(openNow)},
				{Key: "s", Label: "sort"},
				{Key: "*", Label: "save"},
				{Key: "c", Label: "stop"},
				{Key: "h", Label: "history"},
				{Key: "q", Label: "quit"},
			},
		}
		notice = ""

		got := list.Run(raw, sel)
		sel = got.Index
		switch {
		case !got.OK, got.Cmd == "q":
			return nil
		case got.Cmd == "*":
			// The list returns the cursor row only on a selection, so a
			// save pins whatever was highlighted when the key was pressed.
			shop := results[list.Cursor()].Shop
			if a.state.Profile.ToggleSaved(shop.ID) {
				notice = "saved " + shop.Name
			} else {
				notice = "unsaved " + shop.Name
			}
			if err := a.state.Save(); err != nil {
				return err
			}
			focus = shop.ID
		case got.Cmd == "c":
			if err := a.pickHome(raw, false); err != nil {
				return err
			}
			sel = 0
		case got.Cmd == "f":
			raw.Suspend(func() {
				fmt.Print("\n")
				if n, ok := ui.Int("max price, or enter for any: $", 0); ok {
					maxPrice = n
				}
			})
			sel = 0
		case got.Cmd == "a":
			showAll = !showAll
			sel = 0
		case got.Cmd == "o":
			openNow = !openNow
			sel = 0
		case got.Cmd == "s":
			sortBy = sortBy.Next()
			sel = 0
		case got.Cmd == "h":
			if err := a.historyScreen(raw, origin, label); err != nil {
				if errors.Is(err, errAborted) {
					return nil
				}
				return err
			}
		default:
			if err := a.shopScreen(raw, results[got.Index], label); err != nil {
				if errors.Is(err, errAborted) {
					return nil
				}
				return err
			}
		}
	}
}

// walkableShops returns the shops worth showing and the walk-minute radius
// they were found within (0 meaning unbounded). Showing the whole directory
// sorted by distance buries the eight shops you'd actually walk to under
// thirty you wouldn't, so the default is a radius that widens only when the
// neighborhood is genuinely sparse.
func (a *app) walkableShops(origin *geo.Point, maxPrice int, showAll, openNow bool, by catalog.SortBy) ([]catalog.Result, int) {
	q := catalog.Query{Origin: origin, MaxPrice: maxPrice, CollapseBy: "venue", Sort: by}
	if openNow {
		q.OpenAt = time.Now()
	}

	if !showAll && origin != nil {
		for _, minutes := range walkTiers {
			q.MaxMiles = geo.MilesForWalkMinutes(minutes)
			if got := a.cat.Find(q); len(got) >= enoughShops {
				return got, minutes
			}
		}
	}

	q.MaxMiles = 0
	return a.cat.Find(q), 0
}

// countUnpriced reports how many results carry no published price.
func countUnpriced(results []catalog.Result) int {
	n := 0
	for _, r := range results {
		if r.Shop.PriceMin == 0 && r.Shop.PriceMax == 0 {
			n++
		}
	}
	return n
}

// relax drops one filter so an empty result set can recover, returning a note
// for the user and whether anything was actually relaxed. The false return is
// what guarantees the browse loop terminates: every call either changes state
// or reports that there is nothing left to change.
func relax(openNow *bool, maxPrice *int, showAll *bool) (string, bool) {
	switch {
	case *openNow:
		*openNow = false
		return "nothing open right now -- showing every shop", true
	case *maxPrice > 0:
		*maxPrice = 0
		return "nothing under that price -- price filter cleared", true
	case !*showAll:
		*showAll = true
		return "nothing nearby -- widened to the whole directory", true
	default:
		return "", false
	}
}

// pinSaved moves saved shops to the top of the list, keeping the sort order
// within each group. Pinning rather than a separate screen: the point of
// saving a shop is to stop scrolling for it.
func pinSaved(results []catalog.Result, p store.Profile) []catalog.Result {
	if len(p.Saved) == 0 {
		return results
	}
	out := make([]catalog.Result, 0, len(results))
	for _, r := range results {
		if p.IsSaved(r.Shop.ID) {
			out = append(out, r)
		}
	}
	for _, r := range results {
		if !p.IsSaved(r.Shop.ID) {
			out = append(out, r)
		}
	}
	return out
}

// browseTable builds the browse rows, dropping any column that carries no
// information for this result set. Greenpoint is entirely call-only shops with
// no published price or rating, and a column of "—" is just noise.
func browseTable(results []catalog.Result, now time.Time, saved func(string) bool) ([][]string, map[int]bool) {
	n := len(results)
	// The open/closed dot only earns its two columns when something in view
	// actually has hours on file.
	anyHours := false
	for _, r := range results {
		if r.Shop.Hours.Known() {
			anyHours = true
			break
		}
	}
	name := make([]string, n)
	walk := make([]string, n)
	price := make([]string, n)
	rating := make([]string, n)

	var anyWalk, anyPrice, anyRating bool
	for i, r := range results {
		name[i] = openDot(r.Shop, now, anyHours) + r.Shop.Name
		if saved != nil && saved(r.Shop.ID) {
			name[i] = openDot(r.Shop, now, anyHours) + ui.Accent(ui.Sym().Pin+" ") + r.Shop.Name
		}
		if l := r.Shop.Type.Label(); l != "" {
			name[i] += ui.Dim("  " + l)
		}
		if extra := len(r.Alongside); extra > 0 {
			name[i] += ui.Dim(fmt.Sprintf("  +%d", extra))
		}

		walk[i], price[i], rating[i] = ui.Dim("—"), ui.Dim("—"), ui.Dim("—")
		if r.HasOrigin {
			walk[i] = fmt.Sprintf("%d min", r.WalkMin)
			anyWalk = true
		}
		if r.Shop.PriceMin > 0 || r.Shop.PriceMax > 0 {
			price[i] = priceCell(r.Shop)
			anyPrice = true
		}
		if r.Shop.Rating > 0 {
			rating[i] = ratingBadge(r.Shop)
			anyRating = true
		}
	}

	cols := [][]string{name}
	right := map[int]bool{}
	add := func(cells []string, keep, rightAlign bool) {
		if !keep {
			return
		}
		if rightAlign {
			right[len(cols)] = true
		}
		cols = append(cols, cells)
	}
	add(walk, anyWalk, true)
	add(price, anyPrice, true)
	add(rating, anyRating, false)

	rows := make([][]string, n)
	for i := range results {
		row := make([]string, len(cols))
		for j, c := range cols {
			row[j] = c[i]
		}
		rows[i] = row
	}
	return rows, right
}

// openDot marks a shop open or closed at a glance. A shop with no hours on
// file gets blank space, never a dot: claiming it's shut because nobody looked
// is worse than saying nothing.
func openDot(s catalog.Shop, now time.Time, show bool) string {
	if !show {
		return ""
	}
	g := ui.Sym()
	switch st, _ := s.Hours.OpenAt(now); st {
	case catalog.StatusOpen:
		return ui.Good(g.Dot + " ")
	case catalog.StatusClosed:
		return ui.Subtle(g.Ring + " ")
	default:
		return "  "
	}
}

func openLabel(on bool) string {
	if on {
		return "any hours"
	}
	return "open now"
}

// lineBadge renders a subway line in something close to its real colour, so
// the corridors read as distinct groups without section headers.
func lineBadge(line string) string {
	switch line {
	case "G":
		return ui.Green("●") + " " + ui.Dim(line)
	case "L":
		return ui.Dim("●") + " " + ui.Dim(line)
	default:
		return ui.Dim("● " + line)
	}
}

// ratingSource names where a rating came from. Shown on the detail screen but
// not in the browse list: a 4.7 from Google and a 5.0 from Booksy are not the
// same measurement, and the difference matters most right before you book.
func ratingSource(s catalog.Shop) string {
	names := map[string]string{
		"booksy": "on Booksy",
		"fresha": "on Fresha",
		"google": "on Google",
		"web":    "from listings",
	}
	if n, ok := names[s.RatingSrc]; ok {
		return ui.Dim(" " + n)
	}
	return ""
}

// ratingBadge puts a star only on shops that actually have a rating, so the
// column doesn't fill with placeholder glyphs for the call-only shops.
func ratingBadge(s catalog.Shop) string {
	if s.Rating == 0 {
		return ui.Dim("—")
	}
	return ui.Yellow("★") + " " + ui.Stars(s.Rating, s.Reviews)
}

// shopScreen is the detail view: everything you need to decide, and the few
// things you might do about it, as a navigable list.
func (a *app) shopScreen(raw *ui.Raw, r catalog.Result, from string) error {
	chairs := append([]catalog.Shop{r.Shop}, r.Alongside...)
	cur, sel := 0, 0
	note := ""

	for {
		shop := chairs[cur]

		actions := [][]string{
			{ui.Green("Book it"), ui.Dim(bookingAction(shop))},
			{"See open times", ui.Dim("today")},
			{"Log a cut here", ui.Dim("record what you paid")},
		}
		kinds := []string{"book", "times", "log"}
		if shop.Booking.Kind == catalog.KindPhone && shop.Phone != "" {
			actions = append(actions, []string{"Request a time", ui.Dim("prepared call or text")})
			kinds = append(kinds, "request")
		}
		if len(chairs) > 1 {
			actions = append(actions, []string{
				"Switch barber",
				ui.Dim(fmt.Sprintf("%d at this address", len(chairs))),
			})
			kinds = append(kinds, "switch")
		}
		actions = append(actions, []string{ui.Dim("Back"), ""})
		kinds = append(kinds, "back")

		saveLabel := "save"
		if a.state.Profile.IsSaved(shop.ID) {
			saveLabel = "unsave"
		}
		list := &ui.List{
			Title:    shop.Name,
			Subtitle: shopSubtitle(shop, r, from),
			Hint:     a.shopPanels(shop, r, from),
			Note:     note,
			Rows:     actions,
			Height:   len(actions),
			Hints: []ui.KeyHint{
				{Key: "*", Label: saveLabel},
				{Key: ui.EscKey, Label: "back"},
				{Key: "q", Label: "quit"},
			},
		}
		note = ""

		got := list.Run(raw, sel)
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == "*":
			if a.state.Profile.ToggleSaved(shop.ID) {
				note = "saved -- pinned to the top of browse"
			} else {
				note = "unsaved"
			}
			if err := a.state.Save(); err != nil {
				return err
			}
			continue
		case got.Cmd != "":
			return nil
		}
		sel = got.Index

		switch kinds[got.Index] {
		case "back":
			return nil
		case "book":
			if err := a.bookInteractive(raw, shop); err != nil {
				return err
			}
		case "times":
			a.timesInteractive(raw, shop)
		case "request":
			var reqErr error
			raw.Suspend(func() {
				fmt.Println()
				in, ok := ui.Line("when? (like \"fri 3pm\") ")
				if !ok || strings.TrimSpace(in) == "" {
					return
				}
				if reqErr = a.manualBook(shop, in, "", false, false); reqErr != nil {
					ui.Warn("%v", reqErr)
					reqErr = nil
					pause()
				} else {
					pause()
				}
			})
			if reqErr != nil {
				return reqErr
			}
		case "log":
			if err := a.logInteractive(raw, shop); err != nil {
				return err
			}
		case "switch":
			n, err := a.pickBarber(raw, chairs, cur)
			if err != nil {
				return err
			}
			cur = n
		}
	}
}

// shopSubtitle measures from where the user actually is. WalkMin is distance
// from the origin, so it must be labelled with the origin -- pairing it with
// the shop's own nearest stop reads as a walk between two places neither of
// which you are standing in.
func shopSubtitle(shop catalog.Shop, r catalog.Result, from string) string {
	parts := []string{shop.Address}
	if r.HasOrigin && from != "" {
		parts = append(parts, fmt.Sprintf("%d min walk from %s", r.WalkMin, from))
	} else if r.HasOrigin {
		parts = append(parts, fmt.Sprintf("%d min walk", r.WalkMin))
	}
	return strings.Join(parts, " · ")
}

// shopPanels lays the detail screen out as titled panels -- what the place
// is, when it's open, how you book -- side by side when the terminal is wide
// enough and stacked when it isn't.
func (a *app) shopPanels(shop catalog.Shop, r catalog.Result, from string) string {
	cols, _ := ui.Size()
	now := time.Now()

	// Details
	var details []string
	if shop.Rating > 0 {
		details = append(details, ratingBadge(shop)+ratingSource(shop))
	}
	if shop.PriceMin > 0 || shop.PriceMax > 0 {
		line := priceCell(shop)
		if pct, ok := a.pricePercentile(shop); ok {
			line += "  " + ui.Meter(pct, 12)
			details = append(details, line, ui.Subtle(fmt.Sprintf("pricier than %d%% of shops", pct)))
		} else {
			details = append(details, line)
		}
	}
	// Only worth naming the shop's own stop when it isn't where you started.
	if r.Stop.Name != "" && r.Stop.Name != from {
		details = append(details, ui.Subtle("by "+r.Stop.Name))
	}
	if l := shop.Type.Label(); l != "" {
		details = append(details, ui.Subtle(l))
	}
	if l := shop.WalkIn.Label(); l != "" {
		details = append(details, ui.Accent(l))
	}
	if v := a.visitsTo(shop.ID); v > 0 {
		details = append(details, ui.Good("been here "+plural(v, "time")))
	}
	if len(details) == 0 {
		details = append(details, ui.Subtle("nothing published yet"))
	}

	// Hours: the whole week, today marked, so "closes at 7" has context.
	// A short terminal gets today only -- the panels must never push the
	// actions and key hints off the bottom of the screen.
	_, rows := ui.Size()
	stacked := cols < 96
	compact := stacked && rows < 40
	var hours []string
	if shop.Hours.Known() {
		if f := hoursFact(shop, now); len(f) > 0 {
			hours = append(hours, f[0])
		}
		days := 7
		if compact {
			days = 1
		}
		for i := 0; i < days; i++ {
			day := now.AddDate(0, 0, i)
			label, trades := shop.Hours.OnDay(day)
			if !trades {
				label = "closed"
			}
			name := day.Format("Mon")
			if i == 0 {
				hours = append(hours, ui.Title(name)+"  "+label)
			} else {
				hours = append(hours, ui.Subtle(name)+"  "+ui.Subtle(label))
			}
		}
	} else {
		hours = append(hours, ui.Subtle("no hours on file"))
	}

	// Book
	book := []string{bookingPhrase(shop), ui.Subtle(bookingAction(shop))}
	if shop.Phone != "" && shop.Booking.Kind != catalog.KindPhone {
		book = append(book, ui.Subtle("or call "+provider.PrettyPhone(shop.Phone)))
	}
	if a.state.Profile.IsSaved(shop.ID) {
		book = append(book, ui.Accent(ui.Sym().Pin+" saved"))
	}

	width := cols - 4
	gap := 2
	if !stacked {
		width = (cols - 4 - 2*gap) / 3
	} else {
		width = min(width, 64)
	}
	panels := [][]string{
		ui.Box("Details", details, width),
		ui.Box("Hours", hours, width),
		ui.Box("Book", book, width),
	}
	if compact {
		// The Book panel repeats what the first action row says; in a short
		// stacked layout it is the first thing to fold into the Details box.
		panels = [][]string{
			ui.Box("Details", append(details, book...), width),
			ui.Box("Hours", hours, width),
		}
	}
	return strings.Join(ui.Beside(cols-4, gap, panels...), "\n")
}

// pricePercentile says how the shop's cheapest cut ranks against every priced
// shop in the catalog: 0 is the cheapest around, 100 the priciest.
func (a *app) pricePercentile(shop catalog.Shop) (int, bool) {
	if shop.PriceMin == 0 {
		return 0, false
	}
	below, priced := 0, 0
	for _, s := range a.cat.Shops {
		if s.PriceMin == 0 {
			continue
		}
		priced++
		if s.PriceMin < shop.PriceMin {
			below++
		}
	}
	if priced < 2 {
		return 0, false
	}
	return below * 100 / (priced - 1), true
}

// hoursFact renders the open/closed line, plus today's hours when they add
// something the status line doesn't already say. Returns nothing at all for a
// shop whose hours nobody has looked up.
func hoursFact(shop catalog.Shop, now time.Time) []string {
	status, _ := shop.Hours.OpenAt(now)
	label := shop.Hours.StatusLabel(now)
	if label == "" {
		return nil
	}
	out := []string{ui.Yellow(label)}
	if status == catalog.StatusOpen {
		out[0] = ui.Green(label)
	}
	if today := shop.Hours.TodayLabel(now); today != "" && today != "closed today" {
		out = append(out, ui.Dim("today "+today))
	}
	return out
}

func (a *app) pickBarber(raw *ui.Raw, chairs []catalog.Shop, cur int) (int, error) {
	rows := make([][]string, 0, len(chairs))
	for _, c := range chairs {
		rows = append(rows, []string{c.Name, priceCell(c), ratingBadge(c)})
	}
	list := &ui.List{
		Title:    "Barbers at " + chairs[0].Venue,
		Subtitle: chairs[0].Address,
		Hint:     "Each books separately.",
		Rows:     rows,
		Right:    map[int]bool{1: true},
		Height:   len(rows),
		Hints:    []ui.KeyHint{{Key: ui.EscKey, Label: "back"}},
	}

	got := list.Run(raw, cur)
	if !got.OK {
		return cur, errAborted
	}
	if got.Cmd != "" {
		return cur, nil
	}
	return got.Index, nil
}

func (a *app) bookInteractive(raw *ui.Raw, shop catalog.Shop) error {
	action := a.reg.For(shop).Handoff(shop)
	var err error

	raw.Suspend(func() {
		fmt.Println()
		switch action.Type {
		case provider.ActionNone:
			ui.Warn("%s", action.Label)
			pause()
			return
		case provider.ActionCall:
			fmt.Printf("  Call %s\n", ui.Bold(provider.PrettyPhone(shop.Phone)))
		case provider.ActionOpen:
			fmt.Printf("  %s\n", ui.Dim(action.Target))
		}

		if !ui.Yes(action.Label + "? [Y/n] ") {
			return
		}
		if openErr := provider.Open(action.Target); openErr != nil {
			ui.Warn("couldn't open it: %v", openErr)
			pause()
			return
		}
		fmt.Printf("\n  %s\n", ui.Green("opened"))
		if ui.Yes("log this cut now? [Y/n] ") {
			err = a.logCut(shop)
		}
	})
	return err
}

func (a *app) timesInteractive(raw *ui.Raw, shop catalog.Shop) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	raw.Suspend(func() {
		fmt.Printf("\n  %s\n", ui.Dim("checking..."))
		got := a.reg.AvailabilityAcross(ctx, []catalog.Shop{shop}, time.Now())
		fmt.Println()

		switch r := got[0]; {
		case r.Live():
			for _, line := range wrap(slotTimes(r.Slots), 8) {
				fmt.Println("  " + ui.Green(line))
			}
		case errors.Is(r.Err, provider.ErrNeedsCredentials):
			ui.Warn("%s has times, but this install has no API access to them", bookingPhrase(shop))
			fmt.Printf("  %s\n", ui.Dim("book it and you'll see their calendar"))
		default:
			ui.Warn("no live times here -- book it to see their calendar")
		}
		pause()
	})
}

func (a *app) logInteractive(raw *ui.Raw, shop catalog.Shop) error {
	var err error
	raw.Suspend(func() { err = a.logCut(shop) })
	return err
}

// logCut records a cut with three quick questions, defaulting to what you paid
// last time here. Runs in cooked mode, so it needs an active Suspend.
func (a *app) logCut(shop catalog.Shop) error {
	lastPrice, lastTip := a.lastPaidAt(shop.ID)
	fmt.Println()

	price, ok := ui.Int(fmt.Sprintf("what did it cost? [$%d] $", lastPrice), lastPrice)
	if !ok {
		return nil
	}
	tip, ok := ui.Int(fmt.Sprintf("tip? [$%d] $", lastTip), lastTip)
	if !ok {
		return nil
	}
	rating, ok := ui.Int("how'd it come out, 1-5? [skip] ", 0)
	if !ok {
		return nil
	}
	rating = min(rating, 5)

	a.state.AddCut(store.Cut{
		Date:     time.Now(),
		ShopID:   shop.ID,
		ShopName: shop.Name,
		Price:    price,
		Tip:      tip,
		Rating:   rating,
	})
	if err := a.state.Save(); err != nil {
		return fmt.Errorf("saving: %w", err)
	}

	fmt.Printf("\n  %s %s\n", ui.Green("logged"), ui.Dim(fmt.Sprintf("$%d at %s", price+tip, shop.Name)))
	if d, ok := a.state.DueIn(time.Now()); ok && d > 0 {
		fmt.Printf("  %s\n", ui.Dim("next cut due in "+ui.Duration(d)))
	}
	pause()
	return nil
}

// historyScreen lists past cuts and opens the shop behind whichever one you
// pick, which is the fastest route to rebooking a barber you liked.
func (a *app) historyScreen(raw *ui.Raw, origin *geo.Point, from string) error {
	if len(a.state.Cuts) == 0 {
		raw.Suspend(func() {
			fmt.Printf("\n  %s\n", ui.Dim("No cuts logged yet. Log one from any shop screen."))
			pause()
		})
		return nil
	}

	now := time.Now()
	rows := make([][]string, 0, len(a.state.Cuts))
	for _, c := range a.state.Cuts {
		paid := ui.Dim("—")
		if c.Total() > 0 {
			paid = fmt.Sprintf("$%d", c.Total())
		}
		rated := ui.Dim("—")
		if c.Rating > 0 {
			rated = ratingCell(c.Rating)
		}
		rows = append(rows, []string{ui.RelDay(c.Date, now), c.ShopName, paid, rated})
	}

	subtitle := plural(len(a.state.Cuts), "cut") + " logged"
	if d, ok := a.state.DueIn(now); ok {
		if d < 0 {
			subtitle += " · " + ui.Red("overdue by "+ui.Duration(-d))
		} else {
			subtitle += " · next due in " + ui.Duration(d)
		}
	}

	list := &ui.List{
		Title:      "Your cuts",
		Subtitle:   subtitle,
		Hint:       ui.Subtle("select one to go back to that shop"),
		Rows:       rows,
		Right:      map[int]bool{2: true},
		Filterable: true,
		Hints:      []ui.KeyHint{{Key: ui.EscKey, Label: "back"}},
	}

	got := list.Run(raw, 0)
	if !got.OK || got.Cmd != "" {
		return nil
	}

	// Cuts can name a shop that isn't in the directory -- you get your hair cut
	// wherever you like. Say so rather than opening a blank screen.
	cut := a.state.Cuts[got.Index]
	shop, ok := a.cat.Get(cut.ShopID)
	if !ok {
		raw.Suspend(func() {
			fmt.Printf("\n  %s\n", ui.Dim(cut.ShopName+" isn't in the directory."))
			pause()
		})
		return nil
	}
	return a.shopScreen(raw, a.resultFor(shop, origin), from)
}

// resultFor rebuilds the distance context Find would have attached, for paths
// that reach a shop by id rather than through a search.
func (a *app) resultFor(shop catalog.Shop, origin *geo.Point) catalog.Result {
	r := catalog.Result{Shop: shop}
	if stop, ok := shop.NearestStop(); ok {
		r.Stop = stop
	}
	if origin != nil && shop.Located() {
		r.HasOrigin = true
		r.Miles = geo.MilesBetween(*origin, shop.Point)
		r.WalkMin = geo.WalkMinutes(*origin, shop.Point)
	}
	return r
}

// again rebooks wherever you went last, which is the single most common thing
// anyone wants from a tool like this.
func (a *app) againCmd(args []string) error {
	// again has its own flag set rather than forwarding raw args to book:
	// passing -h straight through printed book's usage under the heading of a
	// command the user didn't run.
	fs := flag.NewFlagSet("again", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli again [flags]

Rebook wherever you went last.

FLAGS
`)
		fs.PrintDefaults()
	}
	var (
		yes       = fs.Bool("y", false, "skip the confirmation prompt")
		printOnly = fs.Bool("print", false, "print the target instead of opening it")
	)
	if err := parse(fs, args); err != nil {
		return nil // flag package already reported it
	}

	last, ok := a.state.LastCut()
	if !ok {
		return errors.New("no cut history yet -- run `fade-cli` and book one first")
	}
	shop, found := a.cat.Get(last.ShopID)
	if !found {
		return fmt.Errorf("%q isn't in the directory anymore", last.ShopName)
	}

	fmt.Printf("\n  %s %s\n", ui.Dim("last cut:"), ui.Bold(shop.Name))
	fmt.Printf("  %s\n", ui.Dim(fmt.Sprintf("%s · $%d", ui.RelDay(last.Date, time.Now()), last.Total())))

	fwd := []string{shop.ID}
	if *yes {
		fwd = append(fwd, "-y")
	}
	if *printOnly {
		fwd = append(fwd, "--print")
	}
	return a.book(fwd)
}

func (a *app) visitsTo(shopID string) int {
	n := 0
	for _, c := range a.state.Cuts {
		if c.ShopID == shopID {
			n++
		}
	}
	return n
}

// lastPaidAt recalls what this shop cost last time, to prefill the log prompts.
func (a *app) lastPaidAt(shopID string) (price, tip int) {
	for _, c := range a.state.Cuts {
		if c.ShopID == shopID {
			return c.Price, c.Tip
		}
	}
	return 0, 0
}

// bookingPhrase says how you'll actually book, in words rather than a platform
// name the user has no reason to care about.
func bookingPhrase(s catalog.Shop) string {
	switch s.Booking.Kind {
	case catalog.KindPhone:
		if s.Phone != "" {
			return "call " + provider.PrettyPhone(s.Phone)
		}
		return "call to book"
	case catalog.KindBooksy:
		return "books on Booksy"
	case catalog.KindFresha:
		return "books on Fresha"
	case catalog.KindSquare:
		return "books on Square"
	case catalog.KindSquire:
		return "books on Squire"
	default:
		return "books on their site"
	}
}

// bookingAction says what pressing enter will actually do, which is different
// from bookingPhrase's "who hosts their calendar".
func bookingAction(s catalog.Shop) string {
	if s.Booking.Kind == catalog.KindPhone && s.Phone != "" {
		return "calls " + provider.PrettyPhone(s.Phone)
	}
	if s.Booking.Kind == catalog.KindPhone {
		return "no number on file"
	}
	return "opens in your browser"
}

func slotTimes(slots []provider.Slot) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, s.Start.Format("3:04pm"))
	}
	return out
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func pause() { ui.Line(ui.Dim("press enter")) }
