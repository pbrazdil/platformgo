package order

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/snapshot.rs:196
//	test: test_snapshot_from_market_order
func TestSnapshotFromMarketOrder(t *testing.T) {
	order, err := NewMarketOrder(MarketOrderConfig{
		InstrumentID: ids.MustInstrumentID("EURUSD.SIM"), Side: OrderSideBuy,
		Quantity: decimal.MustQuantity("100"),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := SnapshotFromMarketOrder(order)
	if snapshot.TraderID != order.TraderID() || snapshot.StrategyID != order.StrategyID() ||
		snapshot.InstrumentID != order.InstrumentID() ||
		snapshot.ClientOrderID != order.ClientOrderID() || snapshot.VenueOrderID != nil ||
		snapshot.OrderSide != order.Side() || snapshot.OrderType != order.OrderType() ||
		!snapshot.Quantity.Equal(order.Quantity()) || snapshot.Status != order.core.Status() ||
		snapshot.TsInit != order.core.config.TimestampInit ||
		snapshot.TsLast != order.core.TimestampLast() ||
		!snapshot.FilledQuantity.Equal(order.core.FilledQuantity()) ||
		snapshot.IsPostOnly || snapshot.IsQuoteQuantity {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/events/order/snapshot.rs:222
//	test: test_snapshot_from_limit_order
func TestSnapshotFromLimitOrder(t *testing.T) {
	order, err := NewLimit(LimitConfig{
		InstrumentID: ids.MustInstrumentID("BTCUSDT.BINANCE"), Side: OrderSideSell,
		Quantity: decimal.MustQuantity("0.5"), Price: decimal.MustPrice("50000"),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := SnapshotFromLimitOrder(order)
	if snapshot.OrderType != OrderTypeLimit || snapshot.OrderSide != OrderSideSell ||
		snapshot.Price == nil || snapshot.Price.String() != "50000" ||
		snapshot.InstrumentID != ids.MustInstrumentID("BTCUSDT.BINANCE") ||
		snapshot.Quantity.String() != "0.5" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
