package ui

import (
	"io"
	"testing"
)

// A grouped list: headings at 0 and 3, shops between.
func sectioned() *List {
	return &List{Title: "t", Out: io.Discard, Filterable: true,
		Rows: [][]string{
			{"Graham Av", "2 shops"}, {"alpha"}, {"beta"},
			{"Grand St", "1 shop"}, {"gamma"},
		},
		Sections: map[int]bool{0: true, 3: true},
	}
}

func TestCursorStartsBelowTheFirstHeading(t *testing.T) {
	got := run(sectioned(), Key{Type: KeyEnter})
	if !got.OK || got.Index != 1 {
		t.Errorf("got %+v, want alpha (row 1)", got)
	}
}

func TestCursorSkipsHeadingsGoingDownAndUp(t *testing.T) {
	// alpha -> beta -> (skip Grand St) -> gamma
	got := run(sectioned(), Key{Type: KeyDown}, Key{Type: KeyDown}, Key{Type: KeyEnter})
	if got.Index != 4 {
		t.Errorf("down twice landed on row %d, want gamma (4)", got.Index)
	}
	// gamma -> (skip) -> beta
	got = run(sectioned(), Key{Type: KeyEnd}, Key{Type: KeyUp}, Key{Type: KeyEnter})
	if got.Index != 2 {
		t.Errorf("up from the end landed on row %d, want beta (2)", got.Index)
	}
}

func TestUpFromFirstShopStaysPut(t *testing.T) {
	got := run(sectioned(), Key{Type: KeyUp}, Key{Type: KeyUp}, Key{Type: KeyEnter})
	if got.Index != 1 {
		t.Errorf("got row %d, want alpha (1) -- the heading above must not take the cursor", got.Index)
	}
}

func TestHeadingsVanishWhileFiltering(t *testing.T) {
	l := sectioned()
	seq := []Key{{Type: KeyRune, Rune: '/'}, {Type: KeyRune, Rune: 'a'}}
	l.Run(&keys{seq: seq}, 0)
	for _, idx := range l.view {
		if l.Sections[idx] {
			t.Errorf("heading row %d still visible under a search", idx)
		}
	}
	if len(l.view) != 3 {
		t.Errorf("%d rows match 'a', want alpha, beta, gamma", len(l.view))
	}
}

func TestDigitsCountShopsNotHeadings(t *testing.T) {
	// "2" is the second shop (beta, row 2), not the second row (alpha).
	got := run(sectioned(), Key{Type: KeyRune, Rune: '2'}, Key{Type: KeyEnter})
	if got.Index != 2 {
		t.Errorf("digit 2 chose row %d, want beta (2)", got.Index)
	}
}

func TestPositionCountsShops(t *testing.T) {
	l := sectioned()
	l.refilter()
	if n := l.selectableBefore(len(l.view)); n != 3 {
		t.Errorf("selectable rows = %d, want 3", n)
	}
}

// PageUp near the top of a grouped list must reach the first shop, not stall
// because the paged target lands on the row-0 heading.
func TestPageUpReachesTopShopPastAHeading(t *testing.T) {
	l := sectioned() // [head0, alpha1, beta2, head3, gamma4]
	l.Height = 3
	// Cursor on beta (row 2); PageUp = move(-3) clamps the target to row 0,
	// the heading. It must settle onto the top shop (row 1), not stall on
	// beta the way the old edge guard did.
	got := run(l, Key{Type: KeyDown}, Key{Type: KeyPageUp}, Key{Type: KeyEnter})
	if got.Index == 2 {
		t.Error("PageUp stalled on beta")
	}
	if l.Sections[got.Index] {
		t.Errorf("PageUp landed on heading row %d", got.Index)
	}
	if got.Index != 1 {
		t.Errorf("PageUp landed on row %d, want the top shop (1)", got.Index)
	}
}
