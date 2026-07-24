package instrument

import (
	"bytes"
	"encoding/json"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"github.com/upcomers-org/platformgo/internal/money"
	"testing"
)

func binaryOptionFixture(t *testing.T) BinaryOption {
	t.Helper()
	o, e := NewCheckedBinaryOption(BinaryOptionConfig{InstrumentID: ids.MustInstrumentID("TEST.POLYMARKET"), RawSymbol: ids.MustSymbol("TEST"), AssetClass: AssetClassAlternative, Currency: cryptoFutureUSDC(), Activation: 1, Expiration: 2, PricePrecision: 3, SizePrecision: 2, PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("0.01")})
	if e != nil {
		t.Fatal(e)
	}
	return o
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/binary_option.rs:493
//	test: test_trait_accessors
func TestBinaryOptionTraitAccessors(t *testing.T) {
	o := binaryOptionFixture(t)
	if o.AssetClass() != AssetClassAlternative || o.InstrumentClass() != InstrumentClassBinaryOption || !o.QuoteCurrency().Equal(cryptoFutureUSDC()) || o.IsInverse() || o.Future.Contract.PricePrecision != 3 || o.Future.Contract.SizePrecision != 2 || o.ActivationNanos() == nil || o.ExpirationNanos() == nil {
		t.Fatal("accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/binary_option.rs:508
//	test: test_new_checked_price_precision_mismatch
func TestBinaryOptionNewCheckedPricePrecisionMismatch(t *testing.T) {
	_, e := NewCheckedBinaryOption(BinaryOptionConfig{InstrumentID: ids.MustInstrumentID("TEST.POLYMARKET"), RawSymbol: ids.MustSymbol("TEST"), AssetClass: AssetClassAlternative, Currency: cryptoFutureUSDC(), PricePrecision: 4, SizePrecision: 2, PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("0.01")})
	if e == nil {
		t.Fatal("expected mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/binary_option.rs:541
//	test: test_serialization_roundtrip
func TestBinaryOptionSerializationRoundtrip(t *testing.T) {
	o := binaryOptionFixture(t)
	d, e := json.Marshal(o)
	if e != nil {
		t.Fatal(e)
	}
	var r BinaryOption
	if e = json.Unmarshal(d, &r); e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(r)
	if !o.Equal(r) || !bytes.Equal(d, a) {
		t.Fatal("roundtrip differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/binary_option.rs:548
//	test: test_builder_matches_new_checked
func TestBinaryOptionBuilderMatchesNewChecked(t *testing.T) {
	outcome, desc := "Yes", "Will it happen?"
	maxQ, minQ := decimal.MustQuantity("10000.00"), decimal.MustQuantity("5.00")
	maxN, minN := money.MustNew("100000", cryptoFutureUSDC()), money.MustNew("10", cryptoFutureUSDC())
	maxP, minP := decimal.MustPrice("0.999"), decimal.MustPrice("0.001")
	mi, mm, mf, tf := decimal.MustParse("0.01"), decimal.MustParse("0.02"), decimal.MustParse("0.0002"), decimal.MustParse("0.0004")
	p, e := NewCheckedBinaryOption(BinaryOptionConfig{InstrumentID: ids.MustInstrumentID("TEST.POLYMARKET"), RawSymbol: ids.MustSymbol("TEST"), AssetClass: AssetClassAlternative, Currency: cryptoFutureUSDC(), Activation: 1, Expiration: 2, PricePrecision: 3, SizePrecision: 2, PriceIncrement: decimal.MustPrice("0.001"), SizeIncrement: decimal.MustQuantity("0.01"), Outcome: &outcome, Description: &desc, MaxQuantity: &maxQ, MinQuantity: &minQ, MaxNotional: &maxN, MinNotional: &minN, MaxPrice: &maxP, MinPrice: &minP, MarginInit: &mi, MarginMaint: &mm, MakerFee: &mf, TakerFee: &tf, TsEvent: 3, TsInit: 4})
	if e != nil {
		t.Fatal(e)
	}
	b, e := NewBinaryOptionBuilder().Instrument(ids.MustInstrumentID("TEST.POLYMARKET")).Symbol(ids.MustSymbol("TEST")).Class(AssetClassAlternative).DenominatedIn(cryptoFutureUSDC()).ActiveBetween(1, 2).Precisions(3, 2).Increments(decimal.MustPrice("0.001"), decimal.MustQuantity("0.01")).WithOutcome("Yes").WithDescription("Will it happen?").QuantityLimits(decimal.MustQuantity("10000.00"), decimal.MustQuantity("5.00")).NotionalLimits(money.MustNew("100000", cryptoFutureUSDC()), money.MustNew("10", cryptoFutureUSDC())).PriceLimits(decimal.MustPrice("0.999"), decimal.MustPrice("0.001")).Margins(mi, mm).Fees(mf, tf).Timestamps(3, 4).Build()
	if e != nil {
		t.Fatal(e)
	}
	pj, _ := json.Marshal(p)
	bj, _ := json.Marshal(b)
	if !bytes.Equal(pj, bj) {
		t.Fatal("builder differs")
	}
}
