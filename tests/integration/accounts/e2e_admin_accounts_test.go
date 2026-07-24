package accounts

import (
	"testing"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_admin_accounts.rs:32
//	test: admin_account_read_and_control_plane
//
// Adaptations:
//   - Admin queries and external controls use the isolated synchronous Harness.
//
// Assertions preserved:
//   - Header, balances, fleet list, leverage symbol, flat close-all, and client Forbidden behavior.
func TestAdminAccountReadAndControlPlane(t *testing.T) {
	harness := NewHarness()
	account, _, err := harness.SeedFundedAccount("trader1")
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.UpsertInstrument(btcPerpetual("BTC-PERP", CurrencyUSDC, CurrencyUSDC)); err != nil {
		t.Fatal(err)
	}

	got, err := harness.AdminGetAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Login != account.Login || got.MarketVenue == "" || len(got.PermittedClasses) == 0 {
		t.Fatalf("admin account header = %+v", got)
	}
	balances, err := harness.AdminBalances(account.ID)
	if err != nil || len(balances) == 0 {
		t.Fatalf("balances = %v, error = %v", balances, err)
	}
	listed := harness.ListAccounts(nil)
	found := false
	for _, item := range listed {
		found = found || item.Login == account.Login
	}
	if !found {
		t.Fatal("fleet account list omitted the funded account")
	}

	symbol, err := harness.SetLeverage(
		AdminPrincipal("accounts:write"),
		account.ID,
		"BTC-PERP",
		decimal.MustParse("20"),
	)
	if err != nil || symbol != "BTC-PERP" {
		t.Fatalf("leverage symbol = %q, error = %v", symbol, err)
	}
	closed, err := harness.CloseAll(
		AdminPrincipal("accounts:write"),
		account.ID,
		"01ARZ3NDEKTSV4RRFFQ69G5FAV",
	)
	if err != nil || len(closed) != 0 {
		t.Fatalf("closed positions = %v, error = %v", closed, err)
	}
	client := Principal{Kind: PrincipalClient, Permissions: map[string]bool{"user": true}}
	_, err = harness.SetLeverage(client, account.ID, "BTC-PERP", decimal.MustParse("10"))
	if !IsAppError(err, ErrorForbidden) {
		t.Fatalf("client leverage error = %v, want Forbidden", err)
	}
}
