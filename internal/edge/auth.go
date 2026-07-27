package edge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// AuthClock keeps credential expiry checks testable and explicit.
type AuthClock interface {
	Now() time.Time
}

type authWallClock struct{}

func (authWallClock) Now() time.Time { return time.Now() }

// BrokerCredential is one configured M2M API key. SecretHash is SHA-256 of
// only the secret portion after the public xbk_ prefix.
type BrokerCredential struct {
	Prefix      string
	SecretHash  [sha256.Size]byte
	Subject     string
	Tenant      string
	Scopes      []string
	IPAllowlist []netip.Addr
	ExpiresAt   *time.Time
}

// HMACAuthenticatorConfig defines the compact Phase 3 credential verifier.
type HMACAuthenticatorConfig struct {
	ClientTokenSecret []byte
	BrokerCredentials []BrokerCredential
	Clock             AuthClock
}

// HMACAuthenticator verifies client JWTs and broker xbk_ API keys.
type HMACAuthenticator struct {
	clientSecret []byte
	brokers      map[string]BrokerCredential
	clock        AuthClock
}

// NewHMACAuthenticator constructs a fail-closed credential verifier.
func NewHMACAuthenticator(
	config HMACAuthenticatorConfig,
) (*HMACAuthenticator, error) {
	if len(config.ClientTokenSecret) < 32 {
		return nil, errors.New("auth: client token secret must contain at least 32 bytes")
	}
	if config.Clock == nil {
		config.Clock = authWallClock{}
	}
	brokers := make(map[string]BrokerCredential, len(config.BrokerCredentials))
	subjects := make(map[string]struct{}, len(config.BrokerCredentials))
	for _, credential := range config.BrokerCredentials {
		if credential.Prefix == "" ||
			credential.Subject == "" ||
			credential.Tenant == "" {
			return nil, errors.New(
				"auth: broker prefix, subject, and tenant are required",
			)
		}
		if !validBrokerURN(credential.Subject, "urn:xb:apikey:") {
			return nil, errors.New(
				"auth: broker subject must match urn:xb:apikey:<nonempty>",
			)
		}
		if !validBrokerURN(credential.Tenant, "urn:xb:tenant:") {
			return nil, errors.New(
				"auth: broker tenant must match urn:xb:tenant:<nonempty>",
			)
		}
		if _, exists := brokers[credential.Prefix]; exists {
			return nil, fmt.Errorf("auth: duplicate broker prefix %q", credential.Prefix)
		}
		if _, exists := subjects[credential.Subject]; exists {
			return nil, fmt.Errorf("auth: duplicate broker subject %q", credential.Subject)
		}
		credential.Scopes = append([]string(nil), credential.Scopes...)
		credential.IPAllowlist = append([]netip.Addr(nil), credential.IPAllowlist...)
		brokers[credential.Prefix] = credential
		subjects[credential.Subject] = struct{}{}
	}
	return &HMACAuthenticator{
		clientSecret: append([]byte(nil), config.ClientTokenSecret...),
		brokers:      brokers,
		clock:        config.Clock,
	}, nil
}

func validBrokerURN(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) &&
		len(value) > len(prefix) &&
		len(value) <= 512 &&
		!strings.ContainsRune(value, '\x1f')
}

// ClientClaims is the supported client access-token contract.
type ClientClaims struct {
	Subject  string   `json:"sub"`
	Audience string   `json:"aud"`
	Expires  int64    `json:"exp"`
	Accounts []string `json:"accounts,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

func (claims ClientClaims) GetExpirationTime() (*jwt.NumericDate, error) {
	if claims.Expires == 0 {
		return nil, nil
	}
	return jwt.NewNumericDate(time.Unix(claims.Expires, 0)), nil
}

func (ClientClaims) GetIssuedAt() (*jwt.NumericDate, error) { return nil, nil }

func (ClientClaims) GetNotBefore() (*jwt.NumericDate, error) { return nil, nil }

func (ClientClaims) GetIssuer() (string, error) { return "", nil }

func (claims ClientClaims) GetSubject() (string, error) { return claims.Subject, nil }

func (claims ClientClaims) GetAudience() (jwt.ClaimStrings, error) {
	if claims.Audience == "" {
		return nil, nil
	}
	return jwt.ClaimStrings{claims.Audience}, nil
}

// AuthenticateClient verifies signature, audience, expiry, and subject.
func (auth *HMACAuthenticator) AuthenticateClient(
	_ context.Context,
	token string,
) (Principal, error) {
	if auth == nil {
		return Principal{}, ErrUnauthorized
	}
	if err := rejectAmbiguousJWT(token); err != nil {
		return Principal{}, ErrUnauthorized
	}
	var claims ClientClaims
	parsed, err := jwt.ParseWithClaims(
		token,
		&claims,
		func(candidate *jwt.Token) (any, error) {
			if candidate.Method != jwt.SigningMethodHS256 ||
				candidate.Header["typ"] != "JWT" {
				return nil, ErrUnauthorized
			}
			if _, critical := candidate.Header["crit"]; critical {
				return nil, ErrUnauthorized
			}
			return auth.clientSecret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(string(AudienceClient)),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(auth.clock.Now),
	)
	if err != nil || !parsed.Valid {
		return Principal{}, ErrUnauthorized
	}
	if claims.Subject == "" || claims.Audience != string(AudienceClient) ||
		claims.Expires <= auth.clock.Now().Unix() {
		return Principal{}, ErrUnauthorized
	}
	return Principal{
		Subject: claims.Subject, Audience: AudienceClient,
		Scopes:   append([]string(nil), claims.Scopes...),
		Accounts: append([]string(nil), claims.Accounts...),
	}, nil
}

// AuthenticateBroker verifies the public prefix, secret, expiry, and exact IP
// allowlist before returning scopes.
func (auth *HMACAuthenticator) AuthenticateBroker(
	_ context.Context,
	token string,
	sourceIP string,
) (Principal, error) {
	if auth == nil {
		return Principal{}, ErrUnauthorized
	}
	prefix, secret, ok := strings.Cut(token, ".")
	if !ok || !strings.HasPrefix(prefix, "xbk_") || secret == "" {
		return Principal{}, ErrUnauthorized
	}
	credential, exists := auth.brokers[prefix]
	if !exists {
		return Principal{}, ErrUnauthorized
	}
	actualHash := sha256.Sum256([]byte(secret))
	if !hmac.Equal(actualHash[:], credential.SecretHash[:]) {
		return Principal{}, ErrUnauthorized
	}
	if credential.ExpiresAt != nil && !auth.clock.Now().Before(*credential.ExpiresAt) {
		return Principal{}, ErrUnauthorized
	}
	if len(credential.IPAllowlist) > 0 {
		address, err := parseSourceAddress(sourceIP)
		if err != nil {
			return Principal{}, ErrUnauthorized
		}
		allowed := false
		for _, candidate := range credential.IPAllowlist {
			if candidate == address {
				allowed = true
				break
			}
		}
		if !allowed {
			return Principal{}, ErrUnauthorized
		}
	}
	return Principal{
		Subject: credential.Subject, Tenant: credential.Tenant,
		Audience: AudienceBroker,
		Scopes:   append([]string(nil), credential.Scopes...),
	}, nil
}

// SignClientToken exists for trusted identity composition and tests.
func (auth *HMACAuthenticator) SignClientToken(claims ClientClaims) (string, error) {
	if auth == nil || claims.Subject == "" ||
		claims.Audience != string(AudienceClient) || claims.Expires == 0 {
		return "", errors.New("auth: valid client claims are required")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	return token.SignedString(auth.clientSecret)
}

// HashBrokerSecret creates the stored comparison value for a broker secret.
func HashBrokerSecret(secret string) [sha256.Size]byte {
	return sha256.Sum256([]byte(secret))
}

func parseSourceAddress(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Unmap(), nil
	}
	if addressPort, err := netip.ParseAddrPort(value); err == nil {
		return addressPort.Addr().Unmap(), nil
	}
	return netip.Addr{}, errors.New("invalid source address")
}

func rejectAmbiguousJWT(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ErrUnauthorized
	}
	for _, part := range parts[:2] {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil || rejectDuplicateObjectKeys(raw) != nil {
			return ErrUnauthorized
		}
	}
	return nil
}

func rejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return ErrUnauthorized
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		name, tokenErr := decoder.Token()
		if tokenErr != nil {
			return ErrUnauthorized
		}
		key, ok := name.(string)
		if !ok {
			return ErrUnauthorized
		}
		if _, duplicate := seen[key]; duplicate {
			return ErrUnauthorized
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return ErrUnauthorized
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return ErrUnauthorized
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return ErrUnauthorized
	}
	return nil
}
