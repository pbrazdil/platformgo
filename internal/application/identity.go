package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"golang.org/x/crypto/argon2"
)

const (
	accessTokenTTL        = 15 * time.Minute
	refreshTokenTTL       = 30 * 24 * time.Hour
	maxBrokerTokenTTL     = time.Hour
	defaultBrokerTokenTTL = 15 * time.Minute
	brokerIdempotencyTTL  = 24 * time.Hour
	defaultMaxAPIKeys     = 25
	maximumMaxAPIKeys     = 25
)

// ErrIdentityNotFound is returned by durable identity lookup ports.
var ErrIdentityNotFound = errors.New("identity not found")

// IdentityRecord is the non-secret identity projection loaded from storage.
type IdentityRecord struct {
	UserID       string
	Login        string
	Email        string
	PasswordHash string
}

// AccountRecord is the durable internal account summary loaded for one user.
type AccountRecord struct {
	AccountID        string
	Login            int64
	UserID           string
	BaseCurrency     string
	MarginMode       string
	OmsMode          string
	MarketVenue      string
	PermittedClasses []string
	Status           string
	CreatedAt        time.Time
}

// UserAPIKeyCreation is the complete non-secret durable mutation passed to
// PostgreSQL. The plaintext token is intentionally absent.
type UserAPIKeyCreation struct {
	OwnerUserID          string
	APIKeyID             engine.ID
	Name                 string
	KeyHash              [sha256.Size]byte
	Prefix               string
	Scopes               []string
	CreatedAt            time.Time
	AuditEventID         engine.ID
	RequestID            string
	MaxActive            int
	ConfigurationVersion uint64
}

// UserAPIKeyStore atomically owns key persistence, cap enforcement, and audit.
type UserAPIKeyStore interface {
	CreateUserAPIKey(context.Context, UserAPIKeyCreation) error
}

// IdentityStore is the durable identity boundary.
type IdentityStore interface {
	UserByLogin(context.Context, string) (IdentityRecord, error)
	UserByID(context.Context, string) (IdentityRecord, error)
	BrokerUserByID(context.Context, string, string) (IdentityRecord, error)
	UserAccounts(context.Context, string) ([]string, error)
	AccountsByUser(context.Context, string) ([]AccountRecord, error)
	BrokerUserAccounts(context.Context, string, string) ([]string, error)
	CreateBrokerUser(
		context.Context,
		string,
		string,
		string,
		string,
	) (IdentityRecord, bool, error)
	CreateSession(
		context.Context,
		engine.ID,
		string,
		[sha256.Size]byte,
		time.Time,
	) error
	BrokerEcho(
		context.Context,
		string,
		string,
		[sha256.Size]byte,
		string,
		time.Time,
	) (string, error)
	ReplayBrokerAccount(
		context.Context,
		string,
		string,
		string,
		[sha256.Size]byte,
	) (edge.BrokerAccountAdmission, bool, error)
	CreateBrokerAccount(
		context.Context,
		string,
		string,
		string,
		[sha256.Size]byte,
		edge.BrokerAccountResult,
		time.Time,
		bool,
	) (edge.BrokerAccountAdmission, error)
}

// ClientTokenSigner signs the client JWT contract at the trusted identity edge.
type ClientTokenSigner interface {
	SignClientToken(edge.ClientClaims) (string, error)
}

// IdentityConfig makes time and entropy explicit outside the deterministic
// economic core.
type IdentityConfig struct {
	Clock                      Clock
	Entropy                    io.Reader
	CommandReadiness           func(context.Context) error
	AccountProvisioningTimeout time.Duration
	MaxAPIKeysPerOwner         int
}

// Identity implements password login and broker identity delegation.
type Identity struct {
	store            IdentityStore
	signer           ClientTokenSigner
	clock            Clock
	entropy          io.Reader
	commandReadiness func(context.Context) error
	accountTimeout   time.Duration
	maxAPIKeys       int
}

// NewIdentity constructs the production identity application service.
func NewIdentity(
	store IdentityStore,
	signer ClientTokenSigner,
	config IdentityConfig,
) (*Identity, error) {
	if store == nil || signer == nil {
		return nil, errors.New("identity: store and token signer are required")
	}
	if config.Clock == nil {
		config.Clock = wallClock{}
	}
	if config.Entropy == nil {
		config.Entropy = rand.Reader
	}
	if config.AccountProvisioningTimeout <= 0 {
		config.AccountProvisioningTimeout = 5 * time.Second
	}
	if config.MaxAPIKeysPerOwner == 0 {
		config.MaxAPIKeysPerOwner = defaultMaxAPIKeys
	}
	if config.MaxAPIKeysPerOwner < 0 ||
		config.MaxAPIKeysPerOwner > maximumMaxAPIKeys {
		return nil, errors.New(
			"identity: API-key owner limit must be between 1 and 25",
		)
	}
	return &Identity{
		store: store, signer: signer, clock: config.Clock, entropy: config.Entropy,
		commandReadiness: config.CommandReadiness,
		accountTimeout:   config.AccountProvisioningTimeout,
		maxAPIKeys:       config.MaxAPIKeysPerOwner,
	}, nil
}

// Login verifies an Argon2id password, issues a bounded access token, and
// persists only the SHA-256 hash of the opaque refresh token.
func (identity *Identity) Login(
	ctx context.Context,
	request edge.LoginRequest,
) (edge.LoginResponse, error) {
	record, err := identity.store.UserByLogin(ctx, strings.TrimSpace(request.Login))
	if err != nil || record.PasswordHash == "" ||
		!VerifyPassword(record.PasswordHash, request.Password) {
		return edge.LoginResponse{}, edge.ErrInvalidCredentials
	}
	accounts, err := identity.store.UserAccounts(ctx, record.UserID)
	if err != nil {
		return edge.LoginResponse{}, fmt.Errorf("identity login: accounts: %w", err)
	}
	now := identity.clock.Now().UTC()
	access, err := identity.signer.SignClientToken(edge.ClientClaims{
		Subject: record.UserID, Audience: string(edge.AudienceClient),
		Expires: now.Add(accessTokenTTL).Unix(), Accounts: accounts,
	})
	if err != nil {
		return edge.LoginResponse{}, fmt.Errorf("identity login: sign access token: %w", err)
	}
	refreshBytes := make([]byte, 32)
	if _, err := io.ReadFull(identity.entropy, refreshBytes); err != nil {
		return edge.LoginResponse{}, fmt.Errorf("identity login: refresh entropy: %w", err)
	}
	refresh := base64.RawURLEncoding.EncodeToString(refreshBytes)
	refreshHash := sha256.Sum256([]byte(refresh))
	sessionID := stableID(
		"platformgo.identity.session.v1",
		record.UserID,
		refresh,
		refreshHash,
	)
	if err := identity.store.CreateSession(
		ctx, sessionID, record.UserID, refreshHash, now.Add(refreshTokenTTL),
	); err != nil {
		return edge.LoginResponse{}, fmt.Errorf("identity login: persist session: %w", err)
	}
	return edge.LoginResponse{
		AccessToken: access, RefreshToken: refresh,
	}, nil
}

// Profile returns the authenticated client's durable identity.
func (identity *Identity) Profile(
	ctx context.Context,
	principal edge.Principal,
) (edge.UserProfile, error) {
	record, err := identity.store.UserByID(ctx, principal.Subject)
	if err != nil {
		if errors.Is(err, ErrIdentityNotFound) {
			return edge.UserProfile{}, edge.ErrNotFound
		}
		return edge.UserProfile{}, err
	}
	return edge.UserProfile{
		UserID: record.UserID, Login: record.Login, Email: record.Email,
		Status: "active",
	}, nil
}

// MyAccounts returns complete summaries for only the authenticated user.
func (identity *Identity) MyAccounts(
	ctx context.Context,
	principal edge.Principal,
) ([]edge.MyAccountView, error) {
	records, err := identity.store.AccountsByUser(ctx, principal.Subject)
	if err != nil {
		return nil, fmt.Errorf("identity accounts: %w", err)
	}
	accounts := make([]edge.MyAccountView, 0, len(records))
	for _, record := range records {
		account, err := clientAccountSummary(record)
		if err != nil {
			return nil, fmt.Errorf("identity accounts: %w", err)
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}

// CreateMyAPIKey returns one opaque credential while persisting only its hash.
func (identity *Identity) CreateMyAPIKey(
	ctx context.Context,
	principal edge.Principal,
	requestID string,
	request edge.CreateAPIKeyRequest,
) (edge.APIKeyCreated, error) {
	store, ok := identity.store.(UserAPIKeyStore)
	if !ok {
		return edge.APIKeyCreated{}, errors.New(
			"identity API-key store is unavailable",
		)
	}
	name := strings.TrimSpace(request.Name)
	requestID = strings.TrimSpace(requestID)
	if principal.Subject == "" ||
		principal.Audience != edge.AudienceClient ||
		!strings.HasPrefix(principal.Subject, "urn:xb:user:") ||
		name == "" ||
		name != request.Name ||
		utf8.RuneCountInString(name) > 128 ||
		requestID == "" ||
		utf8.RuneCountInString(requestID) > 128 ||
		len(request.Scopes) > 32 {
		return edge.APIKeyCreated{}, edge.ErrInvalidRequest
	}
	scopes := make([]string, len(request.Scopes))
	seenScopes := make(map[string]struct{}, len(request.Scopes))
	for index, scope := range request.Scopes {
		if scope == "" ||
			scope != strings.TrimSpace(scope) ||
			utf8.RuneCountInString(scope) > 128 {
			return edge.APIKeyCreated{}, edge.ErrInvalidRequest
		}
		if _, exists := seenScopes[scope]; exists {
			return edge.APIKeyCreated{}, edge.ErrInvalidRequest
		}
		seenScopes[scope] = struct{}{}
		scopes[index] = scope
	}

	apiKeyID, err := identity.randomIdentityID()
	if err != nil {
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: key ID entropy: %w",
			err,
		)
	}
	var prefixBytes [6]byte
	if _, readErr := io.ReadFull(
		identity.entropy,
		prefixBytes[:],
	); readErr != nil {
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: prefix entropy: %w",
			readErr,
		)
	}
	var secretBytes [24]byte
	if _, readErr := io.ReadFull(
		identity.entropy,
		secretBytes[:],
	); readErr != nil {
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: secret entropy: %w",
			readErr,
		)
	}
	auditEventID, err := identity.randomIdentityID()
	if err != nil {
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: audit ID entropy: %w",
			err,
		)
	}
	prefix := fmt.Sprintf("%x", prefixBytes)
	secret := fmt.Sprintf("%x", secretBytes)
	keyHash := sha256.Sum256([]byte(secret))
	now := identity.clock.Now().UTC()
	if err := store.CreateUserAPIKey(ctx, UserAPIKeyCreation{
		OwnerUserID:          principal.Subject,
		APIKeyID:             apiKeyID,
		Name:                 name,
		KeyHash:              keyHash,
		Prefix:               prefix,
		Scopes:               scopes,
		CreatedAt:            now,
		AuditEventID:         auditEventID,
		RequestID:            requestID,
		MaxActive:            identity.maxAPIKeys,
		ConfigurationVersion: 1,
	}); err != nil {
		if errors.Is(err, edge.ErrConflict) {
			return edge.APIKeyCreated{}, edge.ErrConflict
		}
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: %w",
			err,
		)
	}
	return edge.APIKeyCreated{
		ID:     "urn:xb:apikey:" + apiKeyID.String(),
		Prefix: prefix,
		Token:  "xbk_" + prefix + "." + secret,
	}, nil
}

func (identity *Identity) randomIdentityID() (engine.ID, error) {
	var id engine.ID
	if _, err := io.ReadFull(identity.entropy, id[:]); err != nil {
		return engine.ID{}, err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id, nil
}

func clientAccountSummary(record AccountRecord) (edge.MyAccountView, error) {
	normalized := func(kind, value string, allowed ...string) (string, error) {
		for _, candidate := range allowed {
			if value == candidate {
				return strings.ToLower(value), nil
			}
		}
		return "", fmt.Errorf("invalid %s %q", kind, value)
	}
	status, err := normalized(
		"account status",
		record.Status,
		"PENDING",
		"ACTIVE",
		"CLOSE_ONLY",
		"FROZEN",
		"READ_ONLY",
		"SUSPENDED",
		"CLOSED",
	)
	if err != nil {
		return edge.MyAccountView{}, err
	}
	marginMode, err := normalized(
		"account margin mode",
		record.MarginMode,
		"CROSS",
		"ISOLATED",
	)
	if err != nil {
		return edge.MyAccountView{}, err
	}
	omsMode, err := normalized(
		"account OMS mode",
		record.OmsMode,
		"NETTING",
		"HEDGING",
	)
	if err != nil {
		return edge.MyAccountView{}, err
	}
	if record.MarketVenue != "HYPERLIQUID" ||
		len(record.PermittedClasses) != 1 ||
		record.PermittedClasses[0] != "CRYPTOCURRENCY" {
		return edge.MyAccountView{}, fmt.Errorf(
			"invalid account market compatibility %q/%v",
			record.MarketVenue,
			record.PermittedClasses,
		)
	}
	return edge.MyAccountView{
		AccountID:        record.AccountID,
		Login:            record.Login,
		UserID:           record.UserID,
		BaseCurrency:     record.BaseCurrency,
		MarginMode:       marginMode,
		OmsMode:          omsMode,
		MarketVenue:      "hyperliquid",
		PermittedClasses: []string{"perps"},
		Status:           status,
		CreatedAt:        record.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

// BrokerEcho durably replays one response per principal and idempotency key.
func (identity *Identity) BrokerEcho(
	ctx context.Context,
	principal edge.Principal,
	idempotencyKey string,
) (string, error) {
	if principal.Subject == "" || idempotencyKey == "" {
		return "", edge.ErrInvalidRequest
	}
	requestHash := sha256.Sum256([]byte("{}"))
	resultID := stableID(
		"platformgo.identity.broker-echo.v1",
		principal.Subject,
		idempotencyKey,
		requestHash,
	).String()
	return identity.store.BrokerEcho(
		ctx,
		principal.Subject,
		idempotencyKey,
		requestHash,
		resultID,
		identity.clock.Now().UTC().Add(brokerIdempotencyTTL),
	)
}

// CreateBrokerUser converges case-insensitively on email.
func (identity *Identity) CreateBrokerUser(
	ctx context.Context,
	principal edge.Principal,
	idempotencyKey string,
	request edge.BrokerUserRequest,
) (edge.BrokerUserResult, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(request.Email))
	normalizedLogin := strings.ToLower(strings.TrimSpace(request.Login))
	requestHash := sha256.Sum256([]byte(normalizedEmail))
	userID := "urn:xb:user:" + stableID(
		"platformgo.identity.user.v1",
		principal.Subject,
		idempotencyKey,
		requestHash,
	).String()
	record, created, err := identity.store.CreateBrokerUser(
		ctx, principal.Tenant, userID, normalizedLogin, normalizedEmail,
	)
	if err != nil {
		return edge.BrokerUserResult{}, fmt.Errorf("identity create broker user: %w", err)
	}
	return edge.BrokerUserResult{ID: record.UserID, Created: created}, nil
}

// CreateBrokerAccount atomically provisions the account ownership graph and
// stores the exact response under a principal-scoped idempotency key.
func (identity *Identity) CreateBrokerAccount(
	ctx context.Context,
	principal edge.Principal,
	idempotencyKey string,
	request edge.BrokerAccountRequest,
) (edge.BrokerAccountAdmission, error) {
	if principal.Subject == "" || principal.Tenant == "" || idempotencyKey == "" ||
		!strings.HasPrefix(request.UserID, "urn:xb:user:") {
		return edge.BrokerAccountAdmission{}, edge.ErrInvalidRequest
	}
	baseCurrency := "USDC"
	if request.BaseCurrency != nil {
		baseCurrency = strings.ToUpper(strings.TrimSpace(*request.BaseCurrency))
	}
	venue := "HYPERLIQUID"
	if request.Venue != nil {
		venue = strings.ToUpper(strings.TrimSpace(*request.Venue))
	}
	if baseCurrency != "USDC" || venue != "HYPERLIQUID" {
		return edge.BrokerAccountAdmission{}, edge.ErrInvalidRequest
	}
	canonical, err := json.Marshal(edge.BrokerAccountRequest{
		UserID: request.UserID, BaseCurrency: &baseCurrency, Venue: &venue,
	})
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"identity create broker account: canonical request: %w",
			err,
		)
	}
	requestHash := sha256.Sum256(canonical)
	accountUUID := stableID(
		"platformgo.identity.broker-account.v1",
		principal.Subject,
		idempotencyKey,
		requestHash,
	)
	login := int64(binary.BigEndian.Uint64(accountUUID[:8]) & (1<<63 - 1))
	if login == 0 {
		login = 1
	}
	now := identity.clock.Now().UTC()
	result := edge.BrokerAccountResult{
		ID:               "urn:xb:account:" + accountUUID.String(),
		Login:            login,
		UserID:           request.UserID,
		BaseCurrency:     baseCurrency,
		MarketVenue:      venue,
		PermittedClasses: []string{"CRYPTOCURRENCY"},
		CreatedAt:        now.Format(time.RFC3339Nano),
	}
	waitContext, cancel := context.WithTimeout(ctx, identity.accountTimeout)
	defer cancel()
	replay, found, err := identity.store.ReplayBrokerAccount(
		waitContext,
		principal.Subject,
		principal.Tenant,
		idempotencyKey,
		requestHash,
	)
	switch {
	case errors.Is(err, ErrIdempotencyConflict):
		return edge.BrokerAccountAdmission{}, edge.ErrIdempotencyConflict
	case err != nil:
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"identity create broker account: load replay: %w",
			err,
		)
	case found:
		return replay, nil
	}
	if identity.commandReadiness != nil {
		if readinessErr := identity.commandReadiness(ctx); readinessErr != nil {
			replay, found, replayErr := identity.store.ReplayBrokerAccount(
				waitContext,
				principal.Subject,
				principal.Tenant,
				idempotencyKey,
				requestHash,
			)
			switch {
			case errors.Is(replayErr, ErrIdempotencyConflict):
				return edge.BrokerAccountAdmission{}, edge.ErrIdempotencyConflict
			case replayErr != nil:
				return edge.BrokerAccountAdmission{}, fmt.Errorf(
					"identity create broker account: recheck replay after readiness loss: %w",
					replayErr,
				)
			case found:
				return replay, nil
			}
			return edge.BrokerAccountAdmission{}, fmt.Errorf(
				"identity create broker account: runtime is not ready: %w",
				readinessErr,
			)
		}
	}
	if _, err := identity.store.BrokerUserByID(
		ctx,
		principal.Tenant,
		request.UserID,
	); err != nil {
		if errors.Is(err, ErrIdentityNotFound) {
			return edge.BrokerAccountAdmission{}, edge.ErrNotFound
		}
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"identity create broker account: load user: %w",
			err,
		)
	}
	return identity.store.CreateBrokerAccount(
		waitContext,
		principal.Subject,
		principal.Tenant,
		idempotencyKey,
		requestHash,
		result,
		now.Add(brokerIdempotencyTTL),
		identity.commandReadiness != nil,
	)
}

// MintBrokerToken issues a bounded delegated client access token.
func (identity *Identity) MintBrokerToken(
	ctx context.Context,
	principal edge.Principal,
	userID string,
	request edge.BrokerTokenRequest,
) (edge.BrokerTokenResponse, error) {
	if principal.Subject == "" || principal.Tenant == "" {
		return edge.BrokerTokenResponse{}, edge.ErrInvalidRequest
	}
	if _, err := identity.store.BrokerUserByID(
		ctx,
		principal.Tenant,
		userID,
	); err != nil {
		if errors.Is(err, ErrIdentityNotFound) {
			return edge.BrokerTokenResponse{}, edge.ErrNotFound
		}
		return edge.BrokerTokenResponse{}, err
	}
	ttl := defaultBrokerTokenTTL
	if request.TTLSeconds != nil {
		if *request.TTLSeconds == 0 ||
			*request.TTLSeconds > uint64(maxBrokerTokenTTL/time.Second) {
			return edge.BrokerTokenResponse{}, edge.ErrInvalidRequest
		}
		ttl = time.Duration(*request.TTLSeconds) * time.Second
	}
	accounts, err := identity.store.BrokerUserAccounts(
		ctx,
		principal.Tenant,
		userID,
	)
	if err != nil {
		return edge.BrokerTokenResponse{}, err
	}
	token, err := identity.signer.SignClientToken(edge.ClientClaims{
		Subject: userID, Audience: string(edge.AudienceClient),
		Expires: identity.clock.Now().UTC().Add(ttl).Unix(), Accounts: accounts,
	})
	if err != nil {
		return edge.BrokerTokenResponse{}, err
	}
	return edge.BrokerTokenResponse{
		AccessToken: token, ExpiresInSecs: uint64(ttl / time.Second),
	}, nil
}

// HashPassword creates a versioned Argon2id hash for durable identity storage.
func HashPassword(password string, entropy io.Reader) (string, error) {
	if len(password) < 12 {
		return "", errors.New("identity password must contain at least 12 bytes")
	}
	if entropy == nil {
		entropy = rand.Reader
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(entropy, salt); err != nil {
		return "", err
	}
	const (
		memory      = uint32(64 * 1024)
		iterations  = uint32(3)
		parallelism = uint8(1)
		keyLength   = uint32(32)
	)
	key := argon2.IDKey(
		[]byte(password), salt, iterations, memory, parallelism, keyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		iterations,
		parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks one versioned Argon2id hash in constant time.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" ||
		parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return false
	}
	var memory uint32
	var iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&memory,
		&iterations,
		&parallelism,
	); err != nil || memory != 64*1024 || iterations != 3 || parallelism != 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false
	}
	actual := argon2.IDKey(
		[]byte(password), salt, iterations, memory, parallelism, 32,
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
