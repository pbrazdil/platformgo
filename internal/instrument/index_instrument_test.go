package instrument

import (
	"bytes"
	"encoding/json"
	"github.com/upcomers-org/platformgo/internal/currency"
	"github.com/upcomers-org/platformgo/internal/decimal"
	"github.com/upcomers-org/platformgo/internal/ids"
	"testing"
)

func spxIndex(t *testing.T) IndexInstrument {
	t.Helper()
	index, err := NewCheckedIndexInstrument(IndexInstrumentConfig{InstrumentID: ids.MustInstrumentID("SPX.INDEX"), RawSymbol: ids.MustSymbol("SPX"), Currency: currency.USD(), PricePrecision: 2, SizePrecision: 0, PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("1")})
	if err != nil {
		t.Fatal(err)
	}
	return index
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/index_instrument.rs:357
//	test: test_trait_accessors
func TestIndexInstrumentTraitAccessors(t *testing.T) {
	i := spxIndex(t)
	if i.InstrumentID != ids.MustInstrumentID("SPX.INDEX") || i.AssetClass() != AssetClassIndex || i.InstrumentClass() != InstrumentClassSpot || !i.QuoteCurrency().Equal(currency.USD()) || i.IsInverse() || i.PricePrecision != 2 || i.SizePrecision != 0 {
		t.Fatal("accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/index_instrument.rs:371
//	test: test_new_checked_price_precision_mismatch
func TestIndexInstrumentNewCheckedPricePrecisionMismatch(t *testing.T) {
	_, err := NewCheckedIndexInstrument(IndexInstrumentConfig{InstrumentID: ids.MustInstrumentID("SPX.INDEX"), RawSymbol: ids.MustSymbol("SPX"), Currency: currency.USD(), PricePrecision: 4, SizePrecision: 0, PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("1")})
	if err == nil {
		t.Fatal("expected mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/index_instrument.rs:389
//	test: test_serialization_roundtrip
func TestIndexInstrumentSerializationRoundtrip(t *testing.T) {
	original := spxIndex(t)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored IndexInstrument
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(restored)
	if !original.Equal(restored) || !bytes.Equal(data, again) {
		t.Fatal("roundtrip differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/index_instrument.rs:396
//	test: test_builder_matches_new_checked
func TestIndexInstrumentBuilderMatchesNewChecked(t *testing.T) {
	p, _ := NewCheckedIndexInstrument(IndexInstrumentConfig{InstrumentID: ids.MustInstrumentID("SPX.INDEX"), RawSymbol: ids.MustSymbol("SPX"), Currency: currency.USD(), PricePrecision: 2, SizePrecision: 0, PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("1"), TsEvent: 1, TsInit: 2})
	b, err := NewIndexInstrumentBuilder().Instrument(ids.MustInstrumentID("SPX.INDEX")).Symbol(ids.MustSymbol("SPX")).DenominatedIn(currency.USD()).Precisions(2, 0).Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("1")).Timestamps(1, 2).Build()
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := json.Marshal(p)
	bj, _ := json.Marshal(b)
	if !bytes.Equal(pj, bj) {
		t.Fatal("builder differs")
	}
}
