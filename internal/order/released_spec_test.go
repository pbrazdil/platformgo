package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/released.rs:77
//	test: defaults_are_sensible
func TestOrderReleasedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderReleasedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.ReleasedPrice.String() != "1.00000" || event.TsEvent != 0 || event.TsInit != 0 {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/released.rs:91
//	test: overrides_apply_through_constructor
func TestOrderReleasedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderReleasedSpec(NewOrderSpecEventIDSequence()).
		WithReleasedPrice(decimal.MustPrice("22000")).Build()
	if event.ReleasedPrice.String() != "22000" || event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/released.rs:101
//	test: event_ids_are_unique_within_a_run
func TestOrderReleasedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderReleasedSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/released.rs:112
//	test: event_id_sequence_is_reproducible
func TestOrderReleasedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderReleasedSpec(s).Build().EventID
	})
}
