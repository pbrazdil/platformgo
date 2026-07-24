package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_cancel.rs:80
//	test: defaults_are_sensible
func TestOrderPendingCancelSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderPendingCancelSpec(NewPendingCancelEventIDSequence()).Build()

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
//	source: crates/model/src/events/order/spec/pending_cancel.rs:96
//	test: overrides_apply_through_constructor
func TestOrderPendingCancelSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderPendingCancelSpec(NewPendingCancelEventIDSequence()).
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
//	source: crates/model/src/events/order/spec/pending_cancel.rs:110
//	test: accepts_an_absent_account_id
func TestOrderPendingCancelSpecAcceptsAnAbsentAccountID(t *testing.T) {
	event := NewOrderPendingCancelSpec(NewPendingCancelEventIDSequence()).
		WithOptionalAccountID(nil).
		Build()

	if event.AccountID != nil {
		t.Fatalf("account ID = %v, want nil", event.AccountID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_cancel.rs:119
//	test: event_ids_are_unique_within_a_run
func TestOrderPendingCancelSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	sequence := NewPendingCancelEventIDSequence()
	a := NewOrderPendingCancelSpec(sequence).Build()
	b := NewOrderPendingCancelSpec(sequence).Build()
	c := NewOrderPendingCancelSpec(sequence).Build()

	if a.EventID == b.EventID || b.EventID == c.EventID || a.EventID == c.EventID {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a.EventID, b.EventID, c.EventID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/pending_cancel.rs:130
//	test: event_id_sequence_is_reproducible
func TestOrderPendingCancelSpecEventIDSequenceIsReproducible(t *testing.T) {
	firstSequence := NewPendingCancelEventIDSequence()
	firstRun := []string{
		NewOrderPendingCancelSpec(firstSequence).Build().EventID,
		NewOrderPendingCancelSpec(firstSequence).Build().EventID,
		NewOrderPendingCancelSpec(firstSequence).Build().EventID,
	}
	secondSequence := NewPendingCancelEventIDSequence()
	secondRun := []string{
		NewOrderPendingCancelSpec(secondSequence).Build().EventID,
		NewOrderPendingCancelSpec(secondSequence).Build().EventID,
		NewOrderPendingCancelSpec(secondSequence).Build().EventID,
	}

	for index := range firstRun {
		if firstRun[index] != secondRun[index] {
			t.Fatalf("event ID %d = %s, want %s", index, secondRun[index], firstRun[index])
		}
	}
}
