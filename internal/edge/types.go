package edge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Audience identifies the externally visible authentication plane.
type Audience string

const (
	AudienceClient Audience = "client"
	AudienceBroker Audience = "broker"
	AudienceAdmin  Audience = "admin"
)

var (
	ErrUnauthorized        = errors.New("unauthorized")
	ErrForbidden           = errors.New("forbidden")
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("conflict")
	ErrRateLimited         = errors.New("rate limited")
	ErrIdempotencyConflict = errors.New("idempotency key conflicts with another request")
)

// RateLimitError preserves the externally visible Retry-After value while
// remaining comparable with ErrRateLimited through errors.Is.
type RateLimitError struct {
	RetryAfterSeconds uint64
}

func (err RateLimitError) Error() string {
	return fmt.Sprintf("rate limited; retry after %d seconds", err.RetryAfterSeconds)
}

func (RateLimitError) Unwrap() error {
	return ErrRateLimited
}

// Principal is the authenticated identity passed to application services.
type Principal struct {
	Subject  string
	Tenant   string
	Audience Audience
	Scopes   []string
	Accounts []string
}

// HasScope reports whether a principal has a named or wildcard capability.
func (principal Principal) HasScope(scope string) bool {
	for _, granted := range principal.Scopes {
		if granted == "*" || granted == scope {
			return true
		}
	}
	return false
}

// OwnsAccount reports whether a client principal may act on an account.
func (principal Principal) OwnsAccount(accountID string) bool {
	for _, owned := range principal.Accounts {
		if owned == "*" || owned == accountID {
			return true
		}
	}
	return false
}

// Authenticator verifies credentials without coupling the HTTP edge to their
// persistence or signing implementation.
type Authenticator interface {
	AuthenticateClient(context.Context, string) (Principal, error)
	AuthenticateBroker(context.Context, string, string) (Principal, error)
}

// SubmitOrderRequest is the frozen client JSON request shape.
type SubmitOrderRequest struct {
	IntentID       string  `json:"intentId"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"`
	Type           string  `json:"type"`
	Quantity       string  `json:"quantity"`
	Price          *string `json:"price,omitempty"`
	TriggerPrice   *string `json:"triggerPrice,omitempty"`
	TrailingOffset *string `json:"trailingOffset,omitempty"`
	ReduceOnly     bool    `json:"reduceOnly"`
	TimeInForce    *string `json:"timeInForce,omitempty"`
	MaxSlippageBPS *uint32 `json:"maxSlippageBps,omitempty"`
}

// OrderAccepted is returned after the command is durably admitted.
type OrderAccepted struct {
	OrderID  string `json:"orderId"`
	IntentID string `json:"intentId"`
}

// StoredResponse is the immutable HTTP response emitted for a durable
// mutation. Body includes the exact wire bytes, including its final newline.
type StoredResponse struct {
	Status  int
	Headers []byte
	Body    []byte
}

// OrderAdmission carries both the typed gRPC result and exact HTTP response.
type OrderAdmission struct {
	OrderAccepted
	Response StoredResponse
}

// CommandSubmitter owns durable idempotency and command admission.
type CommandSubmitter interface {
	SubmitOrder(
		context.Context,
		Principal,
		string,
		string,
		SubmitOrderRequest,
	) (OrderAdmission, error)
}

// RealtimeToken is the frozen realtime-token response shape.
type RealtimeToken struct {
	Token    string   `json:"token"`
	Channels []string `json:"channels"`
}

// RealtimeTokenIssuer creates a connection token scoped to one principal.
type RealtimeTokenIssuer interface {
	IssueClientToken(context.Context, Principal) (RealtimeToken, error)
}

// HealthCheck is one runtime dependency readiness probe.
type HealthCheck struct {
	Name  string
	Check func(context.Context) error
}

// LoginRequest is the frozen client password-login request.
type LoginRequest struct {
	Login    string  `json:"login"`
	Password string  `json:"password"`
	OTP      *string `json:"otp,omitempty"`
}

// LoginResponse returns independently rotatable access and refresh tokens.
type LoginResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
}

// CreateAPIKeyRequest is the client self-service credential request.
type CreateAPIKeyRequest struct {
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	IPAllowlist []string `json:"ipAllowlist"`
	TTLSeconds  *uint64  `json:"ttlSecs,omitempty"`
	TenantID    *string  `json:"tenantId,omitempty"`
}

// UnmarshalJSON preserves the pinned Serde behavior: additive fields are
// ignored, omitted vectors default empty, and explicit null vectors are
// rejected.
func (request *CreateAPIKeyRequest) UnmarshalJSON(data []byte) error {
	type wireRequest CreateAPIKeyRequest
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, name := range []string{"scopes", "ipAllowlist"} {
		if raw, ok := fields[name]; ok &&
			bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("%s must be an array", name)
		}
	}
	var decoded wireRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*request = CreateAPIKeyRequest(decoded)
	return nil
}

// APIKeyCreated returns metadata plus the credential shown exactly once.
type APIKeyCreated struct {
	ID     string `json:"id"`
	Prefix string `json:"prefix"`
	Token  string `json:"token"`
}

// APIKeyAdmission is the exact immutable HTTP response for credential
// creation or replay.
type APIKeyAdmission struct {
	Response StoredResponse
}

// UserProfile is the client-visible identity projection.
type UserProfile struct {
	UserID string `json:"userId"`
	Login  string `json:"login"`
	Email  string `json:"email"`
	Status string `json:"status"`
}

// MyAccountView is the frozen client-visible account summary.
type MyAccountView struct {
	AccountID        string   `json:"accountId"`
	Login            int64    `json:"login"`
	UserID           string   `json:"userId"`
	BaseCurrency     string   `json:"baseCurrency"`
	MarginMode       string   `json:"marginMode"`
	OmsMode          string   `json:"omsMode"`
	MarketVenue      string   `json:"marketVenue"`
	PermittedClasses []string `json:"permittedClasses"`
	Status           string   `json:"status"`
	CreatedAt        string   `json:"createdAt"`
}

// BrokerUserRequest is the broker identity-convergence request.
type BrokerUserRequest struct {
	Login string `json:"login"`
	Email string `json:"email"`
}

// BrokerUserResult reports whether this request created the identity.
type BrokerUserResult struct {
	ID      string `json:"id"`
	Created bool   `json:"created"`
}

// BrokerAccountRequest is the frozen broker provisioning request.
type BrokerAccountRequest struct {
	UserID       string  `json:"userId"`
	BaseCurrency *string `json:"baseCurrency,omitempty"`
	Venue        *string `json:"venue,omitempty"`
}

// BrokerAccountResult is the frozen broker-visible account projection.
type BrokerAccountResult struct {
	ID               string   `json:"id"`
	Login            int64    `json:"login"`
	UserID           string   `json:"userId"`
	BaseCurrency     string   `json:"baseCurrency"`
	MarketVenue      string   `json:"marketVenue"`
	PermittedClasses []string `json:"permittedClasses"`
	CreatedAt        string   `json:"createdAt"`
}

// BrokerAccountAdmission carries the typed account and exact HTTP response.
type BrokerAccountAdmission struct {
	BrokerAccountResult
	Response StoredResponse
}

// BrokerTokenRequest controls the bounded delegated client token lifetime.
type BrokerTokenRequest struct {
	TTLSeconds *uint64 `json:"ttlSecs,omitempty"`
}

// BrokerTokenResponse is a delegated client access token.
type BrokerTokenResponse struct {
	AccessToken   string `json:"accessToken"`
	ExpiresInSecs uint64 `json:"expiresInSecs"`
}

// IdentityService owns password login, identity reads, and broker delegation.
type IdentityService interface {
	Login(context.Context, LoginRequest) (LoginResponse, error)
	Profile(context.Context, Principal) (UserProfile, error)
	MyAccounts(context.Context, Principal) ([]MyAccountView, error)
	CheckClientRate(context.Context, Principal) error
	CreateMyAPIKey(
		context.Context,
		Principal,
		string,
		string,
		CreateAPIKeyRequest,
	) (APIKeyAdmission, error)
	BrokerEcho(
		context.Context,
		Principal,
		string,
	) (string, error)
	CreateBrokerUser(
		context.Context,
		Principal,
		string,
		BrokerUserRequest,
	) (BrokerUserResult, error)
	CreateBrokerAccount(
		context.Context,
		Principal,
		string,
		BrokerAccountRequest,
	) (BrokerAccountAdmission, error)
	MintBrokerToken(
		context.Context,
		Principal,
		string,
		BrokerTokenRequest,
	) (BrokerTokenResponse, error)
}

// InstrumentView is the stable public catalog subset required by the trading
// transport contract.
type InstrumentView struct {
	Symbol          string `json:"symbol"`
	DisplayName     string `json:"displayName"`
	SettlementAsset string `json:"settlementAsset"`
	PriceIncrement  string `json:"priceIncrement"`
	SizeIncrement   string `json:"sizeIncrement"`
	MaxLeverage     string `json:"maxLeverage"`
	MakerFee        string `json:"makerFee"`
	TakerFee        string `json:"takerFee"`
	Enabled         bool   `json:"enabled"`
}

// OrderView is the stable client order projection.
type OrderView struct {
	OrderID        string  `json:"orderId"`
	IntentID       string  `json:"intentId"`
	Symbol         string  `json:"symbol"`
	Side           string  `json:"side"`
	Type           string  `json:"type"`
	Quantity       string  `json:"quantity"`
	Status         string  `json:"status"`
	FilledQuantity string  `json:"filledQuantity"`
	LimitPrice     *string `json:"limitPrice,omitempty"`
	TriggerPrice   *string `json:"triggerPrice,omitempty"`
	TimeInForce    *string `json:"timeInForce,omitempty"`
	ReduceOnly     bool    `json:"reduceOnly"`
	AccountID      string  `json:"accountId"`
}

// PositionView is the stable client position projection subset.
type PositionView struct {
	PositionID string `json:"positionId"`
	Symbol     string `json:"symbol"`
	Side       string `json:"side"`
	Quantity   string `json:"quantity"`
	Status     string `json:"status"`
	AccountID  string `json:"accountId"`
}

// BalanceView is the stable exact client balance projection.
type BalanceView struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Locked   string `json:"locked"`
	Free     string `json:"free"`
	Equity   string `json:"equity"`
}

// PageParams is the frozen keyset-pagination query shape.
type PageParams struct {
	Limit     int
	Cursor    string
	Direction string
}

// FillExecutionView is the narrow immutable execution-time projection proven
// by the first native fill-history source port.
type FillExecutionView struct {
	FillID      string  `json:"fillId"`
	OrderID     string  `json:"orderId"`
	PositionID  string  `json:"positionId"`
	Side        string  `json:"side"`
	TradeType   string  `json:"tradeType"`
	RealizedPnL *string `json:"realizedPnl"`
	FilledAt    string  `json:"filledAt"`
}

// FillExecutionPage is the narrow filtered execution projection proven by
// pinned fill-history source tests. It is not an accepted HTTP contract.
type FillExecutionPage struct {
	Items      []FillExecutionView
	NextCursor *string
	PrevCursor *string
	Total      int64
}

// FundingView is one exact, append-only client funding projection.
type FundingView struct {
	FundingID              string `json:"fundingId"`
	Symbol                 string `json:"symbol"`
	PositionID             string `json:"positionId"`
	PositionSignedQuantity string `json:"positionSignedQty"`
	OraclePrice            string `json:"oraclePrice"`
	FundingRate            string `json:"fundingRate"`
	FundingAmount          string `json:"fundingAmount"`
	Currency               string `json:"currency"`
	FundingTime            string `json:"fundingTime"`
	AccountLogin           *int64 `json:"accountLogin,omitempty"`
}

// FundingPage is the frozen list envelope used by funding history reads.
type FundingPage struct {
	Items      []FundingView `json:"items"`
	NextCursor *string       `json:"nextCursor,omitempty"`
	PrevCursor *string       `json:"prevCursor,omitempty"`
	Total      *int64        `json:"total,omitempty"`
}

// TradingReader provides PostgreSQL-backed compatibility projections.
type TradingReader interface {
	Instruments(context.Context) ([]InstrumentView, error)
	Orders(context.Context, string) ([]OrderView, error)
	Positions(context.Context, string) ([]PositionView, error)
	Balances(context.Context, string) ([]BalanceView, error)
	Funding(context.Context, string, PageParams) (FundingPage, error)
}
