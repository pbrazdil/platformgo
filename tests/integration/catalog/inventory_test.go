package catalog

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_inventory.rs:253
//	test: admin_inventory_consolidates_catalog_stats_and_venue_facts
func TestAdminInventoryConsolidatesCatalogStatsAndVenueFacts(t *testing.T) {
	fixture := newInventoryFixture()
	if status, _ := fixture.query(false, "", ""); status != statusForbidden {
		t.Fatalf("viewer inventory status = %d, want 403", status)
	}
	status, body := fixture.query(true, "", "")
	if status != statusOK {
		t.Fatalf("operator inventory status = %d", status)
	}
	bySymbol := make(map[string]inventoryRow)
	for _, row := range body.Rows {
		bySymbol[row.Symbol] = row
	}
	btc := bySymbol["BTC-PERP"]
	if btc.VenueKind != "dex" || btc.ProductClass != "perps" ||
		btc.MarketDataStatus != "fresh" || btc.VendorMeta == nil ||
		btc.VendorMeta.Venue != "hyperliquid" || !btc.VendorMeta.IsHIP3 {
		t.Fatalf("BTC inventory row = %#v", btc)
	}
	eth := bySymbol["ETH-PERP"]
	if eth.MarketDataStatus != "fresh" || eth.VendorMeta != nil {
		t.Fatalf("ETH inventory row = %#v", eth)
	}
	stale := bySymbol[predictionBinaryLegSymbols()[1]]
	if stale.MarketDataStatus != "stale" {
		t.Fatalf("stale prediction row = %#v", stale)
	}
	closed := bySymbol[predictionBinaryLegSymbols()[0]]
	if closed.MarketDataStatus != "closed" || closed.VenueKind != "dex" ||
		closed.ProductClass != "predictions" {
		t.Fatalf("closed prediction row = %#v", closed)
	}
	counts := body.Counts
	if counts.Total != uint64(len(body.Rows)) ||
		counts.Total != counts.Live+counts.Stale+counts.Closed ||
		counts.Total != counts.Enabled+counts.Disabled ||
		counts.Live < 1 || counts.Stale < 1 || counts.Closed < 1 {
		t.Fatalf("inventory counts = %#v rows=%d", counts, len(body.Rows))
	}
	_, dex := fixture.query(true, "dex", "")
	if dex.Counts.Total != uint64(len(body.Rows)) {
		t.Fatalf("DEX count = %d, want %d", dex.Counts.Total, len(body.Rows))
	}
	_, cex := fixture.query(true, "cex", "")
	if cex.Counts.Total != 0 {
		t.Fatalf("CEX count = %d, want 0", cex.Counts.Total)
	}
	if status, _ := fixture.query(true, "", "nonsense"); status != statusBadRequest {
		t.Fatalf("invalid-filter status = %d, want 400", status)
	}
}
