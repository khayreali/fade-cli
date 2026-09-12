package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"fadecli/internal/catalog"
)

// Fresha covers Fresha-hosted shops, and reads their live availability.
//
// Fresha publishes no partner API, but its marketplace shows a venue's open
// times to anyone before they log in -- and that page is driven by a public
// GraphQL endpoint. This provider has the same conversation the browser has:
// the booking flow is a server-side state machine where every screen returns
// the action tokens for its buttons and the client echoes one back. We
// initialize a cart, add the shop's haircut, pick "any professional", reach
// the time screen and read the day's timeslots. Nothing is booked; the cart
// is abandoned.
//
// The two mutations are addressed by persisted-query hash, captured from
// Fresha's web bundle. Those change when Fresha ships a new build, so every
// failure here degrades to the handoff rather than surfacing as a fault: the
// worst case is what the CLI did before -- "book it to see their calendar".
type Fresha struct {
	Client *http.Client
}

const (
	freshaGraphQL   = "https://www.fresha.com/graphql"
	freshaHashInit  = "02d5d8ce34389c6f0a8fb062c0a6b30c1749508c053bd79b4e396b03d5d0014e"
	freshaHashPress = "93c58971c704f87497d4cbf391df04eb0da5275116e27345866052f8523904f9"
	// A browser-like agent: the endpoint is public, but it is a web app's.
	freshaUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36"
)

// ErrFreshaFlowChanged is returned when the booking flow no longer looks the
// way this code expects -- almost always a Fresha deploy that rotated the
// persisted-query hashes or reshaped a screen. Callers treat it like
// ErrNoLiveAvailability but can say why.
var ErrFreshaFlowChanged = errors.New("fresha's booking flow changed; live times unavailable until this is updated")

func (*Fresha) Kind() catalog.BookingKind { return catalog.KindFresha }

func (f *Fresha) Availability(ctx context.Context, shop catalog.Shop, day time.Time) ([]Slot, error) {
	return f.AvailabilityFor(ctx, shop, day, Service{})
}

func (f *Fresha) openFlow(ctx context.Context, shop catalog.Shop) (*freshaFlow, map[string]any, error) {
	slug := freshaSlug(shop)
	if slug == "" {
		return nil, nil, ErrNoLiveAvailability
	}
	client := f.Client
	if client == nil {
		client = defaultClient()
	}
	flow := &freshaFlow{ctx: ctx, client: client, slug: slug}

	screen, err := flow.initialize()
	return flow, screen, err
}

func (f *Fresha) Services(ctx context.Context, shop catalog.Shop) ([]Service, error) {
	_, screen, err := f.openFlow(ctx, shop)
	if err != nil {
		return nil, err
	}
	var services []Service
	for _, item := range freshaServiceItems(screen) {
		services = append(services, item.Service)
	}
	if len(services) == 0 {
		return nil, ErrFreshaFlowChanged
	}
	return services, nil
}

func (f *Fresha) AvailabilityFor(ctx context.Context, shop catalog.Shop, day time.Time, chosen Service) ([]Slot, error) {
	flow, screen, err := f.openFlow(ctx, shop)
	if err != nil {
		return nil, err
	}
	service, err := flow.addService(screen, chosen.ID)
	if err != nil {
		return nil, err
	}
	timeScreen, err := flow.reachTime(service.screen)
	if err != nil {
		return nil, err
	}
	return flow.slotsOn(timeScreen, day, service, shop)
}

func (*Fresha) Handoff(shop catalog.Shop) Action {
	if shop.Booking.URL != "" {
		return Action{Type: ActionOpen, Target: shop.Booking.URL, Label: "open on Fresha"}
	}
	if shop.Booking.ID != "" {
		return Action{
			Type:   ActionOpen,
			Target: "https://www.fresha.com/a/" + shop.Booking.ID,
			Label:  "open on Fresha",
		}
	}
	return Action{Type: ActionNone, Label: "no Fresha link on file"}
}

// PrepareCheckout selects only the reviewed time in the rechecked cart.
// The browser resumes that cart; login, confirmation and payment stay there.
// Never press onScreenTimeContinue or any confirmation action here.
func (f *Fresha) PrepareCheckout(ctx context.Context, shop catalog.Shop, slot Slot) (string, error) {
	if slot.freshaCartID == "" || slot.freshaAction == "" || !slot.Start.After(time.Now()) {
		return "", errors.New("Fresha checkout expired; refresh the available times")
	}
	start := catalog.InShopTime(slot.Start)
	h, m, ok := freshaSlotClock(map[string]any{"action": map[string]any{"id": slot.freshaAction}}, start.Format("2006-01-02"))
	if !ok || h != start.Hour() || m != start.Minute() {
		return "", ErrFreshaFlowChanged
	}
	client := f.Client
	if client == nil {
		client = defaultClient()
	}
	flow := &freshaFlow{ctx: ctx, client: client, slug: freshaSlug(shop), cartID: slot.freshaCartID}
	screen, err := flow.press(slot.freshaAction)
	if err != nil {
		return "", err
	}
	if !freshaTimeSelected(screen, start) {
		return "", ErrFreshaFlowChanged
	}
	return "https://www.fresha.com/a/" + url.PathEscape(flow.slug) + "/booking?" + url.Values{"cartId": {flow.cartID}}.Encode(), nil
}

// A rejected selection can still return a time screen. Require the exact
// date and radio selection, not merely a successful GraphQL response.
func freshaTimeSelected(screen map[string]any, start time.Time) bool {
	if screenType(screen) != "BookingFlowScreenTime" {
		return false
	}
	st, _ := screen["screenTime"].(map[string]any)
	dates, _ := st["dates"].([]any)
	dateMatches := false
	for _, raw := range dates {
		d, _ := raw.(map[string]any)
		if selected, _ := d["isSelected"].(bool); !selected {
			continue
		}
		date, _ := d["date"].(map[string]any)
		iso, _ := date["iso"].(string)
		dateMatches = strings.HasPrefix(iso, start.Format("2006-01-02"))
		break
	}
	if !dateMatches {
		return false
	}
	day, _ := st["day"].(map[string]any)
	slots, _ := day["timeslots"].([]any)
	for _, raw := range slots {
		s, _ := raw.(map[string]any)
		if selected, _ := s["isSelected"].(bool); !selected {
			continue
		}
		label, _ := s["time"].(string)
		h, m, ok := splitClock(label)
		return ok && h == start.Hour() && m == start.Minute()
	}
	return false
}

// freshaSlug is the venue's /a/<slug> identifier, from the booking id or URL.
func freshaSlug(shop catalog.Shop) string {
	if shop.Booking.ID != "" {
		return shop.Booking.ID
	}
	if i := strings.Index(shop.Booking.URL, "/a/"); i >= 0 {
		slug := shop.Booking.URL[i+3:]
		if j := strings.IndexAny(slug, "/?#"); j >= 0 {
			slug = slug[:j]
		}
		return slug
	}
	return ""
}

// freshaFlow is one booking-flow conversation: a cart and the screens it
// moves through.
type freshaFlow struct {
	ctx    context.Context
	client *http.Client
	slug   string
	cartID string
}

// freshaService is the service we put in the cart, for labelling slots.
type freshaService struct {
	Service
	screen map[string]any // the services screen after the add
}

// gql posts one persisted-query operation and returns data[field].
func (f *freshaFlow) gql(op, hash, field string, variables map[string]any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"operationName": op,
		"variables":     variables,
		"extensions":    map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": hash}},
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(f.ctx, http.MethodPost, freshaGraphQL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", "https://www.fresha.com")
	req.Header.Set("User-Agent", freshaUA)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fresha: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("fresha: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fresha: HTTP %d", resp.StatusCode)
	}

	var envelope struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("fresha: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msg := envelope.Errors[0].Message
		// An unknown hash or a missing variable both mean the client we are
		// imitating has moved on.
		if strings.Contains(msg, "PersistedQuery") || strings.Contains(msg, "Variable") {
			return nil, ErrFreshaFlowChanged
		}
		return nil, fmt.Errorf("fresha: %s", msg)
	}
	out, ok := envelope.Data[field].(map[string]any)
	if !ok {
		return nil, ErrFreshaFlowChanged
	}
	return out, nil
}

// initialize opens a cart for the venue and returns the services screen.
func (f *freshaFlow) initialize() (map[string]any, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	data, err := f.gql("BookingFlow_Initialize_Mutation", freshaHashInit, "bookingFlowInitialize", map[string]any{
		"withRecommendedServices": true,
		"input": map[string]any{
			"locationSlug": f.slug,
			"referer":      "",
			"options": map[string]any{
				"marketingToken": nil, "cnToken": nil, "via": nil,
				"isGroupBooking": false, "isRebook": false,
				"rwgToken": nil, "geiToken": nil, "employeeId": nil, "professionalProfileSlug": nil,
				"shouldShowAllEmployees": false, "isFromLinkBuilder": false, "waitlistEntryToken": nil,
				"firstTouchAt": now, "clientChannelType": "MARKETPLACE",
				"appointmentId": nil, "giftCardCode": nil, "offerItemId": nil, "offerItems": nil,
				"cartId": nil, "providerReferences": nil, "preferredDate": nil, "preferredTimeslot": nil,
				"landingPageUrl":      "https://www.fresha.com/a/" + f.slug + "/booking",
				"externalReferrerUrl": nil,
			},
			"shouldAutoContinue": false,
			"capabilities": []string{
				"SERVICE_ADDONS", "CONFIRMATION", "FULL_UPFRONT_PAYMENT", "MARKETPLACE_REFRESH",
				"DISCOUNTS_AND_BENEFITS", "LOYALTY_POINTS_STORE", "TEAM_MEMBER_GENDER",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	cart, _ := data["cartId"].(string)
	if cart == "" {
		return nil, ErrFreshaFlowChanged
	}
	f.cartID = cart
	return data, nil
}

// press echoes an action token back and returns the resulting screen state.
func (f *freshaFlow) press(actionID string) (map[string]any, error) {
	return f.gql("BookingFlow_ActionButtonPressed_Mutation", freshaHashPress, "bookingFlowActionButtonPressed", map[string]any{
		"id":                      actionID,
		"cartId":                  f.cartID,
		"shouldAutoContinue":      false,
		"withRecommendedServices": true,
	})
}

var haircutLike = regexp.MustCompile(`(?i)cut|fade`)

// addService resolves the choice against a fresh menu, then follows only
// action tokens returned by this cart. Tokens from an earlier cart aren't reused.
func (f *freshaFlow) addService(services map[string]any, id string) (*freshaService, error) {
	items := freshaServiceItems(services)
	if len(items) == 0 {
		return nil, ErrFreshaFlowChanged
	}
	menu := make([]Service, 0, len(items))
	for _, it := range items {
		menu = append(menu, it.Service)
	}
	chosen, err := ResolveService(menu, id)
	if err != nil {
		return nil, err
	}
	var pick freshaItem
	for _, it := range items {
		if it.ID == chosen.ID {
			pick = it
			break
		}
	}

	// A plain service adds directly; one with add-ons opens a modal whose
	// Add button is what puts it in the cart.
	screen, err := f.press(pick.action)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(pick.action, "ServiceVariantAdd") {
		add, ok := findAction(screen, "ModalService.onAdd", "")
		if !ok {
			return nil, ErrFreshaFlowChanged
		}
		if screen, err = f.press(add); err != nil {
			return nil, err
		}
	}
	return &freshaService{Service: pick.Service, screen: screen}, nil
}

type freshaItem struct {
	Service
	action string
}

var (
	priceRe    = regexp.MustCompile(`(\d+)`)
	durationRe = regexp.MustCompile(`(\d+)\s*min`)
	hoursRe    = regexp.MustCompile(`(\d+)\s*h`)
)

// freshaServiceItems flattens screenServices.categories[].items[].
func freshaServiceItems(screen map[string]any) []freshaItem {
	var out []freshaItem
	seen := map[string]bool{}
	ss, _ := screen["screenServices"].(map[string]any)
	cats, _ := ss["categories"].([]any)
	for _, c := range cats {
		cm, _ := c.(map[string]any)
		items, _ := cm["items"].([]any)
		for _, i := range items {
			im, _ := i.(map[string]any)
			name, _ := im["name"].(string)
			act, _ := im["primaryAction"].(map[string]any)
			id, _ := act["id"].(string)
			if name == "" || id == "" {
				continue
			}
			var tokens []json.RawMessage
			var token struct {
				CatalogID string `json:"catalogId"`
			}
			if json.Unmarshal([]byte(id), &tokens) != nil || len(tokens) == 0 || json.Unmarshal(tokens[0], &token) != nil || token.CatalogID == "" || seen[token.CatalogID] {
				continue
			}
			seen[token.CatalogID] = true
			it := freshaItem{Service: Service{ID: token.CatalogID, Name: strings.TrimSpace(name)}, action: id}
			if p, _ := im["price"].(map[string]any); p != nil {
				if s, _ := p["formatted"].(string); s != "" {
					it.PriceLabel = s
					if m := priceRe.FindString(s); m != "" {
						it.Price, _ = strconv.Atoi(m)
					}
				}
			}
			if cap, _ := im["caption"].(string); cap != "" {
				it.DurationLabel = cap
				if m := hoursRe.FindStringSubmatch(cap); m != nil {
					n, _ := strconv.Atoi(m[1])
					it.Duration += time.Duration(n) * time.Hour
				}
				if m := durationRe.FindStringSubmatch(cap); m != nil {
					n, _ := strconv.Atoi(m[1])
					it.Duration += time.Duration(n) * time.Minute
				}
			}
			out = append(out, it)
		}
	}
	return out
}

// reachTime walks services -> professional -> time. A solo shop skips the
// professional screen, so each step checks where it actually landed.
func (f *freshaFlow) reachTime(screen map[string]any) (map[string]any, error) {
	if screenType(screen) == "BookingFlowScreenTime" {
		return screen, nil
	}
	cont, ok := findAction(screen, "onScreenServicesContinue", "")
	if !ok {
		return nil, ErrFreshaFlowChanged
	}
	screen, err := f.press(cont)
	if err != nil {
		return nil, err
	}
	if screenType(screen) == "BookingFlowScreenTime" {
		return screen, nil
	}
	if anyPro, ok := findAction(screen, "onScreenEmployeeSetAny", ""); ok {
		if screen, err = f.press(anyPro); err != nil {
			return nil, err
		}
	}
	cont, ok = findAction(screen, "onScreenEmployeeContinue", "")
	if !ok {
		return nil, ErrFreshaFlowChanged
	}
	if screen, err = f.press(cont); err != nil {
		return nil, err
	}
	if screenType(screen) != "BookingFlowScreenTime" {
		return nil, ErrFreshaFlowChanged
	}
	return screen, nil
}

// slotsOn selects the requested day on the time screen and reads its
// timeslots. A day the venue doesn't trade returns no slots and no error --
// that is an answer, not a failure.
func (f *freshaFlow) slotsOn(screen map[string]any, day time.Time, svc *freshaService, shop catalog.Shop) ([]Slot, error) {
	loc := catalog.ShopLocation()
	day = day.In(loc)
	want := day.Format("2006-01-02")

	st, _ := screen["screenTime"].(map[string]any)
	dates, _ := st["dates"].([]any)
	var selectAction string
	found, selected, bookable := false, false, false
	for _, d := range dates {
		dm, _ := d.(map[string]any)
		date, _ := dm["date"].(map[string]any)
		iso, _ := date["iso"].(string)
		if !strings.HasPrefix(iso, want) {
			continue
		}
		found = true
		selected, _ = dm["isSelected"].(bool)
		bookable, _ = dm["isAvailableToBeBooked"].(bool)
		if a, _ := dm["action"].(map[string]any); a != nil {
			selectAction, _ = a["id"].(string)
		}
		break
	}
	if !found {
		// Beyond the booking window the marketplace offers.
		return nil, fmt.Errorf("fresha: %s is outside the shop's booking window", day.Format("Jan 2"))
	}
	if !bookable {
		return []Slot{}, nil
	}
	if !selected {
		if selectAction == "" {
			return nil, ErrFreshaFlowChanged
		}
		var err error
		if screen, err = f.press(selectAction); err != nil {
			return nil, err
		}
		st, _ = screen["screenTime"].(map[string]any)
	}

	dayNode, _ := st["day"].(map[string]any)
	raw, ok := dayNode["timeslots"].([]any)
	if !ok {
		return nil, ErrFreshaFlowChanged
	}
	slots := make([]Slot, 0, len(raw))
	for _, t := range raw {
		tm, _ := t.(map[string]any)
		hh, mm, ok := freshaSlotClock(tm, want)
		if !ok {
			return nil, ErrFreshaFlowChanged
		}
		slot := svc.slot(time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc), (&Fresha{}).Handoff(shop).Target)
		if action, _ := tm["action"].(map[string]any); action != nil {
			slot.freshaAction, _ = action["id"].(string)
			slot.freshaCartID = f.cartID
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

func splitClock(s string) (hh, mm int, ok bool) {
	// Marketplace locale can change the display from 14:30 to 2:30 PM,
	// including non-breaking spaces. Both represent the same shop time.
	s = strings.ToUpper(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s))
	for _, layout := range []string{"15:04", "3:04PM"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Hour(), t.Minute(), true
		}
	}
	return 0, 0, false
}

func freshaSlotClock(slot map[string]any, date string) (hh, mm int, ok bool) {
	// Prefer the machine-readable date/seconds in the returned button token.
	// Reading this token does not select or hold the time.
	action, _ := slot["action"].(map[string]any)
	if id, _ := action["id"].(string); id != "" {
		var tokens []json.RawMessage
		var token struct {
			Type string `json:"type"`
			Date string `json:"date"`
			Time *int   `json:"time"`
		}
		if json.Unmarshal([]byte(id), &tokens) != nil || len(tokens) == 0 || json.Unmarshal(tokens[0], &token) != nil || token.Type != "onScreenTimeSet" || token.Date != date || token.Time == nil || *token.Time < 0 || *token.Time >= 86400 {
			return 0, 0, false
		}
		return *token.Time / 3600, *token.Time % 3600 / 60, true
	}
	label, _ := slot["time"].(string)
	return splitClock(label)
}

func screenType(screen map[string]any) string {
	s, _ := screen["screen"].(map[string]any)
	t, _ := s["__typename"].(string)
	return t
}

// findAction is a depth-first search for an action token of the given type.
// Fresha serializes a button's action as a JSON array in its "id" field,
// e.g. `[{"type":"onScreenServicesContinue",...},"2900856"]`, under keys like
// action, primaryAction, addAction; the key name is irrelevant, the type is
// what identifies it. must, when set, must also appear in the token.
func findAction(node any, typ, must string) (string, bool) {
	switch n := node.(type) {
	case map[string]any:
		if disabled, _ := n["isDisabled"].(bool); disabled {
			return "", false
		}
		if id, ok := n["id"].(string); ok && strings.HasPrefix(id, "[{") &&
			strings.Contains(id, `"type":"`+typ+`"`) && (must == "" || strings.Contains(id, must)) {
			return id, true
		}
		for _, v := range n {
			if id, ok := findAction(v, typ, must); ok {
				return id, true
			}
		}
	case []any:
		for _, v := range n {
			if id, ok := findAction(v, typ, must); ok {
				return id, true
			}
		}
	}
	return "", false
}
