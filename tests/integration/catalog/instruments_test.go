package catalog

import (
	"slices"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:52
//	test: catalog_upsert_and_list
func TestCatalogUpsertAndList(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.seedInstrument("ETH-PERP", "ETH"); err != nil {
		t.Fatal(err)
	}

	symbols := fixture.symbols()
	if !slices.Contains(symbols, "BTC-PERP") || !slices.Contains(symbols, "ETH-PERP") {
		t.Fatalf("symbols = %v", symbols)
	}
	btc, ok := fixture.bySymbol("BTC-PERP")
	if !ok {
		t.Fatal("BTC perp is absent")
	}
	if btc.Symbol != "BTC-PERP" || btc.BaseAsset != "BTC" || btc.QuoteAsset != "USDC" ||
		btc.MaxLeverage != 50 || btc.PriceIncrement != "0.1" ||
		btc.Provenance != "manual" || btc.ProductClass != "perps" {
		t.Fatalf("BTC perp = %#v", btc)
	}

	if err := fixture.upsert(solPerp()); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.bySymbol("SOL-PERP"); !ok {
		t.Fatal("new perp was not added")
	}
	sol := solPerp()
	sol.MaxLeverage = 10
	if err := fixture.upsert(sol); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range fixture.list() {
		if row.Symbol == "SOL-PERP" {
			count++
			if row.MaxLeverage != 10 {
				t.Fatalf("SOL leverage = %d, want 10", row.MaxLeverage)
			}
		}
	}
	if count != 1 {
		t.Fatalf("SOL row count = %d, want 1", count)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:105
//	test: asset_class_is_patchable_as_typed_enum
func TestAssetClassIsPatchableAsTypedEnum(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.upsert(solPerp()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.patchAssetClass("SOL-PERP", "forex"); err != nil {
		t.Fatal(err)
	}
	sol, _ := fixture.bySymbol("SOL-PERP")
	if sol.AssetClass != "forex" {
		t.Fatalf("asset class = %q, want forex", sol.AssetClass)
	}
	if sol.ProductClass == "perps" {
		t.Fatal("product class was not re-derived from the new asset class")
	}
	if sol.MarginInit != "0.05" {
		t.Fatalf("margin init = %q, want 0.05", sol.MarginInit)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:142
//	test: dotted_symbol_is_rejected
func TestDottedSymbolIsRejected(t *testing.T) {
	fixture := newInstrumentFixture()
	bad := solPerp()
	bad.Symbol = "BAD.SYM"
	if err := fixture.upsert(bad); err == nil {
		t.Fatal("a symbol containing '.' was accepted")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:155
//	test: public_summary_exposes_position_cap_and_margin_init
func TestPublicSummaryExposesPositionCapAndMarginInit(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.upsert(solPerp()); err != nil {
		t.Fatal(err)
	}
	sol, _ := fixture.bySymbol("SOL-PERP")
	if sol.MarginInit != "0.05" || sol.PositionCap != "50000" ||
		sol.TradingMode != "full" || !sol.CanOpen {
		t.Fatalf("public SOL summary = %#v", sol)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:184
//	test: public_summary_exposes_full_catalog_economics
func TestPublicSummaryExposesFullCatalogEconomics(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.upsert(solPerp()); err != nil {
		t.Fatal(err)
	}
	sol, _ := fixture.bySymbol("SOL-PERP")
	if sol.MarginMaint != "0.025" || sol.AssetClass != "crypto" ||
		sol.MinNotional != "10" || sol.MaxNotional != "10000000" ||
		sol.LotSize != "1" || sol.Multiplier != "1" || sol.ProductClass != "perps" {
		t.Fatalf("public SOL economics = %#v", sol)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:217
//	test: leverage_limits_overrides_are_real_and_default_is_unsourced
func TestLeverageLimitsOverridesAreRealAndDefaultIsUnsourced(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.upsert(solPerp()); err != nil {
		t.Fatal(err)
	}
	limits := fixture.limits()
	if limits.DefaultMax != nil {
		t.Fatalf("default max = %d, want nil", *limits.DefaultMax)
	}
	if limits.Overrides["BTC-PERP"] != 50 || limits.Overrides["SOL-PERP"] != 20 {
		t.Fatalf("overrides = %#v", limits.Overrides)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:244
//	test: import_applies_uniform_default_fee_schedule
func TestImportAppliesUniformDefaultFeeSchedule(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.importHyperliquid([]string{"BTC"}); err != nil {
		t.Fatal(err)
	}
	btc, ok := fixture.bySymbol("BTC-PERP")
	if !ok {
		t.Fatal("imported BTC perp is absent")
	}
	if !decimal.MustParse(btc.MakerFee).Equal(decimal.MustParse("0.00015")) {
		t.Fatalf("maker fee = %q", btc.MakerFee)
	}
	if !decimal.MustParse(btc.TakerFee).Equal(decimal.MustParse("0.00045")) {
		t.Fatalf("taker fee = %q", btc.TakerFee)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_instruments.rs:284
//	test: catalog_cache_serves_within_ttl_then_refreshes
func TestCatalogCacheServesWithinTTLThenRefreshes(t *testing.T) {
	fixture := newInstrumentFixture()
	if err := fixture.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	const ttlMillis uint64 = 300
	first, ok := fixture.getSymbolCached("BTC-PERP", ttlMillis)
	if !ok {
		t.Fatal("seeded symbol is absent")
	}

	updatedLeverage := first.MaxLeverage + 7
	fixture.updateLeverageDirect("BTC-PERP", updatedLeverage)
	direct, _ := fixture.getSymbolCached("BTC-PERP", 0)
	if direct.MaxLeverage != updatedLeverage {
		t.Fatalf("direct leverage = %d, want %d", direct.MaxLeverage, updatedLeverage)
	}
	cached, _ := fixture.getSymbolCached("BTC-PERP", ttlMillis)
	if cached.MaxLeverage != first.MaxLeverage {
		t.Fatalf("cached leverage = %d, want %d", cached.MaxLeverage, first.MaxLeverage)
	}

	fixture.advance(ttlMillis + 150)
	refreshed, _ := fixture.getSymbolCached("BTC-PERP", ttlMillis)
	if refreshed.MaxLeverage != updatedLeverage {
		t.Fatalf("refreshed leverage = %d, want %d", refreshed.MaxLeverage, updatedLeverage)
	}
}
