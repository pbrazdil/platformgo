package instrument

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func aaplEquity(t *testing.T) Equity {
	t.Helper()
	isin := "US0378331005"
	equity, err := NewCheckedEquity(EquityConfig{
		InstrumentID:   ids.MustInstrumentID("AAPL.XNAS"),
		RawSymbol:      ids.MustSymbol("AAPL"),
		ISIN:           &isin,
		Currency:       currency.USD(),
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return equity
}

func validEquityConfig() EquityConfig {
	return EquityConfig{
		InstrumentID:   ids.MustInstrumentID("AAPL.XNAS"),
		RawSymbol:      ids.MustSymbol("AAPL"),
		Currency:       currency.USD(),
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:438
//	test: test_trait_accessors
func TestEquityTraitAccessors(t *testing.T) {
	equity := aaplEquity(t)
	if equity.InstrumentID != ids.MustInstrumentID("AAPL.XNAS") ||
		equity.RawSymbol != ids.MustSymbol("AAPL") ||
		equity.AssetClass() != AssetClassEquity ||
		equity.InstrumentClass() != InstrumentClassSpot {
		t.Fatal("identity or class accessor differs")
	}
	if !equity.QuoteCurrency().Equal(currency.USD()) ||
		!equity.SettlementCurrency().Equal(currency.USD()) ||
		equity.IsInverse() || equity.PricePrecision != 2 || equity.SizePrecision() != 0 ||
		!equity.PriceIncrement.Equal(decimal.MustPrice("0.01")) ||
		!equity.SizeIncrement().Equal(decimal.MustQuantity("1")) ||
		!equity.Multiplier().Equal(decimal.MustQuantity("1")) {
		t.Fatal("currency, precision, or sizing accessor differs")
	}
	if equity.BaseCurrency() != nil || equity.Underlying() != nil ||
		equity.OptionKind() != nil || equity.StrikePrice() != nil ||
		equity.ActivationNanos() != nil || equity.ExpirationNanos() != nil {
		t.Fatal("inapplicable accessor was populated")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:460
//	test: test_isin
func TestEquityISIN(t *testing.T) {
	equity := aaplEquity(t)
	if equity.ISIN == nil || *equity.ISIN != "US0378331005" {
		t.Fatalf("ISIN = %v", equity.ISIN)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:468
//	test: test_new_checked_price_precision_mismatch
func TestEquityNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validEquityConfig()
	config.PricePrecision = 3
	if _, err := NewCheckedEquity(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:494
//	test: test_new_checked_zero_price_increment
func TestEquityNewCheckedZeroPriceIncrement(t *testing.T) {
	config := validEquityConfig()
	config.PricePrecision = 0
	config.PriceIncrement = decimal.MustPrice("0")
	if _, err := NewCheckedEquity(config); err == nil {
		t.Fatal("expected zero price-increment error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:520
//	test: test_new_checked_non_ascii_isin
func TestEquityNewCheckedNonASCIIISIN(t *testing.T) {
	config := validEquityConfig()
	isin := "USé378331005"
	config.ISIN = &isin
	_, err := NewCheckedEquity(config)
	if err == nil || !strings.Contains(err.Error(), "non-ASCII") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:547
//	test: test_new_checked_rejects_non_positive_lot_size
func TestEquityNewCheckedRejectsNonPositiveLotSize(t *testing.T) {
	config := validEquityConfig()
	lotSize := decimal.MustQuantity("0")
	config.LotSize = &lotSize
	_, err := NewCheckedEquity(config)
	if err == nil || !strings.Contains(err.Error(), "not positive") {
		t.Fatalf("error = %v", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:574
//	test: test_serialization_roundtrip
func TestEquitySerializationRoundtrip(t *testing.T) {
	original := aaplEquity(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored Equity
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed equity:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/equity.rs:581
//	test: test_builder_matches_new_checked
func TestEquityBuilderMatchesNewChecked(t *testing.T) {
	isin := "US0378331005"
	lotSize := decimal.MustQuantity("100")
	maxQuantity := decimal.MustQuantity("10000.0")
	minQuantity := decimal.MustQuantity("0.001")
	maxPrice := decimal.MustPrice("9999.99")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedEquity(EquityConfig{
		InstrumentID:   ids.MustInstrumentID("AAPL.XNAS"),
		RawSymbol:      ids.MustSymbol("AAPL"),
		ISIN:           &isin,
		Currency:       currency.USD(),
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		LotSize:        &lotSize,
		MaxQuantity:    &maxQuantity,
		MinQuantity:    &minQuantity,
		MaxPrice:       &maxPrice,
		MinPrice:       &minPrice,
		MarginInit:     &marginInit,
		MarginMaint:    &marginMaint,
		MakerFee:       &makerFee,
		TakerFee:       &takerFee,
		TsEvent:        1,
		TsInit:         2,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewEquityBuilder().
		Instrument(ids.MustInstrumentID("AAPL.XNAS")).
		Symbol(ids.MustSymbol("AAPL")).
		WithISIN("US0378331005").
		DenominatedIn(currency.USD()).
		PriceDigits(2).
		TickSize(decimal.MustPrice("0.01")).
		WithLotSize(decimal.MustQuantity("100")).
		WithMaxQuantity(decimal.MustQuantity("10000.0")).
		WithMinQuantity(decimal.MustQuantity("0.001")).
		WithMaxPrice(decimal.MustPrice("9999.99")).
		WithMinPrice(decimal.MustPrice("0.01")).
		Margins(decimal.MustParse("0.01"), decimal.MustParse("0.02")).
		Fees(decimal.MustParse("0.0002"), decimal.MustParse("0.0004")).
		Timestamps(1, 2).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, _ := json.Marshal(positional)
	builtJSON, _ := json.Marshal(built)
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs:\n%s\n%s", positionalJSON, builtJSON)
	}
}
