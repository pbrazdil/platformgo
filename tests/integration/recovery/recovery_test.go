package recovery

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func assertDecimal(t *testing.T, got decimal.Decimal, want string) {
	t.Helper()
	if got.Cmp(decimal.MustParse(want)) != 0 {
		t.Fatalf("got %s want %s", got, want)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_balance_op_exactly_once.rs:82
//	test: deposit_applies_exactly_once_across_apply_window_crash
func TestDepositAppliesExactlyOnceAcrossApplyWindowCrash(t *testing.T) {
	repo := NewRepository()
	runtime := Restart(repo)
	if err := runtime.Deposit("op-1", "trader1", "5000", CrashAfterBalanceClaim); err == nil {
		t.Fatal("failpoint did not crash")
	}
	runtime = Restart(repo)
	if err := runtime.Deposit("op-1", "trader1", "5000", NoFailpoint); err != nil {
		t.Fatal(err)
	}
	assertDecimal(t, repo.Balances["trader1"].Total, "5000")
	if !repo.Ops["op-1"].Applied {
		t.Fatal("saga did not complete")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_parked_protective.rs:12
//	test: parked_protective_set_is_rederivable_across_the_bracket_lifecycle
func TestParkedProtectiveSetIsRederivableAcrossBracketLifecycle(t *testing.T) {
	repo := NewRepository()
	for _, o := range []Order{
		{ID: "entry", BracketID: "b", Leg: "entry", Side: "BUY", Status: "pending", Quantity: "0.003", Price: "50000"},
		{ID: "tp1", BracketID: "b", Leg: "take_profit", Side: "SELL", Status: "pending", Quantity: "0.001", Price: "60000"},
		{ID: "tp2", BracketID: "b", Leg: "take_profit", Side: "SELL", Status: "pending", Quantity: "0.002", Price: "70000"},
		{ID: "sl", BracketID: "b", Leg: "stop_loss", Side: "SELL", Status: "pending", Quantity: "0.003", Price: "40000", MaxSlippageBPS: "1000"},
	} {
		repo.Orders[o.ID] = o
	}
	r := Restart(repo)
	if len(r.ParkedProtectives()) != 0 {
		t.Fatal("pending entry must not park exits")
	}
	r.MarkWorking("entry")
	sets := r.ParkedProtectives()
	if len(sets) != 1 || sets[0].EntryID != "entry" || sets[0].EntryFilled || sets[0].ExitSide != "SELL" || len(sets[0].TakeProfits) != 2 {
		t.Fatal("working-entry protection differs")
	}
	if sets[0].TakeProfits[0].Price != "60000" || sets[0].TakeProfits[0].Quantity != "0.001" || sets[0].StopLoss.Price != "40000" || sets[0].StopLoss.Quantity != "0.003" || sets[0].StopLoss.MaxSlippageBPS != "1000" {
		t.Fatal("protective decimals differ")
	}
	r.Fill("entry", "filled", "0.003")
	if !r.ParkedProtectives()[0].EntryFilled {
		t.Fatal("filled entry not confirmed")
	}
	r.MarkWorking("tp1")
	sets = r.ParkedProtectives()
	if len(sets[0].TakeProfits) != 1 || sets[0].TakeProfits[0].ID != "tp2" || sets[0].StopLoss == nil {
		t.Fatal("armed TP was not removed")
	}
	var armed Order
	for _, o := range r.WorkingOrders() {
		if o.ID == "tp1" {
			armed = o
		}
	}
	if !armed.ReduceOnly {
		t.Fatal("armed protection not reduce-only")
	}
	r.MarkWorking("sl")
	sets = r.ParkedProtectives()
	if sets[0].StopLoss != nil || len(sets[0].TakeProfits) != 1 {
		t.Fatal("armed stop still parked")
	}
	r.MarkWorking("tp2")
	if len(r.ParkedProtectives()) != 0 {
		t.Fatal("fully armed bracket remains parked")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_partial_fill_leaves.rs:11
//	test: partially_filled_resting_order_recovers_at_leaves_quantity
func TestPartiallyFilledRestingOrderRecoversAtLeavesQuantity(t *testing.T) {
	repo := NewRepository()
	repo.Orders["o"] = Order{ID: "o", Status: "working", Quantity: "0.01"}
	r := Restart(repo)
	r.Fill("o", "partially_filled", "0.004")
	orders := r.WorkingOrders()
	if len(orders) != 1 || orders[0].ID != "o" || orders[0].Quantity != "0.006" {
		t.Fatalf("recovered %#v", orders)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_reduce_only.rs:12
//	test: resting_reduce_only_order_recovers_reduce_only
func TestRestingReduceOnlyOrderRecoversReduceOnly(t *testing.T) {
	repo := NewRepository()
	repo.Orders["normal"] = Order{ID: "normal", Status: "working"}
	repo.Orders["reduce"] = Order{ID: "reduce", Status: "working", ReduceOnly: true}
	orders := Restart(repo).WorkingOrders()
	if len(orders) != 2 || orders[0].ReduceOnly || !orders[1].ReduceOnly {
		t.Fatalf("flags changed: %#v", orders)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_schema_guard.rs:55
//	test: nautilus_internal_schema_has_the_columns_our_sql_reads
func TestNautilusInternalSchemaHasColumnsOurSQLReads(t *testing.T) {
	schema := SchemaColumns()
	if len(schema["position"]) == 0 {
		t.Fatal("position schema absent")
	}
	for _, c := range RequiredPositionColumns {
		if !schema["position"][c] {
			t.Fatalf("position missing %s", c)
		}
	}
	for _, c := range RequiredAccountEventColumns {
		if !schema["account_event"][c] {
			t.Fatalf("account_event missing %s", c)
		}
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:110
//	test: deposit_survives_runtime_restart
func TestDepositSurvivesRuntimeRestart(t *testing.T) {
	repo := NewRepository()
	r := Restart(repo)
	if err := r.Deposit("a", "1", "5000", NoFailpoint); err != nil {
		t.Fatal(err)
	}
	r = Restart(repo)
	if err := r.Deposit("b", "1", "3000", NoFailpoint); err != nil {
		t.Fatal(err)
	}
	assertDecimal(t, repo.Balances["1"].Total, "8000")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:207
//	test: exotic_currency_instrument_survives_runtime_restart
func TestExoticCurrencyInstrumentSurvivesRuntimeRestart(t *testing.T) {
	repo := NewRepository()
	repo.Instruments["EXOTIC-PERP.HYPERLIQUID"] = Instrument{"EXOTIC-PERP.HYPERLIQUID", "EXOTIC"}
	r := Restart(repo)
	_ = r.Deposit("a", "1", "5000", NoFailpoint)
	r = Restart(repo)
	_ = r.Deposit("b", "1", "3000", NoFailpoint)
	if repo.Instruments["EXOTIC-PERP.HYPERLIQUID"].Currency != "EXOTIC" {
		t.Fatal("exotic currency lost")
	}
	assertDecimal(t, repo.Balances["1"].Total, "8000")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:348
//	test: overprecise_avg_px_position_survives_runtime_restart
func TestOverpreciseAvgPxPositionSurvivesRuntimeRestart(t *testing.T) {
	repo := NewRepository()
	repo.Positions["p"] = Position{ID: "p", Login: "1", Quantity: "300", AvgPrice: "0.14445666666666668"}
	positions, err := Restart(repo).LoadPositions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := positions["p"]; !ok {
		t.Fatal("position lost")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:481
//	test: stale_open_row_that_is_actually_closed_is_excluded_from_used_margin
func TestStaleOpenRowActuallyClosedExcludedFromUsedMargin(t *testing.T) {
	repo := NewRepository()
	repo.Positions["p"] = Position{ID: "p", Quantity: "300", AvgPrice: "64000", OpenedAt: 10}
	repo.Closed["p"] = 11
	assertDecimal(t, Restart(repo).UsedMargin(), "0")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:581
//	test: opening_is_blocked_fail_closed_while_an_existing_position_is_unpriced
func TestOpeningBlockedFailClosedWhileExistingPositionUnpriced(t *testing.T) {
	repo := NewRepository()
	repo.Positions["p"] = Position{ID: "p", Login: "1", Quantity: "300"}
	err := Restart(repo).CanOpen()
	if err == nil || !strings.Contains(strings.ToUpper(err.Error()), "UNPRICED") {
		t.Fatalf("error %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:698
//	test: cache_load_is_scoped_to_the_nodes_provisioned_logins
func TestCacheLoadScopedToNodesProvisionedLogins(t *testing.T) {
	repo := NewRepository()
	repo.Positions["own"] = Position{ID: "own", Login: "1", Quantity: "1", AvgPrice: "1"}
	repo.Positions["foreign"] = Position{ID: "foreign", Login: "2", Quantity: "1", AvgPrice: "1"}
	loaded, err := Restart(repo).LoadPositions(map[string]bool{"1": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded["own"]; !ok {
		t.Fatal("own position absent")
	}
	if _, ok := loaded["foreign"]; ok {
		t.Fatal("foreign position loaded")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:823
//	test: load_accounts_survives_a_torn_balance_row_without_panicking
func TestLoadAccountsSurvivesTornBalanceRowWithoutPanicking(t *testing.T) {
	repo := NewRepository()
	repo.Balances["1"] = Balance{Total: decimal.MustParse("5000"), Free: decimal.MustParse("5001"), Locked: decimal.MustParse("0")}
	recovered := Restart(repo).LoadAccounts()["1"]
	assertDecimal(t, recovered.Total, "5000")
	assertDecimal(t, recovered.Free, "5000")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:906
//	test: load_positions_fails_closed_on_a_malformed_realized_pnl
func TestLoadPositionsFailsClosedOnMalformedRealizedPnL(t *testing.T) {
	repo := NewRepository()
	repo.Positions["p"] = Position{ID: "p", Login: "1", Quantity: "1", AvgPrice: "1", RealizedPnL: "corrupt"}
	if _, err := Restart(repo).LoadPositions(nil); err == nil {
		t.Fatal("malformed PnL recovered as zero")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/recovery/e2e_recovery.rs:995
//	test: open_position_survives_runtime_restart
func TestOpenPositionSurvivesRuntimeRestart(t *testing.T) {
	repo := NewRepository()
	repo.Positions["p"] = Position{ID: "p", Login: "1", Instrument: "BTC-PERP", Quantity: "0.001", AvgPrice: "64000"}
	r := Restart(repo)
	positions, err := r.LoadPositions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if positions["p"].Quantity != "0.001" {
		t.Fatal("open quantity lost")
	}
	delete(repo.Positions, "p")
	r = Restart(repo)
	positions, _ = r.LoadPositions(nil)
	if len(positions) != 0 {
		t.Fatal("recovered position did not net flat")
	}
}
