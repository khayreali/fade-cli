package main

import (
	"net/url"
	"strings"
	"testing"
)

func TestPaymentSetupUsesProviderAccountsWithoutCollectingCards(t *testing.T) {
	for name, host := range map[string]string{"fresha": "www.fresha.com", "booksy": "booksy.com"} {
		target, help, err := paymentSetup(name)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host != host || u.User != nil || u.RawQuery != "" {
			t.Fatal("unsafe setup", target)
		}
		if !strings.Contains(help, "does not connect an API account") {
			t.Fatal("setup claims account connection")
		}
	}
	if _, _, err := paymentSetup("https://untrusted.invalid"); err == nil {
		t.Fatal("arbitrary payment destination")
	}
	if err := paymentsCmd([]string{"setup", "fresha", "--card", "4242424242424242"}); err == nil {
		t.Fatal("card input accepted")
	}
}
