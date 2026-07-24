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

func ethCurrency() currency.Currency {
	return currency.MustNew("ETH", 8, 0, "Ether", currency.Crypto)
}

func ethusdtPerpetual(t *testing.T) CryptoPerpetual {
	t.Helper()
	maxQuantity := decimal.MustQuantity("10000.0")
	minQuantity := decimal.MustQuantity("0.001")
	minNotional := money.MustNew("10.00", currency.USDT())
	maxPrice := decimal.MustPrice("15000.00")
	minPrice := decimal.MustPrice("1.0")
	marginInit := decimal.MustParse("1.0")
	marginMaint := decimal.MustParse("0.35")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	result, err := NewCheckedCryptoPerpetual(CryptoPerpetualConfig{
		InstrumentID: ids.MustInstrumentID("ETHUSDT-PERP.BINANCE"),
		RawSymbol:    ids.MustSymbol("ETHUSDT"),
		BaseCurrency: ethCurrency(), QuoteCurrency: currency.USDT(), SettlementCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 3,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.001"),
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint, MakerFee: &makerFee, TakerFee: &takerFee,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func validPerpetualConfig() CryptoPerpetualConfig {
	return CryptoPerpetualConfig{
		InstrumentID: ids.MustInstrumentID("TEST.EXCHANGE"),
		RawSymbol:    ids.MustSymbol("TEST"),
		BaseCurrency: currency.BTC(), QuoteCurrency: currency.USDT(), SettlementCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 0,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("1"),
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:499
//	test: test_trait_accessors
func TestCryptoPerpetualTraitAccessors(t *testing.T) {
	perpetual := ethusdtPerpetual(t)
	if perpetual.InstrumentID != ids.MustInstrumentID("ETHUSDT-PERP.BINANCE") ||
		perpetual.AssetClass() != AssetClassCryptocurrency ||
		perpetual.InstrumentClass() != InstrumentClassSwap {
		t.Fatal("identity or class accessor differs")
	}
	if !perpetual.BaseCurrency.Equal(ethCurrency()) ||
		!perpetual.QuoteCurrency.Equal(currency.USDT()) ||
		!perpetual.SettlementCurrency.Equal(currency.USDT()) || perpetual.Inverse {
		t.Fatal("currency or inverse accessor differs")
	}
	if perpetual.PricePrecision != 2 || perpetual.SizePrecision != 3 ||
		!perpetual.PriceIncrement.Equal(decimal.MustPrice("0.01")) ||
		!perpetual.SizeIncrement.Equal(decimal.MustQuantity("0.001")) ||
		!perpetual.Multiplier.Equal(decimal.MustQuantity("1")) ||
		!perpetual.LotSize.Equal(decimal.MustQuantity("1")) {
		t.Fatal("precision or increment accessor differs")
	}
	if perpetual.MaxQuantity == nil || !perpetual.MaxQuantity.Equal(decimal.MustQuantity("10000.0")) ||
		perpetual.MinQuantity == nil || !perpetual.MinQuantity.Equal(decimal.MustQuantity("0.001")) ||
		perpetual.MinNotional == nil || !perpetual.MinNotional.Equal(money.MustNew("10.00", currency.USDT())) {
		t.Fatal("limit accessor differs")
	}
	if perpetual.Underlying() != nil || perpetual.OptionKind() != nil ||
		perpetual.StrikePrice() != nil || perpetual.ActivationNanos() != nil ||
		perpetual.ExpirationNanos() != nil {
		t.Fatal("inapplicable derivative accessor was populated")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:557
//	test: test_inverse_perp_accessors
func TestCryptoPerpetualInversePerpAccessors(t *testing.T) {
	config := validPerpetualConfig()
	config.InstrumentID = ids.MustInstrumentID("BTCUSDT.BITMEX")
	config.RawSymbol = ids.MustSymbol("XBTUSD")
	config.QuoteCurrency = currency.USD()
	config.SettlementCurrency = currency.BTC()
	config.Inverse = true
	config.PricePrecision = 1
	config.PriceIncrement = decimal.MustPrice("0.5")
	perpetual, err := NewCheckedCryptoPerpetual(config)
	if err != nil {
		t.Fatal(err)
	}
	if !perpetual.Inverse || !perpetual.BaseCurrency.Equal(currency.BTC()) ||
		!perpetual.QuoteCurrency.Equal(currency.USD()) ||
		!perpetual.SettlementCurrency.Equal(currency.BTC()) ||
		!perpetual.CostCurrency().Equal(currency.BTC()) {
		t.Fatal("inverse currency accessors differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:566
//	test: test_new_checked_price_precision_mismatch
func TestCryptoPerpetualNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validPerpetualConfig()
	config.PricePrecision = 3
	if _, err := NewCheckedCryptoPerpetual(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:599
//	test: test_new_checked_size_precision_mismatch
func TestCryptoPerpetualNewCheckedSizePrecisionMismatch(t *testing.T) {
	config := validPerpetualConfig()
	config.SizePrecision = 5
	if _, err := NewCheckedCryptoPerpetual(config); err == nil {
		t.Fatal("expected size precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:634
//	test: test_new_checked_rejects_non_positive_sizing
func TestCryptoPerpetualNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	zero := decimal.MustQuantity("0")
	for _, test := range []struct {
		name string
		set  func(*CryptoPerpetualConfig)
	}{
		{"zero multiplier", func(config *CryptoPerpetualConfig) { config.Multiplier = &zero }},
		{"zero lot size", func(config *CryptoPerpetualConfig) { config.LotSize = &zero }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validPerpetualConfig()
			test.set(&config)
			_, err := NewCheckedCryptoPerpetual(config)
			if err == nil || !strings.Contains(err.Error(), "not positive") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:671
//	test: test_serialization_roundtrip
func TestCryptoPerpetualSerializationRoundtrip(t *testing.T) {
	original := ethusdtPerpetual(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored CryptoPerpetual
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed perpetual:\n%s\n%s", data, restoredData)
	}
}

func requiredPerpetualBuilder() *CryptoPerpetualBuilder {
	return NewCryptoPerpetualBuilder().
		Instrument(ids.MustInstrumentID("ETHUSDT-PERP.BINANCE")).
		Symbol(ids.MustSymbol("ETHUSDT")).
		Base(ethCurrency()).
		Quote(currency.USDT()).
		Settlement(currency.USDT()).
		IsInverse(false).
		PriceDigits(2).
		SizeDigits(3).
		TickSize(decimal.MustPrice("0.01")).
		StepSize(decimal.MustQuantity("0.001")).
		Timestamps(0, 0)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:678
//	test: test_builder_matches_new_checked
func TestCryptoPerpetualBuilderMatchesNewChecked(t *testing.T) {
	config := CryptoPerpetualConfig{
		InstrumentID: ids.MustInstrumentID("ETHUSDT-PERP.BINANCE"), RawSymbol: ids.MustSymbol("ETHUSDT"),
		BaseCurrency: ethCurrency(), QuoteCurrency: currency.USDT(), SettlementCurrency: currency.USDT(),
		PricePrecision: 2, SizePrecision: 3,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.001"),
		MaxQuantity: func() *decimal.Quantity { value := decimal.MustQuantity("10000.0"); return &value }(),
	}
	positional, err := NewCheckedCryptoPerpetual(config)
	if err != nil {
		t.Fatal(err)
	}
	built, err := requiredPerpetualBuilder().WithMaxQuantity(decimal.MustQuantity("10000.0")).Build()
	if err != nil {
		t.Fatal(err)
	}
	positionalJSON, _ := json.Marshal(positional)
	builtJSON, _ := json.Marshal(built)
	if !bytes.Equal(positionalJSON, builtJSON) {
		t.Fatalf("builder differs:\n%s\n%s", positionalJSON, builtJSON)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:733
//	test: test_builder_applies_defaults_for_omitted_optionals
func TestCryptoPerpetualBuilderAppliesDefaultsForOmittedOptionals(t *testing.T) {
	perpetual, err := requiredPerpetualBuilder().Build()
	if err != nil {
		t.Fatal(err)
	}
	if !perpetual.Multiplier.Equal(decimal.MustQuantity("1")) ||
		!perpetual.LotSize.Equal(decimal.MustQuantity("1")) ||
		!perpetual.MarginInit.IsZero() || !perpetual.MarginMaint.IsZero() ||
		!perpetual.MakerFee.IsZero() || !perpetual.TakerFee.IsZero() ||
		perpetual.MaxQuantity != nil || perpetual.MinNotional != nil ||
		perpetual.TickScheme != nil || perpetual.Info != nil {
		t.Fatalf("optional defaults differ: %+v", perpetual)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:763
//	test: test_builder_sets_optional_fields_via_value_and_maybe_setters
func TestCryptoPerpetualBuilderSetsOptionalFieldsViaValueAndMaybeSetters(t *testing.T) {
	minNotional := money.MustNew("10.00", currency.USDT())
	perpetual, err := requiredPerpetualBuilder().
		WithMaxQuantity(decimal.MustQuantity("10000.0")).
		WithMinNotional(&minNotional).
		WithMakerFee(decimal.MustParse("0.0002")).
		Build()
	if err != nil {
		t.Fatal(err)
	}
	if perpetual.MaxQuantity == nil || !perpetual.MaxQuantity.Equal(decimal.MustQuantity("10000.0")) ||
		perpetual.MinNotional == nil || !perpetual.MinNotional.Equal(minNotional) ||
		!perpetual.MakerFee.Equal(decimal.MustParse("0.0002")) {
		t.Fatal("builder optional fields differ")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_perpetual.rs:789
//	test: test_builder_propagates_validation_error
func TestCryptoPerpetualBuilderPropagatesValidationError(t *testing.T) {
	_, err := NewCryptoPerpetualBuilder().
		Instrument(ids.MustInstrumentID("TEST.EXCHANGE")).
		Symbol(ids.MustSymbol("TEST")).
		Base(currency.BTC()).
		Quote(currency.USDT()).
		Settlement(currency.USDT()).
		IsInverse(false).
		PriceDigits(3).
		SizeDigits(0).
		TickSize(decimal.MustPrice("0.01")).
		StepSize(decimal.MustQuantity("1")).
		Timestamps(0, 0).
		Build()
	if err == nil {
		t.Fatal("expected builder validation error")
	}
}
