package accounts

import "testing"

func accountStatusByName(t *testing.T, infos []AccountStatusInfo, status AccountStatus) AccountStatusInfo {
	t.Helper()
	for _, info := range infos {
		if info.Status == status {
			return info
		}
	}
	t.Fatalf("status %q is absent", status)
	return AccountStatusInfo{}
}

func containsAccountStatus(statuses []AccountStatus, want AccountStatus) bool {
	for _, status := range statuses {
		if status == want {
			return true
		}
	}
	return false
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_fsm.rs:20
//	test: account_status_fsm_gates_trading_and_transitions
//
// Adaptations:
//   - REST server, auth persistence, and runtime activation are synchronous fixture operations.
//
// Assertions preserved:
//   - Seven status descriptors, trading gates, reversible/terminal transitions, RBAC/broker statuses, and pending-account gate.
func TestAccountStatusFSMGatesTradingAndTransitions(t *testing.T) {
	harness := NewHarness()
	account, user, err := harness.SeedFundedAccount("fsmtrader")
	if err != nil {
		t.Fatal(err)
	}
	ops := AdminPrincipal("accounts:write")
	viewer := AdminPrincipal()
	broker := BrokerPrincipal("accounts:write")
	trader := Principal{Kind: PrincipalClient}
	password := "correct horse battery staple"
	for name, principal := range map[string]Principal{
		"ops": ops, "viewer": viewer, "trader": trader,
	} {
		if status := harness.Authenticate(principal, password); status != StatusOK {
			t.Fatalf("%s login = %d, want %d", name, status, StatusOK)
		}
	}

	infos := AccountStatusInfos()
	if len(infos) != 7 {
		t.Fatalf("status descriptor count = %d, want 7", len(infos))
	}
	active := accountStatusByName(t, infos, StatusActive)
	if !active.CanOpen || !active.CanClose {
		t.Fatal("active must permit open and close")
	}
	closeOnly := accountStatusByName(t, infos, StatusCloseOnly)
	if closeOnly.CanOpen || !closeOnly.CanClose {
		t.Fatal("close_only must permit close only")
	}
	frozen := accountStatusByName(t, infos, StatusFrozen)
	if frozen.CanOpen || frozen.CanClose {
		t.Fatal("frozen must prohibit open and close")
	}
	suspended := accountStatusByName(t, infos, StatusSuspended)
	if suspended.CanOpen || !suspended.CanClose || suspended.Terminal {
		t.Fatal("suspended must be reversible and permit close only")
	}
	if !containsAccountStatus(active.TransitionsTo, StatusSuspended) ||
		!containsAccountStatus(suspended.TransitionsTo, StatusActive) ||
		!containsAccountStatus(suspended.TransitionsTo, StatusClosed) {
		t.Fatal("active/suspended reversible transition graph differs")
	}
	closed := accountStatusByName(t, infos, StatusClosed)
	if !closed.Terminal || len(closed.TransitionsTo) != 0 {
		t.Fatal("closed must be terminal with no transitions")
	}
	if containsAccountStatus(active.TransitionsTo, StatusPending) {
		t.Fatal("active must not transition back to pending")
	}

	if status := harness.SubmitOrder(account.ID, "a-1", false); status != StatusAccepted {
		t.Fatalf("active submit = %d, want %d", status, StatusAccepted)
	}
	if status := harness.TransitionAccount(ops, account.ID, StatusFrozen); status != StatusOK {
		t.Fatalf("active -> frozen = %d, want %d", status, StatusOK)
	}
	if status := harness.SubmitOrder(account.ID, "f-1", false); status != StatusBadRequest {
		t.Fatalf("frozen submit = %d, want %d", status, StatusBadRequest)
	}
	if status := harness.ClosePosition(account.ID); status != StatusBadRequest {
		t.Fatalf("frozen close = %d, want %d", status, StatusBadRequest)
	}
	if status := harness.TransitionAccount(ops, account.ID, StatusPending); status != StatusBadRequest {
		t.Fatalf("frozen -> pending = %d, want %d", status, StatusBadRequest)
	}
	if status := harness.TransitionAccount(ops, account.ID, StatusFrozen); status != StatusBadRequest {
		t.Fatalf("frozen -> frozen = %d, want %d", status, StatusBadRequest)
	}
	if status := harness.TransitionAccount(ops, account.ID, StatusActive); status != StatusOK {
		t.Fatalf("frozen -> active = %d, want %d", status, StatusOK)
	}
	if status := harness.SubmitOrder(account.ID, "a-2", false); status != StatusAccepted {
		t.Fatalf("reactivated submit = %d, want %d", status, StatusAccepted)
	}
	if status := harness.TransitionAccount(ops, account.ID, StatusCloseOnly); status != StatusOK {
		t.Fatalf("active -> close_only = %d, want %d", status, StatusOK)
	}
	if status := harness.SubmitOrder(account.ID, "co-1", false); status != StatusBadRequest {
		t.Fatalf("close-only open = %d, want %d", status, StatusBadRequest)
	}
	if status := harness.TransitionAccount(viewer, account.ID, StatusActive); status != StatusForbidden {
		t.Fatalf("viewer transition = %d, want %d", status, StatusForbidden)
	}
	if status := harness.TransitionAccount(broker, account.ID, StatusActive); status != StatusOK {
		t.Fatalf("broker close_only -> active = %d, want %d", status, StatusOK)
	}

	pending, err := harness.CreateAccount(user.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if status := harness.SubmitOrder(pending.ID, "pending-1", false); status != StatusBadRequest {
		t.Fatalf("pending submit = %d, want %d", status, StatusBadRequest)
	}
}
