package margin

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func requireLiquidationDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.MustParse(want)) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:609
//	test: short_margin_breach_liquidates_with_a_buy_close
func TestShortMarginBreachLiquidatesWithABuyClose(t *testing.T) {
	fixture := newLiquidationFixture()
	account := fixture.addAccount("short-account", false)
	fixture.deposit(account, "1000000")
	position := fixture.open(account, "BTC-PERP", "sell", "0.001", "60000")

	if position.Signed.Sign() >= 0 {
		t.Fatalf("opened signed quantity %s, want a short", position.Signed)
	}
	if position.magnitude().Sign() <= 0 {
		t.Fatalf("opened magnitude %s, want positive", position.magnitude())
	}
	if err := fixture.breach(account); err != nil {
		t.Fatal(err)
	}
	order := fixture.liquidateMarket(account, position)

	if position.Open {
		t.Fatal("breached short account was not liquidated to flat")
	}
	if len(account.Orders) != 1 {
		t.Fatalf("got %d stop-out orders, want exactly one", len(account.Orders))
	}
	requireLiquidationDecimal(t, order.Quantity, "0.001")
	if order.Side != "buy" {
		t.Fatalf("short liquidation side = %q, want buy", order.Side)
	}
	if order.Status != "filled" {
		t.Fatalf("short liquidation status = %q, want filled", order.Status)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:699
//	test: liquidation_stays_within_the_mark_band_and_never_dumps_into_an_adverse_book
func TestLiquidationStaysWithinTheMarkBandAndNeverDumpsIntoAnAdverseBook(t *testing.T) {
	fixture := newLiquidationFixture()
	account := fixture.addAccount("bounded-account", false)
	fixture.deposit(account, "1000000")
	position := fixture.open(account, "BTC-PERP", "buy", "0.001", "60000")
	fixture.setMark(position, "60000")
	if err := fixture.breach(account); err != nil {
		t.Fatal(err)
	}

	order := fixture.liquidateBounded(account, position, "50000", 500)
	floor := decimal.MustParse("57000")
	if position.Open {
		t.Fatal("breached long was not liquidated to flat")
	}
	if order.Close.Cmp(floor) < 0 {
		t.Fatalf("close %s dumped below the 500 bps floor %s", order.Close, floor)
	}
	if !order.ReduceOnly {
		t.Fatal("bounded liquidation was not reduce-only")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout.rs:789
//	test: liquidation_ladder_widens_the_bound_until_a_stuck_position_converges
func TestLiquidationLadderWidensTheBoundUntilAStuckPositionConverges(t *testing.T) {
	fixture := newLiquidationFixture()
	account := fixture.addAccount("ladder-account", false)
	fixture.deposit(account, "1000000")
	position := fixture.open(account, "BTC-PERP", "buy", "0.001", "75000")
	fixture.setMark(position, "75000")
	if err := fixture.breach(account); err != nil {
		t.Fatal(err)
	}

	order, attempted, err := fixture.liquidateWithLadder(account, position, "65800")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempted) != 3 || attempted[0] != 500 || attempted[1] != 1000 || attempted[2] != 1500 {
		t.Fatalf("ladder attempts = %v, want [500 1000 1500]", attempted)
	}
	if position.Open {
		t.Fatal("stuck position did not converge to flat")
	}
	if order.Close.Cmp(decimal.MustParse("60000")) < 0 {
		t.Fatalf("close %s breached widest 2000 bps floor 60000", order.Close)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout_hot_add.rs:121
//	test: hot_added_account_is_stop_out_liquidated
func TestHotAddedAccountIsStopOutLiquidated(t *testing.T) {
	fixture := newLiquidationFixture()
	account := fixture.addAccount("hotmargin", true)
	if account.Status != "pending" || account.MarginInitialized {
		t.Fatalf("hot account initial state = %q, initialized=%v", account.Status, account.MarginInitialized)
	}
	fixture.activate(account)
	if account.Status != "active" || !account.MarginInitialized {
		t.Fatalf("hot account was not reconciled: status=%q initialized=%v", account.Status, account.MarginInitialized)
	}
	fixture.deposit(account, "1000000")
	requireLiquidationDecimal(t, account.Balance, "1000000")
	position := fixture.open(account, "BTC-PERP", "buy", "0.001", "60000")
	if err := fixture.breach(account); err != nil {
		t.Fatal(err)
	}
	order := fixture.liquidateMarket(account, position)

	if position.Open {
		t.Fatal("hot-added account was not liquidated to flat")
	}
	if len(account.Orders) != 1 {
		t.Fatalf("got %d stop-out orders, want exactly one", len(account.Orders))
	}
	requireLiquidationDecimal(t, order.Quantity, "0.001")
	if order.Status != "filled" {
		t.Fatalf("hot-account liquidation status = %q, want filled", order.Status)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/margin/e2e_stopout_worst_pick.rs:58
//	test: stop_out_liquidates_the_largest_notional_position_first
func TestStopOutLiquidatesTheLargestNotionalPositionFirst(t *testing.T) {
	fixture := newLiquidationFixture()
	account := fixture.addAccount("worst-account", false)
	fixture.deposit(account, "1000000")
	btc := fixture.open(account, "BTC-PERP", "buy", "0.001", "60000")
	eth := fixture.open(account, "ETH-PERP", "buy", "0.01", "3000")
	if btc.magnitude().Cmp(eth.magnitude()) >= 0 {
		t.Fatalf("discriminating setup requires BTC quantity %s < ETH quantity %s", btc.magnitude(), eth.magnitude())
	}
	if btc.notional().Cmp(eth.notional()) <= 0 {
		t.Fatalf("discriminating setup requires BTC notional %s > ETH notional %s", btc.notional(), eth.notional())
	}
	if err := fixture.breach(account); err != nil {
		t.Fatal(err)
	}

	first, err := fixture.liquidateWorst(account)
	if err != nil {
		t.Fatal(err)
	}
	if first.Symbol != "BTC-PERP" {
		t.Fatalf("first stop-out symbol = %q, want largest-notional BTC-PERP", first.Symbol)
	}
	if btc.Open || !eth.Open {
		t.Fatalf("wrong position selected: BTC open=%v ETH open=%v", btc.Open, eth.Open)
	}
}
