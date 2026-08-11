package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/provider"
	"fadecli/internal/store"
	"fadecli/internal/ui"
)

// manualBook is the manual connector's front end: validate the time, show the
// prepared request, let the user fire it through a channel, and record the
// appointment. Shared by `book --at` and the interactive shop screen.
func (a *app) manualBook(shop catalog.Shop, whenStr, service string, yes, printOnly bool) error {
	now := time.Now()
	when, err := parseWhen(whenStr, now)
	if err != nil {
		return err
	}
	plan, err := provider.PlanManual(shop, a.state.Profile.Name, service, when, now)
	if err != nil {
		return err
	}

	if printOnly {
		fmt.Println(plan.Message)
		for _, c := range plan.Channels {
			if c.Action.Target != "" {
				fmt.Println(c.Action.Target)
			}
		}
		return nil
	}

	fmt.Println()
	fmt.Printf("  %s  %s\n", ui.Bold(shop.Name), ui.Dim(shop.Address))
	fmt.Printf("  %s %s\n", ui.Dim("requesting"), ui.Bold(catalog.InShopTime(when).Format("Mon Jan 2, 3:04pm")))
	if plan.Warning != "" {
		ui.Warn("%s", plan.Warning)
	}
	fmt.Printf("\n  %s\n  %s\n\n", ui.Dim("your message:"), plan.Message)

	for i, c := range plan.Channels {
		fmt.Printf("  %s %s\n", ui.Accent(fmt.Sprintf("[%d]", i+1)), c.Label)
	}
	fmt.Printf("  %s cancel\n\n", ui.Accent("[n]"))

	pick := 0
	if !yes {
		in, ok := ui.Line("")
		if !ok {
			return nil
		}
		in = strings.TrimSpace(strings.ToLower(in))
		if in == "n" || in == "no" {
			fmt.Println(ui.Dim("  cancelled"))
			return nil
		}
		if in != "" {
			n := 0
			for _, r := range in {
				if r < '0' || r > '9' {
					n = -1
					break
				}
				n = n*10 + int(r-'0')
			}
			if n < 1 || n > len(plan.Channels) {
				return fmt.Errorf("pick 1-%d", len(plan.Channels))
			}
			pick = n - 1
		}
	}
	chosen := plan.Channels[pick]

	// The message rides the clipboard for every channel: pasteable into a
	// text, readable aloud on a call.
	copied := provider.Copy(plan.Message)
	if chosen.Action.Target != "" {
		if err := provider.Open(chosen.Action.Target); err != nil {
			ui.Warn("couldn't open %s: %v", chosen.Action.Target, err)
		}
	}

	status := store.ApptRequested
	if chosen.Channel == provider.ChannelWalkIn {
		// Nobody to wait on: walking in during open hours is its own answer.
		status = store.ApptConfirmed
	}
	id := a.state.AddAppointment(store.Appointment{
		ShopID:   shop.ID,
		ShopName: shop.Name,
		When:     when,
		Service:  plan.Service,
		Channel:  string(chosen.Channel),
		Status:   status,
		Created:  now,
	})
	if err := a.state.Save(); err != nil {
		return fmt.Errorf("saving the appointment: %w", err)
	}

	fmt.Println()
	fmt.Printf("  %s %s %s\n", ui.Green("saved"), ui.Dim("appointment"), ui.Cyan(id))
	if copied {
		ui.Hint("message copied to your clipboard")
	}
	if status == store.ApptRequested {
		ui.Hint("when they say yes: fade-cli appts confirm %s", id)
	}
	return nil
}

// appts lists and manages manual-connector appointments.
func (a *app) appts(args []string) error {
	fs := flag.NewFlagSet("appts", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli appts [confirm|cancel <id>]

List booking requests made through the manual connectors, or update one when
the shop answers.
`)
	}
	if err := parse(fs, args); err != nil {
		return nil
	}

	rest := fs.Args()
	if len(rest) >= 2 {
		appt, err := a.state.ResolveAppointment(rest[1])
		if err != nil {
			return err
		}
		var status string
		switch rest[0] {
		case "confirm":
			status = store.ApptConfirmed
		case "cancel":
			status = store.ApptCancelled
		default:
			return fmt.Errorf("unknown action %q -- use confirm or cancel", rest[0])
		}
		if err := a.state.SetAppointmentStatus(appt.ID, status); err != nil {
			return err
		}
		if err := a.state.Save(); err != nil {
			return err
		}
		fmt.Printf("%s %s %s at %s\n", ui.Green(status), appt.ID, ui.Bold(appt.ShopName),
			catalog.InShopTime(appt.When).Format("Mon Jan 2, 3:04pm"))
		return nil
	}
	if len(rest) == 1 {
		return errors.New("confirm and cancel need an appointment id")
	}

	now := time.Now()
	up := a.state.UpcomingAppointments(now)
	stale := a.state.StaleAppointments(now)
	if len(up) == 0 && len(stale) == 0 {
		fmt.Println(ui.Dim("No appointments. Request one: fade-cli book <shop> --at \"fri 3pm\""))
		return nil
	}

	if len(up) > 0 {
		fmt.Println()
		t := ui.NewTable("id", "when", "shop", "via", "status")
		for _, ap := range up {
			t.Row(ui.Cyan(ap.ID), catalog.InShopTime(ap.When).Format("Mon Jan 2 3:04pm"), ap.ShopName,
				ui.Dim(ap.Channel), apptStatusCell(ap.Status))
		}
		t.Render(os.Stdout)
		fmt.Println()
	}
	if len(stale) > 0 {
		fmt.Println(ui.Dim("  passed without being logged:"))
		for _, ap := range stale {
			fmt.Printf("    %s %s at %s\n", ui.Cyan(ap.ID),
				ui.RelDay(ap.When, now), ap.ShopName)
		}
		ui.Hint("got the cut? fade-cli log add --shop <shop> --price <$>  ·  didn't? fade-cli appts cancel <id>")
	}
	return nil
}

func apptStatusCell(s string) string {
	switch s {
	case store.ApptConfirmed:
		return ui.Green(s)
	case store.ApptRequested:
		return ui.Yellow(s)
	default:
		return ui.Dim(s)
	}
}
