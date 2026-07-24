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

func btcDeribitCryptoFuturesSpread(t *testing.T) CryptoFuturesSpread {
	t.Helper()
	multiplier := decimal.MustQuantity("10")
	minQuantity := decimal.MustQuantity("1")
	makerFee := decimal.MustParse("0.0003")
	takerFee := decimal.MustParse("0.0003")
	spread, err := NewCheckedCryptoFuturesSpread(CryptoFuturesSpreadConfig{
		StrategyType: "FS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-FS-19MAY26_PERP.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-FS-19MAY26_PERP"),
			Underlying:   currency.BTC(), QuoteCurrency: currency.USD(),
			SettlementCurrency: currency.BTC(), Activation: 1_778_544_000_000_000_000,
			Expiration: 1_779_177_600_000_000_000, PricePrecision: 1, SizePrecision: 0,
			PriceIncrement: decimal.MustPrice("0.5"), SizeIncrement: decimal.MustQuantity("1"),
			Multiplier: &multiplier, MinQuantity: &minQuantity,
			MakerFee: &makerFee, TakerFee: &takerFee,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spread
}

func validCryptoFuturesSpreadConfig() CryptoFuturesSpreadConfig {
	return CryptoFuturesSpreadConfig{
		StrategyType: "FS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-FS-TEST.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-FS-TEST"), Underlying: currency.BTC(),
			QuoteCurrency: currency.USD(), SettlementCurrency: currency.BTC(),
			PricePrecision: 1, SizePrecision: 0,
			PriceIncrement: decimal.MustPrice("0.5"), SizeIncrement: decimal.MustQuantity("1"),
		},
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_futures_spread.rs:535
//	test: test_trait_accessors
func TestCryptoFuturesSpreadTraitAccessors(t *testing.T) {
	spread := btcDeribitCryptoFuturesSpread(t)
	if spread.InstrumentID() != ids.MustInstrumentID("BTC-FS-19MAY26_PERP.DERIBIT") ||
		spread.AssetClass() != AssetClassCryptocurrency ||
		spread.InstrumentClass() != InstrumentClassFuturesSpread ||
		!spread.QuoteCurrency().Equal(currency.USD()) ||
		!spread.SettlementCurrency().Equal(currency.BTC()) || spread.IsInverse() {
		t.Fatal("identity, class, or currency accessor differs")
	}
	if spread.Future.Contract.PricePrecision != 1 ||
		spread.Future.Contract.SizePrecision != 0 ||
		!spread.Future.Contract.SizeIncrement.Equal(decimal.MustQuantity("1")) ||
		spread.ActivationNanos() == nil || spread.ExpirationNanos() == nil {
		t.Fatal("precision, sizing, or timestamp accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_futures_spread.rs:568
//	test: test_new_checked_price_precision_mismatch
func TestCryptoFuturesSpreadNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCryptoFuturesSpreadConfig()
	config.Future.PricePrecision = 4
	if _, err := NewCheckedCryptoFuturesSpread(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_futures_spread.rs:606
//	test: test_new_checked_rejects_non_positive_sizing
func TestCryptoFuturesSpreadNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	for _, test := range []struct {
		name       string
		multiplier *decimal.Quantity
		lotSize    *decimal.Quantity
	}{
		{name: "zero_multiplier", multiplier: pointer(decimal.MustQuantity("0"))},
		{name: "zero_lot_size", lotSize: pointer(decimal.MustQuantity("0"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validCryptoFuturesSpreadConfig()
			config.Future.Multiplier, config.Future.LotSize = test.multiplier, test.lotSize
			if _, err := NewCheckedCryptoFuturesSpread(config); err == nil {
				t.Fatal("expected non-positive sizing error")
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_futures_spread.rs:615
//	test: test_serialization_roundtrip
func TestCryptoFuturesSpreadSerializationRoundtrip(t *testing.T) {
	original := btcDeribitCryptoFuturesSpread(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored CryptoFuturesSpread
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	restoredData, err := json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !original.Equal(restored) || !bytes.Equal(data, restoredData) {
		t.Fatalf("round-trip changed spread:\n%s\n%s", data, restoredData)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_futures_spread.rs:622
//	test: test_builder_matches_new_checked
func TestCryptoFuturesSpreadBuilderMatchesNewChecked(t *testing.T) {
	multiplier := decimal.MustQuantity("10")
	lotSize := decimal.MustQuantity("1")
	maxQuantity := decimal.MustQuantity("100")
	minQuantity := decimal.MustQuantity("1")
	maxNotional := money.MustNew("5000000.0", currency.USD())
	minNotional := money.MustNew("10.0", currency.USD())
	maxPrice := decimal.MustPrice("1000000.0")
	minPrice := decimal.MustPrice("0.5")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedCryptoFuturesSpread(CryptoFuturesSpreadConfig{
		StrategyType: "FS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-FS-19MAY26_PERP.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-FS-19MAY26_PERP"),
			Underlying:   currency.BTC(), QuoteCurrency: currency.USD(),
			SettlementCurrency: cryptoFutureUSDC(), Activation: 1, Expiration: 2,
			PricePrecision: 1, SizePrecision: 0,
			PriceIncrement: decimal.MustPrice("0.5"), SizeIncrement: decimal.MustQuantity("1"),
			Multiplier: &multiplier, LotSize: &lotSize,
			MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
			MaxNotional: &maxNotional, MinNotional: &minNotional,
			MaxPrice: &maxPrice, MinPrice: &minPrice,
			MarginInit: &marginInit, MarginMaint: &marginMaint,
			MakerFee: &makerFee, TakerFee: &takerFee, TsEvent: 10, TsInit: 20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewCryptoFuturesSpreadBuilder().
		Instrument(ids.MustInstrumentID("BTC-FS-19MAY26_PERP.DERIBIT")).
		Symbol(ids.MustSymbol("BTC-FS-19MAY26_PERP")).
		Currencies(currency.BTC(), currency.USD(), cryptoFutureUSDC()).
		IsInverse(false).
		WithStrategy("FS").
		ActiveBetween(1, 2).
		Precisions(1, 0).
		Increments(decimal.MustPrice("0.5"), decimal.MustQuantity("1")).
		WithMultiplier(decimal.MustQuantity("10")).
		WithLotSize(decimal.MustQuantity("1")).
		QuantityLimits(decimal.MustQuantity("100"), decimal.MustQuantity("1")).
		NotionalLimits(money.MustNew("5000000.0", currency.USD()), money.MustNew("10.0", currency.USD())).
		PriceLimits(decimal.MustPrice("1000000.0"), decimal.MustPrice("0.5")).
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
