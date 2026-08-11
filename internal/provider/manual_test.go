package provider

import (
	"strings"
	"testing"
	"time"

	"fadecli/internal/catalog"
)

var (
	mFri = time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC) // Friday noon UTC = 8am NY
	mSat = time.Date(2026, time.August, 8, 19, 0, 0, 0, time.UTC) // Saturday 3pm NY
)

func phoneShop(hours catalog.Hours, walkIn catalog.WalkIn) catalog.Shop {
	return catalog.Shop{
		ID: "t", Name: "Test Barbers", Phone: "+17185551234",
		Hours: hours, WalkIn: walkIn,
		Booking: catalog.Booking{Kind: catalog.KindPhone},
	}
}

func TestPlanRefusesAClosedTime(t *testing.T) {
	shop := phoneShop(catalog.Hours{"sat": "10:00-16:00"}, "")
	// 3pm NY Saturday is within hours; 5pm is past close.
	if _, err := PlanManual(shop, "K", "", mSat, mFri); err != nil {
		t.Fatalf("in-hours request refused: %v", err)
	}
	late := mSat.Add(3 * time.Hour) // 6pm NY
	_, err := PlanManual(shop, "K", "", late, mFri)
	if err == nil {
		t.Fatal("out-of-hours request accepted")
	}
	if !strings.Contains(err.Error(), "10am") {
		t.Errorf("error should name the real hours, got: %v", err)
	}
}

func TestPlanRefusesClosedDaysAndThePast(t *testing.T) {
	shop := phoneShop(catalog.Hours{"mon": "10:00-18:00"}, "")
	if _, err := PlanManual(shop, "", "", mSat, mFri); err == nil {
		t.Error("request on a closed day accepted")
	}
	if _, err := PlanManual(shop, "", "", mFri.Add(-time.Hour), mFri); err == nil {
		t.Error("request in the past accepted")
	}
}

func TestPlanWarnsOnUnknownHours(t *testing.T) {
	p, err := PlanManual(phoneShop(nil, ""), "", "", mSat, mFri)
	if err != nil {
		t.Fatalf("unknown hours should warn, not refuse: %v", err)
	}
	if p.Warning == "" {
		t.Error("no warning for unknown hours")
	}
}

func TestChannelOrderFollowsTheMoment(t *testing.T) {
	open := catalog.Hours{"fri": "07:00-20:00", "sat": "10:00-16:00"}
	closedNow := catalog.Hours{"sat": "10:00-16:00"} // closed Friday

	p, _ := PlanManual(phoneShop(open, ""), "", "", mSat, mFri)
	if p.Channels[0].Channel != ChannelCall {
		t.Errorf("open shop should lead with the call, got %v", p.Channels[0].Channel)
	}
	p, _ = PlanManual(phoneShop(closedNow, ""), "", "", mSat, mFri)
	if p.Channels[0].Channel != ChannelSMS {
		t.Errorf("closed shop should lead with the text, got %v", p.Channels[0].Channel)
	}

	p, _ = PlanManual(phoneShop(open, catalog.WalkInOnly), "", "", mSat, mFri)
	if p.Channels[0].Channel != ChannelWalkIn {
		t.Errorf("walk-in-only shop should lead with walking in, got %v", p.Channels[0].Channel)
	}
}

func TestComposedMessageReadsRight(t *testing.T) {
	p, err := PlanManual(phoneShop(nil, ""), "Sam", "fade", mSat, mFri)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fade", "Test Barbers", "Saturday", "3pm", "Sam"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message %q missing %q", p.Message, want)
		}
	}
	// No name on file: the sentence is simply absent, not "My name is ."
	p, _ = PlanManual(phoneShop(nil, ""), "", "", mSat, mFri)
	if strings.Contains(p.Message, "My name") {
		t.Errorf("nameless message still introduces a name: %q", p.Message)
	}
}

func TestSMSLinkEncoding(t *testing.T) {
	got := SMSLink("+17185551234", "3pm & later? I'm +1")
	if strings.ContainsAny(got, " &?'") && !strings.HasPrefix(got, "sms:+17185551234&body=") {
		t.Errorf("unencoded metacharacters in %q", got)
	}
	if !strings.HasPrefix(got, "sms:+17185551234&body=") {
		t.Fatalf("bad prefix: %q", got)
	}
	body := strings.TrimPrefix(got, "sms:+17185551234&body=")
	for _, banned := range []string{" ", "&", "?", "'", "+"} {
		if strings.Contains(body, banned) {
			t.Errorf("body leaves %q unencoded: %q", banned, body)
		}
	}
}
