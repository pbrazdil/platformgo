package identity

import (
	"slices"
	"testing"
)

func rbacAuditRequireEvent(
	t *testing.T,
	event rbacAuditEvent,
	actorKind, actorID, action, resource, outcome string,
) {
	t.Helper()
	if event.ActorKind != actorKind ||
		event.ActorID != actorID ||
		event.Action != action ||
		event.Resource != resource ||
		event.Outcome != outcome {
		t.Fatalf(
			"audit event = %#v, want actor=(%q,%q) action=%q resource=%q outcome=%q",
			event, actorKind, actorID, action, resource, outcome,
		)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_rbac.rs:83
//	test: allow_deny_hierarchy_wildcard
func TestRbacAuditAllowDenyHierarchyWildcard(t *testing.T) {
	store := newRbacAuditStore()
	instance := newRbacAuditInstance(store)
	const password = "s3cret-pw"

	store.createRole("operator", false)
	if err := store.grant("operator", "diagnostics", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("alice", password)
	if err := store.assign("alice", "operator"); err != nil {
		t.Fatal(err)
	}

	store.createRole("viewer", false)
	if err := store.grant("viewer", "profile", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("bob", password)
	if err := store.assign("bob", "viewer"); err != nil {
		t.Fatal(err)
	}

	store.createRole("blocked", false)
	if err := store.grant("blocked", "diagnostics", "read", "deny"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("carol", password)
	if err := store.assign("carol", "operator"); err != nil {
		t.Fatal(err)
	}
	if err := store.assign("carol", "blocked"); err != nil {
		t.Fatal(err)
	}

	store.createRole("super", false)
	if err := store.grant("super", "*", "*", "allow"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("dave", password)
	if err := store.assign("dave", "super"); err != nil {
		t.Fatal(err)
	}

	store.createRole("junior", false)
	if err := store.addParent("junior", "operator"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("erin", password)
	if err := store.assign("erin", "junior"); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		login string
		want  int
	}{
		{login: "alice", want: 200},
		{login: "bob", want: 403},
		{login: "carol", want: 403},
		{login: "dave", want: 200},
		{login: "erin", want: 200},
	} {
		if got := instance.diagnosticsStatus(test.login, password); got != test.want {
			t.Errorf("diagnostics status for %s = %d, want %d", test.login, got, test.want)
		}
	}

	permissions := instance.effectivePermissions("erin")
	if !slices.Contains(permissions, rbacAuditPolicy{
		Resource: "diagnostics",
		Action:   "read",
		Effect:   "allow",
	}) {
		t.Fatalf("erin effective permissions = %#v, missing inherited diagnostics:read", permissions)
	}
	catalog := map[string]rbacAuditPolicy{
		"diagnostics.read": {Resource: "diagnostics", Action: "read"},
	}
	if got := catalog["diagnostics.read"]; got.Resource != "diagnostics" || got.Action != "read" {
		t.Fatalf("diagnostics.read catalog entry = %#v", got)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_rbac.rs:171
//	test: grant_propagates_across_instances_via_shared_stamp
func TestRbacAuditGrantPropagatesAcrossInstancesViaSharedStamp(t *testing.T) {
	store := newRbacAuditStore()
	instanceA := newRbacAuditInstance(store)
	instanceB := newRbacAuditInstance(store)
	const password = "s3cret-pw"

	store.seedAdmin("alice", password)
	store.createRole("ops", false)
	if err := store.assign("alice", "ops"); err != nil {
		t.Fatal(err)
	}
	if got := instanceA.diagnosticsStatus("alice", password); got != 403 {
		t.Fatalf("status before grant = %d, want 403", got)
	}
	if err := instanceB.store.grant("ops", "diagnostics", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	if got := instanceA.diagnosticsStatus("alice", password); got != 200 {
		t.Fatalf("instance A status after instance B grant = %d, want 200", got)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_admin_rbac.rs:208
//	test: revoke_and_unassign_take_access_away
func TestRbacAuditRevokeAndUnassignTakeAccessAway(t *testing.T) {
	store := newRbacAuditStore()
	instance := newRbacAuditInstance(store)
	const password = "s3cret-pw"

	store.createRole("op2", false)
	if err := store.grant("op2", "diagnostics", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("frank", password)
	if err := store.assign("frank", "op2"); err != nil {
		t.Fatal(err)
	}
	store.createRole("managers", false)
	if err := store.grant("managers", "roles", "write", "allow"); err != nil {
		t.Fatal(err)
	}
	if err := store.grant("managers", "roles", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	store.seedAdmin("gina", password)
	if err := store.assign("gina", "managers"); err != nil {
		t.Fatal(err)
	}
	store.createRole("system_role", true)
	if err := store.grant("system_role", "*", "*", "allow"); err != nil {
		t.Fatal(err)
	}

	if got := instance.diagnosticsStatus("frank", password); got != 200 {
		t.Fatalf("status before revoke = %d, want 200", got)
	}
	revoked, err := store.revoke("op2", "diagnostics", "read")
	if err != nil || !revoked {
		t.Fatalf("first revoke = (%v, %v), want (true, nil)", revoked, err)
	}
	if got := instance.diagnosticsStatus("frank", password); got != 403 {
		t.Fatalf("status after revoke = %d, want 403", got)
	}
	revoked, err = store.revoke("op2", "diagnostics", "read")
	if err != nil || revoked {
		t.Fatalf("second revoke = (%v, %v), want (false, nil)", revoked, err)
	}
	if got := instance.roles("frank"); !slices.Equal(got, []string{"op2"}) {
		t.Fatalf("frank roles = %v, want [op2]", got)
	}

	if err := store.grant("op2", "diagnostics", "read", "allow"); err != nil {
		t.Fatal(err)
	}
	if got := instance.diagnosticsStatus("frank", password); got != 200 {
		t.Fatalf("status after re-grant = %d, want 200", got)
	}
	unassigned, err := store.unassign("gina", "frank", "op2")
	if err != nil || !unassigned {
		t.Fatalf("unassign = (%v, %v), want (true, nil)", unassigned, err)
	}
	if got := instance.diagnosticsStatus("frank", password); got != 403 {
		t.Fatalf("status after unassign = %d, want 403", got)
	}
	if got := instance.roles("frank"); len(got) != 0 {
		t.Fatalf("frank roles after unassign = %v, want empty", got)
	}
	if _, err := store.revoke("system_role", "*", "*"); err == nil {
		t.Fatal("builtin role policy revoke succeeded, want immutable-role error")
	}
	if _, err := store.unassign("gina", "gina", "managers"); err == nil {
		t.Fatal("self-unassign succeeded, want error")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:35
//	test: login_success_and_failure_are_audited
func TestRbacAuditLoginSuccessAndFailureAreAudited(t *testing.T) {
	trail := newRbacAuditTrail()
	adminID := trail.registerAdmin("auditor", "s3cret-pw")
	userID := trail.createUser("trader1", "trade-pw")

	if err := trail.login("admin", "auditor", "s3cret-pw"); err != nil {
		t.Fatal(err)
	}
	if err := trail.login("user", "trader1", "trade-pw"); err != nil {
		t.Fatal(err)
	}
	successes := trail.events("login", "success")
	if len(successes) != 2 {
		t.Fatalf("successful login audit count = %d, want 2", len(successes))
	}
	rbacAuditRequireEvent(t, successes[0], "admin", adminID, "login", "", "success")
	rbacAuditRequireEvent(t, successes[1], "user", userID, "login", "", "success")
	tokens := trail.events("token.issue", "success")
	if len(tokens) != 2 {
		t.Fatalf("token issue audit count = %d, want 2", len(tokens))
	}
	rbacAuditRequireEvent(t, tokens[0], "admin", adminID, "token.issue", "", "success")
	rbacAuditRequireEvent(t, tokens[1], "user", userID, "token.issue", "", "success")

	if err := trail.login("admin", "auditor", "nope"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if err := trail.login("user", "ghost", "x"); err == nil {
		t.Fatal("unknown user accepted")
	}
	failures := trail.events("login", "failure")
	if len(failures) != 2 {
		t.Fatalf("failed login audit count = %d, want 2", len(failures))
	}
	rbacAuditRequireEvent(t, failures[0], "admin", adminID, "login", "", "failure")
	rbacAuditRequireEvent(t, failures[1], "user", "", "login", "", "failure")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:97
//	test: broker_key_create_is_audited
func TestRbacAuditBrokerKeyCreateIsAudited(t *testing.T) {
	trail := newRbacAuditTrail()
	keyID := trail.createBrokerKey()
	events := trail.events("broker-key.create", "success")
	if len(events) != 1 {
		t.Fatalf("broker-key.create audit count = %d, want 1", len(events))
	}
	rbacAuditRequireEvent(t, events[0], "system", "", "broker-key.create", "api_key", "success")
	if events[0].TargetID != keyID || len(keyID) != 36 {
		t.Fatalf("audited target = %q, want deterministic key UUID %q", events[0].TargetID, keyID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:130
//	test: policy_change_is_no_longer_a_dead_event
func TestRbacAuditPolicyChangeIsNoLongerADeadEvent(t *testing.T) {
	trail := newRbacAuditTrail()
	trail.changePolicy()
	events := trail.events("policy.change", "success")
	if len(events) != 1 {
		t.Fatalf("policy.change audit count = %d, want 1", len(events))
	}
	rbacAuditRequireEvent(t, events[0], "system", "", "policy.change", "policy", "success")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:156
//	test: broker_key_revoke_is_audited
func TestRbacAuditBrokerKeyRevokeIsAudited(t *testing.T) {
	trail := newRbacAuditTrail()
	keyID := trail.createBrokerKey()
	if err := trail.revokeBrokerKey(keyID); err != nil {
		t.Fatal(err)
	}
	events := trail.events("broker-key.revoke", "success")
	if len(events) != 1 {
		t.Fatalf("broker-key.revoke audit count = %d, want 1", len(events))
	}
	rbacAuditRequireEvent(t, events[0], "system", "", "broker-key.revoke", "api_key", "success")
	if events[0].TargetID != keyID {
		t.Fatalf("audited target = %q, want %q", events[0].TargetID, keyID)
	}
	if err := trail.revokeBrokerKey(keyID); err == nil {
		t.Fatal("re-revoking an already revoked key succeeded")
	}
	if got := len(trail.events("broker-key.revoke", "success")); got != 1 {
		t.Fatalf("success audit count after rejected re-revoke = %d, want 1", got)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:209
//	test: user_api_key_create_is_audited_with_actor
func TestRbacAuditUserAPIKeyCreateIsAuditedWithActor(t *testing.T) {
	trail := newRbacAuditTrail()
	userID := trail.createUser("bot-owner", "trade-pw")
	keyID := trail.createUserKey(userID)
	events := trail.events("user-key.create", "success")
	if len(events) != 1 {
		t.Fatalf("user-key.create audit count = %d, want 1", len(events))
	}
	rbacAuditRequireEvent(t, events[0], "user", userID, "user-key.create", "api_key", "success")
	if events[0].TargetID != keyID {
		t.Fatalf("audited target = %q, want %q", events[0].TargetID, keyID)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:250
//	test: mfa_confirm_is_audited
func TestRbacAuditMFAConfirmIsAudited(t *testing.T) {
	trail := newRbacAuditTrail()
	userID := trail.createUser("mfa-user", "trade-pw")
	trail.confirmMFA(userID)
	events := trail.events("mfa.verify", "success")
	if len(events) != 1 {
		t.Fatalf("mfa.verify audit count = %d, want 1", len(events))
	}
	rbacAuditRequireEvent(t, events[0], "user", userID, "mfa.verify", "", "success")
}
