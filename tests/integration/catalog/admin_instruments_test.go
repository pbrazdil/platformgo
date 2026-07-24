package catalog

import "testing"

func adminInstrument(symbol, asset string, leverage int) instrumentRecord {
	row := solPerp()
	row.Symbol, row.BaseAsset, row.DisplayName = symbol, asset, asset+" Perpetual"
	row.MaxLeverage, row.PriceIncrement, row.SizeIncrement = leverage, "0.1", "0.001"
	row.MaxQuantity, row.PositionCap = "1000", "100"
	row.MarginInit, row.MarginMaint = "0.02", "0.01"
	return row
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_admin_instruments.rs:96
//	test: admin_instruments_overridable_and_rbac_gated
func TestAdminInstrumentsOverridableAndRBACGated(t *testing.T) {
	fixture := newAdminCatalogFixture()
	if got := fixture.upsert(false, adminInstrument("BTC-PERP", "BTC", 5)); got != statusForbidden {
		t.Fatalf("viewer upsert status = %d, want 403", got)
	}
	if got := fixture.upsert(true, adminInstrument("BTC-PERP", "BTC", 7)); got != statusOK {
		t.Fatalf("operator upsert status = %d, want 200", got)
	}
	status, rows := fixture.list(true)
	if status != statusOK {
		t.Fatalf("list status = %d", status)
	}
	btc := rows[0]
	if btc.Symbol != "BTC-PERP" || btc.MaxLeverage != 7 {
		t.Fatalf("listed BTC = %#v", btc)
	}
	if got := fixture.upsert(true, adminInstrument("BTC-PERP", "BTC", 3)); got != statusOK {
		t.Fatalf("re-upsert status = %d", got)
	}
	_, rows = fixture.list(true)
	if len(rows) != 1 || rows[0].MaxLeverage != 3 {
		t.Fatalf("rows after re-upsert = %#v", rows)
	}
	if fixture.setTradingMode("BTC-PERP", "close_only") != statusOK {
		t.Fatal("setting close-only failed")
	}
	if fixture.setEnabled("BTC-PERP", false) != statusOK {
		t.Fatal("disabling instrument failed")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_admin_instruments.rs:208
//	test: admin_instrument_getone_patch_retire_plus_feeds
func TestAdminInstrumentGetOnePatchRetirePlusFeeds(t *testing.T) {
	fixture := newAdminCatalogFixture()
	if fixture.upsert(true, adminInstrument("BTC-PERP", "BTC", 5)) != statusOK {
		t.Fatal("upsert failed")
	}
	status, got := fixture.get(true, "BTC-PERP")
	if status != statusOK || got.Symbol != "BTC-PERP" || got.MaxLeverage != 5 {
		t.Fatalf("get-one status=%d row=%#v", status, got)
	}
	if fixture.patchLeverage("BTC-PERP", 11) != statusOK {
		t.Fatal("patch failed")
	}
	_, patched := fixture.get(true, "BTC-PERP")
	if patched.MaxLeverage != 11 || !patched.Enabled {
		t.Fatalf("patched row = %#v", patched)
	}
	if fixture.retire("BTC-PERP") != statusOK {
		t.Fatal("retire failed")
	}
	_, retired := fixture.get(true, "BTC-PERP")
	if retired.Enabled {
		t.Fatalf("retired row remains enabled: %#v", retired)
	}
	if fixture.feedListStatus() != statusOK || fixture.feedHealthStatus() != statusOK {
		t.Fatal("feed list or health failed")
	}
	if fixture.discoverFeedStatus("__nonexistent__") != statusBadRequest {
		t.Fatal("unknown feed venue was not rejected")
	}
	if status, _ := fixture.get(false, "BTC-PERP"); status != statusForbidden {
		t.Fatalf("viewer get status = %d, want 403", status)
	}
}
