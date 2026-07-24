package catalog

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_feeds.rs:22
//	test: fix_feed_create_persists_and_round_trips
func TestFixFeedCreatePersistsAndRoundTrips(t *testing.T) {
	fixture := newFeedFixture()
	config := standardFixConfig()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	tradingConfig := string(encoded)
	created, err := fixture.createFix("lp-fix", &tradingConfig, true)
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "lp-fix" {
		t.Fatalf("created name = %q", created.Name)
	}
	feeds := fixture.list()
	if len(feeds) != 1 || feeds[0].Protocol != "fix" ||
		feeds[0].Ingestion != "nautilus_adapter" || !feeds[0].Enabled {
		t.Fatalf("feeds = %#v", feeds)
	}
	var stored fixFeedConfig
	if err := json.Unmarshal([]byte(feeds[0].TradingConfig), &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, config) {
		t.Fatalf("stored config = %#v, want %#v", stored, config)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_feeds.rs:67
//	test: fix_feed_without_trading_config_is_rejected
func TestFixFeedWithoutTradingConfigIsRejected(t *testing.T) {
	fixture := newFeedFixture()
	if _, err := fixture.createFix("lp-fix-missing", nil, false); err == nil {
		t.Fatal("FIX feed without trading config was accepted")
	}
	for _, feed := range fixture.list() {
		if feed.Name == "lp-fix-missing" {
			t.Fatal("rejected FIX feed was persisted")
		}
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_feeds.rs:98
//	test: fix_feed_with_invalid_trading_config_is_rejected
func TestFixFeedWithInvalidTradingConfigIsRejected(t *testing.T) {
	fixture := newFeedFixture()
	invalid := `{"nope":true}`
	if _, err := fixture.createFix("lp-fix-invalid", &invalid, false); err == nil {
		t.Fatal("FIX feed with invalid trading config was accepted")
	}
	for _, feed := range fixture.list() {
		if feed.Name == "lp-fix-invalid" {
			t.Fatal("rejected FIX feed was persisted")
		}
	}
}
