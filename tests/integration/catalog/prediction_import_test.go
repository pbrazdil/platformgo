package catalog

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_import.rs:172
//	test: import_categorical_market_materializes_parent_and_legs
func TestImportCategoricalMarketMaterializesParentAndLegs(t *testing.T) {
	fixture := newPredictionFixture()
	result := fixture.importCategorical(true)
	if result.Markets != 1 || result.Inserted != len(predictionCandidates()) || result.Skipped != 0 {
		t.Fatalf("first import = %#v", result)
	}
	market, ok := fixture.getMarket(predictionVenue, predictionMarketKey)
	if !ok || market.Question != predictionMarketQuestion ||
		!market.MutuallyExclusive || market.Status != "open" {
		t.Fatalf("market = %#v, found=%v", market, ok)
	}
	event, ok := fixture.getEvent(predictionVenue, predictionEventKey)
	if !ok || market.EventID == nil || *market.EventID != event.ID {
		t.Fatalf("event=%#v market=%#v found=%v", event, market, ok)
	}
	for index, symbol := range predictionLegSymbols() {
		leg, ok := fixture.getLeg(symbol)
		if !ok || leg.MarketID != market.ID || leg.OutcomeIndex != index ||
			leg.OutcomeLabel != predictionCandidates()[index] ||
			leg.ProductType != "binary_option" || leg.AssetClass != "prediction" || !leg.Enabled {
			t.Fatalf("leg %s = %#v, found=%v", symbol, leg, ok)
		}
	}
	if _, ok := fixture.getMarket(predictionVenue, predictionBinaryKey); ok {
		t.Fatal("unpicked binary market was imported")
	}

	again := fixture.importCategorical(true)
	if again.Markets != 1 || again.Inserted != 0 || again.Updated != len(predictionCandidates()) {
		t.Fatalf("re-import = %#v", again)
	}
	marketAgain, _ := fixture.getMarket(predictionVenue, predictionMarketKey)
	if marketAgain.ID != market.ID {
		t.Fatalf("market ID changed from %q to %q", market.ID, marketAgain.ID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_prediction_import.rs:291
//	test: discover_paginates_and_filters
func TestDiscoverPaginatesAndFilters(t *testing.T) {
	fixture := newPredictionFixture()
	page1 := fixture.discover("", "")
	if len(page1.Items) != 1 || page1.Items[0].SelectKey != predictionMarketKey ||
		len(page1.Items[0].Legs) != len(predictionCandidates()) ||
		page1.Items[0].Imported || page1.NextCursor == nil {
		t.Fatalf("page 1 = %#v", page1)
	}
	page2 := fixture.discover(*page1.NextCursor, "")
	if len(page2.Items) != 1 || page2.Items[0].SelectKey != predictionBinaryKey ||
		page2.NextCursor != nil {
		t.Fatalf("page 2 = %#v", page2)
	}
	filtered := fixture.discover("", predictionEventKey)
	if len(filtered.Items) != 1 || filtered.Items[0].SelectKey != predictionMarketKey ||
		filtered.NextCursor != nil {
		t.Fatalf("filtered page = %#v", filtered)
	}
}
