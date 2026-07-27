package identity

import (
	"strings"
	"testing"
)

func brokerTraderRequireStatus(t *testing.T, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("status=%d, want %d", got, want)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:26
//	test: api_key_auth_plus_idempotency_replay
func TestAPIKeyAuthPlusIdempotencyReplay(t *testing.T) {
	fixture := brokerTraderNewFixture()
	token := fixture.createBrokerKey("partner-prop", []string{"accounts:read"}, nil, 0, "")
	brokerTraderRequireStatus(t, fixture.brokerPing(token, ""), brokerTraderStatusOK)
	brokerTraderRequireStatus(t, fixture.brokerPing("", ""), brokerTraderStatusUnauthorized)
	brokerTraderRequireStatus(t, fixture.brokerPing("xbk_deadbeef.notreal", ""), brokerTraderStatusUnauthorized)

	first := fixture.brokerEcho(token, "k1")
	replayed := fixture.brokerEcho(token, "k1")
	if first.ID != replayed.ID {
		t.Fatalf("same idempotency key did not replay: first=%#v replayed=%#v", first, replayed)
	}
	withoutKey := fixture.brokerEcho(token, "")
	if first.ID == withoutKey.ID {
		t.Fatalf("request without an idempotency key replayed: first=%#v next=%#v", first, withoutKey)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:204
//	test: idempotency_replay_does_not_bypass_scope_gate
func TestIdempotencyReplayDoesNotBypassScopeGate(t *testing.T) {
	fixture := brokerTraderNewFixture()
	user := fixture.createUserForTenant("", "replay-user", "replay-user@test", "")
	writer := fixture.createBrokerKey("writer", []string{"accounts:write"}, nil, 0, "")
	reader := fixture.createBrokerKey("reader", []string{"accounts:read"}, nil, 0, "")
	_, status := fixture.brokerCreateAccount(writer, user.ID, "replay-bypass-probe")
	brokerTraderRequireStatus(t, status, brokerTraderStatusCreated)
	_, status = fixture.brokerCreateAccount(reader, user.ID, "replay-bypass-probe")
	brokerTraderRequireStatus(t, status, brokerTraderStatusForbidden)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:263
//	test: ip_allowlist_rejects_non_matching_source
func TestIPAllowlistRejectsNonMatchingSource(t *testing.T) {
	fixture := brokerTraderNewFixture()
	token := fixture.createBrokerKey("ip-locked", []string{"*"}, []string{"203.0.113.7"}, 0, "")
	brokerTraderRequireStatus(t, fixture.brokerPing(token, "203.0.113.7"), brokerTraderStatusOK)
	brokerTraderRequireStatus(t, fixture.brokerPing(token, "198.51.100.9"), brokerTraderStatusUnauthorized)
	brokerTraderRequireStatus(t, fixture.brokerPing(token, ""), brokerTraderStatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_tenant_isolation.rs:40
//	test: broker_access_is_scoped_to_its_tenant
func TestBrokerAccessIsScopedToItsTenant(t *testing.T) {
	fixture := brokerTraderNewFixture()
	tenantA := fixture.createTenant("site-a")
	tenantB := fixture.createTenant("site-b")
	brokerA := fixture.brokerPrincipal(tenantA)
	brokerB := fixture.brokerPrincipal(tenantB)
	userA := fixture.brokerCreateUser(brokerA, "trader-a", "trader-a@site-a.test")
	accountA, status := fixture.brokerCreateAccount(brokerA, userA.ID, "")
	brokerTraderRequireStatus(t, status, brokerTraderStatusCreated)
	userB := fixture.brokerCreateUser(brokerB, "trader-b", "trader-b@site-b.test")
	accountB, status := fixture.brokerCreateAccount(brokerB, userB.ID, "")
	brokerTraderRequireStatus(t, status, brokerTraderStatusCreated)

	listA, _ := fixture.brokerListAccounts(brokerA)
	listB, _ := fixture.brokerListAccounts(brokerB)
	if len(listA) != 1 || listA[0].ID != accountA.ID || len(listB) != 1 || listB[0].ID != accountB.ID {
		t.Fatalf("tenant account lists leaked: A=%#v B=%#v", listA, listB)
	}
	own, status := fixture.brokerGetAccount(brokerA, accountA.ID)
	if status != brokerTraderStatusOK || own.ID != accountA.ID {
		t.Fatalf("own=%#v status=%d", own, status)
	}
	_, status = fixture.brokerGetAccount(brokerA, accountB.ID)
	if status == brokerTraderStatusOK {
		t.Fatal("broker A read broker B's account")
	}
	if status = fixture.brokerAdjustBalance(brokerA, accountB.ID, "100"); status == brokerTraderStatusOK {
		t.Fatal("broker A adjusted broker B's balance")
	}
	channelA := brokerTraderTenantChannel(tenantA)
	channelB := brokerTraderTenantChannel(tenantB)
	if channelA == channelB || !strings.HasPrefix(channelA, "tenant:") {
		t.Fatalf("tenant channels not isolated: A=%q B=%q", channelA, channelB)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_tenant_isolation.rs:181
//	test: admin_lists_carry_tenant_and_filter_by_it
func TestAdminListsCarryTenantAndFilterByIt(t *testing.T) {
	fixture := brokerTraderNewFixture()
	tenantA := fixture.createTenant("brand-a")
	tenantB := fixture.createTenant("brand-b")
	brokerA := fixture.brokerPrincipal(tenantA)
	brokerB := fixture.brokerPrincipal(tenantB)
	userA := fixture.brokerCreateUser(brokerA, "brand-a-user", "u@brand-a.test")
	accountA, _ := fixture.brokerCreateAccount(brokerA, userA.ID, "")
	userB := fixture.brokerCreateUser(brokerB, "brand-b-user", "u@brand-b.test")
	fixture.brokerCreateAccount(brokerB, userB.ID, "")

	usersA := fixture.adminUsers(tenantA)
	if len(usersA) != 1 || usersA[0].ID != userA.ID || usersA[0].Tenant != tenantA {
		t.Fatalf("usersA=%#v", usersA)
	}
	accountsB := fixture.adminAccounts(tenantB)
	if len(accountsB) != 1 || accountsB[0].Tenant != tenantB || accountsB[0].ID == accountA.ID {
		t.Fatalf("accountsB=%#v", accountsB)
	}
	fixture.createBrokerKey("key-a", []string{"*"}, nil, 0, tenantA)
	fixture.createBrokerKey("key-b", []string{"*"}, nil, 0, tenantB)
	keysA := fixture.adminKeys(tenantA)
	if len(keysA) != 1 {
		t.Fatalf("keysA=%#v", keysA)
	}
	foundKeyA := false
	for _, key := range keysA {
		foundKeyA = foundKeyA || key.Name == "key-a"
		if key.Tenant != tenantA {
			t.Fatalf("key without tenant A in filtered list: %#v", key)
		}
	}
	if !foundKeyA {
		t.Fatalf("key-a missing from %#v", keysA)
	}
	foundKeyB := false
	for _, key := range fixture.adminKeys("") {
		foundKeyB = foundKeyB || key.Name == "key-b" && key.Tenant == tenantB
	}
	if !foundKeyB {
		t.Fatal("brand B key was not tagged with tenant B")
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:175
//	test: trader_lists_own_accounts
func TestTraderListsOwnAccounts(t *testing.T) {
	fixture := brokerTraderNewFixture()
	user := fixture.createUserForTenant("", "owner1", "owner1@test", "correct horse battery staple")
	account, _ := fixture.createAccountForTenant("", user.ID)
	_, status := fixture.listMyAccounts("")
	brokerTraderRequireStatus(t, status, brokerTraderStatusUnauthorized)
	token, status := fixture.login("owner1", "correct horse battery staple", "client")
	brokerTraderRequireStatus(t, status, brokerTraderStatusOK)
	accounts, status := fixture.listMyAccounts(token)
	if status != brokerTraderStatusOK || len(accounts) != 1 {
		t.Fatalf("accounts=%#v status=%d", accounts, status)
	}
	got := accounts[0]
	if got.ID != account.ID || got.Login != account.Login || got.Status != "active" ||
		got.MarginMode != "cross" || got.BaseCurrency != "USDC" {
		t.Fatalf("account=%#v", got)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:229
//	test: client_creates_own_api_key
func TestClientCreatesOwnAPIKey(t *testing.T) {
	fixture := brokerTraderNewFixture()
	fixture.createUserForTenant("", "bottrader", "bot@xb.local", "trade-pw")
	token, status := fixture.login("bottrader", "trade-pw", "client")
	brokerTraderRequireStatus(t, status, brokerTraderStatusOK)
	apiKey, status := fixture.createMyAPIKey(token, "my-bot", []string{"orders:write"})
	if status != brokerTraderStatusCreated || !strings.HasPrefix(apiKey, "xbk_") {
		t.Fatalf("apiKey=%q status=%d", apiKey, status)
	}
	_, status = fixture.createMyAPIKey("", "x", nil)
	brokerTraderRequireStatus(t, status, brokerTraderStatusUnauthorized)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:280
//	test: per_owner_api_key_cap_is_enforced
func TestPerOwnerAPIKeyCapIsEnforced(t *testing.T) {
	fixture := brokerTraderNewFixture()
	fixture.maxKeys = 2
	fixture.createUserForTenant("", "capowner", "capowner@test", "pw")
	token, _ := fixture.login("capowner", "pw", "client")
	for i := range 2 {
		_, status := fixture.createMyAPIKey(token, "bot-"+string(rune('0'+i)), nil)
		brokerTraderRequireStatus(t, status, brokerTraderStatusCreated)
	}
	_, status := fixture.createMyAPIKey(token, "bot-over", nil)
	brokerTraderRequireStatus(t, status, brokerTraderStatusConflict)
}
