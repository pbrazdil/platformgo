package instrument

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
)

const cryptoOptionSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func cryptoOptionUSDC() currency.Currency {
	return currency.MustNew("USDC", 8, 0, "USD Coin", currency.Crypto)
}

func btcDeribitOptionFixture(t *testing.T) CryptoOption {
	t.Helper()
	multiplier := decimal.MustQuantity("1")
	lotSize := decimal.MustQuantity("1")
	maxQuantity := decimal.MustQuantity("9000.0")
	minQuantity := decimal.MustQuantity("0.1")
	minNotional := money.MustNew("10.00", currency.USD())
	makerFee := decimal.MustParse("0.0003")
	takerFee := decimal.MustParse("0.0003")
	option, err := NewCheckedCryptoOption(CryptoOptionConfig{
		InstrumentID: ids.MustInstrumentID("BTC-13JAN23-16000-P.DERIBIT"),
		RawSymbol:    ids.MustSymbol("BTC-13JAN23-16000-P"),
		Underlying:   currency.BTC(), QuoteCurrency: currency.USD(), SettlementCurrency: currency.BTC(),
		Inverse: false, OptionKind: OptionKindPut, StrikePrice: decimal.MustPrice("16000.000"),
		Activation: 1_671_696_002_000_000_000, Expiration: 1_673_596_800_000_000_000,
		PricePrecision: 3, SizePrecision: 1,
		PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("0.1"),
		Multiplier: &multiplier, LotSize: &lotSize,
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity, MinNotional: &minNotional,
		MakerFee: &makerFee, TakerFee: &takerFee,
	})
	if err != nil {
		t.Fatal(err)
	}
	return option
}

func validCryptoOptionConfig() CryptoOptionConfig {
	return CryptoOptionConfig{
		InstrumentID: ids.MustInstrumentID("TEST.DERIBIT"),
		RawSymbol:    ids.MustSymbol("TEST"),
		Underlying:   currency.BTC(), QuoteCurrency: currency.USD(), SettlementCurrency: currency.BTC(),
		OptionKind: OptionKindCall, StrikePrice: decimal.MustPrice("50000.0"),
		PricePrecision: 1, SizePrecision: 1,
		PriceIncrement: decimal.MustPrice("0.1"), SizeIncrement: decimal.MustQuantity("0.1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option.rs:533
//	test: test_trait_accessors
func TestCryptoOptionTraitAccessors(t *testing.T) {
	option := btcDeribitOptionFixture(t)
	if option.InstrumentID != ids.MustInstrumentID("BTC-13JAN23-16000-P.DERIBIT") ||
		option.AssetClass() != AssetClassCryptocurrency ||
		option.InstrumentClass() != InstrumentClassOption {
		t.Fatal("identity or class accessor differs")
	}
	kind := option.OptionKindValue()
	strike := option.StrikePriceValue()
	if kind == nil || *kind != OptionKindPut ||
		strike == nil || !strike.Equal(decimal.MustPrice("16000.000")) {
		t.Fatal("option kind or strike accessor differs")
	}
	if option.IsInverse() || option.PricePrecision != 3 || option.SizePrecision != 1 ||
		option.MinQuantity == nil || !option.MinQuantity.Equal(decimal.MustQuantity("0.1")) ||
		option.ActivationNanosValue() == nil || option.ExpirationNanosValue() == nil {
		t.Fatal("inverse, precision, minimum quantity, or timestamp accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option.rs:566
//	test: test_new_checked_price_precision_mismatch
func TestCryptoOptionNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCryptoOptionConfig()
	config.PricePrecision = 4
	config.PriceIncrement = decimal.MustPrice("0.001")
	if _, err := NewCheckedCryptoOption(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option.rs:603
//	test: test_new_checked_rejects_non_positive_lot_size
func TestCryptoOptionNewCheckedRejectsNonPositiveLotSize(t *testing.T) {
	config := validCryptoOptionConfig()
	zero := decimal.MustQuantity("0")
	config.LotSize = &zero
	if _, err := NewCheckedCryptoOption(config); err == nil {
		t.Fatal("expected non-positive lot-size failure")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option.rs:640
//	test: test_serialization_roundtrip
func TestCryptoOptionSerializationRoundtrip(t *testing.T) {
	option := btcDeribitOptionFixture(t)
	data, err := json.Marshal(option)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized CryptoOption
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !option.Equal(deserialized) {
		t.Fatal("round-trip changed crypto-option identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option.rs:647
//	test: test_builder_matches_new_checked
func TestCryptoOptionBuilderMatchesNewChecked(t *testing.T) {
	usdc := cryptoOptionUSDC()
	multiplier := decimal.MustQuantity("10")
	lotSize := decimal.MustQuantity("5")
	maxQuantity := decimal.MustQuantity("1000.0")
	minQuantity := decimal.MustQuantity("0.1")
	maxNotional := money.MustNew("1000000", usdc)
	minNotional := money.MustNew("10", usdc)
	maxPrice := decimal.MustPrice("99999.999")
	minPrice := decimal.MustPrice("0.001")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")

	positional, err := NewCheckedCryptoOption(CryptoOptionConfig{
		InstrumentID: ids.MustInstrumentID("BTC-13JAN23-16000-P.DERIBIT"),
		RawSymbol:    ids.MustSymbol("BTC-13JAN23-16000-P"),
		Underlying:   currency.BTC(), QuoteCurrency: usdc, SettlementCurrency: currency.USDT(),
		Inverse: false, OptionKind: OptionKindPut, StrikePrice: decimal.MustPrice("16000.000"),
		Activation: 1, Expiration: 2,
		PricePrecision: 3, SizePrecision: 1,
		PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("0.1"),
		Multiplier: &multiplier, LotSize: &lotSize,
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 1, TsInit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	built, err := NewCryptoOptionBuilder().
		Instrument(ids.MustInstrumentID("BTC-13JAN23-16000-P.DERIBIT")).
		Symbol(ids.MustSymbol("BTC-13JAN23-16000-P")).
		Currencies(currency.BTC(), usdc, currency.USDT()).
		IsInverse(false).
		Contract(OptionKindPut, decimal.MustPrice("16000.000")).
		ActiveBetween(1, 2).
		Precisions(3, 1).
		Increments(decimal.MustPrice("0.001"), decimal.MustQuantity("0.1")).
		Sizing(multiplier, lotSize).
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
