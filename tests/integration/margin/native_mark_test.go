package margin

import "testing"

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_native_mark_source.rs:121
//	test: native_injected_mark_drives_the_stop_out
func TestNativeInjectedMarkDrivesTheStopOut(t *testing.T) {
	engine := newNativeMarkMarginEngine()
	entry, qty := engine.openLongViaInjectedQuote()
	if entry != nativeMarkWhole(10_000) || qty != nativeMarkDecimal(1_000) {
		t.Fatalf("opened entry/qty = %s/%s", entry, qty)
	}

	engine.injectNativeMark(entry, 0)
	floor := engine.maintenanceFloor()
	if floor != nativeMarkDecimal(50_000) {
		t.Fatalf("maintenance floor = %s, want 0.050000", floor)
	}
	engine.setBalance(floor.mul(nativeMarkWhole(3)))

	liq, reachable := engine.liquidationPrice()
	if !reachable || liq <= 0 || liq >= entry {
		t.Fatalf("long liquidation price = %s, entry = %s", liq, entry)
	}
	trigger := liq.mul(nativeMarkDecimal(990_000))
	engine.injectNativeMark(trigger, 0)

	if !engine.evaluateStopout() {
		t.Fatal("fresh native mark below liquidation price did not trigger stop-out")
	}
	if engine.valuationSource != "native" {
		t.Fatalf("valuation source = %q, want native", engine.valuationSource)
	}
	if engine.openPositions() != 0 || engine.stopoutCount != 1 {
		t.Fatalf("open positions/stop-outs = %d/%d", engine.openPositions(), engine.stopoutCount)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_native_mark_source.rs:185
//	test: stale_native_mark_fails_closed_to_the_live_quote
func TestStaleNativeMarkFailsClosedToTheLiveQuote(t *testing.T) {
	engine := newNativeMarkMarginEngine()
	entry, _ := engine.openLongViaInjectedQuote()
	floor := engine.maintenanceFloor()
	engine.setBalance(floor.mul(nativeMarkWhole(3)))

	liq, reachable := engine.liquidationPrice()
	if !reachable {
		t.Fatal("healthy long lacks a reachable liquidation price")
	}
	crash := liq.mul(nativeMarkDecimal(990_000))
	engine.injectNativeMark(entry, 60_000)
	engine.injectLiveQuote(crash)

	if !engine.evaluateStopout() {
		t.Fatal("stale native mark did not fail closed to the crashed live quote")
	}
	if engine.valuationSource != "live_quote" {
		t.Fatalf("valuation source = %q, want live_quote", engine.valuationSource)
	}
	if engine.openPositions() != 0 || engine.stopoutCount != 1 {
		t.Fatalf("open positions/stop-outs = %d/%d", engine.openPositions(), engine.stopoutCount)
	}
}
