package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
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

// interactive is what you get by running `fade-cli` with no arguments: the
// city as an index. Neighborhood, then its shops grouped by stop in the order
// the train reaches them from Manhattan. Nothing about where you live is
// assumed or remembered; the map is the same for everyone.
func (a *app) interactive() error {
	raw, ok := ui.EnterRaw()
	if !ok {
		return errors.New("this terminal can't run the browser -- try `fade-cli find`")
	}
	// Restore on every exit path, including a panic: leaving a user's shell in
	// raw mode with echo off is a genuinely bad way to fail.
	defer raw.Restore()

	err := a.neighborhoods(raw)
	if errors.Is(err, errAborted) {
		return nil
	}
	return err
}

// neighborhoods is the landing screen. One city exists, so it is the title
// rather than a list with a single row to press enter on; a second city is
// what would earn a screen above this one.
func (a *app) neighborhoods(raw *ui.Raw) error {
	sel := 0
	for {
		var rows [][]string
		var open []func() error
		if n := len(a.state.Profile.Saved); n > 0 {
			rows = append(rows, []string{ui.Accent(ui.Sym().Pin), "Saved", ui.Subtle("your pinned shops"), plural(n, "shop")})
			open = append(open, func() error { return a.savedList(raw) })
		}
		for _, nb := range geo.Neighborhoods {
			// Count what the list will actually show: venue-collapsed rows,
			// the same shopsByStop the detail screen uses. Counting raw shops
			// here made the index promise "16 shops" and the list show 10.
			n := 0
			for _, g := range a.shopsByStop(nb, 0, false) {
				n += len(g.shops)
			}
			shops := ui.Subtle("none yet")
			if n > 0 {
				shops = plural(n, "shop")
			}
			rows = append(rows, []string{lineBadge(nb.Line()), nb.Name, ui.Subtle(nb.Span()), shops})
			open = append(open, func() error { return a.neighborhoodList(raw, nb) })
		}

		list := &ui.List{
			Title:      geo.City,
			Subtitle:   "pick a neighborhood · shops run outbound from Union Sq",
			Rows:       rows,
			Right:      map[int]bool{3: true},
			Height:     len(rows),
			Filterable: true,
			Hints: []ui.KeyHint{
				{Key: "h", Label: "history"},
				{Key: "q", Label: "quit"},
			},
		}
		got := list.Run(raw, sel)
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == "h":
			if err := a.historyScreen(raw); err != nil {
				return err
			}
			continue
		}
		sel = got.Index
		if err := open[got.Index](); err != nil {
			return err
		}
	}
}

// neighborhoodList is the shop list: grouped by stop, outbound, and within a
// stop by walk time from it. Price and open-now filters apply on top; when
// they empty the list one is relaxed with a note rather than showing nothing.
func (a *app) neighborhoodList(raw *ui.Raw, nb geo.Neighborhood) error {
	maxPrice, openNow := a.state.Profile.MaxPrice, false
	sel, notice, focus := 0, "", ""

	for {
		groups := a.shopsByStop(nb, maxPrice, openNow)
		total := 0
		for _, g := range groups {
			total += len(g.shops)
		}
		if total == 0 {
			note, relaxed := relax(&openNow, &maxPrice)
			if !relaxed {
				raw.Suspend(func() {
					fmt.Printf("\n  %s\n", ui.Subtle("No shops in "+nb.Name+" yet."))
					pause()
				})
				return nil
			}
			notice = note
			continue
		}

		rows, right, results, resultAt := stopGroupedRows(groups, time.Now(), a.state.Profile.IsSaved)
		sections := map[int]bool{}
		for i, r := range resultAt {
			if r < 0 {
				sections[i] = true
			}
		}
		if focus != "" {
			for i, r := range resultAt {
				if r >= 0 && results[r].Shop.ID == focus {
					sel = i
				}
			}
			focus = ""
		}

		subtitle := nb.Span() + " · " + plural(total, "shop")
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

		list := &ui.List{
			Title:      geo.City + " › " + nb.Name,
			Subtitle:   subtitle,
			Note:       notice,
			Rows:       rows,
			Right:      right,
			Sections:   sections,
			Filterable: true,
			Hints: []ui.KeyHint{
				{Key: "f", Label: "price"},
				{Key: "o", Label: openLabel(openNow)},
				{Key: "*", Label: "save"},
				{Key: "h", Label: "history"},
				{Key: ui.EscKey, Label: "back"},
				{Key: "q", Label: "quit"},
			},
		}
		notice = ""

		got := list.Run(raw, sel)
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == ui.EscKey:
			return nil
		case got.Cmd == "f":
			raw.Suspend(func() {
				fmt.Print("\n")
				if n, ok := ui.Int("max price, or enter for any: $", 0); ok {
					maxPrice = n
				}
			})
			sel = 0
		case got.Cmd == "o":
			openNow = !openNow
			sel = 0
		case got.Cmd == "*":
			shop := results[resultAt[list.Cursor()]].Shop
			if a.state.Profile.ToggleSaved(shop.ID) {
				notice = "saved " + shop.Name
			} else {
				notice = "unsaved " + shop.Name
			}
			if err := a.state.Save(); err != nil {
				return err
			}
			focus = shop.ID
		case got.Cmd == "h":
			if err := a.historyScreen(raw); err != nil {
				return err
			}
			sel = got.Index
		default:
			sel = got.Index
			r := results[resultAt[got.Index]]
			if err := a.shopScreen(raw, r, r.Stop.Name); err != nil {
				return err
			}
		}
	}
}

// stopGroup is one stop's shops, nearest first.
type stopGroup struct {
	stop  geo.Stop
	shops []catalog.Result
}

// shopsByStop buckets the filtered catalog by the neighborhood's stops, in
// outbound order, measuring each shop from its own stop.
func (a *app) shopsByStop(nb geo.Neighborhood, maxPrice int, openNow bool) []stopGroup {
	q := catalog.Query{MaxPrice: maxPrice, CollapseBy: "venue"}
	if openNow {
		q.OpenAt = time.Now()
	}
	byStop := map[string][]catalog.Result{}
	for _, r := range a.cat.Find(q) {
		if r.Stop.ID == "" || !nb.Has(r.Stop.ID) {
			continue
		}
		r = atStop(r)
		byStop[r.Stop.ID] = append(byStop[r.Stop.ID], r)
	}

	var out []stopGroup
	for _, stop := range nb.StopList() {
		shops := byStop[stop.ID]
		if len(shops) == 0 {
			continue
		}
		sort.SliceStable(shops, func(i, j int) bool {
			if shops[i].WalkMin != shops[j].WalkMin {
				return shops[i].WalkMin < shops[j].WalkMin
			}
			return shops[i].Shop.Name < shops[j].Shop.Name
		})
		out = append(out, stopGroup{stop: stop, shops: shops})
	}
	return out
}

// atStop measures a result from its own nearest stop, which is the distance
// that means something when nobody has said where they are.
func atStop(r catalog.Result) catalog.Result {
	if r.Stop.ID == "" || !r.Shop.Located() {
		return r
	}
	r.HasOrigin = true
	r.Miles = geo.MilesBetween(r.Stop.Point, r.Shop.Point)
	r.WalkMin = geo.WalkMinutes(r.Stop.Point, r.Shop.Point)
	return r
}

// resultAtStop builds the stop context for a shop reached by id -- from
// history or the saved list -- rather than through a search.
func (a *app) resultAtStop(shop catalog.Shop) catalog.Result {
	r := catalog.Result{Shop: shop}
	if stop, ok := shop.NearestStop(); ok {
		r.Stop = stop
	}
	return atStop(r)
}

// stopGroupedRows flattens the groups into list rows with a heading per
// stop. resultAt maps each row to its result, or -1 for a heading.
func stopGroupedRows(groups []stopGroup, now time.Time, saved func(string) bool) (rows [][]string, right map[int]bool, results []catalog.Result, resultAt []int) {
	for _, g := range groups {
		results = append(results, g.shops...)
	}
	shopRows, right := browseTable(results, now, saved)
	i := 0
	for _, g := range groups {
		rows = append(rows, []string{g.stop.Name, plural(len(g.shops), "shop")})
		resultAt = append(resultAt, -1)
		for range g.shops {
			rows = append(rows, shopRows[i])
			resultAt = append(resultAt, i)
			i++
		}
	}
	return rows, right, results, resultAt
}

// savedList is the pinned shops, flat, each measured from its own stop.
func (a *app) savedList(raw *ui.Raw) error {
	sel, notice := 0, ""
	for {
		var results []catalog.Result
		for _, id := range a.state.Profile.Saved {
			if s, ok := a.cat.Get(id); ok {
				results = append(results, a.resultAtStop(s))
			}
		}
		if len(results) == 0 {
			return nil
		}
		rows, right := browseTable(results, time.Now(), nil)
		for i := range rows {
			rows[i] = append(rows[i], ui.Subtle(results[i].Stop.Name))
		}

		list := &ui.List{
			Title:      geo.City + " › Saved",
			Subtitle:   plural(len(results), "shop") + " · walk times from each shop's stop",
			Note:       notice,
			Rows:       rows,
			Right:      right,
			Filterable: true,
			Hints: []ui.KeyHint{
				{Key: "*", Label: "unsave"},
				{Key: ui.EscKey, Label: "back"},
				{Key: "q", Label: "quit"},
			},
		}
		notice = ""

		got := list.Run(raw, sel)
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == ui.EscKey:
			return nil
		case got.Cmd == "*":
			shop := results[list.Cursor()].Shop
			a.state.Profile.ToggleSaved(shop.ID)
			if err := a.state.Save(); err != nil {
				return err
			}
			notice = "unsaved " + shop.Name
			sel = min(list.Cursor(), len(results)-2)
		default:
			sel = got.Index
			r := results[got.Index]
			if err := a.shopScreen(raw, r, r.Stop.Name); err != nil {
				return err
			}
		}
	}
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
// what guarantees the list loop terminates: every call either changes state
// or reports that there is nothing left to change.
func relax(openNow *bool, maxPrice *int) (string, bool) {
	switch {
	case *openNow:
		*openNow = false
		return "nothing open right now -- showing every shop", true
	case *maxPrice > 0:
		*maxPrice = 0
		return "nothing under that price -- price filter cleared", true
	default:
		return "", false
	}
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
	g := ui.Sym()
	switch line {
	case "G":
		return ui.Good(g.Dot) + " " + ui.Subtle(line)
	case "M":
		return ui.Warning(g.Dot) + " " + ui.Subtle(line)
	default: // the L is gray on the map too
		return ui.Subtle(g.Dot) + " " + ui.Subtle(line)
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
			// The number is the product here; the tel: handoff is a bonus
			// that depends on the machine having something to dial with.
			fmt.Printf("  Call %s\n", ui.Bold(provider.PrettyPhone(shop.Phone)))
			if provider.Copy(provider.PrettyPhone(shop.Phone)) {
				fmt.Printf("  %s\n", ui.Dim("number copied to your clipboard"))
			}
		case provider.ActionOpen:
			fmt.Printf("  %s\n", ui.Dim(action.Target))
		}

		if !ui.Yes(action.Label + "? [Y/n] ") {
			return
		}
		if openErr := provider.Open(action.Target); openErr != nil {
			ui.Warn("couldn't open it: %v", openErr)
			if action.Type == provider.ActionCall {
				fmt.Printf("  %s\n", ui.Dim("dial it from your phone -- the number is on your clipboard"))
			}
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
func (a *app) historyScreen(raw *ui.Raw) error {
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
	r := a.resultAtStop(shop)
	return a.shopScreen(raw, r, r.Stop.Name)
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
