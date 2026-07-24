package instrument

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

const optionSpreadSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func optionSpreadFixture(t *testing.T) OptionSpread {
	t.Helper()
	exchange := "XCME"
	spread, err := NewCheckedOptionSpread(OptionSpreadConfig{
		InstrumentID: ids.MustInstrumentID("UD:U$: GN 2534559.GLBX"),
		RawSymbol:    ids.MustSymbol("UD:U$: GN 2534559"),
		AssetClass:   AssetClassFX,
		Exchange:     &exchange, Underlying: "SR3", StrategyType: "GN",
		Activation: 1_699_304_047_000_000_000, Expiration: 1_708_729_140_000_000_000,
		Currency: currency.USD(), PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("1"), LotSize: decimal.MustQuantity("1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return spread
}

func validOptionSpreadConfig() OptionSpreadConfig {
	exchange := "XCME"
	return OptionSpreadConfig{
		InstrumentID: ids.MustInstrumentID("TEST.GLBX"),
		RawSymbol:    ids.MustSymbol("TEST"),
		AssetClass:   AssetClassFX,
		Exchange:     &exchange, Underlying: "SR3", StrategyType: "GN",
		Currency: currency.USD(), PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("1"), LotSize: decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_spread.rs:495
//	test: test_trait_accessors
func TestOptionSpreadTraitAccessors(t *testing.T) {
	spread := optionSpreadFixture(t)
	if spread.InstrumentID != ids.MustInstrumentID("UD:U$: GN 2534559.GLBX") ||
		spread.AssetClass != AssetClassFX ||
		spread.InstrumentClass() != InstrumentClassOptionSpread {
		t.Fatal("identity or class accessor differs")
	}
	if !spread.QuoteCurrency().Equal(currency.USD()) || spread.IsInverse() ||
		spread.Exchange == nil || *spread.Exchange != "XCME" ||
		spread.SizePrecision != 0 ||
		!spread.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		spread.MinQuantity == nil || !spread.MinQuantity.Equal(decimal.MustQuantity("1")) {
		t.Fatal("currency, exchange, inverse, sizing, or minimum-quantity accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_spread.rs:514
//	test: test_new_checked_price_precision_mismatch
func TestOptionSpreadNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validOptionSpreadConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedOptionSpread(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_spread.rs:546
//	test: test_serialization_roundtrip
func TestOptionSpreadSerializationRoundtrip(t *testing.T) {
	spread := optionSpreadFixture(t)
	data, err := json.Marshal(spread)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized OptionSpread
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !spread.Equal(deserialized) {
		t.Fatal("round-trip changed option-spread identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_spread.rs:553
//	test: test_builder_matches_new_checked
func TestOptionSpreadBuilderMatchesNewChecked(t *testing.T) {
	exchange := "XCME"
	maxQuantity := decimal.MustQuantity("100")
	minQuantity := decimal.MustQuantity("1")
	maxPrice := decimal.MustPrice("999.0")
	minPrice := decimal.MustPrice("1.0")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")

	positional, err := NewCheckedOptionSpread(OptionSpreadConfig{
		InstrumentID: ids.MustInstrumentID("UD:U$: GN 2534559.GLBX"),
		RawSymbol:    ids.MustSymbol("UD:U$: GN 2534559"),
		AssetClass:   AssetClassFX,
		Exchange:     &exchange, Underlying: "SR3", StrategyType: "GN",
		Activation: 1, Expiration: 2, Currency: currency.USD(),
		PricePrecision: 2, PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier: decimal.MustQuantity("10"), LotSize: decimal.MustQuantity("5"),
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 3, TsInit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	built, err := NewOptionSpreadBuilder().
		Instrument(ids.MustInstrumentID("UD:U$: GN 2534559.GLBX")).
		Symbol(ids.MustSymbol("UD:U$: GN 2534559")).
		Class(AssetClassFX).
		OnExchange("XCME").
		ForUnderlying("SR3").
		WithStrategy("GN").
		ActiveBetween(1, 2).
		DenominatedIn(currency.USD()).
		Price(2, decimal.MustPrice("0.01")).
		Sizing(decimal.MustQuantity("10"), decimal.MustQuantity("5")).
		QuantityLimits(maxQuantity, minQuantity).
		PriceLimits(maxPrice, minPrice).
		Margins(marginInit, marginMaint).
		Fees(makerFee, takerFee).
		Timestamps(3, 4).
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
