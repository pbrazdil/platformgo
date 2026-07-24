package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/accepted.rs:82
//	test: defaults_are_sensible
func TestOrderAcceptedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderAcceptedSpec(NewAcceptedEventIDSequence()).Build()

	if event.TraderID != ids.DefaultTraderID() ||
		event.StrategyID != ids.MustStrategyID("S-001") ||
		event.InstrumentID != ids.MustInstrumentID("AUD/USD.SIM") ||
		event.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		event.VenueOrderID != ids.MustVenueOrderID("001") ||
		event.AccountID != ids.MustAccountID("SIM-001") ||
		event.TsEvent != 0 ||
		event.TsInit != 0 ||
		event.Reconciliation {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/accepted.rs:98
//	test: overrides_apply_through_constructor
func TestOrderAcceptedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderAcceptedSpec(NewAcceptedEventIDSequence()).
		WithVenueOrderID(ids.MustVenueOrderID("V-1")).
		WithReconciliation(true).
		Build()

	if event.VenueOrderID != ids.MustVenueOrderID("V-1") ||
		!event.Reconciliation ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/accepted.rs:110
//	test: event_ids_are_unique_within_a_run
func TestOrderAcceptedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	sequence := NewAcceptedEventIDSequence()
	a := NewOrderAcceptedSpec(sequence).Build()
	b := NewOrderAcceptedSpec(sequence).Build()
	c := NewOrderAcceptedSpec(sequence).Build()

	if a.EventID == b.EventID || b.EventID == c.EventID || a.EventID == c.EventID {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a.EventID, b.EventID, c.EventID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/accepted.rs:121
//	test: event_id_sequence_is_reproducible
func TestOrderAcceptedSpecEventIDSequenceIsReproducible(t *testing.T) {
	firstSequence := NewAcceptedEventIDSequence()
	firstRun := []string{
		NewOrderAcceptedSpec(firstSequence).Build().EventID,
		NewOrderAcceptedSpec(firstSequence).Build().EventID,
		NewOrderAcceptedSpec(firstSequence).Build().EventID,
	}
	secondSequence := NewAcceptedEventIDSequence()
	secondRun := []string{
		NewOrderAcceptedSpec(secondSequence).Build().EventID,
		NewOrderAcceptedSpec(secondSequence).Build().EventID,
		NewOrderAcceptedSpec(secondSequence).Build().EventID,
	}

	for index := range firstRun {
		if firstRun[index] != secondRun[index] {
			t.Fatalf("event ID %d = %s, want %s", index, secondRun[index], firstRun[index])
		}
	}
}
