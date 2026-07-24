package nautilus_accounts

import (
	"strings"
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

func eq(t *testing.T, v decimal.Decimal, w string) {
	t.Helper()
	if v.Cmp(decimal.MustParse(w)) != 0 {
		t.Fatalf("got %s want %s", v, w)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/accounts/e2e_balance.rs:154
//	test: deposit_and_withdraw_move_the_engine_balance
func TestDepositAndWithdrawMoveEngineBalance(t *testing.T) {
	f := NewFixture()
	a, _ := f.CreateAccount("a", "trader1", "")
	if err := f.Boot(); err != nil {
		t.Fatal(err)
	}
	start := a.Total
	if err := f.Adjust("d", "a", "deposit", "5000"); err != nil {
		t.Fatal(err)
	}
	eq(t, a.Total, start.Add(decimal.MustParse("5000")).String())
	if err := f.Adjust("w", "a", "withdraw", "2000"); err != nil {
		t.Fatal(err)
	}
	eq(t, a.Total, "3000")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/accounts/e2e_balance.rs:205
//	test: saga_drives_deposit_to_completed_with_latency
func TestSagaDrivesDepositToCompletedWithLatency(t *testing.T) {
	f := NewFixture()
	a, _ := f.CreateAccount("a", "trader1", "")
	_ = f.Boot()
	_ = f.Adjust("d", "a", "deposit", "5000")
	eq(t, a.Total, "5000")
	s := f.Sagas["d"]
	if s.Status != "completed" || s.LedgerStatus != "settled" {
		t.Fatalf("saga %#v", s)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/accounts/e2e_balance.rs:265
//	test: duplicate_adjust_balance_applies_once
func TestDuplicateAdjustBalanceAppliesOnce(t *testing.T) {
	f := NewFixture()
	a, _ := f.CreateAccount("a", "trader1", "")
	_ = f.Boot()
	_ = f.Adjust("same", "a", "deposit", "5000")
	_ = f.Adjust("same", "a", "deposit", "5000")
	eq(t, a.Total, "5000")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/accounts/e2e_balance.rs:322
//	test: over_free_withdraw_fails_the_saga
func TestOverFreeWithdrawFailsSaga(t *testing.T) {
	f := NewFixture()
	a, _ := f.CreateAccount("a", "trader1", "")
	_ = f.Boot()
	_ = f.Adjust("w", "a", "withdraw", "1000000000")
	s := f.Sagas["w"]
	if s.Status != "compensated" || s.LedgerStatus != "reversed" || (!strings.Contains(s.LastError, "free") && !strings.Contains(s.LastError, "exceeds")) {
		t.Fatalf("saga %#v", s)
	}
	eq(t, a.Total, "0")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/it/accounts/e2e_provisioning_saga.rs:14
//	test: provisioning_saga_completes_when_runtime_provisions_the_account
func TestProvisioningSagaCompletesWhenRuntimeProvisionsAccount(t *testing.T) {
	f := NewFixture()
	a, _ := f.CreateAccount("a", "trader1", "")
	if a.Status != Pending {
		t.Fatal("account not pending before runtime")
	}
	if err := f.Boot(); err != nil {
		t.Fatal(err)
	}
	if a.Status != Active || f.Sagas["provision:a"].Status != "completed" {
		t.Fatal("provisioning incomplete")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/accounts/e2e_currency.rs:77
//	test: usd_account_trades_and_projects_in_its_own_currency
func TestUSDAccountTradesAndProjectsInOwnCurrency(t *testing.T) {
	f := NewFixture()
	a, err := f.CreateAccount("usd", "usdtrader", "USD")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Boot(); err != nil {
		t.Fatal(err)
	}
	_ = f.Adjust("d", "usd", "deposit", "1000000")
	rows := f.BalanceRows("usd")
	if _, ok := rows["USD"]; !ok {
		t.Fatal("USD row absent")
	}
	if _, ok := rows["USDC"]; ok {
		t.Fatal("USDC leaked")
	}
	if err = f.Trade("usd", "0.001"); err != nil {
		t.Fatal(err)
	}
	if a.OpenPositions != 1 || a.Total.Sign() <= 0 || a.Equity.Sign() <= 0 {
		t.Fatal("USD trade/equity missing")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/accounts/e2e_currency_boot_guard.rs:49
//	test: engine_refuses_boot_when_account_currency_has_no_seeded_rate
func TestEngineRefusesBootWhenAccountCurrencyHasNoSeededRate(t *testing.T) {
	f := NewFixture()
	f.SeededRates["EUR"] = false
	_, err := f.CreateAccount("eur", "ccyboot", "EUR")
	if err == nil || !strings.Contains(err.Error(), "settles in") {
		t.Fatalf("error %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/accounts/e2e_currency_boot_guard.rs:68
//	test: engine_boots_for_currencies_with_a_seeded_rate
func TestEngineBootsForCurrenciesWithSeededRate(t *testing.T) {
	f := NewFixture()
	if _, err := f.CreateAccount("usd", "ccyboot", "USD"); err != nil {
		t.Fatal(err)
	}
	if err := f.Boot(); err != nil {
		t.Fatalf("boot: %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/nautilus/tests/live/accounts/e2e_hot_add.rs:74
//	test: account_created_at_runtime_is_provisioned_and_trades_without_restart
func TestAccountCreatedAtRuntimeProvisionedAndTradesWithoutRestart(t *testing.T) {
	f := NewFixture()
	seed, _ := f.CreateAccount("seed", "seed", "")
	_ = seed
	if err := f.Boot(); err != nil {
		t.Fatal(err)
	}
	hot, err := f.CreateAccount("hot", "hot", "")
	if err != nil {
		t.Fatal(err)
	}
	if hot.Status != Active {
		t.Fatal("hot account not active")
	}
	if err = f.Adjust("d", "hot", "deposit", "1000000"); err != nil {
		t.Fatal(err)
	}
	eq(t, hot.Total, "1000000")
	if err = f.Trade("hot", "0.001"); err != nil {
		t.Fatal(err)
	}
	if hot.OpenPositions != 1 {
		t.Fatal("hot account did not trade")
	}
}
