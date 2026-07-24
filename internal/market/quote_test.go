package market

import (
	"math"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func testQuote() QuoteTick {
	return MustQuoteTick(
		"EURUSD.SIM",
		decimal.MustPrice("1.0500"),
		decimal.MustPrice("1.0505"),
		decimal.MustQuantity("100000"),
		decimal.MustQuantity("75000"),
		1_000_000_000,
		2_000_000_000,
	)
}

func ethUSDTQuote() QuoteTick {
	return MustQuoteTick(
		"ETHUSDT-PERP.BINANCE",
		decimal.MustPrice("10000.0000"),
		decimal.MustPrice("10001.0000"),
		decimal.MustQuantity("1.00000000"),
		decimal.MustQuantity("1.00000000"),
		0,
		1,
	)
}

func assertQuoteFields(t *testing.T, quote QuoteTick, instrument string, bid, ask, bidSize, askSize string, event, init UnixNanos) {
	t.Helper()
	if quote.InstrumentID != InstrumentID(instrument) ||
		quote.BidPrice.String() != bid ||
		quote.AskPrice.String() != ask ||
		quote.BidSize.String() != bidSize ||
		quote.AskSize.String() != askSize ||
		quote.TsEvent != event ||
		quote.TsInit != init {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:267
//	test: test_quote_tick_new
func TestQuoteTickNew(t *testing.T) {
	assertQuoteFields(t, testQuote(), "EURUSD.SIM", "1.0500", "1.0505", "100000", "75000", 1_000_000_000, 2_000_000_000)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:280
//	test: test_quote_tick_new_checked_valid
func TestQuoteTickNewCheckedValid(t *testing.T) {
	quote, err := NewQuoteTick("GBPUSD.SIM", decimal.MustPrice("1.2500"), decimal.MustPrice("1.2505"), decimal.MustQuantity("50000"), decimal.MustQuantity("60000"), 500_000_000, 1_500_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if quote.InstrumentID != "GBPUSD.SIM" || quote.BidPrice.String() != "1.2500" || quote.AskPrice.String() != "1.2505" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:302
//	test: test_quote_tick_new_with_precision_mismatch_panics
func TestQuoteTickNewWithPrecisionMismatchPanics(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil || !strings.Contains(recovered.(error).Error(), "'bid_price.precision' u8 of 4 was not equal to 'ask_price.precision' u8 of 5") {
			t.Fatalf("unexpected panic: %v", recovered)
		}
	}()
	MustQuoteTick("ETH-USDT-SWAP.OKX", decimal.MustPrice("10000.0000"), decimal.MustPrice("10000.00100"), decimal.MustQuantity("1.000000"), decimal.MustQuantity("1.000000"), 0, 1)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:323
//	test: test_quote_tick_new_checked_with_precision_mismatch_error
func TestQuoteTickNewCheckedWithPrecisionMismatchError(t *testing.T) {
	_, err := NewQuoteTick("ETH-USDT-SWAP.OKX", decimal.MustPrice("10000.0000"), decimal.MustPrice("10000.0010"), decimal.MustQuantity("10.000000"), decimal.MustQuantity("10.0000000"), 0, 1)
	if err == nil || !strings.Contains(err.Error(), "'bid_size.precision' u8 of 6 was not equal to 'ask_size.precision' u8 of 7") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:349
//	test: test_quote_tick_builder
//
// Adaptations:
//   - The Rust builder is replaced by the checked Go constructor; all asserted
//     built fields are preserved.
func TestQuoteTickBuilder(t *testing.T) {
	quote := MustQuoteTick("BTCUSD.CRYPTO", decimal.MustPrice("50000.00"), decimal.MustPrice("50001.00"), decimal.MustQuantity("0.50"), decimal.MustQuantity("0.75"), 3_000_000_000, 4_000_000_000)
	assertQuoteFields(t, quote, "BTCUSD.CRYPTO", "50000.00", "50001.00", "0.50", "0.75", 3_000_000_000, 4_000_000_000)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:371
//	test: test_get_metadata
func TestQuoteGetMetadata(t *testing.T) {
	got := QuoteMetadata("EURUSD.SIM", 5, 8)
	if len(got) != 3 || got["instrument_id"] != "EURUSD.SIM" || got["price_precision"] != "5" || got["size_precision"] != "8" {
		t.Fatalf("unexpected metadata: %#v", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:385
//	test: test_get_fields
//
// Adaptations:
//   - The rewrite uses the pinned high-precision representation, so
//     FixedSizeBinary(16) is authoritative.
func TestQuoteGetFields(t *testing.T) {
	want := []Field{
		{"bid_price", "FixedSizeBinary(16)"}, {"ask_price", "FixedSizeBinary(16)"},
		{"bid_size", "FixedSizeBinary(16)"}, {"ask_size", "FixedSizeBinary(16)"},
		{"ts_event", "UInt64"}, {"ts_init", "UInt64"},
	}
	got := QuoteFields()
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
//	source: crates/model/src/data/quote.rs:437
//	test: test_extract_price
func TestQuoteExtractPrice(t *testing.T) {
	quote := ethUSDTQuote()
	for _, test := range []struct {
		priceType PriceType
		want      string
	}{{PriceTypeBid, "10000.0000"}, {PriceTypeAsk, "10001.0000"}, {PriceTypeMid, "10000.50000"}} {
		got, err := quote.ExtractPrice(test.priceType)
		if err != nil || !got.Equal(decimal.MustPrice(test.want)) {
			t.Errorf("%s: got %s, %v; want %s", test.priceType, got, err, test.want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:451
//	test: test_extract_size
func TestQuoteExtractSize(t *testing.T) {
	quote := ethUSDTQuote()
	for _, priceType := range []PriceType{PriceTypeBid, PriceTypeAsk, PriceTypeMid} {
		got, err := quote.ExtractSize(priceType)
		if err != nil || !got.Equal(decimal.MustQuantity("1.00000000")) {
			t.Errorf("%s: got %s, %v", priceType, got, err)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:462
//	test: test_extract_price_invalid_type
func TestQuoteExtractPriceInvalidType(t *testing.T) {
	_, err := testQuote().ExtractPrice(PriceTypeLast)
	if err == nil || err.Error() != "Cannot extract price from quote with price type LAST" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:472
//	test: test_extract_size_invalid_type
func TestQuoteExtractSizeInvalidType(t *testing.T) {
	_, err := testQuote().ExtractSize(PriceTypeLast)
	if err == nil || err.Error() != "Cannot extract size from quote with price type LAST" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:482
//	test: test_quote_tick_has_ts_init
func TestQuoteTickHasTsInit(t *testing.T) {
	if got := testQuote().TsInit; got != 2_000_000_000 {
		t.Fatalf("TsInit = %d", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:488
//	test: test_quote_tick_display
func TestQuoteTickDisplay(t *testing.T) {
	got := testQuote().String()
	for _, want := range []string{"EURUSD.SIM", "1.0500", "1.0505", "100000", "75000", "1000000000"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:501
//	test: test_quote_tick_with_zero_prices
func TestQuoteTickWithZeroPrices(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("0.0000"), decimal.MustPrice("0.0000"), decimal.MustQuantity("1000.0000"), decimal.MustQuantity("1000.0000"), 0, 0)
	if !quote.BidPrice.Decimal().IsZero() || !quote.AskPrice.Decimal().IsZero() || quote.TsEvent != 0 || quote.TsInit != 0 {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:519
//	test: test_quote_tick_with_max_values
func TestQuoteTickWithMaxValues(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("999999.9999"), decimal.MustPrice("999999.9999"), decimal.MustQuantity("999999999.9999"), decimal.MustQuantity("999999999.9999"), math.MaxUint64, math.MaxUint64)
	if quote.TsEvent != math.MaxUint64 || quote.TsInit != math.MaxUint64 {
		t.Fatalf("unexpected timestamps: %+v", quote)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:535
//	test: test_extract_mid_price_precision
func TestQuoteExtractMidPricePrecision(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("1.00"), decimal.MustPrice("1.02"), decimal.MustQuantity("100.00"), decimal.MustQuantity("100.00"), 1_000_000_000, 2_000_000_000)
	price, _ := quote.ExtractPrice(PriceTypeMid)
	size, _ := quote.ExtractSize(PriceTypeMid)
	if price.String() != "1.010" || size.String() != "100.000" {
		t.Fatalf("midpoint = %s, %s", price, size)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:554
//	test: test_extract_mid_price_uses_raw_midpoint_for_odd_negative_values
func TestQuoteExtractMidPriceUsesRawMidpointForOddNegativeValues(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("-0.0000000000000003"), decimal.MustPrice("-0.0000000000000002"), decimal.MustQuantity("1"), decimal.MustQuantity("1"), 0, 0)
	got, _ := quote.ExtractPrice(PriceTypeMid)
	if got.String() != "-0.0000000000000003" || got.Precision() != decimal.MaxPrecision {
		t.Fatalf("midpoint = %s (precision %d)", got, got.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:572
//	test: test_extract_mid_size_uses_raw_midpoint_for_odd_values
func TestQuoteExtractMidSizeUsesRawMidpointForOddValues(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("1"), decimal.MustPrice("1"), decimal.MustQuantity("0.0000000000000001"), decimal.MustQuantity("0.0000000000000002"), 0, 0)
	got, _ := quote.ExtractSize(PriceTypeMid)
	if got.String() != "0.0000000000000001" || got.Precision() != decimal.MaxPrecision {
		t.Fatalf("midpoint = %s (precision %d)", got, got.Precision())
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:590
//	test: test_extract_mid_size_precision
func TestQuoteExtractMidSizePrecision(t *testing.T) {
	quote := MustQuoteTick("TEST.SIM", decimal.MustPrice("1.00"), decimal.MustPrice("1.01"), decimal.MustQuantity("100.00"), decimal.MustQuantity("101.00"), 1_000_000_000, 2_000_000_000)
	got, _ := quote.ExtractSize(PriceTypeMid)
	if got.String() != "100.500" {
		t.Fatalf("midpoint = %s", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/data/quote.rs:607
//	test: test_to_string
func TestQuoteToString(t *testing.T) {
	const want = "ETHUSDT-PERP.BINANCE,10000.0000,10001.0000,1.00000000,1.00000000,0"
	if got := ethUSDTQuote().String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}
