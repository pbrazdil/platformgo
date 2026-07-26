package application

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"golang.org/x/crypto/argon2"
)

var apiKeyReplayKeyIDPattern = regexp.MustCompile(
	`^[A-Za-z0-9._-]{1,64}$`,
)

const (
	accessTokenTTL        = 15 * time.Minute
	refreshTokenTTL       = 30 * 24 * time.Hour
	maxBrokerTokenTTL     = time.Hour
	defaultBrokerTokenTTL = 15 * time.Minute
	brokerIdempotencyTTL  = 24 * time.Hour
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
	OwnerUserID      string
	APIKeyID         engine.ID
	Name             string
	KeyHash          [sha256.Size]byte
	Prefix           string
	Scopes           []string
	AuditEventID     engine.ID
	RequestID        string
	IdempotencyKey   string
	RequestHash      [sha256.Size]byte
	ReplayKeyID      string
	ReplayNonce      []byte
	ReplayCiphertext []byte
}

// UserAPIKeyCreationResult contains only authenticated encrypted replay
// material. Replayed is false when this request created the durable effect.
type UserAPIKeyCreationResult struct {
	Replayed         bool
	ReplayKeyID      string
	ReplayNonce      []byte
	ReplayCiphertext []byte
}

// UserAPIKeyStore atomically owns key persistence, cap enforcement, and audit.
type UserAPIKeyStore interface {
	ClaimClientRateLimit(context.Context, string, time.Time) (bool, error)
	CreateUserAPIKey(
		context.Context,
		UserAPIKeyCreation,
	) (UserAPIKeyCreationResult, error)
}

// APIKeyReplayKey is one AES-256-GCM key retained for bounded response replay.
// The first configured key encrypts new responses; following keys decrypt
// responses created before rotation.
type APIKeyReplayKey struct {
	ID  string
	Key [32]byte
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
	APIKeyReplayKeys           []APIKeyReplayKey
}

// Identity implements password login and broker identity delegation.
type Identity struct {
	store            IdentityStore
	signer           ClientTokenSigner
	clock            Clock
	entropy          io.Reader
	commandReadiness func(context.Context) error
	accountTimeout   time.Duration
	replayKeyID      string
	replayKeys       map[string]cipher.AEAD
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
	replayKeys := make(map[string]cipher.AEAD, len(config.APIKeyReplayKeys))
	for _, replayKey := range config.APIKeyReplayKeys {
		if !apiKeyReplayKeyIDPattern.MatchString(replayKey.ID) {
			return nil, errors.New("identity: API-key replay key ID is invalid")
		}
		if _, exists := replayKeys[replayKey.ID]; exists {
			return nil, errors.New("identity: duplicate API-key replay key ID")
		}
		block, err := aes.NewCipher(replayKey.Key[:])
		if err != nil {
			return nil, fmt.Errorf("identity: API-key replay cipher: %w", err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, fmt.Errorf("identity: API-key replay GCM: %w", err)
		}
		replayKeys[replayKey.ID] = aead
	}
	activeReplayKeyID := ""
	if len(config.APIKeyReplayKeys) > 0 {
		activeReplayKeyID = config.APIKeyReplayKeys[0].ID
	}
	return &Identity{
		store: store, signer: signer, clock: config.Clock, entropy: config.Entropy,
		commandReadiness: config.CommandReadiness,
		accountTimeout:   config.AccountProvisioningTimeout,
		replayKeyID:      activeReplayKeyID,
		replayKeys:       replayKeys,
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

// CheckClientMutationRate applies the shared durable protected-client limiter
// before credential entropy is consumed.
func (identity *Identity) CheckClientMutationRate(
	ctx context.Context,
	principal edge.Principal,
) error {
	store, ok := identity.store.(UserAPIKeyStore)
	if !ok {
		return errors.New("identity API-key store is unavailable")
	}
	if principal.Subject == "" ||
		principal.Audience != edge.AudienceClient ||
		!strings.HasPrefix(principal.Subject, "urn:xb:user:") {
		return edge.ErrUnauthorized
	}
	allowed, err := store.ClaimClientRateLimit(
		ctx,
		principal.Subject,
		identity.clock.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("identity client rate limit: %w", err)
	}
	if !allowed {
		return edge.ErrRateLimited
	}
	return nil
}

// CreateMyAPIKey returns one opaque credential while persisting only its hash.
// An explicit Idempotency-Key replays the exact encrypted response.
func (identity *Identity) CreateMyAPIKey(
	ctx context.Context,
	principal edge.Principal,
	requestID string,
	idempotencyKey string,
	request edge.CreateAPIKeyRequest,
) (edge.APIKeyCreated, error) {
	store, ok := identity.store.(UserAPIKeyStore)
	if !ok {
		return edge.APIKeyCreated{}, errors.New(
			"identity API-key store is unavailable",
		)
	}
	requestID = strings.TrimSpace(requestID)
	if principal.Subject == "" ||
		principal.Audience != edge.AudienceClient ||
		!strings.HasPrefix(principal.Subject, "urn:xb:user:") ||
		request.Name == "" ||
		requestID == "" ||
		len(requestID) > 128 {
		return edge.APIKeyCreated{}, edge.ErrInvalidRequest
	}
	if identity.replayKeyID == "" {
		return edge.APIKeyCreated{}, errors.New(
			"identity API-key replay encryption is unavailable",
		)
	}
	scopes := make([]string, len(request.Scopes))
	copy(scopes, request.Scopes)
	canonical, err := json.Marshal(struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}{Name: request.Name, Scopes: scopes})
	if err != nil {
		return edge.APIKeyCreated{}, fmt.Errorf(
			"identity create API key: canonical request: %w",
			err,
		)
	}
	requestHash := sha256.Sum256(canonical)

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
	candidate := edge.APIKeyCreated{
		ID:     "urn:xb:apikey:" + apiKeyID.String(),
		Prefix: prefix,
		Token:  "xbk_" + prefix + "." + secret,
	}
	replayNonce, replayCiphertext, err := identity.encryptAPIKeyReplay(
		principal.Subject,
		idempotencyKey,
		requestHash,
		candidate,
	)
	if err != nil {
		return edge.APIKeyCreated{}, err
	}
	stored, err := store.CreateUserAPIKey(ctx, UserAPIKeyCreation{
		OwnerUserID:      principal.Subject,
		APIKeyID:         apiKeyID,
		Name:             request.Name,
		KeyHash:          keyHash,
		Prefix:           prefix,
		Scopes:           scopes,
		AuditEventID:     auditEventID,
		RequestID:        requestID,
		IdempotencyKey:   idempotencyKey,
		RequestHash:      requestHash,
		ReplayKeyID:      identity.replayKeyID,
		ReplayNonce:      replayNonce,
		ReplayCiphertext: replayCiphertext,
	})
	if err != nil {
		switch {
		case errors.Is(err, edge.ErrConflict):
			return edge.APIKeyCreated{}, edge.ErrConflict
		case errors.Is(err, edge.ErrIdempotencyConflict):
			return edge.APIKeyCreated{}, edge.ErrIdempotencyConflict
		default:
			return edge.APIKeyCreated{}, fmt.Errorf(
				"identity create API key: %w",
				err,
			)
		}
	}
	if !stored.Replayed {
		return candidate, nil
	}
	return identity.decryptAPIKeyReplay(
		principal.Subject,
		idempotencyKey,
		requestHash,
		stored,
	)
}

func (identity *Identity) encryptAPIKeyReplay(
	owner string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	response edge.APIKeyCreated,
) ([]byte, []byte, error) {
	aead := identity.replayKeys[identity.replayKeyID]
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(identity.entropy, nonce); err != nil {
		return nil, nil, fmt.Errorf(
			"identity create API key: replay nonce entropy: %w",
			err,
		)
	}
	plaintext, err := json.Marshal(response)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"identity create API key: encode replay: %w",
			err,
		)
	}
	return nonce, aead.Seal(
		nil,
		nonce,
		plaintext,
		apiKeyReplayAAD(
			owner,
			idempotencyKey,
			requestHash,
			identity.replayKeyID,
		),
	), nil
}

func (identity *Identity) decryptAPIKeyReplay(
	owner string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	stored UserAPIKeyCreationResult,
) (edge.APIKeyCreated, error) {
	aead, ok := identity.replayKeys[stored.ReplayKeyID]
	if !ok {
		return edge.APIKeyCreated{}, errors.New(
			"identity create API key: replay key is unavailable",
		)
	}
	plaintext, err := aead.Open(
		nil,
		stored.ReplayNonce,
		stored.ReplayCiphertext,
		apiKeyReplayAAD(
			owner,
			idempotencyKey,
			requestHash,
			stored.ReplayKeyID,
		),
	)
	if err != nil {
		return edge.APIKeyCreated{}, errors.New(
			"identity create API key: replay authentication failed",
		)
	}
	var response edge.APIKeyCreated
	if err := json.Unmarshal(plaintext, &response); err != nil ||
		response.ID == "" ||
		response.Prefix == "" ||
		response.Token == "" {
		return edge.APIKeyCreated{}, errors.New(
			"identity create API key: replay payload is invalid",
		)
	}
	return response, nil
}

func apiKeyReplayAAD(
	owner string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	replayKeyID string,
) []byte {
	aad := make(
		[]byte,
		0,
		len(owner)+len(idempotencyKey)+len(replayKeyID)+sha256.Size+3,
	)
	aad = append(aad, owner...)
	aad = append(aad, 0)
	aad = append(aad, idempotencyKey...)
	aad = append(aad, 0)
	aad = append(aad, replayKeyID...)
	aad = append(aad, 0)
	return append(aad, requestHash[:]...)
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
