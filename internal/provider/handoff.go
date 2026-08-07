package provider

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"fadecli/internal/catalog"
)

// Link hands off to a shop's own booking page.
type Link struct{}

func (Link) Kind() catalog.BookingKind { return catalog.KindLink }

func (Link) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, ErrNoLiveAvailability
}

func (Link) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL == "" {
		return Action{Type: ActionNone, Label: "no booking link on file"}
	}
	return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open booking page"}
}

// Phone hands off to a phone call, which remains how a large share of
// Brooklyn barbershops actually take appointments.
type Phone struct{}

func (Phone) Kind() catalog.BookingKind { return catalog.KindPhone }

func (Phone) Availability(context.Context, catalog.Shop, time.Time) ([]Slot, error) {
	return nil, ErrNoLiveAvailability
}

func (Phone) Handoff(shop catalog.Shop) Action {
	if shop.Phone == "" {
		if shop.Booking.URL != "" {
			return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open booking page"}
		}
		return Action{Type: ActionNone, Label: "no phone number on file"}
	}
	return Action{Type: ActionCall, Target: "tel:" + shop.Phone, Label: "call " + PrettyPhone(shop.Phone)}
}

// PrettyPhone renders +13475991874 as (347) 599-1874, leaving anything that
// isn't a US 11-digit number untouched.
func PrettyPhone(p string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, p)
	if len(digits) == 11 && digits[0] == '1' {
		digits = digits[1:]
	}
	if len(digits) != 10 {
		return p
	}
	return fmt.Sprintf("(%s) %s-%s", digits[:3], digits[3:6], digits[6:])
}

// Open launches a URL or tel: link with the OS handler. It deliberately does
// not wait: the browser taking a moment to start shouldn't block the CLI.
func Open(target string) error {
	if _, err := url.Parse(target); err != nil {
		return fmt.Errorf("bad target %q: %w", target, err)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
