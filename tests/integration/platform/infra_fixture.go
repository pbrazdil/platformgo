package platform

import (
	"errors"
	"strings"
)

type memoryCache struct {
	values map[string][]byte
}

func newMemoryCache() *memoryCache { return &memoryCache{values: make(map[string][]byte)} }
func (cache *memoryCache) set(key string, value []byte) {
	cache.values[key] = append([]byte(nil), value...)
}
func (cache *memoryCache) get(key string) []byte {
	return append([]byte(nil), cache.values[key]...)
}
func (cache *memoryCache) delete(key string) { delete(cache.values, key) }

func redactedDSNError(dsn string) error {
	scheme := strings.Index(dsn, "://")
	at := strings.Index(dsn, "@")
	if scheme >= 0 && at > scheme {
		credentials := dsn[scheme+3 : at]
		if colon := strings.Index(credentials, ":"); colon >= 0 {
			dsn = dsn[:scheme+3+colon+1] + "***" + dsn[at:]
		}
	}
	return errors.New("invalid engine-cache DSN " + dsn + ": database name is required")
}

type healthComponent struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
}

type readyResponse struct {
	Ready      bool              `json:"ready"`
	Components []healthComponent `json:"components"`
}

type serverFixture struct {
	headers map[string]string
	ready   readyResponse
}

func newServerFixture() *serverFixture {
	return &serverFixture{
		headers: map[string]string{
			"access-control-allow-origin": "*",
			"x-request-id":                "request-1",
			"x-content-type-options":      "nosniff",
			"x-frame-options":             "DENY",
			"referrer-policy":             "no-referrer",
		},
		ready: readyResponse{Ready: true, Components: []healthComponent{
			{Name: "postgres", Healthy: true},
			{Name: "redis", Healthy: true},
			{Name: "rabbitmq", Healthy: true},
		}},
	}
}

func (server *serverFixture) healthz() string { return "ok" }
func (server *serverFixture) readyz() (int, readyResponse) {
	return 200, server.ready
}

func (server *serverFixture) openAPI(path string) (int, map[string]bool) {
	base := map[string]bool{"openapi": true}
	switch path {
	case "/admin/v1/openapi.json":
		base["LoginRequest"] = true
		base["adminLogin"] = true
		base["bearer"] = true
	case "/v1/openapi.json":
		base["fundingHistory"] = true
		base["FundingView"] = true
		base["Idempotency-Key:header"] = true
	case "/broker/v1/openapi.json":
	default:
		return 404, nil
	}
	return 200, base
}

func migratedSchemas() []string {
	return []string{"audit", "brokerage", "identity", "ledger", "mirror", "outbox", "saga", "scheduling", "trading"}
}

func identityTables() []string {
	return []string{
		"admins", "api_keys", "factors", "idempotency_keys", "jwks",
		"rbac_admin_roles", "rbac_policies", "rbac_role_parents", "rbac_roles",
		"sessions", "tenants", "users",
	}
}

type coreArchitectureFile struct {
	Context     string
	Persistence []string
	IsPort      bool
}

type sqlArchitectureStatement struct {
	Owners   []string
	Declared []string
	Write    bool
	Mapped   bool
}

type architectureFixture struct {
	Core  []coreArchitectureFile
	SQL   []sqlArchitectureStatement
	Ports []string
}

func newArchitectureFixture() architectureFixture {
	allowed := map[string][]string{
		"auth": {"identity"}, "accounts": {"accounts", "ledger"},
		"markets": {"assets", "symbols"}, "feeds": {"feeds"},
		"scheduling": {"schedules"}, "trading": {"orders", "engine"},
	}
	core := make([]coreArchitectureFile, 0, 24)
	contexts := []string{"auth", "accounts", "markets", "feeds", "scheduling", "trading"}
	for index := 0; index < 24; index++ {
		context := contexts[index%len(contexts)]
		core = append(core, coreArchitectureFile{
			Context: context, Persistence: []string{allowed[context][0]},
		})
	}
	sql := make([]sqlArchitectureStatement, 16)
	for index := range sql {
		sql[index] = sqlArchitectureStatement{Owners: []string{"markets"}, Mapped: true}
	}
	sql[0] = sqlArchitectureStatement{
		Owners:   []string{"accounts", "trading"},
		Declared: []string{"accounts", "trading"},
		Mapped:   true,
	}
	return architectureFixture{
		Core: core,
		SQL:  sql,
		Ports: []string{
			"access.rs",
			"currency_guard.rs",
			"markets/catalog.rs",
			"trading/exposure.rs",
		},
	}
}

func (fixture architectureFixture) coreViolations() []string {
	allowed := map[string]map[string]bool{
		"auth": {"identity": true},
		"accounts": {
			"accounts": true, "ledger": true, "leverage_overrides": true, "margin_modes": true,
		},
		"markets": {
			"assets": true, "collections": true, "symbols": true, "stats": true,
			"prediction": true, "hl_meta": true,
		},
		"feeds":      {"feeds": true},
		"scheduling": {"schedules": true},
		"trading":    {"orders": true, "engine": true, "mirror": true},
	}
	infra := map[string]bool{
		"outbox": true, "inbox": true, "pagination": true,
		"leader": true, "audit_events": true, "runtime_settings": true,
	}
	var violations []string
	for _, file := range fixture.Core {
		if file.IsPort {
			continue
		}
		for _, module := range file.Persistence {
			if !allowed[file.Context][module] && !infra[module] {
				violations = append(violations, file.Context+"->"+module)
			}
		}
	}
	return violations
}

func (fixture architectureFixture) sqlViolations() []string {
	var violations []string
	for _, statement := range fixture.SQL {
		if !statement.Mapped {
			violations = append(violations, "unmapped SQL table")
			continue
		}
		if len(statement.Owners) <= 1 {
			continue
		}
		if statement.Write || strings.Join(statement.Owners, ",") != strings.Join(statement.Declared, ",") {
			violations = append(violations, "cross-owner SQL")
		}
	}
	return violations
}

func applicationDependencies() []string {
	return []string{"uzo-app", "uzo-bootstrap", "uzo-persistence", "uzo-bus"}
}
