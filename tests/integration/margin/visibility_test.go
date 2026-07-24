package margin

import "testing"

func requireVisibilityDecimal(t *testing.T, got visibilityDecimal, want string) {
	t.Helper()
	if got != visibilityDecimalFrom(want) {
		t.Fatalf("decimal = %q, want %q", got, want)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_equity.rs:24
//	test: account_equity_reflects_open_position_on_real_hyperliquid
func TestAccountEquityReflectsOpenPositionOnRealHyperliquid(t *testing.T) {
	engine := newVisibilityEngine("1000000", "1", "25")
	result := engine.submitOrder(visibilityDecimalFrom("0.001"), visibilityDecimalFrom("60000"))
	if !result.Accepted || result.Status != 202 {
		t.Fatalf("order result = %#v, want accepted", result)
	}
	if err := engine.injectMark(visibilityDecimalFrom("120000")); err != nil {
		t.Fatal(err)
	}

	balance := engine.balance()
	positionPnL := engine.position.unrealizedPnL()
	requireVisibilityDecimal(t, positionPnL, "60")
	requireVisibilityDecimal(t, balance.Equity.subtract(balance.Total), "60")
	requireVisibilityDecimal(t, balance.Equity, "1000060")
	if balance.Equity.compare(visibilityDecimalFrom("0")) <= 0 {
		t.Fatalf("equity = %s, want positive", balance.Equity)
	}
	if balance.Free.compare(balance.Equity) > 0 {
		t.Fatalf("free = %s, equity = %s", balance.Free, balance.Equity)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_margin_denial_taxonomy.rs:56
//	test: well_funded_leveraged_order_within_margin_is_accepted
func TestWellFundedLeveragedOrderWithinMarginIsAccepted(t *testing.T) {
	engine := newVisibilityEngine("10000", "1", "10")
	result := engine.submitOrder(visibilityDecimalFrom("0.21"), visibilityDecimalFrom("50000"))

	requireVisibilityDecimal(t, engine.locked, "1050")
	if result.Status != 202 || !result.Accepted || result.Error != nil {
		t.Fatalf("order result = %#v, want accepted 202", result)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_margin_denial_taxonomy.rs:191
//	test: engine_margin_denial_surfaces_as_typed_insufficient_margin
func TestEngineMarginDenialSurfacesAsTypedInsufficientMargin(t *testing.T) {
	engine := newVisibilityEngine("1", "1", "25")
	result := engine.submitOrder(visibilityDecimalFrom("0.1"), visibilityDecimalFrom("60000"))

	if result.Status != 400 || result.Accepted {
		t.Fatalf("order result = %#v, want denied 400", result)
	}
	if result.Error == nil || result.Error.Reason != visibilityInsufficientMargin {
		t.Fatalf("error = %#v, want typed %q", result.Error, visibilityInsufficientMargin)
	}
	requireVisibilityDecimal(t, engine.balance().Total, "1")
	requireVisibilityDecimal(t, engine.balance().Locked, "0")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_visibility.rs:53
//	test: balances_used_margin_equals_qty_entry_margin_init_over_leverage
func TestBalancesUsedMarginEqualsQtyEntryMarginInitOverLeverage(t *testing.T) {
	engine := newVisibilityEngine("10000", "0.5", "25")
	quantity := visibilityDecimalFrom("0.01")
	entry := visibilityDecimalFrom("60000")
	result := engine.submitOrder(quantity, entry)
	if !result.Accepted {
		t.Fatalf("order result = %#v, want accepted", result)
	}

	balance := engine.balance()
	expectedUsed := quantity.multiply(entry).multiply(engine.marginInit).divide(engine.leverage)
	requireVisibilityDecimal(t, expectedUsed, "12")
	if balance.Locked != expectedUsed {
		t.Fatalf("locked = %s, expected qty*entry*marginInit/leverage = %s", balance.Locked, expectedUsed)
	}
	requireVisibilityDecimal(t, balance.Total, "10000")
	requireVisibilityDecimal(t, balance.Free, "9988")
	if balance.Free.compare(balance.Total) >= 0 {
		t.Fatalf("free = %s, total = %s", balance.Free, balance.Total)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_visibility.rs:164
//	test: leverage_change_reprojects_used_margin
func TestLeverageChangeReprojectsUsedMargin(t *testing.T) {
	engine := newVisibilityEngine("10000", "0.5", "25")
	quantity := visibilityDecimalFrom("0.01")
	entry := visibilityDecimalFrom("60000")
	if result := engine.submitOrder(quantity, entry); !result.Accepted {
		t.Fatalf("first order result = %#v, want accepted", result)
	}
	lockedBefore := engine.balance().Locked
	requireVisibilityDecimal(t, lockedBefore, "12")

	engine.closePosition()
	if err := engine.setLeverage(visibilityDecimalFrom("5")); err != nil {
		t.Fatal(err)
	}
	if result := engine.submitOrder(quantity, entry); !result.Accepted {
		t.Fatalf("reopened order result = %#v, want accepted", result)
	}
	lockedAfter := engine.balance().Locked
	requireVisibilityDecimal(t, lockedAfter, "60")
	if lockedAfter.compare(lockedBefore.multiply(visibilityDecimalFrom("2"))) <= 0 {
		t.Fatalf("lower leverage must raise margin by more than 2x: before=%s after=%s", lockedBefore, lockedAfter)
	}
}
