package main

import (
	"flag"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/geo"
	"fadecli/internal/ui"
)

func newFS() (*flag.FlagSet, *string, *bool) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	day := fs.String("day", "today", "")
	print := fs.Bool("print", false, "")
	return fs, day, print
}

func TestParseAcceptsFlagsAfterPositionals(t *testing.T) {
	fs, day, print := newFS()
	if err := parse(fs, []string{"power of barbers", "--print", "--day", "tomorrow"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := fs.Arg(0); got != "power of barbers" {
		t.Errorf("positional = %q", got)
	}
	if !*print {
		t.Error("--print after a positional was not parsed")
	}
	if *day != "tomorrow" {
		t.Errorf("--day = %q, want tomorrow", *day)
	}
}

func TestParseHandlesFlagsBeforePositionals(t *testing.T) {
	fs, day, print := newFS()
	if err := parse(fs, []string{"--day", "fri", "--print", "cabello"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fs.Arg(0) != "cabello" || *day != "fri" || !*print {
		t.Errorf("args=%v day=%q print=%v", fs.Args(), *day, *print)
	}
}

func TestParseHandlesEqualsForm(t *testing.T) {
	fs, day, _ := newFS()
	if err := parse(fs, []string{"cabello", "--day=2026-08-14"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *day != "2026-08-14" {
		t.Errorf("--day = %q", *day)
	}
}

func TestParseDoubleDashStopsFlagParsing(t *testing.T) {
	fs, _, print := newFS()
	if err := parse(fs, []string{"--", "--print"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if *print {
		t.Error("--print after -- should be a positional, not a flag")
	}
	if fs.Arg(0) != "--print" {
		t.Errorf("positional = %q", fs.Arg(0))
	}
}

func TestParseMultiWordShopName(t *testing.T) {
	fs, _, print := newFS()
	if err := parse(fs, []string{"jack", "of", "all", "fadez", "--print"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(fs.Args()) != 4 || !*print {
		t.Errorf("args=%v print=%v", fs.Args(), *print)
	}
}

func TestParseDay(t *testing.T) {
	ny := catalog.ShopLocation()
	// Fri Aug 7, 10:30am ET (given as UTC), so the day is unambiguously the
	// 7th in New York and the weekday cases have a fixed reference. Days are
	// resolved in the shop's zone, so the expected midnights are New York's.
	now := time.Date(2026, time.August, 7, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"today", time.Date(2026, time.August, 7, 0, 0, 0, 0, ny)},
		{"tomorrow", time.Date(2026, time.August, 8, 0, 0, 0, 0, ny)},
		{"mon", time.Date(2026, time.August, 10, 0, 0, 0, 0, ny)},
		// "friday" on a Friday means next Friday, not today.
		{"friday", time.Date(2026, time.August, 14, 0, 0, 0, 0, ny)},
		{"2026-09-01", time.Date(2026, time.September, 1, 0, 0, 0, 0, ny)},
	}
	for _, c := range cases {
		got, err := parseDay(c.in, now)
		if err != nil {
			t.Errorf("parseDay(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseDay(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	if _, err := parseDay("someday", now); err == nil {
		t.Error("expected an error for an unparseable day")
	}
}

// A Pacific machine at 9pm is already on the next day in Brooklyn, and shop
// hours are evaluated there -- so "today" must resolve to the New York day,
// not the machine's. Regression for the slots timezone bug.
func TestParseDayAnchorsToNewYork(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skip("no tz database")
	}
	// Wed 9:30pm PT = Thu 12:30am ET.
	now := time.Date(2026, time.September, 2, 21, 30, 0, 0, la)
	got, err := parseDay("today", now)
	if err != nil {
		t.Fatal(err)
	}
	if d := got.In(catalog.ShopLocation()).Day(); d != 3 {
		t.Errorf("\"today\" resolved to NY day %d, want 3 (it is already Thursday in Brooklyn)", d)
	}
}

func TestParseLogDateLooksBackward(t *testing.T) {
	now := time.Date(2026, time.August, 7, 14, 30, 0, 0, time.UTC)

	got, err := parseLogDate("yesterday", now)
	if err != nil {
		t.Fatalf("parseLogDate: %v", err)
	}
	if want := time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("yesterday = %v, want %v", got, want)
	}

	if _, err := parseLogDate("next tuesday", now); err == nil {
		t.Error("expected an error for an unparseable log date")
	}
}

var testNow = time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)

func result(name string, walk int, hasOrigin bool, priceMin, priceMax int, rating float64) catalog.Result {
	return catalog.Result{
		Shop: catalog.Shop{
			ID: name, Name: name,
			PriceMin: priceMin, PriceMax: priceMax, Rating: rating,
		},
		WalkMin: walk, HasOrigin: hasOrigin,
	}
}

func TestBrowseTableDropsUninformativeColumns(t *testing.T) {
	ui.SetColor(false)

	// Greenpoint: every shop is call-only with no price and no rating.
	rows, right := browseTable([]catalog.Result{
		result("A", 2, true, 0, 0, 0),
		result("B", 4, true, 0, 0, 0),
	}, testNow, nil)
	if got := len(rows[0]); got != 2 {
		t.Errorf("got %d columns, want 2 (name + walk): %q", got, rows[0])
	}
	if !right[1] {
		t.Error("walk column should stay right-aligned after the drop")
	}
}

func TestBrowseTableKeepsColumnsWithAnyData(t *testing.T) {
	ui.SetColor(false)

	// One shop with a rating is enough to earn the column for the whole set.
	rows, _ := browseTable([]catalog.Result{
		result("A", 2, true, 0, 0, 0),
		result("B", 4, true, 0, 0, 5.0),
	}, testNow, nil)
	if got := len(rows[0]); got != 3 {
		t.Errorf("got %d columns, want 3 (name + walk + rating): %q", got, rows[0])
	}
}

func TestBrowseTableAllColumns(t *testing.T) {
	ui.SetColor(false)
	rows, right := browseTable([]catalog.Result{result("A", 2, true, 40, 65, 4.9)}, testNow, nil)
	if got := len(rows[0]); got != 4 {
		t.Errorf("got %d columns, want 4: %q", got, rows[0])
	}
	if !right[1] || !right[2] {
		t.Errorf("walk and price should be right-aligned: %v", right)
	}
}

func TestBrowseTableWithoutOriginDropsWalk(t *testing.T) {
	ui.SetColor(false)
	rows, _ := browseTable([]catalog.Result{result("A", 0, false, 40, 65, 4.9)}, testNow, nil)
	for _, cell := range rows[0] {
		if strings.HasSuffix(cell, " min") {
			t.Errorf("walk column survived with no origin: %q", rows[0])
		}
	}
}

// WalkMin is measured from the origin, so it must be labelled with the origin.
func TestShopSubtitleMeasuresFromTheOrigin(t *testing.T) {
	r := catalog.Result{WalkMin: 18, HasOrigin: true}
	r.Stop.Name = "Greenpoint Av"
	shop := catalog.Shop{Address: "197 Franklin St"}

	got := shopSubtitle(shop, r, "Nassau Av")
	if !strings.Contains(got, "18 min walk from Nassau Av") {
		t.Errorf("subtitle = %q, want the walk measured from Nassau Av", got)
	}
	if strings.Contains(got, "from Greenpoint Av") {
		t.Errorf("subtitle labels the walk with the shop's own stop: %q", got)
	}
}

func TestShopSubtitleWithoutOrigin(t *testing.T) {
	got := shopSubtitle(catalog.Shop{Address: "197 Franklin St"}, catalog.Result{}, "")
	if strings.Contains(got, "walk") {
		t.Errorf("subtitle = %q, want no walk claim without an origin", got)
	}
}

// Reaching a shop by id (from history) must produce the same distance context
// a search would have attached, or the detail screen loses its walk time.
func TestResultForAttachesDistanceContext(t *testing.T) {
	a := &app{}
	graham, _ := geo.StopByID("graham")
	shop := catalog.Shop{ID: "x", Name: "X", Point: geo.Point{Lat: 40.71857, Lon: -73.94309}}

	got := a.resultFor(shop, &graham.Point)
	if !got.HasOrigin {
		t.Fatal("HasOrigin false with an origin supplied")
	}
	if got.WalkMin <= 0 {
		t.Errorf("WalkMin = %d, want a positive walk time", got.WalkMin)
	}
	if got.Stop.ID != "graham" {
		t.Errorf("Stop = %q, want graham", got.Stop.ID)
	}
}

func TestResultForWithoutOrigin(t *testing.T) {
	a := &app{}
	shop := catalog.Shop{ID: "x", Point: geo.Point{Lat: 40.71857, Lon: -73.94309}}
	if got := a.resultFor(shop, nil); got.HasOrigin || got.WalkMin != 0 {
		t.Errorf("got %+v, want no distance context", got)
	}
}

func TestResultForUnlocatedShop(t *testing.T) {
	a := &app{}
	graham, _ := geo.StopByID("graham")
	if got := a.resultFor(catalog.Shop{ID: "x"}, &graham.Point); got.HasOrigin {
		t.Error("a shop with no coordinates should get no walk time")
	}
}

// The browse loop retries after an empty result set, so relax must always make
// progress and must eventually report exhaustion. Without that the UI spins
// forever with nothing on screen.
func TestRelaxAlwaysTerminates(t *testing.T) {
	openNow, maxPrice := true, 40

	var notes []string
	for i := 0; ; i++ {
		if i > 20 {
			t.Fatal("relax never reported exhaustion")
		}
		note, ok := relax(&openNow, &maxPrice)
		if !ok {
			break
		}
		if note == "" {
			t.Error("relaxed a filter without explaining why")
		}
		notes = append(notes, note)
	}

	if len(notes) != 2 {
		t.Errorf("relaxed %d filters, want 2: %v", len(notes), notes)
	}
	if openNow || maxPrice != 0 {
		t.Errorf("filters not fully relaxed: openNow=%v maxPrice=%d", openNow, maxPrice)
	}
}

func TestRelaxDropsOpenNowFirst(t *testing.T) {
	// Open-now is the most likely culprit and the least destructive to drop:
	// clearing a price cap the user typed is more surprising.
	openNow, maxPrice := true, 40
	if _, ok := relax(&openNow, &maxPrice); !ok {
		t.Fatal("expected a relaxation")
	}
	if openNow {
		t.Error("open-now should be dropped first")
	}
	if maxPrice != 40 {
		t.Errorf("price cap = %d, should be untouched on the first relax", maxPrice)
	}
}

func TestRelaxExhaustedIsANoop(t *testing.T) {
	openNow, maxPrice := false, 0
	if note, ok := relax(&openNow, &maxPrice); ok || note != "" {
		t.Errorf("got (%q, %v), want exhausted", note, ok)
	}
}

// The index groups shops by stop in outbound order; a shop is measured from
// its own stop, and headings sit exactly where a group starts.
func TestStopGroupedRowsPutHeadingsBeforeEachGroup(t *testing.T) {
	ui.SetColor(false)
	graham, _ := geo.StopByID("graham")
	grand, _ := geo.StopByID("grand")
	groups := []stopGroup{
		{stop: graham, shops: []catalog.Result{result("A", 3, true, 40, 60, 5), result("B", 6, true, 0, 0, 0)}},
		{stop: grand, shops: []catalog.Result{result("C", 2, true, 30, 30, 4.8)}},
	}
	rows, _, results, at := stopGroupedRows(groups, testNow, nil)
	if len(rows) != 5 || len(at) != 5 {
		t.Fatalf("got %d rows / %d map entries, want 5", len(rows), len(at))
	}
	if at[0] != -1 || rows[0][0] != "Graham Av" {
		t.Errorf("row 0 = %v (at %d), want the Graham heading", rows[0], at[0])
	}
	if at[3] != -1 || rows[3][0] != "Grand St" {
		t.Errorf("row 3 = %v (at %d), want the Grand heading", rows[3], at[3])
	}
	if results[at[4]].Shop.Name != "C" {
		t.Errorf("row 4 maps to %s, want C", results[at[4]].Shop.Name)
	}
}

func TestCountUnpriced(t *testing.T) {
	got := countUnpriced([]catalog.Result{
		result("a", 1, true, 0, 0, 0),
		result("b", 1, true, 40, 65, 0),
		result("c", 1, true, 0, 0, 4.5),
	})
	if got != 2 {
		t.Errorf("countUnpriced = %d, want 2", got)
	}
}

// Every booking kind must produce a label. A kind added to the catalog without
// one here silently rendered as "website", which is wrong for vagaro.
func TestHandoffLabelCoversEveryKind(t *testing.T) {
	for _, k := range []catalog.BookingKind{
		catalog.KindBooksy, catalog.KindFresha, catalog.KindSquare, catalog.KindVagaro,
	} {
		shop := catalog.Shop{Booking: catalog.Booking{Kind: k}}
		if got := handoffLabel(shop); got != string(k) {
			t.Errorf("handoffLabel(%s) = %q, want %q", k, got, k)
		}
	}
	phone := catalog.Shop{Phone: "+13475991874", Booking: catalog.Booking{Kind: catalog.KindPhone}}
	if got := handoffLabel(phone); got != "(347) 599-1874" {
		t.Errorf("phone label = %q", got)
	}
	if got := handoffLabel(catalog.Shop{Booking: catalog.Booking{Kind: catalog.KindLink}}); got != "website" {
		t.Errorf("link label = %q", got)
	}
}

// slots exists to answer "when can I get in", so a closed shop must say so.
func TestOpenCellReportsClosed(t *testing.T) {
	ui.SetColor(false)
	// 23:00 UTC is 19:00 in New York, comfortably after a 16:00 close. Hours
	// are shop-local, so a UTC hour is not the hour the shop experiences.
	fri := time.Date(2026, time.August, 7, 23, 0, 0, 0, time.UTC)

	closed := catalog.Shop{Hours: catalog.Hours{"fri": "09:00-16:00"}}
	if got := openCell(closed, fri); !strings.Contains(got, "opens") {
		t.Errorf("closed shop rendered %q, want an opening time", got)
	}
	unknown := catalog.Shop{}
	if got := openCell(unknown, fri); got != "—" {
		t.Errorf("unknown hours rendered %q, want the placeholder", got)
	}
}

// quiet swaps stdout for the duration of fn, so command tests don't spray
// their tables through the test log.
func quiet(t *testing.T, fn func()) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan struct{})
	go func() { io.Copy(io.Discard, r); close(done) }()
	defer func() { w.Close(); <-done; os.Stdout = old }()
	fn()
}

// Zero and empty are meaningful values here -- "0" means "learn the interval
// from history" -- so applying flags by value made those settings unclearable.
func TestMeAppliesOnlyFlagsActuallyTyped(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FADE_HOME", dir)

	run := func(args ...string) {
		a, err := newApp()
		if err != nil {
			t.Fatal(err)
		}
		quiet(t, func() {
			if err := a.me(args); err != nil {
				t.Fatalf("me %v: %v", args, err)
			}
		})
	}

	run("--set-home", "graham", "--set-max-price", "45", "--set-interval", "14", "--set-name", "Kh")
	a, _ := newApp()
	if a.state.Profile.MaxPrice != 45 || a.state.Profile.IntervalDays != 14 || a.state.Profile.Name != "Kh" {
		t.Fatalf("setup failed: %+v", a.state.Profile)
	}

	run("--set-max-price", "0", "--set-interval", "0", "--set-name", "")
	a, _ = newApp()
	if a.state.Profile.MaxPrice != 0 {
		t.Errorf("max price = %d, want cleared", a.state.Profile.MaxPrice)
	}
	if a.state.Profile.IntervalDays != 0 {
		t.Errorf("interval = %d, want cleared", a.state.Profile.IntervalDays)
	}
	if a.state.Profile.Name != "" {
		t.Errorf("name = %q, want cleared", a.state.Profile.Name)
	}
	// Untyped flags must not be disturbed by the ones that were typed.
	if a.state.Profile.HomeStop != "graham" {
		t.Errorf("home stop = %q, want graham", a.state.Profile.HomeStop)
	}
}

// The usage text promises "Run 'fade-cli <command> -h' for flags", and two
// commands didn't honour it: `dev -h` errored, and `again -h` printed book's
// usage under a heading the user never typed.
func TestEveryCommandAnswersDashH(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FADE_HOME", dir)

	a, err := newApp()
	if err != nil {
		t.Fatal(err)
	}

	cmds := map[string]func([]string) error{
		"find":  a.find,
		"slots": a.slots,
		"book":  a.book,
		"again": a.againCmd,
		"log":   a.log,
		"due":   a.due,
		"me":    a.me,
		"dev":   a.dev,
	}

	// -h goes to stderr for flag sets and stdout for hand-written usage; hide
	// both so the test log stays readable.
	oldErr := os.Stderr
	devNull, _ := os.Open(os.DevNull)
	os.Stderr = devNull
	defer func() { os.Stderr = oldErr; devNull.Close() }()

	for name, fn := range cmds {
		var err error
		quiet(t, func() { err = fn([]string{"-h"}) })
		if err != nil {
			t.Errorf("%s -h returned %v, want nil -- asking for help is not an error", name, err)
		}
	}
}
