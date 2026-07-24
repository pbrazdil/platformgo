package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/submitted.rs:76
//	test: defaults_are_sensible
func TestOrderSubmittedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderSubmittedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.AccountID != ids.MustAccountID("SIM-001") || event.TsEvent != 0 || event.TsInit != 0 {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/submitted.rs:90
//	test: overrides_apply_through_constructor
func TestOrderSubmittedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderSubmittedSpec(NewOrderSpecEventIDSequence()).
		WithAccountID(ids.MustAccountID("SIM-002")).
		WithEventTime(1_000).WithInitTime(2_000).Build()
	if event.AccountID != ids.MustAccountID("SIM-002") ||
		event.TsEvent != 1_000 || event.TsInit != 2_000 ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/submitted.rs:104
//	test: event_ids_are_unique_within_a_run
func TestOrderSubmittedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderSubmittedSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/submitted.rs:115
//	test: event_id_sequence_is_reproducible
func TestOrderSubmittedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderSubmittedSpec(s).Build().EventID
	})
}
