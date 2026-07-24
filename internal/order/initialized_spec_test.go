package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/initialized.rs:146
//	test: defaults_are_sensible
func TestOrderInitializedSpecDefaultsAreSensible(t *testing.T) {
	event := NewOrderInitializedSpec(NewOrderSpecEventIDSequence()).Build()
	requireDefaultSpecIdentity(t, event.TraderID, event.StrategyID, event.InstrumentID, event.ClientOrderID)
	if event.OrderSide != OrderSideBuy || event.OrderType != OrderTypeMarket ||
		event.Quantity.String() != "100000" || event.TimeInForce != TimeInForceDay ||
		event.PostOnly || event.ReduceOnly || event.QuoteQuantity || event.Reconciliation ||
		event.TsEvent != 0 || event.TsInit != 0 ||
		event.Price != nil || event.ActivationPrice != nil || event.TriggerPrice != nil ||
		event.TriggerType != nil || event.LimitOffset != nil || event.TrailingOffset != nil ||
		event.TrailingOffsetType != nil || event.ExpireTime != nil ||
		event.DisplayQuantity != nil || event.EmulationTrigger != nil ||
		event.TriggerInstrumentID != nil || event.ContingencyType != nil ||
		event.OrderListID != nil || event.LinkedOrderIDs != nil ||
		event.ParentOrderID != nil || event.ExecAlgorithmID != nil ||
		event.ExecAlgorithmParams != nil || event.ExecSpawnID != nil || event.Tags != nil {
		t.Fatalf("default event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/initialized.rs:185
//	test: overrides_apply_through_constructor
func TestOrderInitializedSpecOverridesApplyThroughConstructor(t *testing.T) {
	event := NewOrderInitializedSpec(NewOrderSpecEventIDSequence()).
		WithOrderType(OrderTypeLimit).
		WithOrderSide(OrderSideSell).
		WithQuantity(decimal.MustQuantity("50")).
		WithPrice(decimal.MustPrice("1.25000")).
		WithPostOnly(true).Build()
	if event.OrderType != OrderTypeLimit || event.OrderSide != OrderSideSell ||
		event.Quantity.String() != "50" || event.Price == nil ||
		event.Price.String() != "1.25000" || !event.PostOnly ||
		event.TraderID != ids.DefaultTraderID() {
		t.Fatalf("overridden event = %#v", event)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/initialized.rs:203
//	test: event_ids_are_unique_within_a_run
func TestOrderInitializedSpecEventIDsAreUniqueWithinARun(t *testing.T) {
	requireUniqueSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderInitializedSpec(s).Build().EventID
	})
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/spec/initialized.rs:214
//	test: event_id_sequence_is_reproducible
func TestOrderInitializedSpecEventIDSequenceIsReproducible(t *testing.T) {
	requireReproducibleSpecIDs(t, func(s *OrderSpecEventIDSequence) string {
		return NewOrderInitializedSpec(s).Build().EventID
	})
}
