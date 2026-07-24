package catalog

import (
	"slices"
	"testing"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_vendor_meta.rs:92
//	test: import_persists_typed_hl_vendor_meta_sibling
func TestImportPersistsTypedHLVendorMetaSibling(t *testing.T) {
	result, fixture := importHIP3Vendor()
	if result.Inserted != 1 || result.Markets != 0 {
		t.Fatalf("import result = %#v", result)
	}
	meta := fixture.meta
	if meta == nil || !meta.IsHIP3 || meta.Dex == nil || *meta.Dex != "para" ||
		meta.AssetIndex == nil || *meta.AssetIndex != 2 ||
		meta.NativeCoin == nil || *meta.NativeCoin != "para:BTC" ||
		meta.Deployer == nil || *meta.Deployer != "0x000000000000000000000000000000000000dead" ||
		meta.OracleUpdater == nil || *meta.OracleUpdater != "0x000000000000000000000000000000000000beef" ||
		meta.DexFullName == nil || *meta.DexFullName != "Para Builder DEX" {
		t.Fatalf("typed vendor metadata = %#v", meta)
	}
	view := fixture.view
	if view.Symbol != "BTC-PERP" || view.VendorMeta == nil || !view.VendorMeta.IsHIP3 ||
		view.VendorMeta.Dex == nil || *view.VendorMeta.Dex != "para" ||
		view.VendorMeta.Deployer == nil ||
		*view.VendorMeta.Deployer != "0x000000000000000000000000000000000000dead" ||
		view.VendorMeta.OracleUpdater == nil ||
		*view.VendorMeta.OracleUpdater != "0x000000000000000000000000000000000000beef" ||
		view.VendorMeta.DexFullName == nil || *view.VendorMeta.DexFullName != "Para Builder DEX" ||
		view.Category == nil || *view.Category != "hyperliquid_hip3_para" {
		t.Fatalf("instrument DTO = %#v", view)
	}
	if !slices.Equal(typedHLJSONColumns(), []string{"margin_tiers"}) {
		t.Fatalf("JSON columns = %v", typedHLJSONColumns())
	}
}
