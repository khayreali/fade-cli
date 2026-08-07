package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestAddCutKeepsHistoryNewestFirst(t *testing.T) {
	s := &State{}
	s.AddCut(Cut{Date: day(2026, time.June, 14), ShopName: "old"})
	s.AddCut(Cut{Date: day(2026, time.July, 26), ShopName: "new"})
	s.AddCut(Cut{Date: day(2026, time.July, 5), ShopName: "mid"})

	want := []string{"new", "mid", "old"}
	for i, w := range want {
		if s.Cuts[i].ShopName != w {
			t.Errorf("position %d = %q, want %q", i, s.Cuts[i].ShopName, w)
		}
	}
}

func TestIntervalUsesMedianNotMean(t *testing.T) {
	s := &State{}
	// Three ~21-day gaps and one six-month lapse. A mean would be dragged way
	// out; the median should shrug the outlier off.
	for _, d := range []time.Time{
		day(2025, time.October, 1),
		day(2026, time.June, 1),
		day(2026, time.June, 22),
		day(2026, time.July, 13),
	} {
		s.AddCut(Cut{Date: d})
	}

	got := s.Interval()
	if got < 20*24*time.Hour || got > 22*24*time.Hour {
		t.Errorf("Interval = %v, want about 21 days despite the outlier", got)
	}
}

func TestIntervalFallsBackBeforeEnoughHistory(t *testing.T) {
	s := &State{}
	if got := s.Interval(); got != DefaultCutInterval {
		t.Errorf("empty history Interval = %v, want default", got)
	}
	s.AddCut(Cut{Date: day(2026, time.July, 1)})
	if got := s.Interval(); got != DefaultCutInterval {
		t.Errorf("one-cut Interval = %v, want default", got)
	}
}

func TestProfileIntervalOverridesLearned(t *testing.T) {
	s := &State{Profile: Profile{IntervalDays: 14}}
	for _, d := range []time.Time{day(2026, time.June, 1), day(2026, time.June, 22), day(2026, time.July, 13)} {
		s.AddCut(Cut{Date: d})
	}
	if got := s.Interval(); got != 14*24*time.Hour {
		t.Errorf("Interval = %v, want the configured 14 days", got)
	}
}

func TestDueIn(t *testing.T) {
	s := &State{Profile: Profile{IntervalDays: 21}}
	s.AddCut(Cut{Date: day(2026, time.July, 26)})

	d, ok := s.DueIn(day(2026, time.August, 7))
	if !ok {
		t.Fatal("DueIn reported no history")
	}
	if want := 9 * 24 * time.Hour; d != want {
		t.Errorf("DueIn = %v, want %v", d, want)
	}

	// Past the interval, the remaining time should go negative rather than
	// clamping -- "overdue by 5 days" is the useful message.
	if d, _ := s.DueIn(day(2026, time.August, 21)); d >= 0 {
		t.Errorf("DueIn after the due date = %v, want negative", d)
	}
}

func TestDueInWithNoHistory(t *testing.T) {
	s := &State{}
	if _, ok := s.DueIn(time.Now()); ok {
		t.Error("DueIn should report no history when there are no cuts")
	}
}

func TestRegularsRanksByVisitsThenRecency(t *testing.T) {
	s := &State{}
	s.AddCut(Cut{Date: day(2026, time.June, 1), ShopID: "a", ShopName: "A", Price: 40, Tip: 10})
	s.AddCut(Cut{Date: day(2026, time.July, 1), ShopID: "a", ShopName: "A", Price: 40, Tip: 10})
	s.AddCut(Cut{Date: day(2026, time.July, 20), ShopID: "b", ShopName: "B", Price: 55})

	got := s.Regulars()
	if len(got) != 2 {
		t.Fatalf("got %d regulars, want 2", len(got))
	}
	if got[0].ShopID != "a" {
		t.Errorf("top regular = %s, want a", got[0].ShopID)
	}
	if got[0].Visits != 2 || got[0].Spent != 100 {
		t.Errorf("A: visits=%d spent=%d, want 2 and 100", got[0].Visits, got[0].Spent)
	}
	if !got[0].Last.Equal(day(2026, time.July, 1)) {
		t.Errorf("A last visit = %v", got[0].Last)
	}
}

func TestRegularsGroupsUncatalogedShopsByName(t *testing.T) {
	s := &State{}
	s.AddCut(Cut{Date: day(2026, time.June, 1), ShopName: "Some Guy On Knickerbocker"})
	s.AddCut(Cut{Date: day(2026, time.July, 1), ShopName: "Some Guy On Knickerbocker"})

	got := s.Regulars()
	if len(got) != 1 || got[0].Visits != 2 {
		t.Errorf("got %+v, want one entry with 2 visits", got)
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FADE_HOME", dir)

	s, err := Load()
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	s.Profile.HomeStop = "graham"
	s.AddCut(Cut{Date: day(2026, time.July, 26), ShopID: "cabello-brooklyn", ShopName: "Cabello", Price: 55, Tip: 10})
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if got.Profile.HomeStop != "graham" {
		t.Errorf("HomeStop = %q", got.Profile.HomeStop)
	}
	if len(got.Cuts) != 1 || got.Cuts[0].Total() != 65 {
		t.Errorf("cuts round-tripped as %+v", got.Cuts)
	}
}

func TestProfileOriginPrefersExplicitPoint(t *testing.T) {
	p := Profile{HomeStop: "graham"}
	if _, ok := p.Origin(); !ok {
		t.Fatal("stop-only profile should resolve an origin")
	}

	if _, ok := (Profile{}).Origin(); ok {
		t.Error("empty profile should have no origin")
	}
}

// state.json is documented as hand-editable, and the natural way to type a
// history is oldest-first. Every reader here assumes newest-first, so Load
// must normalise it -- otherwise `due` reports the wrong last cut and `again`
// rebooks the wrong shop.
func TestLoadSortsHandEditedCuts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FADE_HOME", dir)

	raw := `{"profile":{"home_stop":"graham"},"cuts":[
	  {"date":"2026-06-20T12:00:00Z","shop_id":"a","shop_name":"Oldest"},
	  {"date":"2026-08-01T12:00:00Z","shop_id":"c","shop_name":"Newest"},
	  {"date":"2026-07-11T12:00:00Z","shop_id":"b","shop_name":"Middle"}]}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	last, ok := s.LastCut()
	if !ok {
		t.Fatal("no cuts loaded")
	}
	if last.ShopName != "Newest" {
		t.Errorf("LastCut = %q, want Newest", last.ShopName)
	}
	for i, want := range []string{"Newest", "Middle", "Oldest"} {
		if s.Cuts[i].ShopName != want {
			t.Errorf("position %d = %q, want %q", i, s.Cuts[i].ShopName, want)
		}
	}
	// With the order fixed, the learned cadence is the real 21-day gap.
	if got := s.Interval(); got < 20*24*time.Hour || got > 22*24*time.Hour {
		t.Errorf("Interval = %v, want about 21 days", got)
	}
}
