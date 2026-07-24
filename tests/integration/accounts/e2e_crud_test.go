package accounts

import "testing"

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_crud.rs:13
//	test: account_create_and_list_for_a_user
//
// Adaptations:
//   - Composition and Postgres are replaced by an isolated synchronous Harness.
//
// Assertions preserved:
//   - Default and explicit currencies, distinct logins, user-scoped listing, and unknown-user rejection.
func TestAccountCreateAndListForAUser(t *testing.T) {
	harness := NewHarness()
	user := harness.CreateUser("trader1")

	first, err := harness.CreateAccount(user.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.BaseCurrency != CurrencyUSDC || first.UserID != user.ID {
		t.Fatalf("default account = %+v", first)
	}

	usd, venue := CurrencyUSD, VenueFixCFD
	second, err := harness.CreateAccount(user.ID, &usd, &venue)
	if err != nil {
		t.Fatal(err)
	}
	if first.Login == second.Login {
		t.Fatal("each account must receive a distinct login")
	}

	accounts := harness.ListAccounts(&user.ID)
	if len(accounts) != 2 {
		t.Fatalf("listed accounts = %d, want 2", len(accounts))
	}
	hasUSD := false
	for _, account := range accounts {
		if account.UserID != user.ID {
			t.Fatalf("account belongs to %q, want %q", account.UserID, user.ID)
		}
		hasUSD = hasUSD || account.BaseCurrency == CurrencyUSD
	}
	if !hasUSD {
		t.Fatal("user account list omitted the USD account")
	}
	if _, err := harness.CreateAccount("00000000-0000-0000-0000-000000000000", nil, nil); err == nil {
		t.Fatal("account creation for an unknown user succeeded")
	}
}

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_crud.rs:81
//	test: deposit_above_money_max_is_rejected_synchronously
//
// Adaptations:
//   - The SQL saga count is asserted directly from the isolated saga repository.
//
// Assertions preserved:
//   - Over-cap amount returns BadRequest and starts no balance saga.
func TestDepositAboveMoneyMaxIsRejectedSynchronously(t *testing.T) {
	harness := NewHarness()
	account, _, err := harness.SeedActiveAccount("overcap")
	if err != nil {
		t.Fatal(err)
	}

	err = harness.AdjustBalance(account.ID, BalanceDeposit, "100000000000000000000")
	if !IsAppError(err, ErrorBadRequest) {
		t.Fatalf("error = %v, want BadRequest", err)
	}
	if count := harness.SagaCount(account.Login); count != 0 {
		t.Fatalf("balance saga count = %d, want 0", count)
	}
}
