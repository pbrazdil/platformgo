package position

import (
	"strconv"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
)

func linearPosition(t *testing.T, side OrderSide, qty, price, fee string) *Position {
	t.Helper()
	instrument := audusd()
	instrument.SizePrecision = 3
	position, err := New(instrument, "1", fill("O-1", "T-1", side, qty, price, cashPtr(fee, usd), 0))
	if err != nil {
		t.Fatal(err)
	}
	return position
}

func inverseInstrument(id string, base currency.Currency) Instrument {
	return Instrument{
		ID: id, PricePrecision: 2, SizePrecision: 0, Multiplier: dec("1"), Inverse: true,
		BaseCurrency: &base, QuoteCurrency: usd, SettlementCurrency: base,
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2532
//	test: test_calculate_pnl_when_given_position_side_flat_returns_zero
func TestCalculatePnLWhenGivenPositionSideFlatReturnsZero(t *testing.T) {
	position := linearPosition(t, Buy, "1", "1", "0")
	position.Side, position.SignedQuantity = Flat, decimal.Decimal{}
	got, err := position.CalculatePnL(dec("1"), dec("1"), dec("1"))
	if err != nil {
		t.Fatal(err)
	}
	requireMoney(t, &got, "0", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2559
//	test: test_calculate_pnl_for_long_position_win
func TestCalculatePnLForLongPositionWin(t *testing.T) {
	position := linearPosition(t, Buy, "12", "10500", "126")
	got, _ := position.CalculatePnL(dec("10500"), dec("10510"), dec("12"))
	requireMoney(t, &got, "120", usd)
	requireMoney(t, position.RealizedPnL, "-126", usd)
	unrealized, _ := position.UnrealizedPnL(dec("10510"))
	requireMoney(t, &unrealized, "120", usd)
	total, _ := position.TotalPnL(dec("10510"))
	requireMoney(t, &total, "-6", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2596
//	test: test_calculate_pnl_for_long_position_loss
func TestCalculatePnLForLongPositionLoss(t *testing.T) {
	position := linearPosition(t, Buy, "12", "10500", "126")
	got, _ := position.CalculatePnL(dec("10500"), dec("10480.5"), dec("10"))
	requireMoney(t, &got, "-195", usd)
	unrealized, _ := position.UnrealizedPnL(dec("10480.5"))
	requireMoney(t, &unrealized, "-234", usd)
	total, _ := position.TotalPnL(dec("10480.5"))
	requireMoney(t, &total, "-360", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2633
//	test: test_calculate_pnl_for_short_position_winning
func TestCalculatePnLForShortPositionWinning(t *testing.T) {
	position := linearPosition(t, Sell, "10.15", "10500", "106.575")
	got, _ := position.CalculatePnL(dec("10500"), dec("10390"), dec("10.15"))
	requireMoney(t, &got, "1116.50", usd)
	requireMoney(t, position.RealizedPnL, "-106.575", usd)
	notional, _ := position.NotionalValue(dec("10390"))
	requireMoney(t, &notional, "105458.50", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2670
//	test: test_calculate_pnl_for_short_position_loss
func TestCalculatePnLForShortPositionLoss(t *testing.T) {
	position := linearPosition(t, Sell, "10", "10500", "105")
	got, _ := position.CalculatePnL(dec("10500"), dec("10670.5"), dec("10"))
	requireMoney(t, &got, "-1705", usd)
	notional, _ := position.NotionalValue(dec("10670.5"))
	requireMoney(t, &notional, "106705", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2707
//	test: test_calculate_pnl_for_inverse1
func TestCalculatePnLForInverse1(t *testing.T) {
	instrument := inverseInstrument("XBTUSD.BITMEX", btc)
	position, _ := New(instrument, "1", fill("O-1", "T-1", Sell, "100000", "10000", cashPtr("0.0075", btc), 0))
	got, _ := position.CalculatePnL(dec("10000"), dec("11000"), dec("100000"))
	requireMoney(t, &got, "-0.90909091", btc)
	requireMoney(t, position.RealizedPnL, "-0.0075", btc)
	notional, _ := position.NotionalValue(dec("11000"))
	requireMoney(t, &notional, "9.09090909", btc)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2747
//	test: test_try_notional_value_for_inverse_zero_price_returns_error
func TestTryNotionalValueForInverseZeroPriceReturnsError(t *testing.T) {
	position, _ := New(inverseInstrument("BTCUSDT.BITMEX", btc), "1", fill("O", "T", Buy, "1", "10000", nil, 0))
	for _, operation := range []func() error{
		func() error { _, err := position.NotionalValue(dec("0")); return err },
		func() error { _, err := position.CalculatePnL(dec("10000"), dec("0"), dec("1")); return err },
		func() error { _, err := position.UnrealizedPnL(dec("0")); return err },
		func() error { _, err := position.TotalPnL(dec("0")); return err },
	} {
		err := operation()
		if err == nil || err.Error() != "price must be positive for inverse notional valuation" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	position.Instrument.BaseCurrency = nil
	for _, operation := range []func() error{
		func() error { _, err := position.NotionalValue(dec("10000")); return err },
		func() error { _, err := position.UnrealizedPnL(dec("10000")); return err },
	} {
		err := operation()
		if err == nil || !strings.Contains(err.Error(), "inverse position BTCUSDT.BITMEX has no base currency") {
			t.Fatalf("unexpected error: %v", err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2796
//	test: test_calculate_pnl_for_inverse2
func TestCalculatePnLForInverse2(t *testing.T) {
	position, _ := New(inverseInstrument("ETHUSD.BITMEX", eth), "1", fill("O", "T", Sell, "100000", "375.95", nil, 0))
	unrealized, _ := position.UnrealizedPnL(dec("370"))
	requireMoney(t, &unrealized, "4.27745208", eth)
	notional, _ := position.NotionalValue(dec("370"))
	requireMoney(t, &notional, "270.27027027", eth)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2834
//	test: test_notional_value_for_quanto_uses_settlement_currency
func TestNotionalValueForQuantoUsesSettlementCurrency(t *testing.T) {
	instrument := Instrument{ID: "ETHBTC.BITMEX", SizePrecision: 0, Multiplier: dec("1"), SettlementCurrency: usdt}
	position, _ := New(instrument, "1", fill("O", "T", Buy, "5", "0.036", nil, 0))
	notional, _ := position.NotionalValue(dec("0.036"))
	requireMoney(t, &notional, "0.18", usdt)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2864
//	test: test_calculate_unrealized_pnl_for_long
func TestCalculateUnrealizedPnLForLong(t *testing.T) {
	position := linearPosition(t, Buy, "2", "10500", "21")
	_ = position.Apply(fill("O-2", "T-2", Buy, "2", "10500", cashPtr("21", usd), 1))
	unrealized, _ := position.UnrealizedPnL(dec("11505.6"))
	requireMoney(t, &unrealized, "4022.40", usd)
	requireMoney(t, position.RealizedPnL, "-42", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2919
//	test: test_calculate_unrealized_pnl_for_short
func TestCalculateUnrealizedPnLForShort(t *testing.T) {
	position := linearPosition(t, Sell, "5.912", "10505.6", "62.1091072")
	unrealized, _ := position.UnrealizedPnL(dec("10407.15"))
	requireMoney(t, &unrealized, "582.0364", usd)
	requireMoney(t, position.RealizedPnL, "-62.1091072", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2954
//	test: test_calculate_unrealized_pnl_for_long_inverse
func TestCalculateUnrealizedPnLForLongInverse(t *testing.T) {
	position, _ := New(inverseInstrument("XBTUSD.BITMEX", btc), "1", fill("O", "T", Buy, "100000", "10500", cashPtr("0.00714286", btc), 0))
	unrealized, _ := position.UnrealizedPnL(dec("11505.6"))
	requireMoney(t, &unrealized, "0.83238969", btc)
	requireMoney(t, position.RealizedPnL, "-0.00714286", btc)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:2988
//	test: test_calculate_unrealized_pnl_for_short_inverse
func TestCalculateUnrealizedPnLForShortInverse(t *testing.T) {
	position, _ := New(inverseInstrument("XBTUSD.BITMEX", btc), "1", fill("O", "T", Sell, "1250000", "15500", cashPtr("0.06048387", btc), 0))
	unrealized, _ := position.UnrealizedPnL(dec("12506.65"))
	requireMoney(t, &unrealized, "19.30166700", btc)
	requireMoney(t, position.RealizedPnL, "-0.06048387", btc)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3024
//	test: test_signed_qty_decimal_qty_for_equity
func TestSignedQtyDecimalQtyForEquity(t *testing.T) {
	long := linearPosition(t, Buy, "25", "10", "0")
	short := linearPosition(t, Sell, "25", "10", "0")
	requireDec(t, long.SignedQuantity, "25")
	requireDec(t, short.SignedQuantity, "-25")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3056
//	test: test_position_with_commission_none
func TestPositionWithCommissionNone(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O", "T", Buy, "1", "1", nil, 0))
	requireMoney(t, position.RealizedPnL, "0", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3067
//	test: test_position_with_commission_zero
func TestPositionWithCommissionZero(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O", "T", Buy, "1", "1", cashPtr("0", usd), 0))
	requireMoney(t, position.RealizedPnL, "0", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3079
//	test: test_cache_purge_order_events
func TestCachePurgeOrderEvents(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 1))
	_ = position.Apply(fill("O-2", "T-2", Buy, "2", "2", nil, 2))
	position.PurgeEventsForOrder("O-1")
	if position.EventCount() != 1 || len(position.TradeIDs()) != 1 || position.TradeIDs()[0] != "T-2" {
		t.Fatalf("unexpected purge state")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3137
//	test: test_purge_all_events_returns_none_for_last_event_and_trade_id
func TestPurgeAllEventsReturnsNoneForLastEventAndTradeID(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O", "T", Buy, "1", "1", nil, 1))
	position.PurgeEventsForOrder("O")
	if position.LastEvent() != nil || position.LastTradeID() != nil || !position.IsClosed() {
		t.Fatalf("unexpected empty shell")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3196
//	test: test_revive_from_empty_shell
func TestReviveFromEmptyShell(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O-1", "T-1", Buy, "1", "1", nil, 1))
	position.PurgeEventsForOrder("O-1")
	if err := position.Apply(fill("O-2", "T-2", Buy, "50000", "1", nil, 3_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if position.Side != Long || position.EventCount() != 1 || position.TsOpened != 3_000_000_000 {
		t.Fatalf("unexpected revived state: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3262
//	test: test_empty_shell_position_invariants
func TestEmptyShellPositionInvariants(t *testing.T) {
	position, _ := New(audusd(), "1", fill("O", "T", Buy, "1", "1", nil, 1))
	position.PurgeEventsForOrder("O")
	if position.EventCount() != 0 || len(position.TradeIDs()) != 0 || position.TsOpened != 0 ||
		position.TsLast != 0 || position.Side != Flat || !position.IsClosed() {
		t.Fatalf("empty-shell invariants failed: %+v", position)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3366
//	test: test_position_pnl_precision_with_very_small_amounts
func TestPositionPnLPrecisionWithVerySmallAmounts(t *testing.T) {
	position := linearPosition(t, Buy, "1", "1", "0.01")
	if position.Commissions()[0].Decimal().Sign() <= 0 || position.RealizedPnL.Decimal().Sign() >= 0 {
		t.Fatal("small commission lost")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3410
//	test: test_position_pnl_precision_with_high_precision_instrument
func TestPositionPnLPrecisionWithHighPrecisionInstrument(t *testing.T) {
	instrument := audusd()
	instrument.ID, instrument.SizePrecision = "ETHUSDT.PERP", 3
	position, _ := New(instrument, "1", fill("O", "T", Buy, "1.1234", "2345.123456789", nil, 0))
	requireDec(t, position.Quantity, "1.123")
	requireDec(t, position.AverageOpen, "2345.123456789")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3463
//	test: test_position_pnl_accumulation_across_many_fills
func TestPositionPnLAccumulationAcrossManyFills(t *testing.T) {
	instrument := audusd()
	position, _ := New(instrument, "1", fill("O-0", "T-0", Buy, "10", "1.00000", cashPtr("0.01", usd), 0))
	for index := 1; index < 100; index++ {
		indexText := strconv.Itoa(index)
		price := dec("1.00000").Add(dec("0.00001").Mul(dec(indexText)))
		_ = position.Apply(Fill{
			ClientOrderID: "O-" + indexText,
			TradeID:       "T-" + indexText,
			Side:          Buy, Quantity: dec("10"), Price: price, Commission: cashPtr("0.01", usd), TsEvent: uint64(index),
		})
	}
	requireDec(t, position.Quantity, "1000")
	requireMoney(t, &position.Commissions()[0], "1", usd)
	if position.AverageOpen.Cmp(dec("1")) <= 0 || position.AverageOpen.Cmp(dec("1.001")) >= 0 {
		t.Fatalf("unexpected average: %s", position.AverageOpen)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3525
//	test: test_position_pnl_with_extreme_price_values
func TestPositionPnLWithExtremePriceValues(t *testing.T) {
	position := linearPosition(t, Buy, "1", "0.00001", "0")
	got, _ := position.CalculatePnL(dec("0.00001"), dec("99999.99999"), dec("1"))
	requireMoney(t, &got, "99999.99998", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3588
//	test: test_position_pnl_roundtrip_precision
func TestPositionPnLRoundtripPrecision(t *testing.T) {
	position := linearPosition(t, Buy, "1", "10", "0.5")
	_ = position.Apply(fill("O-2", "T-2", Sell, "1", "10", cashPtr("0.5", usd), 1))
	requireMoney(t, position.RealizedPnL, "-1", usd)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3647
//	test: test_position_commission_in_base_currency_buy
func TestPositionCommissionInBaseCurrencyBuy(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	requireDec(t, position.Quantity, "0.999")
	if len(position.Adjustments) != 1 {
		t.Fatal("missing base-commission adjustment")
	}
	requireDec(t, *position.Adjustments[0].QuantityChange, "-0.001")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3715
//	test: test_position_commission_in_base_currency_sell
func TestPositionCommissionInBaseCurrencySell(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Sell, "1", "10000", cashPtr("0.001", btc), 0))
	requireDec(t, position.SignedQuantity, "-1.001")
	requireDec(t, position.Quantity, "1.001")
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3777
//	test: test_position_commission_in_quote_currency_no_adjustment
func TestPositionCommissionInQuoteCurrencyNoAdjustment(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O", "T", Buy, "1", "10000", cashPtr("50", usdt), 0))
	requireDec(t, position.Quantity, "1")
	if len(position.Adjustments) != 0 {
		t.Fatal("quote commission adjusted quantity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/position.rs:3820
//	test: test_position_reset_clears_adjustments
func TestPositionResetClearsAdjustments(t *testing.T) {
	position, _ := New(btcusdtSpot(), "1", fill("O-1", "T-1", Buy, "1", "10000", cashPtr("0.001", btc), 0))
	_ = position.Apply(fill("O-2", "T-2", Sell, "0.999", "10000", cashPtr("50", usdt), 1))
	_ = position.Apply(fill("O-3", "T-3", Buy, "2", "10000", cashPtr("0.002", btc), 2))
	if len(position.Adjustments) != 1 {
		t.Fatalf("got %d adjustments", len(position.Adjustments))
	}
	requireDec(t, *position.Adjustments[0].QuantityChange, "-0.002")
}
