package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const (
	defaultRESTAddress   = "0.0.0.0:8080"
	defaultGRPCAddress   = "0.0.0.0:8081"
	defaultHealthAddress = "0.0.0.0:8081"
)

// Config is the deployment-role configuration loaded from frozen environment
// keys. Secrets are never included in its formatted errors.
type Config struct {
	DatabaseURL           string
	NATSURL               string
	NATSStreamLimits      platformnats.StreamLimits
	RESTAddress           string
	GRPCAddress           string
	HealthAddress         string
	AllowedOrigin         string
	TrustedProxies        []netip.Prefix
	ClientTokenSecret     []byte
	MaxAPIKeysPerOwner    int
	BrokerCredentials     []edge.BrokerCredential
	CentrifugoAPIURL      string
	CentrifugoAPIKey      string
	CentrifugoTokenSecret []byte
	CentrifugoTokenTTL    time.Duration
	ShardID               engine.ShardID
}

type brokerEnvironment struct {
	Token       string   `json:"token"`
	Subject     string   `json:"subject"`
	Tenant      string   `json:"tenant"`
	Scopes      []string `json:"scopes"`
	IPAllowlist []string `json:"ipAllowlist"`
	ExpiresAt   string   `json:"expiresAt"`
}

// LoadConfig reads the Phase 3 deployment environment.
func LoadConfig() (Config, error) {
	return loadConfig(os.Getenv)
}

func loadConfig(getenv func(string) string) (Config, error) {
	tokenTTL, err := positiveSeconds(getenv, "UZO_REALTIME_TOKEN_TTL_SECS", 12*60*60)
	if err != nil {
		return Config{}, err
	}
	shard, err := unsignedValue(getenv, "UZO_ENGINE_SHARD_ID", 0)
	if err != nil || shard > uint64(^uint32(0)) {
		return Config{}, errors.New("UZO_ENGINE_SHARD_ID must be a uint32")
	}
	brokers, err := parseBrokers(getenv("UZO_BROKER_API_KEYS"))
	if err != nil {
		return Config{}, err
	}
	trustedProxies, err := parseTrustedProxies(getenv("UZO_TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return Config{}, err
	}
	streamLimits, err := loadStreamLimits(getenv)
	if err != nil {
		return Config{}, err
	}
	maxAPIKeys, err := unsignedValue(
		getenv,
		"UZO_AUTH_MAX_API_KEYS_PER_OWNER",
		25,
	)
	if err != nil || maxAPIKeys == 0 || maxAPIKeys > 25 {
		return Config{}, errors.New(
			"UZO_AUTH_MAX_API_KEYS_PER_OWNER must be between 1 and 25",
		)
	}
	return Config{
		DatabaseURL:           getenv("UZO_DATABASE_URL"),
		NATSURL:               getenv("UZO_NATS_URL"),
		NATSStreamLimits:      streamLimits,
		RESTAddress:           valueOrDefault(getenv, "UZO_HTTP_REST_ADDR", defaultRESTAddress),
		GRPCAddress:           valueOrDefault(getenv, "UZO_HTTP_GRPC_ADDR", defaultGRPCAddress),
		HealthAddress:         valueOrDefault(getenv, "UZO_HTTP_HEALTH_ADDR", defaultHealthAddress),
		AllowedOrigin:         valueOrDefault(getenv, "UZO_CORS_ALLOWED_ORIGINS", "*"),
		TrustedProxies:        trustedProxies,
		ClientTokenSecret:     []byte(getenv("UZO_AUTH_CLIENT_TOKEN_SECRET")),
		MaxAPIKeysPerOwner:    int(maxAPIKeys),
		BrokerCredentials:     brokers,
		CentrifugoAPIURL:      getenv("UZO_REALTIME_API_URL"),
		CentrifugoAPIKey:      getenv("UZO_REALTIME_API_KEY"),
		CentrifugoTokenSecret: []byte(getenv("UZO_REALTIME_TOKEN_SECRET")),
		CentrifugoTokenTTL:    tokenTTL,
		ShardID:               engine.ShardID(shard),
	}, nil
}

func parseTrustedProxies(value string) ([]netip.Prefix, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, errors.New(
				"UZO_TRUSTED_PROXY_CIDRS must contain comma-separated CIDRs",
			)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func loadStreamLimits(
	getenv func(string) string,
) (platformnats.StreamLimits, error) {
	names := []string{
		"UZO_NATS_STREAM_REPLICAS",
		"UZO_NATS_STREAM_MAX_MESSAGES",
		"UZO_NATS_STREAM_MAX_BYTES",
		"UZO_NATS_STREAM_MAX_MESSAGE_BYTES",
		"UZO_NATS_STREAM_MAX_AGE_SECS",
		"UZO_NATS_DUPLICATE_WINDOW_SECS",
	}
	configured := 0
	for _, name := range names {
		if strings.TrimSpace(getenv(name)) != "" {
			configured++
		}
	}
	if configured == 0 {
		return platformnats.StreamLimits{}, nil
	}
	if configured != len(names) {
		return platformnats.StreamLimits{}, fmt.Errorf(
			"JetStream policy must configure all keys: %s",
			strings.Join(names, ", "),
		)
	}
	replicas, err := unsignedValue(getenv, "UZO_NATS_STREAM_REPLICAS", 1)
	if err != nil || replicas == 0 || replicas > uint64(^uint(0)>>1) {
		return platformnats.StreamLimits{}, errors.New(
			"UZO_NATS_STREAM_REPLICAS must be a positive integer",
		)
	}
	maxMessages, err := unsignedValue(
		getenv,
		"UZO_NATS_STREAM_MAX_MESSAGES",
		1_000_000,
	)
	if err != nil || maxMessages == 0 || maxMessages > uint64(1<<63-1) {
		return platformnats.StreamLimits{}, errors.New(
			"UZO_NATS_STREAM_MAX_MESSAGES must be a positive int64",
		)
	}
	maxBytes, err := unsignedValue(
		getenv,
		"UZO_NATS_STREAM_MAX_BYTES",
		2<<30,
	)
	if err != nil || maxBytes == 0 || maxBytes > uint64(1<<63-1) {
		return platformnats.StreamLimits{}, errors.New(
			"UZO_NATS_STREAM_MAX_BYTES must be a positive int64",
		)
	}
	maxMessageBytes, err := unsignedValue(
		getenv,
		"UZO_NATS_STREAM_MAX_MESSAGE_BYTES",
		1<<20,
	)
	if err != nil || maxMessageBytes == 0 || maxMessageBytes > uint64(1<<31-1) {
		return platformnats.StreamLimits{}, errors.New(
			"UZO_NATS_STREAM_MAX_MESSAGE_BYTES must be a positive int32",
		)
	}
	maxAge, err := positiveSeconds(
		getenv,
		"UZO_NATS_STREAM_MAX_AGE_SECS",
		30*24*60*60,
	)
	if err != nil {
		return platformnats.StreamLimits{}, err
	}
	duplicateWindow, err := positiveSeconds(
		getenv,
		"UZO_NATS_DUPLICATE_WINDOW_SECS",
		24*60*60,
	)
	if err != nil {
		return platformnats.StreamLimits{}, err
	}
	return platformnats.StreamLimits{
		Replicas:        int(replicas),
		MaxMessages:     int64(maxMessages),
		MaxBytes:        int64(maxBytes),
		MaxMessageBytes: int32(maxMessageBytes),
		MaxAge:          maxAge,
		DuplicateWindow: duplicateWindow,
	}, nil
}

// ValidateFor reports missing values for one lifecycle command.
func (config Config) ValidateFor(command string) error {
	missing := make([]string, 0, 4)
	if config.DatabaseURL == "" {
		missing = append(missing, "UZO_DATABASE_URL")
	}
	switch command {
	case "serve":
		if len(config.ClientTokenSecret) < 32 {
			missing = append(missing, "UZO_AUTH_CLIENT_TOKEN_SECRET")
		}
		if config.CentrifugoAPIURL == "" {
			missing = append(missing, "UZO_REALTIME_API_URL")
		}
		if len(config.CentrifugoTokenSecret) < 32 {
			missing = append(missing, "UZO_REALTIME_TOKEN_SECRET")
		}
		if config.NATSURL == "" {
			missing = append(missing, "UZO_NATS_URL")
		}
		if config.NATSStreamLimits == (platformnats.StreamLimits{}) {
			missing = append(
				missing,
				"UZO_NATS_STREAM_REPLICAS",
				"UZO_NATS_STREAM_MAX_MESSAGES",
				"UZO_NATS_STREAM_MAX_BYTES",
				"UZO_NATS_STREAM_MAX_MESSAGE_BYTES",
				"UZO_NATS_STREAM_MAX_AGE_SECS",
				"UZO_NATS_DUPLICATE_WINDOW_SECS",
			)
		}
	case "worker", "doctor":
		if config.NATSURL == "" {
			missing = append(missing, "UZO_NATS_URL")
		}
		if command == "worker" &&
			config.NATSStreamLimits == (platformnats.StreamLimits{}) {
			missing = append(
				missing,
				"UZO_NATS_STREAM_REPLICAS",
				"UZO_NATS_STREAM_MAX_MESSAGES",
				"UZO_NATS_STREAM_MAX_BYTES",
				"UZO_NATS_STREAM_MAX_MESSAGE_BYTES",
				"UZO_NATS_STREAM_MAX_AGE_SECS",
				"UZO_NATS_DUPLICATE_WINDOW_SECS",
			)
		}
		if command == "worker" && config.HealthAddress == "" {
			missing = append(missing, "UZO_HTTP_HEALTH_ADDR")
		}
		if command == "doctor" && config.CentrifugoAPIURL == "" {
			missing = append(missing, "UZO_REALTIME_API_URL")
		}
	case "migrate":
	default:
		return fmt.Errorf("unknown lifecycle command %q", command)
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing required environment keys: %s", strings.Join(missing, ", "))
	}
	if command == "serve" &&
		bytes.Equal(config.ClientTokenSecret, config.CentrifugoTokenSecret) {
		return errors.New(
			"client and Centrifugo token secrets must be distinct",
		)
	}
	return nil
}

func parseBrokers(raw string) ([]edge.BrokerCredential, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var values []brokerEnvironment
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, errors.New("UZO_BROKER_API_KEYS must be valid JSON")
	}
	credentials := make([]edge.BrokerCredential, 0, len(values))
	for _, value := range values {
		prefix, secret, ok := strings.Cut(value.Token, ".")
		if !ok || !strings.HasPrefix(prefix, "xbk_") || secret == "" ||
			value.Subject == "" || value.Tenant == "" {
			return nil, errors.New("UZO_BROKER_API_KEYS contains an invalid credential")
		}
		credential := edge.BrokerCredential{
			Prefix: prefix, SecretHash: sha256.Sum256([]byte(secret)),
			Subject: value.Subject, Tenant: value.Tenant,
			Scopes: append([]string(nil), value.Scopes...),
		}
		for _, rawAddress := range value.IPAllowlist {
			address, err := netip.ParseAddr(rawAddress)
			if err != nil {
				return nil, errors.New("UZO_BROKER_API_KEYS contains an invalid IP address")
			}
			credential.IPAllowlist = append(credential.IPAllowlist, address.Unmap())
		}
		if value.ExpiresAt != "" {
			expiresAt, err := time.Parse(time.RFC3339, value.ExpiresAt)
			if err != nil {
				return nil, errors.New("UZO_BROKER_API_KEYS contains an invalid expiry")
			}
			credential.ExpiresAt = &expiresAt
		}
		credentials = append(credentials, credential)
	}
	return credentials, nil
}

func valueOrDefault(getenv func(string) string, name, fallback string) string {
	if value := strings.TrimSpace(getenv(name)); value != "" {
		return value
	}
	return fallback
}

func positiveSeconds(getenv func(string) string, name string, fallback uint64) (time.Duration, error) {
	value, err := unsignedValue(getenv, name, fallback)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	const maxDurationSeconds = uint64((1<<63 - 1) / int64(time.Second))
	if value > maxDurationSeconds {
		return 0, fmt.Errorf("%s exceeds the maximum duration", name)
	}
	return time.Duration(value) * time.Second, nil
}

func unsignedValue(getenv func(string) string, name string, fallback uint64) (uint64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned integer", name)
	}
	return value, nil
}
