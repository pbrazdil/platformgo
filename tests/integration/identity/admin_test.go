package identity

import (
	"strings"
	"testing"
)

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_config_settings.rs:29
//	test: admin_set_risk_levels_applies_and_is_authz_gated
func TestAdminSetRiskLevelsAppliesAndIsAuthzGated(t *testing.T) {
	fixture := newAdminPlaneFixture()
	value, err := fixture.setRiskLevels(true, 120, 60)
	if err != nil || value != "120/60" {
		t.Fatalf("applied = %q, %v", value, err)
	}
	settings, err := fixture.readSettings(true)
	if err != nil || settings.Risk.MarginCallLevelPercent != 120 ||
		settings.Risk.StopOutLevelPercent != 60 {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	if _, err := fixture.setRiskLevels(true, 40, 60); err != errAdminBadRequest {
		t.Fatalf("invalid invariant error = %v", err)
	}
	if _, err := fixture.setRiskLevels(false, 100, 50); err != errAdminForbidden {
		t.Fatalf("non-admin error = %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_config_settings.rs:96
//	test: set_override_rings_the_runtime_setting_doorbell
func TestSetOverrideRingsTheRuntimeSettingDoorbell(t *testing.T) {
	fixture := newAdminPlaneFixture()
	if _, err := fixture.setRiskLevels(true, 110, 55); err != nil {
		t.Fatal(err)
	}
	if len(fixture.doorbells) != 1 ||
		fixture.doorbells[0].Topic != adminRuntimeTopic ||
		fixture.doorbells[0].Key != adminRiskLevelsKey {
		t.Fatalf("doorbells = %#v", fixture.doorbells)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_config_settings.rs:140
//	test: admin_set_slippage_bands_applies_and_is_authz_gated
func TestAdminSetSlippageBandsAppliesAndIsAuthzGated(t *testing.T) {
	fixture := newAdminPlaneFixture()
	value, err := fixture.setSlippageBands(true, 30, 500, 700)
	if err != nil || value != "30/500/700" {
		t.Fatalf("applied = %q, %v", value, err)
	}
	settings, _ := fixture.readSettings(true)
	if settings.Risk.FormMarketSlippageBPS != 30 ||
		settings.Risk.CloseSlippageBPS != 500 ||
		settings.Risk.TPSLSlippageBPS != 700 {
		t.Fatalf("settings = %#v", settings)
	}
	if _, err := fixture.setSlippageBands(true, 0, 500, 700); err != errAdminBadRequest {
		t.Fatalf("zero band error = %v", err)
	}
	if _, err := fixture.setSlippageBands(false, 40, 800, 1000); err != errAdminForbidden {
		t.Fatalf("non-admin error = %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_config_settings.rs:231
//	test: permission_catalog_lists_the_full_scope_and_is_gated
func TestPermissionCatalogListsTheFullScopeAndIsGated(t *testing.T) {
	fixture := newAdminPlaneFixture()
	resources, actions, err := fixture.permissionCatalog(true)
	if err != nil || len(resources) != 11 || len(actions) != 4 {
		t.Fatalf("catalog lengths = %d/%d, %v", len(resources), len(actions), err)
	}
	hasAccounts, hasRead := false, false
	for _, resource := range resources {
		if resource.ID == "" || resource.Label == "" {
			t.Fatalf("incomplete resource = %#v", resource)
		}
		hasAccounts = hasAccounts || resource.ID == "accounts"
	}
	for _, action := range actions {
		hasRead = hasRead || action.ID == "read"
	}
	if !hasAccounts || !hasRead {
		t.Fatalf("known entries absent")
	}
	if _, _, err := fixture.permissionCatalog(false); err != errAdminForbidden {
		t.Fatalf("non-admin error = %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_login.rs:11
//	test: login_issues_token_then_me_accepts_it
func TestLoginIssuesTokenThenMeAcceptsIt(t *testing.T) {
	fixture := newAdminPlaneFixture()
	fixture.registerAdmin("root", "root@xb.local", "s3cret-pw")
	status, token := fixture.login("root", "s3cret-pw")
	if status != adminStatusOK || token == "" {
		t.Fatalf("login = %d/%q", status, token)
	}
	status, subject, audience := fixture.me(token)
	if status != adminStatusOK || !strings.HasPrefix(subject, "admin::urn:xb:admin:") ||
		audience != "admin" {
		t.Fatalf("me = %d/%q/%q", status, subject, audience)
	}
	if status, _ := fixture.login("root", "nope"); status != adminStatusUnauthorized {
		t.Fatalf("wrong-password status = %d", status)
	}
	if status, _, _ := fixture.me(""); status != adminStatusUnauthorized {
		t.Fatalf("anonymous status = %d", status)
	}
	if status, _, _ := fixture.me("not.a.jwt"); status != adminStatusUnauthorized {
		t.Fatalf("invalid-token status = %d", status)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_login.rs:70
//	test: cli_dispatches_through_the_command_layer
func TestCLIDispatchesThroughTheCommandLayer(t *testing.T) {
	fixture := newAdminPlaneFixture()
	created, err := fixture.runCommand("admin.register", map[string]string{
		"login": "ops", "email": "ops@xb.local", "password": "s3cret-pw",
	})
	if err != nil || !strings.HasPrefix(created["id"], "urn:xb:admin:") {
		t.Fatalf("created = %#v, %v", created, err)
	}
	output, err := fixture.runCommand("admin.login", map[string]string{
		"login": "ops", "password": "s3cret-pw",
	})
	if err != nil || output["accessToken"] == "" {
		t.Fatalf("login output = %#v, %v", output, err)
	}
	if _, err := fixture.runCommand("admin.login", map[string]string{
		"login": "ops", "password": "nope",
	}); err == nil {
		t.Fatal("wrong password unexpectedly succeeded")
	}
	if _, err := fixture.runCommand("nope.nope", nil); err == nil {
		t.Fatal("unknown command unexpectedly succeeded")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_operators.rs:34
//	test: admin_operator_rbac_and_session_plane
func TestAdminOperatorRBACAndSessionPlane(t *testing.T) {
	fixture := newAdminPlaneFixture()
	admin := fixture.registerAdmin("ops1", "ops1@example.com", "correct horse battery staple")
	fixture.createRole("support", "read-only support")
	if err := fixture.grant(true, "support", "accounts", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	fixture.assign(admin.ID, "support")
	if fixture.admins[admin.ID].Login != "ops1" || fixture.roles["support"] == "" ||
		len(fixture.policies["support"]) != 1 ||
		fixture.policies["support"][0].Object != "accounts" ||
		fixture.policies["support"][0].Action != "read" ||
		!fixture.assignments[admin.ID]["support"] {
		t.Fatalf("operator RBAC plane is incomplete")
	}
	for _, ownerKind := range fixture.brokerKeys {
		if ownerKind != "broker" {
			t.Fatalf("unexpected API-key owner %q", ownerKind)
		}
	}
	disabled, err := fixture.setAdminStatus(admin.ID, "disabled")
	if err != nil || disabled.Status != "disabled" {
		t.Fatalf("disable = %#v, %v", disabled, err)
	}
	if _, err := fixture.setAdminStatus(admin.ID, "banished"); err == nil {
		t.Fatal("unknown status unexpectedly accepted")
	}
	user := fixture.seedUser("sesstrader")
	if len(fixture.sessions[user.ID]) != 0 {
		t.Fatalf("new user sessions = %#v", fixture.sessions[user.ID])
	}
	if err := fixture.revokeSession("unknown-session"); err != errAdminBadRequest {
		t.Fatalf("unknown revoke error = %v", err)
	}
	if err := fixture.grant(false, "support", "accounts", "write", "allow"); err != errAdminForbidden {
		t.Fatalf("non-admin grant error = %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_settings.rs:20
//	test: admin_settings_read_and_forbidden
func TestAdminSettingsReadAndForbidden(t *testing.T) {
	fixture := newAdminPlaneFixture()
	settings, err := fixture.readSettings(true)
	if err != nil || settings.Environment == "" ||
		settings.Risk.MarginCallLevelPercent <= 0 ||
		settings.Risk.StopOutLevelPercent <= 0 || len(settings.Currencies) == 0 {
		t.Fatalf("settings = %#v, %v", settings, err)
	}
	if _, err := fixture.readSettings(false); err != errAdminForbidden {
		t.Fatalf("non-admin error = %v", err)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_users.rs:28
//	test: admin_user_read_and_mutate_plane
func TestAdminUserReadAndMutatePlane(t *testing.T) {
	fixture := newAdminPlaneFixture()
	user := fixture.seedUser("usermgmt1")
	listed := fixture.listUsers("")
	found := false
	for _, item := range listed {
		found = found || item.ID == user.ID
	}
	if !found {
		t.Fatal("seeded user missing from list")
	}
	for _, item := range fixture.listUsers("usermgmt1") {
		if !strings.Contains(item.Login, "usermgmt1") {
			t.Fatalf("search leaked user %#v", item)
		}
	}
	got := fixture.users[user.ID]
	if got.ID != user.ID || got.Status != "active" || got.FailedAttempts != 0 {
		t.Fatalf("fresh user = %#v", got)
	}
	updated, err := fixture.updateUser(true, user.ID, "usermgmt1.new@example.com")
	if err != nil || updated.Email != "usermgmt1.new@example.com" {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if disabled := fixture.setUserStatus(user.ID, "disabled"); disabled.Status != "disabled" {
		t.Fatalf("disabled = %#v", disabled)
	}
	if active := fixture.setUserStatus(user.ID, "active"); active.Status != "active" {
		t.Fatalf("active = %#v", active)
	}
	if unlocked := fixture.unlockUser(user.ID); unlocked.FailedAttempts != 0 {
		t.Fatalf("unlocked = %#v", unlocked)
	}
	if _, err := fixture.updateUser(false, user.ID, "nope@example.com"); err != errAdminForbidden {
		t.Fatalf("non-admin update error = %v", err)
	}
}
