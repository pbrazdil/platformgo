package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/rejected.rs:86
//	test: defaults_are_sensible
func TestOrderRejectedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderRejectedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.AccountID != ids.MustAccountID("SIM-001") || event.Reason != "TEST" ||
		event.TsEvent != 0 || event.TsInit != 0 || event.Reconciliation || event.DuePostOnly {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/rejected.rs:103
//	test: overrides_apply_through_constructor
func TestOrderRejectedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderRejectedSpec(NewOrderSpecEventIDSequence()).
		WithReason("INSUFFICIENT_MARGIN").WithReconciliation(true).WithDuePostOnly(true).Build()
	if event.Reason != "INSUFFICIENT_MARGIN" || !event.Reconciliation ||
		!event.DuePostOnly || event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/rejected.rs:117
//	test: event_ids_are_unique_within_a_run
func TestOrderRejectedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderRejectedSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/rejected.rs:128
//	test: event_id_sequence_is_reproducible
func TestOrderRejectedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderRejectedSpec(s).Build().EventID
	})
}
