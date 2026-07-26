package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
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

func (store *memoryIdentityStore) BrokerEcho(
	_ context.Context,
	_ string,
	_ string,
	_ [sha256.Size]byte,
	resultID string,
	_ time.Time,
) (string, error) {
	return resultID, nil
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
	if err != nil || echo == "" {
		t.Fatalf("echo=%q err=%v", echo, err)
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

type fixedAuthClockForApplication struct{ value time.Time }

func (clock fixedAuthClockForApplication) Now() time.Time { return clock.value }
