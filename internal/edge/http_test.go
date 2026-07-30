package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type testAuthenticator struct{}

func (testAuthenticator) AuthenticateClient(
	_ context.Context,
	token string,
) (Principal, error) {
	if token != "client-token" {
		return Principal{}, ErrUnauthorized
	}
	return Principal{
		Subject:  "urn:xb:user:user-7",
		Audience: AudienceClient,
		Accounts: []string{"urn:xb:account:acct-7"},
	}, nil
}

func (testAuthenticator) AuthenticateBroker(
	_ context.Context,
	token string,
	sourceIP string,
) (Principal, error) {
	if !strings.HasPrefix(sourceIP, "203.0.113.7") {
		return Principal{}, ErrUnauthorized
	}
	var scopes []string
	switch token {
	case "broker-key":
		scopes = []string{"accounts:read"}
	case "broker-noscope":
		scopes = []string{"users:write"}
	case "broker-wildcard":
		scopes = []string{"*"}
	case "broker-full":
		scopes = []string{"accounts:read", "accounts:write", "tokens:mint"}
	case "broker-other":
		return Principal{
			Subject:  "urn:xb:apikey:broker-2",
			Tenant:   "urn:xb:tenant:other",
			Audience: AudienceBroker,
			Scopes:   []string{"accounts:read"},
		}, nil
	default:
		return Principal{}, ErrUnauthorized
	}
	return Principal{
		Subject:  "urn:xb:apikey:broker-1",
		Tenant:   "urn:xb:tenant:broker-1",
		Audience: AudienceBroker,
		Scopes:   scopes,
	}, nil
}

type testCommands struct {
	mu       sync.Mutex
	requests map[string][]byte
	results  map[string]OrderAccepted
}

func (commands *testCommands) SubmitOrder(
	_ context.Context,
	principal Principal,
	accountID string,
	key string,
	request SubmitOrderRequest,
) (OrderAdmission, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	if commands.requests == nil {
		commands.requests = make(map[string][]byte)
		commands.results = make(map[string]OrderAccepted)
	}
	scope := principal.Subject + "\x00" + accountID + "\x00" + key
	canonical, _ := json.Marshal(request)
	if previous, exists := commands.requests[scope]; exists {
		if !bytes.Equal(previous, canonical) {
			return OrderAdmission{}, ErrIdempotencyConflict
		}
		return testOrderAdmission(commands.results[scope]), nil
	}
	result := OrderAccepted{
		OrderID:  "00000000-0000-4000-8000-000000000007",
		IntentID: request.IntentID,
	}
	commands.requests[scope] = canonical
	commands.results[scope] = result
	return testOrderAdmission(result), nil
}

func testOrderAdmission(result OrderAccepted) OrderAdmission {
	body, _ := json.Marshal(result)
	return OrderAdmission{
		OrderAccepted: result,
		Response: StoredResponse{
			Status:  http.StatusAccepted,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    append(body, '\n'),
		},
	}
}

type testRealtime struct{}

func (testRealtime) IssueClientToken(
	_ context.Context,
	principal Principal,
) (RealtimeToken, error) {
	return RealtimeToken{
		Token:    "token-for-" + principal.Subject,
		Channels: []string{"user:" + principal.Subject},
	}, nil
}

type testIdentity struct{}

func (testIdentity) Login(
	_ context.Context,
	request LoginRequest,
) (LoginResponse, error) {
	if request.Login != "trader1" || request.Password != "correct horse battery staple" {
		return LoginResponse{}, ErrInvalidCredentials
	}
	return LoginResponse{
		AccessToken: "client-token", RefreshToken: "refresh-token",
	}, nil
}

func (testIdentity) Profile(
	_ context.Context,
	principal Principal,
) (UserProfile, error) {
	return UserProfile{
		UserID: principal.Subject, Login: "crm-trader-1",
		Email: "trader1@crm.example", Status: "active",
	}, nil
}

func (testIdentity) MyAccounts(
	_ context.Context,
	principal Principal,
) ([]MyAccountView, error) {
	return []MyAccountView{{
		AccountID: "urn:xb:account:test",
		UserID:    principal.Subject,
		Status:    "active",
	}}, nil
}

func (testIdentity) CheckClientRate(
	_ context.Context,
	_ Principal,
) error {
	return nil
}

func (testIdentity) CreateMyAPIKey(
	_ context.Context,
	_ Principal,
	_ string,
	_ string,
	_ CreateAPIKeyRequest,
) (APIKeyAdmission, error) {
	response := APIKeyCreated{
		ID:     "urn:xb:apikey:00000000-0000-4000-8000-000000000001",
		Prefix: "000000000001",
		Token:  "xbk_000000000001.secret",
	}
	body, _ := json.Marshal(response)
	return APIKeyAdmission{Response: StoredResponse{
		Status:  http.StatusCreated,
		Headers: []byte(`{"Content-Type":["application/json"]}`),
		Body:    append(body, '\n'),
	}}, nil
}

type rateLimitedIdentity struct {
	testIdentity
	createCalls int
	rateCalls   int
	allowRate   bool
}

type brokerEchoCountingIdentity struct {
	testIdentity
	calls int
}

func (identity *brokerEchoCountingIdentity) BrokerEcho(
	ctx context.Context,
	principal Principal,
	key string,
) (StoredResponse, error) {
	identity.calls++
	return identity.testIdentity.BrokerEcho(ctx, principal, key)
}

func (identity *rateLimitedIdentity) CheckClientRate(
	_ context.Context,
	_ Principal,
) error {
	identity.rateCalls++
	if identity.allowRate {
		return nil
	}
	return ErrRateLimited
}

func (identity *rateLimitedIdentity) CreateMyAPIKey(
	_ context.Context,
	_ Principal,
	_ string,
	_ string,
	_ CreateAPIKeyRequest,
) (APIKeyAdmission, error) {
	identity.createCalls++
	return APIKeyAdmission{}, RateLimitError{RetryAfterSeconds: 60}
}

func (testIdentity) BrokerEcho(
	_ context.Context,
	principal Principal,
	key string,
) (StoredResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"id": principal.Subject + ":" + key,
	})
	return StoredResponse{
		Status:  http.StatusOK,
		Headers: []byte(`{"Content-Type":["application/json"]}`),
		Body:    append(body, '\n'),
	}, nil
}

func (testIdentity) CreateBrokerUser(
	_ context.Context,
	_ Principal,
	_ string,
	request BrokerUserRequest,
) (BrokerUserResult, error) {
	return BrokerUserResult{
		ID: "urn:xb:user:" + strings.ToLower(request.Login), Created: true,
	}, nil
}

func (testIdentity) CreateBrokerAccount(
	_ context.Context,
	_ Principal,
	_ string,
	request BrokerAccountRequest,
) (BrokerAccountAdmission, error) {
	result := BrokerAccountResult{
		ID:    "urn:xb:account:00000000-0000-4000-8000-000000000009",
		Login: 9, UserID: request.UserID, BaseCurrency: "USDC",
		MarketVenue:      "HYPERLIQUID",
		PermittedClasses: []string{"CRYPTOCURRENCY"},
		CreatedAt:        "2026-07-25T00:00:00Z",
	}
	body, _ := json.Marshal(result)
	return BrokerAccountAdmission{
		BrokerAccountResult: result,
		Response: StoredResponse{
			Status:  http.StatusCreated,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    append(body, '\n'),
		},
	}, nil
}

func (testIdentity) MintBrokerToken(
	_ context.Context,
	_ Principal,
	_ string,
	request BrokerTokenRequest,
) (BrokerTokenResponse, error) {
	ttl := uint64(900)
	if request.TTLSeconds != nil {
		ttl = *request.TTLSeconds
	}
	return BrokerTokenResponse{
		AccessToken: "client-token", ExpiresInSecs: ttl,
	}, nil
}

type testTradingReader struct{}

func (testTradingReader) Instruments(context.Context) ([]InstrumentView, error) {
	return []InstrumentView{{Symbol: "BTC-PERP", Enabled: true}}, nil
}

func (testTradingReader) Orders(
	_ context.Context,
	accountID string,
) ([]OrderView, error) {
	return []OrderView{{
		OrderID:  "urn:xb:order:00000000-0000-4000-8000-000000000007",
		IntentID: "intent-7", Symbol: "BTC-PERP", Status: "pending",
		AccountID: accountID,
	}}, nil
}

func (testTradingReader) Positions(context.Context, string) ([]PositionView, error) {
	return []PositionView{}, nil
}

func (testTradingReader) Balances(context.Context, string) ([]BalanceView, error) {
	return []BalanceView{{Currency: "USDC", Total: "1000", Free: "1000", Equity: "1000"}}, nil
}

func (testTradingReader) Fills(
	context.Context,
	string,
	FillExecutionFilter,
) (FillExecutionPage, error) {
	return FillExecutionPage{
		Items: []FillExecutionView{{
			FillID:     "019fa844-26c0-7000-8000-000000000103",
			OrderID:    "urn:xb:order:019fa844-26c0-7000-8000-000000000111",
			PositionID: "019fa844-26c0-7000-8000-000000000131",
			Side:       "BUY",
			TradeType:  "open",
			Reason:     "manual",
			FilledAt:   "2026-07-24T10:00:00Z",
		}},
		Total: 1,
	}, nil
}

func (testTradingReader) Funding(
	_ context.Context,
	_ string,
	params PageParams,
) (FundingPage, error) {
	cursor := "next-funding"
	total := int64(1)
	return FundingPage{
		Items: []FundingView{{
			FundingID:              "019f9b6d-3154-4db1-b639-57c246e92403",
			Symbol:                 "BTC-PERP",
			PositionID:             "706f732d31",
			PositionSignedQuantity: "1",
			OraclePrice:            "1000",
			FundingRate:            "0.0000125",
			FundingAmount:          "-2",
			Currency:               "USDC",
			FundingTime:            "2026-07-26T00:58:20Z",
		}},
		NextCursor: &cursor,
		Total:      &total,
	}, nil
}

func newTestServer(commands CommandSubmitter) *Server {
	return NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Commands:      commands,
		Realtime:      testRealtime{},
		Identity:      testIdentity{},
		Trading:       testTradingReader{},
		Readiness: []HealthCheck{
			{Name: "postgres", Check: func(context.Context) error { return nil }},
			{Name: "redis", Check: func(context.Context) error { return nil }},
			{Name: "rabbitmq", Check: func(context.Context) error { return nil }},
		},
		OpenAPI: map[string][]byte{
			"/admin/v1/openapi.json":  []byte(`{"openapi":"3.1.0"}`),
			"/v1/openapi.json":        []byte(`{"openapi":"3.1.0"}`),
			"/broker/v1/openapi.json": []byte(`{"openapi":"3.1.0"}`),
		},
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.0/24"),
		},
	})
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_health.rs:9
// test: readyz_reports_all_dependencies_healthy
func TestReadyzReportsAllDependenciesHealthyOverHTTP(t *testing.T) {
	response := performRequest(t, newTestServer(&testCommands{}).Handler(), http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body struct {
		Ready      bool `json:"ready"`
		Components []struct {
			Name    string `json:"name"`
			Healthy bool   `json:"healthy"`
		} `json:"components"`
	}
	decodeResponse(t, response, &body)
	if !body.Ready {
		t.Fatal("ready = false")
	}
	var names []string
	for _, component := range body.Components {
		if !component.Healthy {
			t.Fatalf("component %q unhealthy", component.Name)
		}
		names = append(names, component.Name)
	}
	if !reflect.DeepEqual(names, []string{"postgres", "redis", "rabbitmq"}) {
		t.Fatalf("components = %v", names)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_hardening.rs:9
// test: cors_preflight_and_request_id
func TestCORSPreflightAndRequestHardening(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	preflight := performRequest(t, handler, http.MethodOptions, "/v1/auth/login", nil, map[string]string{
		"origin":                        "https://terminal.example.com",
		"access-control-request-method": "POST",
	})
	if preflight.Header().Get("access-control-allow-origin") == "" {
		t.Fatal("preflight missing access-control-allow-origin")
	}
	response := performRequest(t, handler, http.MethodGet, "/healthz", nil, nil)
	want := map[string]string{
		"x-content-type-options": "nosniff",
		"x-frame-options":        "DENY",
		"referrer-policy":        "no-referrer",
	}
	for name, expected := range want {
		if got := response.Header().Get(name); got != expected {
			t.Errorf("%s = %q, want %q", name, got, expected)
		}
	}
	if response.Header().Get("x-request-id") == "" {
		t.Fatal("response missing x-request-id")
	}
}

func TestAPIKeyRateLimitResponseComesFromAtomicCredentialAdmission(t *testing.T) {
	identity := &rateLimitedIdentity{}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      identity,
	}).Handler()
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/v1/me/api-keys",
		[]byte(`{"name":"blocked"}`),
		map[string]string{
			"authorization":   "Bearer client-token",
			"content-type":    "application/json",
			"idempotency-key": "blocked-key",
		},
	)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf(
			"rate-limit status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	if identity.createCalls != 1 {
		t.Fatalf("credential admission calls = %d, want 1", identity.createCalls)
	}
	if got := response.Header().Get("retry-after"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	if !strings.Contains(response.Body.String(), `"code":"too_many_requests"`) {
		t.Fatalf("unexpected rate-limit body: %s", response.Body.String())
	}
}

func TestAPIKeyRequiresIdempotencyBeforeRequestDecoding(t *testing.T) {
	identity := &rateLimitedIdentity{allowRate: true}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      identity,
	}).Handler()
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/v1/me/api-keys",
		[]byte(`not-json`),
		map[string]string{
			"authorization": "Bearer client-token",
			"content-type":  "application/json",
		},
	)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(
			response.Body.String(),
			`"message":"Idempotency-Key is required"`,
		) {
		t.Fatalf(
			"missing-idempotency response = %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	if identity.createCalls != 0 {
		t.Fatalf(
			"credential admission calls = %d, want 0",
			identity.createCalls,
		)
	}
	if identity.rateCalls != 1 {
		t.Fatalf(
			"protected-client rate calls = %d, want 1",
			identity.rateCalls,
		)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_lifecycle.rs:14
// test: server_boots_rest_openapi_via_real_composition
func TestOpenAPIDocumentsAreServed(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	for _, path := range []string{
		"/admin/v1/openapi.json",
		"/v1/openapi.json",
		"/broker/v1/openapi.json",
	} {
		response := performRequest(t, handler, http.MethodGet, path, nil, nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.Code)
		}
		var document map[string]any
		decodeResponse(t, response, &document)
		if document["openapi"] == nil {
			t.Fatalf("%s missing openapi version", path)
		}
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/trading/e2e_rest.rs:10
// test: trader_trading_flow_transport
func TestSubmitOrderAuthenticatesOwnsAndReplaysExactly(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	instruments := performRequest(t, handler, http.MethodGet, "/v1/instruments", nil, nil)
	if instruments.Code != http.StatusOK ||
		!strings.Contains(instruments.Body.String(), "BTC-PERP") {
		t.Fatalf("instrument response = %d %s", instruments.Code, instruments.Body.String())
	}
	login := performRequest(
		t,
		handler,
		http.MethodPost,
		"/v1/auth/login",
		[]byte(`{"login":"trader1","password":"correct horse battery staple"}`),
		map[string]string{"content-type": "application/json"},
	)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", login.Code, login.Body.String())
	}
	body := []byte(`{"intentId":"intent-7","symbol":"BTC-PERP","side":"BUY","type":"LIMIT","quantity":"1.250","price":"100.50"}`)
	headers := map[string]string{
		"authorization":   "Bearer client-token",
		"idempotency-key": "idem-7",
		"content-type":    "application/json",
	}
	first := performRequest(t, handler, http.MethodPost, "/v1/accounts/urn:xb:account:acct-7/orders", body, headers)
	second := performRequest(t, handler, http.MethodPost, "/v1/accounts/urn:xb:account:acct-7/orders", body, headers)
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted {
		t.Fatalf("statuses = %d, %d; want 202", first.Code, second.Code)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body differs:\n%s\n%s", first.Body.String(), second.Body.String())
	}
	var accepted OrderAccepted
	decodeResponse(t, first, &accepted)
	if accepted.IntentID != "intent-7" || accepted.OrderID == "" {
		t.Fatalf("accepted = %#v", accepted)
	}
	for _, resource := range []string{"orders", "positions", "balances", "funding"} {
		response := performRequest(
			t,
			handler,
			http.MethodGet,
			"/v1/accounts/urn:xb:account:acct-7/"+resource,
			nil,
			headers,
		)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d body=%s", resource, response.Code, response.Body.String())
		}
		if resource == "orders" && !strings.Contains(response.Body.String(), `"intentId":"intent-7"`) {
			t.Fatalf("orders = %s", response.Body.String())
		}
		if resource == "positions" && strings.TrimSpace(response.Body.String()) != "[]" {
			t.Fatalf("positions = %s", response.Body.String())
		}
		if resource == "balances" && !strings.Contains(response.Body.String(), `"currency":"USDC"`) {
			t.Fatalf("balances = %s", response.Body.String())
		}
		if resource == "funding" &&
			(!strings.Contains(response.Body.String(), `"fundingAmount":"-2"`) ||
				!strings.Contains(response.Body.String(), `"total":1`)) {
			t.Fatalf("funding = %s", response.Body.String())
		}
	}
	invalidFundingPage := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:acct-7/funding?limit=not-a-number",
		nil,
		headers,
	)
	if invalidFundingPage.Code != http.StatusBadRequest {
		t.Fatalf(
			"invalid funding page status = %d, want 400",
			invalidFundingPage.Code,
		)
	}

	unauthenticated := performRequest(t, handler, http.MethodPost, "/v1/accounts/urn:xb:account:acct-7/orders", body, nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthenticated.Code)
	}
	forbidden := performRequest(t, handler, http.MethodPost, "/v1/accounts/urn:xb:account:other/orders", body, headers)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("foreign account status = %d, want 403", forbidden.Code)
	}
}

func TestFillsRouteIsAuthenticatedAndAccountScoped(t *testing.T) {
	reader := &recordingFillsReader{}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      testIdentity{},
		Trading:       reader,
	}).Handler()
	headers := map[string]string{"authorization": "Bearer client-token"}

	owned := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:acct-7/fills?limit=2&side=buy",
		nil,
		headers,
	)
	if owned.Code != http.StatusOK {
		t.Fatalf("owned fills status = %d body=%s", owned.Code, owned.Body.String())
	}
	const wantBody = `{"items":[{"fillId":"019fa844-26c0-7000-8000-000000000103","orderId":"urn:xb:order:019fa844-26c0-7000-8000-000000000111","positionId":"019fa844-26c0-7000-8000-000000000131","side":"BUY","tradeType":"open","reason":"manual","realizedPnl":null,"settlementCurrency":null,"filledAt":"2026-07-24T10:00:00Z"}],"total":1}` + "\n"
	if owned.Body.String() != wantBody {
		t.Fatalf("owned fills body = %s, want exact %s", owned.Body.String(), wantBody)
	}
	if reader.calls != 1 ||
		reader.accountID != "urn:xb:account:acct-7" ||
		reader.filter.Side != "BUY" ||
		reader.filter.Limit != 2 {
		t.Fatalf(
			"fills read calls=%d account=%q filter=%#v",
			reader.calls,
			reader.accountID,
			reader.filter,
		)
	}

	anonymous := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:acct-7/fills",
		nil,
		nil,
	)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf(
			"anonymous fills status = %d, want 401",
			anonymous.Code,
		)
	}
	if reader.calls != 1 {
		t.Fatalf("anonymous request reached fills reader: calls=%d", reader.calls)
	}

	foreign := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:other/fills",
		nil,
		headers,
	)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("foreign fills status = %d, want 403", foreign.Code)
	}
	if reader.calls != 1 {
		t.Fatalf("foreign request reached fills reader: calls=%d", reader.calls)
	}

	malformed := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/not-a-urn/fills",
		nil,
		headers,
	)
	if malformed.Code != http.StatusBadRequest {
		t.Fatalf("malformed fills status = %d, want 400", malformed.Code)
	}
	if reader.calls != 1 {
		t.Fatalf("malformed request reached fills reader: calls=%d", reader.calls)
	}
}

type recordingFillsReader struct {
	testTradingReader
	calls     int
	accountID string
	filter    FillExecutionFilter
}

func (reader *recordingFillsReader) Fills(
	ctx context.Context,
	accountID string,
	filter FillExecutionFilter,
) (FillExecutionPage, error) {
	reader.calls++
	reader.accountID = accountID
	reader.filter = filter
	return reader.testTradingReader.Fills(ctx, accountID, filter)
}

func TestFillsRouteRejectsMalformedQueriesBeforeRead(t *testing.T) {
	reader := &recordingFillsReader{}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      testIdentity{},
		Trading:       reader,
	}).Handler()
	headers := map[string]string{"authorization": "Bearer client-token"}
	validCursor := "MTc4NDkwMTYwMDAwMDAwMDEwMDowMTlmYTg0NC0yNmMwLTcwMDAtODAwMC0wMDAwMDAwMDAxMDM"
	validTradeID := "019fa844-26c0-7000-8000-000000000103"

	for _, rawQuery := range []string{
		"unknown=value",
		"side=buy&side=sell",
		"side=",
		"side=%20",
		"side=hold",
		"tradeId=",
		"tradeId=not-a-uuid",
		"limit=",
		"limit=zero",
		"limit=0",
		"limit=-1",
		"limit=201",
		"limit=999999999999999999999",
		"cursor=not-base64!",
		"cursor=MTIz",
		"direction=",
		"direction=sideways",
		"side=%zz",
	} {
		response := performRequest(
			t,
			handler,
			http.MethodGet,
			"/v1/accounts/urn:xb:account:acct-7/fills?"+rawQuery,
			nil,
			headers,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"query %q status=%d body=%s, want 400",
				rawQuery,
				response.Code,
				response.Body.String(),
			)
		}
		if reader.calls != 0 {
			t.Fatalf("query %q reached fills reader", rawQuery)
		}
	}

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:acct-7/fills?tradeId="+
			validTradeID+"&cursor="+validCursor+"&direction=backward&limit=200",
		nil,
		headers,
	)
	if response.Code != http.StatusOK || reader.calls != 1 {
		t.Fatalf(
			"valid query status=%d calls=%d body=%s",
			response.Code,
			reader.calls,
			response.Body.String(),
		)
	}
	if reader.filter.TradeID != validTradeID ||
		reader.filter.Cursor != validCursor ||
		reader.filter.Direction != "backward" ||
		reader.filter.Limit != 200 {
		t.Fatalf("valid query filter = %#v", reader.filter)
	}
}

type emptyFillsReader struct{ testTradingReader }

func (emptyFillsReader) Fills(
	context.Context,
	string,
	FillExecutionFilter,
) (FillExecutionPage, error) {
	return FillExecutionPage{}, nil
}

type recordingBrokerFillsReader struct {
	calls     int
	tenant    string
	accountID string
	filter    FillExecutionFilter
	page      FillExecutionPage
	err       error
}

func (reader *recordingBrokerFillsReader) BrokerFills(
	_ context.Context,
	tenant string,
	accountID string,
	filter FillExecutionFilter,
) (FillExecutionPage, error) {
	reader.calls++
	reader.tenant = tenant
	reader.accountID = accountID
	reader.filter = filter
	return reader.page, reader.err
}

func TestBrokerFillsRouteFreezesGateOrderAndTenantAuthority(t *testing.T) {
	reader := &recordingBrokerFillsReader{
		page: FillExecutionPage{Items: []FillExecutionView{}, Total: 0},
	}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		BrokerFills:   reader,
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.0/24"),
		},
		RequestID: func() string { return "broker-fills-gate" },
	}).Handler()
	const path = "/broker/v1/accounts/urn:xb:account:00000000-0000-4000-8000-000000000009/fills?limit=bad"

	for _, test := range []struct {
		name   string
		key    string
		status int
		body   string
	}{
		{
			name: "invalid credential dominates malformed query",
			key:  "invalid", status: http.StatusUnauthorized,
			body: `{"code":"unauthorized","message":"unauthorized","requestId":"broker-fills-gate"}` + "\n",
		},
		{
			name: "scope dominates malformed query",
			key:  "broker-noscope", status: http.StatusForbidden,
			body: `{"code":"forbidden","message":"forbidden","requestId":"broker-fills-gate"}` + "\n",
		},
		{
			name: "valid scope reaches parsing",
			key:  "broker-key", status: http.StatusBadRequest,
			body: `{"code":"invalid_request","message":"invalid fills page","requestId":"broker-fills-gate"}` + "\n",
		},
		{
			name: "wildcard scope reaches parsing",
			key:  "broker-wildcard", status: http.StatusBadRequest,
			body: `{"code":"invalid_request","message":"invalid fills page","requestId":"broker-fills-gate"}` + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := performRequest(
				t,
				handler,
				http.MethodGet,
				path,
				nil,
				map[string]string{
					"x-api-key":       test.key,
					"x-forwarded-for": "203.0.113.7",
				},
			)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf(
					"status = %d body=%q, want status=%d body=%q",
					response.Code,
					response.Body.String(),
					test.status,
					test.body,
				)
			}
			if reader.calls != 0 {
				t.Fatalf("reader calls = %d, want 0", reader.calls)
			}
		})
	}

	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/broker/v1/accounts/urn:xb:account:00000000-0000-4000-8000-000000000009/fills?side=buy&limit=2",
		nil,
		map[string]string{
			"x-api-key":       "broker-key",
			"x-forwarded-for": "203.0.113.7",
		},
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s, want 200", response.Code, response.Body.String())
	}
	if reader.calls != 1 {
		t.Fatalf("reader calls = %d, want 1", reader.calls)
	}
	if reader.tenant != "urn:xb:tenant:broker-1" {
		t.Fatalf("tenant = %q, want authenticated tenant", reader.tenant)
	}
	if reader.accountID != "urn:xb:account:00000000-0000-4000-8000-000000000009" {
		t.Fatalf("account = %q, want canonical account URN", reader.accountID)
	}
	if reader.filter.Side != "BUY" || reader.filter.Limit != 2 {
		t.Fatalf("filter = %#v, want canonical side and limit", reader.filter)
	}
}

func TestBrokerFillsRouteRejectsNoncanonicalAccountIDBeforeRead(t *testing.T) {
	reader := &recordingBrokerFillsReader{}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		BrokerFills:   reader,
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.0/24"),
		},
	}).Handler()
	headers := map[string]string{
		"x-api-key":       "broker-key",
		"x-forwarded-for": "203.0.113.7",
	}
	for _, accountID := range []string{
		"00000000-0000-4000-8000-000000000009",
		"urn:xb:account:00000000-0000-4000-8000-00000000000A",
		"not-a-uuid",
	} {
		response := performRequest(
			t,
			handler,
			http.MethodGet,
			"/broker/v1/accounts/"+accountID+"/fills",
			nil,
			headers,
		)
		if response.Code != http.StatusBadRequest {
			t.Fatalf(
				"account %q status=%d body=%s, want 400",
				accountID,
				response.Code,
				response.Body.String(),
			)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d, want 0", reader.calls)
	}
}

func TestBrokerFillsRouteMapsAuthorizationAndStorageErrorsWithoutPartialPage(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{
			name:   "tenant denial",
			err:    ErrForbidden,
			status: http.StatusForbidden,
			body: `{"code":"forbidden","message":"forbidden","requestId":"broker-fills-request"}` +
				"\n",
		},
		{
			name:   "storage failure",
			err:    errors.New("contains-sensitive-fill-id"),
			status: http.StatusServiceUnavailable,
			body: `{"code":"unavailable","message":"trading views unavailable","requestId":"broker-fills-request"}` +
				"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingBrokerFillsReader{
				page: FillExecutionPage{
					Items: []FillExecutionView{{
						FillID: "must-not-leak",
					}},
					Total: 99,
				},
				err: test.err,
			}
			handler := NewServer(ServerConfig{
				Authenticator: testAuthenticator{},
				BrokerFills:   reader,
				TrustedProxies: []netip.Prefix{
					netip.MustParsePrefix("192.0.2.0/24"),
				},
				RequestID: func() string { return "broker-fills-request" },
			}).Handler()
			response := performRequest(
				t,
				handler,
				http.MethodGet,
				"/broker/v1/accounts/urn:xb:account:00000000-0000-4000-8000-000000000009/fills",
				nil,
				map[string]string{
					"x-api-key":       "broker-key",
					"x-forwarded-for": "203.0.113.7",
				},
			)
			if response.Code != test.status || response.Body.String() != test.body {
				t.Fatalf(
					"response status=%d body=%q, want status=%d body=%q",
					response.Code,
					response.Body.String(),
					test.status,
					test.body,
				)
			}
			if strings.Contains(response.Body.String(), "must-not-leak") ||
				strings.Contains(response.Body.String(), "99") ||
				strings.Contains(response.Body.String(), "sensitive") {
				t.Fatalf("response leaked partial page or storage detail: %s", response.Body.String())
			}
		})
	}
}

func TestFillsRouteReturnsNonNullEmptyItems(t *testing.T) {
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      testIdentity{},
		Trading:       emptyFillsReader{},
	}).Handler()
	response := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/accounts/urn:xb:account:acct-7/fills",
		nil,
		map[string]string{"authorization": "Bearer client-token"},
	)
	const want = `{"items":[],"total":0}` + "\n"
	if response.Code != http.StatusOK || response.Body.String() != want {
		t.Fatalf(
			"empty fills status=%d body=%s, want %s",
			response.Code,
			response.Body.String(),
			want,
		)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/identity/e2e_broker.rs:343
// test: broker_token_exchange_on_behalf_of
func TestBrokerTokenExchangeOnBehalfOfOverHTTP(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	headers := map[string]string{
		"x-api-key":       "broker-full",
		"x-forwarded-for": "203.0.113.7",
		"content-type":    "application/json",
	}
	created := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/users",
		[]byte(`{"login":"crm-trader-1","email":"trader1@crm.example"}`),
		headers,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("create user = %d %s", created.Code, created.Body.String())
	}
	ttl := uint64(120)
	minted := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/users/urn:xb:user:crm-trader-1/token",
		[]byte(`{"ttlSecs":120}`),
		headers,
	)
	var token BrokerTokenResponse
	decodeResponse(t, minted, &token)
	if minted.Code != http.StatusOK ||
		token.ExpiresInSecs != ttl ||
		token.AccessToken == "" {
		t.Fatalf("minted = %d %#v", minted.Code, token)
	}
	me := performRequest(
		t,
		handler,
		http.MethodGet,
		"/v1/me",
		nil,
		map[string]string{"authorization": "Bearer " + token.AccessToken},
	)
	if me.Code != http.StatusOK ||
		!strings.Contains(me.Body.String(), `"login":"crm-trader-1"`) {
		t.Fatalf("me = %d %s", me.Code, me.Body.String())
	}
	denied := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/users/urn:xb:user:crm-trader-1/token",
		[]byte(`{}`),
		map[string]string{
			"x-api-key": "broker-key", "x-forwarded-for": "203.0.113.7",
		},
	)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("no-mint status = %d", denied.Code)
	}
}

func TestSubmitOrderRejectsIdempotencyConflictAndInvalidDecimal(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	headers := map[string]string{
		"authorization":   "Bearer client-token",
		"idempotency-key": "same-key",
	}
	path := "/v1/accounts/urn:xb:account:acct-7/orders"
	first := []byte(`{"intentId":"intent-7","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"1"}`)
	if response := performRequest(t, handler, http.MethodPost, path, first, headers); response.Code != http.StatusAccepted {
		t.Fatalf("first status = %d", response.Code)
	}
	changed := []byte(`{"intentId":"intent-7","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"2"}`)
	if response := performRequest(t, handler, http.MethodPost, path, changed, headers); response.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409", response.Code)
	}
	invalid := []byte(`{"intentId":"intent-8","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"1e3"}`)
	if response := performRequest(t, handler, http.MethodPost, path, invalid, headers); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid decimal status = %d, want 400", response.Code)
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/identity/e2e_broker.rs:26
// test: api_key_auth_plus_idempotency_replay
func TestBrokerAPIKeyAndPrincipalScopedEchoReplay(t *testing.T) {
	handler := newTestServer(&testCommands{}).Handler()
	headers := map[string]string{
		"x-api-key":       "broker-key",
		"x-forwarded-for": "203.0.113.7",
	}
	if response := performRequest(t, handler, http.MethodGet, "/broker/v1/ping", nil, headers); response.Code != http.StatusOK {
		t.Fatalf("authenticated ping = %d", response.Code)
	}
	if response := performRequest(t, handler, http.MethodGet, "/broker/v1/ping", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous ping = %d, want 401", response.Code)
	}
	headers["idempotency-key"] = "k1"
	first := performRequest(t, handler, http.MethodPost, "/broker/v1/echo", nil, headers)
	second := performRequest(t, handler, http.MethodPost, "/broker/v1/echo", nil, headers)
	if first.Body.String() != second.Body.String() {
		t.Fatalf("echo replay differs: %s != %s", first.Body.String(), second.Body.String())
	}
	otherHeaders := map[string]string{
		"x-api-key":       "broker-other",
		"x-forwarded-for": "203.0.113.7",
		"idempotency-key": "k1",
	}
	other := performRequest(
		t, handler, http.MethodPost, "/broker/v1/echo", nil, otherHeaders,
	)
	if other.Code != http.StatusOK || other.Body.String() == first.Body.String() {
		t.Fatalf(
			"principal-scoped echo status=%d first=%s other=%s",
			other.Code,
			first.Body.String(),
			other.Body.String(),
		)
	}
	accountBody := []byte(`{"userId":"urn:xb:user:user-7"}`)
	readOnly := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/accounts",
		accountBody,
		headers,
	)
	if readOnly.Code != http.StatusForbidden {
		t.Fatalf("read-only account provision = %d, want 403", readOnly.Code)
	}
	fullHeaders := map[string]string{
		"x-api-key":       "broker-full",
		"x-forwarded-for": "203.0.113.7",
		"idempotency-key": "account-1",
	}
	created := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/accounts",
		accountBody,
		fullHeaders,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("full-scope account provision = %d, want 201", created.Code)
	}
}

func TestBrokerKeylessEchoFailsClosedWithoutGeneratedIdentity(t *testing.T) {
	identity := &brokerEchoCountingIdentity{}
	handler := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		Identity:      identity,
		RequestID:     func() string { return "" },
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("192.0.2.0/24"),
		},
	}).Handler()
	headers := map[string]string{
		"x-api-key":       "broker-key",
		"x-forwarded-for": "203.0.113.7",
		"x-request-id":    "transport-attempt",
	}
	for attempt := 0; attempt < 2; attempt++ {
		response := performRequest(
			t,
			handler,
			http.MethodPost,
			"/broker/v1/echo",
			nil,
			headers,
		)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf(
				"keyless attempt %d status = %d, want 503",
				attempt+1,
				response.Code,
			)
		}
	}
	if identity.calls != 0 {
		t.Fatalf("keyless entropy failure reached identity %d times", identity.calls)
	}

	headers["idempotency-key"] = "caller-owned-key"
	response := performRequest(
		t,
		handler,
		http.MethodPost,
		"/broker/v1/echo",
		nil,
		headers,
	)
	if response.Code != http.StatusOK || identity.calls != 1 {
		t.Fatalf(
			"explicit key status=%d identity calls=%d",
			response.Code,
			identity.calls,
		)
	}
}

func TestBrokerIPAllowlistTrustsForwardingOnlyFromConfiguredProxies(t *testing.T) {
	server := NewServer(ServerConfig{
		Authenticator: testAuthenticator{},
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
		},
	})
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/broker/v1/ping",
		nil,
	)
	request.Header.Set("x-api-key", "broker-key")
	request.Header.Set("x-forwarded-for", "203.0.113.7")
	request.RemoteAddr = "198.51.100.9:4000"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("spoofed forwarded IP status = %d, want 401", response.Code)
	}

	request = httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/broker/v1/ping",
		nil,
	)
	request.Header.Set("x-api-key", "broker-key")
	request.Header.Set("x-forwarded-for", "203.0.113.7")
	request.RemoteAddr = "10.0.0.5:4000"
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("trusted proxy status = %d, want 200", response.Code)
	}
}

func TestClientIPUsesRightmostUntrustedHopAndRejectsMalformedChains(t *testing.T) {
	trusted := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("2001:db8:ffff::/48"),
	}
	tests := []struct {
		name       string
		remoteAddr string
		headers    []string
		want       string
	}{
		{
			name:       "untrusted peer ignores spoof",
			remoteAddr: "198.51.100.9:4000",
			headers:    []string{"203.0.113.7"},
			want:       "198.51.100.9:4000",
		},
		{
			name:       "trusted single proxy",
			remoteAddr: "10.0.0.5:4000",
			headers:    []string{"203.0.113.7"},
			want:       "203.0.113.7",
		},
		{
			name:       "trusted multi hop",
			remoteAddr: "10.0.0.5:4000",
			headers: []string{
				"192.0.2.99, 203.0.113.7",
				"10.2.0.8",
			},
			want: "203.0.113.7",
		},
		{
			name:       "trusted ipv6 proxy",
			remoteAddr: "[2001:db8:ffff::5]:4000",
			headers:    []string{"2001:db8::7"},
			want:       "2001:db8::7",
		},
		{
			name:       "malformed trusted chain",
			remoteAddr: "10.0.0.5:4000",
			headers:    []string{"203.0.113.7, malformed"},
			want:       "",
		},
		{
			name:       "empty trusted header",
			remoteAddr: "10.0.0.5:4000",
			headers:    []string{""},
			want:       "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(
				context.Background(),
				http.MethodGet,
				"/",
				nil,
			)
			request.RemoteAddr = test.remoteAddr
			request.Header.Del("x-forwarded-for")
			for _, value := range test.headers {
				request.Header.Add("x-forwarded-for", value)
			}
			if got := clientIP(request, trusted); got != test.want {
				t.Fatalf("client IP = %q, want %q", got, test.want)
			}
		})
	}
}

func TestUnhealthyDependencyMakesReadinessFailClosed(t *testing.T) {
	server := newTestServer(&testCommands{})
	server.readiness[1].Check = func(context.Context) error { return errors.New("down") }
	response := performRequest(t, server.Handler(), http.MethodGet, "/readyz", nil, nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func performRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body []byte,
	headers map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(
		context.Background(),
		method,
		path,
		bytes.NewReader(body),
	)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func FuzzSubmitOrderDecoder(f *testing.F) {
	f.Add(`{"intentId":"i","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"1"}`)
	f.Add(`{"intentId":"i","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"NaN"}`)
	handler := newTestServer(&testCommands{}).Handler()
	f.Fuzz(func(t *testing.T, body string) {
		request := httptest.NewRequestWithContext(
			context.Background(),
			http.MethodPost,
			"/v1/accounts/urn:xb:account:acct-7/orders",
			strings.NewReader(body),
		)
		request.Header.Set("authorization", "Bearer client-token")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code >= 500 {
			t.Fatalf("decoder returned server failure %d for %q", response.Code, body)
		}
	})
}
