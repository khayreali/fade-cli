package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func appt(when time.Time, shop string) Appointment {
	return Appointment{ShopID: shop, ShopName: shop, When: when,
		Channel: "call", Status: ApptRequested, Created: when.Add(-24 * time.Hour)}
}

func TestAppointmentsSortAndResolve(t *testing.T) {
	s := &State{}
	id1 := s.AddAppointment(appt(day(2026, time.August, 20), "b"))
	id2 := s.AddAppointment(appt(day(2026, time.August, 12), "a"))
	if s.Appointments[0].ShopID != "a" {
		t.Error("appointments not sorted soonest-first")
	}
	if got, err := s.ResolveAppointment(id2); err != nil || got.ShopID != "a" {
		t.Errorf("ResolveAppointment(%q) = %v, %v", id2, got.ShopID, err)
	}
	_ = id1
}

func TestAppointmentIDsAreUniquePerSecond(t *testing.T) {
	s := &State{}
	a := appt(day(2026, time.August, 20), "x")
	id1 := s.AddAppointment(a)
	id2 := s.AddAppointment(a) // identical creation time
	if id1 == id2 {
		t.Errorf("colliding ids: %s", id1)
	}
}

func TestCancelledIsTerminal(t *testing.T) {
	s := &State{}
	id := s.AddAppointment(appt(day(2026, time.August, 20), "x"))
	if err := s.SetAppointmentStatus(id, ApptCancelled); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAppointmentStatus(id, ApptConfirmed); err == nil {
		t.Error("resurrected a cancelled appointment")
	}
}

func TestUpcomingVsStale(t *testing.T) {
	s := &State{}
	now := day(2026, time.August, 15)
	s.AddAppointment(appt(day(2026, time.August, 10), "past"))
	s.AddAppointment(appt(day(2026, time.August, 20), "future"))
	cancelled := s.AddAppointment(appt(day(2026, time.August, 25), "gone"))
	s.SetAppointmentStatus(cancelled, ApptCancelled)

	up := s.UpcomingAppointments(now)
	if len(up) != 1 || up[0].ShopID != "future" {
		t.Errorf("upcoming = %v", up)
	}
	stale := s.StaleAppointments(now)
	if len(stale) != 1 || stale[0].ShopID != "past" {
		t.Errorf("stale = %v", stale)
	}
	if next, ok := s.NextAppointment(now); !ok || next.ShopID != "future" {
		t.Errorf("next = %v, %v", next, ok)
	}
}

// The state file is hand-editable; appointments typed out of order must load
// sorted, same contract as cuts.
func TestLoadSortsHandEditedAppointments(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FADE_HOME", dir)
	raw := `{"profile":{},"appointments":[
	 {"id":"b","shop_id":"b","shop_name":"B","when":"2026-08-20T15:00:00Z","channel":"call","status":"requested","created":"2026-08-01T00:00:00Z"},
	 {"id":"a","shop_id":"a","shop_name":"A","when":"2026-08-12T15:00:00Z","channel":"sms","status":"confirmed","created":"2026-08-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if s.Appointments[0].ID != "a" {
		t.Error("hand-edited appointments not sorted on load")
	}
}
