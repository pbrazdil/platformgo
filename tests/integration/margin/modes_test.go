package margin

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_funding_gate.rs:52
//	test: unfunded_account_is_rejected_then_trades_after_deposit
func TestUnfundedAccountIsRejectedThenTradesAfterDeposit(t *testing.T) {
	fixture := newMarginModesFixture()
	const symbol = "BTC-PERP"

	err := fixture.marketBuy(symbol, "gate-unfunded", "0.001", "1000")
	if err == nil || !strings.Contains(err.Error(), "insufficient_funds") ||
		!strings.Contains(err.Error(), "free margin") {
		t.Fatalf("unfunded order error=%v", err)
	}
	if quantity := fixture.openQuantity(symbol); !quantity.IsZero() {
		t.Fatalf("unfunded order opened quantity %s", quantity)
	}

	fixture.deposit("1000000")
	if err := fixture.marketBuy(symbol, "gate-funded-0", "0.001", "1000"); err != nil {
		t.Fatalf("funded order: %v", err)
	}
	if quantity := fixture.openQuantity(symbol); quantity.Cmp(decimal.MustParse("0.001")) != 0 {
		t.Fatalf("funded open quantity=%s, want 0.001", quantity)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_isolated_hedging_guard.rs:42
//	test: hedging_account_downgrades_a_stale_isolated_override_to_cross
func TestHedgingAccountDowngradesAStaleIsolatedOverrideToCross(t *testing.T) {
	fixture := newMarginModesFixture()
	fixture.setBalance("1000000")
	const symbol = "BTC-PERP"

	if err := fixture.setMarginMode(symbol, marginModeIsolated); err != nil {
		t.Fatal(err)
	}
	fixture.setOMS(omsModeHedging)
	if err := fixture.marketBuy(symbol, "hedgeiso-0", "0.001", "1000"); err != nil {
		t.Fatal(err)
	}

	position := fixture.positions[symbol]
	if position.SignedQuantity.IsZero() {
		t.Fatal("expected an open position")
	}
	if position.MarginMode != marginModeCross {
		t.Fatalf("hedging account margin mode=%q, want cross", position.MarginMode)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_isolated_liquidation.rs:81
//	test: isolated_position_liquidates_on_its_own_collateral_while_the_account_stays_solvent
func TestIsolatedPositionLiquidatesOnItsOwnCollateralWhileAccountStaysSolvent(t *testing.T) {
	fixture := newMarginModesFixture()
	fixture.setBalance("1000000")
	const symbol = "BTC-PERP"
	totalBefore := fixture.balance
	if totalBefore.Sign() <= 0 {
		t.Fatalf("initial balance=%s", totalBefore)
	}

	if err := fixture.setMarginMode(symbol, marginModeIsolated); err != nil {
		t.Fatal(err)
	}
	if err := fixture.marketBuy(symbol, "isoopen-0", "0.001", "1000"); err != nil {
		t.Fatal(err)
	}
	position := fixture.positions[symbol]
	if position.EntryPrice.Sign() <= 0 {
		t.Fatalf("entry price=%s", position.EntryPrice)
	}

	crashMark, err := position.EntryPrice.Quo(decimal.MustParse("10"), 8, decimal.RoundHalfEven)
	if err != nil {
		t.Fatal(err)
	}
	fixture.injectMark(symbol, crashMark.Normalize().String())

	if quantity := fixture.openQuantity(symbol); !quantity.IsZero() {
		t.Fatalf("position remains open at quantity %s", quantity)
	}
	if stopouts := fixture.stopoutCount(); stopouts < 1 {
		t.Fatalf("stopout count=%d", stopouts)
	}
	halfBefore, err := totalBefore.Quo(decimal.MustParse("2"), 8, decimal.RoundHalfEven)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.balance.Cmp(halfBefore) <= 0 {
		t.Fatalf("isolated loss made account insolvent: before=%s after=%s", totalBefore, fixture.balance)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_margin_mode_open_guard.rs:122
//	test: margin_mode_and_leverage_are_locked_while_a_position_is_open
func TestMarginModeAndLeverageAreLockedWhilePositionIsOpen(t *testing.T) {
	fixture := newMarginModesFixture()
	fixture.setBalance("1000000")
	const symbol = "BTC-PERP"

	if err := fixture.marketBuy(symbol, "guard-open", "0.02", "1000"); err != nil {
		t.Fatal(err)
	}
	if quantity := fixture.openQuantity(symbol); quantity.Cmp(decimal.MustParse("0.02")) != 0 {
		t.Fatalf("open quantity=%s", quantity)
	}
	if err := fixture.setMarginMode(symbol, marginModeIsolated); err == nil ||
		!strings.Contains(err.Error(), "while a position is open") {
		t.Fatalf("mode guard error=%v", err)
	}
	if err := fixture.setLeverage(symbol, "5"); err == nil ||
		!strings.Contains(err.Error(), "while a position is open") {
		t.Fatalf("leverage guard error=%v", err)
	}

	fixture.close(symbol, "guard-close")
	if quantity := fixture.openQuantity(symbol); !quantity.IsZero() {
		t.Fatalf("close left quantity=%s", quantity)
	}
	if err := fixture.setMarginMode(symbol, marginModeIsolated); err != nil {
		t.Fatalf("set mode while flat: %v", err)
	}
	if err := fixture.setLeverage(symbol, "5"); err != nil {
		t.Fatalf("set leverage while flat: %v", err)
	}
	if mode := fixture.modeBySymbol[symbol]; mode != marginModeIsolated {
		t.Fatalf("flat margin mode=%q", mode)
	}
	if leverage := fixture.leverageBySymbol[symbol]; leverage.Cmp(decimal.MustParse("5")) != 0 {
		t.Fatalf("flat leverage=%s", leverage)
	}
}
