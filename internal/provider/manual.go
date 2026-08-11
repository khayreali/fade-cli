// Manual connectors cover the shops no API can reach. Every booking platform
// in this catalog is seller-scoped -- a third-party client cannot create a
// booking on any of them -- and 27 shops have no platform at all. For those,
// "booking" means a phone call, a text, or walking in. A manual connector does
// everything short of the human step: it validates the requested time against
// the shop's hours, composes the request message, picks the sensible channels
// for the moment (texting a closed shop beats calling it), and hands back
// actions the CLI can execute -- dial, prefilled SMS, clipboard.
package provider

import (
	"fmt"
	"strings"
	"time"

	"fadecli/internal/catalog"
)

// Channel is one way to complete a manual booking.
type Channel string

const (
	ChannelCall   Channel = "call"
	ChannelSMS    Channel = "sms"
	ChannelWalkIn Channel = "walkin"
	ChannelWeb    Channel = "web"
)

// ChannelOption is a channel plus the words explaining it right now
// ("call now -- open until 8pm" vs "call when they open at 10am").
type ChannelOption struct {
	Channel Channel
	Label   string
	Action  Action
}

// ManualPlan is a prepared booking request for a shop a human must finish.
type ManualPlan struct {
	Shop    catalog.Shop
	When    time.Time
	Service string
	Message string
	// Channels are ordered by suitability for this moment.
	Channels []ChannelOption
	// Warning is set when the plan is usable but something needs saying --
	// unknown hours, mostly. A time that is provably outside opening hours is
	// an error from PlanManual instead, not a warning.
	Warning string
}

// PlanManual builds the booking request. It refuses a time the shop is
// provably closed for: sending "can I come at 3pm Sunday" to a shop that is
// shut Sundays wastes everyone's time, and the error names the real hours so
// the caller can pick again.
func PlanManual(shop catalog.Shop, yourName, service string, when, now time.Time) (ManualPlan, error) {
	if service == "" {
		service = "haircut"
	}
	p := ManualPlan{Shop: shop, When: when, Service: service}

	if when.Before(now) {
		return p, fmt.Errorf("%s is in the past", when.Format("Mon Jan 2 3:04pm"))
	}
	if shop.Hours.Known() {
		if st, _ := shop.Hours.OpenAt(when); st != catalog.StatusOpen {
			if label, trades := shop.Hours.OnDay(when); trades {
				return p, fmt.Errorf("%s is closed at %s on %s -- their hours that day are %s",
					shop.Name, when.Format("3:04pm"), when.Format("Monday"), label)
			}
			return p, fmt.Errorf("%s is closed on %ss", shop.Name, when.Format("Monday"))
		}
	} else {
		p.Warning = "hours unknown -- the shop may not be open then"
	}

	p.Message = composeRequest(shop.Name, yourName, service, when)
	p.Channels = rankChannels(shop, p.Message, now)
	if len(p.Channels) == 0 {
		return p, fmt.Errorf("%s has no phone, no link and no walk-in policy on file", shop.Name)
	}
	return p, nil
}

// composeRequest writes what you'd say: plain, short, complete. The same text
// works read aloud on a call or pasted into a text.
func composeRequest(shopName, yourName, service string, when time.Time) string {
	when = catalog.InShopTime(when)
	var b strings.Builder
	fmt.Fprintf(&b, "Hi! I'd like to book a %s at %s on %s around %s.",
		service, shopName, when.Format("Monday Jan 2"), clock(when))
	if yourName != "" {
		fmt.Fprintf(&b, " My name is %s.", yourName)
	}
	b.WriteString(" Does that work?")
	return b.String()
}

func clock(t time.Time) string {
	if t.Minute() == 0 {
		return t.Format("3pm")
	}
	return t.Format("3:04pm")
}

// rankChannels orders the ways to reach the shop by what makes sense right
// now. The ordering rules, in words:
//   - A walk-in-only shop leads with walk-in; there is nothing to arrange.
//   - An open shop that takes calls leads with the call -- a live answer beats
//     an async text.
//   - A closed shop leads with the text: it will be read when they open, and
//     calling a closed shop reaches nobody. The call option stays, labelled
//     with when they next open.
//   - A booking URL, when present, is offered too; manual planning is mostly
//     for phone shops but nothing forbids preparing a message anyway.
func rankChannels(shop catalog.Shop, message string, now time.Time) []ChannelOption {
	status, next := shop.Hours.OpenAt(now)
	open := status == catalog.StatusOpen

	var out []ChannelOption
	addCall := func(label string) {
		if shop.Phone == "" {
			return
		}
		out = append(out, ChannelOption{
			Channel: ChannelCall,
			Label:   label,
			Action:  Action{Type: ActionCall, Target: "tel:" + shop.Phone, Label: "dial " + PrettyPhone(shop.Phone)},
		})
	}
	addSMS := func(label string) {
		if shop.Phone == "" {
			return
		}
		out = append(out, ChannelOption{
			Channel: ChannelSMS,
			Label:   label,
			Action:  Action{Type: ActionOpen, Target: SMSLink(shop.Phone, message), Label: "text " + PrettyPhone(shop.Phone)},
		})
	}

	if shop.WalkIn == catalog.WalkInOnly {
		label := "walk in -- they take no appointments"
		if open {
			label = "walk in now -- they take no appointments and they're open"
		}
		out = append(out, ChannelOption{Channel: ChannelWalkIn, Label: label})
		addCall("call to check the wait")
		return out
	}

	switch {
	case open:
		addCall("call now -- " + shop.Hours.StatusLabel(now))
		addSMS("text them instead")
	case status == catalog.StatusClosed:
		addSMS("text now -- they'll see it when they open")
		label := "call when they open"
		if !next.IsZero() {
			label = "call after they open -- " + shop.Hours.StatusLabel(now)
		}
		addCall(label)
	default: // hours unknown
		addCall("call -- hours unknown")
		addSMS("or text them")
	}

	if shop.WalkIn == catalog.WalkInWelcome {
		out = append(out, ChannelOption{Channel: ChannelWalkIn, Label: "or just walk in -- they take walk-ins"})
	}
	if shop.Booking.URL != "" {
		out = append(out, ChannelOption{
			Channel: ChannelWeb,
			Label:   "book online instead",
			Action:  Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open booking page"},
		})
	}
	return out
}

// SMSLink builds an sms: URL that opens Messages with the number and body
// prefilled. Spaces are percent-encoded by hand: QueryEscape's '+' renders
// literally in message bodies on macOS.
func SMSLink(phone, body string) string {
	esc := strings.NewReplacer(
		" ", "%20", "\n", "%0A", "&", "%26", "?", "%3F", "#", "%23", "'", "%27", "+", "%2B", ",", "%2C",
	).Replace(body)
	return "sms:" + phone + "&body=" + esc
}
