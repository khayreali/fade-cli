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

// Copy puts text on the system clipboard, reporting whether it worked. Half
// the catalog books by phone, and a number you can paste beats one you have to
// read off the screen and retype.
func Copy(text string) bool {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip")
	default:
		switch {
		case lookPath("wl-copy"):
			cmd = exec.Command("wl-copy")
		case lookPath("xclip"):
			cmd = exec.Command("xclip", "-selection", "clipboard")
		default:
			return false
		}
	}
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

func lookPath(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// Open launches a URL or tel: link with the OS handler.
//
// On macOS `open` returns as soon as it has handed the target to Launch
// Services, so it is safe to wait for it -- and worth it, because that is
// where "no application knows how to open tel:" comes back. Elsewhere the
// helper may stay alive as long as the browser does, so it is only started.
func Open(target string) error {
	if _, err := url.Parse(target); err != nil {
		return fmt.Errorf("bad target %q: %w", target, err)
	}
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("open", target).CombinedOutput()
		if err != nil {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				return fmt.Errorf("%s", msg)
			}
			return err
		}
		return nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	default:
		return exec.Command("xdg-open", target).Start()
	}
}
