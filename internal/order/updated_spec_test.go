package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/updated.rs:93
//	test: defaults_are_sensible
func TestOrderUpdatedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderUpdatedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.Quantity.String() != "100000" || event.TsEvent != 0 || event.TsInit != 0 ||
		event.Reconciliation || event.VenueOrderID != nil || event.AccountID != nil ||
		event.Price != nil || event.TriggerPrice != nil || event.ProtectionPrice != nil ||
		event.IsQuoteQuantity {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/updated.rs:114
//	test: overrides_apply_through_constructor
func TestOrderUpdatedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderUpdatedSpec(NewOrderSpecEventIDSequence()).
		WithQuantity(decimal.MustQuantity("50000")).
		WithPrice(decimal.MustPrice("22000")).
		WithQuoteQuantity(true).Build()
	if event.Quantity.String() != "50000" || event.Price == nil ||
		event.Price.String() != "22000" || !event.IsQuoteQuantity ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/updated.rs:128
//	test: event_ids_are_unique_within_a_run
func TestOrderUpdatedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderUpdatedSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/updated.rs:139
//	test: event_id_sequence_is_reproducible
func TestOrderUpdatedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderUpdatedSpec(s).Build().EventID
	})
}
