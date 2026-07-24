package accounts

import (
	"strings"
	"testing"
)

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_balance_saga.rs:10
//	test: happy_path_completes_when_stub_applies
//
// Adaptations:
//   - Async stub delivery and polling are replaced by synchronous saga advancement.
//
// Assertions preserved:
//   - Completed status and exactly one AdjustBalance with the account login and exact amount.
func TestBalanceSagaHappyPathCompletesWhenStubApplies(t *testing.T) {
	harness := NewHarness()
	account, _, err := harness.SeedActiveAccount("trader1")
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.AdjustBalance(account.ID, BalanceDeposit, "5000"); err != nil {
		t.Fatal(err)
	}
	saga, ok := harness.LastSaga(account.Login)
	if !ok || saga.Status != SagaCompleted {
		t.Fatalf("saga = %+v, found=%v", saga, ok)
	}
	received := harness.ReceivedBalanceCommands()
	if len(received) != 1 {
		t.Fatalf("received commands = %d, want 1", len(received))
	}
	if received[0].AccountLogin != account.Login || received[0].Amount.String() != "5000" {
		t.Fatalf("received command = %+v", received[0])
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_balance_saga.rs:30
//	test: permanent_reject_drives_saga_to_compensated
//
// Adaptations:
//   - Stub rejection is an explicit per-fixture fault.
//
// Assertions preserved:
//   - Compensated status and rejection text containing "insufficient".
func TestBalanceSagaPermanentRejectDrivesSagaToCompensated(t *testing.T) {
	harness := NewHarness()
	harness.SetBalanceFault(BalanceRejectsPermanently, "insufficient free balance")
	account, _, err := harness.SeedActiveAccount("trader1")
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.AdjustBalance(account.ID, BalanceWithdraw, "1000000"); err != nil {
		t.Fatal(err)
	}
	saga, ok := harness.LastSaga(account.Login)
	if !ok || saga.Status != SagaCompensated {
		t.Fatalf("saga = %+v, found=%v", saga, ok)
	}
	if !strings.Contains(strings.ToLower(saga.LastError), "insufficient") {
		t.Fatalf("last error = %q", saga.LastError)
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_balance_saga.rs:55
//	test: apply_timeout_drives_retry
//
// Adaptations:
//   - The ignored 90-second poller test uses an explicit drop fault and synchronous retry budget.
//
// Assertions preserved:
//   - Exhausted retries compensate the saga and emit more than one attempt.
func TestBalanceSagaApplyTimeoutDrivesRetry(t *testing.T) {
	harness := NewHarness()
	harness.SetBalanceFault(BalanceDropsAll, "")
	account, _, err := harness.SeedActiveAccount("trader1")
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.AdjustBalance(account.ID, BalanceDeposit, "5000"); err != nil {
		t.Fatal(err)
	}
	saga, ok := harness.LastSaga(account.Login)
	if !ok || saga.Status != SagaCompensated {
		t.Fatalf("saga = %+v, found=%v", saga, ok)
	}
	if attempts := len(harness.ReceivedBalanceCommands()); attempts <= 1 {
		t.Fatalf("received attempts = %d, want > 1", attempts)
	}
}
