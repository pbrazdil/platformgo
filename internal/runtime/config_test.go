package runtime

import (
	"strings"
	"testing"
	"time"

	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
)

func TestLoadConfigPreservesFrozenEnvironmentKeys(t *testing.T) {
	values := map[string]string{
		"UZO_DATABASE_URL":                      "postgres://example",
		"UZO_NATS_URL":                          "nats://example",
		"UZO_NATS_STREAM_REPLICAS":              "1",
		"UZO_NATS_STREAM_MAX_MESSAGES":          "2000000",
		"UZO_NATS_STREAM_MAX_BYTES":             "3221225472",
		"UZO_NATS_STREAM_MAX_MESSAGE_BYTES":     "2097152",
		"UZO_NATS_STREAM_MAX_AGE_SECS":          "1209600",
		"UZO_NATS_DUPLICATE_WINDOW_SECS":        "43200",
		"UZO_HTTP_REST_ADDR":                    "127.0.0.1:9000",
		"UZO_HTTP_GRPC_ADDR":                    "127.0.0.1:9001",
		"UZO_HTTP_HEALTH_ADDR":                  "127.0.0.1:9002",
		"UZO_TRUSTED_PROXY_CIDRS":               "10.0.0.0/8,2001:db8:ffff::/48",
		"UZO_AUTH_CLIENT_TOKEN_SECRET":          "0123456789abcdef0123456789abcdef",
		"UZO_AUTH_API_KEY_REPLAY_KEYS":          `[{"id":"v1","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="}]`,
		"UZO_AUTH_API_KEY_REPLAY_ACTIVE_KEY_ID": "v1",
		"UZO_AUTH_MAX_API_KEYS_PER_OWNER":       "17",
		"UZO_API_RATE_LIMIT_MAX_REQUESTS":       "321",
		"UZO_API_RATE_LIMIT_WINDOW_SECS":        "45",
		"UZO_API_IDEMPOTENCY_TTL_SECS":          "7200",
		"UZO_REALTIME_API_URL":                  "http://centrifugo:8000",
		"UZO_REALTIME_TOKEN_SECRET":             "abcdef0123456789abcdef0123456789",
		"UZO_REALTIME_TOKEN_TTL_SECS":           "3600",
		"UZO_ENGINE_SHARD_ID":                   "7",
		"UZO_BROKER_API_KEYS": `[{
		"token":"xbk_partner.secret",
		"subject":"urn:xb:apikey:partner",
		"tenant":"urn:xb:tenant:partner",
		"scopes":["accounts:read"],
		"ipAllowlist":["203.0.113.7"],
		"expiresAt":"2027-01-01T00:00:00Z"
	}]`,
	}
	config, err := loadConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if err := config.ValidateFor("serve"); err != nil {
		t.Fatal(err)
	}
	if config.RESTAddress != "127.0.0.1:9000" ||
		config.GRPCAddress != "127.0.0.1:9001" ||
		config.HealthAddress != "127.0.0.1:9002" ||
		len(config.BrokerCredentials) != 1 ||
		config.BrokerCredentials[0].Prefix != "xbk_partner" ||
		len(config.TrustedProxies) != 2 ||
		config.TrustedProxies[0].String() != "10.0.0.0/8" ||
		config.ShardID != 7 ||
		len(config.APIKeyReplayKeys) != 1 ||
		config.APIKeyReplayKeys[0].ID != "v1" ||
		config.APIKeyReplayActiveID != "v1" ||
		config.LegacyAPIKeyPolicy.MaxActivePerOwner == nil ||
		*config.LegacyAPIKeyPolicy.MaxActivePerOwner != 17 ||
		config.LegacyAPIKeyPolicy.RateLimitMaxRequests == nil ||
		*config.LegacyAPIKeyPolicy.RateLimitMaxRequests != 321 ||
		config.LegacyAPIKeyPolicy.RateLimitWindowSecs == nil ||
		*config.LegacyAPIKeyPolicy.RateLimitWindowSecs != 45 ||
		config.LegacyAPIKeyPolicy.IdempotencyTTLSecs == nil ||
		*config.LegacyAPIKeyPolicy.IdempotencyTTLSecs != 7200 ||
		config.NATSStreamLimits.MaxBytes != 3<<30 ||
		config.NATSStreamLimits.MaxAge.String() != "336h0m0s" {
		t.Fatalf("config = %#v", config)
	}
}

func TestAPIKeyReplayRotationRequiresExplicitActiveKey(t *testing.T) {
	const keys = `[
		{"id":"old","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="},
		{"id":"new","key":"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="}
	]`
	_, err := loadConfig(func(name string) string {
		if name == "UZO_AUTH_API_KEY_REPLAY_KEYS" {
			return keys
		}
		return ""
	})
	if err == nil ||
		!strings.Contains(err.Error(), "UZO_AUTH_API_KEY_REPLAY_ACTIVE_KEY_ID") {
		t.Fatalf("missing active replay key error = %v", err)
	}
	config, err := loadConfig(func(name string) string {
		switch name {
		case "UZO_AUTH_API_KEY_REPLAY_KEYS":
			return keys
		case "UZO_AUTH_API_KEY_REPLAY_ACTIVE_KEY_ID":
			return "old"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.APIKeyReplayActiveID != "old" {
		t.Fatalf("active replay key = %q", config.APIKeyReplayActiveID)
	}
}

func TestLegacyAPIKeyPolicyPreservesSourceDomains(t *testing.T) {
	values := map[string]string{
		"UZO_AUTH_MAX_API_KEYS_PER_OWNER": "-1",
		"UZO_API_RATE_LIMIT_MAX_REQUESTS": "0",
		"UZO_API_RATE_LIMIT_WINDOW_SECS":  "0",
		"UZO_API_IDEMPOTENCY_TTL_SECS":    "18446744073709551615",
	}
	config, err := loadConfig(func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	if *config.LegacyAPIKeyPolicy.MaxActivePerOwner != -1 ||
		*config.LegacyAPIKeyPolicy.RateLimitMaxRequests != 0 ||
		*config.LegacyAPIKeyPolicy.RateLimitWindowSecs != 0 ||
		*config.LegacyAPIKeyPolicy.IdempotencyTTLSecs != ^uint64(0) {
		t.Fatalf("legacy policy = %#v", config.LegacyAPIKeyPolicy)
	}
}

func TestLegacyAPIKeyPolicyRejectsOutOfDomainValues(t *testing.T) {
	for name, value := range map[string]string{
		"UZO_AUTH_MAX_API_KEYS_PER_OWNER": "9223372036854775808",
		"UZO_API_RATE_LIMIT_MAX_REQUESTS": "4294967296",
		"UZO_API_RATE_LIMIT_WINDOW_SECS":  "18446744073709551616",
		"UZO_API_IDEMPOTENCY_TTL_SECS":    "-1",
	} {
		_, err := loadConfig(func(candidate string) string {
			if candidate == name {
				return value
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Fatalf("%s error = %v", name, err)
		}
	}
}

func TestTrustedProxyConfigRequiresExplicitCIDRs(t *testing.T) {
	_, err := loadConfig(func(name string) string {
		if name == "UZO_TRUSTED_PROXY_CIDRS" {
			return "10.0.0.1,not-a-cidr"
		}
		return ""
	})
	if err == nil ||
		!strings.Contains(err.Error(), "UZO_TRUSTED_PROXY_CIDRS") {
		t.Fatalf("trusted proxy error = %v", err)
	}
}

func TestAPIKeyReplayKeysRequireValidAES256Material(t *testing.T) {
	for _, value := range []string{
		`[]`,
		`[{"id":"v1","key":"short"}]`,
		`[{"id":"v1","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="},{"id":"v1","key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="}]`,
		`invalid`,
	} {
		_, err := loadConfig(func(name string) string {
			if name == "UZO_AUTH_API_KEY_REPLAY_KEYS" {
				return value
			}
			return ""
		})
		if err == nil ||
			!strings.Contains(err.Error(), "UZO_AUTH_API_KEY_REPLAY_KEYS") {
			t.Fatalf("replay keys %q error = %v", value, err)
		}
	}
}

func TestConfigErrorsNameKeysWithoutSecretValues(t *testing.T) {
	_, err := loadConfig(func(name string) string {
		if name == "UZO_BROKER_API_KEYS" {
			return "super-secret-invalid-json"
		}
		return ""
	})
	if err == nil || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkerRequiresCompleteExplicitJetStreamPolicy(t *testing.T) {
	config := Config{
		DatabaseURL:   "postgres://example",
		NATSURL:       "nats://example",
		HealthAddress: "127.0.0.1:8081",
	}
	err := config.ValidateFor("worker")
	if err == nil ||
		!strings.Contains(err.Error(), "UZO_NATS_STREAM_MAX_BYTES") ||
		!strings.Contains(err.Error(), "UZO_NATS_STREAM_MAX_AGE_SECS") {
		t.Fatalf("worker policy error = %v", err)
	}
	_, err = loadConfig(func(name string) string {
		if name == "UZO_NATS_STREAM_MAX_BYTES" {
			return "1073741824"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "must configure all keys") {
		t.Fatalf("partial JetStream policy error = %v", err)
	}
}

func TestWorkerHandlersCannotUnionPostgreSQLAuthority(t *testing.T) {
	for _, handlers := range [][]string{
		{"outbox-publisher", "event-consumer"},
		{"outbox-publisher", "realtime-publisher"},
		{"event-consumer", "realtime-publisher"},
	} {
		if _, err := databaseRoleForHandlers(handlers); err == nil {
			t.Fatalf("mixed-authority handlers %v accepted", handlers)
		}
	}
	for _, test := range []struct {
		handlers []string
		want     databaseRuntimeRole
	}{
		{
			handlers: []string{"outbox-publisher"},
			want:     databaseRoleOutbox,
		},
		{
			handlers: []string{"event-consumer", "event-consumer:orders"},
			want:     databaseRoleEngine,
		},
		{
			handlers: []string{"realtime-publisher"},
			want:     databaseRoleRealtime,
		},
	} {
		got, err := databaseRoleForHandlers(test.handlers)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("handlers %v role = %q, want %q", test.handlers, got, test.want)
		}
	}
}

func TestRealtimePublisherDoesNotDependOnNATS(t *testing.T) {
	if workerNeedsNATS([]string{"realtime-publisher"}) {
		t.Fatal("realtime-only publisher unexpectedly requires NATS")
	}
	if !workerNeedsNATS([]string{"outbox-publisher"}) ||
		!workerNeedsNATS([]string{"event-consumer"}) {
		t.Fatal("NATS-backed worker did not require NATS")
	}
	config := Config{
		DatabaseURL:      "postgres://example",
		HealthAddress:    "127.0.0.1:8081",
		CentrifugoAPIURL: "http://centrifugo:8000",
		CentrifugoAPIKey: "api-key",
	}
	if err := validateRealtimeWorkerConfig(config); err != nil {
		t.Fatalf("realtime-only config without NATS: %v", err)
	}
}

func TestServeRequiresDistinctClientAndRealtimeTokenSecrets(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	config := Config{
		DatabaseURL:       "postgres://example",
		NATSURL:           "nats://example",
		NATSStreamLimits:  runtimeTestLimitsForUnit(),
		ClientTokenSecret: secret,
		APIKeyReplayKeys: []APIKeyReplayKey{{
			ID: "v1",
		}},
		CentrifugoAPIURL:      "http://centrifugo:8000",
		CentrifugoTokenSecret: append([]byte(nil), secret...),
	}
	if err := config.ValidateFor("serve"); err == nil ||
		!strings.Contains(err.Error(), "must be distinct") {
		t.Fatalf("serve secret error = %v", err)
	}
}

func runtimeTestLimitsForUnit() platformnats.StreamLimits {
	return platformnats.StreamLimits{
		Replicas: 1, MaxMessages: 1, MaxBytes: 1, MaxMessageBytes: 1,
		MaxAge: time.Second, DuplicateWindow: time.Second,
	}
}
