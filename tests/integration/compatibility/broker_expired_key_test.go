package compatibility_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/upcomers-org/platformgo/internal/edge"
)

type brokerExpiryClock struct {
	unixNano atomic.Int64
}

func newBrokerExpiryClock(now time.Time) *brokerExpiryClock {
	clock := &brokerExpiryClock{}
	clock.Set(now)
	return clock
}

func (clock *brokerExpiryClock) Now() time.Time {
	return time.Unix(0, clock.unixNano.Load()).UTC()
}

func (clock *brokerExpiryClock) Set(now time.Time) {
	clock.unixNano.Store(now.UTC().UnixNano())
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:307
//	test: expired_key_is_rejected
//
// Adaptations:
//   - Source-created broker key setup is represented by the production
//     configured HMAC credential authority used by the Go runtime.
//   - The two-second wall-clock sleep is replaced by explicit clock inputs.
//
// Assertions preserved:
//   - The key is accepted before its one-second expiry.
//   - The same key receives HTTP 401 after two seconds.
//
// Invariant strengthening:
//   - The current Go fail-closed boundary also rejects at the exact expiry
//     instant.
func TestExpiredKeyIsRejected(t *testing.T) {
	startedAt := time.Date(2026, time.July, 27, 20, 0, 0, 0, time.UTC)
	expiresAt := startedAt.Add(time.Second)
	clock := newBrokerExpiryClock(startedAt)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-broker-expiry-client-secret"),
		Clock:             clock,
		BrokerCredentials: []edge.BrokerCredential{{
			Prefix:     "xbk_short_lived",
			SecretHash: edge.HashBrokerSecret("expiry-secret"),
			Subject:    "urn:xb:apikey:short-lived",
			Tenant:     "urn:xb:tenant:broker-expiry",
			Scopes:     []string{"*"},
			ExpiresAt:  &expiresAt,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		RequestID:     func() string { return "broker-expiry-request" },
	}).Handler())
	t.Cleanup(server.Close)
	client := &http.Client{Timeout: 2 * time.Second}
	t.Cleanup(client.CloseIdleConnections)

	if status := brokerExpiryPingStatus(t, client, server.URL); status != http.StatusOK {
		t.Fatalf("before-expiry ping status=%d, want %d", status, http.StatusOK)
	}
	clock.Set(expiresAt)
	if status := brokerExpiryPingStatus(t, client, server.URL); status != http.StatusUnauthorized {
		t.Fatalf("at-expiry ping status=%d, want %d", status, http.StatusUnauthorized)
	}
	clock.Set(startedAt.Add(2 * time.Second))
	if status := brokerExpiryPingStatus(t, client, server.URL); status != http.StatusUnauthorized {
		t.Fatalf("after-expiry ping status=%d, want %d", status, http.StatusUnauthorized)
	}
}

func brokerExpiryPingStatus(
	t *testing.T,
	client *http.Client,
	serverURL string,
) int {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		serverURL+"/broker/v1/ping",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("x-api-key", "xbk_short_lived.expiry-secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response.StatusCode
}
