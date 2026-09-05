package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

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
	slug := freshaSlug(shop)
	if slug == "" {
		return nil, ErrNoLiveAvailability
	}
	client := f.Client
	if client == nil {
		client = defaultClient()
	}
	flow := &freshaFlow{ctx: ctx, client: client, slug: slug}

	screen, err := flow.initialize()
	if err != nil {
		return nil, err
	}
	service, err := flow.addHaircut(screen)
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
	name     string
	price    int
	duration time.Duration
	screen   map[string]any // the services screen after the add
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

// addHaircut puts the shop's haircut (or its first service) in the cart.
// Availability depends on the service's duration, so this matters: a shave
// slot is not a haircut slot.
func (f *freshaFlow) addHaircut(services map[string]any) (*freshaService, error) {
	items := freshaServiceItems(services)
	if len(items) == 0 {
		return nil, ErrFreshaFlowChanged
	}
	pick := items[0]
	for _, it := range items {
		if haircutLike.MatchString(it.name) {
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
	return &freshaService{name: pick.name, price: pick.price, duration: pick.duration, screen: screen}, nil
}

type freshaItem struct {
	name     string
	price    int
	duration time.Duration
	action   string
}

var (
	priceRe    = regexp.MustCompile(`(\d+)`)
	durationRe = regexp.MustCompile(`(\d+)\s*min`)
)

// freshaServiceItems flattens screenServices.categories[].items[].
func freshaServiceItems(screen map[string]any) []freshaItem {
	var out []freshaItem
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
			it := freshaItem{name: strings.TrimSpace(name), action: id}
			if p, _ := im["price"].(map[string]any); p != nil {
				if s, _ := p["formatted"].(string); s != "" {
					if m := priceRe.FindString(s); m != "" {
						it.price, _ = strconv.Atoi(m)
					}
				}
			}
			if cap, _ := im["caption"].(string); cap != "" {
				if m := durationRe.FindStringSubmatch(cap); m != nil {
					n, _ := strconv.Atoi(m[1])
					it.duration = time.Duration(n) * time.Minute
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
	raw, _ := dayNode["timeslots"].([]any)
	slots := make([]Slot, 0, len(raw))
	for _, t := range raw {
		tm, _ := t.(map[string]any)
		hhmm, _ := tm["time"].(string)
		hh, mm, ok := splitClock(hhmm)
		if !ok {
			continue
		}
		slots = append(slots, Slot{
			Start:    time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc),
			Duration: svc.duration,
			Service:  svc.name,
			Barber:   "any",
			Price:    svc.price,
			BookURL:  (&Fresha{}).Handoff(shop).Target,
		})
	}
	return slots, nil
}

func splitClock(s string) (hh, mm int, ok bool) {
	h, m, found := strings.Cut(s, ":")
	if !found {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
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
