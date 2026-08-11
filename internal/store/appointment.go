package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Appointment is a booking made through a manual connector: the CLI prepared
// the request and a human finished it by call, text or walking in. Status
// tracks the round-trip -- a texted request is not a booking until the shop
// answers, and conflating the two would have the tool promising chairs it
// cannot promise.
type Appointment struct {
	ID       string    `json:"id"`
	ShopID   string    `json:"shop_id"`
	ShopName string    `json:"shop_name"`
	When     time.Time `json:"when"`
	Service  string    `json:"service,omitempty"`
	// Channel is how the request went out: call, sms, walkin, web.
	Channel string `json:"channel"`
	// Status: requested (sent, awaiting the shop), confirmed (shop said yes),
	// cancelled. Walk-ins are born confirmed -- there is nobody to wait on.
	Status  string    `json:"status"`
	Created time.Time `json:"created"`
	Notes   string    `json:"notes,omitempty"`
}

const (
	ApptRequested = "requested"
	ApptConfirmed = "confirmed"
	ApptCancelled = "cancelled"
)

// AddAppointment records one and returns its id. Ids are short and derived
// from the creation time; two appointments created the same second get a
// disambiguating suffix.
func (s *State) AddAppointment(a Appointment) string {
	base := a.Created.Format("0102-1504")
	id := base
	for n := 2; s.findAppt(id) >= 0; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	a.ID = id
	s.Appointments = append(s.Appointments, a)
	sort.SliceStable(s.Appointments, func(i, j int) bool {
		return s.Appointments[i].When.Before(s.Appointments[j].When)
	})
	return id
}

func (s *State) findAppt(id string) int {
	for i, a := range s.Appointments {
		if a.ID == id {
			return i
		}
	}
	return -1
}

// ResolveAppointment finds one by exact id or unambiguous prefix. Same
// contract as shop resolution: ambiguity is reported, never guessed through,
// because the next step is telling a shop you're not coming.
func (s *State) ResolveAppointment(q string) (Appointment, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return Appointment{}, fmt.Errorf("no appointment given")
	}
	var hits []int
	for i, a := range s.Appointments {
		if a.ID == q {
			return a, nil
		}
		if strings.HasPrefix(a.ID, q) {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return Appointment{}, fmt.Errorf("no appointment matching %q", q)
	case 1:
		return s.Appointments[hits[0]], nil
	default:
		return Appointment{}, fmt.Errorf("%q matches %d appointments -- use the full id", q, len(hits))
	}
}

// SetAppointmentStatus transitions an appointment. Cancelled is terminal.
func (s *State) SetAppointmentStatus(id, status string) error {
	i := s.findAppt(id)
	if i < 0 {
		return fmt.Errorf("no appointment %q", id)
	}
	if s.Appointments[i].Status == ApptCancelled {
		return fmt.Errorf("appointment %s is cancelled and stays cancelled", id)
	}
	s.Appointments[i].Status = status
	return nil
}

// UpcomingAppointments returns non-cancelled appointments at or after now,
// soonest first (the slice is kept sorted by When).
func (s *State) UpcomingAppointments(now time.Time) []Appointment {
	var out []Appointment
	for _, a := range s.Appointments {
		if a.Status == ApptCancelled || a.When.Before(now) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// StaleAppointments returns non-cancelled appointments whose time has passed:
// the prompt to either log the cut or clean up.
func (s *State) StaleAppointments(now time.Time) []Appointment {
	var out []Appointment
	for _, a := range s.Appointments {
		if a.Status == ApptCancelled || !a.When.Before(now) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// NextAppointment is the soonest upcoming one, if any.
func (s *State) NextAppointment(now time.Time) (Appointment, bool) {
	up := s.UpcomingAppointments(now)
	if len(up) == 0 {
		return Appointment{}, false
	}
	return up[0], true
}
