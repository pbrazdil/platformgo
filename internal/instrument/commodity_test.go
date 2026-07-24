package instrument

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const commoditySourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func goldCommodityFixture(t *testing.T) Commodity {
	t.Helper()
	lotSize := decimal.MustQuantity("1")
	commodity, err := NewCheckedCommodity(CommodityConfig{
		InstrumentID:   ids.MustInstrumentID("GOLD.COMEX"),
		RawSymbol:      ids.MustSymbol("GOLD"),
		AssetClass:     AssetClass("COMMODITY"),
		QuoteCurrency:  currency.USD(),
		PricePrecision: 2, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("1"),
		LotSize:        &lotSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	return commodity
}

func validCommodityConfig() CommodityConfig {
	return CommodityConfig{
		InstrumentID:   ids.MustInstrumentID("TEST.COMEX"),
		RawSymbol:      ids.MustSymbol("TEST"),
		AssetClass:     AssetClass("COMMODITY"),
		QuoteCurrency:  currency.USD(),
		PricePrecision: 2, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/commodity.rs:478
//	test: test_trait_accessors
func TestCommodityTraitAccessors(t *testing.T) {
	gold := goldCommodityFixture(t)
	if gold.InstrumentID != ids.MustInstrumentID("GOLD.COMEX") ||
		gold.AssetClass() != AssetClass("COMMODITY") ||
		gold.InstrumentClass() != InstrumentClassSpot {
		t.Fatal("identity or class accessor differs")
	}
	if !gold.QuoteCurrency.Equal(currency.USD()) || gold.IsInverse() ||
		gold.PricePrecision != 2 || gold.SizePrecision != 0 ||
		!gold.AllowsNegativePrice() {
		t.Fatal("currency, inverse, precision, or negative-price accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/commodity.rs:490
//	test: test_new_checked_price_precision_mismatch
func TestCommodityNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCommodityConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedCommodity(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/commodity.rs:520
//	test: test_new_checked_rejects_non_positive_lot_size
func TestCommodityNewCheckedRejectsNonPositiveLotSize(t *testing.T) {
	config := validCommodityConfig()
	zero := decimal.MustQuantity("0")
	config.LotSize = &zero
	_, err := NewCheckedCommodity(config)
	if err == nil || !strings.Contains(err.Error(), "not positive") {
		t.Fatalf("error = %v, want not-positive failure", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/commodity.rs:551
//	test: test_serialization_roundtrip
func TestCommoditySerializationRoundtrip(t *testing.T) {
	gold := goldCommodityFixture(t)
	data, err := json.Marshal(gold)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized Commodity
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !gold.Equal(deserialized) {
		t.Fatal("round-trip changed commodity identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/commodity.rs:558
//	test: test_builder_matches_new_checked
func TestCommodityBuilderMatchesNewChecked(t *testing.T) {
	lotSize := decimal.MustQuantity("1")
	maxQuantity := decimal.MustQuantity("10000")
	minQuantity := decimal.MustQuantity("1")
	maxNotional := money.MustNew("5000000", currency.USD())
	minNotional := money.MustNew("10", currency.USD())
	maxPrice := decimal.MustPrice("100000.00")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")

	positional, err := NewCheckedCommodity(CommodityConfig{
		InstrumentID:   ids.MustInstrumentID("GOLD.COMEX"),
		RawSymbol:      ids.MustSymbol("GOLD"),
		AssetClass:     AssetClass("COMMODITY"),
		QuoteCurrency:  currency.USD(),
		PricePrecision: 2, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("1"),
		LotSize: &lotSize, MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	built, err := NewCommodityBuilder().
		Instrument(ids.MustInstrumentID("GOLD.COMEX")).
		Symbol(ids.MustSymbol("GOLD")).
		Class(AssetClass("COMMODITY")).
		Quote(currency.USD()).
		Precisions(2, 0).
		Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("1")).
		WithLotSize(lotSize).
		QuantityLimits(maxQuantity, minQuantity).
		NotionalLimits(maxNotional, minNotional).
		PriceLimits(maxPrice, minPrice).
		Margins(marginInit, marginMaint).
		Fees(makerFee, takerFee).
		Timestamps(1, 2).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, err := json.Marshal(positional)
	if err != nil {
		t.Fatal(err)
	}
	builtJSON, err := json.Marshal(built)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs from checked constructor:\n%s\n%s", positionalJSON, builtJSON)
	}
}
