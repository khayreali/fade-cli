package main

import (
	"flag"
	"io"
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
	// A Friday, so the weekday cases have a fixed reference.
	now := time.Date(2026, time.August, 7, 14, 30, 0, 0, time.UTC)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"today", time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)},
		{"tomorrow", time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)},
		{"mon", time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)},
		// "friday" on a Friday means next Friday, not today.
		{"friday", time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)},
		{"2026-09-01", time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)},
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
	}, testNow)
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
	}, testNow)
	if got := len(rows[0]); got != 3 {
		t.Errorf("got %d columns, want 3 (name + walk + rating): %q", got, rows[0])
	}
}

func TestBrowseTableAllColumns(t *testing.T) {
	ui.SetColor(false)
	rows, right := browseTable([]catalog.Result{result("A", 2, true, 40, 65, 4.9)}, testNow)
	if got := len(rows[0]); got != 4 {
		t.Errorf("got %d columns, want 4: %q", got, rows[0])
	}
	if !right[1] || !right[2] {
		t.Errorf("walk and price should be right-aligned: %v", right)
	}
}

func TestBrowseTableWithoutOriginDropsWalk(t *testing.T) {
	ui.SetColor(false)
	rows, _ := browseTable([]catalog.Result{result("A", 0, false, 40, 65, 4.9)}, testNow)
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
