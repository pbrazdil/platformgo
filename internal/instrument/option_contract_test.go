package instrument

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func applOptionContract(t *testing.T) OptionContract {
	t.Helper()
	exchange := "GMNI"
	contract, err := NewCheckedOptionContract(OptionContractConfig{
		InstrumentID:   ids.MustInstrumentID("AAPL211217C00150000.OPRA"),
		RawSymbol:      ids.MustSymbol("AAPL211217C00150000"),
		AssetClass:     AssetClassEquity,
		Exchange:       &exchange,
		Underlying:     "AAPL",
		OptionKind:     OptionKindCall,
		StrikePrice:    decimal.MustPrice("149.0"),
		Currency:       currency.USD(),
		Activation:     1_631_836_800_000_000_000,
		Expiration:     1_639_699_200_000_000_000,
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier:     decimal.MustQuantity("1"),
		LotSize:        decimal.MustQuantity("1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func validOptionContractConfig() OptionContractConfig {
	exchange := "GMNI"
	return OptionContractConfig{
		InstrumentID:   ids.MustInstrumentID("TEST.OPRA"),
		RawSymbol:      ids.MustSymbol("TEST"),
		AssetClass:     AssetClassEquity,
		Exchange:       &exchange,
		Underlying:     "AAPL",
		OptionKind:     OptionKindCall,
		StrikePrice:    decimal.MustPrice("150.0"),
		Currency:       currency.USD(),
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier:     decimal.MustQuantity("1"),
		LotSize:        decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_contract.rs:499
//	test: test_trait_accessors
func TestOptionContractTraitAccessors(t *testing.T) {
	contract := applOptionContract(t)
	if contract.InstrumentID != ids.MustInstrumentID("AAPL211217C00150000.OPRA") ||
		contract.AssetClass != AssetClassEquity ||
		contract.InstrumentClass() != InstrumentClassOption ||
		!contract.QuoteCurrency().Equal(currency.USD()) || contract.IsInverse() {
		t.Fatal("identity, class, or currency accessor differs")
	}
	optionKind := contract.OptionKindValue()
	strikePrice := contract.StrikePriceValue()
	underlying := contract.UnderlyingValue()
	if optionKind == nil || *optionKind != OptionKindCall ||
		strikePrice == nil || !strikePrice.Equal(decimal.MustPrice("149.0")) ||
		underlying == nil || *underlying != "AAPL" ||
		contract.Exchange == nil || *contract.Exchange != "GMNI" ||
		contract.ActivationNanosValue() == nil || contract.ExpirationNanosValue() == nil {
		t.Fatal("option metadata accessor differs")
	}
	if contract.SizePrecision != 0 ||
		!contract.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		contract.MinQuantity == nil ||
		!contract.MinQuantity.Equal(decimal.MustQuantity("1")) {
		t.Fatal("size defaults differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_contract.rs:529
//	test: test_new_checked_price_precision_mismatch
func TestOptionContractNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validOptionContractConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedOptionContract(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_contract.rs:562
//	test: test_new_checked_zero_multiplier
func TestOptionContractNewCheckedZeroMultiplier(t *testing.T) {
	config := validOptionContractConfig()
	config.Multiplier = decimal.MustQuantity("0")
	if _, err := NewCheckedOptionContract(config); err == nil {
		t.Fatal("expected zero multiplier error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_contract.rs:595
//	test: test_serialization_roundtrip
func TestOptionContractSerializationRoundtrip(t *testing.T) {
	original := applOptionContract(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored OptionContract
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed contract:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/option_contract.rs:602
//	test: test_builder_matches_new_checked
func TestOptionContractBuilderMatchesNewChecked(t *testing.T) {
	exchange := "GMNI"
	maxQuantity := decimal.MustQuantity("100")
	minQuantity := decimal.MustQuantity("1")
	maxPrice := decimal.MustPrice("999.0")
	minPrice := decimal.MustPrice("1.0")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedOptionContract(OptionContractConfig{
		InstrumentID: ids.MustInstrumentID("AAPL211217C00150000.OPRA"),
		RawSymbol:    ids.MustSymbol("AAPL211217C00150000"), AssetClass: AssetClassEquity,
		Exchange: &exchange, Underlying: "AAPL", OptionKind: OptionKindCall,
		StrikePrice: decimal.MustPrice("149.0"), Currency: currency.USD(),
		Activation: 1, Expiration: 2, PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"), Multiplier: decimal.MustQuantity("10"),
		LotSize: decimal.MustQuantity("5"), MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxPrice: &maxPrice, MinPrice: &minPrice, MarginInit: &marginInit,
		MarginMaint: &marginMaint, MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 3, TsInit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewOptionContractBuilder().
		Instrument(ids.MustInstrumentID("AAPL211217C00150000.OPRA")).
		Symbol(ids.MustSymbol("AAPL211217C00150000")).
		Class(AssetClassEquity).
		OnExchange("GMNI").
		ForUnderlying("AAPL").
		WithOptionKind(OptionKindCall).
		AtStrike(decimal.MustPrice("149.0")).
		DenominatedIn(currency.USD()).
		ActiveBetween(1, 2).
		PriceDigits(2).
		TickSize(decimal.MustPrice("0.01")).
		WithMultiplier(decimal.MustQuantity("10")).
		WithLotSize(decimal.MustQuantity("5")).
		WithMaxQuantity(decimal.MustQuantity("100")).
		WithMinQuantity(decimal.MustQuantity("1")).
		WithMaxPrice(decimal.MustPrice("999.0")).
		WithMinPrice(decimal.MustPrice("1.0")).
		Margins(decimal.MustParse("0.01"), decimal.MustParse("0.02")).
		Fees(decimal.MustParse("0.0002"), decimal.MustParse("0.0004")).
		Timestamps(3, 4).
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
