package market

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func testTrade() TradeTick {
	return MustTradeTick("EURUSD.SIM", decimal.MustPrice("1.0500"), decimal.MustQuantity("100000"), Buyer, "T-001", 1_000_000_000, 2_000_000_000)
}

func ethUSDTTrade() TradeTick {
	return MustTradeTick("ETHUSDT-PERP.BINANCE", decimal.MustPrice("10000.0000"), decimal.MustQuantity("1.00000000"), Buyer, "123456789", 0, 1)
}

func assertTradeFields(t *testing.T, trade TradeTick, instrument, price, size string, side AggressorSide, id string, event, init UnixNanos) {
	t.Helper()
	if trade.InstrumentID != InstrumentID(instrument) || trade.Price.String() != price ||
		trade.Size.String() != size || trade.AggressorSide != side ||
		trade.TradeID != TradeID(id) || trade.TsEvent != event || trade.TsInit != init {
		t.Fatalf("unexpected trade: %+v", trade)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:202
//	test: test_trade_tick_new
func TestTradeTickNew(t *testing.T) {
	assertTradeFields(t, testTrade(), "EURUSD.SIM", "1.0500", "100000", Buyer, "T-001", 1_000_000_000, 2_000_000_000)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:215
//	test: test_trade_tick_new_checked_valid
func TestTradeTickNewCheckedValid(t *testing.T) {
	trade, err := NewTradeTick("GBPUSD.SIM", decimal.MustPrice("1.2500"), decimal.MustQuantity("50000"), Seller, "T-002", 500_000_000, 1_500_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if trade.InstrumentID != "GBPUSD.SIM" || trade.Price.String() != "1.2500" || trade.AggressorSide != Seller {
		t.Fatalf("unexpected trade: %+v", trade)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:235
//	test: test_trade_tick_new_with_zero_size_panics
func TestTradeTickNewWithZeroSizePanics(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || recovered.(error).Error() != "invalid `Quantity` for 'size' not positive, was 0" {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	MustTradeTick("ETH-USDT-SWAP.OKX", decimal.MustPrice("10000.00"), decimal.MustQuantity("0"), Buyer, "123456789", 0, 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:256
//	test: test_trade_tick_new_checked_with_zero_size_error
func TestTradeTickNewCheckedWithZeroSizeError(t *testing.T) {
	_, err := NewTradeTick("ETH-USDT-SWAP.OKX", decimal.MustPrice("10000.00"), decimal.MustQuantity("0"), Buyer, "123456789", 0, 1)
	if err == nil || !strings.Contains(err.Error(), "invalid `Quantity` for 'size' not positive") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:285
//	test: test_trade_tick_builder
//
// Adaptations:
//   - The Rust builder is replaced by the checked Go constructor; all asserted
//     built fields are preserved.
func TestTradeTickBuilder(t *testing.T) {
	trade := MustTradeTick("BTCUSD.CRYPTO", decimal.MustPrice("50000.00"), decimal.MustQuantity("0.50"), Seller, "T-999", 3_000_000_000, 4_000_000_000)
	assertTradeFields(t, trade, "BTCUSD.CRYPTO", "50000.00", "0.50", Seller, "T-999", 3_000_000_000, 4_000_000_000)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:307
//	test: test_get_metadata
func TestTradeGetMetadata(t *testing.T) {
	got := TradeMetadata("EURUSD.SIM", 5, 8)
	if len(got) != 3 || got["instrument_id"] != "EURUSD.SIM" || got["price_precision"] != "5" || got["size_precision"] != "8" {
		t.Fatalf("unexpected metadata: %#v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:321
//	test: test_get_fields
//
// Adaptations:
//   - The rewrite uses the pinned high-precision representation, so
//     FixedSizeBinary(16) is authoritative.
func TestTradeGetFields(t *testing.T) {
	want := []Field{
		{"price", "FixedSizeBinary(16)"}, {"size", "FixedSizeBinary(16)"},
		{"aggressor_side", "UInt8"}, {"trade_id", "Utf8"},
		{"ts_event", "UInt64"}, {"ts_init", "UInt64"},
	}
	got := TradeFields()
	if len(got) != len(want) {
		t.Fatalf("got %d fields, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("field %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:350
//	test: test_trade_tick_with_different_aggressor_sides
func TestTradeTickWithDifferentAggressorSides(t *testing.T) {
	for _, side := range []AggressorSide{Buyer, Seller, NoAggressor} {
		trade := MustTradeTick("TEST.SIM", decimal.MustPrice("100.00"), decimal.MustQuantity("1000"), side, "T-TEST", 1_000_000_000, 2_000_000_000)
		if trade.AggressorSide != side {
			t.Errorf("side = %s, want %s", trade.AggressorSide, side)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:365
//	test: test_trade_tick_hash
func TestTradeTickHash(t *testing.T) {
	if testTrade().Hash64() != testTrade().Hash64() {
		t.Fatal("equal trades have different hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:379
//	test: test_trade_tick_hash_different_trades
func TestTradeTickHashDifferentTrades(t *testing.T) {
	left := testTrade()
	right := testTrade()
	right.Price = decimal.MustPrice("1.0501")
	if left.Hash64() == right.Hash64() {
		t.Fatal("different trades have equal hashes")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:394
//	test: test_trade_tick_partial_eq
func TestTradeTickPartialEq(t *testing.T) {
	left, equal, different := testTrade(), testTrade(), testTrade()
	different.Size = decimal.MustQuantity("80000")
	if !left.Equal(equal) || left.Equal(different) {
		t.Fatal("unexpected equality result")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:405
//	test: test_trade_tick_clone
func TestTradeTickClone(t *testing.T) {
	left := testTrade()
	right := left
	if !left.Equal(right) ||
		left.InstrumentID != right.InstrumentID || !left.Price.Equal(right.Price) ||
		!left.Size.Equal(right.Size) || left.AggressorSide != right.AggressorSide ||
		left.TradeID != right.TradeID || left.TsEvent != right.TsEvent || left.TsInit != right.TsInit {
		t.Fatal("copied trade differs from source")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:420
//	test: test_trade_tick_debug
func TestTradeTickDebug(t *testing.T) {
	got := testTrade().DebugString()
	for _, want := range []string{"TradeTick", "EURUSD.SIM", "1.0500", "Buyer", "T-001"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:432
//	test: test_trade_tick_has_ts_init
func TestTradeTickHasTsInit(t *testing.T) {
	if got := testTrade().TsInit; got != 2_000_000_000 {
		t.Fatalf("TsInit = %d", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:438
//	test: test_trade_tick_display
func TestTradeTickDisplay(t *testing.T) {
	got := testTrade().String()
	for _, want := range []string{"EURUSD.SIM", "1.0500", "100000", "BUYER", "T-001", "1000000000"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:451
//	test: test_trade_tick_serialization
func TestTradeTickSerialization(t *testing.T) {
	source := testTrade()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TradeTick
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !source.Equal(decoded) {
		t.Fatalf("round trip differs: %s", data)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:461
//	test: test_trade_tick_with_zero_price
func TestTradeTickWithZeroPrice(t *testing.T) {
	trade := MustTradeTick("TEST.SIM", decimal.MustPrice("0.0000"), decimal.MustQuantity("1000.0000"), Buyer, "T-ZERO", 0, 0)
	if !trade.Price.Decimal().IsZero() || trade.TsEvent != 0 || trade.TsInit != 0 {
		t.Fatalf("unexpected trade: %+v", trade)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:478
//	test: test_trade_tick_with_max_values
func TestTradeTickWithMaxValues(t *testing.T) {
	trade := MustTradeTick("TEST.SIM", decimal.MustPrice("999999.9999"), decimal.MustQuantity("999999999.9999"), Seller, "T-MAX", math.MaxUint64, math.MaxUint64)
	if trade.TsEvent != math.MaxUint64 || trade.TsInit != math.MaxUint64 {
		t.Fatalf("unexpected timestamps: %+v", trade)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:494
//	test: test_trade_tick_with_different_trade_ids
func TestTradeTickWithDifferentTradeIDs(t *testing.T) {
	left := MustTradeTick("TEST.SIM", decimal.MustPrice("100.00"), decimal.MustQuantity("1000"), Buyer, "TRADE-123", 1_000_000_000, 2_000_000_000)
	right := MustTradeTick("TEST.SIM", decimal.MustPrice("100.00"), decimal.MustQuantity("1000"), Buyer, "TRADE-456", 1_000_000_000, 2_000_000_000)
	if left.TradeID == right.TradeID || left.Equal(right) {
		t.Fatal("different trade IDs compare equal")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:520
//	test: test_to_string
func TestTradeToString(t *testing.T) {
	const want = "ETHUSDT-PERP.BINANCE,10000.0000,1.00000000,BUYER,123456789,0"
	if got := ethUSDTTrade().String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/trade.rs:529
//	test: test_deserialize_raw_string
func TestTradeDeserializeRawString(t *testing.T) {
	data := []byte(`{
		"type":"TradeTick",
		"instrument_id":"ETHUSDT-PERP.BINANCE",
		"price":"10000.0000",
		"size":"1.00000000",
		"aggressor_side":"BUYER",
		"trade_id":"123456789",
		"ts_event":0,
		"ts_init":1
	}`)
	var trade TradeTick
	if err := json.Unmarshal(data, &trade); err != nil {
		t.Fatal(err)
	}
	if trade.AggressorSide != Buyer || trade.InstrumentID != "ETHUSDT-PERP.BINANCE" ||
		trade.Price.String() != "10000.0000" || trade.Size.String() != "1.00000000" ||
		trade.TradeID != "123456789" {
		t.Fatalf("unexpected trade: %+v", trade)
	}
}
