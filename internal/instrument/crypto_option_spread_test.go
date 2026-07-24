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

func btcDeribitCryptoOptionSpread(t *testing.T) CryptoOptionSpread {
	t.Helper()
	multiplier := decimal.MustQuantity("1")
	minQuantity := decimal.MustQuantity("0.1")
	makerFee := decimal.MustParse("0.0003")
	takerFee := decimal.MustParse("0.0003")
	spread, err := NewCheckedCryptoOptionSpread(CryptoOptionSpreadConfig{
		StrategyType: "CS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-CS-19MAY26-70000_75000.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-CS-19MAY26-70000_75000"),
			Underlying:   currency.BTC(), QuoteCurrency: currency.USD(),
			SettlementCurrency: currency.BTC(), Activation: 1_778_544_000_000_000_000,
			Expiration: 1_779_177_600_000_000_000, PricePrecision: 4, SizePrecision: 1,
			PriceIncrement: decimal.MustPrice("0.0001"), SizeIncrement: decimal.MustQuantity("0.1"),
			Multiplier: &multiplier, MinQuantity: &minQuantity,
			MakerFee: &makerFee, TakerFee: &takerFee,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return spread
}

func validCryptoOptionSpreadConfig() CryptoOptionSpreadConfig {
	return CryptoOptionSpreadConfig{
		StrategyType: "CS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-CS-TEST.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-CS-TEST"), Underlying: currency.BTC(),
			QuoteCurrency: currency.USD(), SettlementCurrency: currency.BTC(),
			PricePrecision: 4, SizePrecision: 1,
			PriceIncrement: decimal.MustPrice("0.0001"), SizeIncrement: decimal.MustQuantity("0.1"),
		},
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option_spread.rs:534
//	test: test_trait_accessors
func TestCryptoOptionSpreadTraitAccessors(t *testing.T) {
	spread := btcDeribitCryptoOptionSpread(t)
	if spread.InstrumentID() != ids.MustInstrumentID("BTC-CS-19MAY26-70000_75000.DERIBIT") ||
		spread.AssetClass() != AssetClassCryptocurrency ||
		spread.InstrumentClass() != InstrumentClassOptionSpread ||
		!spread.QuoteCurrency().Equal(currency.USD()) ||
		!spread.SettlementCurrency().Equal(currency.BTC()) || spread.IsInverse() {
		t.Fatal("identity, class, or currency accessor differs")
	}
	contract := spread.Spread.Future.Contract
	if contract.PricePrecision != 4 || contract.SizePrecision != 1 ||
		!contract.SizeIncrement.Equal(decimal.MustQuantity("0.1")) ||
		spread.ActivationNanos() == nil || spread.ExpirationNanos() == nil {
		t.Fatal("precision, sizing, or timestamp accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option_spread.rs:567
//	test: test_new_checked_price_precision_mismatch
func TestCryptoOptionSpreadNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := validCryptoOptionSpreadConfig()
	config.Future.PriceIncrement = decimal.MustPrice("0.001")
	if _, err := NewCheckedCryptoOptionSpread(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option_spread.rs:605
//	test: test_new_checked_rejects_non_positive_sizing
func TestCryptoOptionSpreadNewCheckedRejectsNonPositiveSizing(t *testing.T) {
	for _, test := range []struct {
		name       string
		multiplier *decimal.Quantity
		lotSize    *decimal.Quantity
	}{
		{name: "zero_multiplier", multiplier: pointer(decimal.MustQuantity("0"))},
		{name: "zero_lot_size", lotSize: pointer(decimal.MustQuantity("0"))},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := validCryptoOptionSpreadConfig()
			config.Future.Multiplier, config.Future.LotSize = test.multiplier, test.lotSize
			if _, err := NewCheckedCryptoOptionSpread(config); err == nil {
				t.Fatal("expected non-positive sizing error")
			}
		})
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/crypto_option_spread.rs:614
//	test: test_serialization_roundtrip
func TestCryptoOptionSpreadSerializationRoundtrip(t *testing.T) {
	original := btcDeribitCryptoOptionSpread(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored CryptoOptionSpread
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
//	source: crates/model/src/instruments/crypto_option_spread.rs:621
//	test: test_builder_matches_new_checked
func TestCryptoOptionSpreadBuilderMatchesNewChecked(t *testing.T) {
	multiplier := decimal.MustQuantity("10")
	lotSize := decimal.MustQuantity("5")
	maxQuantity := decimal.MustQuantity("1000.0")
	minQuantity := decimal.MustQuantity("0.1")
	maxNotional := money.MustNew("1000000.0", cryptoFutureUSDC())
	minNotional := money.MustNew("10.0", cryptoFutureUSDC())
	maxPrice := decimal.MustPrice("9.9999")
	minPrice := decimal.MustPrice("0.0001")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedCryptoOptionSpread(CryptoOptionSpreadConfig{
		StrategyType: "CS",
		Future: CryptoFutureConfig{
			InstrumentID: ids.MustInstrumentID("BTC-CS-19MAY26-70000_75000.DERIBIT"),
			RawSymbol:    ids.MustSymbol("BTC-CS-19MAY26-70000_75000"),
			Underlying:   currency.BTC(), QuoteCurrency: cryptoFutureUSDC(),
			SettlementCurrency: currency.USDT(), Activation: 1, Expiration: 2,
			PricePrecision: 4, SizePrecision: 1,
			PriceIncrement: decimal.MustPrice("0.0001"), SizeIncrement: decimal.MustQuantity("0.1"),
			Multiplier: &multiplier, LotSize: &lotSize,
			MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
			MaxNotional: &maxNotional, MinNotional: &minNotional,
			MaxPrice: &maxPrice, MinPrice: &minPrice,
			MarginInit: &marginInit, MarginMaint: &marginMaint,
			MakerFee: &makerFee, TakerFee: &takerFee, TsEvent: 1, TsInit: 2,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	built, err := NewCryptoOptionSpreadBuilder().
		Instrument(ids.MustInstrumentID("BTC-CS-19MAY26-70000_75000.DERIBIT")).
		Symbol(ids.MustSymbol("BTC-CS-19MAY26-70000_75000")).
		Currencies(currency.BTC(), cryptoFutureUSDC(), currency.USDT()).
		IsInverse(false).
		WithStrategy("CS").
		ActiveBetween(1, 2).
		Precisions(4, 1).
		Increments(decimal.MustPrice("0.0001"), decimal.MustQuantity("0.1")).
		WithMultiplier(decimal.MustQuantity("10")).
		WithLotSize(decimal.MustQuantity("5")).
		QuantityLimits(decimal.MustQuantity("1000.0"), decimal.MustQuantity("0.1")).
		NotionalLimits(money.MustNew("1000000.0", cryptoFutureUSDC()), money.MustNew("10.0", cryptoFutureUSDC())).
		PriceLimits(decimal.MustPrice("9.9999"), decimal.MustPrice("0.0001")).
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
