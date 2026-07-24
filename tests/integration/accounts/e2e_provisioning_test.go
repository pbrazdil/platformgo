package accounts

import "testing"

// Ported from:
//
//	platform: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/accounts/e2e_provisioning.rs:17
//	test: account_provisioning_via_admin_and_broker_apis
//
// Adaptations:
//   - HTTP clients, server startup, RBAC persistence, and broker keys are fixture-owned principals.
//
// Assertions preserved:
//   - Login statuses; admin, viewer, broker, and anonymous status codes; assigned login, user URN, and currencies.
func TestAccountProvisioningViaAdminAndBrokerAPIs(t *testing.T) {
	harness := NewHarness()
	user := harness.CreateUser("trader1")
	password := "correct horse battery staple"
	ops := AdminPrincipal("accounts:create")
	viewer := AdminPrincipal()
	broker := BrokerPrincipal("accounts:write")

	if status := harness.Authenticate(ops, password); status != StatusOK {
		t.Fatalf("ops login = %d, want %d", status, StatusOK)
	}
	status, view := harness.ProvisionAccount(ops, user.ID, nil, nil)
	if status != StatusCreated {
		t.Fatalf("admin provision status = %d, want %d", status, StatusCreated)
	}
	if view == nil || view.Login <= 0 || view.UserID != user.ID ||
		view.BaseCurrency != CurrencyUSDC || user.URN() != "urn:user:"+view.UserID {
		t.Fatalf("admin provisioned view = %+v", view)
	}

	if status := harness.Authenticate(viewer, password); status != StatusOK {
		t.Fatalf("viewer login = %d, want %d", status, StatusOK)
	}
	status, _ = harness.ProvisionAccount(viewer, user.ID, nil, nil)
	if status != StatusForbidden {
		t.Fatalf("viewer provision status = %d, want %d", status, StatusForbidden)
	}

	usd, venue := CurrencyUSD, VenueFixCFD
	status, view = harness.ProvisionAccount(broker, user.ID, &usd, &venue)
	if status != StatusCreated {
		t.Fatalf("broker provision status = %d, want %d", status, StatusCreated)
	}
	if view == nil || view.BaseCurrency != CurrencyUSD ||
		user.URN() != "urn:user:"+view.UserID {
		t.Fatalf("broker provisioned view = %+v", view)
	}

	status, _ = harness.ProvisionAccount(Principal{Kind: PrincipalAnonymous}, user.ID, nil, nil)
	if status != StatusUnauthorized {
		t.Fatalf("anonymous provision status = %d, want %d", status, StatusUnauthorized)
	}
}
