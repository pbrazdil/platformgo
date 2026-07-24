package nautilusmisc

import "testing"

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/nautilus/tests/live/config/e2e_feed_status.rs:22
// test: bridge_reports_feed_pricing_status_connected_once_it_prices
func TestBridgeReportsFeedPricingStatusConnectedOnceItPrices(t *testing.T) {
	fixture := newTradingFixture()
	fixture.importBTCPerpetual()
	if fixture.feedStatus != "disconnected" {
		t.Fatalf("fresh feed status = %q, want disconnected", fixture.feedStatus)
	}
	fixture.receivePrice()
	if fixture.feedStatus != "connected" {
		t.Fatalf("priced feed status = %q, want connected", fixture.feedStatus)
	}
}
