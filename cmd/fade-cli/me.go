package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"fadecli/internal/geo"
	"fadecli/internal/ui"
)

func (a *app) me(args []string) error {
	fs := flag.NewFlagSet("me", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli me [flags]

Show your profile, or set it. Your home stop is what `+"`fade-cli find`"+` measures from.

FLAGS
`)
		fs.PrintDefaults()
	}
	var (
		setHome     = fs.String("set-home", "", "home stop id (see `fade-cli stops`)")
		setName     = fs.String("set-name", "", "your name, for bookings")
		setPhone    = fs.String("set-phone", "", "your phone, for bookings")
		setEmail    = fs.String("set-email", "", "your email, for bookings")
		setMax      = fs.Int("set-max-price", 0, "default price ceiling for find")
		setInterval = fs.Int("set-interval", 0, "days between cuts (0 = learn it from history)")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}

	// Apply flags the user actually typed, not flags with a non-empty value.
	// Inferring intent from the value makes every zero and empty string
	// unsettable, so `--set-interval 0` -- documented as "learn it from
	// history" -- silently did nothing.
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	changed := len(set) > 0

	if set["set-home"] {
		stop, ok := geo.StopByID(*setHome)
		if !ok {
			return fmt.Errorf("unknown stop %q -- run `fade-cli stops` to see them", *setHome)
		}
		a.state.Profile.HomeStop = stop.ID
		a.state.Profile.HomePoint = nil // an explicit stop supersedes an old point
	}
	for _, f := range []struct {
		name string
		val  *string
		dst  *string
	}{
		{"set-name", setName, &a.state.Profile.Name},
		{"set-phone", setPhone, &a.state.Profile.Phone},
		{"set-email", setEmail, &a.state.Profile.Email},
	} {
		if set[f.name] {
			*f.dst = *f.val
		}
	}
	if set["set-max-price"] {
		a.state.Profile.MaxPrice = *setMax
	}
	if set["set-interval"] {
		a.state.Profile.IntervalDays = *setInterval
	}

	if changed {
		if err := a.state.Save(); err != nil {
			return fmt.Errorf("saving: %w", err)
		}
	}

	p := a.state.Profile
	home := ui.Dim("not set")
	if s, ok := geo.StopByID(p.HomeStop); ok {
		home = fmt.Sprintf("%s %s", s.Name, ui.Dim(s.Line+" train"))
	}

	fmt.Println()
	t := ui.NewTable()
	t.Row(ui.Dim("home"), home)
	t.Row(ui.Dim("name"), orUnset(p.Name))
	t.Row(ui.Dim("phone"), orUnset(p.Phone))
	t.Row(ui.Dim("email"), orUnset(p.Email))
	t.Row(ui.Dim("max price"), orUnsetInt(p.MaxPrice, "$"))
	interval, src := a.state.IntervalWithSource()
	t.Row(ui.Dim("cut every"), ui.Duration(interval)+"  "+ui.Dim("("+src.Label()+")"))
	t.Row(ui.Dim("cuts logged"), fmt.Sprint(len(a.state.Cuts)))
	t.Render(os.Stdout)
	fmt.Println()

	if d, ok := a.state.DueIn(time.Now()); ok {
		if d < 0 {
			ui.Hint("you're overdue by %s", ui.Duration(-d))
		} else {
			ui.Hint("next cut due in %s", ui.Duration(d))
		}
	}
	if p.HomeStop == "" {
		ui.Hint("set a home stop for walk times: fade-cli me --set-home graham")
	}
	return nil
}

func (a *app) stops(args []string) error {
	counts := map[string]int{}
	for _, s := range a.cat.Shops {
		if stop, ok := s.NearestStop(); ok {
			counts[stop.ID]++
		}
	}

	fmt.Println()
	for _, cor := range geo.Corridors {
		total := 0
		for _, s := range cor.Stops {
			total += counts[s.ID]
		}
		fmt.Printf("%s %s\n", ui.Bold(cor.Name),
			ui.Dim(fmt.Sprintf("%s train · %d shops", cor.Line, total)))

		t := ui.NewTable("id", "stop", "shops").Indent("  ")
		t.RightAlign(2)
		for _, s := range cor.Stops {
			n := ui.Dim("0")
			if c := counts[s.ID]; c > 0 {
				n = fmt.Sprint(c)
			}
			t.Row(ui.Cyan(s.ID), s.Name, n)
		}
		t.Render(os.Stdout)
		fmt.Println()
	}

	ui.Hint("fade-cli find --near <id>   fade-cli find --to <id>")
	return nil
}

func orUnset(s string) string {
	if s == "" {
		return ui.Dim("not set")
	}
	return s
}

func orUnsetInt(v int, prefix string) string {
	if v == 0 {
		return ui.Dim("not set")
	}
	return fmt.Sprintf("%s%d", prefix, v)
}
