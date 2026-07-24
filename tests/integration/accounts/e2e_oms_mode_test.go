package accounts

import (
	"errors"
	"testing"
)

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_oms_mode.rs:15
//	test: oms_mode_flip_requires_no_open_orders
//
// Adaptations:
//   - Catalog, order repository, and command handlers are synchronous fixture state.
//
// Assertions preserved:
//   - Flat mode flip succeeds, open order denies with OPEN_ORDERS, cancellation succeeds, then flip succeeds.
func TestOmsModeFlipRequiresNoOpenOrders(t *testing.T) {
	harness := NewHarness()
	account, _, err := harness.SeedActiveAccount("trader1")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := harness.SetOmsMode(account.ID, OmsModeHedging)
	if err != nil || updated.OmsMode != OmsModeHedging {
		t.Fatalf("hedging update = %+v, error = %v", updated, err)
	}
	if status := harness.SubmitOrder(account.ID, "oms-gate", true); status != StatusAccepted {
		t.Fatalf("limit order status = %d, want %d", status, StatusAccepted)
	}

	_, err = harness.SetOmsMode(account.ID, OmsModeNetting)
	var appError *AppError
	if !errors.As(err, &appError) || appError.Kind != ErrorDenied || appError.Reason != ReasonOpenOrders {
		t.Fatalf("mode-flip error = %#v, want Denied(OPEN_ORDERS)", err)
	}
	if !harness.CancelOrder(account.ID, "oms-gate") {
		t.Fatal("in-flight order did not cancel")
	}
	updated, err = harness.SetOmsMode(account.ID, OmsModeNetting)
	if err != nil || updated.OmsMode != OmsModeNetting {
		t.Fatalf("netting update = %+v, error = %v", updated, err)
	}
}
