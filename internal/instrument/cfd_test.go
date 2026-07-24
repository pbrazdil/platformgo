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

const cfdSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func cfdEUR() currency.Currency {
	return currency.MustNew("EUR", 2, 978, "Euro", currency.Fiat)
}

func goldCfdFixture(t *testing.T) Cfd {
	t.Helper()
	lotSize := decimal.MustQuantity("1")
	cfd, err := NewCheckedCfd(CfdConfig{
		InstrumentID:   ids.MustInstrumentID("GOLD-CFD.SIM"),
		RawSymbol:      ids.MustSymbol("GOLD-CFD"),
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
	return cfd
}

func validCfdConfig() CfdConfig {
	return CfdConfig{
		InstrumentID:   ids.MustInstrumentID("TEST.SIM"),
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
//	source: crates/model/src/instruments/cfd.rs:483
//	test: test_trait_accessors
func TestCfdTraitAccessors(t *testing.T) {
	gold := goldCfdFixture(t)
	if gold.InstrumentID != ids.MustInstrumentID("GOLD-CFD.SIM") ||
		gold.AssetClass() != AssetClass("COMMODITY") ||
		gold.InstrumentClass() != InstrumentClassCFD {
		t.Fatal("identity or class accessor differs")
	}
	if !gold.QuoteCurrency.Equal(currency.USD()) || gold.IsInverse() ||
		gold.PricePrecision != 2 || gold.SizePrecision != 0 {
		t.Fatal("currency, inverse, or precision accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/cfd.rs:494
//	test: test_new_checked_price_precision_mismatch
func TestCfdNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCfdConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedCfd(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/cfd.rs:525
//	test: test_new_checked_rejects_non_positive_lot_size
func TestCfdNewCheckedRejectsNonPositiveLotSize(t *testing.T) {
	config := validCfdConfig()
	zero := decimal.MustQuantity("0")
	config.LotSize = &zero
	_, err := NewCheckedCfd(config)
	if err == nil || !strings.Contains(err.Error(), "not positive") {
		t.Fatalf("error = %v, want not-positive failure", err)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/cfd.rs:557
//	test: test_serialization_roundtrip
func TestCfdSerializationRoundtrip(t *testing.T) {
	gold := goldCfdFixture(t)
	data, err := json.Marshal(gold)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized Cfd
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !gold.Equal(deserialized) {
		t.Fatal("round-trip changed CFD identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/cfd.rs:564
//	test: test_builder_matches_new_checked
func TestCfdBuilderMatchesNewChecked(t *testing.T) {
	euro := cfdEUR()
	lotSize := decimal.MustQuantity("100")
	maxQuantity := decimal.MustQuantity("10000.00")
	minQuantity := decimal.MustQuantity("5.00")
	maxNotional := money.MustNew("1000000", currency.USD())
	minNotional := money.MustNew("100", currency.USD())
	maxPrice := decimal.MustPrice("2.00000")
	minPrice := decimal.MustPrice("0.50000")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")

	positional, err := NewCheckedCfd(CfdConfig{
		InstrumentID: ids.MustInstrumentID("EURUSD-CFD.SIM"),
		RawSymbol:    ids.MustSymbol("EURUSD-CFD"),
		AssetClass:   AssetClassFX,
		BaseCurrency: &euro, QuoteCurrency: currency.USD(),
		PricePrecision: 5, SizePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.00001"), SizeIncrement: decimal.MustQuantity("0.01"),
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

	built, err := NewCfdBuilder().
		Instrument(ids.MustInstrumentID("EURUSD-CFD.SIM")).
		Symbol(ids.MustSymbol("EURUSD-CFD")).
		Class(AssetClassFX).
		Base(euro).
		Quote(currency.USD()).
		Precisions(5, 2).
		Increments(decimal.MustPrice("0.00001"), decimal.MustQuantity("0.01")).
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
