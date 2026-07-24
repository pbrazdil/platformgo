package instrument

import (
	"sync"
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:98
//	test: test_venue_constants
func TestVenueConstants(t *testing.T) {
	tests := []struct {
		name  string
		venue func() ids.Venue
	}{
		{"CBCM", VenueCBCM},
		{"GLBX", VenueGLBX},
		{"NYUM", VenueNYUM},
		{"XCBT", VenueXCBT},
		{"XCEC", VenueXCEC},
		{"XCME", VenueXCME},
		{"XFXS", VenueXFXS},
		{"XNYM", VenueXNYM},
	}
	for _, test := range tests {
		first := test.venue()
		second := test.venue()
		if first != second {
			t.Fatalf("%s calls differ: %s != %s", test.name, first, second)
		}
		if first.String() != test.name {
			t.Fatalf("%s string = %q", test.name, first)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:142
//	test: test_venue_constants_uniqueness
func TestVenueConstantsUniqueness(t *testing.T) {
	venues := CommonVenues()
	for firstIndex, first := range venues {
		for secondIndex, second := range venues {
			if firstIndex != secondIndex && first == second {
				t.Fatalf("venues at indices %d and %d should differ", firstIndex, secondIndex)
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:169
//	test: test_venue_map_contains_all_venues
func TestVenueMapContainsAllVenues(t *testing.T) {
	venueMap := CommonVenueMap()
	for _, code := range []string{"CBCM", "GLBX", "NYUM", "XCBT", "XCEC", "XCME", "XFXS", "XNYM"} {
		if _, exists := venueMap[code]; !exists {
			t.Fatalf("venue map does not contain %s", code)
		}
	}
	if len(venueMap) != 8 {
		t.Fatalf("venue map length = %d, want 8", len(venueMap))
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:187
//	test: test_venue_map_values_match_constants
func TestVenueMapValuesMatchConstants(t *testing.T) {
	venueMap := CommonVenueMap()
	expected := map[string]ids.Venue{
		"CBCM": VenueCBCM(),
		"GLBX": VenueGLBX(),
		"NYUM": VenueNYUM(),
		"XCBT": VenueXCBT(),
		"XCEC": VenueXCEC(),
		"XCME": VenueXCME(),
		"XFXS": VenueXFXS(),
		"XNYM": VenueXNYM(),
	}
	for code, venue := range expected {
		if venueMap[code] != venue {
			t.Fatalf("%s value = %s, want %s", code, venueMap[code], venue)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:202
//	test: test_venue_map_lookup_nonexistent
func TestVenueMapLookupNonexistent(t *testing.T) {
	for _, code := range []string{"INVALID", "", "NYSE"} {
		if venue, exists := LookupCommonVenue(code); exists {
			t.Fatalf("lookup %q returned %s", code, venue)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:212
//	test: test_venue_constants_lazy_initialization
func TestVenueConstantsLazyInitialization(t *testing.T) {
	cbcmCalls := make([]ids.Venue, 10)
	for index := range cbcmCalls {
		cbcmCalls[index] = VenueCBCM()
	}
	first := cbcmCalls[0]
	for _, venue := range cbcmCalls {
		if venue != first {
			t.Fatalf("CBCM calls differ: %s != %s", venue, first)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:226
//	test: test_all_venue_strings
func TestAllVenueStrings(t *testing.T) {
	expected := []struct {
		text  string
		venue ids.Venue
	}{
		{"CBCM", VenueCBCM()},
		{"GLBX", VenueGLBX()},
		{"NYUM", VenueNYUM()},
		{"XCBT", VenueXCBT()},
		{"XCEC", VenueXCEC()},
		{"XCME", VenueXCME()},
		{"XFXS", VenueXFXS()},
		{"XNYM", VenueXNYM()},
	}
	for _, item := range expected {
		if item.venue.String() != item.text {
			t.Fatalf("venue string = %q, want %q", item.venue, item.text)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:247
//	test: test_venue_constants_thread_safety
func TestVenueConstantsThreadSafety(t *testing.T) {
	const workers = 4
	results := make(chan []ids.Venue, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			results <- CommonVenues()
		}()
	}
	group.Wait()
	close(results)
	expected := CommonVenues()
	for venues := range results {
		if len(venues) != len(expected) {
			t.Fatalf("venue count = %d", len(venues))
		}
		for index := range expected {
			if venues[index] != expected[index] {
				t.Fatalf("venue %d = %s, want %s", index, venues[index], expected[index])
			}
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/venues.rs:287
//	test: test_venue_map_thread_safety
func TestVenueMapThreadSafety(t *testing.T) {
	const workers = 4
	results := make(chan *ids.Venue, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			venue, exists := LookupCommonVenue("XCME")
			if !exists {
				results <- nil
				return
			}
			results <- &venue
		}()
	}
	group.Wait()
	close(results)
	for venue := range results {
		if venue == nil || *venue != VenueXCME() {
			t.Fatalf("lookup result = %v, want XCME", venue)
		}
	}
}
