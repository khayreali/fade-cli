package main

import (
	"errors"
	"flag"
	"fmt"
	"strings"

	"fadecli/internal/provider"
)

// Payment setup stays in the provider's normal browser/app session. We do
// not copy cookies, collect PAN/CVV, or pretend a payment token is portable
// between unrelated merchant processors.
func paymentSetup(name string) (target, instructions string, err error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fresha":
		return "https://www.fresha.com/auth", `Fresha saved payments

Sign in to your CUSTOMER account in your normal browser.
Open Wallet, then Cards to add or change your saved card. You can also save
a card during Fresha's booking checkout. Use Apple Pay when it is offered.

Then return to fade and choose your service and time. Continue from review:
Fresha resumes that cart in the browser where you signed in. Review the final
total and any cancellation policy, then confirm there.

Opening this page does not connect an API account or verify a saved card.
Your login and payment details stay with Fresha and its payment providers.
`, nil
	case "booksy":
		return "https://booksy.com/en-us/", `Booksy saved payments

Sign in to your Booksy customer account. Mobile Payments supports a saved
card or Apple/Google Pay when the business enables it. If the website doesn't
offer card setup, use the Booksy customer app to manage your payment method.

Booksy currently requires selecting the service/time again after leaving fade.
Review the actual booking, total and payment terms before confirming.

Opening this page does not connect an API account or verify a saved card.
Your login and payment details stay with Booksy and its payment providers.
`, nil
	default:
		return "", "", errors.New("supported payment setup: fresha or booksy")
	}
}

func paymentsCmd(args []string) error {
	fs := flag.NewFlagSet("payments", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "show setup instructions and URL without opening a browser")
	if err := parse(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Print(`Use your existing booking accounts, not a separate fade wallet.

  fade-cli payments setup fresha
  fade-cli payments setup booksy

Save your card with the provider, or use the wallet option its checkout offers.
fade opens your normal browser, so existing logins and saved payment methods
can be reused. Setup is provider-specific; no shop needs to join fade.

No card numbers, CVV, passwords, cookies or payment keys belong in CLI flags
or fade's profile. Final payment and any bank authentication stay in checkout.
fade cannot verify a payment or appointment just because you opened a page.
`)
		return nil
	}
	if len(rest) != 2 || rest[0] != "setup" {
		return errors.New("use payments setup <fresha|booksy> [--print]")
	}
	target, help, err := paymentSetup(rest[1])
	if err != nil {
		return err
	}
	fmt.Print(help)
	if *printOnly {
		fmt.Println(target)
		return nil
	}
	return provider.Open(target)
}
