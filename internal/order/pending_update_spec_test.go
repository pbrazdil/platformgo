package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_update.rs:80
//	test: defaults_are_sensible
func TestOrderPendingUpdateSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderPendingUpdateSpec(NewPendingUpdateEventIDSequence()).Build()

	if event.TraderID != ids.DefaultTraderID() ||
		event.StrategyID != ids.MustStrategyID("S-001") ||
		event.InstrumentID != ids.MustInstrumentID("AUD/USD.SIM") ||
		event.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		event.AccountID != nil ||
		event.TsEvent != 0 ||
		event.TsInit != 0 ||
		event.Reconciliation ||
		event.VenueOrderID != nil {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_update.rs:96
//	test: overrides_apply_through_constructor
func TestOrderPendingUpdateSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderPendingUpdateSpec(NewPendingUpdateEventIDSequence()).
		WithAccountID(ids.MustAccountID("SIM-002")).
		WithVenueOrderID(ids.MustVenueOrderID("V-1")).
		WithReconciliation(true).
		Build()

	if event.AccountID == nil ||
		*event.AccountID != ids.MustAccountID("SIM-002") ||
		event.VenueOrderID == nil ||
		*event.VenueOrderID != ids.MustVenueOrderID("V-1") ||
		!event.Reconciliation ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_update.rs:110
//	test: accepts_an_absent_account_id
func TestOrderPendingUpdateSpecAcceptsAnAbsentAccountID(t *testing.T) {
	event := NewOrderPendingUpdateSpec(NewPendingUpdateEventIDSequence()).
		WithOptionalAccountID(nil).
		Build()

	if event.AccountID != nil {
		t.Fatalf("account ID = %v, want nil", event.AccountID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_update.rs:119
//	test: event_ids_are_unique_within_a_run
func TestOrderPendingUpdateSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	sequence := NewPendingUpdateEventIDSequence()
	a := NewOrderPendingUpdateSpec(sequence).Build()
	b := NewOrderPendingUpdateSpec(sequence).Build()
	c := NewOrderPendingUpdateSpec(sequence).Build()

	if a.EventID == b.EventID || b.EventID == c.EventID || a.EventID == c.EventID {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a.EventID, b.EventID, c.EventID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_update.rs:130
//	test: event_id_sequence_is_reproducible
func TestOrderPendingUpdateSpecEventIDSequenceIsReproducible(t *testing.T) {
	firstSequence := NewPendingUpdateEventIDSequence()
	firstRun := []string{
		NewOrderPendingUpdateSpec(firstSequence).Build().EventID,
		NewOrderPendingUpdateSpec(firstSequence).Build().EventID,
		NewOrderPendingUpdateSpec(firstSequence).Build().EventID,
	}
	secondSequence := NewPendingUpdateEventIDSequence()
	secondRun := []string{
		NewOrderPendingUpdateSpec(secondSequence).Build().EventID,
		NewOrderPendingUpdateSpec(secondSequence).Build().EventID,
		NewOrderPendingUpdateSpec(secondSequence).Build().EventID,
	}

	for index := range firstRun {
		if firstRun[index] != secondRun[index] {
			t.Fatalf("event ID %d = %s, want %s", index, secondRun[index], firstRun[index])
		}
	}
}
