package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/denied.rs:77
//	test: defaults_are_sensible
func TestOrderDeniedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderDeniedSpec(NewDeniedEventIDSequence()).Build()

	if event.TraderID != ids.DefaultTraderID() ||
		event.StrategyID != ids.MustStrategyID("S-001") ||
		event.InstrumentID != ids.MustInstrumentID("AUD/USD.SIM") ||
		event.ClientOrderID != ids.MustClientOrderID("O-19700101-000000-001-001-1") ||
		event.Reason != OrderDenialReason("TEST") ||
		event.TsEvent != 0 ||
		event.TsInit != 0 {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/denied.rs:91
//	test: overrides_apply_through_constructor
func TestOrderDeniedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderDeniedSpec(NewDeniedEventIDSequence()).
		WithReason(OrderDenialReason("MAX_ORDER_RATE")).
		WithEventTime(1_000).
		WithInitTime(2_000).
		Build()

	if event.Reason != OrderDenialReason("MAX_ORDER_RATE") ||
		event.TsEvent != 1_000 ||
		event.TsInit != 2_000 ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/denied.rs:105
//	test: event_ids_are_unique_within_a_run
func TestOrderDeniedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	sequence := NewDeniedEventIDSequence()
	a := NewOrderDeniedSpec(sequence).Build()
	b := NewOrderDeniedSpec(sequence).Build()
	c := NewOrderDeniedSpec(sequence).Build()

	if a.EventID == b.EventID || b.EventID == c.EventID || a.EventID == c.EventID {
		t.Fatalf("event IDs were not unique: %s, %s, %s", a.EventID, b.EventID, c.EventID)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/denied.rs:116
//	test: event_id_sequence_is_reproducible
func TestOrderDeniedSpecEventIDSequenceIsReproducible(t *testing.T) {
	firstSequence := NewDeniedEventIDSequence()
	firstRun := []string{
		NewOrderDeniedSpec(firstSequence).Build().EventID,
		NewOrderDeniedSpec(firstSequence).Build().EventID,
		NewOrderDeniedSpec(firstSequence).Build().EventID,
	}
	secondSequence := NewDeniedEventIDSequence()
	secondRun := []string{
		NewOrderDeniedSpec(secondSequence).Build().EventID,
		NewOrderDeniedSpec(secondSequence).Build().EventID,
		NewOrderDeniedSpec(secondSequence).Build().EventID,
	}

	for index := range firstRun {
		if firstRun[index] != secondRun[index] {
			t.Fatalf("event ID %d = %s, want %s", index, secondRun[index], firstRun[index])
		}
	}
}
