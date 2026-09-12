package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"fadecli/internal/catalog"
	"fadecli/internal/phone"
	"fadecli/internal/store"
	"fadecli/internal/ui"
)

const callSetup = `AI phone booking (opt-in pilot)

1. Create a Vapi account and import an outbound-capable phone number.
   Vapi's free numbers do not support outbound calls. Calling incurs charges.
2. Set FADE_VAPI_API_KEY privately through your shell or secret manager.
   Set FADE_VAPI_PHONE_NUMBER_ID to the imported number's ID.
   Never paste keys into chat, command arguments, or a committed file.
3. Set your booking name and callback with fade-cli me.
4. Set FADE_AI_CALL_ALLOWED_NUMBERS to comma-separated +1 numbers whose
   owners agreed to receive AI calls. Start with your own test number.

Preview (no network, no call):
  fade-cli calls preview <shop> --at "fri 3pm" --under 60
Test call to your own approved number:
  fade-cli calls start <shop> --at "fri 3pm" --under 60 --test-to <your-number>
Live call after the shop opts in:
  fade-cli calls start <shop> --at "fri 3pm" --under 60
Review later, even after closing the terminal:
  fade-cli calls list
  fade-cli calls status <request-id>
  fade-cli calls confirm <request-id>

Every call requires typing CALL after reviewing the destination and request.
One attempt per number per New York calendar day; no automatic retries.
The assistant discloses AI, asks permission, and may reserve only the exact
time/service within your total price cap. No deposits, cards or fee agreements.
Audio recording is disabled. Vapi and its voice/model providers process the
conversation; text/analysis may be retained in your Vapi account. Review their
retention settings before use. Local call receipts contain contact details.

This pilot requires your own account. A public managed service needs a backend,
user authentication, secret storage, consent records and per-user spending limits.
Docs: https://docs.vapi.ai/calls/outbound-calling
`

func callJobs() (phone.Jobs, error) {
	dir, err := store.Dir()
	return phone.Jobs{Dir: filepath.Join(dir, "calls")}, err
}

func (a *app) calls(args []string) error {
	if len(args) == 0 || args[0] == "setup" || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(callSetup)
		return nil
	}
	if args[0] == "start" || args[0] == "preview" {
		return a.callRequest(args[0], args[1:])
	}
	jobs, err := callJobs()
	if err != nil {
		return err
	}
	if args[0] == "list" && len(args) == 1 {
		list, err := jobs.List()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("No AI call requests yet. Run fade-cli calls setup.")
			return nil
		}
		for _, job := range list {
			state := "call submitted; check status"
			if job.CallID == "" {
				state = "submission uncertain; check dashboard"
			}
			fmt.Printf("%s  %s  %s  %s\n", job.ID, cleanCallText(job.Request.ShopName), job.Request.When.Format("Jan 2 3:04pm"), state)
		}
		return nil
	}
	if len(args) < 2 || (args[0] != "status" && args[0] != "confirm" && args[0] != "attach") {
		return errors.New("use calls setup, preview, start, list, status, confirm or attach")
	}
	if (args[0] == "attach" && len(args) != 3) || (args[0] != "attach" && len(args) != 2) {
		return errors.New("wrong number of arguments")
	}
	job, err := jobs.Read(args[1])
	if err != nil {
		return err
	}
	id := job.CallID
	if args[0] == "attach" {
		id = args[2]
	}
	if id == "" {
		return fmt.Errorf("submission outcome is unknown; do not redial. Find fade-%s in Vapi, then run: fade-cli calls attach %s <vapi-call-id>", job.ID, job.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := phone.Client{Config: phone.FromEnv(os.Getenv)}
	call, err := client.Get(ctx, id)
	if err != nil {
		return err
	}
	if call.Metadata.RequestID != job.ID || call.Customer.Number != job.Request.Number {
		return errors.New("remote call does not match this request; refusing to attach or confirm it")
	}
	if args[0] == "attach" {
		if err := jobs.Attach(job.ID, call.ID); err != nil {
			return err
		}
	}
	showCallPlan(job.Request)
	fmt.Printf("\nCall status: %s\n", cleanCallText(call.Status))
	if call.EndedReason != "" {
		fmt.Println("End reason:", cleanCallText(call.EndedReason))
	}
	if result := call.Result(); result != nil {
		fmt.Printf("AI-extracted outcome (review required): %s\n%s\nShop evidence: %s\n", cleanCallText(result.Outcome), cleanCallText(result.Summary), cleanCallText(result.Evidence))
		fmt.Printf("Reported time: %s\n", cleanCallText(result.When))
		if result.TotalPrice != nil {
			fmt.Printf("Reported total: $%.2f\n", *result.TotalPrice)
		}
	} else if call.Status == "ended" {
		fmt.Println("No booking result yet. Analysis may still be processing; check status again. Do not redial.")
	}
	if args[0] == "confirm" {
		if !call.Reviewable(job.Request) {
			return errors.New("no exact, within-budget shop confirmation to save; review the call with the shop")
		}
		return a.confirmCall(job, call)
	}
	if call.Reviewable(job.Request) {
		ui.Hint("after reviewing the evidence: fade-cli calls confirm %s", job.ID)
	} else {
		fmt.Println("No confirmed appointment has been saved.")
	}
	return nil
}

func (a *app) callRequest(mode string, args []string) error {
	fs := flag.NewFlagSet("calls "+mode, flag.ContinueOnError)
	at := fs.String("at", "", "exact preferred time, in New York time")
	service := fs.String("service", "haircut", "service to request")
	under := fs.Int("under", 0, "maximum total USD, including mandatory fees and taxes")
	testTo := fs.String("test-to", "", "simulate with your own approved +1 number; never books")
	if err := parse(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	shop, err := a.mustResolve(strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	when, err := parseWhen(*at, time.Now())
	if err != nil {
		return err
	}
	r, err := phone.Plan(shop, a.state.Profile.Name, a.state.Profile.Phone, *service, when, *under, *testTo, time.Now())
	if err != nil {
		return fmt.Errorf("%w (booking name/callback: fade-cli me --set-name ... --set-phone ...)", err)
	}
	showCallPlan(r)
	if mode == "preview" {
		fmt.Println("\nPreview only. No data sent, no call placed.")
		return nil
	}
	config := phone.FromEnv(os.Getenv)
	if err := config.Ready(r.Number); err != nil {
		return err
	}
	if !r.Test {
		if err := phone.CanCallNow(shop, time.Now()); err != nil {
			return err
		}
	}
	if !ui.IsInteractive() {
		return errors.New("starting a paid AI call requires an interactive terminal; use preview for scripts")
	}
	fmt.Println("\nVapi and its voice/model providers receive this request and conversation.")
	fmt.Println("Audio recording disabled; provider-side text/analysis may be retained. Call limit: 3 minutes.")
	line, ok := ui.Line("Type CALL to authorize this one paid call, or enter to cancel: ")
	if !ok || strings.TrimSpace(line) != "CALL" {
		fmt.Println("Cancelled. No call placed.")
		return nil
	}
	// Revalidate after review, which might have crossed closing time.
	if _, err = phone.Plan(shop, r.Name, r.Callback, r.Service, r.When, r.MaxPrice, *testTo, time.Now()); err != nil {
		return err
	}
	if !r.Test {
		if err := phone.CanCallNow(shop, time.Now()); err != nil {
			return err
		}
	}
	jobs, err := callJobs()
	if err != nil {
		return err
	}
	job, err := jobs.Reserve(r, time.Now())
	if err != nil {
		return err
	}
	fmt.Println("Request ID:", job.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	call, err := (phone.Client{Config: config}).Start(ctx, r, job.ID)
	if err != nil {
		return fmt.Errorf("%w; request %s retained. Do not redial; inspect Vapi for fade-%s", err, job.ID, job.ID)
	}
	if err := jobs.Attach(job.ID, call.ID); err != nil {
		return fmt.Errorf("call submitted as %s, but saving its ID failed: %w; use calls attach", call.ID, err)
	}
	fmt.Println("AI call submitted. You can close the CLI; the call continues remotely.")
	ui.Hint("fade-cli calls status %s", job.ID)
	return nil
}

func showCallPlan(r phone.Request) {
	label := "AI booking request"
	if r.Test {
		label = "TEST CALL - simulation only, no reservation"
	}
	fmt.Printf("\n%s\nShop: %s\nDial: %s\nService: %s\nWhen: %s\nMaximum total: $%d\nName: %s\nCallback: %s\n", label,
		cleanCallText(r.ShopName), r.Number, cleanCallText(r.Service), catalog.InShopTime(r.When).Format("Mon Jan 2, 2006 3:04pm MST"), r.MaxPrice, cleanCallText(r.Name), r.Callback)
}

// Never let provider summaries print terminal escape sequences.
func cleanCallText(s string) string {
	r := []rune(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s))
	if len(r) > 1200 {
		r = r[:1200]
	}
	return string(r)
}

func (a *app) confirmCall(job phone.Job, call phone.Call) error {
	if !call.Reviewable(job.Request) || call.Metadata.RequestID != job.ID || call.Customer.Number != job.Request.Number {
		return errors.New("call has no matching, reviewable booking result")
	}
	// Load fresh so a long-running UI doesn't overwrite a newer profile.
	state, err := store.Load()
	if err != nil {
		return err
	}
	for _, ap := range state.Appointments {
		if ap.CallID == call.ID {
			fmt.Println("Already recorded as", ap.ID, "("+ap.Status+")")
			a.state = state
			return nil
		}
	}
	id := state.AddAppointment(store.Appointment{ShopID: job.Request.ShopID, ShopName: job.Request.ShopName,
		When: job.Request.When, Service: job.Request.Service, Channel: "ai-call", Status: store.ApptConfirmed,
		Created: time.Now(), CallID: call.ID, Notes: "User reviewed AI-extracted shop confirmation."})
	if err := state.Save(); err != nil {
		return err
	}
	a.state = state
	fmt.Println("Saved reviewed appointment:", id)
	return nil
}

func (a *app) callScreen(raw *ui.Raw, shop catalog.Shop) error {
	var runErr error
	raw.Suspend(func() {
		if err := phone.FromEnv(os.Getenv).Ready(shop.Phone); err != nil {
			fmt.Print("\n" + callSetup)
			ui.Warn("%v", err)
			pause()
			return
		}
		fmt.Println("\nAI calling pilot. Run fade-cli calls setup for credentials and approved numbers.")
		when, ok := ui.Line("Preferred time (e.g. fri 3pm; blank cancels): ")
		if !ok || strings.TrimSpace(when) == "" {
			return
		}
		service, ok := ui.Line("Service (enter for haircut): ")
		if !ok {
			return
		}
		if strings.TrimSpace(service) == "" {
			service = "haircut"
		}
		budget, ok := ui.Line("Maximum total price in USD: ")
		if !ok {
			return
		}
		runErr = a.callRequest("start", []string{shop.ID, "--at", when, "--service", service, "--under", budget})
		if runErr != nil {
			ui.Warn("%v", runErr)
			runErr = nil
		}
		pause()
	})
	return runErr
}
