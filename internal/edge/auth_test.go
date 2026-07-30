package edge

import (
	"context"
	"encoding/base64"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type fixedAuthClock struct{ value time.Time }

func (clock fixedAuthClock) Now() time.Time { return clock.value }

func TestHMACAuthenticatorEnforcesAudienceExpiryAndAccountClaims(t *testing.T) {
	now := time.Date(2026, time.July, 25, 15, 0, 0, 0, time.UTC)
	auth, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.SignClientToken(ClientClaims{
		Subject: "urn:xb:user:user-7", Audience: "client",
		Expires:  now.Add(time.Minute).Unix(),
		Accounts: []string{"urn:xb:account:acct-7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.AuthenticateClient(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.OwnsAccount("urn:xb:account:acct-7") ||
		principal.OwnsAccount("urn:xb:account:other") {
		t.Fatalf("principal accounts = %v", principal.Accounts)
	}
	expired, err := auth.SignClientToken(ClientClaims{
		Subject: "urn:xb:user:user-7", Audience: "client",
		Expires: now.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, authenticateErr := auth.AuthenticateClient(context.Background(), expired); authenticateErr == nil {
		t.Fatal("expired token accepted")
	}
	wrongAudience := signClientTestToken(t, jwt.SigningMethodHS256, ClientClaims{
		Subject: "urn:xb:user:user-7", Audience: "admin",
		Expires: now.Add(time.Minute).Unix(),
	}, auth.clientSecret, nil)
	if _, err := auth.AuthenticateClient(context.Background(), wrongAudience); err == nil {
		t.Fatal("cross-audience token accepted")
	}
}

func TestHMACAuthenticatorEnforcesAdminAudienceExpiryAndSubject(t *testing.T) {
	now := time.Date(2026, time.July, 30, 22, 0, 0, 0, time.UTC)
	if _, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		AdminTokenSecret:  []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClock{value: now},
	}); err == nil {
		t.Fatal("shared client/admin token authority accepted")
	}
	auth, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		AdminTokenSecret:  []byte("admin-secret-0123456789abcdef012345"),
		Clock:             fixedAuthClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.SignAdminToken(AdminClaims{
		Subject:  "urn:xb:admin:00000000-0000-4000-8000-000000000001",
		Audience: "admin",
		Expires:  now.Add(time.Minute).Unix(),
		Roles:    []string{"forged-token-role-must-not-authorize"},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.AuthenticateAdmin(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Subject !=
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000001" ||
		principal.Audience != AudienceAdmin ||
		len(principal.Scopes) != 0 {
		t.Fatalf("admin principal = %#v", principal)
	}

	for name, claims := range map[string]AdminClaims{
		"wrong audience": {
			Subject:  "urn:xb:admin:00000000-0000-4000-8000-000000000001",
			Audience: "client",
			Expires:  now.Add(time.Minute).Unix(),
		},
		"expired": {
			Subject:  "urn:xb:admin:00000000-0000-4000-8000-000000000001",
			Audience: "admin",
			Expires:  now.Unix(),
		},
		"noncanonical subject": {
			Subject:  "urn:xb:admin:ROOT",
			Audience: "admin",
			Expires:  now.Add(time.Minute).Unix(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := signClientTestToken(
				t,
				jwt.SigningMethodHS256,
				claims,
				auth.adminSecret,
				nil,
			)
			if _, authenticateErr := auth.AuthenticateAdmin(
				context.Background(),
				candidate,
			); authenticateErr == nil {
				t.Fatal("admin token accepted")
			}
		})
	}

	if _, authenticateErr := auth.AuthenticateClient(
		context.Background(),
		token,
	); authenticateErr == nil {
		t.Fatal("admin token accepted by client authenticator")
	}
	clientSignedAdmin := signClientTestToken(
		t,
		jwt.SigningMethodHS256,
		AdminClaims{
			Subject:  "urn:xb:admin:00000000-0000-4000-8000-000000000001",
			Audience: "admin",
			Expires:  now.Add(time.Minute).Unix(),
		},
		auth.clientSecret,
		nil,
	)
	if _, authenticateErr := auth.AuthenticateAdmin(
		context.Background(),
		clientSignedAdmin,
	); authenticateErr == nil {
		t.Fatal("client-secret-signed admin token accepted")
	}
	clientOnly, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clientOnly.AuthenticateAdmin(
		context.Background(),
		token,
	); err == nil {
		t.Fatal("admin token accepted without configured admin authority")
	}
}

func TestClientTokenRejectsAmbiguousAndCrossTypeJWTs(t *testing.T) {
	now := time.Date(2026, time.July, 25, 15, 0, 0, 0, time.UTC)
	secret := []byte("0123456789abcdef0123456789abcdef")
	auth, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: secret,
		Clock:             fixedAuthClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	valid := ClientClaims{
		Subject: "urn:xb:user:user-7", Audience: "client",
		Expires: now.Add(time.Minute).Unix(),
	}
	duplicatePayload := `{"sub":"urn:xb:user:user-7","aud":"client","aud":"admin","exp":` +
		"1784991660}"
	tests := map[string]string{
		"none algorithm": signClientTestToken(
			t, jwt.SigningMethodNone, valid, jwt.UnsafeAllowNoneSignatureType, nil,
		),
		"wrong algorithm": signClientTestToken(
			t, jwt.SigningMethodHS384, valid, secret, nil,
		),
		"truncated":        "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30",
		"malformed base64": "%%%.e30.signature",
		"duplicate claim":  signRawJWT(t, duplicatePayload, secret),
		"missing audience": signClientTestToken(t, jwt.SigningMethodHS256, ClientClaims{
			Subject: valid.Subject, Expires: valid.Expires,
		}, secret, nil),
		"missing expiry": signClientTestToken(t, jwt.SigningMethodHS256, ClientClaims{
			Subject: valid.Subject, Audience: valid.Audience,
		}, secret, nil),
		"unknown critical header": signClientTestToken(
			t, jwt.SigningMethodHS256, valid, secret,
			map[string]any{"crit": []string{"future"}, "future": true},
		),
		"realtime token replay": signClientTestToken(
			t,
			jwt.SigningMethodHS256,
			jwt.MapClaims{
				"sub": valid.Subject, "exp": valid.Expires,
				"channels": []string{"user:user-7"},
			},
			secret,
			nil,
		),
	}
	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := auth.AuthenticateClient(context.Background(), token); err == nil {
				t.Fatal("token accepted")
			}
		})
	}
}

func signClientTestToken(
	t *testing.T,
	method jwt.SigningMethod,
	claims jwt.Claims,
	key any,
	headers map[string]any,
) string {
	t.Helper()
	token := jwt.NewWithClaims(method, claims)
	token.Header["typ"] = "JWT"
	for name, value := range headers {
		token.Header[name] = value
	}
	encoded, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func signRawJWT(t *testing.T, payload string, secret []byte) string {
	t.Helper()
	encoding := base64.RawURLEncoding
	unsigned := encoding.EncodeToString(
		[]byte(`{"alg":"HS256","typ":"JWT"}`),
	) + "." + encoding.EncodeToString([]byte(payload))
	signature, err := jwt.SigningMethodHS256.Sign(unsigned, secret)
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + encoding.EncodeToString(signature)
}

func TestNewHMACAuthenticatorValidatesBrokerPrincipalConfiguration(t *testing.T) {
	valid := BrokerCredential{
		Prefix:     "xbk_partner",
		SecretHash: HashBrokerSecret("secret"),
		Subject:    "urn:xb:apikey:partner",
		Tenant:     "urn:xb:tenant:partner",
	}
	tests := []struct {
		name        string
		credentials []BrokerCredential
		wantError   bool
	}{
		{
			name:        "valid credential",
			credentials: []BrokerCredential{valid},
		},
		{
			name: "subject has wrong namespace",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:user:partner",
				Tenant:     "urn:xb:tenant:partner",
			}},
			wantError: true,
		},
		{
			name: "subject has empty identifier",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:",
				Tenant:     "urn:xb:tenant:partner",
			}},
			wantError: true,
		},
		{
			name: "tenant has wrong namespace",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:partner",
				Tenant:     "urn:xb:account:partner",
			}},
			wantError: true,
		},
		{
			name: "tenant has empty identifier",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:partner",
				Tenant:     "urn:xb:tenant:",
			}},
			wantError: true,
		},
		{
			name: "subject contains scope separator",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:partner\x1fother",
				Tenant:     "urn:xb:tenant:partner",
			}},
			wantError: true,
		},
		{
			name: "tenant exceeds durable bound",
			credentials: []BrokerCredential{{
				Prefix:     "xbk_partner",
				SecretHash: HashBrokerSecret("secret"),
				Subject:    "urn:xb:apikey:partner",
				Tenant:     "urn:xb:tenant:" + strings.Repeat("x", 512),
			}},
			wantError: true,
		},
		{
			name: "duplicate subject across prefixes and tenants",
			credentials: []BrokerCredential{
				valid,
				{
					Prefix:     "xbk_reseller",
					SecretHash: HashBrokerSecret("other-secret"),
					Subject:    valid.Subject,
					Tenant:     "urn:xb:tenant:reseller",
				},
			},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
				ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
				BrokerCredentials: test.credentials,
			})
			if test.wantError && err == nil {
				t.Fatal("configuration accepted")
			}
			if !test.wantError && err != nil {
				t.Fatalf("valid configuration rejected: %v", err)
			}
		})
	}
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/identity/e2e_broker.rs:263
// test: ip_allowlist_rejects_non_matching_source
func TestBrokerAPIKeyEnforcesScopeExpiryAndIPAllowlist(t *testing.T) {
	now := time.Date(2026, time.July, 25, 15, 0, 0, 0, time.UTC)
	expires := now.Add(time.Minute)
	auth, err := NewHMACAuthenticator(HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		Clock:             fixedAuthClock{value: now},
		BrokerCredentials: []BrokerCredential{{
			Prefix: "xbk_partner", SecretHash: HashBrokerSecret("secret"),
			Subject:     "urn:xb:apikey:partner",
			Tenant:      "urn:xb:tenant:partner",
			Scopes:      []string{"accounts:read"},
			IPAllowlist: []netip.Addr{netip.MustParseAddr("203.0.113.7")},
			ExpiresAt:   &expires,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.AuthenticateBroker(
		context.Background(), "xbk_partner.secret", "203.0.113.7:443",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.HasScope("accounts:read") || principal.HasScope("accounts:write") {
		t.Fatalf("scopes = %v", principal.Scopes)
	}
	for _, attempt := range []struct {
		token string
		ip    string
	}{
		{token: "xbk_partner." + "wrong", ip: "203.0.113.7"},
		{token: "xbk_partner." + "secret", ip: "198.51.100.9"},
		{token: "xbk_partner." + "secret", ip: ""},
	} {
		if _, err := auth.AuthenticateBroker(context.Background(), attempt.token, attempt.ip); err == nil {
			t.Fatalf("accepted token=%q ip=%q", attempt.token, attempt.ip)
		}
	}
	auth.clock = fixedAuthClock{value: expires}
	if _, err := auth.AuthenticateBroker(
		context.Background(), "xbk_partner.secret", "203.0.113.7",
	); err == nil {
		t.Fatal("expired key accepted")
	}
}
