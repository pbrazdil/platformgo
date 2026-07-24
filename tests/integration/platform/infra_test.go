package platform

import (
	"reflect"
	"strings"
	"testing"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_arch_boundaries.rs:38
// test: core_contexts_do_not_reach_foreign_persistence_modules
func TestCoreContextsDoNotReachForeignPersistenceModules(t *testing.T) {
	fixture := newArchitectureFixture()
	if len(fixture.Core) <= 20 {
		t.Fatalf("architecture scan covered %d core files, want more than 20", len(fixture.Core))
	}
	if violations := fixture.coreViolations(); len(violations) != 0 {
		t.Fatalf("foreign persistence reaches: %v", violations)
	}
	fault := fixture
	fault.Core = append(fault.Core, coreArchitectureFile{
		Context: "auth", Persistence: []string{"orders"},
	})
	if violations := fault.coreViolations(); len(violations) != 1 {
		t.Fatalf("fault injection found %d violations, want 1: %v", len(violations), violations)
	}
	wantPorts := []string{
		"access.rs",
		"currency_guard.rs",
		"markets/catalog.rs",
		"trading/exposure.rs",
	}
	if !reflect.DeepEqual(fixture.Ports, wantPorts) {
		t.Fatalf("ports = %v, want %v", fixture.Ports, wantPorts)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_arch_boundaries.rs:199
// test: persistence_sql_statements_do_not_span_multiple_owners
func TestPersistenceSQLStatementsDoNotSpanMultipleOwners(t *testing.T) {
	fixture := newArchitectureFixture()
	if len(fixture.SQL) <= 15 {
		t.Fatalf("SQL scan covered %d statements, want more than 15", len(fixture.SQL))
	}
	if violations := fixture.sqlViolations(); len(violations) != 0 {
		t.Fatalf("SQL ownership violations: %v", violations)
	}
	faults := fixture
	faults.SQL = append(faults.SQL,
		sqlArchitectureStatement{
			Owners: []string{"accounts", "trading"}, Declared: []string{"accounts", "trading"},
			Write: true, Mapped: true,
		},
		sqlArchitectureStatement{Owners: []string{"unknown"}},
	)
	if violations := faults.sqlViolations(); len(violations) != 2 {
		t.Fatalf("fault injection found %d violations, want 2: %v", len(violations), violations)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_cache.rs:8
// test: cache_set_get_delete_roundtrip
func TestCacheSetGetDeleteRoundtrip(t *testing.T) {
	cache := newMemoryCache()
	cache.set("greeting", []byte("hello"))
	if got := string(cache.get("greeting")); got != "hello" {
		t.Fatalf("get = %q, want hello", got)
	}
	cache.delete("greeting")
	if got := cache.get("greeting"); got != nil {
		t.Fatalf("get after delete = %q, want nil", got)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_dsn_redaction.rs:6
// test: engine_cache_dsn_error_redacts_the_password
func TestEngineCacheDSNErrorRedactsThePassword(t *testing.T) {
	const secret = "topsecret_pw_8f3a"
	err := redactedDSNError("postgres://cacheuser:" + secret + "@localhost:5432/")
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("error did not redact password: %q", err)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_hardening.rs:9
// test: cors_preflight_and_request_id
func TestCORSPreflightAndRequestID(t *testing.T) {
	headers := newServerFixture().headers
	want := map[string]string{
		"access-control-allow-origin": "*",
		"x-content-type-options":      "nosniff",
		"x-frame-options":             "DENY",
		"referrer-policy":             "no-referrer",
	}
	for name, value := range want {
		if headers[name] != value {
			t.Errorf("%s = %q, want %q", name, headers[name], value)
		}
	}
	if headers["x-request-id"] == "" {
		t.Error("x-request-id is empty")
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_health.rs:9
// test: readyz_reports_all_dependencies_healthy
func TestReadyzReportsAllDependenciesHealthy(t *testing.T) {
	status, response := newServerFixture().readyz()
	if status != 200 {
		t.Fatalf("readyz status = %d, want 200", status)
	}
	if !response.Ready {
		t.Fatal("ready = false")
	}
	want := []healthComponent{
		{Name: "postgres", Healthy: true},
		{Name: "redis", Healthy: true},
		{Name: "rabbitmq", Healthy: true},
	}
	if !reflect.DeepEqual(response.Components, want) {
		t.Fatalf("components = %#v, want %#v", response.Components, want)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_kernel_isolation.rs:3
// test: app_does_not_link_the_nautilus_kernel
func TestAppDoesNotLinkTheNautilusKernel(t *testing.T) {
	forbidden := []string{
		"nautilus-live", "nautilus-system", "nautilus-execution", "nautilus-common",
		"nautilus-sandbox", "nautilus-portfolio", "nautilus-data",
		"nautilus-hyperliquid", "nautilus-trading", "nautilus-risk",
	}
	tree := strings.Join(applicationDependencies(), "\n")
	for _, dependency := range forbidden {
		if strings.Contains(tree, dependency) {
			t.Errorf("application links forbidden dependency %q", dependency)
		}
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_lifecycle.rs:14
// test: server_boots_rest_openapi_via_real_composition
func TestServerBootsRESTOpenAPIViaRealComposition(t *testing.T) {
	server := newServerFixture()
	if got := server.healthz(); got != "ok" {
		t.Fatalf("healthz = %q, want ok", got)
	}
	for _, path := range []string{
		"/admin/v1/openapi.json", "/v1/openapi.json", "/broker/v1/openapi.json",
	} {
		status, document := server.openAPI(path)
		if status != 200 || !document["openapi"] {
			t.Fatalf("%s: status=%d document=%v", path, status, document)
		}
		if path == "/admin/v1/openapi.json" &&
			(!document["LoginRequest"] || !document["adminLogin"] || !document["bearer"]) {
			t.Fatalf("admin OpenAPI contract missing: %v", document)
		}
		if path == "/v1/openapi.json" &&
			(!document["fundingHistory"] || !document["FundingView"] || !document["Idempotency-Key:header"]) {
			t.Fatalf("client OpenAPI contract missing: %v", document)
		}
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_lifecycle.rs:97
// test: outbox_runner_drains_then_exits_on_shutdown
func TestOutboxRunnerDrainsThenExitsOnShutdown(t *testing.T) {
	bus := newMessageBus()
	outbox := newOutboxFixture(bus)
	id := outbox.write("test.shutdown.created", []byte("bye"))
	if !outbox.shutdownRunner() {
		t.Fatal("outbox runner did not exit")
	}
	message, ok := bus.next()
	if !ok || message.ID != id {
		t.Fatalf("delivered = %#v, %v; want id %q", message, ok, id)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_migrate.rs:7
// test: migrate_creates_all_context_schemas
func TestMigrateCreatesAllContextSchemas(t *testing.T) {
	wantSchemas := []string{
		"audit", "brokerage", "identity", "ledger", "mirror",
		"outbox", "saga", "scheduling", "trading",
	}
	if got := migratedSchemas(); !reflect.DeepEqual(got, wantSchemas) {
		t.Fatalf("schemas = %v, want %v", got, wantSchemas)
	}
	wantIdentityTables := []string{
		"admins", "api_keys", "factors", "idempotency_keys", "jwks",
		"rbac_admin_roles", "rbac_policies", "rbac_role_parents", "rbac_roles",
		"sessions", "tenants", "users",
	}
	if got := identityTables(); !reflect.DeepEqual(got, wantIdentityTables) {
		t.Fatalf("identity tables = %v, want %v", got, wantIdentityTables)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_outbox_breaker.rs:25
// test: outbox_breaker_sheds_nonessential_and_exposes_metrics
func TestOutboxBreakerShedsNonessentialAndExposesMetrics(t *testing.T) {
	var breaker outboxBreaker
	if breaker.update(500) || breaker.update(1000) || breaker.update(4999) {
		t.Fatal("breaker shed below the critical threshold")
	}
	if !breaker.update(5000) || !breaker.update(2000) {
		t.Fatal("breaker did not preserve shedding hysteresis")
	}
	if breaker.update(500) {
		t.Fatal("breaker did not recover at the low-water mark")
	}
	breaker.update(5000)
	if err := breaker.publish("analytics.test.breaker"); err == nil {
		t.Fatal("nonessential publish succeeded while shedding")
	}
	for _, topic := range []string{"uzo.commands.test.breaker", "uzo.events.trading.order_filled"} {
		if err := breaker.publish(topic); err != nil {
			t.Fatalf("essential publish %q failed: %v", topic, err)
		}
	}
	breaker.backlog = 5
	if err := breaker.ready(2, 3); err == nil {
		t.Fatal("ready check accepted critical backlog")
	}
	metrics := breaker.metrics()
	for _, name := range []string{
		"outbox_pending_depth", "outbox_pending_by_topic", "transient_publish_failures_total",
	} {
		if !strings.Contains(metrics, name) {
			t.Errorf("metrics missing %q", name)
		}
	}
}
