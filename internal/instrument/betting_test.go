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

const bettingInstrumentSourceRevision = "116c9b5159ebeb6b578b737d72298cac8d723723"

func bettingGBP() currency.Currency {
	return currency.MustNew("GBP", 2, 826, "Pound sterling", currency.Fiat)
}

func bettingInstrumentFixture(t *testing.T) BettingInstrument {
	t.Helper()
	maxQuantity := decimal.MustQuantity("1000")
	minQuantity := decimal.MustQuantity("1")
	maxNotional := money.MustNew("10000", bettingGBP())
	minNotional := money.MustNew("10", bettingGBP())
	maxPrice := decimal.MustPrice("100.00")
	minPrice := decimal.MustPrice("1.00")
	one := decimal.MustParse("1")
	zero := decimal.MustParse("0")
	instrument, err := NewCheckedBettingInstrument(BettingInstrumentConfig{
		InstrumentID: ids.MustInstrumentID("1-123456789.BETFAIR"),
		RawSymbol:    ids.MustSymbol("1-123456789"),
		EventTypeID:  6423, EventTypeName: "American Football",
		CompetitionID: 12_282_733, CompetitionName: "NFL",
		EventID: 29_678_534, EventName: "NFL", EventCountryCode: "GB",
		EventOpenDate: 1_644_276_600_000_000_000,
		BettingType:   "ODDS", MarketID: "1-123456789",
		MarketName: "AFC Conference Winner", MarketType: "SPECIAL",
		MarketStartTime: 1_644_276_600_000_000_000,
		SelectionID:     50214, SelectionName: "Kansas City Chiefs",
		SelectionHandicap: 0,
		Currency:          bettingGBP(),
		PricePrecision:    2, SizePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("0.01"),
		MaxQuantity:    &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &one, MarginMaint: &one, MakerFee: &zero, TakerFee: &zero,
	})
	if err != nil {
		t.Fatal(err)
	}
	return instrument
}

func requireBettingPrice(t *testing.T, got *decimal.Price, want string) {
	t.Helper()
	if got == nil || !got.Equal(decimal.MustPrice(want)) {
		t.Fatalf("price = %v, want %s", got, want)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:600
//	test: test_trait_accessors
func TestBettingInstrumentTraitAccessors(t *testing.T) {
	betting := bettingInstrumentFixture(t)
	if betting.AssetClass() != AssetClassAlternative ||
		betting.InstrumentClass() != InstrumentClassSportsBetting ||
		!betting.Currency.Equal(bettingGBP()) || betting.IsInverse() {
		t.Fatal("class, currency, or inverse accessor differs")
	}
	if betting.PricePrecision != 2 || betting.SizePrecision != 2 ||
		!betting.PriceIncrement.Equal(decimal.MustPrice("0.01")) ||
		!betting.SizeIncrement.Equal(decimal.MustQuantity("0.01")) ||
		!betting.MarginInit.Equal(decimal.MustParse("1")) ||
		!betting.MarginMaint.Equal(decimal.MustParse("1")) {
		t.Fatal("precision, increment, or margin accessor differs")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:614
//	test: test_new_checked_price_precision_mismatch
func TestBettingInstrumentNewCheckedPricePrecisionMismatch(t *testing.T) {
	config := BettingInstrumentConfig{
		InstrumentID: ids.MustInstrumentID("1-123.BETFAIR"),
		RawSymbol:    ids.MustSymbol("1-123"),
		EventTypeID:  6423, EventTypeName: "Football",
		CompetitionID: 1, CompetitionName: "NFL",
		EventID: 1, EventName: "NFL", EventCountryCode: "GB",
		BettingType: "ODDS", MarketID: "1-123", MarketName: "Winner", MarketType: "SPECIAL",
		SelectionID: 50214, SelectionName: "Team",
		Currency: bettingGBP(), PricePrecision: 4, SizePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"),
		SizeIncrement:  decimal.MustQuantity("0.01"),
	}
	if _, err := NewCheckedBettingInstrument(config); err == nil {
		t.Fatal("expected price precision mismatch")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:658
//	test: test_serialization_roundtrip
func TestBettingInstrumentSerializationRoundtrip(t *testing.T) {
	betting := bettingInstrumentFixture(t)
	data, err := json.Marshal(betting)
	if err != nil {
		t.Fatal(err)
	}
	var deserialized BettingInstrument
	if err := json.Unmarshal(data, &deserialized); err != nil {
		t.Fatal(err)
	}
	if !betting.Equal(deserialized) {
		t.Fatal("round-trip changed betting instrument identity")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:665
//	test: test_betfair_tick_scheme_navigation
func TestBettingInstrumentBetfairTickSchemeNavigation(t *testing.T) {
	betting := bettingInstrumentFixture(t)
	betting.MaxPrice, betting.MinPrice = nil, nil

	requireBettingPrice(t, betting.EffectiveMinPrice(), "1.01")
	requireBettingPrice(t, betting.EffectiveMaxPrice(), "1000.00")
	ask, askOK := betting.NextAskPrice(4.0, 1)
	bid, bidOK := betting.NextBidPrice(2.027, 2)
	if !askOK || !ask.Equal(decimal.MustPrice("4.10")) {
		t.Fatalf("next ask = %v, %v", ask, askOK)
	}
	if !bidOK || !bid.Equal(decimal.MustPrice("1.99")) {
		t.Fatalf("next bid = %v, %v", bid, bidOK)
	}
	if got := len(betting.NextBidPrices(1.102, 20)); got != 10 {
		t.Fatalf("next bid price count = %d, want 10", got)
	}
	if got := len(betting.NextAskPrices(1.102, 20)); got != 20 {
		t.Fatalf("next ask price count = %d, want 20", got)
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:678
//	test: test_non_betfair_venue_no_tick_scheme
func TestBettingInstrumentNonBetfairVenueHasNoTickScheme(t *testing.T) {
	betting := bettingInstrumentFixture(t)
	betting.InstrumentID = ids.MustInstrumentID("1-123456789.SMARKETS")
	betting.MaxPrice, betting.MinPrice = nil, nil

	if betting.EffectiveTickScheme() != nil ||
		betting.EffectiveMinPrice() != nil ||
		betting.EffectiveMaxPrice() != nil {
		t.Fatal("non-Betfair instrument received an implicit tick scheme or price limit")
	}
}

// Ported from:
//
//	NautilusTrader: 116c9b5159ebeb6b578b737d72298cac8d723723
//	source: crates/model/src/instruments/betting.rs:689
//	test: test_builder_matches_new_checked
func TestBettingInstrumentBuilderMatchesNewChecked(t *testing.T) {
	maxQuantity := decimal.MustQuantity("1000")
	minQuantity := decimal.MustQuantity("1")
	maxNotional := money.MustNew("10000", bettingGBP())
	minNotional := money.MustNew("10", bettingGBP())
	maxPrice := decimal.MustPrice("100.00")
	minPrice := decimal.MustPrice("1.00")
	marginInit := decimal.MustParse("0.01")
	marginMaint := decimal.MustParse("0.02")
	makerFee := decimal.MustParse("0.0002")
	takerFee := decimal.MustParse("0.0004")
	positional, err := NewCheckedBettingInstrument(BettingInstrumentConfig{
		InstrumentID: ids.MustInstrumentID("1-123456789.BETFAIR"),
		RawSymbol:    ids.MustSymbol("1-123456789"),
		EventTypeID:  6423, EventTypeName: "American Football",
		CompetitionID: 12_282_733, CompetitionName: "NFL",
		EventID: 29_678_534, EventName: "NFL", EventCountryCode: "GB", EventOpenDate: 1,
		BettingType: "ODDS", MarketID: "1-123456789",
		MarketName: "AFC Conference Winner", MarketType: "SPECIAL", MarketStartTime: 2,
		SelectionID: 50214, SelectionName: "Kansas City Chiefs",
		Currency: bettingGBP(), PricePrecision: 2, SizePrecision: 2,
		PriceIncrement: decimal.MustPrice("0.01"), SizeIncrement: decimal.MustQuantity("0.01"),
		MaxQuantity: &maxQuantity, MinQuantity: &minQuantity,
		MaxNotional: &maxNotional, MinNotional: &minNotional,
		MaxPrice: &maxPrice, MinPrice: &minPrice,
		MarginInit: &marginInit, MarginMaint: &marginMaint,
		MakerFee: &makerFee, TakerFee: &takerFee,
		TsEvent: 3, TsInit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}

	built, err := NewBettingInstrumentBuilder().
		Instrument(ids.MustInstrumentID("1-123456789.BETFAIR")).
		Symbol(ids.MustSymbol("1-123456789")).
		EventType(6423, "American Football").
		Competition(12_282_733, "NFL").
		Event(29_678_534, "NFL", "GB", 1).
		Market("ODDS", "1-123456789", "AFC Conference Winner", "SPECIAL", 2).
		Selection(50214, "Kansas City Chiefs", 0).
		Denomination(bettingGBP()).
		Precisions(2, 2).
		Increments(decimal.MustPrice("0.01"), decimal.MustQuantity("0.01")).
		QuantityLimits(maxQuantity, minQuantity).
		NotionalLimits(maxNotional, minNotional).
		PriceLimits(maxPrice, minPrice).
		Margins(marginInit, marginMaint).
		Fees(makerFee, takerFee).
		Timestamps(3, 4).
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
