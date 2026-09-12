package main

import (
	"bufio"
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

func (a *app) book(args []string) error {
	fs := flag.NewFlagSet("book", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli book <shop> [flags]

Choose a service, day and time, then review before continuing to booking.
Use --print for just the link, or -y to open the shop's page directly.

FLAGS
`)
		fs.PrintDefaults()
	}
	var (
		yes     = fs.Bool("y", false, "skip the confirmation prompt")
		print   = fs.Bool("print", false, "print the target instead of opening it")
		at      = fs.String("at", "", "request a time, like \"fri 3pm\" -- runs the manual connector")
		service = fs.String("service", "", "what to book (default haircut)")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}

	name := strings.Join(fs.Args(), " ")
	if name == "" {
		fs.Usage()
		return errors.New("name a shop")
	}

	shop, err := a.mustResolve(name)
	if err != nil {
		return err
	}
	// A requested time means the manual connector: prepare the ask, pick a
	// channel, record the appointment.
	if *at != "" {
		return a.manualBook(shop, *at, *service, *yes, *print)
	}
	if !*yes && !*print && ui.IsInteractive() {
		if raw, ok := ui.EnterRaw(); ok {
			defer raw.Restore()
			return a.bookingScreen(raw, shop, *service)
		}
	}
	action := a.reg.For(shop).Handoff(shop)
	if action.Type == provider.ActionNone {
		return fmt.Errorf("%s: %s", shop.Name, action.Label)
	}
	if *print {
		fmt.Println(action.Target)
		return nil
	}

	now := time.Now()
	fmt.Println()
	fmt.Printf("  %s\n", ui.Bold(shop.Name))
	fmt.Printf("  %s\n", ui.Dim(shop.Address))
	if shop.Rating > 0 {
		fmt.Printf("  %s   %s\n", ui.Stars(shop.Rating, shop.Reviews), priceCell(shop))
	}
	fmt.Printf("  %s\n\n", bookingStatus(shop, now))

	// For a walk-in shop that's open, turning up is the answer -- saying only
	// "call them" turns a shop you could be sitting in within ten minutes into
	// a phone call you have to make first.
	if l := shop.WalkIn.Label(); l != "" {
		if st, _ := shop.Hours.OpenAt(now); st == catalog.StatusOpen {
			fmt.Printf("  %s\n\n", ui.Green("open now — "+l+", no appointment needed"))
		} else {
			fmt.Printf("  %s\n\n", ui.Cyan(l))
		}
	}

	// Calling a shop that shut two hours ago is the most common way this
	// command wastes someone's time, so say so before they commit to it.
	if action.Type == provider.ActionCall {
		fmt.Printf("  %s %s\n", ui.Dim("books by phone"), ui.Bold(provider.PrettyPhone(shop.Phone)))
	} else {
		fmt.Printf("  %s %s\n", ui.Dim(bookingPhrase(shop)+" —"), action.Target)
	}
	fmt.Println()

	if *yes {
		return a.doHandoff(shop, action)
	}
	return a.offerHandoff(shop, action)
}

// bookingStatus is the open/closed line shown before you act.
func bookingStatus(shop catalog.Shop, now time.Time) string {
	label := shop.Hours.StatusLabel(now)
	switch st, _ := shop.Hours.OpenAt(now); st {
	case catalog.StatusOpen:
		return ui.Green("● " + label)
	case catalog.StatusClosed:
		return ui.Yellow("○ " + label)
	default:
		return ui.Dim("○ hours unknown")
	}
}

// offerHandoff gives the user the choice the situation actually calls for:
// dial now, or take the number away and call when they're open.
func (a *app) offerHandoff(shop catalog.Shop, action provider.Action) error {
	verb, target := "call now", provider.PrettyPhone(shop.Phone)
	if action.Type == provider.ActionOpen {
		verb, target = "open in browser", action.Target
	}
	// Don't offer to "call now" a shop that shut hours ago; copying the number
	// for later is the sensible default there.
	if st, _ := shop.Hours.OpenAt(time.Now()); st == catalog.StatusClosed && action.Type == provider.ActionCall {
		verb = "call anyway"
	}

	fmt.Printf("  %s   %s   %s\n",
		ui.Accent("[enter]")+" "+verb,
		ui.Accent("[c]")+" copy",
		ui.Accent("[n]")+" cancel")

	choice, ok := ui.Line("")
	if !ok {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "y", "yes":
		return a.doHandoff(shop, action)
	case "c":
		if provider.Copy(target) {
			fmt.Printf("\n  %s %s\n", ui.Green("copied"), ui.Dim(target))
		} else {
			fmt.Printf("\n  %s %s\n", ui.Yellow("no clipboard tool —"), target)
		}
		return nil
	default:
		fmt.Println(ui.Dim("  cancelled"))
		return nil
	}
}

func (a *app) doHandoff(shop catalog.Shop, action provider.Action) error {
	if err := provider.Open(action.Target); err != nil {
		return fmt.Errorf("opening %s: %w", action.Target, err)
	}
	ui.Hint("after your cut: fade-cli log add --shop %s --price <$>", shop.ID)
	return nil
}

// confirm defaults to yes, since the user already typed the shop name. A bare
// enter should do the obvious thing. Non-interactive input declines rather
// than opening a browser nobody asked for.
func confirm(prompt string) bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}
