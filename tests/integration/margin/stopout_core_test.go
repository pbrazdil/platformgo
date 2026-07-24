package margin

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:148
//	test: perps_liquidates_at_hl_maintenance_floor_above_scalar_floor
func TestStopoutCorePerpsLiquidatesAtHLMaintenanceFloorAboveScalarFloor(t *testing.T) {
	fixture := newStopoutCoreFixture()
	quantity := stopoutCoreDecimal("0.001")
	entry := stopoutCoreDecimal("10000")
	fixture.openLong(quantity, entry)

	hlFloor := fixture.maintenanceAt(entry)
	scalarFloor := stopoutCoreDivide(fixture.scalarUsed, stopoutCoreDecimal("2"))
	if hlFloor.Cmp(scalarFloor) <= 0 {
		t.Fatalf("HL floor %s must exceed scalar floor %s", hlFloor, scalarFloor)
	}
	targetEquity := stopoutCoreDivide(
		scalarFloor.Add(hlFloor),
		stopoutCoreDecimal("2"),
	)

	fixture.injectMark(entry, 0)
	fixture.setBalance(targetEquity)

	if fixture.position.open {
		t.Fatal("position remained open below the HL maintenance floor")
	}
	if targetEquity.Cmp(scalarFloor) <= 0 {
		t.Fatalf("target equity %s must remain above scalar floor %s", targetEquity, scalarFloor)
	}
	assertStopoutCoreLongClose(t, fixture, quantity)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:298
//	test: displayed_hl_liq_price_is_where_the_engine_fires
func TestStopoutCoreDisplayedHLLiqPriceIsWhereEngineFires(t *testing.T) {
	fixture := newStopoutCoreFixture()
	quantity := stopoutCoreDecimal("0.001")
	entry := stopoutCoreDecimal("10000")
	fixture.openLong(quantity, entry)
	fixture.injectMark(entry, 0)

	hlFloor := fixture.maintenanceAt(entry)
	equity := hlFloor.Mul(stopoutCoreDecimal("3"))
	fixture.setBalance(equity)
	if !fixture.position.open {
		t.Fatal("healthy position liquidated at the entry mark")
	}
	materialized := fixture.maintenanceAt(fixture.lastValuation)
	onePercent := stopoutCoreDivide(hlFloor, stopoutCoreDecimal("100"))
	difference := materialized.Sub(hlFloor)
	if difference.Sign() < 0 {
		difference = difference.Neg()
	}
	if materialized.Sign() <= 0 || difference.Cmp(onePercent) >= 0 {
		t.Fatalf("materialized maintenance %s is not within 1%% of HL floor %s", materialized, hlFloor)
	}

	displayed, ok := fixture.liquidationPrice(hlFloor)
	if !ok || displayed.Sign() <= 0 || displayed.Cmp(entry) >= 0 {
		t.Fatalf("expected positive long liquidation price below entry, got %s", displayed)
	}
	trigger := displayed.Mul(stopoutCoreDecimal("0.99"))
	fixture.injectMark(trigger, 0)

	if fixture.position.open {
		t.Fatalf("position remained open at mark %s below displayed liquidation price %s", trigger, displayed)
	}
	assertStopoutCoreLongClose(t, fixture, quantity)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:415
//	test: stale_mark_fails_closed_to_the_crashed_live_quote
func TestStopoutCoreStaleMarkFailsClosedToCrashedLiveQuote(t *testing.T) {
	fixture := newStopoutCoreFixture()
	quantity := stopoutCoreDecimal("0.001")
	entry := stopoutCoreDecimal("10000")

	// The resting 20000 buy is crossed and filled by the injected 10000 quote.
	limit := stopoutCoreDecimal("20000")
	crossingQuote := stopoutCoreDecimal("10000")
	if crossingQuote.Cmp(limit) > 0 {
		t.Fatalf("quote %s did not cross buy limit %s", crossingQuote, limit)
	}
	fixture.openLong(quantity, crossingQuote)
	fixture.injectQuote(crossingQuote)
	fixture.injectMark(entry, 0)

	hlFloor := fixture.maintenanceAt(entry)
	fixture.setBalance(hlFloor.Mul(stopoutCoreDecimal("3")))
	displayed, ok := fixture.liquidationPrice(hlFloor)
	if !ok {
		t.Fatal("healthy long did not have a reachable liquidation price")
	}
	crash := displayed.Mul(stopoutCoreDecimal("0.99"))

	fixture.injectMark(entry, 60_000)
	fixture.injectQuote(crash)

	if fixture.position.open {
		t.Fatal("stale healthy mark masked the crashed live quote")
	}
	if fixture.valuationSource != "live_quote" {
		t.Fatalf("valuation used %q, want stale-mark fallback to live_quote", fixture.valuationSource)
	}
	if !fixture.lastValuation.Equal(crash) {
		t.Fatalf("valued at %s, want crashed quote %s", fixture.lastValuation, crash)
	}
	assertStopoutCoreLongClose(t, fixture, quantity)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:521
//	test: margin_breach_triggers_auto_liquidation_to_flat
func TestStopoutCoreMarginBreachTriggersAutoLiquidationToFlat(t *testing.T) {
	fixture := newStopoutCoreFixture()
	quantity := stopoutCoreDecimal("0.001")
	entry := stopoutCoreDecimal("10000")
	fixture.openLong(quantity, entry)
	fixture.injectMark(entry, 0)
	if !fixture.position.open {
		t.Fatal("funded position did not remain open")
	}

	fixture.setBalance(stopoutCoreDecimal("0.01"))

	if fixture.position.open {
		t.Fatal("breached account was not automatically liquidated to flat")
	}
	assertStopoutCoreLongClose(t, fixture, quantity)
}

func assertStopoutCoreLongClose(t *testing.T, fixture *stopoutCoreFixture, quantity decimal.Decimal) {
	t.Helper()
	if len(fixture.stopoutOrders) != 1 {
		t.Fatalf("got %d stop-out orders, want exactly one", len(fixture.stopoutOrders))
	}
	order := fixture.stopoutOrders[0]
	if !order.quantity.Equal(quantity) {
		t.Fatalf("close quantity %s, want live position quantity %s", order.quantity, quantity)
	}
	if order.side != "SELL" {
		t.Fatalf("long close side %q, want SELL", order.side)
	}
	if order.status != "filled" {
		t.Fatalf("stop-out status %q, want filled", order.status)
	}
	if order.intentID != "stopout:1" {
		t.Fatalf("stop-out intent %q does not show deterministic deduplication", order.intentID)
	}
}
