package edge

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const maxRequestBodyBytes = 1 << 20

var plainDecimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
var canonicalUUID = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

// Server is the dependency-injected HTTP compatibility edge.
type Server struct {
	auth           Authenticator
	commands       CommandSubmitter
	realtime       RealtimeTokenIssuer
	identity       IdentityService
	trading        TradingReader
	brokerAccount  BrokerAccountReader
	brokerAccounts BrokerAccountLister
	brokerFills    BrokerFillsReader
	brokerFunding  BrokerFundingReader
	brokerBalances BrokerBalancesReader
	readiness      []HealthCheck
	openAPI        map[string][]byte
	allowOrigin    string
	trustedProxies []netip.Prefix
	requestID      func() string
}

// ServerConfig contains only externally observable edge configuration.
type ServerConfig struct {
	Authenticator  Authenticator
	Commands       CommandSubmitter
	Realtime       RealtimeTokenIssuer
	Identity       IdentityService
	Trading        TradingReader
	BrokerAccount  BrokerAccountReader
	BrokerAccounts BrokerAccountLister
	BrokerFills    BrokerFillsReader
	BrokerFunding  BrokerFundingReader
	BrokerBalances BrokerBalancesReader
	Readiness      []HealthCheck
	OpenAPI        map[string][]byte
	AllowOrigin    string
	TrustedProxies []netip.Prefix
	RequestID      func() string
}

// NewServer builds a standard-library HTTP handler.
func NewServer(config ServerConfig) *Server {
	origin := config.AllowOrigin
	if origin == "" {
		origin = "*"
	}
	requestID := config.RequestID
	if requestID == nil {
		requestID = newRequestID
	}
	return &Server{
		auth:           config.Authenticator,
		commands:       config.Commands,
		realtime:       config.Realtime,
		identity:       config.Identity,
		trading:        config.Trading,
		brokerAccount:  config.BrokerAccount,
		brokerAccounts: config.BrokerAccounts,
		brokerFills:    config.BrokerFills,
		brokerFunding:  config.BrokerFunding,
		brokerBalances: config.BrokerBalances,
		readiness:      append([]HealthCheck(nil), config.Readiness...),
		openAPI:        config.OpenAPI,
		allowOrigin:    origin,
		trustedProxies: append([]netip.Prefix(nil), config.TrustedProxies...),
		requestID:      requestID,
	}
}

// Handler returns the currently implemented Phase 3 HTTP compatibility slice.
func (server *Server) Handler() http.Handler {
	return server.harden(http.HandlerFunc(server.route))
}

func (server *Server) route(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodOptions {
		writer.Header().Set("access-control-allow-methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		writer.Header().Set("access-control-allow-headers", "Authorization,Content-Type,Idempotency-Key,X-API-Key")
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/healthz":
		writer.Header().Set("content-type", "text/plain; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("ok"))
	case request.Method == http.MethodGet && request.URL.Path == "/readyz":
		server.handleReadiness(writer, request)
	case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/openapi.json"):
		server.handleOpenAPI(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/auth/login":
		server.handleLogin(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/me/accounts":
		server.handleMyAccounts(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/v1/me/api-keys":
		server.handleCreateMyAPIKey(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/me":
		server.handleProfile(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/v1/instruments":
		server.handleInstruments(writer, request)
	case request.Method == http.MethodPost &&
		(request.URL.Path == "/v1/realtime/token" ||
			request.URL.Path == "/v1/me/realtime/token"):
		server.handleRealtimeToken(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/broker/v1/ping":
		server.handleBrokerPing(writer, request)
	case request.Method == http.MethodGet && request.URL.Path == "/broker/v1/accounts":
		server.handleBrokerAccounts(writer, request)
	case request.Method == http.MethodGet:
		if accountID, ok := brokerAccountRoute(request.URL.Path); ok {
			server.handleBrokerAccount(writer, request, accountID)
			return
		}
		if accountID, ok := brokerFillsRoute(request.URL.Path); ok {
			server.handleBrokerFills(writer, request, accountID)
			return
		}
		if accountID, ok := brokerFundingRoute(request.URL.Path); ok {
			server.handleBrokerFunding(writer, request, accountID)
			return
		}
		if accountID, ok := brokerBalancesRoute(request.URL.Path); ok {
			server.handleBrokerBalances(writer, request, accountID)
			return
		}
		if accountID, resource, ok := accountReadRoute(request.URL.Path); ok {
			server.handleAccountRead(writer, request, accountID, resource)
			return
		}
		writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
	case request.Method == http.MethodPost && request.URL.Path == "/broker/v1/echo":
		server.handleBrokerEcho(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/broker/v1/users":
		server.handleBrokerCreateUser(writer, request)
	case request.Method == http.MethodPost && request.URL.Path == "/broker/v1/accounts":
		server.handleBrokerCreateAccount(writer, request)
	case request.Method == http.MethodPost &&
		strings.HasPrefix(request.URL.Path, "/broker/v1/users/") &&
		strings.HasSuffix(request.URL.Path, "/token"):
		server.handleBrokerMintToken(writer, request)
	case request.Method == http.MethodPost:
		accountID, ok := orderSubmitAccount(request.URL.Path)
		if ok {
			server.handleSubmitOrder(writer, request, accountID)
			return
		}
		writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
	default:
		writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
	}
}

func (server *Server) handleLogin(writer http.ResponseWriter, request *http.Request) {
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	var body LoginRequest
	if err := decodeJSON(request.Body, &body); err != nil ||
		strings.TrimSpace(body.Login) == "" || body.Password == "" {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid login request")
		return
	}
	response, err := server.identity.Login(request.Context(), body)
	if errors.Is(err, ErrInvalidCredentials) {
		writeError(writer, request, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return
	}
	if err != nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleProfile(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.clientPrincipal(writer, request)
	if !ok {
		return
	}
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	response, err := server.identity.Profile(request.Context(), principal)
	if errors.Is(err, ErrNotFound) {
		writeError(writer, request, http.StatusNotFound, "not_found", "identity not found")
		return
	}
	if err != nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleMyAccounts(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.clientPrincipal(writer, request)
	if !ok {
		return
	}
	if server.identity == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"identity unavailable",
		)
		return
	}
	response, err := server.identity.MyAccounts(request.Context(), principal)
	if err != nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"identity unavailable",
		)
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleCreateMyAPIKey(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.authenticatedClientPrincipal(writer, request)
	if !ok {
		return
	}
	if server.identity == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"identity unavailable",
		)
		return
	}
	idempotencyKey := request.Header.Get("idempotency-key")
	if strings.TrimSpace(idempotencyKey) == "" {
		server.rejectInvalidAPIKeyRequest(
			writer,
			request,
			principal,
			"Idempotency-Key is required",
		)
		return
	}
	var body CreateAPIKeyRequest
	if err := decodeCompatibleJSON(request.Body, &body); err != nil ||
		!validOptionalTenantID(body.TenantID) {
		server.rejectInvalidAPIKeyRequest(
			writer,
			request,
			principal,
			"invalid API-key request",
		)
		return
	}
	response, err := server.identity.CreateMyAPIKey(
		request.Context(),
		principal,
		request.Header.Get("x-request-id"),
		idempotencyKey,
		body,
	)
	switch {
	case errors.Is(err, ErrInvalidRequest):
		server.rejectInvalidAPIKeyRequest(
			writer,
			request,
			principal,
			"invalid API-key request",
		)
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(
			writer,
			request,
			http.StatusConflict,
			"idempotency_conflict",
			"idempotency key conflicts with another request",
		)
	case errors.Is(err, ErrConflict):
		writeError(
			writer,
			request,
			http.StatusConflict,
			"conflict",
			"active API-key limit reached",
		)
	case errors.Is(err, ErrRateLimited):
		writeClientRateLimit(writer, request, err)
	case err != nil:
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"identity unavailable",
		)
	default:
		writeStoredResponse(writer, response.Response)
	}
}

func (server *Server) rejectInvalidAPIKeyRequest(
	writer http.ResponseWriter,
	request *http.Request,
	principal Principal,
	message string,
) {
	if err := server.identity.CheckClientRate(
		request.Context(),
		principal,
	); err != nil {
		if errors.Is(err, ErrRateLimited) {
			writeClientRateLimit(writer, request, err)
		} else {
			writeError(
				writer,
				request,
				http.StatusServiceUnavailable,
				"unavailable",
				"identity unavailable",
			)
		}
		return
	}
	writeError(
		writer,
		request,
		http.StatusBadRequest,
		"invalid_request",
		message,
	)
}

func (server *Server) handleInstruments(writer http.ResponseWriter, request *http.Request) {
	if server.trading == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "catalog unavailable")
		return
	}
	response, err := server.trading.Instruments(request.Context())
	if err != nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "catalog unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleAccountRead(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
	resource string,
) {
	principal, ok := server.clientPrincipal(writer, request)
	if !ok {
		return
	}
	if !strings.HasPrefix(accountID, "urn:xb:account:") {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid account id")
		return
	}
	if !principal.OwnsAccount(accountID) {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if server.trading == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "trading views unavailable")
		return
	}
	var response any
	var err error
	switch resource {
	case "orders":
		response, err = server.trading.Orders(request.Context(), accountID)
	case "positions":
		response, err = server.trading.Positions(request.Context(), accountID)
	case "balances":
		response, err = server.trading.Balances(request.Context(), accountID)
	case "fills":
		filter, parseErr := fillExecutionFilter(request)
		if parseErr != nil {
			writeError(
				writer,
				request,
				http.StatusBadRequest,
				"invalid_request",
				"invalid fills page",
			)
			return
		}
		var page FillExecutionPage
		page, err = server.trading.Fills(
			request.Context(),
			accountID,
			filter,
		)
		if page.Items == nil {
			page.Items = make([]FillExecutionView, 0)
		}
		response = page
	case "funding":
		params, parseErr := fundingPageParams(request)
		if parseErr != nil {
			writeError(
				writer,
				request,
				http.StatusBadRequest,
				"invalid_request",
				"invalid funding page",
			)
			return
		}
		response, err = server.trading.Funding(
			request.Context(),
			accountID,
			params,
		)
	default:
		writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return
	}
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) {
			message := "invalid funding page"
			if resource == "fills" {
				message = "invalid fills page"
			}
			writeError(
				writer,
				request,
				http.StatusBadRequest,
				"invalid_request",
				message,
			)
			return
		}
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "trading views unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (server *Server) handleBrokerCreateUser(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:write") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	var body BrokerUserRequest
	if err := decodeJSON(request.Body, &body); err != nil ||
		strings.TrimSpace(body.Login) == "" ||
		strings.TrimSpace(body.Email) == "" {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid user request")
		return
	}
	key, ok := server.brokerIdempotencyKey(writer, request)
	if !ok {
		return
	}
	response, err := server.identity.CreateBrokerUser(
		request.Context(), principal, key, body,
	)
	if errors.Is(err, ErrIdempotencyConflict) {
		writeError(writer, request, http.StatusConflict, "idempotency_conflict", err.Error())
		return
	}
	if err != nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	writeJSON(writer, http.StatusCreated, response)
}

func (server *Server) handleBrokerCreateAccount(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:write") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	var body BrokerAccountRequest
	if err := decodeJSON(request.Body, &body); err != nil ||
		!strings.HasPrefix(body.UserID, "urn:xb:user:") {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid account request")
		return
	}
	key, ok := server.brokerIdempotencyKey(writer, request)
	if !ok {
		return
	}
	response, err := server.identity.CreateBrokerAccount(
		request.Context(),
		principal,
		key,
		body,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(writer, request, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, ErrNotFound):
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid account request")
	case err != nil:
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
	default:
		writeStoredResponse(writer, response.Response)
	}
}

func (server *Server) handleBrokerMintToken(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("tokens:mint") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	userID := strings.TrimSuffix(
		strings.TrimPrefix(request.URL.Path, "/broker/v1/users/"),
		"/token",
	)
	if !strings.HasPrefix(userID, "urn:xb:user:") {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid user id")
		return
	}
	var body BrokerTokenRequest
	if err := decodeJSON(request.Body, &body); err != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid token request")
		return
	}
	response, err := server.identity.MintBrokerToken(
		request.Context(), principal, userID, body,
	)
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrInvalidRequest):
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid token request")
	case err != nil:
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
	default:
		writeJSON(writer, http.StatusOK, response)
	}
}

func (server *Server) harden(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestID := strings.TrimSpace(request.Header.Get("x-request-id"))
		if requestID == "" {
			requestID = strings.TrimSpace(server.requestID())
			if requestID == "" {
				requestID = "request-unavailable"
			}
		}
		request.Header.Set("x-request-id", requestID)
		writer.Header().Set("x-request-id", requestID)
		writer.Header().Set("x-content-type-options", "nosniff")
		writer.Header().Set("x-frame-options", "DENY")
		writer.Header().Set("referrer-policy", "no-referrer")
		writer.Header().Set("access-control-allow-origin", server.allowOrigin)
		next.ServeHTTP(writer, request)
	})
}

func (server *Server) handleReadiness(writer http.ResponseWriter, request *http.Request) {
	type component struct {
		Name    string `json:"name"`
		Healthy bool   `json:"healthy"`
	}
	response := struct {
		Ready      bool        `json:"ready"`
		Components []component `json:"components"`
	}{Ready: true, Components: make([]component, 0, len(server.readiness))}
	for _, check := range server.readiness {
		healthy := check.Check != nil && check.Check(request.Context()) == nil
		response.Components = append(response.Components, component{Name: check.Name, Healthy: healthy})
		response.Ready = response.Ready && healthy
	}
	status := http.StatusOK
	if !response.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(writer, status, response)
}

func (server *Server) handleOpenAPI(writer http.ResponseWriter, request *http.Request) {
	document, ok := server.openAPI[request.URL.Path]
	if !ok || !json.Valid(document) {
		writeError(writer, request, http.StatusNotFound, "not_found", "route not found")
		return
	}
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(document)
}

func (server *Server) handleSubmitOrder(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
) {
	principal, ok := server.clientPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.OwnsAccount(accountID) {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	if server.commands == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "command service unavailable")
		return
	}
	var body SubmitOrderRequest
	if err := decodeJSON(request.Body, &body); err != nil || ValidateSubmitOrder(body) != nil {
		writeError(writer, request, http.StatusBadRequest, "invalid_request", "invalid submit-order request")
		return
	}
	idempotencyKey := strings.TrimSpace(request.Header.Get("idempotency-key"))
	if idempotencyKey == "" {
		idempotencyKey = "intent:" + body.IntentID
	}
	accepted, err := server.commands.SubmitOrder(
		request.Context(),
		principal,
		accountID,
		idempotencyKey,
		body,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(writer, request, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, ErrForbidden):
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
	case err != nil:
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "command admission unavailable")
	default:
		writeStoredResponse(writer, accepted.Response)
	}
}

func (server *Server) handleRealtimeToken(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.clientPrincipal(writer, request)
	if !ok {
		return
	}
	if server.realtime == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "realtime unavailable")
		return
	}
	token, err := server.realtime.IssueClientToken(request.Context(), principal)
	if err != nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "realtime unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, token)
}

func (server *Server) handleBrokerPing(writer http.ResponseWriter, request *http.Request) {
	if _, ok := server.brokerPrincipal(writer, request); !ok {
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"ok": true})
}

func (server *Server) handleBrokerAccount(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	const accountPrefix = "urn:xb:account:"
	if !strings.HasPrefix(accountID, accountPrefix) ||
		!canonicalUUID.MatchString(strings.TrimPrefix(accountID, accountPrefix)) {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid account id",
		)
		return
	}
	if server.brokerAccount == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"account view unavailable",
		)
		return
	}
	account, err := server.brokerAccount.BrokerAccount(
		request.Context(),
		principal.Tenant,
		accountID,
	)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"unknown account",
		)
	case err != nil:
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"account view unavailable",
		)
	default:
		writeJSON(writer, http.StatusOK, account)
	}
}

func (server *Server) handleBrokerAccounts(
	writer http.ResponseWriter,
	request *http.Request,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	userID, err := brokerAccountListUserID(request)
	if err != nil {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid user id",
		)
		return
	}
	if server.brokerAccounts == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"account list unavailable",
		)
		return
	}
	accounts, err := server.brokerAccounts.BrokerAccounts(
		request.Context(),
		principal.Tenant,
		userID,
	)
	if err != nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"account list unavailable",
		)
		return
	}
	if accounts == nil {
		accounts = make([]MyAccountView, 0)
	}
	writeJSON(writer, http.StatusOK, accounts)
}

func (server *Server) handleBrokerFills(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	const accountPrefix = "urn:xb:account:"
	if !strings.HasPrefix(accountID, accountPrefix) ||
		!canonicalUUID.MatchString(strings.TrimPrefix(accountID, accountPrefix)) {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid account id",
		)
		return
	}
	filter, err := fillExecutionFilter(request)
	if err != nil {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid fills page",
		)
		return
	}
	if server.brokerFills == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
		return
	}
	page, err := server.brokerFills.BrokerFills(
		request.Context(),
		principal.Tenant,
		accountID,
		filter,
	)
	switch {
	case errors.Is(err, ErrForbidden):
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrInvalidRequest):
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid fills page",
		)
	case err != nil:
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
	default:
		if page.Items == nil {
			page.Items = make([]FillExecutionView, 0)
		}
		writeJSON(writer, http.StatusOK, page)
	}
}

func (server *Server) handleBrokerBalances(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	const accountPrefix = "urn:xb:account:"
	if !strings.HasPrefix(accountID, accountPrefix) ||
		!canonicalUUID.MatchString(strings.TrimPrefix(accountID, accountPrefix)) {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid account id",
		)
		return
	}
	if server.brokerBalances == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
		return
	}
	balances, err := server.brokerBalances.BrokerBalances(
		request.Context(),
		principal.Tenant,
		accountID,
	)
	switch {
	case errors.Is(err, ErrForbidden):
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
	case err != nil:
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
	default:
		if balances == nil {
			balances = make([]BalanceView, 0)
		}
		writeJSON(writer, http.StatusOK, balances)
	}
}

func (server *Server) handleBrokerFunding(
	writer http.ResponseWriter,
	request *http.Request,
	accountID string,
) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	const accountPrefix = "urn:xb:account:"
	if !strings.HasPrefix(accountID, accountPrefix) ||
		!canonicalUUID.MatchString(strings.TrimPrefix(accountID, accountPrefix)) {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid account id",
		)
		return
	}
	params, err := brokerFundingPageParams(request)
	if err != nil {
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid funding page",
		)
		return
	}
	if server.brokerFunding == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
		return
	}
	page, err := server.brokerFunding.BrokerFunding(
		request.Context(),
		principal.Tenant,
		accountID,
		params,
	)
	switch {
	case errors.Is(err, ErrForbidden):
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
	case errors.Is(err, ErrInvalidRequest):
		writeError(
			writer,
			request,
			http.StatusBadRequest,
			"invalid_request",
			"invalid funding page",
		)
	case err != nil:
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"trading views unavailable",
		)
	default:
		if page.Items == nil {
			page.Items = make([]FundingView, 0)
		}
		writeJSON(writer, http.StatusOK, page)
	}
}

func (server *Server) handleBrokerEcho(writer http.ResponseWriter, request *http.Request) {
	principal, ok := server.brokerPrincipal(writer, request)
	if !ok {
		return
	}
	if !principal.HasScope("accounts:read") {
		writeError(writer, request, http.StatusForbidden, "forbidden", "forbidden")
		return
	}
	key, ok := server.brokerIdempotencyKey(writer, request)
	if !ok {
		return
	}
	if server.identity == nil {
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
		return
	}
	response, err := server.identity.BrokerEcho(
		request.Context(),
		principal,
		key,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		writeError(writer, request, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, ErrRateLimited):
		var rateLimit RateLimitError
		if errors.As(err, &rateLimit) {
			slog.Info(
				"broker echo capacity limited",
				"capacity_scope", rateLimit.CapacityScope,
				"retry_after_seconds", rateLimit.RetryAfterSeconds,
			)
		}
		writeClientRateLimit(writer, request, err)
	case err != nil:
		writeError(writer, request, http.StatusServiceUnavailable, "unavailable", "identity unavailable")
	default:
		writeStoredResponse(writer, response)
	}
}

func (server *Server) clientPrincipal(
	writer http.ResponseWriter,
	request *http.Request,
) (Principal, bool) {
	principal, ok := server.authenticatedClientPrincipal(writer, request)
	if !ok {
		return Principal{}, false
	}
	if server.identity == nil {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"identity unavailable",
		)
		return Principal{}, false
	}
	if err := server.identity.CheckClientRate(
		request.Context(),
		principal,
	); err != nil {
		if errors.Is(err, ErrRateLimited) {
			writeClientRateLimit(writer, request, err)
		} else {
			writeError(
				writer,
				request,
				http.StatusServiceUnavailable,
				"unavailable",
				"identity unavailable",
			)
		}
		return Principal{}, false
	}
	return principal, true
}

func (server *Server) brokerIdempotencyKey(
	writer http.ResponseWriter,
	request *http.Request,
) (string, bool) {
	key := strings.TrimSpace(request.Header.Get("idempotency-key"))
	if key != "" {
		return key, true
	}
	key = strings.TrimSpace(server.requestID())
	if key == "" {
		writeError(
			writer,
			request,
			http.StatusServiceUnavailable,
			"unavailable",
			"request identity unavailable",
		)
		return "", false
	}
	return key, true
}

func (server *Server) authenticatedClientPrincipal(
	writer http.ResponseWriter,
	request *http.Request,
) (Principal, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(request.Header.Get("authorization"), "Bearer "))
	if server.auth == nil || raw == "" {
		writeError(writer, request, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return Principal{}, false
	}
	principal, err := server.auth.AuthenticateClient(request.Context(), raw)
	if err != nil || principal.Audience != AudienceClient {
		writeError(writer, request, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return Principal{}, false
	}
	return principal, true
}

func (server *Server) brokerPrincipal(
	writer http.ResponseWriter,
	request *http.Request,
) (Principal, bool) {
	raw := strings.TrimSpace(request.Header.Get("x-api-key"))
	sourceIP := clientIP(request, server.trustedProxies)
	if server.auth == nil || raw == "" {
		writeError(writer, request, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return Principal{}, false
	}
	principal, err := server.auth.AuthenticateBroker(request.Context(), raw, sourceIP)
	if err != nil || principal.Audience != AudienceBroker {
		writeError(writer, request, http.StatusUnauthorized, "unauthorized", "unauthorized")
		return Principal{}, false
	}
	return principal, true
}

func orderSubmitAccount(path string) (string, bool) {
	const prefix = "/v1/accounts/"
	const suffix = "/orders"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	accountID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if accountID == "" || strings.Contains(accountID, "/") {
		return "", false
	}
	return accountID, true
}

func accountReadRoute(path string) (string, string, bool) {
	const prefix = "/v1/accounts/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(path, prefix)
	for _, resource := range []string{"orders", "positions", "balances", "fills", "funding"} {
		suffix := "/" + resource
		if strings.HasSuffix(remainder, suffix) {
			accountID := strings.TrimSuffix(remainder, suffix)
			return accountID, resource, accountID != ""
		}
	}
	return "", "", false
}

func brokerFillsRoute(path string) (string, bool) {
	const prefix = "/broker/v1/accounts/"
	const suffix = "/fills"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	accountID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if accountID == "" || strings.Contains(accountID, "/") {
		return "", false
	}
	return accountID, true
}

func brokerAccountRoute(path string) (string, bool) {
	const prefix = "/broker/v1/accounts/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	accountID := strings.TrimPrefix(path, prefix)
	if accountID == "" || strings.Contains(accountID, "/") {
		return "", false
	}
	return accountID, true
}

func brokerBalancesRoute(path string) (string, bool) {
	const prefix = "/broker/v1/accounts/"
	const suffix = "/balances"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	accountID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if accountID == "" || strings.Contains(accountID, "/") {
		return "", false
	}
	return accountID, true
}

func brokerFundingRoute(path string) (string, bool) {
	const prefix = "/broker/v1/accounts/"
	const suffix = "/funding"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return "", false
	}
	accountID := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	if accountID == "" || strings.Contains(accountID, "/") {
		return "", false
	}
	return accountID, true
}

func brokerAccountListUserID(request *http.Request) (*string, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	entries, ok := values["userId"]
	if !ok {
		return nil, nil
	}
	if len(entries) != 1 {
		return nil, errors.New("userId must appear once")
	}
	const userPrefix = "urn:xb:user:"
	userID := entries[0]
	if !strings.HasPrefix(userID, userPrefix) ||
		!canonicalUUID.MatchString(strings.TrimPrefix(userID, userPrefix)) {
		return nil, errors.New("userId is not canonical")
	}
	return &userID, nil
}

func fillExecutionFilter(request *http.Request) (FillExecutionFilter, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return FillExecutionFilter{}, err
	}
	for key, entries := range values {
		switch key {
		case "side", "tradeId", "limit", "cursor", "direction":
		default:
			return FillExecutionFilter{}, ErrInvalidRequest
		}
		if len(entries) != 1 {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
		if entries[0] == "" {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
	}
	one := func(key string) string {
		entries := values[key]
		if len(entries) == 0 {
			return ""
		}
		return entries[0]
	}
	filter := FillExecutionFilter{
		Side:      strings.TrimSpace(one("side")),
		TradeID:   strings.TrimSpace(one("tradeId")),
		Cursor:    one("cursor"),
		Direction: one("direction"),
	}
	if _, present := values["side"]; present && filter.Side == "" {
		return FillExecutionFilter{}, ErrInvalidRequest
	}
	if _, present := values["tradeId"]; present && filter.TradeID == "" {
		return FillExecutionFilter{}, ErrInvalidRequest
	}
	if filter.Side != "" {
		side := strings.ToUpper(filter.Side)
		if side != "BUY" && side != "SELL" {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
		filter.Side = side
	}
	if filter.TradeID != "" && !canonicalUUID.MatchString(filter.TradeID) {
		return FillExecutionFilter{}, ErrInvalidRequest
	}
	if filter.Cursor != "" {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if decodeErr != nil {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
		parts := strings.SplitN(string(raw), ":", 2)
		if len(parts) != 2 || !canonicalUUID.MatchString(parts[1]) {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
		if _, parseErr := strconv.ParseInt(parts[0], 10, 64); parseErr != nil {
			return FillExecutionFilter{}, ErrInvalidRequest
		}
	}
	switch filter.Direction {
	case "", "next", "prev", "backward":
	default:
		return FillExecutionFilter{}, ErrInvalidRequest
	}
	rawLimit := one("limit")
	if rawLimit == "" {
		return filter, nil
	}
	limit, err := strconv.ParseInt(rawLimit, 10, 32)
	if err != nil || limit < 1 || limit > 200 {
		return FillExecutionFilter{}, ErrInvalidRequest
	}
	filter.Limit = int(limit)
	return filter, nil
}

func fundingPageParams(request *http.Request) (PageParams, error) {
	params := PageParams{
		Cursor:    request.URL.Query().Get("cursor"),
		Direction: request.URL.Query().Get("direction"),
	}
	rawLimit := request.URL.Query().Get("limit")
	if rawLimit == "" {
		return params, nil
	}
	limit, err := strconv.ParseInt(rawLimit, 10, 32)
	if err != nil {
		return PageParams{}, err
	}
	params.Limit = int(limit)
	return params, nil
}

func brokerFundingPageParams(request *http.Request) (PageParams, error) {
	params, err := fundingPageParams(request)
	if err != nil {
		return PageParams{}, err
	}
	if params.Cursor == "" {
		return params, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(params.Cursor)
	if err != nil {
		return PageParams{}, ErrInvalidRequest
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return PageParams{}, ErrInvalidRequest
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil ||
		!canonicalUUID.MatchString(parts[1]) {
		return PageParams{}, ErrInvalidRequest
	}
	return params, nil
}

func decodeJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxRequestBodyBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func decodeCompatibleJSON(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxRequestBodyBytes+1))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func validOptionalTenantID(value *string) bool {
	if value == nil {
		return true
	}
	const prefix = "urn:xb:tenant:"
	body := strings.TrimPrefix(*value, prefix)
	if body == *value || body == "" {
		return false
	}
	decoded := new(big.Int)
	base := big.NewInt(62)
	for _, character := range []byte(body) {
		var digit int64
		switch {
		case character >= '0' && character <= '9':
			digit = int64(character - '0')
		case character >= 'A' && character <= 'Z':
			digit = int64(character-'A') + 10
		case character >= 'a' && character <= 'z':
			digit = int64(character-'a') + 36
		default:
			return false
		}
		decoded.Mul(decoded, base)
		decoded.Add(decoded, big.NewInt(digit))
		if decoded.BitLen() > 128 {
			return false
		}
	}
	return true
}

// ValidateSubmitOrder validates transport-level shape without performing
// instrument-specific economic checks.
func ValidateSubmitOrder(request SubmitOrderRequest) error {
	if request.IntentID == "" || request.Symbol == "" || request.Quantity == "" {
		return errors.New("required field missing")
	}
	switch request.Side {
	case "BUY", "SELL", "buy", "sell":
	default:
		return errors.New("invalid side")
	}
	switch request.Type {
	case "", "MARKET", "LIMIT", "STOP_MARKET", "STOP_LIMIT",
		"TAKE_PROFIT_MARKET", "TAKE_PROFIT_LIMIT", "TRAILING_STOP_MARKET":
	default:
		return errors.New("invalid type")
	}
	for _, value := range []*string{&request.Quantity, request.Price, request.TriggerPrice, request.TrailingOffset} {
		if value != nil && !plainDecimal.MatchString(*value) {
			return errors.New("invalid decimal")
		}
	}
	return nil
}

func clientIP(request *http.Request, trustedProxies []netip.Prefix) string {
	peer, err := parseSourceAddress(request.RemoteAddr)
	if err != nil || !addressInPrefixes(peer, trustedProxies) {
		return strings.TrimSpace(request.RemoteAddr)
	}
	values := request.Header.Values("x-forwarded-for")
	if len(values) == 0 {
		return peer.String()
	}
	var chain []netip.Addr
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			address, err := parseSourceAddress(item)
			if err != nil {
				return ""
			}
			chain = append(chain, address)
		}
	}
	if len(chain) == 0 {
		return ""
	}
	for index := len(chain) - 1; index >= 0; index-- {
		if !addressInPrefixes(chain[index], trustedProxies) {
			return chain[index].String()
		}
	}
	return chain[0].String()
}

func addressInPrefixes(address netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func newRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(raw[:])
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeStoredResponse(writer http.ResponseWriter, response StoredResponse) {
	var headers map[string][]string
	if response.Status < 100 ||
		response.Status > 599 ||
		json.Unmarshal(response.Headers, &headers) != nil {
		writeJSON(writer, http.StatusInternalServerError, struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{
			Code: "invalid_stored_response", Message: "invalid stored response",
		})
		return
	}
	for name, values := range headers {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	writer.WriteHeader(response.Status)
	_, _ = writer.Write(response.Body)
}

func writeClientRateLimit(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	var rateLimit RateLimitError
	retryAfter := uint64(0)
	if errors.As(err, &rateLimit) {
		retryAfter = rateLimit.RetryAfterSeconds
	}
	if retryAfter > 0 {
		writer.Header().Set("retry-after", strconv.FormatUint(retryAfter, 10))
	}
	writeError(
		writer,
		request,
		http.StatusTooManyRequests,
		"too_many_requests",
		"rate_limited",
	)
}

func writeError(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	code string,
	message string,
) {
	writeJSON(writer, status, struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId,omitempty"`
	}{
		Code:      code,
		Message:   message,
		RequestID: request.Header.Get("x-request-id"),
	})
}
