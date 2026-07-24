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

func btcusdtCryptoFuture(t *testing.T) CryptoFuture {
	t.Helper()
	maxQuantity := decimal.MustQuantity("9000.0")
	minQuantity := decimal.MustQuantity("0.000001")
	minNotional := money.MustNew("10.00", currency.USDT())
	maxPrice := decimal.MustPrice("1000000.00")
	minPrice := decimal.MustPrice("0.01")
	future, err := NewCheckedCryptoFuture(CryptoFutureConfig{
		InstrumentID: ids.MustInstrumentID("ETHUSDT-123.BINANCE"),
		RawSymbol:    ids.MustSymbol("BTCUSDT"), Underlying: currency.BTC(),
		QuoteCurrency: currency.USDT(), SettlementCurrency: currency.USDT(),
		Activation: 1_396_915_200_000_000_000, Expiration: 1_404_777_600_000_000_000,
		PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.000001"),
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
	})
	if err != nil {
		t.Fatal(err)
	}
	return future
}

func validCryptoFutureConfig() CryptoFutureConfig {
	return CryptoFutureConfig{
		InstrumentID: ids.MustInstrumentID("TEST.BINANCE"), RawSymbol: ids.MustSymbol("TEST"),
		Underlying: currency.BTC(), QuoteCurrency: currency.USDT(),
		SettlementCurrency: currency.USDT(), PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.000001"),
	}
}

func cryptoFutureUSDC() currency.Currency {
	return currency.MustNew("USDC", 6, 0, "USD Coin", currency.Crypto)
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_future.rs:517
//	test: test_trait_accessors
func TestCryptoFutureTraitAccessors(t *testing.T) {
	future := btcusdtCryptoFuture(t)
	if future.InstrumentID() != ids.MustInstrumentID("ETHUSDT-123.BINANCE") ||
		future.AssetClass() != AssetClassCryptocurrency ||
		future.InstrumentClass() != InstrumentClassFuture ||
		!future.QuoteCurrency().Equal(currency.USDT()) ||
		!future.SettlementCurrency().Equal(currency.USDT()) || future.IsInverse() {
		t.Fatal("identity, class, or currency accessor differs")
	}
	if future.Contract.PricePrecision != 2 || future.Contract.SizePrecision != 6 ||
		future.ActivationNanos() == nil || future.ExpirationNanos() == nil {
		t.Fatal("precision or contract timestamp accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_future.rs:543
//	test: test_new_checked_price_precision_mismatch
func TestCryptoFutureNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCryptoFutureConfig()
	config.PricePrecision = 4
	if _, err := NewCheckedCryptoFuture(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_future.rs:580
//	test: test_new_checked_rejects_non_positive_sizing
func TestCryptoFutureNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	for _, test := range []struct {
		name       string
		multiplier *decimal.Quantity
		lotSize    *decimal.Quantity
	}{
		{name: "zero_multiplier", multiplier: pointer(decimal.MustQuantity("0"))},
		{name: "zero_lot_size", lotSize: pointer(decimal.MustQuantity("0"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validCryptoFutureConfig()
			config.Multiplier, config.LotSize = test.multiplier, test.lotSize
			_, err := NewCheckedCryptoFuture(config)
			if err == nil || !strings.Contains(err.Error(), "not positive") {
				t.Fatalf("error = %v, expected not positive", err)
			}
		})
	}
}

func pointer[T any](value T) *T { return &value }

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_future.rs:619
//	test: test_serialization_roundtrip
func TestCryptoFutureSerializationRoundtrip(t *testing.T) {
	original := btcusdtCryptoFuture(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored CryptoFuture
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed future:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_future.rs:626
//	test: test_builder_matches_new_checked
func TestCryptoFutureBuilderMatchesNewChecked(t *testing.T) {
	multiplier := decimal.MustQuantity("10")
	lotSize := decimal.MustQuantity("1")
	maxQuantity := decimal.MustQuantity("9000.0")
	minQuantity := decimal.MustQuantity("0.000001")
	maxNotional := money.MustNew("5000000.0", currency.USDT())
	minNotional := money.MustNew("10.0", currency.USDT())
	maxPrice := decimal.MustPrice("1000000.00")
	minPrice := decimal.MustPrice("0.01")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedCryptoFuture(CryptoFutureConfig{
		InstrumentID: ids.MustInstrumentID("ETHUSDT-123.BINANCE"),
		RawSymbol:    ids.MustSymbol("BTCUSDT"), Underlying: currency.BTC(),
		QuoteCurrency: currency.USDT(), SettlementCurrency: cryptoFutureUSDC(),
		Activation: 1, Expiration: 2, PricePrecision: 2, SizePrecision: 6,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.000001"),
		Multiplier: &multiplier, LotSize: &lotSize,
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee, TsEvent: 10, TsInit: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewCryptoFutureBuilder().
		Instrument(ids.MustInstrumentID("ETHUSDT-123.BINANCE")).
		Symbol(ids.MustSymbol("BTCUSDT")).
		Currencies(currency.BTC(), currency.USDT(), cryptoFutureUSDC()).
		IsInverse(false).
		ActiveBetween(1, 2).
		Precisions(2, 6).
		Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("0.000001")).
		WithMultiplier(decimal.MustQuantity("10")).
		WithLotSize(decimal.MustQuantity("1")).
		QuantityLimits(decimal.MustQuantity("9000.0"), decimal.MustQuantity("0.000001")).
		NotionalLimits(money.MustNew("5000000.0", currency.USDT()), money.MustNew("10.0", currency.USDT())).
		PriceLimits(decimal.MustPrice("1000000.00"), decimal.MustPrice("0.01")).
		Margins(decimal.MustParse("0.01"), decimal.MustParse("0.02")).
		Fees(decimal.MustParse("0.0002"), decimal.MustParse("0.0004")).
		Timestamps(10, 20).
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
