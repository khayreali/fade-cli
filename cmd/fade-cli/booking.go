package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"fadecli/internal/catalog"
	"fadecli/internal/provider"
	"fadecli/internal/ui"
)

// bookingScreen is shared by the shop screen and `book`. Every step keeps
// its selection when going back; checking times never records a completed cut.
func (a *app) bookingScreen(raw *ui.Raw, shop catalog.Shop, query string) error {
	p, live := a.reg.For(shop).(provider.ServiceProvider)
	if !live {
		return a.handoffScreen(raw, shop, "", nil)
	}
	var services []provider.Service
	err := bookingWait(raw, "Services at "+shop.Name, func(ctx context.Context) error {
		var err error
		services, err = p.Services(ctx, shop)
		return err
	})
	if errors.Is(err, errAborted) || errors.Is(err, context.Canceled) {
		return bookingExit(err)
	}
	if err != nil {
		return a.handoffScreen(raw, shop, "Could not load services. "+availabilityMessage(err), nil)
	}
	sel, note := 0, ""
	if chosen, err := provider.ResolveService(services, query); err == nil {
		for i, s := range services {
			if s.ID == chosen.ID {
				sel = i
			}
		}
	} else if query != "" {
		note = err.Error()
	}
	for {
		rows := make([][]string, len(services))
		for i, s := range services {
			rows[i] = []string{s.Name, ui.Subtle(serviceDuration(s)), servicePrice(s)}
		}
		list := &ui.List{Title: shop.Name, Subtitle: "Step 1 of 3 · Choose a service",
			Hint: "Choose from the shop's live menu.", Note: note, Rows: rows, Filterable: true,
			Right: map[int]bool{2: true}, Hints: []ui.KeyHint{{Key: "b", Label: "booking page"}, {Key: ui.EscKey, Label: "back"}, {Key: "q", Label: "quit"}}}
		got := list.Run(raw, sel)
		note = ""
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == ui.EscKey:
			return nil
		case got.Cmd == "b":
			if err := a.handoffScreen(raw, shop, "", nil); err != nil {
				return err
			}
		default:
			sel = got.Index
			if err := a.bookingTimes(raw, shop, p, services[sel]); err != nil {
				return err
			}
		}
	}
}

func bookingWait(raw *ui.Raw, title string, work func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := raw.Wait(ctx, title, work)
	if errors.Is(err, ui.ErrInterrupted) {
		return errAborted
	}
	return err
}

func bookingExit(err error) error {
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// bookingDays shows calendar dates in shop time, including today's remaining
// hours. Hours guide the choice; only the provider can say a day has openings.
func bookingDays(raw *ui.Raw, shop catalog.Shop, chosen time.Time) (time.Time, error) {
	today, _ := parseDay("today", time.Now())
	rows := make([][]string, 14)
	sel := 0
	for i := range rows {
		day := today.AddDate(0, 0, i)
		label := day.Format("Mon, Jan 2")
		if i == 0 {
			label = "Today · " + label
		}
		if i == 1 {
			label = "Tomorrow · " + label
		}
		hours := "hours not published"
		if shop.Hours.Known() {
			if h, open := shop.Hours.OnDay(day); open {
				hours = h
			} else {
				hours = "closed per published hours"
			}
		}
		rows[i] = []string{label, ui.Subtle(hours)}
		if catalog.SameShopDay(day, chosen) {
			sel = i
		}
	}
	list := &ui.List{Title: "Choose a day", Subtitle: shop.Name + " · New York time",
		Rows: rows, Hints: []ui.KeyHint{{Key: ui.EscKey, Label: "services"}, {Key: "q", Label: "quit"}}}
	got := list.Run(raw, sel)
	if !got.OK || got.Cmd == "q" {
		return time.Time{}, errAborted
	}
	if got.Cmd != "" {
		return time.Time{}, nil
	}
	return today.AddDate(0, 0, got.Index), nil
}

func (a *app) bookingTimes(raw *ui.Raw, shop catalog.Shop, p provider.ServiceProvider, service provider.Service) error {
	day, err := bookingDays(raw, shop, time.Now())
	if err != nil || day.IsZero() {
		return err
	}
	var slots []provider.Slot
	var checkErr error
	refresh, sel, note := true, 0, ""
	for {
		if refresh {
			// Keep the result local to the worker: a canceled request may still
			// be winding down while the next screen is already on display.
			var fetched []provider.Slot
			checkErr = bookingWait(raw, "Checking "+day.Format("Mon, Jan 2"), func(ctx context.Context) error {
				var err error
				fetched, err = p.AvailabilityFor(ctx, shop, day, service)
				return err
			})
			if errors.Is(checkErr, errAborted) || errors.Is(checkErr, context.Canceled) {
				return bookingExit(checkErr)
			}
			slots = nil
			if checkErr == nil {
				slots = futureSlots(fetched, time.Now())
				if len(slots) > 0 {
					service = serviceFromSlot(service, slots[0])
				}
			}
			refresh, sel = false, 0
		}
		rows := make([][]string, 0, len(slots)+1)
		sections := map[int]bool{}
		period := ""
		at := []int{}
		for i, s := range slots {
			h := catalog.InShopTime(s.Start).Hour()
			label := "Morning"
			if h >= 12 {
				label = "Afternoon"
			}
			if h >= 17 {
				label = "Evening"
			}
			if label != period {
				sections[len(rows)] = true
				rows = append(rows, []string{label})
				at = append(at, -1)
				period = label
			}
			rows = append(rows, []string{catalog.InShopTime(s.Start).Format("3:04pm"), ui.Subtle("any available professional")})
			at = append(at, i)
		}
		hint := service.Name + " · " + servicePrice(service) + " · " + serviceDuration(service)
		if checkErr != nil {
			hint += "\n" + ui.Warning("Could not check times. "+availabilityMessage(checkErr))
		} else if len(slots) == 0 {
			hint += "\n" + ui.Subtle("No openings returned for this service and day.")
		} else {
			hint += "\n" + ui.Subtle(plural(len(slots), "opening")+" · select a time to review")
		}
		if len(slots) == 0 {
			rows = append(rows, []string{"Choose another day"})
		}
		list := &ui.List{Title: shop.Name + " · " + day.Format("Mon, Jan 2"), Subtitle: "Step 2 of 3 · Choose a time · New York time",
			Hint: hint, Note: note, Rows: rows, Sections: sections,
			Hints: []ui.KeyHint{{Key: "d", Label: "day"}, {Key: "r", Label: "refresh"}, {Key: "b", Label: "booking page"}, {Key: ui.EscKey, Label: "services"}, {Key: "q", Label: "quit"}}}
		got := list.Run(raw, sel)
		note = ""
		switch {
		case !got.OK, got.Cmd == "q":
			return errAborted
		case got.Cmd == ui.EscKey:
			return nil
		case got.Cmd == "r":
			refresh = true
		case got.Cmd == "b":
			if err := a.handoffScreen(raw, shop, "", nil); err != nil {
				return err
			}
		case got.Cmd == "d", len(slots) == 0:
			chosen, err := bookingDays(raw, shop, day)
			if err != nil {
				return err
			}
			if !chosen.IsZero() {
				day, refresh = chosen, true
			}
		default:
			sel = got.Index
			slot := slots[at[got.Index]]
			if err := a.handoffScreen(raw, shop, "", &bookingChoice{service: service, slot: slot, provider: p}); err != nil {
				return err
			}
			refresh = true // don't reuse a calendar the user may have spent minutes away from
		}
	}
}

type bookingChoice struct {
	service  provider.Service
	slot     provider.Slot
	provider provider.ServiceProvider
}

// handoffScreen distinguishes resumable checkout from a plain shop link.
// Neither preparing a cart nor opening it is a confirmed appointment.
func (a *app) handoffScreen(raw *ui.Raw, shop catalog.Shop, note string, choice *bookingChoice) error {
	action := a.reg.For(shop).Handoff(shop)
	sel := 0
	for {
		cols, height := ui.Size()
		lines := []string{shop.Name, shop.Address}
		title, subtitle := "Book with "+shop.Name, "Choose how to continue"
		verb, explanation := "Continue to booking page", "Choose your service and time on the shop's page."
		copyLabel, copyText := "Copy link", action.Target
		if action.Type == provider.ActionCall {
			verb, explanation = "Call "+provider.PrettyPhone(shop.Phone), "Arrange your visit directly with the shop."
			copyLabel, copyText = "Copy number", provider.PrettyPhone(shop.Phone)
			lines = append(lines, bookingStatus(shop, time.Now()))
			if policy := shop.WalkIn.Label(); policy != "" {
				lines = append(lines, policy)
			}
		}
		if choice != nil {
			title, subtitle = "Review your choice", "Step 3 of 3 · Continue with the shop"
			lines = append(lines, choice.service.Name, servicePrice(choice.service)+" · "+serviceDuration(choice.service),
				catalog.InShopTime(choice.slot.Start).Format("Mon, Jan 2 · 3:04pm MST"))
			if height >= 28 {
				lines = append(lines, "Any available professional")
			}
			explanation = "Select this service and time again on the booking page.\nYour appointment is confirmed only when the shop confirms it."
			if _, ok := choice.provider.(provider.CheckoutProvider); ok {
				verb = "Continue with this service and time"
				explanation = "Your selection carries into the shop's checkout.\nReview, sign in and confirm there. Nothing is booked yet."
			}
			copyLabel, copyText = "Copy booking details", bookingSummary(shop, *choice, action.Target)
		}
		panel := strings.Join(ui.Box("Your visit", lines, min(cols-4, 68)), "\n")
		rows := [][]string{{ui.Accent(verb)}, {copyLabel}, {"Back"}}
		aiRow, backRow := -1, 2
		if action.Type == provider.ActionCall && shop.WalkIn != catalog.WalkInOnly {
			rows = [][]string{{ui.Accent(verb)}, {copyLabel}, {"Ask an AI to call (opt-in pilot)"}, {"Back"}}
			aiRow, backRow = 2, 3
		}
		if action.Type == provider.ActionNone {
			rows = [][]string{{"Back"}}
			explanation = action.Label
		}
		list := &ui.List{Title: title, Subtitle: subtitle, Hint: panel + "\n" + explanation, Note: note, Rows: rows,
			Hints: []ui.KeyHint{{Key: ui.EscKey, Label: "back"}, {Key: "q", Label: "quit"}}}
		if shop.Booking.Kind == catalog.KindFresha || shop.Booking.Kind == catalog.KindBooksy {
			list.Hints = append([]ui.KeyHint{{Key: "p", Label: "payment setup"}}, list.Hints...)
		}
		got := list.Run(raw, sel)
		if !got.OK || got.Cmd == "q" {
			return errAborted
		}
		if got.Cmd == "p" {
			raw.Suspend(func() {
				if err := paymentsCmd([]string{"setup", string(shop.Booking.Kind)}); err != nil {
					ui.Warn("%v", err)
				}
				pause()
			})
			continue
		}
		if got.Cmd == ui.EscKey || got.Index == backRow || action.Type == provider.ActionNone {
			return nil
		}
		sel = got.Index
		if got.Index == aiRow {
			if err := a.callScreen(raw, shop); err != nil {
				return err
			}
			continue
		}
		if got.Index == 1 {
			if provider.Copy(copyText) {
				note = "Copied."
			} else {
				note = "Clipboard unavailable. Details are shown above."
			}
			continue
		}
		target := action.Target
		if choice != nil {
			var current provider.Slot
			err := bookingWait(raw, "Rechecking your time", func(ctx context.Context) error {
				var err error
				current, err = recheckBooking(ctx, shop, *choice, time.Now())
				return err
			})
			if errors.Is(err, errAborted) {
				return err
			}
			if errors.Is(err, context.Canceled) {
				continue
			}
			if err != nil {
				note = err.Error()
				continue
			}
			if current.Service != choice.slot.Service || current.Price != choice.slot.Price || current.PriceLabel != choice.slot.PriceLabel || current.Duration != choice.slot.Duration || current.DurationLabel != choice.slot.DurationLabel {
				choice.slot = current
				choice.service = serviceFromSlot(choice.service, current)
				note = "The shop updated its details. Review them before continuing."
				continue
			}
			if checkout, ok := choice.provider.(provider.CheckoutProvider); ok {
				err := bookingWait(raw, "Preparing your checkout", func(ctx context.Context) error {
					var err error
					target, err = checkout.PrepareCheckout(ctx, shop, current)
					return err
				})
				if errors.Is(err, errAborted) {
					return err
				}
				if err != nil {
					note = "Could not carry your selection over. Go back to refresh or use the booking page."
					continue
				}
			}
		}
		if err := provider.Open(target); err != nil {
			note = "Could not open: " + err.Error()
			continue
		}
		note = "Opened. Finish arranging your visit with the shop."
	}
}

func recheckBooking(ctx context.Context, shop catalog.Shop, c bookingChoice, now time.Time) (provider.Slot, error) {
	menu, err := c.provider.Services(ctx, shop)
	if err != nil {
		return provider.Slot{}, err
	}
	service, err := provider.ResolveService(menu, c.service.ID)
	if err != nil {
		return provider.Slot{}, err
	}
	slots, err := c.provider.AvailabilityFor(ctx, shop, c.slot.Start, service)
	if err != nil {
		return provider.Slot{}, err
	}
	for _, slot := range slots {
		if slot.Start.Equal(c.slot.Start) && slot.Start.After(now) {
			return slot, nil
		}
	}
	return provider.Slot{}, errors.New("that time is no longer available; go back to choose another")
}

func serviceFromSlot(s provider.Service, slot provider.Slot) provider.Service {
	s.Name, s.Price, s.Duration = slot.Service, slot.Price, slot.Duration
	s.PriceLabel, s.DurationLabel = slot.PriceLabel, slot.DurationLabel
	return s
}

func bookingSummary(shop catalog.Shop, c bookingChoice, url string) string {
	next := "Not booked yet. Select these details on the booking page:"
	if _, ok := c.provider.(provider.CheckoutProvider); ok {
		next = "Not booked yet. Continue from fade's review screen to carry this selection into checkout. Shop page:"
	}
	return strings.Join([]string{shop.Name, shop.Address, c.service.Name + " · " + servicePrice(c.service) + " · " + serviceDuration(c.service),
		catalog.InShopTime(c.slot.Start).Format("Mon, Jan 2, 2006 · 3:04pm MST"), "Any available professional", next, url}, "\n")
}

func servicePrice(s provider.Service) string {
	if s.PriceLabel != "" {
		return s.PriceLabel
	}
	if s.Price > 0 {
		return fmt.Sprintf("$%d", s.Price)
	}
	return "price at booking"
}

func serviceDuration(s provider.Service) string {
	if s.DurationLabel != "" {
		return s.DurationLabel
	}
	if s.Duration > 0 {
		return fmt.Sprintf("%d min", int(s.Duration.Minutes()))
	}
	return "duration at booking"
}

func availabilityMessage(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "The shop took too long to respond. Try again."
	case errors.Is(err, provider.ErrNeedsCredentials), errors.Is(err, provider.ErrNoLiveAvailability):
		return "Use the shop's booking page or contact details."
	default:
		return "The booking service is unavailable. Try again or open its page."
	}
}

func futureSlots(slots []provider.Slot, now time.Time) []provider.Slot {
	out := make([]provider.Slot, 0, len(slots))
	for _, s := range slots {
		if s.Start.After(now) {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}
