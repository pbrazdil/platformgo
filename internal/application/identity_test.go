package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type memoryIdentityStore struct {
	users                map[string]IdentityRecord
	accounts             map[string][]string
	accountRecords       map[string][]AccountRecord
	brokerRequests       map[string][sha256.Size]byte
	brokerResults        map[string]edge.BrokerAccountAdmission
	session              engine.ID
	refresh              [sha256.Size]byte
	runtimeReadyRequired bool
	apiKeyReplays        map[[sha256.Size]byte]memoryAPIKeyReplay
	apiKeyCreateCalls    int
	apiKeyRateCalls      int
}

type memoryAPIKeyReplay struct {
	requestHash [sha256.Size]byte
	result      UserAPIKeyReplayResult
}

type failingIdentityEntropy struct{}

func (failingIdentityEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy must not be read")
}

func (store *memoryIdentityStore) UserByLogin(
	_ context.Context,
	login string,
) (IdentityRecord, error) {
	for _, user := range store.users {
		if user.Login == login {
			return user, nil
		}
	}
	return IdentityRecord{}, ErrIdentityNotFound
}

func (store *memoryIdentityStore) UserByID(
	_ context.Context,
	userID string,
) (IdentityRecord, error) {
	user, ok := store.users[userID]
	if !ok {
		return IdentityRecord{}, ErrIdentityNotFound
	}
	return user, nil
}

func (store *memoryIdentityStore) BrokerUserByID(
	ctx context.Context,
	_ string,
	userID string,
) (IdentityRecord, error) {
	return store.UserByID(ctx, userID)
}

func (store *memoryIdentityStore) UserAccounts(
	_ context.Context,
	userID string,
) ([]string, error) {
	return append([]string(nil), store.accounts[userID]...), nil
}

func (store *memoryIdentityStore) AccountsByUser(
	_ context.Context,
	userID string,
) ([]AccountRecord, error) {
	return append([]AccountRecord(nil), store.accountRecords[userID]...), nil
}

func (store *memoryIdentityStore) BrokerUserAccounts(
	ctx context.Context,
	_ string,
	userID string,
) ([]string, error) {
	return store.UserAccounts(ctx, userID)
}

func (store *memoryIdentityStore) CreateBrokerUser(
	_ context.Context,
	_ string,
	userID string,
	login string,
	email string,
) (IdentityRecord, bool, error) {
	for _, user := range store.users {
		if user.Email == email {
			return user, false, nil
		}
	}
	user := IdentityRecord{UserID: userID, Login: login, Email: email}
	store.users[userID] = user
	return user, true, nil
}

func (store *memoryIdentityStore) CreateSession(
	_ context.Context,
	sessionID engine.ID,
	_ string,
	refreshHash [sha256.Size]byte,
	_ time.Time,
) error {
	if !store.session.IsZero() {
		return errors.New("duplicate session")
	}
	store.session = sessionID
	store.refresh = refreshHash
	return nil
}

func (store *memoryIdentityStore) ClaimClientRateLimit(
	_ context.Context,
	_ string,
) (ClientRateLimitResult, error) {
	store.apiKeyRateCalls++
	return ClientRateLimitResult{Allowed: true}, nil
}

func (store *memoryIdentityStore) ReplayUserAPIKey(
	_ context.Context,
	_ string,
	idempotencyHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
) (UserAPIKeyReplayResult, error) {
	replay, ok := store.apiKeyReplays[idempotencyHash]
	if !ok {
		return UserAPIKeyReplayResult{}, nil
	}
	if replay.requestHash != requestHash {
		return UserAPIKeyReplayResult{}, edge.ErrIdempotencyConflict
	}
	return replay.result, nil
}

func (store *memoryIdentityStore) CreateUserAPIKey(
	_ context.Context,
	creation UserAPIKeyCreation,
) (UserAPIKeyCreationResult, error) {
	store.apiKeyCreateCalls++
	if store.apiKeyReplays == nil {
		store.apiKeyReplays = make(
			map[[sha256.Size]byte]memoryAPIKeyReplay,
		)
	}
	result := UserAPIKeyReplayResult{
		Found:            true,
		ResponseStatus:   201,
		ReplayKeyID:      creation.ReplayKeyID,
		ReplayNonce:      append([]byte(nil), creation.ReplayNonce...),
		ReplayCiphertext: append([]byte(nil), creation.ReplayCiphertext...),
	}
	store.apiKeyReplays[creation.IdempotencyHash] = memoryAPIKeyReplay{
		requestHash: creation.RequestHash,
		result:      result,
	}
	return UserAPIKeyCreationResult{
		Outcome:          "created",
		ResponseStatus:   result.ResponseStatus,
		ReplayKeyID:      result.ReplayKeyID,
		ReplayNonce:      result.ReplayNonce,
		ReplayCiphertext: result.ReplayCiphertext,
	}, nil
}

func (store *memoryIdentityStore) BrokerEcho(
	_ context.Context,
	_ string,
	_ [sha256.Size]byte,
	_ [sha256.Size]byte,
	response edge.StoredResponse,
) (edge.StoredResponse, error) {
	return response, nil
}

func (store *memoryIdentityStore) ReplayBrokerAccount(
	_ context.Context,
	principal string,
	_ string,
	key string,
	requestHash [sha256.Size]byte,
) (edge.BrokerAccountAdmission, bool, error) {
	scope := principal + "\x00" + key
	previous, found := store.brokerRequests[scope]
	if !found {
		return edge.BrokerAccountAdmission{}, false, nil
	}
	if previous != requestHash {
		return edge.BrokerAccountAdmission{}, true, ErrIdempotencyConflict
	}
	return store.brokerResults[scope], true, nil
}

func (store *memoryIdentityStore) CreateBrokerAccount(
	_ context.Context,
	principal string,
	_ string,
	key string,
	requestHash [sha256.Size]byte,
	result edge.BrokerAccountResult,
	_ time.Time,
	requireRuntimeReady bool,
) (edge.BrokerAccountAdmission, error) {
	store.runtimeReadyRequired = requireRuntimeReady
	store.accounts[result.UserID] = append(store.accounts[result.UserID], result.ID)
	body, _ := json.Marshal(result)
	admission := edge.BrokerAccountAdmission{
		BrokerAccountResult: result,
		Response: edge.StoredResponse{
			Status:  201,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    append(body, '\n'),
		},
	}
	if store.brokerRequests == nil {
		store.brokerRequests = make(map[string][sha256.Size]byte)
		store.brokerResults = make(map[string]edge.BrokerAccountAdmission)
	}
	scope := principal + "\x00" + key
	store.brokerRequests[scope] = requestHash
	store.brokerResults[scope] = admission
	return admission, nil
}

func TestIdentityPasswordLoginProfileAndBrokerDelegation(t *testing.T) {
	passwordHash, err := HashPassword(
		"correct horse battery staple",
		bytes.NewReader(bytes.Repeat([]byte{7}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 14, 0, 0, 0, time.UTC)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClockForApplication{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryIdentityStore{
		users: map[string]IdentityRecord{
			"urn:xb:user:trader-1": {
				UserID: "urn:xb:user:trader-1", Login: "trader1",
				Email: "trader1@example.com", PasswordHash: passwordHash,
			},
		},
		accounts: map[string][]string{
			"urn:xb:user:trader-1": {"urn:xb:account:acct-1"},
		},
		accountRecords: map[string][]AccountRecord{
			"urn:xb:user:trader-1": {
				{
					AccountID:    "urn:xb:account:acct-1",
					Login:        73000001,
					UserID:       "urn:xb:user:trader-1",
					BaseCurrency: "USDC",
					MarginMode:   "CROSS",
					OmsMode:      "NETTING",
					MarketVenue:  "HYPERLIQUID",
					PermittedClasses: []string{
						"CRYPTOCURRENCY",
					},
					Status: "ACTIVE",
					CreatedAt: time.Date(
						2026,
						time.July,
						26,
						8,
						9,
						10,
						0,
						time.UTC,
					),
				},
			},
		},
	}
	identity, err := NewIdentity(store, authenticator, IdentityConfig{
		Clock:   fixedClock{value: now},
		Entropy: bytes.NewReader(bytes.Repeat([]byte{9}, 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	login, err := identity.Login(context.Background(), edge.LoginRequest{
		Login: "trader1", Password: "correct horse battery staple",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := authenticator.AuthenticateClient(
		context.Background(),
		login.AccessToken,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.OwnsAccount("urn:xb:account:acct-1") ||
		login.RefreshToken == "" ||
		store.session.IsZero() {
		t.Fatalf("login=%#v principal=%#v", login, principal)
	}
	if _, loginErr := identity.Login(context.Background(), edge.LoginRequest{
		Login: "trader1", Password: "wrong password",
	}); !errors.Is(loginErr, edge.ErrInvalidCredentials) {
		t.Fatalf("wrong password error = %v", loginErr)
	}
	profile, err := identity.Profile(context.Background(), principal)
	if err != nil || profile.Login != "trader1" {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	accounts, err := identity.MyAccounts(context.Background(), principal)
	if err != nil || !reflect.DeepEqual(accounts, []edge.MyAccountView{{
		AccountID:        "urn:xb:account:acct-1",
		Login:            73000001,
		UserID:           "urn:xb:user:trader-1",
		BaseCurrency:     "USDC",
		MarginMode:       "cross",
		OmsMode:          "netting",
		MarketVenue:      "hyperliquid",
		PermittedClasses: []string{"perps"},
		Status:           "active",
		CreatedAt:        "2026-07-26T08:09:10Z",
	}}) {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	created, err := identity.CreateBrokerUser(
		context.Background(),
		edge.Principal{
			Subject:  "urn:xb:apikey:crm",
			Tenant:   "urn:xb:tenant:crm",
			Audience: edge.AudienceBroker,
		},
		"user-1",
		edge.BrokerUserRequest{Login: "crm-user", Email: "USER@EXAMPLE.COM"},
	)
	if err != nil || !created.Created {
		t.Fatalf("created=%#v err=%v", created, err)
	}
	echo, err := identity.BrokerEcho(
		context.Background(),
		edge.Principal{
			Subject:  "urn:xb:apikey:crm",
			Tenant:   "urn:xb:tenant:crm",
			Audience: edge.AudienceBroker,
		},
		"echo-1",
	)
	if err != nil ||
		echo.Status != 200 ||
		len(echo.Headers) == 0 ||
		len(echo.Body) == 0 {
		t.Fatalf("echo=%#v err=%v", echo, err)
	}
	account, err := identity.CreateBrokerAccount(
		context.Background(),
		edge.Principal{
			Subject:  "urn:xb:apikey:crm",
			Tenant:   "urn:xb:tenant:crm",
			Audience: edge.AudienceBroker,
		},
		"account-1",
		edge.BrokerAccountRequest{UserID: created.ID},
	)
	if err != nil || account.ID == "" || account.BaseCurrency != "USDC" {
		t.Fatalf("account=%#v err=%v", account, err)
	}
	ttl := uint64(120)
	minted, err := identity.MintBrokerToken(
		context.Background(),
		edge.Principal{
			Subject:  "urn:xb:apikey:crm",
			Tenant:   "urn:xb:tenant:crm",
			Audience: edge.AudienceBroker,
		},
		created.ID,
		edge.BrokerTokenRequest{TTLSeconds: &ttl},
	)
	if err != nil || minted.ExpiresInSecs != ttl || minted.AccessToken == "" {
		t.Fatalf("minted=%#v err=%v", minted, err)
	}
}

func TestUserAPIKeyReadinessAllowsReplayButRejectsNewWork(t *testing.T) {
	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"0123456789abcdef0123456789abcdef",
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryIdentityStore{}
	var replayKey APIKeyReplayKey
	replayKey.ID = "test-v1"
	copy(replayKey.Key[:], bytes.Repeat([]byte{3}, len(replayKey.Key)))
	readyIdentity, err := NewIdentity(
		store,
		authenticator,
		IdentityConfig{
			Entropy:                 bytes.NewReader(bytes.Repeat([]byte{7}, 512)),
			APIKeyReplayKeys:        []APIKeyReplayKey{replayKey},
			APIKeyReplayActiveKeyID: replayKey.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{
		Subject:  "urn:xb:user:readiness",
		Audience: edge.AudienceClient,
	}
	request := edge.CreateAPIKeyRequest{
		Name:   "readiness-key",
		Scopes: []string{"orders:write"},
	}
	created, err := readyIdentity.CreateMyAPIKey(
		context.Background(),
		principal,
		"request-ready",
		"readiness-key",
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	notReady := errors.New("runtime intentionally unready")
	unreadyIdentity, err := NewIdentity(
		store,
		authenticator,
		IdentityConfig{
			Entropy:                 failingIdentityEntropy{},
			CommandReadiness:        func(context.Context) error { return notReady },
			APIKeyReplayKeys:        []APIKeyReplayKey{replayKey},
			APIKeyReplayActiveKeyID: replayKey.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := unreadyIdentity.CreateMyAPIKey(
		context.Background(),
		principal,
		strings.Repeat("r", 129),
		"readiness-key",
		request,
	)
	if err != nil || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("unready replay = %#v, error = %v", replayed, err)
	}
	_, conflictErr := unreadyIdentity.CreateMyAPIKey(
		context.Background(),
		principal,
		"request-conflict",
		"readiness-key",
		edge.CreateAPIKeyRequest{Name: "changed"},
	)
	if !errors.Is(conflictErr, edge.ErrIdempotencyConflict) {
		t.Fatalf("unready conflict error = %v", conflictErr)
	}
	_, newErr := unreadyIdentity.CreateMyAPIKey(
		context.Background(),
		principal,
		"request-new",
		"new-readiness-key",
		edge.CreateAPIKeyRequest{Name: "new"},
	)
	if newErr == nil || !strings.Contains(newErr.Error(), notReady.Error()) {
		t.Fatalf("unready new-work error = %v", newErr)
	}
	if store.apiKeyCreateCalls != 1 {
		t.Fatalf("unready create calls = %d, want 1", store.apiKeyCreateCalls)
	}

	var committed edge.APIKeyAdmission
	racingIdentity, err := NewIdentity(
		store,
		authenticator,
		IdentityConfig{
			Entropy: failingIdentityEntropy{},
			CommandReadiness: func(readinessContext context.Context) error {
				var commitErr error
				committed, commitErr = readyIdentity.CreateMyAPIKey(
					readinessContext,
					principal,
					"request-race-commit",
					"readiness-race",
					edge.CreateAPIKeyRequest{Name: "race"},
				)
				if commitErr != nil {
					t.Fatalf("concurrent readiness commit: %v", commitErr)
				}
				return notReady
			},
			APIKeyReplayKeys:        []APIKeyReplayKey{replayKey},
			APIKeyReplayActiveKeyID: replayKey.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	raceReplay, err := racingIdentity.CreateMyAPIKey(
		context.Background(),
		principal,
		"request-race-replay",
		"readiness-race",
		edge.CreateAPIKeyRequest{Name: "race"},
	)
	if err != nil || !reflect.DeepEqual(raceReplay, committed) {
		t.Fatalf(
			"readiness-race replay = %#v, committed %#v, error = %v",
			raceReplay,
			committed,
			err,
		)
	}
}

func TestBrokerAccountReplaysWhileUnreadyButRejectsNewWork(t *testing.T) {
	now := time.Date(2026, time.July, 25, 14, 0, 0, 0, time.UTC)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClockForApplication{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryIdentityStore{
		users: map[string]IdentityRecord{
			"urn:xb:user:broker-1": {
				UserID: "urn:xb:user:broker-1",
				Login:  "broker-1",
				Email:  "broker-1@example.com",
			},
		},
		accounts: make(map[string][]string),
	}
	ready := true
	identity, err := NewIdentity(store, authenticator, IdentityConfig{
		Clock: fixedAuthClockForApplication{value: now},
		CommandReadiness: func(context.Context) error {
			if ready {
				return nil
			}
			return errors.New("workers unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{
		Subject:  "urn:xb:apikey:broker",
		Tenant:   "urn:xb:tenant:broker",
		Audience: edge.AudienceBroker,
	}
	request := edge.BrokerAccountRequest{UserID: "urn:xb:user:broker-1"}
	first, err := identity.CreateBrokerAccount(
		context.Background(), principal, "key", request,
	)
	if err != nil {
		t.Fatal(err)
	}
	ready = false
	replayed, err := identity.CreateBrokerAccount(
		context.Background(), principal, "key", request,
	)
	if err != nil ||
		first.ID != replayed.ID ||
		!bytes.Equal(first.Response.Body, replayed.Response.Body) {
		t.Fatalf("replay=%#v first=%#v err=%v", replayed, first, err)
	}
	changed := request
	changed.UserID = "urn:xb:user:other"
	if _, err := identity.CreateBrokerAccount(
		context.Background(), principal, "key", changed,
	); !errors.Is(err, edge.ErrIdempotencyConflict) {
		t.Fatalf("changed replay error=%v, want idempotency conflict", err)
	}
	if _, err := identity.CreateBrokerAccount(
		context.Background(), principal, "new-key", request,
	); err == nil {
		t.Fatal("new account was admitted while readiness was false")
	}
	if len(store.brokerRequests) != 1 {
		t.Fatalf("unready account effects=%d, want 1", len(store.brokerRequests))
	}
	if !store.runtimeReadyRequired {
		t.Fatal("production-readiness configuration was not bound to Begin")
	}
}

func TestBrokerAccountLinearizesReplayAcrossReadinessLoss(t *testing.T) {
	for _, test := range []struct {
		name     string
		userID   string
		conflict bool
	}{
		{name: "same request replays", userID: "urn:xb:user:broker-1"},
		{name: "changed request conflicts", userID: "urn:xb:user:other", conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Date(2026, time.July, 25, 14, 0, 0, 0, time.UTC)
			authenticator, err := edge.NewHMACAuthenticator(
				edge.HMACAuthenticatorConfig{
					ClientTokenSecret: []byte(
						"0123456789abcdef0123456789abcdef",
					),
					Clock: fixedAuthClockForApplication{value: now},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			store := &memoryIdentityStore{
				users: map[string]IdentityRecord{
					"urn:xb:user:broker-1": {
						UserID: "urn:xb:user:broker-1",
						Login:  "broker-1",
						Email:  "broker-1@example.com",
					},
				},
				accounts: make(map[string][]string),
			}
			atReadiness := make(chan struct{})
			releaseReadiness := make(chan struct{})
			identity, err := NewIdentity(store, authenticator, IdentityConfig{
				Clock: fixedAuthClockForApplication{value: now},
				CommandReadiness: func(ctx context.Context) error {
					if ctx.Value(readinessRaceContextKey{}) == nil {
						return nil
					}
					close(atReadiness)
					<-releaseReadiness
					return errors.New("workers unavailable")
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			principal := edge.Principal{
				Subject:  "urn:xb:apikey:broker",
				Tenant:   "urn:xb:tenant:broker",
				Audience: edge.AudienceBroker,
			}
			type retryResult struct {
				admission edge.BrokerAccountAdmission
				err       error
			}
			resultChannel := make(chan retryResult, 1)
			retryContext := context.WithValue(
				context.Background(),
				readinessRaceContextKey{},
				true,
			)
			go func() {
				admission, createErr := identity.CreateBrokerAccount(
					retryContext,
					principal,
					"key",
					edge.BrokerAccountRequest{UserID: test.userID},
				)
				resultChannel <- retryResult{
					admission: admission,
					err:       createErr,
				}
			}()
			<-atReadiness
			first, err := identity.CreateBrokerAccount(
				context.Background(),
				principal,
				"key",
				edge.BrokerAccountRequest{
					UserID: "urn:xb:user:broker-1",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			close(releaseReadiness)
			retry := <-resultChannel
			if test.conflict {
				if !errors.Is(retry.err, edge.ErrIdempotencyConflict) {
					t.Fatalf(
						"retry error=%v, want idempotency conflict",
						retry.err,
					)
				}
			} else if retry.err != nil ||
				!reflect.DeepEqual(
					first.BrokerAccountResult,
					retry.admission.BrokerAccountResult,
				) ||
				first.Response.Status != retry.admission.Response.Status ||
				!bytes.Equal(
					first.Response.Headers,
					retry.admission.Response.Headers,
				) ||
				!bytes.Equal(
					first.Response.Body,
					retry.admission.Response.Body,
				) {
				t.Fatalf(
					"first=%#v retry=%#v error=%v",
					first,
					retry.admission,
					retry.err,
				)
			}
			if len(store.brokerRequests) != 1 ||
				len(store.accounts["urn:xb:user:broker-1"]) != 1 {
				t.Fatalf(
					"requests=%d accounts=%d, want 1 and 1",
					len(store.brokerRequests),
					len(store.accounts["urn:xb:user:broker-1"]),
				)
			}
		})
	}
}

func TestClientAccountSummaryRejectsUnsupportedBaseCurrency(t *testing.T) {
	_, err := clientAccountSummary(AccountRecord{
		AccountID:        "urn:xb:account:00000000-0000-4000-8000-000000000001",
		Login:            73000001,
		UserID:           "urn:xb:user:00000000-0000-4000-8000-000000000001",
		BaseCurrency:     "BTC",
		MarginMode:       "CROSS",
		OmsMode:          "NETTING",
		MarketVenue:      "HYPERLIQUID",
		PermittedClasses: []string{"CRYPTOCURRENCY"},
		Status:           "ACTIVE",
		CreatedAt:        time.Date(2026, 7, 30, 8, 9, 10, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("clientAccountSummary accepted unsupported economic base currency")
	}
}

func TestClientAccountSummaryRejectsTimestampOutsideRFC3339(t *testing.T) {
	_, err := clientAccountSummary(AccountRecord{
		AccountID:        "urn:xb:account:00000000-0000-4000-8000-000000000001",
		Login:            73000001,
		UserID:           "urn:xb:user:00000000-0000-4000-8000-000000000001",
		BaseCurrency:     "USDC",
		MarginMode:       "CROSS",
		OmsMode:          "NETTING",
		MarketVenue:      "HYPERLIQUID",
		PermittedClasses: []string{"CRYPTOCURRENCY"},
		Status:           "ACTIVE",
		CreatedAt:        time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("clientAccountSummary accepted timestamp outside RFC3339")
	}
}

type fixedAuthClockForApplication struct{ value time.Time }

func (clock fixedAuthClockForApplication) Now() time.Time { return clock.value }
