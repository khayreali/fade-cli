package ui

import (
	"bufio"
	"strings"
	"testing"
)

func reader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func TestIntUsesDefaultOnBareEnter(t *testing.T) {
	SetColor(false)
	got, ok := intFrom(reader("\n"), "", 55)
	if !ok || got != 55 {
		t.Errorf("got (%d, %v), want (55, true)", got, ok)
	}
}

func TestIntStripsDollarSign(t *testing.T) {
	got, ok := intFrom(reader("$42\n"), "", 0)
	if !ok || got != 42 {
		t.Errorf("got (%d, %v), want (42, true)", got, ok)
	}
}

func TestIntRetriesOnGarbage(t *testing.T) {
	got, ok := intFrom(reader("abc\n17\n"), "", 0)
	if !ok || got != 17 {
		t.Errorf("got (%d, %v), want (17, true)", got, ok)
	}
}

func TestIntRejectsNegatives(t *testing.T) {
	got, ok := intFrom(reader("-5\n8\n"), "", 0)
	if !ok || got != 8 {
		t.Errorf("got (%d, %v), want (8, true) -- negative should be retried", got, ok)
	}
}

func TestIntReportsEOF(t *testing.T) {
	if _, ok := intFrom(reader(""), "", 0); ok {
		t.Error("EOF should report ok=false")
	}
}

func TestYesDefaultsToYes(t *testing.T) {
	for _, in := range []string{"\n", "y\n", "Y\n", "yes\n"} {
		if !yesFrom(reader(in), "") {
			t.Errorf("input %q should be yes", in)
		}
	}
	for _, in := range []string{"n\n", "no\n", "nope\n"} {
		if yesFrom(reader(in), "") {
			t.Errorf("input %q should be no", in)
		}
	}
}

func TestYesDeclinesOnEOF(t *testing.T) {
	// Ctrl-D at a confirmation must not be read as consent -- the next step
	// opens a browser or dials a shop.
	if yesFrom(reader(""), "") {
		t.Error("EOF should decline")
	}
}
