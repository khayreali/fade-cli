package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"fadecli/internal/phone"
	"fadecli/internal/store"
)

func TestCallConfirmationIsReviewedAndIdempotent(t *testing.T) {
	t.Setenv("FADE_HOME", t.TempDir())
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.Profile.Name = "Test Customer"
	if err := state.Save(); err != nil {
		t.Fatal(err)
	}
	a := &app{state: state}
	var call phone.Call
	if err := json.Unmarshal([]byte(`{"id":"remote-1","status":"ended","customer":{"number":"+12125550123"},"metadata":{"fadeRequestId":"local-1"},"artifact":{"structuredOutputs":{"output":{"name":"fade-booking-result","result":{"outcome":"booked","shopConfirmed":true,"service":"Haircut","customerName":"Test Customer","requiresPayment":false,"feeAgreementRequired":false,"when":"2026-09-15T15:00:00-04:00","totalPrice":55,"evidence":"Yes, the appointment is confirmed.","summary":"Reserved."}}}}}`), &call); err != nil {
		t.Fatal(err)
	}
	when, _ := time.Parse(time.RFC3339, "2026-09-15T15:00:00-04:00")
	job := phone.Job{ID: "local-1", Request: phone.Request{ShopID: "example", ShopName: "Example", Number: "+12125550123", Name: "Test Customer", Service: "Haircut", When: when, MaxPrice: 60}}
	if err := a.confirmCall(job, call); err != nil {
		t.Fatal(err)
	}
	if err := a.confirmCall(job, call); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Appointments) != 1 || loaded.Profile.Name != "Test Customer" || loaded.Appointments[0].CallID != "remote-1" {
		t.Fatal("duplicate or missing appointment", loaded.Appointments)
	}
	if err := loaded.SetAppointmentStatus(loaded.Appointments[0].ID, store.ApptCancelled); err != nil {
		t.Fatal(err)
	}
	if err := loaded.Save(); err != nil {
		t.Fatal(err)
	}
	if err := a.confirmCall(job, call); err != nil {
		t.Fatal(err)
	}
	if a.state.Appointments[0].Status != store.ApptCancelled {
		t.Fatal("resurrected cancelled appointment")
	}
	job.Request.Test = true
	if err := a.confirmCall(job, call); err == nil {
		t.Fatal("test call became appointment")
	}
	job.Request.Test = false
	call.Customer.Number = "+12125550124"
	if err := a.confirmCall(job, call); err == nil {
		t.Fatal("wrong destination accepted")
	}
}

func TestCallTextCannotControlTerminal(t *testing.T) {
	s := cleanCallText("result\x1b[2J\n\x07\r" + strings.Repeat("x", 5000))
	if strings.ContainsAny(s, "\x1b\n\r\x07") || len([]rune(s)) > 1200 {
		t.Fatal("unsafe output")
	}
}
