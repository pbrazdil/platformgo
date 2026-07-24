package instrument

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
)

func esFuturesContract(t *testing.T) FuturesContract {
	t.Helper()
	exchange := "XCME"
	contract, err := NewCheckedFuturesContract(FuturesContractConfig{
		InstrumentID:   ids.MustInstrumentID("ESZ21.GLBX"),
		RawSymbol:      ids.MustSymbol("ESZ21"),
		AssetClass:     AssetClassIndex,
		Exchange:       &exchange,
		Underlying:     "ES",
		Activation:     1_631_232_000_000_000_000,
		Expiration:     1_639_699_200_000_000_000,
		Currency:       currency.USD(),
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

func validFuturesContractConfig() FuturesContractConfig {
	exchange := "XCME"
	return FuturesContractConfig{
		InstrumentID:   ids.MustInstrumentID("ESZ21.GLBX"),
		RawSymbol:      ids.MustSymbol("ESZ21"),
		AssetClass:     AssetClassIndex,
		Exchange:       &exchange,
		Underlying:     "ES",
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
//	source: crates/model/src/instruments/futures_contract.rs:483
//	test: test_trait_accessors
func TestFuturesContractTraitAccessors(t *testing.T) {
	contract := esFuturesContract(t)
	if contract.InstrumentID != ids.MustInstrumentID("ESZ21.GLBX") ||
		contract.RawSymbol != ids.MustSymbol("ESZ21") ||
		contract.AssetClass != AssetClassIndex ||
		contract.InstrumentClass() != InstrumentClassFuture {
		t.Fatal("identity or class accessor differs")
	}
	if !contract.QuoteCurrency().Equal(currency.USD()) || contract.IsInverse() ||
		contract.PricePrecision != 2 || contract.SizePrecision != 0 ||
		!contract.PriceIncrement.Equal(decimal.MustPrice("0.01")) ||
		!contract.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		!contract.Multiplier.Equal(decimal.MustQuantity("1")) ||
		!contract.LotSize.Equal(decimal.MustQuantity("1")) {
		t.Fatal("currency, precision, or sizing accessor differs")
	}
	underlying := contract.UnderlyingValue()
	if underlying == nil || *underlying != "ES" ||
		contract.Exchange == nil || *contract.Exchange != "XCME" ||
		contract.ActivationNanosValue() == nil || contract.ExpirationNanosValue() == nil {
		t.Fatal("contract metadata accessor differs")
	}
	if contract.MinQuantity == nil || !contract.MinQuantity.Equal(decimal.MustQuantity("1")) {
		t.Fatalf("minimum quantity = %v", contract.MinQuantity)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_contract.rs:505
//	test: test_new_checked_price_precision_mismatch
func TestFuturesContractNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validFuturesContractConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedFuturesContract(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_contract.rs:536
//	test: test_new_checked_zero_multiplier
func TestFuturesContractNewCheckedZeroMultiplier(t *testing.T) {
	config := validFuturesContractConfig()
	config.Multiplier = decimal.MustQuantity("0")
	if _, err := NewCheckedFuturesContract(config); err == nil {
		t.Fatal("expected zero multiplier error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_contract.rs:567
//	test: test_new_checked_zero_lot_size
func TestFuturesContractNewCheckedZeroLotSize(t *testing.T) {
	config := validFuturesContractConfig()
	config.LotSize = decimal.MustQuantity("0")
	if _, err := NewCheckedFuturesContract(config); err == nil {
		t.Fatal("expected zero lot-size error")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/futures_contract.rs:598
//	test: test_serialization_roundtrip
func TestFuturesContractSerializationRoundtrip(t *testing.T) {
	original := esFuturesContract(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored FuturesContract
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
//	source: crates/model/src/instruments/futures_contract.rs:606
//	test: test_builder_matches_new_checked
func TestFuturesContractBuilderMatchesNewChecked(t *testing.T) {
	exchange := "XCME"
	maxQuantity := decimal.MustQuantity("10000")
	minQuantity := decimal.MustQuantity("5")
	maxPrice := decimal.MustPrice("9999.99")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedFuturesContract(FuturesContractConfig{
		InstrumentID:   ids.MustInstrumentID("ESZ21.GLBX"),
		RawSymbol:      ids.MustSymbol("ESZ21"),
		AssetClass:     AssetClassIndex,
		Exchange:       &exchange,
		Underlying:     "ES",
		Activation:     1_000,
		Expiration:     2_000,
		Currency:       currency.USD(),
		PricePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		Multiplier:     decimal.MustQuantity("50"),
		LotSize:        decimal.MustQuantity("10"),
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
	built, err := NewFuturesContractBuilder().
		Instrument(ids.MustInstrumentID("ESZ21.GLBX")).
		Symbol(ids.MustSymbol("ESZ21")).
		Class(AssetClassIndex).
		OnExchange("XCME").
		ForUnderlying("ES").
		ActiveBetween(1_000, 2_000).
		DenominatedIn(currency.USD()).
		PriceDigits(2).
		TickSize(decimal.MustPrice("0.01")).
		WithMultiplier(decimal.MustQuantity("50")).
		WithLotSize(decimal.MustQuantity("10")).
		WithMaxQuantity(decimal.MustQuantity("10000")).
		WithMinQuantity(decimal.MustQuantity("5")).
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
