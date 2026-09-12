package phone

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fadecli/internal/catalog"
)

func fixture(t *testing.T) (Request, catalog.Shop, time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, catalog.ShopLocation())
	shop := catalog.Shop{ID: "example", Name: "Example Barber", Phone: "+12125550123", Hours: catalog.Hours{"tue": "09:00-18:00"}}
	r, err := Plan(shop, "Test Customer", "+12125550124", "haircut", now.Add(time.Hour), 60, "", now)
	if err != nil {
		t.Fatal(err)
	}
	return r, shop, now
}

func TestPlanLimitsAndShopClock(t *testing.T) {
	r, shop, now := fixture(t)
	if r.When.Location() != catalog.ShopLocation() {
		t.Fatal("not in shop time")
	}
	for _, tc := range []struct {
		name, phone, service string
		when                 time.Time
		max                  int
	}{
		{"", "+12125550124", "haircut", r.When, 60},
		{"A", "911", "haircut", r.When, 60},
		{"A", "+19005550123", "haircut", r.When, 60},
		{"A", "+12125550124", "haircut\x1b[2J", r.When, 60},
		{"A", "+12125550124", "haircut", r.When, 0},
		{"A", "+12125550124", "haircut", now, 60},
		{"A", "+12125550124", "haircut", now.AddDate(0, 0, 31), 60},
		{"A", "+12125550124", "haircut", now.Add(9 * time.Hour), 60},
	} {
		if _, err := Plan(shop, tc.name, tc.phone, tc.service, tc.when, tc.max, "", now); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	shop.WalkIn = catalog.WalkInOnly
	if _, err := Plan(shop, r.Name, r.Callback, r.Service, r.When, 60, "", now); err == nil {
		t.Fatal("walk-in booked")
	}
	test, err := Plan(shop, r.Name, r.Callback, r.Service, r.When, 60, "+12125550125", now)
	if err != nil || !test.Test || test.Number == shop.Phone {
		t.Fatal("test destination not isolated", err)
	}
	if CanCallNow(shop, now.In(time.UTC)) != nil {
		t.Fatal("machine timezone changed calling hours")
	}
	for _, hour := range []int{0, 8, 18, 20, 23} {
		if CanCallNow(shop, time.Date(2026, 9, 15, hour, 0, 0, 0, catalog.ShopLocation())) == nil {
			t.Fatalf("called at %d", hour)
		}
	}
}

func TestPayloadBoundaries(t *testing.T) {
	r, _, _ := fixture(t)
	p := Payload(r, "request-1", "phone-1")
	a := p["assistant"].(map[string]any)
	if a["maxDurationSeconds"] != 180 || !strings.Contains(a["firstMessage"].(string), "AI") {
		t.Fatal(a)
	}
	artifact := a["artifactPlan"].(map[string]any)
	if artifact["recordingEnabled"] != false || artifact["loggingEnabled"] != false {
		t.Fatal("recording enabled")
	}
	b, _ := json.Marshal(p)
	for _, want := range []string{"Never pay", "Never agree to deposits", "Never transfer", "exactly the requested", "structuredOutputs", "fadeRequestId", "Is it okay"} {
		if !strings.Contains(string(b), want) {
			t.Fatal("missing safeguard", want)
		}
	}
	for _, bad := range []string{"apiKey", "transferCall", "schedulePlan", "customers", "serverUrl"} {
		if strings.Contains(string(b), `"`+bad+`"`) {
			t.Fatal("unexpected field", bad)
		}
	}
	r.Test = true
	if !strings.Contains(Payload(r, "a", "b")["assistant"].(map[string]any)["firstMessage"].(string), "No real appointment") {
		t.Fatal("test disclosure missing")
	}
}

func TestResultNeverSilentlyConfirms(t *testing.T) {
	r, _, _ := fixture(t)
	decode := func(extra string) Call {
		var c Call
		body := `{"status":"ended","artifact":{"structuredOutputs":{"one":{"name":"fade-booking-result","result":{"outcome":"booked","shopConfirmed":true,"service":"haircut","customerName":"Test Customer","requiresPayment":false,"feeAgreementRequired":false,"when":"2026-09-15T11:00:00-04:00","totalPrice":55,"evidence":"Yes, reserved for 11am.","summary":"Booked."` + extra + `}}}}}`
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	call := decode("")
	if !call.Reviewable(r) {
		t.Fatal("valid result not reviewable")
	}
	for _, override := range []string{`,"outcome":"payment_required"`, `,"shopConfirmed":false`, `,"totalPrice":61`, `,"totalPrice":null`, `,"evidence":""`, `,"when":"2026-09-15T12:00:00-04:00"`, `,"when":"tomorrow"`} {
		if decode(override).Reviewable(r) {
			t.Fatal("accepted", override)
		}
	}
	for _, override := range []string{`,"service":"beard trim"`, `,"customerName":"Someone else"`, `,"requiresPayment":true`, `,"requiresPayment":null`, `,"feeAgreementRequired":true`, `,"feeAgreementRequired":null`} {
		if decode(override).Reviewable(r) {
			t.Fatal("unsafe result accepted", override)
		}
	}
	call.Status = "in-progress"
	if call.Reviewable(r) {
		t.Fatal("ongoing call confirmed")
	}
	call = decode("")
	r.Test = true
	if call.Reviewable(r) {
		t.Fatal("test call confirmed")
	}
	r.Test = false
	call.Artifact.Outputs["two"] = call.Artifact.Outputs["one"]
	if call.Reviewable(r) {
		t.Fatal("ambiguous result accepted")
	}
}

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientFailClosedNoRetriesOrSecretLeaks(t *testing.T) {
	r, _, _ := fixture(t)
	count := 0
	client := Client{Config: Config{Key: "secret-for-test", NumberID: "phone-1", Allowed: []string{r.Number}}, HTTP: &http.Client{Transport: transport(func(req *http.Request) (*http.Response, error) {
		count++
		if req.URL.String() != "https://api.vapi.ai/call" || req.Header.Get("Authorization") != "Bearer secret-for-test" {
			t.Fatal("wrong request")
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("secret-for-test private transcript"))}, nil
	})}}
	if _, err := client.Start(context.Background(), r, "local-id"); err == nil || strings.Contains(err.Error(), "secret-for-test") {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("retried paid POST")
	}
	client.Config.Allowed = nil
	if _, err := client.Start(context.Background(), r, "id"); err == nil || count != 1 {
		t.Fatal("unapproved destination called")
	}
	client.Config.Allowed = []string{r.Number}
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) { return nil, errors.New("secret-for-test") })
	if _, err := client.Start(context.Background(), r, "id"); err == nil || strings.Contains(err.Error(), "secret-for-test") {
		t.Fatal(err)
	}
	client.HTTP.Transport = transport(func(req *http.Request) (*http.Response, error) {
		count++
		return &http.Response{StatusCode: 307, Header: http.Header{"Location": {"https://untrusted.invalid/call"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	before := count
	_, err := client.Start(context.Background(), r, "id")
	if err == nil || count != before+1 {
		t.Fatal("redirect replayed call")
	}
	if _, err := client.Get(context.Background(), "../../calls"); err == nil {
		t.Fatal("unsafe ID")
	}
}

func TestJobsReserveBeforeDialAndBlockConcurrentDuplicates(t *testing.T) {
	r, _, now := fixture(t)
	jobs := Jobs{Dir: filepath.Join(t.TempDir(), "calls")}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := jobs.Reserve(r, now); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("duplicate calls allowed", wins.Load())
	}
	list, err := jobs.List()
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	job := list[0]
	if job.CallID != "" {
		t.Fatal("claimed remote success before dialing")
	}
	if err := jobs.Attach(job.ID, "remote-1"); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Attach(job.ID, "remote-2"); err == nil {
		t.Fatal("replaced remote ID")
	}
	restored, err := jobs.Read(job.ID)
	if err != nil || restored.CallID != "remote-1" {
		t.Fatal(restored, err)
	}
	info, _ := os.Stat(filepath.Join(jobs.Dir, job.ID+".json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("contact data not private")
	}
	if _, err := jobs.Read("../state"); err == nil {
		t.Fatal("unsafe local ID")
	}
	if _, err := jobs.Reserve(r, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal("next day incorrectly blocked", err)
	}
}
