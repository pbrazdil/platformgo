package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/triggered.rs:80
//	test: defaults_are_sensible
func TestOrderTriggeredSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderTriggeredSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.TsEvent != 0 || event.TsInit != 0 || event.Reconciliation ||
		event.VenueOrderID != nil || event.AccountID != nil {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/triggered.rs:96
//	test: overrides_apply_through_constructor
func TestOrderTriggeredSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderTriggeredSpec(NewOrderSpecEventIDSequence()).
		WithVenueOrderID(ids.MustVenueOrderID("V-1")).
		WithAccountID(ids.MustAccountID("SIM-002")).
		WithReconciliation(true).Build()
	if event.VenueOrderID == nil || *event.VenueOrderID != ids.MustVenueOrderID("V-1") ||
		event.AccountID == nil || *event.AccountID != ids.MustAccountID("SIM-002") ||
		!event.Reconciliation || event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/triggered.rs:110
//	test: event_ids_are_unique_within_a_run
func TestOrderTriggeredSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderTriggeredSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/triggered.rs:121
//	test: event_id_sequence_is_reproducible
func TestOrderTriggeredSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderTriggeredSpec(s).Build().EventID
	})
}
