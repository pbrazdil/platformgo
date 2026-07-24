package position

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/money"
)

const sourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

var (
	usd  = currency.MustNew("USD", 2, 840, "US Dollar", currency.Fiat)
	usdt = currency.MustNew("USDT", 8, 0, "Tether", currency.Crypto)
	btc  = currency.MustNew("BTC", 8, 0, "Bitcoin", currency.Crypto)
	eth  = currency.MustNew("ETH", 8, 0, "Ether", currency.Crypto)
)

func dec(text string) decimal.Decimal { return decimal.MustParse(text) }
func cash(text string, c currency.Currency) money.Money {
	return money.MustNew(text, c)
}
func cashPtr(text string, c currency.Currency) *money.Money {
	value := cash(text, c)
	return &value
}
func decPtr(text string) *decimal.Decimal {
	value := dec(text)
	return &value
}
func textPtr(text string) *string { return &text }

func audusd() Instrument {
	return Instrument{
		ID: "AUD/USD.SIM", PricePrecision: 5, SizePrecision: 0, Multiplier: dec("1"),
		QuoteCurrency: usd, SettlementCurrency: usd,
	}
}

func ethusdtSpot() Instrument {
	return Instrument{
		ID: "ETHUSDT.BINANCE", PricePrecision: 2, SizePrecision: 5, Multiplier: dec("1"),
		CurrencyPair: true, BaseCurrency: &eth, QuoteCurrency: usdt, SettlementCurrency: usdt,
	}
}

func btcusdtSpot() Instrument {
	return Instrument{
		ID: "BTCUSDT.BINANCE", PricePrecision: 2, SizePrecision: 6, Multiplier: dec("1"),
		CurrencyPair: true, BaseCurrency: &btc, QuoteCurrency: usdt, SettlementCurrency: usdt,
	}
}

func fill(order, trade string, side OrderSide, qty, price string, commission *money.Money, ts uint64) Fill {
	return Fill{
		ClientOrderID: order, TradeID: trade, Side: side, Quantity: dec(qty), Price: dec(price),
		Commission: commission, TsEvent: ts, TsInit: ts,
	}
}

func requireDec(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func requireMoney(t *testing.T, got *money.Money, want string, denomination currency.Currency) {
	t.Helper()
	if got == nil {
		t.Fatalf("got nil money, want %s %s", want, denomination)
	}
	expected := cash(want, denomination)
	if !got.Equal(expected) {
		t.Fatalf("got %s, want %s", got, expected)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1388
//	test: test_position_long_display
func TestPositionLongDisplay(t *testing.T) {
	position, err := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", cashPtr("2", usd), 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := position.String(), "Position(LONG 1 AUD/USD.SIM, id=1)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1394
//	test: test_position_short_display
func TestPositionShortDisplay(t *testing.T) {
	position, err := New(audusd(), "1", fill("O-1", "T-1", Sell, "1", "1", cashPtr("2", usd), 0))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := position.String(), "Position(SHORT 1 AUD/USD.SIM, id=1)"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1401
//	test: test_two_trades_with_same_trade_id_error
func TestTwoTradesWithSameTradeIDError(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 0))
	err := position.Apply(fill("O-2", "T-1", Buy, "1", "1", nil, 1))
	if err == nil || !strings.Contains(err.Error(), "`fill.trade_id` already contained in `trade_ids`") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1442
//	test: test_position_applies_fills_with_negative_prices
func TestPositionAppliesFillsWithNegativePrices(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "50000", "-5", nil, 0))
	if err := position.Apply(fill("O-2", "T-2", Buy, "50000", "-7", nil, 1)); err != nil {
		t.Fatal(err)
	}
	requireDec(t, position.Quantity, "100000")
	requireDec(t, position.SignedQuantity, "100000")
	requireDec(t, position.AverageOpen, "-6")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1487
//	test: test_position_filled_with_buy_order
func TestPositionFilledWithBuyOrder(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "100000", "1.00001", cashPtr("2", usd), 0))
	requireDec(t, position.Quantity, "100000")
	requireDec(t, position.PeakQuantity, "100000")
	requireDec(t, position.SignedQuantity, "100000")
	requireDec(t, position.AverageOpen, "1.00001")
	requireMoney(t, position.RealizedPnL, "-2", usd)
	unrealized, _ := position.UnrealizedPnL(dec("1.0005"))
	requireMoney(t, &unrealized, "49", usd)
	total, _ := position.TotalPnL(dec("1.0005"))
	requireMoney(t, &total, "47", usd)
	if !position.IsLong() || !position.IsOpen() || position.IsClosed() || position.ClosingOrderSide() != Sell ||
		position.EventCount() != 1 || position.String() != "Position(LONG 100_000 AUD/USD.SIM, id=1)" {
		t.Fatalf("unexpected long position state: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1542
//	test: test_position_filled_with_sell_order
func TestPositionFilledWithSellOrder(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Sell, "100000", "1.00001", cashPtr("2", usd), 0))
	requireDec(t, position.SignedQuantity, "-100000")
	requireMoney(t, position.RealizedPnL, "-2", usd)
	unrealized, _ := position.UnrealizedPnL(dec("1.0005"))
	requireMoney(t, &unrealized, "-49", usd)
	total, _ := position.TotalPnL(dec("1.0005"))
	requireMoney(t, &total, "-51", usd)
	if !position.IsShort() || position.ClosingOrderSide() != Buy ||
		position.String() != "Position(SHORT 100_000 AUD/USD.SIM, id=1)" {
		t.Fatalf("unexpected short position state: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1598
//	test: test_position_partial_fills_with_buy_order
func TestPositionPartialFillsWithBuyOrder(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "50000", "1.00001", cashPtr("2", usd), 0))
	unrealized, _ := position.UnrealizedPnL(dec("1.0005"))
	requireMoney(t, &unrealized, "24.50", usd)
	total, _ := position.TotalPnL(dec("1.0005"))
	requireMoney(t, &total, "22.50", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1642
//	test: test_position_partial_fills_with_two_sell_orders
func TestPositionPartialFillsWithTwoSellOrders(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Sell, "50000", "1.00001", cashPtr("2", usd), 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "50000", "1.00002", cashPtr("2", usd), 1))
	requireDec(t, position.AverageOpen, "1.000015")
	requireMoney(t, position.RealizedPnL, "-4", usd)
	unrealized, _ := position.UnrealizedPnL(dec("1.0005"))
	requireMoney(t, &unrealized, "-48.50", usd)
	total, _ := position.TotalPnL(dec("1.0005"))
	requireMoney(t, &total, "-52.50", usd)
	requireMoney(t, &position.Commissions()[0], "4", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1699
//	test: test_position_filled_with_buy_order_then_sell_order
func TestPositionFilledWithBuyOrderThenSellOrder(t *testing.T) {
	position, _ := New(audusd(), "P-1", fill("O-1", "T-1", Buy, "150000", "1.00001", cashPtr("2", usd), 1_000_000_000))
	_ = position.Apply(fill("O-2", "T-2", Sell, "150000", "1.00011", cashPtr("0", usd), 2_000_000_000))
	if position.Side != Flat || !position.IsClosed() || position.Duration != 1_000_000_000 {
		t.Fatalf("unexpected closed state: %+v", position)
	}
	requireDec(t, *position.AverageClose, "1.00011")
	requireMoney(t, position.RealizedPnL, "13", usd)
	if position.String() != "Position(FLAT AUD/USD.SIM, id=P-1)" {
		t.Fatal(position.String())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1765
//	test: test_position_filled_with_sell_order_then_buy_order
func TestPositionFilledWithSellOrderThenBuyOrder(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Sell, "100000", "1", cashPtr("2", usd), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "50000", "1.00001", cashPtr("2", usd), 1))
	_ = position.Apply(fill("O-3", "T-3", Buy, "50000", "1.00003", cashPtr("2", usd), 2))
	requireDec(t, *position.AverageClose, "1.00002")
	requireMoney(t, position.RealizedPnL, "-8", usd)
	requireMoney(t, &position.Commissions()[0], "6", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1844
//	test: test_position_filled_with_no_change
func TestPositionFilledWithNoChange(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "100000", "1", cashPtr("2", usd), 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "100000", "1", cashPtr("2", usd), 1))
	requireMoney(t, position.RealizedPnL, "-4", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1911
//	test: test_position_long_with_multiple_filled_orders
func TestPositionLongWithMultipleFilledOrders(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "100000", "1", cashPtr("2", usd), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "100000", "1.00001", cashPtr("2", usd), 1))
	_ = position.Apply(fill("O-3", "T-3", Sell, "200000", "1.00010", cashPtr("2", usd), 2))
	requireDec(t, position.AverageOpen, "1.000005")
	requireMoney(t, position.RealizedPnL, "13", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:1998
//	test: test_pnl_calculation_from_trading_technologies_example
func TestPnLCalculationFromTradingTechnologiesExample(t *testing.T) {
	position, _ := New(ethusdtSpot(), "1", fill("O-1", "T-1", Buy, "12", "100", cashPtr("0.12", usdt), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "17", "99", cashPtr("0.1683", usdt), 1))
	requireDec(t, position.Quantity, "29")
	requireDec(t, position.AverageOpen, "99.4137931034482758620689655172")
	requireMoney(t, position.RealizedPnL, "-0.2883", usdt)
	_ = position.Apply(fill("O-3", "T-3", Sell, "9", "101", cashPtr("0.0909", usdt), 2))
	requireMoney(t, position.RealizedPnL, "13.89666207", usdt)
	_ = position.Apply(fill("O-4", "T-4", Sell, "4", "105", cashPtr("0.042", usdt), 3))
	requireMoney(t, position.RealizedPnL, "36.19948966", usdt)
	_ = position.Apply(fill("O-5", "T-5", Buy, "3", "103", cashPtr("0.0309", usdt), 4))
	requireMoney(t, position.RealizedPnL, "36.16858966", usdt)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2124
//	test: test_position_closed_and_reopened
func TestPositionClosedAndReopened(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "150000", "1.00001", cashPtr("2", usd), 1))
	_ = position.Apply(fill("O-2", "T-2", Sell, "150000", "1.00011", cashPtr("0", usd), 2))
	_ = position.Apply(fill("O-3", "T-3", Buy, "150000", "1.00012", cashPtr("0", usd), 3))
	if position.Side != Long || position.EventCount() != 1 || position.TsOpened != 3 {
		t.Fatalf("unexpected reopened state: %+v", position)
	}
	requireDec(t, position.AverageOpen, "1.00012")
	requireMoney(t, position.RealizedPnL, "0", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2215
//	test: test_fill_void_replays_across_position_close_and_reopen
func TestFillVoidReplaysAcrossPositionCloseAndReopen(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-OPEN", "T-1", Buy, "10", "1", cashPtr("1", usd), 1))
	_ = position.Apply(fill("O-CLOSE", "T-2", Sell, "10", "1.1", cashPtr("1", usd), 2))
	_ = position.Apply(fill("O-REOPEN", "T-3", Buy, "5", "1.2", cashPtr("1", usd), 3))
	if err := position.ApplyFillVoid(FillVoid{
		ClientOrderID: "O-CLOSE", TradeID: "T-2", VoidedQuantity: dec("5"), CommissionVoid: cashPtr("0.5", usd),
	}); err != nil {
		t.Fatal(err)
	}
	requireDec(t, position.Quantity, "10")
	requireDec(t, position.BuyQuantity, "15")
	requireDec(t, position.SellQuantity, "5")
	requireMoney(t, &position.Commissions()[0], "2.5", usd)
	if len(position.replay) != 3 || len(position.FillVoids) != 1 {
		t.Fatalf("unexpected replay counts")
	}
	encoded, err := json.Marshal(position)
	if err != nil {
		t.Fatal(err)
	}
	var restored Position
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	requireDec(t, restored.Quantity, "10")
	requireMoney(t, &restored.Commissions()[0], "2.5", usd)
	if len(restored.replay) != 3 || len(restored.FillVoids) != 1 {
		t.Fatalf("round-trip lost replay state")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2299
//	test: test_full_fill_void_preserves_unvoided_commission
func TestFullFillVoidPreservesUnvoidedCommission(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "10", "1", cashPtr("1", usd), 1))
	if err := position.ApplyFillVoid(FillVoid{ClientOrderID: "O-1", TradeID: "T-1", VoidedQuantity: dec("10")}); err != nil {
		t.Fatal(err)
	}
	if !position.IsClosed() {
		t.Fatalf("expected flat shell")
	}
	requireMoney(t, &position.Commissions()[0], "1", usd)
	requireMoney(t, position.RealizedPnL, "-1", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2340
//	test: test_fill_void_replays_netting_flip_fragments_with_one_trade_id
func TestFillVoidReplaysNettingFlipFragmentsWithOneTradeID(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-BUY", "T-BUY", Buy, "10", "1", nil, 1))
	_ = position.Apply(fill("O-FLIP", "T-FLIP", Sell, "10", "2", nil, 2))
	position.replay = append(position.replay, replayEvent{Fill: &Fill{
		ClientOrderID: "O-FLIP", TradeID: "T-FLIP", Side: Sell, Quantity: dec("5"), Price: dec("2"), TsEvent: 2, TsInit: 2,
	}})
	if err := position.ApplyFillVoid(FillVoid{ClientOrderID: "O-FLIP", TradeID: "T-FLIP", VoidedQuantity: dec("12")}); err != nil {
		t.Fatal(err)
	}
	requireDec(t, position.SignedQuantity, "7")
	requireDec(t, position.BuyQuantity, "10")
	requireDec(t, position.SellQuantity, "3")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2397
//	test: test_position_realized_pnl_with_interleaved_order_sides
func TestPositionRealizedPnLWithInterleavedOrderSides(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "12", "10000", cashPtr("120", usdt), 0))
	_ = position.Apply(fill("O-2", "T-2", Buy, "17", "9999", cashPtr("169.983", usdt), 1))
	requireMoney(t, position.RealizedPnL, "-289.983", usdt)
	_ = position.Apply(fill("O-3", "T-3", Sell, "9", "10001", cashPtr("90.009", usdt), 2))
	requireMoney(t, position.RealizedPnL, "-365.71613793", usdt)
	_ = position.Apply(fill("O-4", "T-4", Buy, "3", "10003", cashPtr("30.009", usdt), 3))
	requireMoney(t, position.RealizedPnL, "-395.72513793", usdt)
	_ = position.Apply(fill("O-5", "T-5", Sell, "4", "10005", cashPtr("40.02", usdt), 4))
	requireMoney(t, position.RealizedPnL, "-415.27137481", usdt)
	requireDec(t, position.Quantity, "19")
}
