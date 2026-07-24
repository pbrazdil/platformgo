package trading

import (
	"reflect"
	"testing"
)

func requireTradingViewError(t *testing.T, err error, code string) {
	t.Helper()
	viewErr, ok := err.(*tradingViewError)
	if !ok || viewErr.Code != code {
		t.Fatalf("error=%#v, want code %q", err, code)
	}
}

func intValue(value *int) int {
	if value == nil {
		return -1
	}
	return *value
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_margin_config.rs:73
//	test: margin_config_reports_effective_leverage_and_is_ownership_gated
func TestMarginConfigReportsEffectiveLeverageAndIsOwnershipGated(t *testing.T) {
	fixture := newAccountViewsFixture()
	view, err := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if err != nil {
		t.Fatal(err)
	}
	if view.Asset != "BTC-PERP" || view.Leverage != "50" || view.MaxLeverage != 50 ||
		view.MarginMode != "cross" || view.MarginInit != "0.02" || view.MarginMaint != "0.01" {
		t.Fatalf("view=%#v", view)
	}
	_, err = fixture.marginConfig("user-2", "acct-1", "BTC-PERP")
	requireTradingViewError(t, err, "forbidden")
	err = fixture.setLeverage("user-2", "acct-1", "BTC-PERP", "5")
	requireTradingViewError(t, err, "forbidden")
	_, err = fixture.marginConfig("user-1", "acct-1", "UNKNOWN-PERP")
	requireTradingViewError(t, err, "not_found")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_margin_config.rs:151
//	test: set_leverage_caps_at_catalog_max_and_persists_a_valid_override
func TestSetLeverageCapsAtCatalogMaxAndPersistsAValidOverride(t *testing.T) {
	fixture := newAccountViewsFixture()
	for _, invalid := range []string{"100", "0", "1.5"} {
		err := fixture.setLeverage("user-1", "acct-1", "BTC-PERP", invalid)
		requireTradingViewError(t, err, "bad_request")
	}
	unchanged, err := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if err != nil || unchanged.Leverage != "50" {
		t.Fatalf("unchanged=%#v err=%v", unchanged, err)
	}
	if err := fixture.setLeverage("user-1", "acct-1", "BTC-PERP", "5"); err != nil {
		t.Fatal(err)
	}
	after, err := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if err != nil || after.Leverage != "5" || after.MaxLeverage != 50 {
		t.Fatalf("after=%#v err=%v", after, err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_margin_config.rs:250
//	test: margin_config_clamps_a_stale_override_to_a_lowered_catalog_max
func TestMarginConfigClampsAStaleOverrideToALoweredCatalogMax(t *testing.T) {
	fixture := newAccountViewsFixture()
	if err := fixture.setLeverage("user-1", "acct-1", "BTC-PERP", "5"); err != nil {
		t.Fatal(err)
	}
	mid, _ := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if mid.Leverage != "5" {
		t.Fatalf("mid=%#v", mid)
	}
	instrument := fixture.instruments["BTC-PERP"]
	instrument.MaxLeverage = 3
	fixture.instruments["BTC-PERP"] = instrument
	after, err := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if err != nil || after.Leverage != "3" || after.MaxLeverage != 3 {
		t.Fatalf("after=%#v err=%v", after, err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_margin_config.rs:304
//	test: set_margin_mode_persists_and_round_trips_through_the_read
func TestSetMarginModePersistsAndRoundTripsThroughTheRead(t *testing.T) {
	fixture := newAccountViewsFixture()
	before, _ := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if before.MarginMode != "cross" {
		t.Fatalf("before=%#v", before)
	}
	if err := fixture.setMarginMode("user-1", "acct-1", "BTC-PERP", "isolated"); err != nil {
		t.Fatal(err)
	}
	after, _ := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if after.MarginMode != "isolated" {
		t.Fatalf("after=%#v", after)
	}
	fixture.instruments["ETH-PERP"] = marginInstrument{
		Symbol: "ETH-PERP", MarginInit: "0.02", MarginMaint: "0.01", MaxLeverage: 50,
	}
	other, err := fixture.marginConfig("user-1", "acct-1", "ETH-PERP")
	if err != nil || other.MarginMode != "cross" {
		t.Fatalf("other=%#v err=%v", other, err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_margin_config.rs:366
//	test: margin_config_put_is_atomic_and_rejects_fractional_leverage
func TestMarginConfigPutIsAtomicAndRejectsFractionalLeverage(t *testing.T) {
	fixture := newAccountViewsFixture()
	if err := fixture.putMarginConfig("user-1", "acct-1", "BTC-PERP", "cross", "10"); err != nil {
		t.Fatal(err)
	}
	err := fixture.putMarginConfig("user-1", "acct-1", "BTC-PERP", "portfolio", "7")
	requireTradingViewError(t, err, "bad_request")
	after, _ := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if after.Leverage != "10" || after.MarginMode != "cross" {
		t.Fatalf("rejected update was not atomic: %#v", after)
	}
	err = fixture.putMarginConfig("user-1", "acct-1", "BTC-PERP", "cross", "1.5")
	requireTradingViewError(t, err, "bad_request")
	still, _ := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if still.Leverage != "10" {
		t.Fatalf("fractional update mutated state: %#v", still)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:39
//	test: admin_fleet_fills_blotter_reads_and_is_gated
func TestAdminFleetFillsBlotterReadsAndIsGated(t *testing.T) {
	fixture := newAccountViewsFixture()
	page, err := fixture.fleetBlotter("admin", "fills")
	if err != nil || len(page.Items) != 0 || intValue(page.Total) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	_, err = fixture.fleetBlotter("user-1", "fills")
	requireTradingViewError(t, err, "forbidden")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:71
//	test: admin_fleet_orders_blotter_reads_and_is_gated
func TestAdminFleetOrdersBlotterReadsAndIsGated(t *testing.T) {
	fixture := newAccountViewsFixture()
	page, err := fixture.fleetBlotter("admin", "orders")
	if err != nil || len(page.Items) != 0 || intValue(page.Total) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	_, err = fixture.fleetBlotter("user-1", "orders")
	requireTradingViewError(t, err, "forbidden")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:108
//	test: admin_fleet_positions_blotter_reads_and_is_gated
func TestAdminFleetPositionsBlotterReadsAndIsGated(t *testing.T) {
	fixture := newAccountViewsFixture()
	page, err := fixture.fleetBlotter("admin", "positions")
	if err != nil || len(page.Items) != 0 || intValue(page.Total) != 0 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	_, err = fixture.fleetBlotter("user-1", "positions")
	requireTradingViewError(t, err, "forbidden")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_admin_fleet_blotter.rs:136
//	test: admin_risk_monitor_reads_and_is_gated
func TestAdminRiskMonitorReadsAndIsGated(t *testing.T) {
	fixture := newAccountViewsFixture()
	rows, err := fixture.riskMonitor("admin")
	if err != nil || len(rows) != 0 {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	_, err = fixture.riskMonitor("user-1")
	requireTradingViewError(t, err, "forbidden")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_pagination.rs:13
//	test: orders_keyset_pagination_forward_and_back
func TestOrdersKeysetPaginationForwardAndBack(t *testing.T) {
	fixture := newAccountViewsFixture()
	for i := 0; i < 5; i++ {
		fixture.addPagedOrder("p-" + string(rune('0'+i)))
	}
	p1, err := fixture.ordersPage(2, "", "")
	if err != nil || len(p1.Items) != 2 || intValue(p1.Total) != 5 ||
		p1.PrevCursor != "" || p1.NextCursor == "" {
		t.Fatalf("p1=%#v err=%v", p1, err)
	}
	p2, err := fixture.ordersPage(2, p1.NextCursor, "")
	if err != nil || len(p2.Items) != 2 || p2.Total != nil ||
		p2.PrevCursor == "" || p2.NextCursor == "" {
		t.Fatalf("p2=%#v err=%v", p2, err)
	}
	p3, err := fixture.ordersPage(2, p2.NextCursor, "")
	if err != nil || len(p3.Items) != 1 || p3.NextCursor != "" || p3.PrevCursor == "" {
		t.Fatalf("p3=%#v err=%v", p3, err)
	}
	seen := make(map[string]bool)
	for _, page := range []cursorPage{p1, p2, p3} {
		for _, id := range page.Items {
			if seen[id] {
				t.Fatalf("duplicate order id %q", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("seen=%#v", seen)
	}
	back, err := fixture.ordersPage(2, p3.PrevCursor, "prev")
	if err != nil || !reflect.DeepEqual(back.Items, p2.Items) {
		t.Fatalf("back=%#v p2=%#v err=%v", back, p2, err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_pagination.rs:114
//	test: positions_are_a_bounded_single_page_not_keyset
func TestPositionsAreABoundedSinglePageNotKeyset(t *testing.T) {
	fixture := newAccountViewsFixture()
	page, err := fixture.positionsPage("", 2, "prev")
	if err != nil || len(page.Items) != 0 || intValue(page.Total) != 0 ||
		page.NextCursor != "" || page.PrevCursor != "" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	for _, status := range []string{"closed", "liquidated"} {
		filtered, err := fixture.positionsPage(status, 2, "")
		if err != nil || intValue(filtered.Total) != 0 {
			t.Fatalf("status=%q page=%#v err=%v", status, filtered, err)
		}
	}
	_, err = fixture.positionsPage("bogus", 2, "")
	requireTradingViewError(t, err, "bad_request")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_balances.rs:18
//	test: flat_account_balance_is_pure_cash_and_scale_stripped
func TestFlatAccountBalanceIsPureCashAndScaleStripped(t *testing.T) {
	fixture := newAccountViewsFixture()
	fixture.seedCash("acct-1", "1000.000000000000000", "999.999", "0.001")
	balance := fixture.balance("acct-1")
	if balance.Currency != "USDC" || balance.Total != "1000" || balance.Equity != "1000" ||
		balance.Free != "1000" || balance.Locked != "0" || balance.CrossEquity != "1000" ||
		balance.UnrealizedPnL != "0" || balance.MarginRatio != nil || balance.MaintenanceMargin != "0" {
		t.Fatalf("balance=%#v", balance)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_balances.rs:83
//	test: a_working_order_locks_its_reserved_margin
func TestAWorkingOrderLocksItsReservedMargin(t *testing.T) {
	fixture := newAccountViewsFixture()
	fixture.seedCash("acct-1", "10000", "0", "10000")
	before := fixture.balance("acct-1")
	if before.Locked != "0" {
		t.Fatalf("before=%#v", before)
	}
	command := limitBuyFixture("resting-1")
	command.OrderType, command.Quantity, command.LimitPrice = "LIMIT", "1", "50000"
	if _, err := fixture.orders.submit(command); err != nil {
		t.Fatal(err)
	}
	fixture.orders.markWorking("resting-1")
	config, err := fixture.marginConfig("user-1", "acct-1", "BTC-PERP")
	if err != nil || config.MarginInit != "0.02" || config.Leverage != "50" {
		t.Fatalf("config=%#v err=%v", config, err)
	}
	after := fixture.balance("acct-1")
	if after.Locked != "20" || after.Free != "9980" {
		t.Fatalf("after=%#v", after)
	}
}
