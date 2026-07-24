package catalog

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_polymarket_pagination.rs:69
//	test: discover_survives_gamma_offset_cap_422
func TestDiscoverSurvivesGammaOffsetCap422(t *testing.T) {
	page1 := gammaDiscover("")
	if len(page1.Items) != 1 || page1.NextCursor == nil {
		t.Fatalf("page 1 = %#v", page1)
	}
	page2 := gammaDiscover(*page1.NextCursor)
	if len(page2.Items) != 0 || page2.NextCursor != nil {
		t.Fatalf("graceful terminal page = %#v", page2)
	}
}
