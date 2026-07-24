package catalog

import (
	"strings"
	"testing"
)

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/live/catalog/e2e_feeds_admin.rs:11
//	test: feed_protocols_create_and_list
//
// Adaptations:
//   - Live application composition replaced by an isolated feed adapter fixture.
func TestFeedProtocolsCreateAndList(t *testing.T) {
	fixture := newLiveCatalogFixture()
	protocol := fixture.protocol("hyperliquid_ws")
	if protocol.DefaultVenue != "hyperliquid" || len(protocol.ProductTypes) == 0 ||
		!strings.Contains(protocol.DefaultPricingConfig, "info_url") {
		t.Fatalf("Hyperliquid protocol defaults = %#v", protocol)
	}
	if feeds := fixture.listFeeds(); len(feeds) != 0 {
		t.Fatalf("feeds before create = %#v", feeds)
	}
	created, err := fixture.createFeed("hyperliquid", "hyperliquid_ws", true, true)
	if err != nil {
		t.Fatalf("create Hyperliquid feed: %v", err)
	}
	if created.Name != "hyperliquid" || created.Protocol != "hyperliquid_ws" ||
		!created.Enabled || created.PricingStatus != "disconnected" {
		t.Fatalf("created feed = %#v", created)
	}
	feeds := fixture.listFeeds()
	if len(feeds) != 1 || feeds[0].Name != "hyperliquid" ||
		feeds[0].Protocol != "hyperliquid_ws" || !feeds[0].Enabled ||
		feeds[0].PricingStatus != "disconnected" {
		t.Fatalf("feeds after create = %#v", feeds)
	}
	if _, err := fixture.createFeed("lp-fix", "fix", false, false); err == nil {
		t.Fatal("FIX without trading config was accepted")
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/live/catalog/e2e_feeds_admin.rs:79
//	test: feed_test_probes_live_hyperliquid
//
// Adaptations:
//   - Live Hyperliquid metadata call replaced by a checked-in deterministic universe.
func TestFeedProbeUsesDeterministicHyperliquidUniverse(t *testing.T) {
	fixture := newLiveCatalogFixture()
	if _, err := fixture.createFeed("hyperliquid", "hyperliquid_ws", true, true); err != nil {
		t.Fatalf("create feed: %v", err)
	}
	report, err := fixture.probeFeed("hyperliquid")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !report.OK || report.InstrumentCount <= 10 || report.PricingStatus != "connected" {
		t.Fatalf("probe report = %#v", report)
	}
	if got := fixture.listFeeds()[0].PricingStatus; got != "connected" {
		t.Fatalf("persisted pricing status = %q", got)
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/live/catalog/e2e_hyperliquid.rs:11
//	test: discover_import_and_never_clobber_on_real_hyperliquid
//
// Adaptations:
//   - Live venue discovery replaced by a fixed complete-enough market universe.
func TestDiscoverImportAndNeverClobberHyperliquid(t *testing.T) {
	fixture := newLiveCatalogFixture()
	discovered := fixture.discoverHyperliquid()
	if len(discovered) <= 50 {
		t.Fatalf("discovered %d markets, want > 50", len(discovered))
	}
	for _, market := range discovered {
		if strings.ContainsAny(market.VenueSymbol+market.SuggestedSymbol, "*?") || market.Imported {
			t.Fatalf("invalid initial market = %#v", market)
		}
	}
	btc := findLiveMarket(t, discovered, "BTC")
	if btc.QuoteCurrency != "USD" || btc.SettlementCurrency != "USDC" {
		t.Fatalf("BTC currencies = %#v", btc)
	}
	result := fixture.importHyperliquid([]string{"BTC", "ETH"}, 25)
	if result.Inserted != 2 {
		t.Fatalf("inserted = %d, want 2", result.Inserted)
	}
	instrument := fixture.instrument("BTC-PERP")
	if instrument.Provenance != "synced" || instrument.MaxLeverage != 25 || !instrument.Enabled {
		t.Fatalf("imported BTC = %#v", instrument)
	}
	if !findLiveMarket(t, fixture.discoverHyperliquid(), "BTC").Imported {
		t.Fatal("BTC was not marked imported")
	}
	fixture.operatorOverride("BTC-PERP", 10)
	result = fixture.importHyperliquid([]string{"BTC"}, 25)
	instrument = fixture.instrument("BTC-PERP")
	if result.Skipped != 1 || instrument.MaxLeverage != 10 ||
		instrument.Provenance != "synced_with_overrides" {
		t.Fatalf("reimport = %#v, instrument = %#v", result, instrument)
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/live/catalog/e2e_polymarket.rs:7
//	test: discover_polymarket_markets_with_legs
//
// Adaptations:
//   - Live Polymarket discovery replaced by deterministic paginated markets.
func TestDiscoverPolymarketMarketsWithLegs(t *testing.T) {
	fixture := newLiveCatalogFixture()
	first, cursor := fixture.discoverPolymarket("")
	if len(first) == 0 || cursor == "" {
		t.Fatalf("first page = %#v, cursor = %q", first, cursor)
	}
	firstKeys := make(map[string]struct{}, len(first))
	for _, market := range first {
		firstKeys[market.SelectKey] = struct{}{}
		if market.Parent == "" || len(market.Legs) < 2 {
			t.Fatalf("market hierarchy = %#v", market)
		}
		for _, leg := range market.Legs {
			if leg.QuoteCurrency != "USDC" || !strings.Contains(leg.VenueSymbol, "-") ||
				strings.ContainsAny(leg.SuggestedSymbol, "*?") ||
				leg.OutcomeIndex == nil || leg.OutcomeLabel == nil {
				t.Fatalf("invalid prediction leg = %#v", leg)
			}
		}
	}
	second, _ := fixture.discoverPolymarket(cursor)
	advanced := false
	for _, market := range second {
		if _, exists := firstKeys[market.SelectKey]; !exists {
			advanced = true
		}
	}
	if !advanced {
		t.Fatal("second page did not advance")
	}
}

func findLiveMarket(t *testing.T, markets []liveDiscoveredMarket, symbol string) liveDiscoveredMarket {
	t.Helper()
	for _, market := range markets {
		if market.VenueSymbol == symbol {
			return market
		}
	}
	t.Fatalf("%s not found", symbol)
	return liveDiscoveredMarket{}
}
