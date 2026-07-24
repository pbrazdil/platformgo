package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/canceled.rs:80
//	test: defaults_are_sensible
func TestOrderCanceledSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderCanceledSpec(NewCanceledEventIDSequence()).Build()

	if event.TraderID != ids.DefaultTraderID() ||
		event.StrategyID != ids.MustStrategyID("S-001") ||
		event.InstrumentID != ids.MustInstrumentID("AUD/USD.SIM") ||
		event.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		event.TsEvent != 0 ||
		event.TsInit != 0 ||
		event.Reconciliation ||
		event.VenueOrderID != nil ||
		event.AccountID != nil {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/canceled.rs:96
//	test: overrides_apply_through_constructor
func TestOrderCanceledSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderCanceledSpec(NewCanceledEventIDSequence()).
		WithVenueOrderID(ids.MustVenueOrderID("V-1")).
		WithAccountID(ids.MustAccountID("SIM-002")).
		WithReconciliation(true).
		Build()

	if event.VenueOrderID == nil ||
		*event.VenueOrderID != ids.MustVenueOrderID("V-1") ||
		event.AccountID == nil ||
		*event.AccountID != ids.MustAccountID("SIM-002") ||
		!event.Reconciliation ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/canceled.rs:110
//	test: event_ids_are_unique_within_a_run
func TestOrderCanceledSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	sequence := NewCanceledEventIDSequence()
	a := NewOrderCanceledSpec(sequence).Build()
	b := NewOrderCanceledSpec(sequence).Build()
	c := NewOrderCanceledSpec(sequence).Build()

	if a.EventID == b.EventID || b.EventID == c.EventID || a.EventID == c.EventID {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a.EventID, b.EventID, c.EventID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/canceled.rs:121
//	test: event_id_sequence_is_reproducible
func TestOrderCanceledSpecEventIDSequenceIsReproducible(t *testing.T) {
	firstSequence := NewCanceledEventIDSequence()
	firstRun := []string{
		NewOrderCanceledSpec(firstSequence).Build().EventID,
		NewOrderCanceledSpec(firstSequence).Build().EventID,
		NewOrderCanceledSpec(firstSequence).Build().EventID,
	}
	secondSequence := NewCanceledEventIDSequence()
	secondRun := []string{
		NewOrderCanceledSpec(secondSequence).Build().EventID,
		NewOrderCanceledSpec(secondSequence).Build().EventID,
		NewOrderCanceledSpec(secondSequence).Build().EventID,
	}

	for index := range firstRun {
		if firstRun[index] != secondRun[index] {
			t.Fatalf("event ID %d = %s, want %s", index, secondRun[index], firstRun[index])
		}
	}
}
