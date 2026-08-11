package catalog

import (
	"testing"
	"time"

	"fadecli/internal/geo"
)

// The hot path: browse re-runs Find on every screen change, and walkableShops
// may call it up to three times widening the radius.
func BenchmarkFindNearby(b *testing.B) {
	c, err := Load("")
	if err != nil {
		b.Fatal(err)
	}
	graham, _ := geo.StopByID("graham")
	q := Query{Origin: &graham.Point, MaxMiles: 0.8, CollapseBy: "venue"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := c.Find(q); len(got) == 0 {
			b.Fatal("no results")
		}
	}
}

func BenchmarkFindOpenNow(b *testing.B) {
	c, err := Load("")
	if err != nil {
		b.Fatal(err)
	}
	graham, _ := geo.StopByID("graham")
	at := time.Date(2026, time.August, 7, 15, 0, 0, 0, time.UTC)
	q := Query{Origin: &graham.Point, OpenAt: at, CollapseBy: "venue"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Find(q)
	}
}

func BenchmarkNearestStop(b *testing.B) {
	p := geo.Point{Lat: 40.7146, Lon: -73.9441}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		geo.NearestStop(p)
	}
}

func BenchmarkLoad(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Load(""); err != nil {
			b.Fatal(err)
		}
	}
}
